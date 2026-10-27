package e2e_test

// P4 (content + approval) over HTTP + SSE against the compose.test.yml stack: LLM_PROVIDER=mock
// (scenarios chosen with X-Mock-LLM-Scenario, llm-tools.md §3.1), canonical seed of
// testdata/seed/manifest.json, SEGMENT_REFERENCE_TIME=2026-01-15T12:00:00Z. Every call goes through
// the strict OpenAPI client, so every JSON response is validated against openapi.yaml; SSE event
// payloads (not read by the transport) are validated here against the x-sse-events components.
// Every test creates its own campaigns; customers are never mutated (approveContent check 6 needs a
// customer mutation and is therefore NOT tested here — openapi approveContent "FORBIDDEN in the
// shared seeded E2E database").

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"
)

// Content fixtures of llm-tools.md §3.3.
const (
	e2eOKSMS      = "Merhaba {{first_name}}, sepetindeki ürünler seni bekliyor! Bugüne özel %30 indirimle alışverişini tamamla: example.com/sepet"
	e2eOKSubject  = "Sepetin seni bekliyor: %30 indirim fırsatı"
	e2eOKBody     = "Merhaba {{first_name}},\n\nSepetine eklediğin ürünler hâlâ seni bekliyor. {{city}} için bugüne özel %30 indirim tanımladık.\n\nAlışverişini tamamlamak için example.com/sepet adresini ziyaret et.\n\nSevgiler,\nSmart Campaign Composer"
	e2eHTMLSMS    = "Merhaba {{first_name}}, <b>%30</b> indirim seni bekliyor!"
	e2eHTMLSubj   = "<i>Sepetin</i> seni bekliyor"
	e2eHTMLBody   = "Merhaba {{first_name}},\n\n<script>alert('xss')</script>\n\nİndirim kodun: <a href=\"javascript:alert(1)\">tıkla</a>"
	e2eFallback   = "Değerli müşterimiz"
	unknownVerID  = "00000000-0000-4000-8000-0000000000aa"
	e2eSMSVarFree = 110 // CNT_OK sms template without {{first_name}} (llm-tools.md §3.3)
)

var e2eSMS161 = strings.Repeat("İndirim ", 20) + "!"

type contentDoc struct {
	SMS struct {
		Body string `json:"body"`
	} `json:"sms"`
	Email struct {
		Subject string `json:"subject"`
		Body    string `json:"body"`
	} `json:"email"`
}

func mkContent(sms, subject, body string) contentDoc {
	var c contentDoc
	c.SMS.Body, c.Email.Subject, c.Email.Body = sms, subject, body
	return c
}

func okDoc() contentDoc   { return mkContent(e2eOKSMS, e2eOKSubject, e2eOKBody) }
func htmlDoc() contentDoc { return mkContent(e2eHTMLSMS, e2eHTMLSubj, e2eHTMLBody) }

func editDoc(tag string) contentDoc {
	return mkContent("Merhaba {{first_name}}, "+tag+" sepetin seni bekliyor.", "Sepetin bekliyor "+tag, "Merhaba {{first_name}},\n\n"+tag+" indirim.")
}

func (c contentDoc) json(t *testing.T) []byte {
	t.Helper()
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal content: %v", err)
	}
	return b
}

type worstCaseDoc struct {
	SMSRunes     int `json:"sms_runes"`
	SubjectRunes int `json:"subject_runes"`
}

type deltaData struct {
	Attempt int    `json:"attempt"`
	Channel string `json:"channel"`
	Field   string `json:"field"`
	Text    string `json:"text"`
}

type invalidData struct {
	Attempt          int         `json:"attempt"`
	Final            bool        `json:"final"`
	Errors           errorFields `json:"errors"`
	ContentVersionID *string     `json:"content_version_id"`
}

func (d invalidData) has(path, code string) bool {
	for _, f := range d.Errors {
		if f.Path == path && f.Code == code {
			return true
		}
	}
	return false
}

type validatedData struct {
	Attempt          int          `json:"attempt"`
	ContentVersionID string       `json:"content_version_id"`
	VersionNumber    int          `json:"version_number"`
	Content          contentDoc   `json:"content"`
	WorstCase        worstCaseDoc `json:"worst_case"`
}

type versionDoc struct {
	ID               string        `json:"id"`
	CampaignID       string        `json:"campaign_id"`
	VersionNumber    int           `json:"version_number"`
	SegmentRevision  int           `json:"segment_revision"`
	Status           string        `json:"status"`
	Source           string        `json:"source"`
	Content          contentDoc    `json:"content"`
	WorstCase        *worstCaseDoc `json:"worst_case"`
	ValidationErrors errorFields   `json:"validation_errors"`
	LLMAttempts      *int          `json:"llm_attempts"`
	CreatedAt        string        `json:"created_at"`
}

func (v versionDoc) hasError(path, code string) bool {
	for _, f := range v.ValidationErrors {
		if f.Path == path && f.Code == code {
			return true
		}
	}
	return false
}

type campaignDoc struct {
	ID      string `json:"id"`
	State   string `json:"state"`
	Segment *struct {
		Audience      audienceDoc `json:"audience"`
		LongestValues longestDoc  `json:"longest_values"`
		Revision      int         `json:"revision"`
	} `json:"segment"`
	LatestContentVersion *versionDoc `json:"latest_content_version"`
	Approval             *struct {
		ID               string `json:"id"`
		ContentVersionID string `json:"content_version_id"`
		ApprovedAt       string `json:"approved_at"`
	} `json:"approval"`
}

// ---- stream ----

type sseEvent struct {
	Name string
	Data string
}

type genResult struct {
	events    []sseEvent
	deltas    []deltaData
	repair    *invalidData // the non-final invalid (end of attempt 1)
	validated *validatedData
	final     *invalidData
}

// text concatenates the delta texts of one attempt per (channel, field).
func (g genResult) text(attempt int) contentDoc {
	var sms, subj, body strings.Builder
	for _, d := range g.deltas {
		if d.Attempt != attempt {
			continue
		}
		switch d.Channel + "." + d.Field {
		case "sms.body":
			sms.WriteString(d.Text)
		case "email.subject":
			subj.WriteString(d.Text)
		case "email.body":
			body.WriteString(d.Text)
		}
	}
	return mkContent(sms.String(), subj.String(), body.String())
}

var sseSchemas = map[string]string{"delta": "ContentDeltaEvent", "invalid": "ContentInvalidEvent", "validated": "ContentValidatedEvent"}

func postContent(t *testing.T, ctx context.Context, api, id string, body []byte, headers map[string]string) *http.Response {
	t.Helper()
	var r *bytes.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	var req *http.Request
	var err error
	if r != nil {
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, api+"/api/campaigns/"+id+"/content", r)
	} else {
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, api+"/api/campaigns/"+id+"/content", nil)
	}
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST content: %v", err)
	}
	return resp
}

func newSSEScanner(resp *http.Response) *bufio.Scanner {
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	return sc
}

// nextEvent reads the next event (comments skipped); ok=false at the end of the body.
func nextEvent(t *testing.T, sc *bufio.Scanner) (sseEvent, bool) {
	t.Helper()
	var ev sseEvent
	seen := false
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if seen {
				return ev, true
			}
		case strings.HasPrefix(line, ":"):
		case strings.HasPrefix(line, "event: "):
			ev.Name, seen = strings.TrimPrefix(line, "event: "), true
		case strings.HasPrefix(line, "data: "):
			if ev.Data != "" {
				t.Fatalf("event %q has more than one data line", ev.Name)
			}
			ev.Data, seen = strings.TrimPrefix(line, "data: "), true
		case strings.HasPrefix(line, "id:"), strings.HasPrefix(line, "retry:"):
			t.Fatalf("content stream line %q: id/retry belong to the status stream only (sse-events.md §0)", line)
		default:
			t.Fatalf("unexpected SSE line %q", line)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read SSE: %v", err)
	}
	if seen {
		t.Fatalf("SSE body ended inside an event (no blank line): %+v", ev)
	}
	return sseEvent{}, false
}

func validateEvent(t *testing.T, ev sseEvent) {
	t.Helper()
	name, ok := sseSchemas[ev.Name]
	if !ok {
		t.Fatalf("unknown content event %q", ev.Name)
	}
	spec, err := apiContract.Spec()
	if err != nil {
		t.Fatalf("load openapi.yaml: %v", err)
	}
	if problems := spec.ValidateComponent(name, []byte(ev.Data)); len(problems) > 0 {
		t.Fatalf("%s event does not follow components.schemas.%s: %v\ndata: %s", ev.Name, name, problems, ev.Data)
	}
}

func assertSSEResponse(t *testing.T, resp *http.Response) {
	t.Helper()
	if resp.StatusCode != http.StatusOK {
		var b bytes.Buffer
		_, _ = b.ReadFrom(resp.Body)
		t.Fatalf("POST content status = %d, want 200; body: %s", resp.StatusCode, b.String())
	}
	for k, v := range map[string]string{"Content-Type": "text/event-stream; charset=utf-8", "Cache-Control": "no-cache", "X-Accel-Buffering": "no"} {
		if got := resp.Header.Get(k); got != v {
			t.Fatalf("%s = %q, want %q", k, got, v)
		}
	}
}

// generate runs one content generation to the end of the body and checks the stream grammar of
// sse-events.md §1.2: attempt1 ( terminal | invalid(final=false) attempt2 terminal ), exactly one
// terminal event, nothing after it.
func generate(t *testing.T, api, id, sc string) genResult {
	t.Helper()
	resp := postContent(t, t.Context(), api, id, nil, scenario(sc))
	defer func() { _ = resp.Body.Close() }()
	assertSSEResponse(t, resp)
	s := newSSEScanner(resp)
	var g genResult
	attempt := 1
	for {
		ev, ok := nextEvent(t, s)
		if !ok {
			break
		}
		if g.validated != nil || g.final != nil {
			t.Fatalf("event %q after the terminal event", ev.Name)
		}
		validateEvent(t, ev)
		g.events = append(g.events, ev)
		switch ev.Name {
		case "delta":
			var d deltaData
			mustUnmarshal(t, ev.Data, &d)
			if d.Attempt != attempt {
				t.Fatalf("delta attempt %d during attempt %d", d.Attempt, attempt)
			}
			g.deltas = append(g.deltas, d)
		case "invalid":
			var d invalidData
			mustUnmarshal(t, ev.Data, &d)
			if d.Final {
				g.final = &d
				break
			}
			if g.repair != nil || attempt != 1 {
				t.Fatalf("second non-final invalid event: %s", ev.Data)
			}
			g.repair = &d
			attempt = 2
		case "validated":
			var d validatedData
			mustUnmarshal(t, ev.Data, &d)
			g.validated = &d
		}
	}
	if g.validated == nil && g.final == nil {
		t.Fatalf("stream ended without a terminal event; events: %+v", g.events)
	}
	if g.validated != nil && g.validated.Attempt != attempt {
		t.Fatalf("validated attempt = %d, want %d", g.validated.Attempt, attempt)
	}
	if g.final != nil && g.final.Attempt != attempt {
		t.Fatalf("final invalid attempt = %d, want %d", g.final.Attempt, attempt)
	}
	return g
}

func mustUnmarshal(t *testing.T, s string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(s), v); err != nil {
		t.Fatalf("decode %s: %v", s, err)
	}
}

// ---- REST helpers ----

func audienceReady(t *testing.T, api string, m manifestDoc) string {
	t.Helper()
	id, _ := createCampaign(t, api, m.ExampleBrief.Brief)
	status, _, body := doJSON(t, http.MethodPost, api+"/api/campaigns/"+id+"/segment", nil, scenario("ok"))
	if status != http.StatusOK {
		t.Fatalf("POST segment status = %d; body: %s", status, body)
	}
	return id
}

func getCampaignDoc(t *testing.T, api, id string) campaignDoc {
	t.Helper()
	status, _, body := doJSON(t, http.MethodGet, api+"/api/campaigns/"+id, nil, nil)
	if status != http.StatusOK {
		t.Fatalf("GET campaign status = %d; body: %s", status, body)
	}
	var c campaignDoc
	mustUnmarshal(t, string(body), &c)
	return c
}

func listVersions(t *testing.T, api, id string) []versionDoc {
	t.Helper()
	status, _, body := doJSON(t, http.MethodGet, api+"/api/campaigns/"+id+"/content-versions", nil, nil)
	if status != http.StatusOK {
		t.Fatalf("GET content-versions status = %d; body: %s", status, body)
	}
	var page struct {
		Items []versionDoc `json:"items"`
	}
	mustUnmarshal(t, string(body), &page)
	return page.Items
}

func getVersion(t *testing.T, api, id, vid string) versionDoc {
	t.Helper()
	status, _, body := doJSON(t, http.MethodGet, api+"/api/campaigns/"+id+"/content-versions/"+vid, nil, nil)
	if status != http.StatusOK {
		t.Fatalf("GET content-version status = %d; body: %s", status, body)
	}
	var v versionDoc
	mustUnmarshal(t, string(body), &v)
	return v
}

func postEdit(t *testing.T, api, id, expected string, body []byte) (int, http.Header, []byte) {
	t.Helper()
	h := map[string]string{}
	if expected != "" {
		h["X-Expected-Version"] = expected
	}
	return doJSON(t, http.MethodPost, api+"/api/campaigns/"+id+"/content-versions", body, h)
}

func mustEdit(t *testing.T, api, id, expected string, c contentDoc) versionDoc {
	t.Helper()
	status, _, body := postEdit(t, api, id, expected, c.json(t))
	if status != http.StatusCreated {
		t.Fatalf("POST content-versions (expected %s) status = %d, want 201; body: %s", expected, status, body)
	}
	var v versionDoc
	mustUnmarshal(t, string(body), &v)
	return v
}

func approve(t *testing.T, api, id, vid string) (int, http.Header, []byte) {
	t.Helper()
	return doJSON(t, http.MethodPost, api+"/api/campaigns/"+id+"/approve", []byte(`{"content_version_id":"`+vid+`"}`), nil)
}

func mustApprove(t *testing.T, api, id, vid string) campaignDoc {
	t.Helper()
	status, _, body := approve(t, api, id, vid)
	if status != http.StatusOK {
		t.Fatalf("POST approve status = %d, want 200; body: %s", status, body)
	}
	var c campaignDoc
	mustUnmarshal(t, string(body), &c)
	return c
}

// assertEditConflict checks 409 content_edit_conflict and error.current_version (README §5.1).
func assertEditConflict(t *testing.T, status int, hdr http.Header, body []byte, wantID string, wantNumber int) {
	t.Helper()
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body: %s", status, body)
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var env struct {
		Error struct {
			Code           string `json:"code"`
			Message        string `json:"message"`
			RequestID      string `json:"request_id"`
			CurrentVersion *struct {
				ID            *string `json:"id"`
				VersionNumber *int    `json:"version_number"`
			} `json:"current_version"`
		} `json:"error"`
	}
	if err := dec.Decode(&env); err != nil {
		t.Fatalf("conflict body has unexpected keys (%v): %s", err, body)
	}
	e := env.Error
	if e.Code != "content_edit_conflict" || e.Message == "" || e.RequestID != hdr.Get("X-Request-ID") {
		t.Fatalf("conflict error = %+v, want content_edit_conflict with message and request id", e)
	}
	if e.CurrentVersion == nil {
		t.Fatalf("error.current_version missing: %s", body)
	}
	if wantID == "" {
		if e.CurrentVersion.ID != nil || e.CurrentVersion.VersionNumber != nil {
			t.Fatalf("current_version = %s, want both null", body)
		}
		return
	}
	if e.CurrentVersion.ID == nil || *e.CurrentVersion.ID != wantID || e.CurrentVersion.VersionNumber == nil || *e.CurrentVersion.VersionNumber != wantNumber {
		t.Fatalf("current_version = %s, want {%s %d}", body, wantID, wantNumber)
	}
}

type groupsDoc struct {
	Groups map[string]struct {
		CustomerIDs []string `json:"customer_ids"`
	} `json:"groups"`
}

func readGroups(t *testing.T) groupsDoc {
	t.Helper()
	b, err := os.ReadFile(filepath.FromSlash(manifestPath))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var g groupsDoc
	mustUnmarshal(t, string(b), &g)
	return g
}

// ---- generateContent ----

// REQ-CNT-04, REQ-AI-01, REQ-CNT-05, REQ-AUD-04
// Scenario ok end to end: deltas then validated (attempt 1); the deltas of each field concatenate
// to the validated content (CNT_OK); worst_case uses the manifest's longest first name; the version
// is stored (validated, source llm) and is the campaign's latest; the state is content_ready; the
// audience (consent filtered) is untouched by content.
func TestE2E_ContentGenerateValidated(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	id := audienceReady(t, api, m)

	g := generate(t, api, id, "ok")
	if g.repair != nil || g.final != nil || g.validated == nil {
		t.Fatalf("ok stream: repair=%v final=%v validated=%v, want only validated", g.repair, g.final, g.validated)
	}
	v := g.validated
	if diff := cmp.Diff(okDoc(), g.text(1)); diff != "" {
		t.Fatalf("delta concatenation mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(okDoc(), v.Content); diff != "" {
		t.Fatalf("validated content mismatch (-want +got):\n%s", diff)
	}
	wantWC := worstCaseDoc{SMSRunes: e2eSMSVarFree + m.ExampleBrief.LongestValues.FirstName, SubjectRunes: utf8.RuneCountInString(e2eOKSubject)}
	if v.WorstCase != wantWC || v.VersionNumber != 1 || v.Attempt != 1 {
		t.Fatalf("validated worst_case/version/attempt = %+v/%d/%d, want %+v/1/1", v.WorstCase, v.VersionNumber, v.Attempt, wantWC)
	}
	if v.WorstCase.SMSRunes > 160 {
		t.Fatalf("validated worst_case.sms_runes = %d > 160", v.WorstCase.SMSRunes)
	}

	c := getCampaignDoc(t, api, id)
	if c.State != "content_ready" || c.Approval != nil || c.LatestContentVersion == nil || c.LatestContentVersion.ID != v.ContentVersionID {
		t.Fatalf("campaign after validated = state %s approval %v latest %+v; want content_ready, null, %s", c.State, c.Approval, c.LatestContentVersion, v.ContentVersionID)
	}
	lv := *c.LatestContentVersion
	if lv.Status != "validated" || lv.Source != "llm" || lv.LLMAttempts == nil || *lv.LLMAttempts != 1 || lv.SegmentRevision != 1 || lv.VersionNumber != 1 || len(lv.ValidationErrors) != 0 {
		t.Fatalf("latest version = %+v, want validated/llm/1 attempt/revision 1/number 1/no errors", lv)
	}
	if lv.WorstCase == nil || *lv.WorstCase != wantWC {
		t.Fatalf("latest worst_case = %v, want %+v", lv.WorstCase, wantWC)
	}
	list := listVersions(t, api, id)
	if len(list) != 1 || list[0].ID != v.ContentVersionID {
		t.Fatalf("content-versions = %+v, want the one validated version", list)
	}
	if got := getVersion(t, api, id, v.ContentVersionID); got.ID != v.ContentVersionID || got.CampaignID != id {
		t.Fatalf("GET content-version = %+v", got)
	}
	a := c.Segment.Audience
	if a != m.ExampleBrief.Audience {
		t.Fatalf("audience after content = %+v, want the manifest's %+v (content never changes consent)", a, m.ExampleBrief.Audience)
	}
	assertExampleCount(t, api, m)
}

// REQ-CNT-04, REQ-AI-01
// then_ok_after_repair: attempt 1 fails (non-final invalid, sms.body sms_too_long 161, null id),
// the deltas restart with attempt 2 and the stream ends with validated attempt 2; one stored
// version with llm_attempts 2. The request body {} is accepted like no body.
func TestE2E_ContentRepairAttempt(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	id := audienceReady(t, api, m)
	g := generate(t, api, id, "then_ok_after_repair")
	if g.repair == nil || g.validated == nil {
		t.Fatalf("stream repair=%v validated=%v, want both", g.repair, g.validated)
	}
	r := g.repair
	if r.Attempt != 1 || r.Final || r.ContentVersionID != nil || !r.has("sms.body", "sms_too_long") {
		t.Fatalf("non-final invalid = %+v, want attempt 1, null id, sms.body sms_too_long", *r)
	}
	for _, f := range r.Errors {
		if f.Code == "sms_too_long" && (f.Limit == nil || *f.Limit != 160 || f.Actual == nil || *f.Actual != 161) {
			t.Fatalf("sms_too_long limit/actual = %v/%v, want 160/161", f.Limit, f.Actual)
		}
	}
	if got := g.text(1).SMS.Body; got != e2eSMS161 {
		t.Fatalf("attempt 1 sms deltas = %q, want CNT_SMS_161", got)
	}
	if diff := cmp.Diff(okDoc(), g.text(2)); diff != "" {
		t.Fatalf("attempt 2 delta concatenation mismatch (-want +got):\n%s", diff)
	}
	list := listVersions(t, api, id)
	if len(list) != 1 || list[0].LLMAttempts == nil || *list[0].LLMAttempts != 2 || list[0].Status != "validated" {
		t.Fatalf("content-versions = %+v, want one validated version with llm_attempts 2", list)
	}
}

// REQ-CNT-04, REQ-AI-01, REQ-CNT-01, REQ-CNT-02, REQ-CNT-03 (sse-events.md §1.1, §1.3.2)
// Scenarios whose last attempt is still invalid end with invalid final:true attempt 2. A
// structurally valid draft that breaks content rules is stored with status invalid (its id is in the
// event); otherwise nothing is stored. Either way the state stays audience_ready, there is no latest
// version, and the draft cannot be approved.
func TestE2E_ContentFinalInvalid(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	// anyPath: the contract names the code but not the path (content invalid_json).
	tests := []struct {
		scenario string
		stored   bool
		path     string
		anyPath  bool
		code     string
	}{
		{scenario: "over_limit_sms", stored: true, path: "sms.body", code: "sms_too_long"},
		{scenario: "over_limit_subject", stored: true, path: "email.subject", code: "subject_too_long"},
		{scenario: "unknown_variable", stored: true, path: "sms.body", code: "unknown_variable"},
		{scenario: "invalid_json", anyPath: true, code: "invalid_json"},
		{scenario: "schema_violation", path: "html", code: "schema_violation"},
		{scenario: "injection_attempt", path: "sql", code: "schema_violation"},
		{scenario: "truncated_stream", path: "", code: "truncated_output"},
		{scenario: "always_invalid", path: "html", code: "schema_violation"},
	}
	for _, tc := range tests {
		t.Run(tc.scenario, func(t *testing.T) {
			t.Parallel()
			id := audienceReady(t, api, m)
			g := generate(t, api, id, tc.scenario)
			if g.validated != nil || g.final == nil || g.repair == nil {
				t.Fatalf("stream validated=%v final=%v repair=%v, want repair + final invalid", g.validated, g.final, g.repair)
			}
			f := *g.final
			if f.Attempt != 2 || !f.Final {
				t.Fatalf("final invalid = %+v, want attempt 2 final", f)
			}
			found := false
			for _, e := range f.Errors {
				if e.Code == tc.code && (tc.anyPath || e.Path == tc.path) {
					found = true
				}
			}
			if !found {
				t.Fatalf("final errors = %+v, want %s at %q", f.Errors, tc.code, tc.path)
			}

			c := getCampaignDoc(t, api, id)
			if c.State != "audience_ready" || c.LatestContentVersion != nil {
				t.Fatalf("after final invalid state/latest = %s/%v, want audience_ready/null", c.State, c.LatestContentVersion)
			}
			list := listVersions(t, api, id)
			if !tc.stored {
				if f.ContentVersionID != nil || len(list) != 0 {
					t.Fatalf("content_version_id = %v, versions = %d; want null and none stored", f.ContentVersionID, len(list))
				}
				return
			}
			if f.ContentVersionID == nil || len(list) != 1 || list[0].ID != *f.ContentVersionID {
				t.Fatalf("content_version_id = %v, versions = %+v; want the stored draft", f.ContentVersionID, list)
			}
			d := getVersion(t, api, id, *f.ContentVersionID)
			if d.Status != "invalid" || d.Source != "llm" || !d.hasError(tc.path, tc.code) || d.LLMAttempts == nil || *d.LLMAttempts != 2 {
				t.Fatalf("draft = %+v, want status invalid, source llm, %s %s, llm_attempts 2", d, tc.path, tc.code)
			}
			if tc.code == "unknown_variable" && d.WorstCase != nil {
				t.Fatalf("draft worst_case = %+v, want null for an unknown variable", *d.WorstCase)
			}
			status, hdr, body := approve(t, api, id, d.ID)
			assertError(t, status, hdr, body, http.StatusConflict, "content_not_validated", false)
		})
	}
}

// REQ-APR-03, REQ-CNT-04 (D38)
// A stored invalid draft consumes a version number: draft v1, then the validated version is v2.
func TestE2E_ContentInvalidDraftConsumesVersionNumber(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	id := audienceReady(t, api, m)
	draft := generate(t, api, id, "over_limit_sms")
	if draft.final == nil || draft.final.ContentVersionID == nil {
		t.Fatalf("over_limit_sms stored no draft: %+v", draft.final)
	}
	g := generate(t, api, id, "ok")
	if g.validated == nil || g.validated.VersionNumber != 2 {
		t.Fatalf("validated after a draft = %+v, want version_number 2", g.validated)
	}
	var nums []int
	for _, v := range listVersions(t, api, id) {
		nums = append(nums, v.VersionNumber)
	}
	if diff := cmp.Diff([]int{2, 1}, nums); diff != "" {
		t.Fatalf("listed version numbers (newest first) mismatch (-want +got):\n%s", diff)
	}
}

// REQ-CNT-04, REQ-AI-01
// An unreachable LLM before the stream opens is HTTP 502 llm_unavailable as JSON (no SSE) and the
// campaign is unchanged.
func TestE2E_ContentLLMUnavailableIs502(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	id := audienceReady(t, api, m)
	status, hdr, body := doJSON(t, http.MethodPost, api+"/api/campaigns/"+id+"/content", nil, scenario("llm_unavailable"))
	assertError(t, status, hdr, body, http.StatusBadGateway, "llm_unavailable", false)
	if c := getCampaignDoc(t, api, id); c.State != "audience_ready" || c.LatestContentVersion != nil {
		t.Fatalf("after 502 state/latest = %s/%v, want audience_ready/null", c.State, c.LatestContentVersion)
	}
	if n := len(listVersions(t, api, id)); n != 0 {
		t.Fatalf("versions after 502 = %d, want 0", n)
	}
}

// REQ-CNT-04
// generateContent refusals before the stream: unknown campaign 404, draft (no segment) 409
// invalid_state; nothing is stored.
func TestE2E_ContentGenerateRequestChecks(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	draftID, _ := createCampaign(t, api, m.ExampleBrief.Brief)
	readyID := audienceReady(t, api, m)
	tests := []struct {
		name    string
		id      string
		body    []byte
		headers map[string]string
		status  int
		code    string
		field   *fieldWant
	}{
		{name: "unknown campaign", id: unknownCampaignID, headers: scenario("ok"), status: 404, code: "campaign_not_found"},
		{name: "draft campaign", id: draftID, headers: scenario("ok"), status: 409, code: "invalid_state"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			status, hdr, body := doJSON(t, http.MethodPost, api+"/api/campaigns/"+tc.id+"/content", tc.body, tc.headers)
			e := assertError(t, status, hdr, body, tc.status, tc.code, tc.field != nil)
			if tc.field != nil && !e.has(*tc.field) {
				t.Fatalf("error.fields = %+v, want %+v", e.Fields, *tc.field)
			}
		})
	}
	if n := len(listVersions(t, api, readyID)); n != 0 {
		t.Fatalf("refused generations stored %d versions", n)
	}
}

// REQ-CNT-04, REQ-APR-03 (D41, llm-tools.md §3.2 stalled_stream)
// While a stream is open (stalled_stream: first delta, then waits) the campaign's generation lock
// refuses a second content or segment generation, an edit and an approval with 409
// generation_in_progress; reads still work. When the client closes the connection no version is
// stored, the state is unchanged and the lock is released (an edit is then validated normally).
func TestE2E_ContentGenerationLock(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	id := audienceReady(t, api, m)
	v1 := generate(t, api, id, "ok").validated
	if v1 == nil {
		t.Fatal("setup: no validated version")
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	resp := postContent(t, ctx, api, id, nil, scenario("stalled_stream"))
	assertSSEResponse(t, resp)
	sc := newSSEScanner(resp)
	first, ok := nextEvent(t, sc)
	if !ok || first.Name != "delta" {
		t.Fatalf("first event = %+v, want a delta", first)
	}
	validateEvent(t, first)

	status, hdr, body := doJSON(t, http.MethodPost, api+"/api/campaigns/"+id+"/content", nil, scenario("ok"))
	assertError(t, status, hdr, body, http.StatusConflict, "generation_in_progress", false)
	status, hdr, body = doJSON(t, http.MethodPost, api+"/api/campaigns/"+id+"/segment", nil, scenario("ok"))
	assertError(t, status, hdr, body, http.StatusConflict, "generation_in_progress", false)
	status, hdr, body = postEdit(t, api, id, v1.ContentVersionID, editDoc("kilit").json(t))
	assertError(t, status, hdr, body, http.StatusConflict, "generation_in_progress", false)
	status, hdr, body = approve(t, api, id, v1.ContentVersionID)
	assertError(t, status, hdr, body, http.StatusConflict, "generation_in_progress", false)
	if c := getCampaignDoc(t, api, id); c.State != "content_ready" {
		t.Fatalf("GET during the stream state = %s, want content_ready", c.State)
	}
	_ = listVersions(t, api, id)
	if st, _, b := doJSON(t, http.MethodGet, api+"/api/campaigns/"+id+"/preview", nil, nil); st != http.StatusOK {
		t.Fatalf("GET preview during the stream = %d; body: %s", st, b)
	}

	cancel()
	_ = resp.Body.Close()

	// Poll until the lock is released: an over-limit edit gets 409 generation_in_progress while the
	// lock is held and 422 (nothing stored) once it is free.
	tooLong := mkContent(strings.Repeat("a", 161), "Konu", "Gövde").json(t)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.After(15 * time.Second)
	for released := false; !released; {
		select {
		case <-deadline:
			t.Fatal("the generation lock was not released within 15s after the client disconnected")
		case <-ticker.C:
			status, hdr, body := postEdit(t, api, id, v1.ContentVersionID, tooLong)
			switch status {
			case http.StatusConflict:
				assertError(t, status, hdr, body, http.StatusConflict, "generation_in_progress", false)
			case http.StatusUnprocessableEntity:
				released = true
			default:
				t.Fatalf("edit after disconnect status = %d; body: %s", status, body)
			}
		}
	}
	list := listVersions(t, api, id)
	if len(list) != 1 || list[0].ID != v1.ContentVersionID {
		t.Fatalf("versions after the interrupted stream = %+v, want only v1", list)
	}
	if c := getCampaignDoc(t, api, id); c.State != "content_ready" || c.LatestContentVersion == nil || c.LatestContentVersion.ID != v1.ContentVersionID {
		t.Fatalf("after the interrupted stream state/latest = %s/%v, want content_ready/v1", c.State, c.LatestContentVersion)
	}
	mustApprove(t, api, id, v1.ContentVersionID)
}

// ---- editContent ----

// REQ-APR-01, REQ-APR-03 (D40)
// Optimistic concurrency over HTTP: a stale base is 409 content_edit_conflict with
// error.current_version (both null before any version; the current id and number afterwards); the
// first edit on "none" is 201 with the edit fields; repeating it is a conflict too. The service
// semantics (stale after a validated regeneration, an invalid draft is never a base, nothing stored,
// numbering) live in TestService_EditExpectedVersion and TestService_VersionNumbersCountInvalidDrafts.
func TestE2E_EditOptimisticConcurrency(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	id := audienceReady(t, api, m)

	status, hdr, body := postEdit(t, api, id, "0b8f6c2e-6d0a-4a47-9c39-0f2d3b8e1a11", editDoc("x").json(t))
	assertEditConflict(t, status, hdr, body, "", 0)

	v1 := mustEdit(t, api, id, "none", editDoc("v1"))
	if v1.VersionNumber != 1 || v1.Status != "validated" || v1.Source != "edit" || v1.LLMAttempts != nil || v1.WorstCase == nil || len(v1.ValidationErrors) != 0 {
		t.Fatalf("first edit = %+v, want version 1, validated, edit, null llm_attempts, worst case", v1)
	}
	if c := getCampaignDoc(t, api, id); c.State != "content_ready" || c.LatestContentVersion.ID != v1.ID {
		t.Fatalf("after first edit state/latest = %s/%v", c.State, c.LatestContentVersion)
	}

	status, hdr, body = postEdit(t, api, id, "none", editDoc("v1").json(t))
	assertEditConflict(t, status, hdr, body, v1.ID, 1)
}

// REQ-APR-02, REQ-CNT-01, REQ-CNT-02, REQ-CNT-03, REQ-CNT-05
// An edit sent straight to the API goes through the same validator as LLM output: every invalid
// body is 422 with the contracted field error (lengths after substituting the segment's longest
// values, with limit/actual) and NOTHING is stored. Header and body format problems (missing,
// malformed or upper-case X-Expected-Version, broken JSON, > 64 KiB) are handler checks and live in
// internal/httpapi TestEditContent_ExpectedVersionHeader and TestEditContent_BodyChecks.
func TestE2E_EditValidation(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	id := audienceReady(t, api, m)
	base := generate(t, api, id, "ok").validated
	if base == nil {
		t.Fatal("setup: no validated version")
	}
	L := m.ExampleBrief.LongestValues.FirstName
	fit := strings.Repeat("a", 160-L)

	tests := []struct {
		name     string
		expected string
		body     []byte
		status   int
		code     string
		want     fieldWant
		limit    int
		actual   int
	}{
		{name: "161-char SMS", expected: base.ContentVersionID, body: mkContent(strings.Repeat("a", 161), e2eOKSubject, e2eOKBody).json(t), status: 422, code: "validation_failed", want: fieldWant{"sms.body", "sms_too_long"}, limit: 160, actual: 161},
		{name: "161 Turkish letters", expected: base.ContentVersionID, body: mkContent(strings.Repeat("ş", 161), e2eOKSubject, e2eOKBody).json(t), status: 422, code: "validation_failed", want: fieldWant{"sms.body", "sms_too_long"}, limit: 160, actual: 161},
		{name: "first_name placeholder one over after substitution", expected: base.ContentVersionID, body: mkContent("{{first_name}}"+fit+"a", e2eOKSubject, e2eOKBody).json(t), status: 422, code: "validation_failed", want: fieldWant{"sms.body", "sms_too_long"}, limit: 160, actual: 161},
		{name: "81-char subject", expected: base.ContentVersionID, body: mkContent(e2eOKSMS, strings.Repeat("ğ", 81), e2eOKBody).json(t), status: 422, code: "validation_failed", want: fieldWant{"email.subject", "subject_too_long"}, limit: 80, actual: 81},
		{name: "5001-char email body", expected: base.ContentVersionID, body: mkContent(e2eOKSMS, e2eOKSubject, strings.Repeat("ü", 5001)).json(t), status: 422, code: "validation_failed", want: fieldWant{"email.body", "email_body_too_long"}, limit: 5000, actual: 5001},
		{name: "unknown variable", expected: base.ContentVersionID, body: mkContent("şifren {{password}}", e2eOKSubject, e2eOKBody).json(t), status: 422, code: "validation_failed", want: fieldWant{"sms.body", "unknown_variable"}},
		{name: "variable names are case-sensitive", expected: base.ContentVersionID, body: mkContent("{{First_Name}} merhaba", e2eOKSubject, e2eOKBody).json(t), status: 422, code: "validation_failed", want: fieldWant{"sms.body", "unknown_variable"}},
		{name: "malformed variable", expected: base.ContentVersionID, body: mkContent(e2eOKSMS, "Merhaba {{first_name", e2eOKBody).json(t), status: 422, code: "validation_failed", want: fieldWant{"email.subject", "malformed_variable"}},
		{name: "empty SMS", expected: base.ContentVersionID, body: mkContent("", e2eOKSubject, e2eOKBody).json(t), status: 422, code: "validation_failed", want: fieldWant{"sms.body", "required"}},
		{name: "whitespace-only email body", expected: base.ContentVersionID, body: mkContent(e2eOKSMS, e2eOKSubject, " \n ").json(t), status: 422, code: "validation_failed", want: fieldWant{"email.body", "required"}},
		{name: "missing key", expected: base.ContentVersionID, body: []byte(`{"sms":{"body":"a"},"email":{"subject":"b"}}`), status: 422, code: "validation_failed", want: fieldWant{"email.body", "schema_violation"}},
		{name: "unknown key", expected: base.ContentVersionID, body: []byte(`{"sms":{"body":"a"},"email":{"subject":"b","body":"c"},"html":"<script>alert(1)</script>"}`), status: 422, code: "validation_failed", want: fieldWant{"html", "schema_violation"}},
	}
	// The group returns only after its parallel subtests finished.
	t.Run("rejected", func(t *testing.T) {
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				status, hdr, body := postEdit(t, api, id, tc.expected, tc.body)
				e := assertError(t, status, hdr, body, tc.status, tc.code, tc.code == "validation_failed")
				if tc.code != "validation_failed" {
					return
				}
				if !e.has(tc.want) {
					t.Fatalf("error.fields = %+v, want %+v", e.Fields, tc.want)
				}
				if tc.limit != 0 {
					for _, f := range e.Fields {
						if f.Path == tc.want.Path && (f.Limit == nil || *f.Limit != tc.limit || f.Actual == nil || *f.Actual != tc.actual) {
							t.Fatalf("%s limit/actual = %v/%v, want %d/%d", f.Code, f.Limit, f.Actual, tc.limit, tc.actual)
						}
					}
				}
			})
		}
	})
	list := listVersions(t, api, id)
	if len(list) != 1 || list[0].ID != base.ContentVersionID {
		t.Fatalf("rejected edits stored versions: %+v, want only the base", list)
	}
	if c := getCampaignDoc(t, api, id); c.State != "content_ready" {
		t.Fatalf("state after rejected edits = %s, want content_ready", c.State)
	}
}

// REQ-CNT-01, REQ-CNT-02, REQ-CNT-05
// Boundary edits that ARE accepted: 160 Turkish letters, a first_name template that is exactly 160
// after substitution (worst_case.sms_runes 160), an 80-char subject.
func TestE2E_EditAcceptsBoundaries(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	L := m.ExampleBrief.LongestValues.FirstName
	tests := []struct {
		name     string
		c        contentDoc
		wantSMS  int
		wantSubj int
	}{
		{name: "160 Turkish letters", c: mkContent(strings.Repeat("ş", 160), strings.Repeat("ğ", 80), "Gövde"), wantSMS: 160, wantSubj: 80},
		{name: "first_name template exactly 160 after substitution", c: mkContent("{{first_name}}"+strings.Repeat("a", 160-L), "Konu", "Gövde"), wantSMS: 160, wantSubj: 4},
		{name: "spaces inside the braces", c: mkContent("{{ first_name }}"+strings.Repeat("a", 160-L), "Konu {{city}}", "Gövde"), wantSMS: 160, wantSubj: 5 + m.ExampleBrief.LongestValues.City},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			id := audienceReady(t, api, m)
			v := mustEdit(t, api, id, "none", tc.c)
			if v.WorstCase == nil || v.WorstCase.SMSRunes != tc.wantSMS || v.WorstCase.SubjectRunes != tc.wantSubj {
				t.Fatalf("worst_case = %v, want {%d %d}", v.WorstCase, tc.wantSMS, tc.wantSubj)
			}
			if diff := cmp.Diff(tc.c, v.Content); diff != "" {
				t.Fatalf("stored content mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// ---- approveContent ----

// REQ-APR-01, REQ-APR-03 (D39; openapi approveContent, README §5.4 P4 matrix)
// Approve the latest validated version: 200 approved Campaign bound to it; approving it again is
// idempotent (same approval id and approved_at); an edit after approval drops the campaign to
// content_ready with no approval; the old version is then stale (409); the new one can be approved;
// after a new segment the campaign is audience_ready and approving is 409 invalid_state.
// Before that, a body over 64 KiB (413) and an unknown campaign (404) are refused and change
// nothing. 409 generation_in_progress and
// content_not_validated are covered by TestE2E_ContentGenerationLock and TestE2E_ContentFinalInvalid;
// the 422 worst-case re-check needs a customer mutation (forbidden here, openapi approveContent).
func TestE2E_ApproveFlow(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	id := audienceReady(t, api, m)
	v1 := generate(t, api, id, "ok").validated
	if v1 == nil {
		t.Fatal("setup: no validated version")
	}

	vid := v1.ContentVersionID
	refused := []struct {
		name    string
		id      string
		body    []byte
		headers map[string]string
		status  int
		code    string
		field   *fieldWant
	}{
		{name: "body over 65536 bytes", id: id, body: []byte(`{"content_version_id":"` + vid + `"` + strings.Repeat(" ", 65537) + `}`), status: 413, code: "payload_too_large"},
		{name: "unknown campaign", id: unknownCampaignID, body: []byte(`{"content_version_id":"` + vid + `"}`), status: 404, code: "campaign_not_found"},
	}
	// The group returns only after its parallel subtests finished.
	t.Run("refused", func(t *testing.T) {
		for _, tc := range refused {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				status, hdr, body := doJSON(t, http.MethodPost, api+"/api/campaigns/"+tc.id+"/approve", tc.body, tc.headers)
				e := assertError(t, status, hdr, body, tc.status, tc.code, tc.field != nil)
				if tc.field != nil && !e.hasLoose(*tc.field) {
					t.Fatalf("error.fields = %+v, want %+v", e.Fields, *tc.field)
				}
			})
		}
	})
	if c := getCampaignDoc(t, api, id); c.State != "content_ready" || c.Approval != nil {
		t.Fatalf("after refused approvals state/approval = %s/%v, want content_ready/null", c.State, c.Approval)
	}

	c1 := mustApprove(t, api, id, v1.ContentVersionID)
	if c1.State != "approved" || c1.Approval == nil || c1.Approval.ContentVersionID != v1.ContentVersionID {
		t.Fatalf("approve = state %s approval %+v, want approved bound to v1", c1.State, c1.Approval)
	}
	c2 := mustApprove(t, api, id, v1.ContentVersionID)
	if c2.Approval == nil || c2.Approval.ID != c1.Approval.ID || c2.Approval.ApprovedAt != c1.Approval.ApprovedAt {
		t.Fatalf("second approve = %+v, want the unchanged approval %+v", c2.Approval, c1.Approval)
	}

	v2 := mustEdit(t, api, id, v1.ContentVersionID, editDoc("onay sonrası"))
	c3 := getCampaignDoc(t, api, id)
	if c3.State != "content_ready" || c3.Approval != nil || c3.LatestContentVersion.ID != v2.ID {
		t.Fatalf("after edit state/approval/latest = %s/%v/%v, want content_ready/null/v2", c3.State, c3.Approval, c3.LatestContentVersion)
	}
	status, hdr, body := approve(t, api, id, v1.ContentVersionID)
	assertError(t, status, hdr, body, http.StatusConflict, "stale_content_version", false)
	c4 := mustApprove(t, api, id, v2.ID)
	if c4.State != "approved" || c4.Approval.ContentVersionID != v2.ID || c4.Approval.ID == c1.Approval.ID {
		t.Fatalf("re-approval = %+v, want a new approval bound to v2", c4.Approval)
	}

	// A new segment returns the campaign to audience_ready: the validated v2 is now refused with
	// 409 invalid_state (check 4 comes before the stale check 5).
	status, _, body = doJSON(t, http.MethodPost, api+"/api/campaigns/"+id+"/segment", nil, scenario("ok"))
	if status != http.StatusOK {
		t.Fatalf("POST segment status = %d; body: %s", status, body)
	}
	if c := getCampaignDoc(t, api, id); c.State != "audience_ready" || c.Approval != nil {
		t.Fatalf("after a new segment state/approval = %s/%v, want audience_ready/null", c.State, c.Approval)
	}
	status, hdr, body = approve(t, api, id, v2.ID)
	assertError(t, status, hdr, body, http.StatusConflict, "invalid_state", false)
}

// ---- previewContent ----

type previewDoc struct {
	CampaignID       string `json:"campaign_id"`
	ContentVersionID string `json:"content_version_id"`
	SampleIndex      int    `json:"sample_index"`
	SampleCount      int    `json:"sample_count"`
	Customer         struct {
		ID        string `json:"id"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
		City      string `json:"city"`
	} `json:"customer"`
	SMS struct {
		Text        string `json:"text"`
		Runes       int    `json:"runes"`
		Limit       int    `json:"limit"`
		WithinLimit bool   `json:"within_limit"`
	} `json:"sms"`
	Email struct {
		Subject      string `json:"subject"`
		SubjectRunes int    `json:"subject_runes"`
		SubjectLimit int    `json:"subject_limit"`
		WithinLimit  bool   `json:"within_limit"`
		Text         string `json:"text"`
		HTML         string `json:"html"`
	} `json:"email"`
}

func preview(t *testing.T, api, id, query string) (previewDoc, []byte) {
	t.Helper()
	status, _, body := doJSON(t, http.MethodGet, api+"/api/campaigns/"+id+"/preview"+query, nil, nil)
	if status != http.StatusOK {
		t.Fatalf("GET preview%s status = %d, want 200; body: %s", query, status, body)
	}
	var p previewDoc
	mustUnmarshal(t, string(body), &p)
	return p, body
}

// REQ-APR-01, REQ-CNT-03, REQ-CNT-05
// The preview renders the latest validated version for one deterministic sample: the
// sample_index-th matched customer in UUID text order (default 0); the same request returns the
// same bytes; sample_count is the matched count (before consent); an empty first name renders the
// fallback; lengths are reported in runes; no email or phone is exposed.
func TestE2E_Preview(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	groups := readGroups(t)
	id := audienceReady(t, api, m)
	v := generate(t, api, id, "ok").validated
	if v == nil {
		t.Fatal("setup: no validated version")
	}

	p0, raw0 := preview(t, api, id, "")
	if p0.CampaignID != id || p0.ContentVersionID != v.ContentVersionID || p0.SampleIndex != 0 || p0.SampleCount != m.ExampleBrief.Audience.Matched {
		t.Fatalf("default preview = %+v, want latest version, index 0, count %d", p0, m.ExampleBrief.Audience.Matched)
	}
	_, again := preview(t, api, id, "")
	if !bytes.Equal(raw0, again) {
		t.Fatalf("the same preview request returned different bodies:\n%s\n%s", raw0, again)
	}
	// The strict client already refuses extra keys (Preview.customer has no email/phone).
	if p0.SMS.Limit != 160 || p0.Email.SubjectLimit != 80 {
		t.Fatalf("limits = %d/%d, want 160/80", p0.SMS.Limit, p0.Email.SubjectLimit)
	}
	if p0.SMS.Runes != utf8.RuneCountInString(p0.SMS.Text) || p0.Email.SubjectRunes != utf8.RuneCountInString(p0.Email.Subject) {
		t.Fatalf("rune counts %d/%d do not match the texts %q/%q", p0.SMS.Runes, p0.Email.SubjectRunes, p0.SMS.Text, p0.Email.Subject)
	}
	if p0.SMS.WithinLimit != (p0.SMS.Runes <= 160) || p0.Email.WithinLimit != (p0.Email.SubjectRunes <= 80) || !p0.SMS.WithinLimit {
		t.Fatalf("within_limit flags = %v/%v for %d/%d runes", p0.SMS.WithinLimit, p0.Email.WithinLimit, p0.SMS.Runes, p0.Email.SubjectRunes)
	}
	if strings.Contains(p0.SMS.Text, "{{") || strings.Contains(p0.Email.Text, "{{") {
		t.Fatalf("rendered text still contains a placeholder: %q / %q", p0.SMS.Text, p0.Email.Text)
	}

	// UUID text order: the first five samples have strictly increasing customer ids.
	prev := p0.Customer.ID
	for i := 1; i < 5; i++ {
		p, _ := preview(t, api, id, "?sample_index="+strconv.Itoa(i))
		if p.SampleIndex != i || p.Customer.ID <= prev {
			t.Fatalf("sample %d = index %d customer %s, want index %d and an id after %s", i, p.SampleIndex, p.Customer.ID, i, prev)
		}
		prev = p.Customer.ID
	}
	last, _ := preview(t, api, id, "?sample_index="+strconv.Itoa(m.ExampleBrief.Audience.Matched-1))
	if last.SampleIndex != m.ExampleBrief.Audience.Matched-1 {
		t.Fatalf("last sample index = %d", last.SampleIndex)
	}

	empty := groups.Groups["empty_first_name"].CustomerIDs
	if len(empty) == 0 {
		t.Fatal("manifest has no empty_first_name group")
	}
	pe, _ := preview(t, api, id, "?customer_id="+empty[0]+"&sample_index=3")
	if pe.Customer.ID != empty[0] || pe.Customer.FirstName != "" {
		t.Fatalf("customer preview = %+v, want customer %s with an empty first name (customer_id wins over sample_index)", pe.Customer, empty[0])
	}
	wantPrefix := "Merhaba " + e2eFallback + ","
	if !strings.HasPrefix(pe.SMS.Text, wantPrefix) || !strings.HasPrefix(pe.Email.Text, wantPrefix) {
		t.Fatalf("empty first name renders %q / %q, want the prefix %q", pe.SMS.Text, pe.Email.Text, wantPrefix)
	}

	for _, cid := range groups.Groups["long_first_name"].CustomerIDs {
		pl, _ := preview(t, api, id, "?customer_id="+cid)
		if pl.SMS.Runes > 160 || !pl.SMS.WithinLimit {
			t.Fatalf("long first name customer %s renders %d runes, want <= 160 (validated with the worst case)", cid, pl.SMS.Runes)
		}
	}
}

// REQ-APR-01
// Preview of an unknown campaign is 404 (HTTP status table: "Bilinmeyen kampanya").
func TestE2E_PreviewErrors(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	tests := []struct {
		name   string
		id     string
		query  string
		status int
		code   string
		field  *fieldWant
	}{
		{name: "unknown campaign", id: unknownCampaignID, status: 404, code: "campaign_not_found"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			status, hdr, body := doJSON(t, http.MethodGet, api+"/api/campaigns/"+tc.id+"/preview"+tc.query, nil, nil)
			e := assertError(t, status, hdr, body, tc.status, tc.code, tc.field != nil)
			if tc.field != nil && !e.has(*tc.field) {
				t.Fatalf("error.fields = %+v, want %+v", e.Fields, *tc.field)
			}
		})
	}
}

// REQ-UI-01, REQ-AI-01
// html_in_content: the content is valid and stored as text; the preview's sms.text and
// email.subject/text are plain text (tags kept verbatim as characters) and email.html is escaped
// with no element outside the fixed skeleton (no script, no anchor, no <b>/<i>).
func TestE2E_PreviewEscapesHTML(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	id := audienceReady(t, api, m)
	g := generate(t, api, id, "html_in_content")
	if g.validated == nil {
		t.Fatalf("html_in_content did not validate: %+v", g.final)
	}
	if diff := cmp.Diff(htmlDoc(), g.validated.Content); diff != "" {
		t.Fatalf("stored content mismatch (-want +got):\n%s", diff)
	}
	p, _ := preview(t, api, id, "")
	if !strings.Contains(p.SMS.Text, "<b>%30</b>") || p.Email.Subject != e2eHTMLSubj || !strings.Contains(p.Email.Text, "<script>alert('xss')</script>") {
		t.Fatalf("plain-text fields were altered: sms %q subject %q text %q", p.SMS.Text, p.Email.Subject, p.Email.Text)
	}
	h := p.Email.HTML
	for _, bad := range []string{"<script", "<a ", "<a>", "<b>", "<i>", "href=\"javascript"} {
		if strings.Contains(strings.ToLower(h), bad) {
			t.Fatalf("email.html contains %q: %s", bad, h)
		}
	}
	for _, good := range []string{"&lt;script&gt;", "&lt;i&gt;Sepetin&lt;/i&gt;"} {
		if !strings.Contains(h, good) {
			t.Fatalf("email.html lacks the escaped %q: %s", good, h)
		}
	}
	allowed := []string{"!doctype", "html", "/html", "head", "/head", "meta", "title", "/title", "body", "/body", "p", "/p", "br"}
	for _, part := range strings.Split(h, "<")[1:] {
		name := strings.ToLower(strings.FieldsFunc(part, func(r rune) bool { return r == ' ' || r == '>' || r == '\n' || r == '\t' })[0])
		if !slices.Contains(allowed, name) {
			t.Fatalf("email.html has an element outside the skeleton: <%s in %s", name, h)
		}
	}
}

// ---- content versions ----

// REQ-APR-03 (openapi listContentVersions, getContentVersion)
// Unknown campaigns and versions are 404; a version of another campaign is not found through this
// campaign.
func TestE2E_ContentVersionsNotFound(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	id := audienceReady(t, api, m)
	other := audienceReady(t, api, m)
	ov := generate(t, api, other, "ok").validated
	if ov == nil {
		t.Fatal("setup failed")
	}
	if n := len(listVersions(t, api, id)); n != 0 {
		t.Fatalf("versions of a fresh campaign = %d, want 0", n)
	}
	tests := []struct {
		name, path, code string
	}{
		{name: "list for an unknown campaign", path: "/api/campaigns/" + unknownCampaignID + "/content-versions", code: "campaign_not_found"},
		{name: "version of an unknown campaign", path: "/api/campaigns/" + unknownCampaignID + "/content-versions/" + ov.ContentVersionID, code: "campaign_not_found"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			status, hdr, body := doJSON(t, http.MethodGet, api+tc.path, nil, nil)
			assertError(t, status, hdr, body, http.StatusNotFound, tc.code, false)
		})
	}
}
