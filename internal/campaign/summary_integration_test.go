package campaign_test

// campaign.SummaryLister (REQ-DSH-01 C9) over the real Postgres of compose.test.yml (TEST_DATABASE_URL,
// scc_app) with the harness of service_integration_test.go / schedule_integration_test.go; skips
// under -short (testdb.App).
//
// The database is shared with the other integration packages, which create campaigns in parallel.
// So the tests assert exact values only about their own campaigns, and about each single
// SummaryPage only what holds inside one read (the order rule between consecutive items, the
// filter, total = the overview count of the filtered states, overview.campaigns.total = the sum of
// the seven states). The service clock starts at dshRef, far after every other test clock, so plans
// made here are never due for another test's scheduler; every plan is canceled in t.Cleanup.
//
// Not asserted here: C9's "overview is identical for different States/Order/Limit/Offset". That needs
// a database of its own; on the shared database parallel tests change the overview between two
// ListSummaries calls, so two responses cannot be compared exactly. Only the single-read rules above
// (C4, C5) are checked on every response.

import (
	"io"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/gulcint/smart-campaign-composer/internal/campaign"
	"github.com/gulcint/smart-campaign-composer/internal/llm"
	"github.com/gulcint/smart-campaign-composer/internal/postgres"
)

var dshRef = time.Date(2031, 3, 1, 9, 0, 0, 0, time.UTC)

// dshMultiLineBrief is a full brief with a second line and Turkish letters; summaries carry it whole.
const dshMultiLineBrief = "Ankara'daki altın üyelere hafta sonu kargo bedava duyurusu gönder.\nİkinci satır: kısa ve samimi olsun (ğüşİıöç)."

var dshAllStates = dshStates("draft", "audience_ready", "content_ready", "approved", "scheduled", "sending", "completed")

func newSummaryLister(t *testing.T, h *harness) campaign.SummaryLister {
	t.Helper()
	return campaign.NewSummaryLister(campaign.Deps{
		Store:  postgres.NewCampaignStore(h.pool),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func stateCount(c campaign.StateCounts, s campaign.State) int {
	switch string(s) {
	case "draft":
		return c.Draft
	case "audience_ready":
		return c.AudienceReady
	case "content_ready":
		return c.ContentReady
	case "approved":
		return c.Approved
	case "scheduled":
		return c.Scheduled
	case "sending":
		return c.Sending
	case "completed":
		return c.Completed
	}
	return -1
}

// listSummaries calls ListSummaries and checks every rule that holds inside one SummaryPage.
func listSummaries(t *testing.T, l campaign.SummaryLister, f campaign.SummaryFilter) campaign.SummaryPage {
	t.Helper()
	p, err := l.ListSummaries(t.Context(), f)
	if err != nil {
		t.Fatalf("ListSummaries(%+v) error = %v", f, err)
	}
	checkSummaryPage(t, f, p)
	return p
}

func checkSummaryPage(t *testing.T, f campaign.SummaryFilter, p campaign.SummaryPage) {
	t.Helper()
	// REQ-DSH-01 C6: limit/offset are echoed; items <= limit; Items is never nil.
	if p.Limit != f.Limit || p.Offset != f.Offset {
		t.Fatalf("SummaryPage limit/offset = %d/%d, want the echo %d/%d", p.Limit, p.Offset, f.Limit, f.Offset)
	}
	if p.Items == nil {
		t.Fatalf("SummaryPage.Items is nil for %+v, want a (possibly empty) slice", f)
	}
	if len(p.Items) > f.Limit {
		t.Fatalf("SummaryPage has %d items for limit %d", len(p.Items), f.Limit)
	}
	// REQ-DSH-01 C4: overview.campaigns.total is the sum of the seven states.
	oc := p.Overview.Campaigns
	if sum := oc.Draft + oc.AudienceReady + oc.ContentReady + oc.Approved + oc.Scheduled + oc.Sending + oc.Completed; oc.Total != sum {
		t.Fatalf("overview.campaigns = %+v, want total = sum of the seven states (%d)", oc, sum)
	}
	// REQ-DSH-01 C5: total and overview come from the same read.
	states := f.States
	if len(states) == 0 {
		states = dshAllStates
	}
	want := 0
	for _, s := range states {
		want += stateCount(oc, s)
	}
	if len(f.States) == 0 {
		want = oc.Total
	}
	if p.Total != want {
		t.Fatalf("SummaryPage.Total = %d for states %v, want %d from the overview of the same response %+v", p.Total, f.States, want, oc)
	}
	for i, it := range p.Items {
		// REQ-DSH-01 C6: only the filtered states.
		if len(f.States) > 0 && !slices.Contains(f.States, it.State) {
			t.Fatalf("items[%d] state %s is outside the filter %v", i, it.State, f.States)
		}
		// REQ-DSH-01 C2, C3: audience null only without a segment (draft); deliveries only once sending.
		if (string(it.State) == "draft") != (it.Audience == nil) {
			t.Fatalf("items[%d] state %s audience = %+v, want null exactly for draft", i, it.State, it.Audience)
		}
		sent := string(it.State) == "sending" || string(it.State) == "completed"
		if sent != (it.Deliveries != nil) {
			t.Fatalf("items[%d] state %s deliveries = %+v, want an object exactly for sending/completed", i, it.State, it.Deliveries)
		}
		if i == 0 {
			continue
		}
		prev := p.Items[i-1]
		if !summaryOrdered(f.Order, prev, it) {
			t.Fatalf("items[%d] (%s) and items[%d] (%s) break order %q", i-1, describeSummary(prev), i, describeSummary(it), f.Order)
		}
	}
}

// summaryOrdered: recent = created_at descending, ties id ascending; scheduled_at = ascending with
// nulls last, ties (and nulls) id ascending. The id order is the lower-case text order.
func summaryOrdered(order string, a, b campaign.Summary) bool {
	idLess := strings.ToLower(a.ID) < strings.ToLower(b.ID)
	switch order {
	case campaign.SummaryOrderScheduledAt:
		switch {
		case a.ScheduledAt == nil && b.ScheduledAt == nil:
			return idLess
		case a.ScheduledAt == nil:
			return false
		case b.ScheduledAt == nil:
			return true
		case a.ScheduledAt.Equal(*b.ScheduledAt):
			return idLess
		default:
			return a.ScheduledAt.Before(*b.ScheduledAt)
		}
	default:
		if a.CreatedAt.Equal(b.CreatedAt) {
			return idLess
		}
		return a.CreatedAt.After(b.CreatedAt)
	}
}

func describeSummary(s campaign.Summary) string {
	at := "null"
	if s.ScheduledAt != nil {
		at = s.ScheduledAt.Format(time.RFC3339Nano)
	}
	return "id " + s.ID + " created_at " + s.CreatedAt.Format(time.RFC3339Nano) + " scheduled_at " + at
}

// collectSummaries pages through f (limit 200) and returns the items by id (first sighting wins).
func collectSummaries(t *testing.T, l campaign.SummaryLister, f campaign.SummaryFilter) map[string]campaign.Summary {
	t.Helper()
	f.Limit = 200
	out := map[string]campaign.Summary{}
	for f.Offset = 0; ; f.Offset += f.Limit {
		p := listSummaries(t, l, f)
		for _, it := range p.Items {
			if _, ok := out[it.ID]; !ok {
				out[it.ID] = it
			}
		}
		if f.Offset+f.Limit >= p.Total || len(p.Items) == 0 {
			return out
		}
	}
}

// statusCounts reads campaign.StatusCounts as {total, queued, sending, sent, failed} (REQ-DSH-01 C3
// names the type and the five keys, not the Go field names): field names lower-cased, every field an
// integer.
func statusCounts(t *testing.T, c campaign.StatusCounts) map[string]int {
	t.Helper()
	v := reflect.ValueOf(c)
	out := map[string]int{}
	for i := range v.NumField() {
		f := v.Field(i)
		if !f.CanInt() {
			t.Fatalf("campaign.StatusCounts field %s is %s, want an integer count", v.Type().Field(i).Name, f.Kind())
		}
		out[strings.ToLower(v.Type().Field(i).Name)] = int(f.Int())
	}
	return out
}

// dbDeliveryCounts counts the campaign's delivery rows (both channels, both variants) the way the
// /status counts do: total and one count per status (REQ-SND: queued, sending, sent, failed); a and b
// are the rows of variant a and b (REQ-ABT-03 deliveries.variant).
func (h *harness) dbDeliveryCounts(t *testing.T, id string) (counts map[string]int, a, b int) {
	t.Helper()
	var total, queued, sending, sent, failed int
	err := h.pool.QueryRow(t.Context(), `SELECT count(*),
			count(*) FILTER (WHERE status::text = 'queued'),
			count(*) FILTER (WHERE status::text = 'sending'),
			count(*) FILTER (WHERE status::text = 'sent'),
			count(*) FILTER (WHERE status::text = 'failed'),
			count(*) FILTER (WHERE variant = 'a'),
			count(*) FILTER (WHERE variant = 'b')
		FROM deliveries WHERE campaign_id = $1`, id).Scan(&total, &queued, &sending, &sent, &failed, &a, &b)
	if err != nil {
		t.Fatalf("count deliveries of %s: %v", id, err)
	}
	return map[string]int{"total": total, "queued": queued, "sending": sending, "sent": sent, "failed": failed}, a, b
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

// REQ-DSH-01 C2, C3, C6, C9
// Each own campaign appears under the filter of its state with the values of campaign.Service.Get:
// brief in full (multi-line), state, scheduled_at, sending_started_at; audience = the segment's
// {matched, sms, email} (null for draft); deliveries null before sending, an object once sending
// equal field by field to the /status counts of an A/B send (variants a and b together), all zero
// for a send whose both channel audiences are empty. A state filter never returns an own
// campaign of another state; nil and zero-length States both mean "no filter".
func TestSummaryLister_ItemsMatchTheCampaigns(t *testing.T) {
	t.Parallel()
	h := newSchHarness(t, llm.NewMock(llm.MockOptions{}), dshRef, 0)
	l := newSummaryLister(t, h)
	bg := t.Context()

	draft, err := h.svc.Create(bg, dshMultiLineBrief)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	ready := h.audienceReady(t)
	schedID, schedV := h.schApproved(t)
	at := dshRef.Add(3 * time.Hour)
	h.schSchedule(t, schedID, schedV, at)
	// The sending campaign sends an A/B version, so its deliveries carry both variants (C3).
	sendingID := h.audienceReady(t)
	sendingV := h.edit(t, sendingID, campaign.ExpectedVersionNone, abVersionContent(false)).ID
	if _, err := h.svc.Approve(bg, sendingID, sendingV); err != nil {
		t.Fatalf("Approve(A/B version) error = %v", err)
	}
	if _, err := h.svc.Send(bg, sendingID, sendingV); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	doneID, doneV := h.schApprovedEmpty(t)
	if res, err := h.svc.Send(bg, doneID, doneV); err != nil || res.State != campaign.StateCompleted {
		t.Fatalf("Send(empty audience) = %+v, %v; want completed", res, err)
	}

	own := map[string]string{draft.ID: "draft", ready: "audience_ready", schedID: "scheduled", sendingID: "sending", doneID: "completed"}
	byState := map[string]map[string]campaign.Summary{}
	for _, s := range []string{"draft", "audience_ready", "scheduled", "sending", "completed"} {
		byState[s] = collectSummaries(t, l, campaign.SummaryFilter{States: dshStates(s), Order: campaign.SummaryOrderRecent})
	}
	for id, state := range own {
		for s, items := range byState {
			_, found := items[id]
			if found != (s == state) {
				t.Fatalf("own %s campaign %s found under state=%s: %v, want %v", state, id, s, found, s == state)
			}
		}
	}

	for id, state := range own {
		it := byState[state][id]
		c := h.get(t, id)
		if it.ID != id || it.State != c.State || it.Brief != c.Brief {
			t.Fatalf("summary of %s id/state/brief = %s/%s/%q, want %s/%s/%q", state, it.ID, it.State, it.Brief, id, c.State, c.Brief)
		}
		if !sameTime(it.ScheduledAt, c.ScheduledAt) || !sameTime(it.SendingStartedAt, c.SendingStartedAt) {
			t.Fatalf("summary of %s scheduled_at/sending_started_at = %v/%v, want %v/%v", state, it.ScheduledAt, it.SendingStartedAt, c.ScheduledAt, c.SendingStartedAt)
		}
		if state == "draft" {
			if it.Audience != nil || it.Deliveries != nil || it.ScheduledAt != nil || it.SendingStartedAt != nil || it.CompletedAt != nil {
				t.Fatalf("draft summary = %+v, want audience, deliveries and the three times null", it)
			}
			continue
		}
		a := c.Segment.Audience
		if it.Audience == nil || it.Audience.Matched != a.Matched || it.Audience.SMS != a.SMS || it.Audience.Email != a.Email {
			t.Fatalf("summary of %s audience = %+v, want {%d %d %d} of the segment", state, it.Audience, a.Matched, a.SMS, a.Email)
		}
	}
	if got := byState["draft"][draft.ID].Brief; got != dshMultiLineBrief {
		t.Fatalf("draft summary brief = %q, want the full multi-line brief %q", got, dshMultiLineBrief)
	}
	if got := byState["scheduled"][schedID]; got.ScheduledAt == nil || !got.ScheduledAt.Equal(at) || got.Deliveries != nil {
		t.Fatalf("scheduled summary scheduled_at/deliveries = %v/%+v, want %s/null", got.ScheduledAt, got.Deliveries, at.Format(time.RFC3339))
	}
	if got := byState["sending"][sendingID]; got.Deliveries == nil || got.SendingStartedAt == nil {
		t.Fatalf("sending summary deliveries/sending_started_at = %+v/%v, want both set", got.Deliveries, got.SendingStartedAt)
	}

	// REQ-DSH-01 C3, C9: the sending campaign's deliveries equal its /status counts field by field,
	// variant A and B rows counted together. docs/requirements.md names no Go call for /status (only
	// GET …/status), so the counts are read from the campaign's delivery rows, which are what /status
	// counts: total and one count per status. This harness runs no dispatcher (only campaign.Service;
	// the checks above already rely on the campaign staying sending), so the rows stay queued and the
	// counts are settled. To stay exact even if a worker of another package touched the rows, the rows
	// are counted right before and right after one ListSummaries: only an equal pair brackets the
	// summary's snapshot, and that pair is compared field by field. total never changes after the send
	// (one row per channel audience member), so it is checked on every attempt.
	wantTotal := h.audienceTotal(t, sendingID)
	settled := false
	for attempt := 0; attempt < 20 && !settled; attempt++ {
		before, a, b := h.dbDeliveryCounts(t, sendingID)
		got := collectSummaries(t, l, campaign.SummaryFilter{States: dshStates("sending", "completed"), Order: campaign.SummaryOrderRecent})[sendingID]
		after, _, _ := h.dbDeliveryCounts(t, sendingID)
		if a == 0 || b == 0 || a+b != wantTotal || before["total"] != wantTotal {
			t.Fatalf("setup: deliveries of the A/B send = %+v with %d of variant a and %d of b, want %d rows of both variants", before, a, b, wantTotal)
		}
		if got.ID != sendingID || got.Deliveries == nil {
			t.Fatalf("sending campaign %s missing under state=sending,completed or without deliveries: %+v", sendingID, got)
		}
		gotCounts := statusCounts(t, *got.Deliveries)
		if gotCounts["total"] != wantTotal {
			t.Fatalf("sending summary deliveries = %v, want total %d (variants a and b together)", gotCounts, wantTotal)
		}
		if cmp.Equal(before, after) {
			if diff := cmp.Diff(before, gotCounts); diff != "" {
				t.Fatalf("sending summary deliveries differ from the /status counts of its delivery rows (-status +summary):\n%s", diff)
			}
			settled = true
		}
	}
	if !settled {
		t.Fatalf("delivery rows of %s changed around each of 20 ListSummaries calls; no settled read to compare", sendingID)
	}
	if got := byState["completed"][doneID]; got.Deliveries == nil || *got.Deliveries != (campaign.StatusCounts{}) || got.CompletedAt == nil {
		t.Fatalf("empty-audience completed summary deliveries/completed_at = %+v/%v, want all zero and set", got.Deliveries, got.CompletedAt)
	}

	// nil and zero-length States are the same: no filter (checkSummaryPage: total = overview total).
	for name, states := range map[string][]campaign.State{"nil": nil, "zero-length": {}} {
		p := listSummaries(t, l, campaign.SummaryFilter{States: states, Order: campaign.SummaryOrderRecent, Limit: 1})
		if p.Total < len(own) {
			t.Fatalf("States %s: total = %d, want >= %d (no filter)", name, p.Total, len(own))
		}
	}
}

// REQ-DSH-01 C6, C9
// order=scheduled_at: two own plans at the same instant come in id order; every page of
// state=scheduled,draft is ascending by scheduled_at with the null (draft) ones last and ties by
// id; every page of the unfiltered recent order is created_at descending with ties by id
// (checkSummaryPage).
func TestSummaryLister_Order(t *testing.T) {
	t.Parallel()
	h := newSchHarness(t, llm.NewMock(llm.MockOptions{}), dshRef, 0)
	l := newSummaryLister(t, h)
	at := dshRef.Add(5 * time.Hour)
	var ids []string
	for range 2 {
		id, vid := h.schApproved(t)
		h.schSchedule(t, id, vid, at)
		ids = append(ids, id)
	}
	if _, err := h.svc.Create(t.Context(), h.m.ExampleBrief.Brief); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	slices.SortFunc(ids, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })

	f := campaign.SummaryFilter{States: dshStates("scheduled"), Order: campaign.SummaryOrderScheduledAt, Limit: 200}
	found := false
	for f.Offset = 0; !found; f.Offset += 150 { // overlapping pages: the pair is always inside one page
		p := listSummaries(t, l, f)
		pos := map[string]int{}
		for i, it := range p.Items {
			pos[it.ID] = i
		}
		i0, ok0 := pos[ids[0]]
		i1, ok1 := pos[ids[1]]
		if ok0 && ok1 {
			if i0 > i1 {
				t.Fatalf("plans at the same scheduled_at: %s at %d before %s at %d, want id ascending", ids[1], i1, ids[0], i0)
			}
			found = true
		}
		if !found && f.Offset+f.Limit >= p.Total {
			t.Fatalf("own scheduled campaigns %v not found together in state=scheduled order=scheduled_at", ids)
		}
	}

	listSummaries(t, l, campaign.SummaryFilter{States: dshStates("scheduled", "draft"), Order: campaign.SummaryOrderScheduledAt, Limit: 200})
	listSummaries(t, l, campaign.SummaryFilter{Order: campaign.SummaryOrderScheduledAt, Limit: 200})
	listSummaries(t, l, campaign.SummaryFilter{Order: campaign.SummaryOrderRecent, Limit: 200})
}

// REQ-DSH-01 C4, C5, C6, C9
// limit/offset are echoed (5/5); an offset at or beyond total gives an empty, non-nil Items with
// total still the filtered count; in every response total equals the overview count of the
// filtered states (state=scheduled, state=sending,completed, no filter) and overview counts the
// own campaigns at least once per state.
func TestSummaryLister_PagingAndSnapshot(t *testing.T) {
	t.Parallel()
	h := newSchHarness(t, llm.NewMock(llm.MockOptions{}), dshRef, 0)
	l := newSummaryLister(t, h)
	if _, err := h.svc.Create(t.Context(), h.m.ExampleBrief.Brief); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	id, vid := h.schApproved(t)
	h.schSchedule(t, id, vid, dshRef.Add(time.Hour))
	doneID, doneV := h.schApprovedEmpty(t)
	if _, err := h.svc.Send(t.Context(), doneID, doneV); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	filters := []struct {
		name string
		f    campaign.SummaryFilter
	}{
		{name: "no filter, limit 5 offset 5", f: campaign.SummaryFilter{Order: campaign.SummaryOrderRecent, Limit: 5, Offset: 5}},
		{name: "state=scheduled", f: campaign.SummaryFilter{States: dshStates("scheduled"), Order: campaign.SummaryOrderScheduledAt, Limit: 5}},
		{name: "state=sending,completed", f: campaign.SummaryFilter{States: dshStates("sending", "completed"), Order: campaign.SummaryOrderRecent, Limit: 5}},
		{name: "state=completed, limit 1", f: campaign.SummaryFilter{States: dshStates("completed"), Order: campaign.SummaryOrderRecent, Limit: 1}},
		{name: "offset far beyond the end", f: campaign.SummaryFilter{States: dshStates("scheduled"), Order: campaign.SummaryOrderRecent, Limit: 50, Offset: 1 << 30}},
	}
	for _, tc := range filters {
		t.Run(tc.name, func(t *testing.T) {
			p := listSummaries(t, l, tc.f)
			oc := p.Overview.Campaigns
			if oc.Draft < 1 || oc.Scheduled < 1 || oc.Completed < 1 {
				t.Fatalf("overview.campaigns = %+v, want draft, scheduled and completed >= 1 (own campaigns)", oc)
			}
			if tc.f.Offset >= p.Total && len(p.Items) != 0 {
				t.Fatalf("offset %d >= total %d but %d items, want none", tc.f.Offset, p.Total, len(p.Items))
			}
			if tc.f.Offset < p.Total && len(p.Items) == 0 {
				t.Fatalf("offset %d < total %d but no items", tc.f.Offset, p.Total)
			}
		})
	}
}
