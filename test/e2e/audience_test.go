package e2e_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// The api of compose.test.yml runs with LLM_PROVIDER=mock, SEGMENT_REFERENCE_TIME=2026-01-15T12:00:00Z
// and the canonical seed (--seed=42 --users=5000), i.e. the dataset of testdata/seed/manifest.json
// (docs/contracts/README.md §7). Every test creates its own campaign; nothing here mutates customers.

const manifestPath = "../../testdata/seed/manifest.json"

const unknownCampaignID = "00000000-0000-4000-8000-000000000000"

var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// REQ-AUD-00, REQ-AUD-03, REQ-AUD-04, REQ-AI-01
// Example brief end to end over HTTP: POST /api/campaigns creates a draft; POST /segment with the
// mock LLM stores the structured rule of the manifest, counts it per channel after consent, and
// moves the campaign to audience_ready; GET returns the stored segment.
func TestE2E_ExampleBriefAudience(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)

	id, created := createCampaign(t, api, m.ExampleBrief.Brief)
	assertDraft(t, created, id, m.ExampleBrief.Brief)

	status, _, body := doJSON(t, http.MethodGet, api+"/api/campaigns/"+id, nil, nil)
	if status != http.StatusOK {
		t.Fatalf("GET campaign status = %d, want 200; body: %s", status, body)
	}
	assertDraft(t, decodeObject(t, body), id, m.ExampleBrief.Brief)

	status, _, body = doJSON(t, http.MethodPost, api+"/api/campaigns/"+id+"/segment", nil, scenario("ok"))
	if status != http.StatusOK {
		t.Fatalf("POST segment status = %d, want 200; body: %s", status, body)
	}
	res := decodeObject(t, body)
	if diff := cmp.Diff([]string{"campaign_id", "segment", "state"}, sortedKeys(res)); diff != "" {
		t.Fatalf("SegmentResult keys mismatch (-want +got):\n%s", diff)
	}
	if res["campaign_id"] != id || res["state"] != "audience_ready" {
		t.Fatalf("SegmentResult campaign_id/state = %v/%v, want %s/audience_ready", res["campaign_id"], res["state"], id)
	}
	seg := asObject(t, res["segment"])
	assertSegment(t, seg, m, segmentWant{rule: m.ExampleBrief.Rule, audience: m.ExampleBrief.Audience, longest: m.ExampleBrief.LongestValues, revision: 1, attempts: 1})

	status, _, body = doJSON(t, http.MethodGet, api+"/api/campaigns/"+id, nil, nil)
	if status != http.StatusOK {
		t.Fatalf("GET campaign status = %d, want 200; body: %s", status, body)
	}
	got := decodeObject(t, body)
	if got["state"] != "audience_ready" {
		t.Fatalf("stored state = %v, want audience_ready", got["state"])
	}
	if diff := cmp.Diff(seg, asObject(t, got["segment"])); diff != "" {
		t.Fatalf("GET campaign segment differs from the POST /segment result (-post +get):\n%s", diff)
	}

	// Regenerating the segment keeps audience_ready and increments the revision.
	status, _, body = doJSON(t, http.MethodPost, api+"/api/campaigns/"+id+"/segment", []byte(`{}`), scenario("ok"))
	if status != http.StatusOK {
		t.Fatalf("second POST segment status = %d, want 200; body: %s", status, body)
	}
	res = decodeObject(t, body)
	if res["state"] != "audience_ready" {
		t.Fatalf("state after regeneration = %v, want audience_ready", res["state"])
	}
	assertSegment(t, asObject(t, res["segment"]), m, segmentWant{rule: m.ExampleBrief.Rule, audience: m.ExampleBrief.Audience, longest: m.ExampleBrief.LongestValues, revision: 2, attempts: 1})
}

// REQ-AI-01, REQ-AUD-01
// One repair attempt rescues invalid model output: then_ok_after_repair => 200 with llm_attempts 2.
func TestE2E_SegmentRepairAttemptSucceeds(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	id, _ := createCampaign(t, api, m.ExampleBrief.Brief)
	status, _, body := doJSON(t, http.MethodPost, api+"/api/campaigns/"+id+"/segment", nil, scenario("then_ok_after_repair"))
	if status != http.StatusOK {
		t.Fatalf("POST segment status = %d, want 200; body: %s", status, body)
	}
	res := decodeObject(t, body)
	assertSegment(t, asObject(t, res["segment"]), m, segmentWant{rule: m.ExampleBrief.Rule, audience: m.ExampleBrief.Audience, longest: m.ExampleBrief.LongestValues, revision: 1, attempts: 2})
}

// REQ-AUD-01, REQ-AUD-02, REQ-AI-01
// Output still invalid after the repair attempt => 502 llm_invalid_output with the last attempt's
// field errors; an unreachable LLM => 502 llm_unavailable. The campaign is unchanged and the
// response carries neither the raw model output nor database text (openapi BadGateway).
func TestE2E_SegmentInvalidOutputIs502(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	tests := []struct {
		scenario  string
		code      string
		field     *fieldWant
		forbidden []string
	}{
		{scenario: "invalid_json", code: "llm_invalid_output", field: &fieldWant{"", "invalid_json"}},
		{scenario: "schema_violation", code: "llm_invalid_output", field: &fieldWant{"sql", "schema_violation"}, forbidden: []string{"SELECT id FROM customers"}},
		{scenario: "injection_attempt", code: "llm_invalid_output", field: &fieldWant{"rule.raw_sql", "schema_violation"}, forbidden: []string{"DROP TABLE"}},
		{scenario: "truncated_stream", code: "llm_invalid_output", field: &fieldWant{"", "truncated_output"}},
		{scenario: "always_invalid", code: "llm_invalid_output", field: &fieldWant{"sql", "schema_violation"}, forbidden: []string{"SELECT id FROM customers"}},
		{scenario: "llm_unavailable", code: "llm_unavailable"},
	}
	for _, tc := range tests {
		t.Run(tc.scenario, func(t *testing.T) {
			t.Parallel()
			id, _ := createCampaign(t, api, m.ExampleBrief.Brief)
			status, hdr, body := doJSON(t, http.MethodPost, api+"/api/campaigns/"+id+"/segment", nil, scenario(tc.scenario))
			e := assertError(t, status, hdr, body, http.StatusBadGateway, tc.code, tc.field != nil)
			if tc.field != nil && !e.has(*tc.field) {
				t.Fatalf("error.fields = %+v, want %+v", e.Fields, *tc.field)
			}
			for _, s := range append(tc.forbidden, "SQLSTATE", "pq:", "pgx", "ERROR:") {
				if strings.Contains(string(body), s) {
					t.Fatalf("502 body contains %q: %s", s, body)
				}
			}
			status, _, body = doJSON(t, http.MethodGet, api+"/api/campaigns/"+id, nil, nil)
			if status != http.StatusOK {
				t.Fatalf("GET campaign status = %d; body: %s", status, body)
			}
			assertDraft(t, decodeObject(t, body), id, m.ExampleBrief.Brief)
		})
	}
}

// REQ-AUD-01, REQ-AI-01
// A failed regeneration leaves the previously stored segment and state untouched.
func TestE2E_FailedRegenerationKeepsPreviousSegment(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	id, _ := createCampaign(t, api, m.ExampleBrief.Brief)
	status, _, body := doJSON(t, http.MethodPost, api+"/api/campaigns/"+id+"/segment", nil, scenario("ok"))
	if status != http.StatusOK {
		t.Fatalf("POST segment status = %d; body: %s", status, body)
	}
	before := asObject(t, decodeObject(t, body)["segment"])

	status, hdr, body := doJSON(t, http.MethodPost, api+"/api/campaigns/"+id+"/segment", nil, scenario("always_invalid"))
	assertError(t, status, hdr, body, http.StatusBadGateway, "llm_invalid_output", true)

	status, _, body = doJSON(t, http.MethodGet, api+"/api/campaigns/"+id, nil, nil)
	if status != http.StatusOK {
		t.Fatalf("GET campaign status = %d; body: %s", status, body)
	}
	got := decodeObject(t, body)
	if got["state"] != "audience_ready" {
		t.Fatalf("state after failed regeneration = %v, want audience_ready", got["state"])
	}
	if diff := cmp.Diff(before, asObject(t, got["segment"])); diff != "" {
		t.Fatalf("segment changed after a failed regeneration (-before +after):\n%s", diff)
	}
}

// REQ-AUD-02
// A model rule whose value is SQL text is stored as a plain value: 200, nobody matches, the
// longest values fall back to 18/0/0 (empty matched set), and the tables are intact afterwards.
func TestE2E_SQLInValueIsOnlyAValue(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	id, _ := createCampaign(t, api, m.ExampleBrief.Brief)
	status, _, body := doJSON(t, http.MethodPost, api+"/api/campaigns/"+id+"/segment", nil, scenario("sql_in_value"))
	if status != http.StatusOK {
		t.Fatalf("POST segment status = %d, want 200; body: %s", status, body)
	}
	seg := asObject(t, decodeObject(t, body)["segment"])
	assertSegment(t, seg, m, segmentWant{
		rule:     json.RawMessage(`{"all":[{"field":"city","op":"eq","value":"'; DROP TABLE customers; --"}]}`),
		audience: audienceDoc{},
		longest:  longestDoc{FirstName: 18},
		revision: 1,
		attempts: 1,
	})
	assertExampleCount(t, api, m)
}

// REQ-AUD-01, REQ-AUD-02, REQ-AUD-03
// POST /api/segments/count runs the same Parse/Validate/Compile path without the LLM: the manifest
// rule gives the manifest audience; segment-dsl.md §7.3 inputs are refused with their field errors.
func TestE2E_CountSegment(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)

	t.Run("example rule equals the manifest", func(t *testing.T) {
		t.Parallel()
		assertExampleCount(t, api, m)
	})

	t.Run("SQL text value counts zero", func(t *testing.T) {
		t.Parallel()
		status, _, body := doJSON(t, http.MethodPost, api+"/api/segments/count",
			[]byte(`{"rule":{"all":[{"field":"city","op":"eq","value":"'; DROP TABLE customers; --"}]}}`), nil)
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200; body: %s", status, body)
		}
		got := decodeObject(t, body)
		if diff := cmp.Diff(map[string]any{"matched": 0.0, "sms": 0.0, "email": 0.0}, audienceCounts(t, got["audience"])); diff != "" {
			t.Fatalf("audience mismatch (-want +got):\n%s", diff)
		}
		assertExampleCount(t, api, m)
	})

	rejected := []struct {
		name string
		body string
		want fieldWant
	}{
		{name: "sql key next to rule", body: `{"rule":` + string(m.ExampleBrief.Rule) + `,"sql":"SELECT * FROM customers"}`, want: fieldWant{"sql", "schema_violation"}},
		{name: "where key in rule", body: `{"rule":{"where":"1=1"}}`, want: fieldWant{"rule.where", "schema_violation"}},
		{name: "email field", body: `{"rule":{"all":[{"field":"email","op":"eq","value":"a@example.com"}]}}`, want: fieldWant{"rule.all[0].field", "unknown_field"}},
		{name: "consent field", body: `{"rule":{"all":[{"field":"sms_opt_in","op":"eq","value":true}]}}`, want: fieldWant{"rule.all[0].field", "unknown_field"}},
		{name: "like operator", body: `{"rule":{"all":[{"field":"city","op":"like","value":"%stan%"}]}}`, want: fieldWant{"rule.all[0].op", "unknown_operator"}},
		{name: "gt on city", body: `{"rule":{"all":[{"field":"city","op":"gt","value":"A"}]}}`, want: fieldWant{"rule.all[0].op", "operator_not_allowed"}},
		{name: "SQL in an integer value", body: `{"rule":{"all":[{"field":"orders_30d","op":"eq","value":"0 OR 1=1"}]}}`, want: fieldWant{"rule.all[0].value", "type_mismatch"}},
		{name: "language outside the enum", body: `{"rule":{"all":[{"field":"language","op":"eq","value":"de"}]}}`, want: fieldWant{"rule.all[0].value", "invalid_value"}},
		{name: "depth 4", body: `{"rule":{"all":[{"any":[{"not":{"all":[{"field":"city","op":"eq","value":"İzmir"}]}}]}]}}`, want: fieldWant{"rule.all[0].any[0].not", "max_depth_exceeded"}},
		{name: "eleven conditions", body: `{"rule":{"all":[` + strings.TrimSuffix(strings.Repeat(`{"field":"city","op":"eq","value":"İzmir"},`, 11), ",") + `]}}`, want: fieldWant{"rule", "max_conditions_exceeded"}},
		{name: "bare condition root", body: `{"rule":{"field":"city","op":"eq","value":"İstanbul"}}`, want: fieldWant{"rule", "schema_violation"}},
		{name: "mixed condition and group", body: `{"rule":{"all":[{"field":"city","op":"eq","value":"İzmir","all":[]}]}}`, want: fieldWant{"rule.all[0]", "schema_violation"}},
	}
	for _, tc := range rejected {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			t.Parallel()
			status, hdr, body := doJSON(t, http.MethodPost, api+"/api/segments/count", []byte(tc.body), nil)
			e := assertError(t, status, hdr, body, http.StatusUnprocessableEntity, "validation_failed", true)
			if !e.has(tc.want) {
				t.Fatalf("error.fields = %+v, want %+v", e.Fields, tc.want)
			}
			for _, s := range []string{"SQLSTATE", "ERROR:", "syntax error"} {
				if strings.Contains(string(body), s) {
					t.Fatalf("422 body contains database text %q: %s", s, body)
				}
			}
		})
	}
}

// REQ-AUD-05
// Brief limits over HTTP: empty or whitespace => required, more than 2000 chars => too_long,
// 2000 accepted.
func TestE2E_BriefLimits(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	str := func(s string) []byte {
		b, err := json.Marshal(map[string]string{"brief": s})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return b
	}
	tests := []struct {
		name      string
		body      []byte
		status    int
		code      string
		field     *fieldWant
		wantBrief string
	}{
		{name: "2000 Turkish letters are accepted", body: str(strings.Repeat("ş", 2000)), status: http.StatusCreated, wantBrief: strings.Repeat("ş", 2000)},
		{name: "decomposed letters count after NFC", body: str(strings.Repeat("ş", 2000)), status: http.StatusCreated, wantBrief: strings.Repeat("ş", 2000)},
		{name: "1 char is accepted", body: str("x"), status: http.StatusCreated, wantBrief: "x"},
		{name: "empty brief is required", body: str(""), status: http.StatusUnprocessableEntity, code: "validation_failed", field: &fieldWant{"brief", "required"}},
		{name: "whitespace-only brief is required", body: str(" \n\t "), status: http.StatusUnprocessableEntity, code: "validation_failed", field: &fieldWant{"brief", "required"}},
		{name: "2001 chars are too long", body: str(strings.Repeat("a", 2001)), status: http.StatusUnprocessableEntity, code: "validation_failed", field: &fieldWant{"brief", "too_long"}},
		{name: "2001 Turkish letters are too long", body: str(strings.Repeat("ğ", 2001)), status: http.StatusUnprocessableEntity, code: "validation_failed", field: &fieldWant{"brief", "too_long"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			status, hdr, body := doJSON(t, http.MethodPost, api+"/api/campaigns", tc.body, nil)
			if tc.status == http.StatusCreated {
				if status != http.StatusCreated {
					t.Fatalf("status = %d, want 201; body: %s", status, body)
				}
				c := decodeObject(t, body)
				id, _ := c["id"].(string)
				assertDraft(t, c, id, tc.wantBrief)
				if loc := hdr.Get("Location"); loc != "/api/campaigns/"+id {
					t.Fatalf("Location = %q, want /api/campaigns/%s", loc, id)
				}
				return
			}
			e := assertError(t, status, hdr, body, tc.status, tc.code, tc.field != nil)
			if tc.field != nil && !e.hasLoose(*tc.field) {
				t.Fatalf("error.fields = %+v, want %+v", e.Fields, *tc.field)
			}
			if hdr.Get("Location") != "" {
				t.Fatalf("a rejected brief got a Location header %q", hdr.Get("Location"))
			}
		})
	}
}

// REQ-AUD-05
// Request bodies are limited to 65536 bytes: 65537 => 413 payload_too_large; exactly 65536 passes
// the size check and is then refused as a too long brief.
func TestE2E_BodySizeLimit(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	mk := func(n int) []byte {
		b := []byte(`{"brief":"` + strings.Repeat("a", n-12) + `"}`)
		if len(b) != n {
			t.Fatalf("test setup: body is %d bytes, want %d", len(b), n)
		}
		return b
	}
	status, hdr, body := doJSON(t, http.MethodPost, api+"/api/campaigns", mk(65537), nil)
	assertError(t, status, hdr, body, http.StatusRequestEntityTooLarge, "payload_too_large", false)

	status, hdr, body = doJSON(t, http.MethodPost, api+"/api/campaigns", mk(65536), nil)
	e := assertError(t, status, hdr, body, http.StatusUnprocessableEntity, "validation_failed", true)
	if !e.has(fieldWant{"brief", "too_long"}) {
		t.Fatalf("error.fields = %+v, want {brief too_long}", e.Fields)
	}

	status, hdr, body = doJSON(t, http.MethodPost, api+"/api/segments/count", []byte(`{"rule":{"all":[{"field":"city","op":"eq","value":"`+strings.Repeat("a", 65537)+`"}]}}`), nil)
	assertError(t, status, hdr, body, http.StatusRequestEntityTooLarge, "payload_too_large", false)
}

// REQ-AUD-00
// 404 for unknown campaigns (HTTP status table: "Bilinmeyen kampanya").
func TestE2E_NotFoundAndMethodNotAllowed(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	notFound := []struct {
		method, path, code string
	}{
		{method: http.MethodGet, path: "/api/campaigns/" + unknownCampaignID, code: "campaign_not_found"},
		{method: http.MethodPost, path: "/api/campaigns/" + unknownCampaignID + "/segment", code: "campaign_not_found"},
	}
	for _, tc := range notFound {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			t.Parallel()
			status, hdr, body := doJSON(t, tc.method, api+tc.path, nil, scenario("ok"))
			assertError(t, status, hdr, body, http.StatusNotFound, tc.code, false)
		})
	}
}

// ---- manifest ----

type audienceDoc struct {
	Matched int `json:"matched"`
	SMS     int `json:"sms"`
	Email   int `json:"email"`
}

type longestDoc struct {
	FirstName int `json:"first_name"`
	LastName  int `json:"last_name"`
	City      int `json:"city"`
}

type manifestDoc struct {
	Params struct {
		RefTime string `json:"ref_time"`
	} `json:"params"`
	ExampleBrief struct {
		Brief         string          `json:"brief"`
		Rule          json.RawMessage `json:"rule"`
		Audience      audienceDoc     `json:"audience"`
		LongestValues longestDoc      `json:"longest_values"`
	} `json:"example_brief"`
}

func readManifest(t *testing.T) manifestDoc {
	t.Helper()
	b, err := os.ReadFile(filepath.FromSlash(manifestPath))
	if err != nil {
		t.Fatalf("read %s: %v", manifestPath, err)
	}
	var m manifestDoc
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	return m
}

// ---- HTTP helpers ----

func scenario(name string) map[string]string { return map[string]string{"X-Mock-LLM-Scenario": name} }

// doJSON sends a request; a non-nil body defaults to Content-Type application/json.
func doJSON(t *testing.T, method, url string, body []byte, headers map[string]string) (int, http.Header, []byte) {
	t.Helper()
	var req *http.Request
	var err error
	if body != nil {
		req, err = http.NewRequestWithContext(t.Context(), method, url, bytes.NewReader(body))
	} else {
		req, err = http.NewRequestWithContext(t.Context(), method, url, nil)
	}
	if err != nil {
		t.Fatalf("NewRequest(%s %s): %v", method, url, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read body of %s %s: %v", method, url, err)
	}
	return resp.StatusCode, resp.Header, buf.Bytes()
}

func createCampaign(t *testing.T, api, brief string) (string, map[string]any) {
	t.Helper()
	b, err := json.Marshal(map[string]string{"brief": brief})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	status, hdr, body := doJSON(t, http.MethodPost, api+"/api/campaigns", b, nil)
	if status != http.StatusCreated {
		t.Fatalf("POST /api/campaigns status = %d, want 201; body: %s", status, body)
	}
	c := decodeObject(t, body)
	id, _ := c["id"].(string)
	if !uuidRE.MatchString(id) {
		t.Fatalf("campaign id %q is not a lower-case UUID", id)
	}
	if loc := hdr.Get("Location"); loc != "/api/campaigns/"+id {
		t.Fatalf("Location = %q, want /api/campaigns/%s", loc, id)
	}
	return id, c
}

func decodeObject(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("body is not a JSON object (%v): %s", err, body)
	}
	return m
}

func asObject(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("value %v (%T) is not an object", v, v)
	}
	return m
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// campaignKeys: the Campaign document; scheduled_at is always present (REQ-SCH-01 C4).
var campaignKeys = []string{"approval", "brief", "completed_at", "created_at", "id", "latest_content_version", "scheduled_at", "segment", "sending_started_at", "state", "updated_at"}

// assertDraft checks a Campaign in state draft with no segment, content or approval.
func assertDraft(t *testing.T, c map[string]any, id, brief string) {
	t.Helper()
	if diff := cmp.Diff(campaignKeys, sortedKeys(c)); diff != "" {
		t.Fatalf("Campaign keys mismatch (-want +got):\n%s", diff)
	}
	if c["id"] != id || c["brief"] != brief || c["state"] != "draft" {
		t.Fatalf("campaign id/brief/state = %v/%q/%v, want %s/%q/draft", c["id"], c["brief"], c["state"], id, brief)
	}
	for _, k := range []string{"segment", "latest_content_version", "approval", "sending_started_at", "completed_at", "scheduled_at"} {
		if c[k] != nil {
			t.Fatalf("draft campaign %s = %v, want null", k, c[k])
		}
	}
	for _, k := range []string{"created_at", "updated_at"} {
		s, _ := c[k].(string)
		if _, err := time.Parse(time.RFC3339Nano, s); err != nil || !strings.HasSuffix(s, "Z") {
			t.Fatalf("%s = %q, want an RFC 3339 UTC timestamp", k, s)
		}
	}
}

type segmentWant struct {
	rule     json.RawMessage
	audience audienceDoc
	longest  longestDoc
	revision int
	attempts int
}

var segmentKeys = []string{"audience", "generated_at", "llm_attempts", "longest_values", "reference_time", "revision", "rule"}

func assertSegment(t *testing.T, seg map[string]any, m manifestDoc, want segmentWant) {
	t.Helper()
	if diff := cmp.Diff(segmentKeys, sortedKeys(seg)); diff != "" {
		t.Fatalf("Segment keys mismatch (-want +got):\n%s", diff)
	}
	var wantRule any
	if err := json.Unmarshal(want.rule, &wantRule); err != nil {
		t.Fatalf("bad want rule: %v", err)
	}
	if diff := cmp.Diff(wantRule, seg["rule"]); diff != "" {
		t.Fatalf("segment.rule mismatch (-want +got):\n%s", diff)
	}
	wantAudience := map[string]any{"matched": float64(want.audience.Matched), "sms": float64(want.audience.SMS), "email": float64(want.audience.Email)}
	if diff := cmp.Diff(wantAudience, audienceCounts(t, seg["audience"])); diff != "" {
		t.Fatalf("segment.audience mismatch (-want +got):\n%s", diff)
	}
	wantLongest := map[string]any{"first_name": float64(want.longest.FirstName), "last_name": float64(want.longest.LastName), "city": float64(want.longest.City)}
	if diff := cmp.Diff(wantLongest, seg["longest_values"]); diff != "" {
		t.Fatalf("segment.longest_values mismatch (-want +got):\n%s", diff)
	}
	assertTimeEquals(t, "segment.reference_time", seg["reference_time"], m.Params.RefTime)
	if seg["revision"] != float64(want.revision) {
		t.Fatalf("segment.revision = %v, want %d", seg["revision"], want.revision)
	}
	if seg["llm_attempts"] != float64(want.attempts) {
		t.Fatalf("segment.llm_attempts = %v, want %d", seg["llm_attempts"], want.attempts)
	}
	if s, _ := seg["generated_at"].(string); s == "" {
		t.Fatalf("segment.generated_at is empty")
	}
}

func assertTimeEquals(t *testing.T, name string, got any, want string) {
	t.Helper()
	s, _ := got.(string)
	g, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("%s = %v, not RFC 3339: %v", name, got, err)
	}
	w, err := time.Parse(time.RFC3339, want)
	if err != nil {
		t.Fatalf("bad want time %q: %v", want, err)
	}
	if !g.Equal(w) {
		t.Fatalf("%s = %s, want %s", name, s, want)
	}
}

// assertExampleCount checks POST /api/segments/count for the manifest rule (also proves the
// customers table is intact).
func assertExampleCount(t *testing.T, api string, m manifestDoc) {
	t.Helper()
	status, _, body := doJSON(t, http.MethodPost, api+"/api/segments/count", []byte(`{"rule":`+string(m.ExampleBrief.Rule)+`}`), nil)
	if status != http.StatusOK {
		t.Fatalf("POST /api/segments/count status = %d, want 200; body: %s", status, body)
	}
	got := decodeObject(t, body)
	if diff := cmp.Diff([]string{"audience", "reference_time"}, sortedKeys(got)); diff != "" {
		t.Fatalf("SegmentCount keys mismatch (-want +got):\n%s", diff)
	}
	a := m.ExampleBrief.Audience
	want := map[string]any{"matched": float64(a.Matched), "sms": float64(a.SMS), "email": float64(a.Email)}
	if diff := cmp.Diff(want, audienceCounts(t, got["audience"])); diff != "" {
		t.Fatalf("count audience mismatch with manifest (-want +got):\n%s", diff)
	}
	assertTimeEquals(t, "reference_time", got["reference_time"], m.Params.RefTime)
}

// ---- error body ----

type fieldWant struct {
	Path string
	Code string
}

type errorFields []struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Limit   *int   `json:"limit"`
	Actual  *int   `json:"actual"`
}

type apiError struct {
	Code      string      `json:"code"`
	Message   string      `json:"message"`
	Fields    errorFields `json:"fields"`
	RequestID string      `json:"request_id"`
}

func (e apiError) has(w fieldWant) bool {
	for _, f := range e.Fields {
		if f.Path == w.Path && f.Code == w.Code {
			return true
		}
	}
	return false
}

// hasLoose matches the code, and the path only when w.Path is set.
func (e apiError) hasLoose(w fieldWant) bool {
	for _, f := range e.Fields {
		if f.Code == w.Code && (w.Path == "" || f.Path == w.Path) {
			return true
		}
	}
	return false
}

// assertError checks status and the exact README §5 error body shape.
func assertError(t *testing.T, status int, hdr http.Header, body []byte, wantStatus int, wantCode string, withFields bool) apiError {
	t.Helper()
	if status != wantStatus {
		t.Fatalf("status = %d, want %d; body: %s", status, wantStatus, body)
	}
	if ct := hdr.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var raw map[string]map[string]any
	if err := json.Unmarshal(body, &raw); err != nil || len(raw) != 1 || raw["error"] == nil {
		t.Fatalf("body is not {\"error\":{...}} (%v): %s", err, body)
	}
	_, hasFields := raw["error"]["fields"]
	if hasFields != withFields {
		t.Fatalf("error.fields present = %v, want %v; body: %s", hasFields, withFields, body)
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var env struct {
		Error apiError `json:"error"`
	}
	if err := dec.Decode(&env); err != nil {
		t.Fatalf("error body has unexpected keys (%v): %s", err, body)
	}
	e := env.Error
	if e.Code != wantCode {
		t.Fatalf("error.code = %q, want %q; body: %s", e.Code, wantCode, body)
	}
	if e.Message == "" || e.RequestID == "" {
		t.Fatalf("error.message / request_id empty: %s", body)
	}
	if e.RequestID != hdr.Get("X-Request-ID") {
		t.Fatalf("error.request_id = %q, X-Request-ID = %q; want equal", e.RequestID, hdr.Get("X-Request-ID"))
	}
	if withFields && len(e.Fields) == 0 {
		t.Fatalf("error.fields is empty: %s", body)
	}
	for _, f := range e.Fields {
		if f.Message == "" {
			t.Fatalf("field error %+v has no message", f)
		}
	}
	return e
}
