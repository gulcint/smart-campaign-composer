package httpapi_test

// GET /api/campaign-summaries (REQ-DSH-01) with a fake campaign.SummaryLister given as
// httpapi.Deps.Summaries: the CampaignSummaryPage JSON shape, the query reaching ListSummaries as a
// campaign.SummaryFilter, 422 before ListSummaries, 500 on a lister error or a missing lister, 405
// for other methods, and the route in httpapi.Routes().

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/gulcint/smart-campaign-composer/internal/campaign"
	"github.com/gulcint/smart-campaign-composer/internal/clock"
	"github.com/gulcint/smart-campaign-composer/internal/httpapi"
	"github.com/gulcint/smart-campaign-composer/internal/llm"
)

const summariesPath = "/api/campaign-summaries"

// summaryLister records every ListSummaries call and answers page / err.
type summaryLister struct {
	mu    sync.Mutex
	page  campaign.SummaryPage
	err   error
	calls []campaign.SummaryFilter
}

func (s *summaryLister) ListSummaries(_ context.Context, f campaign.SummaryFilter) (campaign.SummaryPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, f)
	if s.err != nil {
		return campaign.SummaryPage{}, s.err
	}
	return s.page, nil
}

func (s *summaryLister) filters() []campaign.SummaryFilter {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]campaign.SummaryFilter(nil), s.calls...)
}

var _ campaign.SummaryLister = (*summaryLister)(nil)

func newSummaryRouter(t *testing.T, l campaign.SummaryLister) http.Handler {
	t.Helper()
	return httpapi.New(httpapi.Deps{
		Service:     fakeService{},
		Summaries:   l,
		LLMProvider: llm.ProviderMock,
		DB:          okPinger(),
		Metrics:     http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "# fake\n") }),
		Clock:       clock.NewFake(refTime),
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

const (
	sumCompletedID = "7d0c1f9e-3b8a-4c52-9e41-2f6a8d1b5c30"
	sumDraftID     = "0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
	sumBrief       = "Son 30 günde sepetine ürün ekleyip alışveriş yapmayan İstanbul'daki kullanıcılara %30 indirim hatırlatması gönder.\nSamimi ama kısa olsun."
)

var (
	sumCreated = time.Date(2026, 11, 2, 9, 12, 40, 3154000, time.UTC)
	sumStarted = time.Date(2026, 11, 2, 9, 15, 4, 120511000, time.UTC)
	sumDone    = time.Date(2026, 11, 2, 9, 15, 21, 904377000, time.UTC)
)

func samplePage() campaign.SummaryPage {
	return campaign.SummaryPage{
		Items: []campaign.Summary{
			{
				ID: sumCompletedID, Brief: sumBrief, State: campaign.StateCompleted,
				Audience:         &campaign.SummaryAudience{Matched: 412, SMS: 342, Email: 361},
				SendingStartedAt: ptrTime(sumStarted), CompletedAt: ptrTime(sumDone),
				CreatedAt: sumCreated, UpdatedAt: sumDone,
				Deliveries: &campaign.StatusCounts{},
			},
			{
				ID: sumDraftID, Brief: "Taslak", State: campaign.State("draft"),
				CreatedAt: sumCreated, UpdatedAt: sumCreated,
			},
		},
		Total: 9, Limit: 5, Offset: 0,
		Overview: campaign.Overview{
			Campaigns: campaign.StateCounts{Total: 9, Draft: 2, AudienceReady: 1, ContentReady: 1, Approved: 0, Scheduled: 2, Sending: 0, Completed: 3},
		},
	}
}

var zeroCountsJSON = map[string]any{"total": float64(0), "queued": float64(0), "sending": float64(0), "sent": float64(0), "failed": float64(0)}

func assertTimeJSON(t *testing.T, name string, got any, want *time.Time) {
	t.Helper()
	if want == nil {
		if got != nil {
			t.Fatalf("%s = %v, want null", name, got)
		}
		return
	}
	s, _ := got.(string)
	ts, err := time.Parse(time.RFC3339Nano, s)
	if err != nil || !ts.Equal(*want) {
		t.Fatalf("%s = %v, want %s", name, got, want.Format(time.RFC3339Nano))
	}
}

// REQ-DSH-01 C1, C2, C3, C4, C9
// 200 with a CampaignSummaryPage: keys exactly items, total, limit, offset, overview; each item has
// exactly the ten CampaignSummary keys (no segment, content, approval, contact data); audience is
// exactly {matched, sms, email} or null; deliveries is exactly {total, queued, sending, sent,
// failed} (no by_variant) or null; overview.campaigns has the eight keys and overview.deliveries the
// five. The brief is not shortened.
func TestListCampaignSummaries_ResponseShape(t *testing.T) {
	t.Parallel()
	l := &summaryLister{page: samplePage()}
	rec := do(t, newSummaryRouter(t, l), http.MethodGet, summariesPath)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	body := decodeMap(t, rec.Body.Bytes())
	if diff := cmp.Diff([]string{"items", "limit", "offset", "overview", "total"}, keys(body)); diff != "" {
		t.Fatalf("CampaignSummaryPage keys mismatch (-want +got):\n%s", diff)
	}
	if body["total"] != float64(9) || body["limit"] != float64(5) || body["offset"] != float64(0) {
		t.Fatalf("total/limit/offset = %v/%v/%v, want 9/5/0", body["total"], body["limit"], body["offset"])
	}
	items, ok := body["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("items = %v, want an array of 2", body["items"])
	}
	itemKeys := []string{"audience", "brief", "completed_at", "created_at", "deliveries", "id", "scheduled_at", "sending_started_at", "state", "updated_at"}
	for i, raw := range items {
		it, _ := raw.(map[string]any)
		if diff := cmp.Diff(itemKeys, keys(it)); diff != "" {
			t.Fatalf("items[%d] CampaignSummary keys mismatch (-want +got):\n%s", i, diff)
		}
	}
	done := items[0].(map[string]any)
	if done["id"] != sumCompletedID || done["brief"] != sumBrief || done["state"] != "completed" {
		t.Fatalf("items[0] id/brief/state = %v/%q/%v, want %s/%q/completed", done["id"], done["brief"], done["state"], sumCompletedID, sumBrief)
	}
	wantAudience := map[string]any{"matched": float64(412), "sms": float64(342), "email": float64(361)}
	if diff := cmp.Diff(wantAudience, done["audience"]); diff != "" {
		t.Fatalf("items[0].audience mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(zeroCountsJSON, done["deliveries"]); diff != "" {
		t.Fatalf("items[0].deliveries mismatch (-want +got):\n%s", diff)
	}
	assertTimeJSON(t, "items[0].scheduled_at", done["scheduled_at"], nil)
	assertTimeJSON(t, "items[0].sending_started_at", done["sending_started_at"], &sumStarted)
	assertTimeJSON(t, "items[0].completed_at", done["completed_at"], &sumDone)
	assertTimeJSON(t, "items[0].created_at", done["created_at"], &sumCreated)
	assertTimeJSON(t, "items[0].updated_at", done["updated_at"], &sumDone)

	draft := items[1].(map[string]any)
	for _, k := range []string{"audience", "deliveries", "scheduled_at", "sending_started_at", "completed_at"} {
		if draft[k] != nil {
			t.Fatalf("draft items[1].%s = %v, want null", k, draft[k])
		}
	}

	ov, _ := body["overview"].(map[string]any)
	if diff := cmp.Diff([]string{"campaigns", "deliveries"}, keys(ov)); diff != "" {
		t.Fatalf("overview keys mismatch (-want +got):\n%s", diff)
	}
	wantCampaigns := map[string]any{"total": float64(9), "draft": float64(2), "audience_ready": float64(1), "content_ready": float64(1), "approved": float64(0), "scheduled": float64(2), "sending": float64(0), "completed": float64(3)}
	if diff := cmp.Diff(wantCampaigns, ov["campaigns"]); diff != "" {
		t.Fatalf("overview.campaigns mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(zeroCountsJSON, ov["deliveries"]); diff != "" {
		t.Fatalf("overview.deliveries mismatch (-want +got):\n%s", diff)
	}
}

// REQ-DSH-01 C1
// items is always an array, also when the lister returns no items (nil or empty slice).
func TestListCampaignSummaries_EmptyItemsIsAnArray(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		items []campaign.Summary
	}{
		{name: "nil items", items: nil},
		{name: "empty items", items: []campaign.Summary{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := samplePage()
			p.Items, p.Offset = tc.items, 400
			rec := do(t, newSummaryRouter(t, &summaryLister{page: p}), http.MethodGet, summariesPath+"?offset=400")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
			}
			items, ok := decodeMap(t, rec.Body.Bytes())["items"].([]any)
			if !ok || len(items) != 0 {
				t.Fatalf("items = %s, want []", rec.Body.String())
			}
		})
	}
}

// REQ-DSH-01 C6, C9
// The query reaches ListSummaries once as the parsed campaign.SummaryFilter (defaults, comma and
// %2C lists, duplicates once, first value of a repeated parameter).
func TestListCampaignSummaries_QueryReachesTheLister(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		query string
		want  campaign.SummaryFilter
	}{
		{name: "no parameter", query: "", want: campaign.SummaryFilter{Order: campaign.SummaryOrderRecent, Limit: 50}},
		{name: "scheduled section of the panel", query: "?state=scheduled&order=scheduled_at&limit=5&offset=0",
			want: campaign.SummaryFilter{States: dshStatesHTTP("scheduled"), Order: campaign.SummaryOrderScheduledAt, Limit: 5}},
		{name: "sent section with an encoded comma", query: "?state=sending%2Ccompleted&order=recent&limit=5&offset=5",
			want: campaign.SummaryFilter{States: dshStatesHTTP("sending", "completed"), Order: campaign.SummaryOrderRecent, Limit: 5, Offset: 5}},
		{name: "duplicate state once", query: "?state=completed,completed", want: campaign.SummaryFilter{States: dshStatesHTTP("completed"), Order: campaign.SummaryOrderRecent, Limit: 50}},
		{name: "first value of a repeated limit", query: "?limit=7&limit=0", want: campaign.SummaryFilter{Order: campaign.SummaryOrderRecent, Limit: 7}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			l := &summaryLister{page: samplePage()}
			rec := do(t, newSummaryRouter(t, l), http.MethodGet, summariesPath+tc.query)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
			}
			got := l.filters()
			if len(got) != 1 {
				t.Fatalf("ListSummaries calls = %d, want 1", len(got))
			}
			if diff := cmp.Diff(tc.want, got[0], cmpopts.EquateEmpty()); diff != "" {
				t.Fatalf("SummaryFilter mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func dshStatesHTTP(ss ...string) []campaign.State {
	out := make([]campaign.State, 0, len(ss))
	for _, s := range ss {
		out = append(out, campaign.State(s))
	}
	return out
}

// REQ-DSH-01 C7, C9
// Invalid parameters: 422 validation_failed with every invalid parameter in one response, each
// invalid_value, ordered query.state, query.order, query.limit, query.offset; ListSummaries is not
// called.
func TestListCampaignSummaries_InvalidQueryIs422(t *testing.T) {
	t.Parallel()
	st := fieldPC{"query.state", "invalid_value"}
	or := fieldPC{"query.order", "invalid_value"}
	li := fieldPC{"query.limit", "invalid_value"}
	of := fieldPC{"query.offset", "invalid_value"}
	tests := []struct {
		name  string
		query string
		want  []fieldPC
	}{
		{name: "state=x&limit=0", query: "?state=x&limit=0", want: []fieldPC{st, li}},
		{name: "all four in reverse order", query: "?offset=-1&limit=201&order=oldest&state=Sending", want: []fieldPC{st, or, li, of}},
		{name: "state= (empty)", query: "?state=", want: []fieldPC{st}},
		{name: "state=sending,%20completed", query: "?state=sending,%20completed", want: []fieldPC{st}},
		{name: "order= (empty)", query: "?order=", want: []fieldPC{or}},
		{name: "limit=abc", query: "?limit=abc", want: []fieldPC{li}},
		{name: "offset=abc", query: "?offset=abc", want: []fieldPC{of}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			l := &summaryLister{page: samplePage()}
			rec := do(t, newSummaryRouter(t, l), http.MethodGet, summariesPath+tc.query)
			e := assertErrorBody(t, rec, http.StatusUnprocessableEntity, "validation_failed", true)
			if diff := cmp.Diff(tc.want, fieldsPC(e)); diff != "" {
				t.Fatalf("fields mismatch (-want +got):\n%s", diff)
			}
			if n := len(l.filters()); n != 0 {
				t.Fatalf("ListSummaries calls = %d, want 0 (refused before the lister)", n)
			}
		})
	}
}

// REQ-DSH-01 C9, C8
// A ListSummaries error is 500 internal_error without the database error text; a nil
// Deps.Summaries answers 500.
func TestListCampaignSummaries_ServerErrors(t *testing.T) {
	t.Parallel()
	dbText := `ERROR: relation "campaigns" does not exist (SQLSTATE 42P01)`
	t.Run("lister error", func(t *testing.T) {
		t.Parallel()
		l := &summaryLister{err: errors.New(dbText)}
		rec := do(t, newSummaryRouter(t, l), http.MethodGet, summariesPath)
		assertErrorBody(t, rec, http.StatusInternalServerError, "internal_error", false)
		if strings.Contains(rec.Body.String(), "SQLSTATE") || strings.Contains(rec.Body.String(), "relation") {
			t.Fatalf("500 body leaks the database error: %s", rec.Body.String())
		}
	})
	t.Run("nil Summaries", func(t *testing.T) {
		t.Parallel()
		rec := do(t, newSummaryRouter(t, nil), http.MethodGet, summariesPath)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500 without a SummaryLister; body: %s", rec.Code, rec.Body.String())
		}
	})
}

// REQ-DSH-01 C8
// Methods other than GET are 405 method_not_allowed and never reach the lister.
func TestListCampaignSummaries_OtherMethodsAre405(t *testing.T) {
	t.Parallel()
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			l := &summaryLister{page: samplePage()}
			rec := send(t, newSummaryRouter(t, l), method, summariesPath, []byte(`{}`), nil)
			assertErrorBody(t, rec, http.StatusMethodNotAllowed, "method_not_allowed", false)
			if n := len(l.filters()); n != 0 {
				t.Fatalf("ListSummaries calls = %d, want 0", n)
			}
		})
	}
}

// REQ-DSH-01 C9
// httpapi.Routes() lists GET /api/campaign-summaries.
func TestRoutes_IncludeCampaignSummaries(t *testing.T) {
	t.Parallel()
	got := fmt.Sprintf("%+v", httpapi.Routes())
	if !strings.Contains(got, summariesPath) || !strings.Contains(got, http.MethodGet) {
		t.Fatalf("httpapi.Routes() = %s, want it to contain {GET %s}", got, summariesPath)
	}
}
