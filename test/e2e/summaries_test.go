package e2e_test

// GET /api/campaign-summaries (operationId listCampaignSummaries, REQ-DSH-01) against the
// compose.test.yml stack. The E2E database is shared with parallel tests, so the test creates its
// own draft, scheduled and completed campaigns and asserts exact values only about them; order,
// filter and "total = overview count" are asserted inside each single response; counts over all
// campaigns are lower bounds only. Every call goes through the strict OpenAPI client.

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

var (
	dshPageKeys     = []string{"items", "limit", "offset", "overview", "total"}
	dshItemKeys     = []string{"audience", "brief", "completed_at", "created_at", "deliveries", "id", "scheduled_at", "sending_started_at", "state", "updated_at"}
	dshCampaignKeys = []string{"approved", "audience_ready", "completed", "content_ready", "draft", "scheduled", "sending", "total"}
	dshStateNames   = []string{"draft", "audience_ready", "content_ready", "approved", "scheduled", "sending", "completed"}
)

const dshE2EBrief = "İzmir'deki gümüş üyelere hafta sonu ücretsiz kargo hatırlatması gönder.\nKısa ve samimi olsun (ğüşİıöç)."

type dshPage struct {
	Items    []map[string]any `json:"items"`
	Total    int              `json:"total"`
	Limit    int              `json:"limit"`
	Offset   int              `json:"offset"`
	Overview struct {
		Campaigns  map[string]int `json:"campaigns"`
		Deliveries sndCounts      `json:"deliveries"`
	} `json:"overview"`
}

// dshList reads one page and checks what holds inside a single response: the exact key sets, the
// filter, the order, total = the overview count of the filtered states, the overview sums.
func dshList(t *testing.T, api, query string) dshPage {
	t.Helper()
	status, _, body := doJSON(t, http.MethodGet, api+"/api/campaign-summaries"+query, nil, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /api/campaign-summaries%s status = %d, want 200; body: %s", query, status, body)
	}
	raw := decodeObject(t, body)
	if diff := cmp.Diff(dshPageKeys, sortedKeys(raw)); diff != "" {
		t.Fatalf("CampaignSummaryPage keys mismatch (-want +got):\n%s", diff)
	}
	if _, ok := raw["items"].([]any); !ok {
		t.Fatalf("items = %v, want an array", raw["items"])
	}
	var p dshPage
	mustUnmarshal(t, string(body), &p)
	if len(p.Items) > p.Limit {
		t.Fatalf("%d items for limit %d", len(p.Items), p.Limit)
	}

	ov := asObject(t, raw["overview"])
	if diff := cmp.Diff(dshCampaignKeys, sortedKeys(asObject(t, ov["campaigns"]))); diff != "" {
		t.Fatalf("overview.campaigns keys mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(sndCountKeys, sortedKeys(asObject(t, ov["deliveries"]))); diff != "" {
		t.Fatalf("overview.deliveries keys mismatch (-want +got):\n%s", diff)
	}
	sum := 0
	for _, s := range dshStateNames {
		sum += p.Overview.Campaigns[s]
	}
	if p.Overview.Campaigns["total"] != sum {
		t.Fatalf("overview.campaigns = %v, want total = the sum of the seven states (%d)", p.Overview.Campaigns, sum)
	}
	d := p.Overview.Deliveries
	if d.Total != d.Queued+d.Sending+d.Sent+d.Failed {
		t.Fatalf("overview.deliveries = %+v, want total = queued + sending + sent + failed", d)
	}

	q := dshQuery(t, query)
	states := q["state"]
	want := p.Overview.Campaigns["total"]
	if len(states) > 0 {
		want = 0
		for _, s := range states {
			want += p.Overview.Campaigns[s]
		}
	}
	if p.Total != want {
		t.Fatalf("total = %d for %q, want %d from the overview of the same response %v", p.Total, query, want, p.Overview.Campaigns)
	}

	for i, it := range p.Items {
		if diff := cmp.Diff(dshItemKeys, sortedKeys(it)); diff != "" {
			t.Fatalf("items[%d] CampaignSummary keys mismatch (-want +got):\n%s", i, diff)
		}
		state, _ := it["state"].(string)
		if len(states) > 0 && !slices.Contains(states, state) {
			t.Fatalf("items[%d] state %q is outside the filter %v", i, state, states)
		}
		if i > 0 && !dshOrdered(t, q["order"], p.Items[i-1], it) {
			t.Fatalf("items[%d] and items[%d] break the order %v:\n%v\n%v", i-1, i, q["order"], p.Items[i-1], it)
		}
	}
	return p
}

// dshQuery returns the de-duplicated state list and the order of a query string.
func dshQuery(t *testing.T, query string) map[string][]string {
	t.Helper()
	out := map[string][]string{"order": {"recent"}}
	for _, part := range strings.Split(strings.TrimPrefix(query, "?"), "&") {
		k, v, _ := strings.Cut(part, "=")
		switch k {
		case "state":
			for _, s := range strings.Split(strings.ReplaceAll(v, "%2C", ","), ",") {
				if !slices.Contains(out["state"], s) {
					out["state"] = append(out["state"], s)
				}
			}
		case "order":
			out["order"] = []string{v}
		}
	}
	return out
}

func dshTime(t *testing.T, v any) *time.Time {
	t.Helper()
	if v == nil {
		return nil
	}
	s, _ := v.(string)
	ts, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("timestamp %v: %v", v, err)
	}
	return &ts
}

// dshOrdered: recent = created_at not increasing, ties id ascending; scheduled_at = not decreasing,
// nulls last, ties (and nulls) id ascending.
func dshOrdered(t *testing.T, order []string, a, b map[string]any) bool {
	t.Helper()
	idLess := a["id"].(string) < b["id"].(string)
	if len(order) == 1 && order[0] == "scheduled_at" {
		x, y := dshTime(t, a["scheduled_at"]), dshTime(t, b["scheduled_at"])
		switch {
		case x == nil && y == nil:
			return idLess
		case x == nil:
			return false
		case y == nil:
			return true
		case x.Equal(*y):
			return idLess
		default:
			return x.Before(*y)
		}
	}
	x, y := dshTime(t, a["created_at"]), dshTime(t, b["created_at"])
	if x.Equal(*y) {
		return idLess
	}
	return x.After(*y)
}

// dshAll pages through base (which carries limit=200) and returns every item by id.
func dshAll(t *testing.T, api, base string) (map[string]map[string]any, dshPage) {
	t.Helper()
	out := map[string]map[string]any{}
	var first dshPage
	for offset := 0; ; offset += 200 {
		q := base
		if offset > 0 {
			q += "&offset=" + strconv.Itoa(offset)
		}
		p := dshList(t, api, q)
		if offset == 0 {
			first = p
		}
		for _, it := range p.Items {
			if id, _ := it["id"].(string); out[id] == nil {
				out[id] = it
			}
		}
		if offset+200 >= p.Total || len(p.Items) == 0 {
			return out, first
		}
	}
}

func dshGetCampaign(t *testing.T, api, id string) map[string]any {
	t.Helper()
	status, _, body := doJSON(t, http.MethodGet, api+"/api/campaigns/"+id, nil, nil)
	if status != http.StatusOK {
		t.Fatalf("GET campaign %s status = %d; body: %s", id, status, body)
	}
	return decodeObject(t, body)
}

// dshAssertSameAsCampaign: brief, state and the five timestamps are those of GET campaign; audience
// is the three counts of segment.audience (null without a segment).
func dshAssertSameAsCampaign(t *testing.T, it map[string]any, c map[string]any) {
	t.Helper()
	if it["brief"] != c["brief"] || it["state"] != c["state"] || it["id"] != c["id"] {
		t.Fatalf("summary id/brief/state = %v/%q/%v, want %v/%q/%v", it["id"], it["brief"], it["state"], c["id"], c["brief"], c["state"])
	}
	for _, k := range []string{"scheduled_at", "sending_started_at", "completed_at", "created_at", "updated_at"} {
		a, b := dshTime(t, it[k]), dshTime(t, c[k])
		if (a == nil) != (b == nil) || (a != nil && !a.Equal(*b)) {
			t.Fatalf("summary %s = %v, want %v as in GET campaign", k, it[k], c[k])
		}
	}
	if c["segment"] == nil {
		if it["audience"] != nil {
			t.Fatalf("summary audience = %v, want null without a segment", it["audience"])
		}
		return
	}
	a := asObject(t, asObject(t, c["segment"])["audience"])
	want := map[string]any{"matched": a["matched"], "sms": a["sms"], "email": a["email"]}
	if diff := cmp.Diff(want, it["audience"]); diff != "" {
		t.Fatalf("summary audience mismatch with segment.audience (-want +got):\n%s", diff)
	}
}

// REQ-DSH-01 C10, C2, C3
// Own campaigns: the draft is found under state=draft&order=recent&limit=200 with audience,
// deliveries and the three times null; the scheduled one under
// state=scheduled&order=scheduled_at&limit=200 with scheduled_at of the send response and
// deliveries null; the completed one under state=sending,completed&order=recent&limit=200 with
// deliveries = /status counts and audience = segment.audience. Every item equals GET campaign (brief
// in full). No filter returns an own campaign of another state. Order and totals are checked inside
// each response; overview counts are lower bounds.
func TestE2E_CampaignSummaries_OwnCampaigns(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	if m.ExampleBrief.Audience.SMS+m.ExampleBrief.Audience.Email == 0 {
		t.Fatal("setup: the example brief audience is empty in the manifest; this test needs deliveries")
	}
	deadline := time.After(trkStreamDeadline)

	draftID, _ := createCampaign(t, api, dshE2EBrief)

	schedID, schedVID := sndApprovedEmpty(t, api, m)
	at := schServerNow(t, api).Add(2 * time.Hour)
	planned := schMustSchedule(t, api, schedID, schedVID, at)

	doneID, doneVID := approvedCampaign(t, api, m)
	stream := trkOpen(t, api, doneID, doneID, nil)
	stream.next(t, deadline, "the snapshot")
	trkSend(t, api, doneID, doneVID)
	stream.readToCompleted(t, deadline)

	own := map[string]string{draftID: "draft", schedID: "scheduled", doneID: "completed"}
	lists := map[string]string{
		"draft":     "?state=draft&order=recent&limit=200",
		"scheduled": "?state=scheduled&order=scheduled_at&limit=200",
		"completed": "?state=sending,completed&order=recent&limit=200",
	}
	found := map[string]map[string]map[string]any{}
	for name, q := range lists {
		found[name], _ = dshAll(t, api, q)
	}
	for id, state := range own {
		for name, items := range found {
			if got := items[id] != nil; got != (name == state) {
				t.Fatalf("own %s campaign %s in the %s list: %v, want %v", state, id, name, got, name == state)
			}
		}
	}

	draft := found["draft"][draftID]
	dshAssertSameAsCampaign(t, draft, dshGetCampaign(t, api, draftID))
	if draft["brief"] != dshE2EBrief {
		t.Fatalf("draft summary brief = %q, want the full multi-line brief", draft["brief"])
	}
	for _, k := range []string{"audience", "deliveries", "scheduled_at", "sending_started_at", "completed_at"} {
		if draft[k] != nil {
			t.Fatalf("draft summary %s = %v, want null", k, draft[k])
		}
	}

	sched := found["scheduled"][schedID]
	dshAssertSameAsCampaign(t, sched, dshGetCampaign(t, api, schedID))
	if a, b := dshTime(t, sched["scheduled_at"]), dshTime(t, planned["scheduled_at"]); a == nil || !a.Equal(*b) {
		t.Fatalf("scheduled summary scheduled_at = %v, want %v of the send response", sched["scheduled_at"], planned["scheduled_at"])
	}
	if sched["deliveries"] != nil {
		t.Fatalf("scheduled summary deliveries = %v, want null", sched["deliveries"])
	}

	done := found["completed"][doneID]
	dshAssertSameAsCampaign(t, done, dshGetCampaign(t, api, doneID))
	status, _, sbody := doJSON(t, http.MethodGet, api+"/api/campaigns/"+doneID+"/status", nil, nil)
	if status != http.StatusOK {
		t.Fatalf("GET status = %d; body: %s", status, sbody)
	}
	if diff := cmp.Diff(decodeObject(t, sbody)["counts"], done["deliveries"]); diff != "" {
		t.Fatalf("completed summary deliveries differ from /status counts (-status +summary):\n%s", diff)
	}
	var doneCounts sndCounts
	b, err := json.Marshal(done["deliveries"])
	if err != nil {
		t.Fatalf("marshal deliveries: %v", err)
	}
	mustUnmarshal(t, string(b), &doneCounts)

	// Lower bounds over all campaigns, read after the own campaign completed.
	p := dshList(t, api, "")
	if p.Overview.Deliveries.Sent < doneCounts.Sent || p.Overview.Campaigns["completed"] < 1 {
		t.Fatalf("overview deliveries.sent/campaigns.completed = %d/%d, want >= %d/1", p.Overview.Deliveries.Sent, p.Overview.Campaigns["completed"], doneCounts.Sent)
	}
}

// REQ-DSH-01 C1, C5, C6, C7
// Smoke: defaults are echoed (limit 50, offset 0) and limit=5&offset=5 is echoed; an offset past
// the end gives items [] with total still the filtered count; state=x&limit=0 is one 422 with the
// fields query.state then query.limit.
func TestE2E_CampaignSummaries_Paging(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	createCampaign(t, api, dshE2EBrief)

	if p := dshList(t, api, ""); p.Limit != 50 || p.Offset != 0 {
		t.Fatalf("defaults limit/offset = %d/%d, want 50/0", p.Limit, p.Offset)
	}
	if p := dshList(t, api, "?limit=5&offset=5"); p.Limit != 5 || p.Offset != 5 {
		t.Fatalf("limit/offset = %d/%d, want the echo 5/5", p.Limit, p.Offset)
	}
	if p := dshList(t, api, "?state=draft&order=recent&limit=200&offset=100000000"); len(p.Items) != 0 || p.Total < 1 {
		t.Fatalf("past the end: %d items, total %d; want [] and the filtered total >= 1", len(p.Items), p.Total)
	}

	status, hdr, body := doJSON(t, http.MethodGet, api+"/api/campaign-summaries?state=x&limit=0", nil, nil)
	e := assertError(t, status, hdr, body, http.StatusUnprocessableEntity, "validation_failed", true)
	got := make([]fieldWant, 0, len(e.Fields))
	for _, f := range e.Fields {
		got = append(got, fieldWant{f.Path, f.Code})
	}
	want := []fieldWant{{"query.state", "invalid_value"}, {"query.limit", "invalid_value"}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("422 fields mismatch (-want +got):\n%s", diff)
	}
}
