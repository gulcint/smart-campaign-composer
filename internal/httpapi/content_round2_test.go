package httpapi_test

// P4 round 2 handler tests (independent review of PR #50): canonical UUIDs at the HTTP boundary
// (D47 a), editContent check order with a structurally invalid body (D47 c), getContentVersion order,
// and "after HTTP 200 only events" (sse-events.md §0).

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/gulcint/smart-campaign-composer/internal/campaign"
	"github.com/gulcint/smart-campaign-composer/internal/clock"
	"github.com/gulcint/smart-campaign-composer/internal/composer"
	"github.com/gulcint/smart-campaign-composer/internal/llm"
)

// idService records the campaign id every content operation receives.
type idService struct {
	*contentService
	mu  sync.Mutex
	ids []string
}

func (s *idService) record(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ids = append(s.ids, id)
}

func (s *idService) seen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.ids...)
}

func (s *idService) StartContentGeneration(ctx context.Context, id string) (campaign.ContentRun, error) {
	s.record(id)
	return s.contentService.StartContentGeneration(ctx, id)
}

func (s *idService) EditContent(ctx context.Context, id, expected string, raw []byte) (campaign.ContentVersion, error) {
	s.record(id)
	return s.contentService.EditContent(ctx, id, expected, raw)
}

func (s *idService) ListContentVersions(ctx context.Context, id string) ([]campaign.ContentVersion, error) {
	s.record(id)
	return s.contentService.ListContentVersions(ctx, id)
}

func (s *idService) GetContentVersion(ctx context.Context, id, vid string) (campaign.ContentVersion, error) {
	s.record(id)
	return s.contentService.GetContentVersion(ctx, id, vid)
}

func (s *idService) Approve(ctx context.Context, id, vid string) (campaign.Campaign, error) {
	s.record(id)
	return s.contentService.Approve(ctx, id, vid)
}

func (s *idService) Preview(ctx context.Context, id string, q campaign.PreviewQuery) (campaign.Preview, error) {
	s.record(id)
	return s.contentService.Preview(ctx, id, q)
}

var _ campaign.Service = (*idService)(nil)

// mixedCase upper-cases every other hex letter: "6b0e1f8a-..." => "6B0e1F8a-...".
func mixedCase(s string) string {
	var b strings.Builder
	up := true
	for _, r := range s {
		if r >= 'a' && r <= 'f' {
			if up {
				r -= 'a' - 'A'
			}
			up = !up
		}
		b.WriteRune(r)
	}
	return b.String()
}

// REQ-APR-01, REQ-APR-03, REQ-CNT-04 (D47 a, README §5.4 step 1)
// An upper-case (or mixed-case) UUID is valid and is converted to the lower-case canonical form at
// the HTTP boundary: the campaign id in the path, the version id in the getContentVersion path, the
// approve body's content_version_id and previewContent's content_version_id query all reach the
// service in lower case, so they behave exactly like the lower-case form.
func TestContentRoutes_UpperCaseIDsReachTheServiceCanonical(t *testing.T) {
	t.Parallel()
	for _, variant := range []struct {
		name string
		f    func(string) string
	}{{"upper case", strings.ToUpper}, {"mixed case", mixedCase}} {
		cid, vid := variant.f(campID), variant.f(versionID)
		if cid == campID || vid == versionID {
			t.Fatalf("test setup: %s did not change the ids", variant.name)
		}
		tests := []struct {
			name    string
			method  string
			path    string
			body    []byte
			headers map[string]string
			status  int
			check   func(t *testing.T, s *idService)
		}{
			{
				name: "generateContent", method: http.MethodPost, path: "/api/campaigns/" + cid + "/content", status: http.StatusOK,
			},
			{
				name: "editContent", method: http.MethodPost, path: "/api/campaigns/" + cid + "/content-versions",
				body: jsonBody(t, sampleContent()), headers: map[string]string{"X-Expected-Version": versionID}, status: http.StatusCreated,
				check: func(t *testing.T, s *idService) {
					t.Helper()
					if calls := s.editCalls(); len(calls) != 1 || calls[0].id != campID || calls[0].expected != versionID {
						t.Fatalf("EditContent calls = %+v, want id %s, expected %s", calls, campID, versionID)
					}
				},
			},
			{
				name: "listContentVersions", method: http.MethodGet, path: "/api/campaigns/" + cid + "/content-versions", status: http.StatusOK,
			},
			{
				name: "getContentVersion", method: http.MethodGet, path: "/api/campaigns/" + cid + "/content-versions/" + vid, status: http.StatusOK,
			},
			{
				name: "approveContent", method: http.MethodPost, path: "/api/campaigns/" + cid + "/approve",
				body: []byte(`{"content_version_id":"` + vid + `"}`), status: http.StatusOK,
				check: func(t *testing.T, s *idService) {
					t.Helper()
					if calls := s.approveCalls(); len(calls) != 1 || calls[0] != versionID {
						t.Fatalf("Approve versions = %v, want [%s]", calls, versionID)
					}
				},
			},
			{
				name: "previewContent", method: http.MethodGet, path: "/api/campaigns/" + cid + "/preview?content_version_id=" + vid, status: http.StatusOK,
				check: func(t *testing.T, s *idService) {
					t.Helper()
					want := []campaign.PreviewQuery{{ContentVersionID: versionID}}
					if diff := cmp.Diff(want, s.previewCalls()); diff != "" {
						t.Fatalf("PreviewQuery mismatch (-want +got):\n%s", diff)
					}
				},
			},
		}
		for _, tc := range tests {
			t.Run(variant.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				s := &idService{contentService: newContentService()}
				s.run = &replayRun{before: []composer.Event{composer.ValidatedEvent{Attempt: 1, ContentVersionID: versionID, VersionNumber: 1, Content: sampleContent()}}}
				h := newContentRouter(t, s, llm.ProviderMock, clock.NewFake(refTime))
				rec := send(t, h, tc.method, tc.path, tc.body, tc.headers)
				if rec.Code != tc.status {
					t.Fatalf("status = %d, want %d; body: %s", rec.Code, tc.status, rec.Body.String())
				}
				if diff := cmp.Diff([]string{campID}, s.seen()); diff != "" {
					t.Fatalf("campaign ids received by the service mismatch (-want +got):\n%s", diff)
				}
				if tc.check != nil {
					tc.check(t, s)
				}
			})
		}
	}
}

// REQ-APR-01, REQ-APR-03 (openapi ExpectedVersion pattern, README §5.4 step 2b)
// The X-Expected-Version header is NOT canonicalized: its pattern is lower-case only, so an
// upper-case UUID there is 422 invalid_value even when the campaign id in the path is upper case.
// (openapi.yaml wins over D47's general sentence; see the report.)
func TestEditContent_UpperCaseExpectedVersionStaysInvalid(t *testing.T) {
	t.Parallel()
	svc := newContentService()
	h := newContentRouter(t, svc, llm.ProviderMock, clock.NewFake(refTime))
	rec := send(t, h, http.MethodPost, "/api/campaigns/"+strings.ToUpper(campID)+"/content-versions", jsonBody(t, sampleContent()), map[string]string{"X-Expected-Version": strings.ToUpper(versionID)})
	e := assertErrorBody(t, rec, http.StatusUnprocessableEntity, "validation_failed", true)
	if !hasField(e, "header.X-Expected-Version", "invalid_value") {
		t.Fatalf("error.fields = %v, want header.X-Expected-Version invalid_value", e["fields"])
	}
	if n := len(svc.editCalls()); n != 0 {
		t.Fatalf("EditContent calls = %d, want 0", n)
	}
}

// REQ-APR-01, REQ-APR-02, REQ-APR-03 (D47 c, README §5.4 step 3 exception, openapi editContent)
// content.Parse runs inside the use case, so a structurally invalid edit body ({"sms":{}}, valid
// JSON) is handed to the service verbatim and the use-case order decides: 404 campaign_not_found,
// 409 generation_in_progress / invalid_state / content_edit_conflict, then 422. Only a missing or
// malformed X-Expected-Version (and a malformed path id) are answered by the handler first.
func TestEditContent_StructurallyInvalidBodyFollowsTheUseCaseOrder(t *testing.T) {
	t.Parallel()
	body := []byte(`{"sms":{}}`)
	n := 4
	cur := version2ID
	tests := []struct {
		name      string
		path      string
		header    *string
		svcErr    error
		status    int
		code      string
		field     *[2]string
		wantCalls int
		conflict  bool
	}{
		{name: "stale base is a conflict, not a 422", header: strPtr(versionID), svcErr: &campaign.EditConflictError{CurrentID: &cur, CurrentVersionNumber: &n}, status: 409, conflict: true, wantCalls: 1},
		{name: "unknown campaign is 404, not a 422", header: strPtr("none"), svcErr: campaign.ErrCampaignNotFound, status: 404, code: "campaign_not_found", wantCalls: 1},
		{name: "generation running is 409, not a 422", header: strPtr(versionID), svcErr: campaign.ErrGenerationInProgress, status: 409, code: "generation_in_progress", wantCalls: 1},
		{name: "draft campaign is 409 invalid_state, not a 422", header: strPtr("none"), svcErr: campaign.ErrInvalidState, status: 409, code: "invalid_state", wantCalls: 1},
		{name: "malformed header wins over everything", header: strPtr("v1"), svcErr: campaign.ErrCampaignNotFound, status: 422, code: "validation_failed", field: &[2]string{"header.X-Expected-Version", "invalid_value"}},
		{name: "missing header wins over everything", header: nil, svcErr: campaign.ErrCampaignNotFound, status: 422, code: "validation_failed", field: &[2]string{"header.X-Expected-Version", "schema_violation"}},
		{name: "malformed path id wins over a malformed header", path: "/api/campaigns/not-a-uuid/content-versions", header: strPtr("v1"), status: 404, code: "campaign_not_found"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := newContentService()
			svc.editErr = tc.svcErr
			h := newContentRouter(t, svc, llm.ProviderMock, clock.NewFake(refTime))
			path := tc.path
			if path == "" {
				path = "/api/campaigns/" + campID + "/content-versions"
			}
			headers := map[string]string{}
			if tc.header != nil {
				headers["X-Expected-Version"] = *tc.header
			}
			rec := send(t, h, http.MethodPost, path, body, headers)
			if tc.conflict {
				assertConflictBody(t, rec, version2ID, float64(4))
			} else {
				e := assertErrorBody(t, rec, tc.status, tc.code, tc.field != nil)
				if tc.field != nil && !hasField(e, tc.field[0], tc.field[1]) {
					t.Fatalf("error.fields = %v, want %v", e["fields"], *tc.field)
				}
			}
			calls := svc.editCalls()
			if len(calls) != tc.wantCalls {
				t.Fatalf("EditContent calls = %d, want %d", len(calls), tc.wantCalls)
			}
			if tc.wantCalls == 1 {
				var got, want any
				_ = json.Unmarshal(calls[0].raw, &got)
				_ = json.Unmarshal(body, &want)
				if diff := cmp.Diff(want, got); diff != "" {
					t.Fatalf("EditContent received a different body (-want +got):\n%s", diff)
				}
			}
		})
	}
}

// REQ-APR-03 (README §5.4 steps 1 and 4, openapi getContentVersion)
// A malformed version id on an UNKNOWN campaign is 404 campaign_not_found, never
// content_version_not_found: the campaign is checked first.
func TestGetContentVersion_MalformedVersionOnUnknownCampaign(t *testing.T) {
	t.Parallel()
	h := newContentRouter(t, newContentService(), llm.ProviderMock, clock.NewFake(refTime))
	for _, vid := range []string{"not-a-uuid", "v3", "123"} {
		t.Run(vid, func(t *testing.T) {
			t.Parallel()
			rec := send(t, h, http.MethodGet, "/api/campaigns/"+unknownID+"/content-versions/"+vid, nil, nil)
			assertErrorBody(t, rec, http.StatusNotFound, "campaign_not_found", false)
		})
	}
}

// failingRun emits its events, then fails: it returns err, or panics with panicValue when set.
type failingRun struct {
	events     []composer.Event
	err        error
	panicValue any
}

func (r *failingRun) Stream(_ context.Context, emit func(composer.Event) error) error {
	for _, ev := range r.events {
		if err := emit(ev); err != nil {
			return err
		}
	}
	if r.panicValue != nil {
		panic(r.panicValue)
	}
	return r.err
}

// REQ-CNT-04, REQ-AI-01 (sse-events.md §0, §1.2, §1.3)
// After HTTP 200 a failure inside the run (an error or a panic after the first event) is reported
// only through events: the status stays 200 with the SSE headers, the body stays well-formed SSE
// (event + one compact JSON data line per block), no JSON error body and no second status is
// appended, and the only event that may follow is a terminal invalid (final true,
// content_version_id null, internal_error at path "").
func TestGenerateContent_FailureAfter200IsReportedOnlyAsEvents(t *testing.T) {
	t.Parallel()
	first := composer.DeltaEvent{Attempt: 1, Channel: "sms", Field: "body", Text: "Merhaba"}
	tests := []struct {
		name string
		run  *failingRun
	}{
		{name: "run returns an error", run: &failingRun{events: []composer.Event{first}, err: errors.New(`ERROR: deadlock detected (SQLSTATE 40P01)`)}},
		{name: "run panics", run: &failingRun{events: []composer.Event{first}, panicValue: "boom"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := newContentService()
			svc.run = tc.run
			srv := httptest.NewUnstartedServer(newContentRouter(t, svc, llm.ProviderMock, clock.NewFake(refTime)))
			srv.Config.ErrorLog = log.New(io.Discard, "", 0)
			srv.Start()
			t.Cleanup(srv.Close)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/api/campaigns/"+campID+"/content", nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := srv.Client().Do(req)
			if err != nil {
				t.Fatalf("POST content: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			// A panic may abort the connection: what arrived before that is what the client sees.
			raw, _ := io.ReadAll(resp.Body)
			body := string(raw)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200; body: %s", resp.StatusCode, body)
			}
			assertSSEHeaders(t, resp.Header)
			if !strings.HasSuffix(body, "\n\n") {
				t.Fatalf("body does not end at a block boundary: %q", body)
			}
			for _, l := range strings.Split(body, "\n") {
				if strings.HasPrefix(strings.TrimSpace(l), "{") {
					t.Fatalf("body contains a bare JSON line %q (an error body appended after 200)", l)
				}
			}
			var events []sseBlock
			for _, b := range parseSSE(t, body) {
				if len(b.lines) == 1 && strings.HasPrefix(b.lines[0], ":") {
					continue // heartbeat comment
				}
				if len(b.lines) != 2 || b.event == "" || b.data == "" {
					t.Fatalf("block %q is not an event line plus one data line", b.lines)
				}
				var m map[string]any
				if err := json.Unmarshal([]byte(b.data), &m); err != nil {
					t.Fatalf("data %q is not a JSON object: %v", b.data, err)
				}
				if _, ok := m["error"]; ok {
					t.Fatalf("event data %s carries an error body", b.data)
				}
				events = append(events, b)
			}
			if len(events) == 0 || events[0].event != "delta" {
				t.Fatalf("events = %+v, want the delta first", events)
			}
			jsonEqual(t, events[0].data, first)
			if len(events) > 2 {
				t.Fatalf("got %d events after the failure, want at most one terminal event", len(events)-1)
			}
			if len(events) == 2 {
				last := events[1]
				if last.event != "invalid" {
					t.Fatalf("event after the failure = %q, want invalid", last.event)
				}
				var inv struct {
					Final            bool    `json:"final"`
					ContentVersionID *string `json:"content_version_id"`
					Errors           []struct {
						Path string `json:"path"`
						Code string `json:"code"`
					} `json:"errors"`
				}
				if err := json.Unmarshal([]byte(last.data), &inv); err != nil {
					t.Fatalf("decode invalid: %v", err)
				}
				if !inv.Final || inv.ContentVersionID != nil || len(inv.Errors) == 0 || inv.Errors[0].Path != "" || inv.Errors[0].Code != "internal_error" {
					t.Fatalf("terminal event = %s, want final invalid, null version, internal_error at path \"\"", last.data)
				}
			}
			if strings.Contains(body, "SQLSTATE") || strings.Contains(body, "deadlock") || strings.Contains(body, "boom") {
				t.Fatalf("body leaks the internal failure text: %q", body)
			}
		})
	}
}
