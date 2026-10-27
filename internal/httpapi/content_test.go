package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/gulcint/smart-campaign-composer/internal/campaign"
	"github.com/gulcint/smart-campaign-composer/internal/clock"
	"github.com/gulcint/smart-campaign-composer/internal/composer"
	"github.com/gulcint/smart-campaign-composer/internal/content"
	"github.com/gulcint/smart-campaign-composer/internal/httpapi"
	"github.com/gulcint/smart-campaign-composer/internal/llm"
	"github.com/gulcint/smart-campaign-composer/internal/segment"
	"github.com/gulcint/smart-campaign-composer/internal/validation"
)

const (
	versionID  = "0b8f6c2e-6d0a-4a47-9c39-0f2d3b8e1a11"
	version2ID = "5c1d7e3a-2b4f-4c6d-8e9f-0a1b2c3d4e5f"
	customerID = "2b34d38c-486c-53d2-8fba-c448b990e359"
	okSMSText  = "Merhaba {{first_name}}, sepetindeki ürünler seni bekliyor! Bugüne özel %30 indirimle alışverişini tamamla: example.com/sepet"
)

func ipt(n int) *int { return &n }

func sampleContent() content.Content {
	return content.Content{SMS: content.SMS{Body: okSMSText}, Email: content.Email{Subject: "Sepetin seni bekliyor: %30 indirim fırsatı", Body: "Merhaba {{first_name}},\n\nİndirim seni bekliyor."}}
}

func sampleVersion() campaign.ContentVersion {
	return campaign.ContentVersion{
		ID: versionID, CampaignID: campID, VersionNumber: 3, SegmentRevision: 1,
		Status: campaign.ContentValidated, Source: campaign.SourceEdit, Content: sampleContent(),
		WorstCase: &content.WorstCase{SMSRunes: 160, SubjectRunes: 42}, ValidationErrors: []validation.FieldError{},
		CreatedAt: refTime,
	}
}

func sampleVersionJSON() map[string]any {
	return map[string]any{
		"id": versionID, "campaign_id": campID, "version_number": float64(3), "segment_revision": float64(1),
		"status": "validated", "source": "edit",
		"content": map[string]any{
			"sms":   map[string]any{"body": okSMSText},
			"email": map[string]any{"subject": "Sepetin seni bekliyor: %30 indirim fırsatı", "body": "Merhaba {{first_name}},\n\nİndirim seni bekliyor."},
		},
		"worst_case":        map[string]any{"sms_runes": float64(160), "subject_runes": float64(42)},
		"validation_errors": []any{},
		"llm_attempts":      nil,
		"created_at":        refTimeText,
	}
}

func approvedCampaign() campaign.Campaign {
	v := sampleVersion()
	return campaign.Campaign{
		ID: campID, Brief: exampleBrief, State: campaign.StateApproved,
		Segment: &campaign.Segment{
			Rule: json.RawMessage(exampleRule), Audience: segment.Audience{Matched: 584, SMS: 350, Email: 368},
			LongestValues: content.Longest{FirstName: 50, LastName: 8, City: 8}, ReferenceTime: refTime, Revision: 1, LLMAttempts: 1, GeneratedAt: refTime,
		},
		LatestContentVersion: &v,
		Approval:             &campaign.Approval{ID: version2ID, ContentVersionID: versionID, ApprovedAt: refTime},
		CreatedAt:            refTime, UpdatedAt: refTime,
	}
}

func samplePreview() campaign.Preview {
	return campaign.Preview{
		CampaignID: campID, ContentVersionID: versionID, SampleIndex: 0, SampleCount: 584,
		Customer: campaign.PreviewCustomer{ID: customerID, FirstName: "", LastName: "Kaya", City: "İstanbul"},
		SMS:      campaign.PreviewSMS{Text: "Merhaba Değerli müşterimiz", Runes: 26, Limit: 160, WithinLimit: true},
		Email:    campaign.PreviewEmail{Subject: "Konu", SubjectRunes: 4, SubjectLimit: 80, WithinLimit: true, Text: "Gövde", HTML: "<!doctype html><html lang=\"tr\"><head></head><body><p>Gövde</p></body></html>"},
	}
}

// ---- fake service and run ----

type editCall struct {
	id, expected string
	raw          []byte
}

type contentService struct {
	fakeService

	mu          sync.Mutex
	startErr    error
	run         campaign.ContentRun
	startCtxs   []context.Context
	editErr     error
	edits       []editCall
	approveErr  error
	approved    []string
	previewErr  error
	previews    []campaign.PreviewQuery
	versionsErr error
}

func newContentService() *contentService { return &contentService{} }

func (s *contentService) StartContentGeneration(ctx context.Context, id string) (campaign.ContentRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.startCtxs = append(s.startCtxs, ctx)
	if s.startErr != nil {
		return nil, s.startErr
	}
	if id != campID {
		return nil, campaign.ErrCampaignNotFound
	}
	return s.run, nil
}

func (s *contentService) EditContent(_ context.Context, id, expected string, raw []byte) (campaign.ContentVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.edits = append(s.edits, editCall{id: id, expected: expected, raw: append([]byte(nil), raw...)})
	if s.editErr != nil {
		return campaign.ContentVersion{}, s.editErr
	}
	return sampleVersion(), nil
}

func (s *contentService) ListContentVersions(_ context.Context, id string) ([]campaign.ContentVersion, error) {
	if s.versionsErr != nil {
		return nil, s.versionsErr
	}
	if id != campID {
		return nil, campaign.ErrCampaignNotFound
	}
	return []campaign.ContentVersion{sampleVersion()}, nil
}

func (s *contentService) GetContentVersion(_ context.Context, id, vid string) (campaign.ContentVersion, error) {
	if id != campID {
		return campaign.ContentVersion{}, campaign.ErrCampaignNotFound
	}
	if vid != versionID {
		return campaign.ContentVersion{}, campaign.ErrContentVersionNotFound
	}
	return sampleVersion(), nil
}

func (s *contentService) Approve(_ context.Context, id, vid string) (campaign.Campaign, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.approved = append(s.approved, vid)
	if s.approveErr != nil {
		return campaign.Campaign{}, s.approveErr
	}
	return approvedCampaign(), nil
}

func (s *contentService) Preview(_ context.Context, id string, q campaign.PreviewQuery) (campaign.Preview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.previews = append(s.previews, q)
	if s.previewErr != nil {
		return campaign.Preview{}, s.previewErr
	}
	return samplePreview(), nil
}

func (s *contentService) startCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.startCtxs)
}

func (s *contentService) editCalls() []editCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]editCall(nil), s.edits...)
}

func (s *contentService) approveCalls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.approved...)
}

func (s *contentService) previewCalls() []campaign.PreviewQuery {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]campaign.PreviewQuery(nil), s.previews...)
}

var _ campaign.Service = (*contentService)(nil)

// replayRun is a fake campaign.ContentRun: it emits before, then (if gate is non-nil) waits until
// gate is closed or ctx is done, then emits after. ctxDone is closed when it observed ctx.Done.
type replayRun struct {
	before, after []composer.Event
	gate          chan struct{}
	ctxDone       chan struct{}
}

func (r *replayRun) Stream(ctx context.Context, emit func(composer.Event) error) error {
	for _, ev := range r.before {
		if err := emit(ev); err != nil {
			return err
		}
	}
	if r.gate != nil {
		select {
		case <-r.gate:
		case <-ctx.Done():
			if r.ctxDone != nil {
				close(r.ctxDone)
			}
			return ctx.Err()
		}
	}
	for _, ev := range r.after {
		if err := emit(ev); err != nil {
			return err
		}
	}
	return nil
}

func newContentRouter(t *testing.T, svc campaign.Service, provider llm.Provider, c clock.Clock) http.Handler {
	t.Helper()
	return httpapi.New(httpapi.Deps{
		Service:     svc,
		LLMProvider: provider,
		DB:          okPinger(),
		Metrics:     http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "# fake\n") }),
		Clock:       c,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

// ---- SSE parsing ----

type sseBlock struct {
	lines []string
	event string
	data  string
}

// parseSSE splits a complete SSE body into blocks (separated by a blank line).
func parseSSE(t *testing.T, body string) []sseBlock {
	t.Helper()
	if !strings.HasSuffix(body, "\n\n") && body != "" {
		t.Fatalf("SSE body does not end with a blank line: %q", body)
	}
	var out []sseBlock
	for _, raw := range strings.Split(strings.TrimSuffix(body, "\n\n"), "\n\n") {
		if raw == "" {
			continue
		}
		b := sseBlock{lines: strings.Split(raw, "\n")}
		for _, l := range b.lines {
			switch {
			case strings.HasPrefix(l, "event: "):
				b.event = strings.TrimPrefix(l, "event: ")
			case strings.HasPrefix(l, "data: "):
				if b.data != "" {
					t.Fatalf("block has more than one data line: %q", raw)
				}
				b.data = strings.TrimPrefix(l, "data: ")
			}
		}
		out = append(out, b)
	}
	return out
}

func jsonEqual(t *testing.T, got string, want any) {
	t.Helper()
	wb, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal want: %v", err)
	}
	var g, w any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("data is not JSON (%v): %s", err, got)
	}
	_ = json.Unmarshal(wb, &w)
	if diff := cmp.Diff(w, g); diff != "" {
		t.Fatalf("data mismatch (-want +got):\n%s", diff)
	}
}

func assertSSEHeaders(t *testing.T, h http.Header) {
	t.Helper()
	want := map[string]string{
		"Content-Type":      "text/event-stream; charset=utf-8",
		"Cache-Control":     "no-cache",
		"X-Accel-Buffering": "no",
	}
	for k, v := range want {
		if got := h.Get(k); got != v {
			t.Fatalf("%s = %q, want %q", k, got, v)
		}
	}
}

// ---- generateContent ----

func repairEvents() []composer.Event {
	return []composer.Event{
		composer.DeltaEvent{Attempt: 1, Channel: "sms", Field: "body", Text: "İndirim İndirim İndi"},
		composer.DeltaEvent{Attempt: 1, Channel: "email", Field: "subject", Text: "Sepetin seni bekliyor"},
		composer.InvalidEvent{Attempt: 1, Final: false, Errors: []validation.FieldError{{Path: "sms.body", Code: "sms_too_long", Message: "SMS body exceeds 160 characters", Limit: ipt(160), Actual: ipt(161)}}},
		composer.DeltaEvent{Attempt: 2, Channel: "sms", Field: "body", Text: "Merhaba {{first_name"},
		composer.DeltaEvent{Attempt: 2, Channel: "email", Field: "body", Text: "Merhaba\n\"tırnak\""},
		composer.ValidatedEvent{Attempt: 2, ContentVersionID: versionID, VersionNumber: 1, Content: sampleContent(), WorstCase: content.WorstCase{SMSRunes: 160, SubjectRunes: 42}},
	}
}

// REQ-CNT-04 (sse-events.md §0, §1, PLAN §0.5)
// POST /content writes HTTP 200 with the SSE headers and every composer event (delta…, non-final
// invalid, restarted delta…, validated) in order; the last event is the terminal one.
func TestGenerateContent_StreamsEventsAsSSE(t *testing.T) {
	t.Parallel()
	events := repairEvents()
	svc := newContentService()
	svc.run = &replayRun{before: events}
	h := newContentRouter(t, svc, llm.ProviderMock, clock.NewFake(refTime))
	rec := send(t, h, http.MethodPost, "/api/campaigns/"+campID+"/content", nil, map[string]string{"Accept": "text/event-stream"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	assertSSEHeaders(t, rec.Header())
	blocks := parseSSE(t, rec.Body.String())
	if len(blocks) != len(events) {
		t.Fatalf("got %d SSE blocks, want %d; body:\n%s", len(blocks), len(events), rec.Body.String())
	}
	for i, b := range blocks {
		if len(b.lines) != 2 || !strings.HasPrefix(b.lines[0], "event: ") || !strings.HasPrefix(b.lines[1], "data: ") {
			t.Fatalf("block %d = %q, want exactly an event line then one data line", i, b.lines)
		}
		if b.event != events[i].Name() {
			t.Fatalf("block %d event = %q, want %q", i, b.event, events[i].Name())
		}
		jsonEqual(t, b.data, events[i])
	}
	if n := svc.startCount(); n != 1 {
		t.Fatalf("StartContentGeneration calls = %d, want 1", n)
	}
}

// REQ-CNT-04 (sse-events.md §1.3)
// A final invalid event (here internal_error from the run) is written with final:true and an
// explicit content_version_id: null.
func TestGenerateContent_FinalInvalidHasExplicitNullVersion(t *testing.T) {
	t.Parallel()
	svc := newContentService()
	svc.run = &replayRun{before: []composer.Event{
		composer.DeltaEvent{Attempt: 1, Channel: "sms", Field: "body", Text: "Merhaba"},
		composer.InvalidEvent{Attempt: 1, Final: true, Errors: []validation.FieldError{{Path: "", Code: validation.CodeInternalError, Message: "internal error"}}},
	}}
	h := newContentRouter(t, svc, llm.ProviderMock, clock.NewFake(refTime))
	rec := send(t, h, http.MethodPost, "/api/campaigns/"+campID+"/content", []byte(`{}`), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	blocks := parseSSE(t, rec.Body.String())
	last := blocks[len(blocks)-1]
	if last.event != "invalid" {
		t.Fatalf("last event = %q, want invalid", last.event)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(last.data), &m); err != nil {
		t.Fatalf("data: %v", err)
	}
	v, present := m["content_version_id"]
	if !present || v != nil || m["final"] != true {
		t.Fatalf("final invalid data = %s, want final true and content_version_id null (present)", last.data)
	}
}

// REQ-CNT-04, REQ-AI-01 (README §5.3, §5.4)
// Errors detected before the stream opens are JSON responses, never SSE.
func TestGenerateContent_ErrorsBeforeTheStreamAreJSON(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "unknown campaign", err: campaign.ErrCampaignNotFound, status: http.StatusNotFound, code: "campaign_not_found"},
		{name: "draft campaign", err: campaign.ErrInvalidState, status: http.StatusConflict, code: "invalid_state"},
		{name: "generation running", err: campaign.ErrGenerationInProgress, status: http.StatusConflict, code: "generation_in_progress"},
		{name: "LLM unreachable before the stream", err: fmt.Errorf("open: %w", llm.ErrUnavailable), status: http.StatusBadGateway, code: "llm_unavailable"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := newContentService()
			svc.startErr = tc.err
			h := newContentRouter(t, svc, llm.ProviderMock, clock.NewFake(refTime))
			rec := send(t, h, http.MethodPost, "/api/campaigns/"+campID+"/content", nil, nil)
			assertErrorBody(t, rec, tc.status, tc.code, false)
		})
	}
}

// REQ-AUD-05
// A request body over 64 KiB is refused (413) before the generation starts.
func TestGenerateContent_RequestIsCheckedBeforeTheService(t *testing.T) {
	t.Parallel()
	tooLarge := []byte(`{"x":"` + strings.Repeat("a", 65537-8) + `"}`)
	if len(tooLarge) != 65537 {
		t.Fatalf("test setup: %d bytes", len(tooLarge))
	}
	tests := []struct {
		name    string
		path    string
		body    []byte
		headers map[string]string
		status  int
		code    string
		field   *[2]string
	}{
		{name: "body over 64 KiB", path: "/api/campaigns/" + campID + "/content", body: tooLarge, status: http.StatusRequestEntityTooLarge, code: "payload_too_large"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := newContentService()
			svc.run = &replayRun{}
			h := newContentRouter(t, svc, llm.ProviderMock, clock.NewFake(refTime))
			rec := send(t, h, http.MethodPost, tc.path, tc.body, tc.headers)
			e := assertErrorBody(t, rec, tc.status, tc.code, tc.field != nil)
			if tc.field != nil && !hasField(e, tc.field[0], tc.field[1]) {
				t.Fatalf("error.fields = %v, want %v", e["fields"], *tc.field)
			}
			if n := svc.startCount(); n != 0 {
				t.Fatalf("StartContentGeneration calls = %d, want 0", n)
			}
		})
	}
}

// REQ-AI-02 (llm-tools.md §3.1)
// With the mock provider a known X-Mock-LLM-Scenario reaches StartContentGeneration's context.
func TestGenerateContent_ScenarioHeaderReachesTheService(t *testing.T) {
	t.Parallel()
	svc := newContentService()
	svc.run = &replayRun{before: []composer.Event{composer.ValidatedEvent{Attempt: 1, ContentVersionID: versionID, VersionNumber: 1, Content: sampleContent(), WorstCase: content.WorstCase{SMSRunes: 160, SubjectRunes: 42}}}}
	h := newContentRouter(t, svc, llm.ProviderMock, clock.NewFake(refTime))
	rec := send(t, h, http.MethodPost, "/api/campaigns/"+campID+"/content", nil, map[string]string{"X-Mock-LLM-Scenario": "over_limit_subject"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", rec.Code, rec.Body.String())
	}
	svc.mu.Lock()
	ctx := svc.startCtxs[0]
	svc.mu.Unlock()
	if s, ok := llm.ScenarioFromContext(ctx); !ok || s != llm.ScenarioOverLimitSubject {
		t.Fatalf("ScenarioFromContext = %q, %v; want over_limit_subject", s, ok)
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
		})
	}
}

// ---- editContent ----

// REQ-APR-01
// X-Expected-Version is required: missing => 422, not a version id or "none" => 422, without
// calling the service. Valid values reach the service verbatim.
func TestEditContent_ExpectedVersionHeader(t *testing.T) {
	t.Parallel()
	body := jsonBody(t, sampleContent())
	tests := []struct {
		name     string
		path     string
		header   *string
		body     []byte
		status   int
		code     string
		field    *[2]string
		expected string // value the service must receive (success cases)
	}{
		{name: "missing header", header: nil, body: body, status: 422, code: "validation_failed", field: &[2]string{"header.X-Expected-Version", "schema_violation"}},
		{name: "empty header", header: strPtr(""), body: body, status: 422, code: "validation_failed"},
		{name: "version label", header: strPtr("v1"), body: body, status: 422, code: "validation_failed", field: &[2]string{"header.X-Expected-Version", "invalid_value"}},
		{name: "quoted ETag style", header: strPtr(`"` + versionID + `"`), body: body, status: 422, code: "validation_failed", field: &[2]string{"header.X-Expected-Version", "invalid_value"}},
		{name: "none reaches the service", header: strPtr("none"), body: body, status: 201, expected: "none"},
		{name: "a UUID reaches the service", header: strPtr(versionID), body: body, status: 201, expected: versionID},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := newContentService()
			h := newContentRouter(t, svc, llm.ProviderMock, clock.NewFake(refTime))
			path := tc.path
			if path == "" {
				path = "/api/campaigns/" + campID + "/content-versions"
			}
			headers := map[string]string{}
			if tc.header != nil {
				headers["X-Expected-Version"] = *tc.header
			}
			rec := send(t, h, http.MethodPost, path, tc.body, headers)
			if tc.status == 201 {
				if rec.Code != http.StatusCreated {
					t.Fatalf("status = %d, want 201; body: %s", rec.Code, rec.Body.String())
				}
				assertBodyJSON(t, rec, sampleVersionJSON())
				calls := svc.editCalls()
				if len(calls) != 1 || calls[0].id != campID || calls[0].expected != tc.expected {
					t.Fatalf("EditContent calls = %+v, want one with expected %q", calls, tc.expected)
				}
				var got, want any
				_ = json.Unmarshal(calls[0].raw, &got)
				_ = json.Unmarshal(tc.body, &want)
				if diff := cmp.Diff(want, got); diff != "" {
					t.Fatalf("EditContent received a different body (-want +got):\n%s", diff)
				}
				return
			}
			e := assertErrorBody(t, rec, tc.status, tc.code, tc.code == "validation_failed")
			if tc.field != nil && !hasField(e, tc.field[0], tc.field[1]) {
				t.Fatalf("error.fields = %v, want %v", e["fields"], *tc.field)
			}
			if n := len(svc.editCalls()); n != 0 {
				t.Fatalf("EditContent calls = %d, want 0", n)
			}
		})
	}
}

func strPtr(s string) *string { return &s }

// assertConflictBody checks the 409 content_edit_conflict body: error.{code,message,request_id,
// current_version{id,version_number}} and no fields.
func assertConflictBody(t *testing.T, rec *httptest.ResponseRecorder, wantID any, wantNumber any) {
	t.Helper()
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body: %s", rec.Code, rec.Body.String())
	}
	var top map[string]map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &top); err != nil || len(top) != 1 {
		t.Fatalf("body is not {\"error\":{...}} (%v): %s", err, rec.Body.String())
	}
	e := top["error"]
	if diff := cmp.Diff([]string{"code", "current_version", "message", "request_id"}, keys(e)); diff != "" {
		t.Fatalf("error keys mismatch (-want +got):\n%s", diff)
	}
	if e["code"] != "content_edit_conflict" || e["request_id"] != rec.Header().Get("X-Request-ID") {
		t.Fatalf("error = %v, want code content_edit_conflict with the request id", e)
	}
	want := map[string]any{"id": wantID, "version_number": wantNumber}
	if diff := cmp.Diff(want, e["current_version"]); diff != "" {
		t.Fatalf("error.current_version mismatch (-want +got):\n%s", diff)
	}
}

// REQ-APR-01, REQ-APR-02, REQ-APR-03 (README §5.3, §5.4)
// editContent maps service results: 201 ContentVersion; EditConflictError => 409
// content_edit_conflict with error.current_version (both null when none); lock/state => 409;
// validation => 422 with the field list (limit/actual for lengths); unknown campaign => 404.
func TestEditContent_ResponseMapping(t *testing.T) {
	t.Parallel()
	n := 4
	id := version2ID
	verr := &validation.Error{Fields: []validation.FieldError{
		{Path: "sms.body", Code: validation.CodeSMSTooLong, Message: "SMS body exceeds 160 characters", Limit: ipt(160), Actual: ipt(161), Err: content.ErrSMSTooLong},
		{Path: "email.subject", Code: validation.CodeUnknownVariable, Message: "unknown variable", Err: content.ErrUnknownVariable},
	}}
	tests := []struct {
		name   string
		err    error
		status int
		code   string
		fields []any
		cur    *[2]any
	}{
		{name: "conflict with a current version", err: &campaign.EditConflictError{CurrentID: &id, CurrentVersionNumber: &n}, status: 409, cur: &[2]any{version2ID, float64(4)}},
		{name: "conflict without a current version", err: &campaign.EditConflictError{}, status: 409, cur: &[2]any{nil, nil}},
		{name: "generation running", err: campaign.ErrGenerationInProgress, status: 409, code: "generation_in_progress"},
		{name: "sending campaign", err: campaign.ErrInvalidState, status: 409, code: "invalid_state"},
		{name: "unknown campaign", err: campaign.ErrCampaignNotFound, status: 404, code: "campaign_not_found"},
		{name: "invalid content", err: verr, status: 422, code: "validation_failed", fields: []any{
			map[string]any{"path": "sms.body", "code": "sms_too_long", "message": "SMS body exceeds 160 characters", "limit": float64(160), "actual": float64(161)},
			map[string]any{"path": "email.subject", "code": "unknown_variable", "message": "unknown variable"},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := newContentService()
			svc.editErr = tc.err
			h := newContentRouter(t, svc, llm.ProviderMock, clock.NewFake(refTime))
			rec := send(t, h, http.MethodPost, "/api/campaigns/"+campID+"/content-versions", jsonBody(t, sampleContent()), map[string]string{"X-Expected-Version": versionID})
			if tc.cur != nil {
				assertConflictBody(t, rec, tc.cur[0], tc.cur[1])
				return
			}
			e := assertErrorBody(t, rec, tc.status, tc.code, tc.fields != nil)
			if tc.fields != nil {
				if diff := cmp.Diff(tc.fields, e["fields"]); diff != "" {
					t.Fatalf("error.fields mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}

// REQ-AUD-05
// An edit body over 64 KiB is refused (413) before the service.
func TestEditContent_BodyChecks(t *testing.T) {
	t.Parallel()
	big := []byte(`{"sms":{"body":"` + strings.Repeat("a", 65537) + `"},"email":{"subject":"a","body":"b"}}`)
	tests := []struct {
		name        string
		body        []byte
		contentType string
		status      int
		code        string
	}{
		{name: "over 64 KiB", body: big, contentType: "application/json", status: 413, code: "payload_too_large"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := newContentService()
			h := newContentRouter(t, svc, llm.ProviderMock, clock.NewFake(refTime))
			rec := send(t, h, http.MethodPost, "/api/campaigns/"+campID+"/content-versions", tc.body, map[string]string{"X-Expected-Version": "none", "Content-Type": tc.contentType})
			assertErrorBody(t, rec, tc.status, tc.code, false)
			if n := len(svc.editCalls()); n != 0 {
				t.Fatalf("EditContent calls = %d, want 0", n)
			}
		})
	}
}

// ---- approveContent ----

// REQ-APR-03, REQ-CNT-04, REQ-CNT-05
// The approval carries the version id; results map to 200 approved Campaign, 404 unknown campaign,
// 409 (generation running, invalid draft, wrong state, stale version) and 422 worst-case fields.
func TestApproveContent(t *testing.T) {
	t.Parallel()
	worst := &validation.Error{Fields: []validation.FieldError{
		{Path: "sms.body", Code: validation.CodeSMSTooLongWorstCase, Message: "worst case exceeds 160", Limit: ipt(160), Actual: ipt(163), Err: content.ErrWorstCaseTooLong},
	}}
	tests := []struct {
		name      string
		body      []byte
		err       error
		status    int
		code      string
		field     *[2]string
		callsWant int
	}{
		{name: "approved", body: []byte(`{"content_version_id":"` + versionID + `"}`), status: 200, callsWant: 1},
		{name: "unknown campaign", body: []byte(`{"content_version_id":"` + versionID + `"}`), err: campaign.ErrCampaignNotFound, status: 404, code: "campaign_not_found", callsWant: 1},
		{name: "generation running", body: []byte(`{"content_version_id":"` + versionID + `"}`), err: campaign.ErrGenerationInProgress, status: 409, code: "generation_in_progress", callsWant: 1},
		{name: "invalid draft", body: []byte(`{"content_version_id":"` + versionID + `"}`), err: campaign.ErrContentNotValidated, status: 409, code: "content_not_validated", callsWant: 1},
		{name: "wrong state", body: []byte(`{"content_version_id":"` + versionID + `"}`), err: campaign.ErrInvalidState, status: 409, code: "invalid_state", callsWant: 1},
		{name: "stale version", body: []byte(`{"content_version_id":"` + versionID + `"}`), err: campaign.ErrStaleContentVersion, status: 409, code: "stale_content_version", callsWant: 1},
		{name: "worst case over the limit", body: []byte(`{"content_version_id":"` + versionID + `"}`), err: worst, status: 422, code: "validation_failed", field: &[2]string{"sms.body", "sms_too_long_worst_case"}, callsWant: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := newContentService()
			svc.approveErr = tc.err
			h := newContentRouter(t, svc, llm.ProviderMock, clock.NewFake(refTime))
			rec := send(t, h, http.MethodPost, "/api/campaigns/"+campID+"/approve", tc.body, nil)
			if tc.status == 200 {
				if rec.Code != http.StatusOK {
					t.Fatalf("status = %d; body: %s", rec.Code, rec.Body.String())
				}
				var got map[string]any
				_ = json.Unmarshal(rec.Body.Bytes(), &got)
				if got["state"] != "approved" || got["approval"] == nil {
					t.Fatalf("body = %s, want the approved Campaign", rec.Body.String())
				}
			} else {
				e := assertErrorBody(t, rec, tc.status, tc.code, tc.code == "validation_failed")
				if tc.field != nil {
					found := false
					fs, _ := e["fields"].([]any)
					for _, f := range fs {
						fm := f.(map[string]any)
						if fm["code"] == tc.field[1] && (tc.field[0] == "" || fm["path"] == tc.field[0]) {
							found = true
						}
					}
					if !found {
						t.Fatalf("error.fields = %v, want %v", e["fields"], *tc.field)
					}
				}
			}
			calls := svc.approveCalls()
			if len(calls) != tc.callsWant {
				t.Fatalf("Approve calls = %d, want %d", len(calls), tc.callsWant)
			}
			if tc.callsWant == 1 && calls[0] != versionID {
				t.Fatalf("Approve received version %q, want %q", calls[0], versionID)
			}
		})
	}
}

// ---- previewContent ----

// REQ-APR-01
// The preview's sample recipient is selected by sample_index (default 0), passed to the service;
// an unknown campaign is 404.
func TestPreviewContent(t *testing.T) {
	t.Parallel()
	base := "/api/campaigns/" + campID + "/preview"
	t.Run("defaults", func(t *testing.T) {
		t.Parallel()
		svc := newContentService()
		h := newContentRouter(t, svc, llm.ProviderMock, clock.NewFake(refTime))
		rec := send(t, h, http.MethodGet, base, nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; body: %s", rec.Code, rec.Body.String())
		}
		b, _ := json.Marshal(samplePreview())
		var want map[string]any
		_ = json.Unmarshal(b, &want)
		assertBodyJSON(t, rec, want)
		if diff := cmp.Diff([]campaign.PreviewQuery{{}}, svc.previewCalls()); diff != "" {
			t.Fatalf("PreviewQuery mismatch (-want +got):\n%s", diff)
		}
	})
	t.Run("parameters are passed through", func(t *testing.T) {
		t.Parallel()
		svc := newContentService()
		h := newContentRouter(t, svc, llm.ProviderMock, clock.NewFake(refTime))
		rec := send(t, h, http.MethodGet, base+"?sample_index=7&customer_id="+customerID+"&content_version_id="+versionID, nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; body: %s", rec.Code, rec.Body.String())
		}
		want := []campaign.PreviewQuery{{CustomerID: customerID, ContentVersionID: versionID, SampleIndex: 7}}
		if diff := cmp.Diff(want, svc.previewCalls()); diff != "" {
			t.Fatalf("PreviewQuery mismatch (-want +got):\n%s", diff)
		}
	})
	mapped := []struct {
		err    error
		status int
		code   string
	}{
		{err: campaign.ErrCampaignNotFound, status: 404, code: "campaign_not_found"},
	}
	for _, tc := range mapped {
		t.Run("maps "+tc.code, func(t *testing.T) {
			t.Parallel()
			svc := newContentService()
			svc.previewErr = tc.err
			h := newContentRouter(t, svc, llm.ProviderMock, clock.NewFake(refTime))
			assertErrorBody(t, send(t, h, http.MethodGet, base, nil, nil), tc.status, tc.code, false)
		})
	}
}

// ---- content versions ----

// REQ-APR-03
// Old versions are listable: GET /content-versions returns {items:[ContentVersion...]};
// GET /content-versions/{id} returns one; an unknown campaign is 404.
func TestContentVersions_ListAndGet(t *testing.T) {
	t.Parallel()
	svc := newContentService()
	h := newContentRouter(t, svc, llm.ProviderMock, clock.NewFake(refTime))

	rec := send(t, h, http.MethodGet, "/api/campaigns/"+campID+"/content-versions", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d; body: %s", rec.Code, rec.Body.String())
	}
	assertBodyJSON(t, rec, map[string]any{"items": []any{sampleVersionJSON()}})

	rec = send(t, h, http.MethodGet, "/api/campaigns/"+campID+"/content-versions/"+versionID, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d; body: %s", rec.Code, rec.Body.String())
	}
	assertBodyJSON(t, rec, sampleVersionJSON())

	cases := []struct {
		path  string
		codes []string
	}{
		{path: "/api/campaigns/" + unknownID + "/content-versions", codes: []string{"campaign_not_found"}},
		{path: "/api/campaigns/" + unknownID + "/content-versions/" + versionID, codes: []string{"campaign_not_found"}},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			rec := send(t, h, http.MethodGet, tc.path, nil, nil)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404; body: %s", rec.Code, rec.Body.String())
			}
			var top map[string]map[string]any
			_ = json.Unmarshal(rec.Body.Bytes(), &top)
			code, _ := top["error"]["code"].(string)
			if !slices.Contains(tc.codes, code) {
				t.Fatalf("error.code = %q, want one of %v", code, tc.codes)
			}
		})
	}
}
