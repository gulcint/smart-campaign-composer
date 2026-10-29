package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

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
	campID       = "6b0e1f8a-3c2d-4e5f-8a9b-0c1d2e3f4a5b"
	unknownID    = "00000000-0000-4000-8000-000000000000"
	exampleBrief = "Son 30 günde sepetine ürün ekleyip alışveriş yapmayan İstanbul'daki kullanıcılara %30 indirim hatırlatması gönder. Samimi ama kısa olsun."
	exampleRule  = `{"all":[{"field":"city","op":"eq","value":"İstanbul"},{"field":"last_cart_add_at","op":"within_last","value":{"days":30}},{"field":"orders_30d","op":"eq","value":0}]}`
	refTimeText  = "2026-01-15T12:00:00Z"
)

// REQ-AUD-05
// POST /api/campaigns: 201 with Location and the Campaign body (all nullable keys present).
func TestCreateCampaign_Created(t *testing.T) {
	t.Parallel()
	svc := newAudienceService()
	h := newAudienceRouter(t, svc, llm.ProviderMock)
	rec := send(t, h, http.MethodPost, "/api/campaigns", jsonBody(t, map[string]any{"brief": exampleBrief}), nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/api/campaigns/"+campID {
		t.Fatalf("Location = %q, want %q", loc, "/api/campaigns/"+campID)
	}
	assertBodyJSON(t, rec, draftCampaignJSON())
	if got := svc.briefs(); len(got) != 1 || strings.TrimSpace(got[0]) != exampleBrief {
		t.Fatalf("Service.Create briefs = %q, want one call with the example brief", got)
	}
}

// REQ-AUD-05
// Brief validation errors from the service are 422 validation_failed with the field list.
func TestCreateCampaign_ValidationErrorBody(t *testing.T) {
	t.Parallel()
	limit, actual := 2000, 2001
	tests := []struct {
		name   string
		fe     validation.FieldError
		want   map[string]any
		status int
	}{
		{
			name:   "empty brief is required",
			fe:     validation.FieldError{Path: "brief", Code: validation.CodeRequired, Message: "brief is required", Err: validation.ErrRequired},
			want:   map[string]any{"path": "brief", "code": "required", "message": "brief is required"},
			status: http.StatusUnprocessableEntity,
		},
		{
			name:   "2001 chars are too long",
			fe:     validation.FieldError{Path: "brief", Code: validation.CodeTooLong, Message: "brief exceeds 2000 characters", Limit: &limit, Actual: &actual, Err: validation.ErrTooLong},
			want:   map[string]any{"path": "brief", "code": "too_long", "message": "brief exceeds 2000 characters", "limit": float64(2000), "actual": float64(2001)},
			status: http.StatusUnprocessableEntity,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := newAudienceService()
			svc.createErr = &validation.Error{Fields: []validation.FieldError{tc.fe}}
			h := newAudienceRouter(t, svc, llm.ProviderMock)
			rec := send(t, h, http.MethodPost, "/api/campaigns", jsonBody(t, map[string]any{"brief": "x"}), nil)
			e := assertErrorBody(t, rec, tc.status, "validation_failed", true)
			if diff := cmp.Diff([]any{tc.want}, e["fields"]); diff != "" {
				t.Fatalf("error.fields mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// REQ-AUD-05
// A body over 64 KiB is refused (413) before the service (and so the LLM) is called.
func TestCreateCampaign_BodyIsCheckedBeforeTheService(t *testing.T) {
	t.Parallel()
	tooLarge := []byte(`{"brief":"` + strings.Repeat("a", 65537-12) + `"}`)
	if len(tooLarge) != 65537 {
		t.Fatalf("test setup: body is %d bytes, want 65537", len(tooLarge))
	}
	tests := []struct {
		name        string
		body        []byte
		contentType string
		status      int
		code        string
		fieldCode   string // "" => no fields
	}{
		{name: "65537 bytes are too large", body: tooLarge, contentType: "application/json", status: http.StatusRequestEntityTooLarge, code: "payload_too_large"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := newAudienceService()
			h := newAudienceRouter(t, svc, llm.ProviderMock)
			rec := send(t, h, http.MethodPost, "/api/campaigns", tc.body, map[string]string{"Content-Type": tc.contentType})
			e := assertErrorBody(t, rec, tc.status, tc.code, tc.fieldCode != "")
			if tc.fieldCode != "" && !hasFieldCode(e, tc.fieldCode) {
				t.Fatalf("error.fields = %v, want a %s entry", e["fields"], tc.fieldCode)
			}
			if n := len(svc.briefs()); n != 0 {
				t.Fatalf("Service.Create was called %d times, want 0", n)
			}
		})
	}
}

// REQ-AUD-05
// Exactly 65536 bytes is within the body limit (65537 => 413): the request reaches the service.
func TestCreateCampaign_BodyOf64KiBIsNotTooLarge(t *testing.T) {
	t.Parallel()
	body := []byte(`{"brief":"` + strings.Repeat("a", 65536-12) + `"}`)
	if len(body) != 65536 {
		t.Fatalf("test setup: body is %d bytes, want 65536", len(body))
	}
	limit, actual := 2000, 65524
	svc := newAudienceService()
	svc.createErr = &validation.Error{Fields: []validation.FieldError{{Path: "brief", Code: validation.CodeTooLong, Message: "too long", Limit: &limit, Actual: &actual, Err: validation.ErrTooLong}}}
	h := newAudienceRouter(t, svc, llm.ProviderMock)
	rec := send(t, h, http.MethodPost, "/api/campaigns", body, nil)
	assertErrorBody(t, rec, http.StatusUnprocessableEntity, "validation_failed", true)
	if n := len(svc.briefs()); n != 1 {
		t.Fatalf("Service.Create calls = %d, want 1", n)
	}
}

// REQ-AUD-00
// GET /api/campaigns/{id}: 200 for a known campaign, 404 campaign_not_found for an unknown one.
func TestGetCampaign(t *testing.T) {
	t.Parallel()
	svc := newAudienceService()
	h := newAudienceRouter(t, svc, llm.ProviderMock)

	rec := send(t, h, http.MethodGet, "/api/campaigns/"+campID, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	assertBodyJSON(t, rec, draftCampaignJSON())

	for _, id := range []string{unknownID} {
		t.Run("not found "+id, func(t *testing.T) {
			t.Parallel()
			rec := send(t, h, http.MethodGet, "/api/campaigns/"+id, nil, nil)
			assertErrorBody(t, rec, http.StatusNotFound, "campaign_not_found", false)
		})
	}
}

// REQ-AUD-00, REQ-AUD-01, REQ-AI-01
// POST /api/campaigns/{id}/segment maps service results to the contracted responses
// (README §5.3): 200 SegmentResult; invalid LLM output after repair => 502 llm_invalid_output with
// the last attempt's fields; unavailable LLM => 502 llm_unavailable; 404/409; anything else 500.
func TestGenerateSegment_ResponseMapping(t *testing.T) {
	t.Parallel()
	invalidOutput := errors.Join(composer.ErrInvalidOutput, &validation.Error{Fields: []validation.FieldError{
		{Path: "sql", Code: validation.CodeSchemaViolation, Message: "unknown property", Err: validation.ErrSchemaViolation},
	}})
	dbErr := errors.New(`ERROR: relation "customer_stats_at" does not exist (SQLSTATE 42P01)`)

	tests := []struct {
		name       string
		err        error
		body       []byte
		status     int
		code       string // "" => success
		fields     []any
		mustNotSay []string
	}{
		{name: "valid rule without body", status: http.StatusOK},
		{name: "invalid output after repair", err: invalidOutput, status: http.StatusBadGateway, code: "llm_invalid_output",
			fields: []any{map[string]any{"path": "sql", "code": "schema_violation", "message": "unknown property"}}},
		{name: "LLM unavailable", err: fmt.Errorf("generate segment: %w", llm.ErrUnavailable), status: http.StatusBadGateway, code: "llm_unavailable"},
		{name: "unknown campaign", err: campaign.ErrCampaignNotFound, status: http.StatusNotFound, code: "campaign_not_found"},
		{name: "campaign already sending", err: campaign.ErrInvalidState, status: http.StatusConflict, code: "invalid_state"},
		{name: "another generation running", err: campaign.ErrGenerationInProgress, status: http.StatusConflict, code: "generation_in_progress"},
		{name: "database error is an opaque 500", err: dbErr, status: http.StatusInternalServerError, code: "internal_error",
			mustNotSay: []string{"relation", "SQLSTATE", "customer_stats_at", "42P01"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := newAudienceService()
			svc.segmentErr = tc.err
			h := newAudienceRouter(t, svc, llm.ProviderMock)
			rec := send(t, h, http.MethodPost, "/api/campaigns/"+campID+"/segment", tc.body, nil)
			if tc.code == "" {
				if rec.Code != tc.status {
					t.Fatalf("status = %d, want %d; body: %s", rec.Code, tc.status, rec.Body.String())
				}
				assertBodyJSON(t, rec, segmentResultJSON())
				return
			}
			wantFields := tc.code == "llm_invalid_output"
			e := assertErrorBody(t, rec, tc.status, tc.code, wantFields)
			if tc.fields != nil {
				if diff := cmp.Diff(tc.fields, e["fields"]); diff != "" {
					t.Fatalf("error.fields mismatch (-want +got):\n%s", diff)
				}
			}
			for _, s := range tc.mustNotSay {
				if strings.Contains(rec.Body.String(), s) {
					t.Fatalf("response body contains %q: %s", s, rec.Body.String())
				}
			}
		})
	}
}

// REQ-AI-02
// The mock LLM scenario is selectable per request: a known name reaches the service through
// llm.WithScenario.
func TestGenerateSegment_MockScenarioHeader(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		provider     llm.Provider
		header       string
		wantStatus   int
		wantScenario llm.Scenario // "" => none on the context
		wantCalled   bool
	}{
		{name: "mock with a known scenario", provider: llm.ProviderMock, header: "then_ok_after_repair", wantStatus: http.StatusOK, wantScenario: llm.ScenarioThenOKAfterRepair, wantCalled: true},
		{name: "mock with sql_in_value", provider: llm.ProviderMock, header: "sql_in_value", wantStatus: http.StatusOK, wantScenario: llm.ScenarioSQLInValue, wantCalled: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := newAudienceService()
			h := newAudienceRouter(t, svc, tc.provider)
			rec := send(t, h, http.MethodPost, "/api/campaigns/"+campID+"/segment", nil, map[string]string{"X-Mock-LLM-Scenario": tc.header})
			if tc.wantStatus == http.StatusUnprocessableEntity {
				e := assertErrorBody(t, rec, http.StatusUnprocessableEntity, "validation_failed", true)
				if !hasField(e, "header.X-Mock-LLM-Scenario", "invalid_value") {
					t.Fatalf("error.fields = %v, want {header.X-Mock-LLM-Scenario invalid_value}", e["fields"])
				}
			} else if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			ctxs := svc.segmentContexts()
			if !tc.wantCalled {
				if len(ctxs) != 0 {
					t.Fatalf("Service.GenerateSegment was called %d times, want 0 (LLM must not be called)", len(ctxs))
				}
				return
			}
			if len(ctxs) != 1 {
				t.Fatalf("Service.GenerateSegment calls = %d, want 1", len(ctxs))
			}
			s, ok := llm.ScenarioFromContext(ctxs[0])
			if tc.wantScenario == "" {
				if ok {
					t.Fatalf("scenario %q reached the service, want none (header ignored for claude)", s)
				}
				return
			}
			if !ok || s != tc.wantScenario {
				t.Fatalf("ScenarioFromContext = %q, %v; want %q, true", s, ok, tc.wantScenario)
			}
		})
	}
}

// REQ-AUD-01, REQ-AUD-03
// POST /api/segments/count passes the raw document to the service; 200 SegmentCount, 422 for an
// invalid rule, 400 for a body that is not JSON (without calling the service).
func TestCountSegment(t *testing.T) {
	t.Parallel()
	body := []byte(`{"rule":` + exampleRule + `}`)

	t.Run("valid rule is counted", func(t *testing.T) {
		t.Parallel()
		svc := newAudienceService()
		h := newAudienceRouter(t, svc, llm.ProviderMock)
		rec := send(t, h, http.MethodPost, "/api/segments/count", body, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
		}
		assertBodyJSON(t, rec, map[string]any{
			"audience":       map[string]any{"matched": float64(584), "sms": float64(350), "email": float64(368)},
			"reference_time": refTimeText,
		})
		raws := svc.countBodies()
		if len(raws) != 1 {
			t.Fatalf("Service.CountRule calls = %d, want 1", len(raws))
		}
		var got, want any
		_ = json.Unmarshal(raws[0], &got)
		_ = json.Unmarshal(body, &want)
		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("CountRule received a different document (-want +got):\n%s", diff)
		}
	})

	t.Run("invalid rule is 422 with the field error", func(t *testing.T) {
		t.Parallel()
		svc := newAudienceService()
		svc.countErr = &validation.Error{Fields: []validation.FieldError{
			{Path: "rule.all[0].field", Code: validation.CodeUnknownField, Message: "unknown field", Err: segment.ErrUnknownField},
		}}
		h := newAudienceRouter(t, svc, llm.ProviderMock)
		rec := send(t, h, http.MethodPost, "/api/segments/count", []byte(`{"rule":{"all":[{"field":"email","op":"eq","value":"a@example.com"}]}}`), nil)
		e := assertErrorBody(t, rec, http.StatusUnprocessableEntity, "validation_failed", true)
		if !hasField(e, "rule.all[0].field", "unknown_field") {
			t.Fatalf("error.fields = %v, want {rule.all[0].field unknown_field}", e["fields"])
		}
	})

}

// ---- fake service ----

// audienceService embeds fakeService (system_test.go) and implements the P3 use cases.
type audienceService struct {
	fakeService

	mu         sync.Mutex
	createErr  error
	segmentErr error
	countErr   error
	created    []string
	segCtxs    []context.Context
	counted    [][]byte
}

func newAudienceService() *audienceService { return &audienceService{} }

func draftCampaign() campaign.Campaign {
	return campaign.Campaign{ID: campID, Brief: exampleBrief, State: campaign.StateDraft, CreatedAt: refTime, UpdatedAt: refTime}
}

func (s *audienceService) Create(_ context.Context, brief string) (campaign.Campaign, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.created = append(s.created, brief)
	if s.createErr != nil {
		return campaign.Campaign{}, s.createErr
	}
	return draftCampaign(), nil
}

func (s *audienceService) Get(_ context.Context, id string) (campaign.Campaign, error) {
	if id != campID {
		return campaign.Campaign{}, campaign.ErrCampaignNotFound
	}
	return draftCampaign(), nil
}

func (s *audienceService) GenerateSegment(ctx context.Context, id string) (campaign.SegmentResult, error) {
	s.mu.Lock()
	s.segCtxs = append(s.segCtxs, ctx)
	err := s.segmentErr
	s.mu.Unlock()
	if err != nil {
		return campaign.SegmentResult{}, err
	}
	if id != campID {
		return campaign.SegmentResult{}, campaign.ErrCampaignNotFound
	}
	return campaign.SegmentResult{
		CampaignID: campID,
		State:      campaign.StateAudienceReady,
		Segment: campaign.Segment{
			Rule:          json.RawMessage(exampleRule),
			Audience:      segment.Audience{Matched: 584, SMS: 350, Email: 368},
			LongestValues: content.Longest{FirstName: 50, LastName: 8, City: 8},
			ReferenceTime: refTime,
			Revision:      1,
			LLMAttempts:   1,
			GeneratedAt:   refTime,
		},
	}, nil
}

func (s *audienceService) CountRule(_ context.Context, raw []byte) (campaign.SegmentCount, error) {
	s.mu.Lock()
	s.counted = append(s.counted, append([]byte(nil), raw...))
	err := s.countErr
	s.mu.Unlock()
	if err != nil {
		return campaign.SegmentCount{}, err
	}
	return campaign.SegmentCount{Audience: segment.Audience{Matched: 584, SMS: 350, Email: 368}, ReferenceTime: refTime}, nil
}

func (s *audienceService) briefs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.created...)
}

func (s *audienceService) segmentContexts() []context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]context.Context(nil), s.segCtxs...)
}

func (s *audienceService) countBodies() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]byte(nil), s.counted...)
}

var _ campaign.Service = (*audienceService)(nil)

// ---- expected bodies ----

func draftCampaignJSON() map[string]any {
	return map[string]any{
		"id":                     campID,
		"brief":                  exampleBrief,
		"state":                  "draft",
		"segment":                nil,
		"latest_content_version": nil,
		"approval":               nil,
		"scheduled_at":           nil, // REQ-SCH-01: always present, null in draft
		"sending_started_at":     nil,
		"completed_at":           nil,
		"created_at":             refTimeText,
		"updated_at":             refTimeText,
	}
}

func segmentResultJSON() map[string]any {
	var rule any
	_ = json.Unmarshal([]byte(exampleRule), &rule)
	return map[string]any{
		"campaign_id": campID,
		"state":       "audience_ready",
		"segment": map[string]any{
			"rule":           rule,
			"audience":       map[string]any{"matched": float64(584), "sms": float64(350), "email": float64(368)},
			"longest_values": map[string]any{"first_name": float64(50), "last_name": float64(8), "city": float64(8)},
			"reference_time": refTimeText,
			"revision":       float64(1),
			"llm_attempts":   float64(1),
			"generated_at":   refTimeText,
		},
	}
}

// ---- helpers ----

func newAudienceRouter(t *testing.T, svc campaign.Service, provider llm.Provider) http.Handler {
	t.Helper()
	return httpapi.New(httpapi.Deps{
		Service:     svc,
		LLMProvider: provider,
		DB:          okPinger(),
		Metrics:     http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "# fake\n") }),
		Clock:       clock.NewFake(refTime),
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func jsonBody(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// send issues a request; a non-nil body defaults to Content-Type application/json.
func send(t *testing.T, h http.Handler, method, path string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req := httptest.NewRequestWithContext(t.Context(), method, path, r)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func assertBodyJSON(t *testing.T, rec *httptest.ResponseRecorder, want map[string]any) {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body is not JSON (%v): %s", err, rec.Body.String())
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("body mismatch (-want +got):\n%s", diff)
	}
}

// assertErrorBody checks the README §5 error shape exactly and returns the "error" object.
func assertErrorBody(t *testing.T, rec *httptest.ResponseRecorder, status int, code string, withFields bool) map[string]any {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, status, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var top map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &top); err != nil {
		t.Fatalf("body is not JSON (%v): %s", err, rec.Body.String())
	}
	if len(top) != 1 {
		t.Fatalf("top-level keys = %v, want only \"error\"", keys(top))
	}
	e, ok := top["error"].(map[string]any)
	if !ok {
		t.Fatalf("error is %T, want an object; body: %s", top["error"], rec.Body.String())
	}
	wantKeys := []string{"code", "message", "request_id"}
	if withFields {
		wantKeys = append(wantKeys, "fields")
	}
	slices.Sort(wantKeys)
	if diff := cmp.Diff(wantKeys, keys(e)); diff != "" {
		t.Fatalf("error keys mismatch (-want +got):\n%s\nbody: %s", diff, rec.Body.String())
	}
	if e["code"] != code {
		t.Fatalf("error.code = %v, want %s", e["code"], code)
	}
	if msg, _ := e["message"].(string); msg == "" {
		t.Fatalf("error.message is empty")
	}
	rid, _ := e["request_id"].(string)
	if rid == "" || rid != rec.Header().Get("X-Request-ID") {
		t.Fatalf("error.request_id = %q, want the X-Request-ID header %q", rid, rec.Header().Get("X-Request-ID"))
	}
	if withFields {
		fs, ok := e["fields"].([]any)
		if !ok || len(fs) == 0 {
			t.Fatalf("error.fields = %v, want a non-empty array", e["fields"])
		}
		for _, f := range fs {
			fm, ok := f.(map[string]any)
			if !ok {
				t.Fatalf("field error %v is not an object", f)
			}
			for _, k := range []string{"path", "code", "message"} {
				if _, ok := fm[k]; !ok {
					t.Fatalf("field error %v lacks %q", fm, k)
				}
			}
		}
	}
	return e
}

func hasField(e map[string]any, path, code string) bool {
	fs, _ := e["fields"].([]any)
	for _, f := range fs {
		if fm, ok := f.(map[string]any); ok && fm["path"] == path && fm["code"] == code {
			return true
		}
	}
	return false
}

func hasFieldCode(e map[string]any, code string) bool {
	fs, _ := e["fields"].([]any)
	for _, f := range fs {
		if fm, ok := f.(map[string]any); ok && fm["code"] == code {
			return true
		}
	}
	return false
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
