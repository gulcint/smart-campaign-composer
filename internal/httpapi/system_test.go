package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/gulcint/smart-campaign-composer/internal/campaign"
	"github.com/gulcint/smart-campaign-composer/internal/clock"
	"github.com/gulcint/smart-campaign-composer/internal/httpapi"
	"github.com/gulcint/smart-campaign-composer/internal/llm"
)

var refTime = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

var mockScenarioNames = []any{
	"ok", "invalid_json", "schema_violation", "over_limit_sms", "over_limit_subject", "unknown_variable",
	"injection_attempt", "truncated_stream", "then_ok_after_repair", "always_invalid", "sql_in_value",
	"html_in_content", "llm_unavailable", "stalled_stream",
}

// REQ-AI-03
// GET /api/config exposes the effective provider; with the mock it also lists the 14 scenarios
// (openapi AppConfig, additionalProperties: false).
func TestGetConfig_ReportsEffectiveProvider(t *testing.T) {
	t.Parallel()
	limits := map[string]any{
		"sms_max_runes":           float64(160),
		"email_subject_max_runes": float64(80),
		"email_body_max_runes":    float64(5000),
		"brief_max_runes":         float64(2000),
		"request_body_max_bytes":  float64(65536),
	}
	tests := []struct {
		name     string
		provider llm.Provider
		want     map[string]any
	}{
		{
			name:     "mock provider with scenario list",
			provider: llm.ProviderMock,
			want: map[string]any{
				"llm_provider":        "mock",
				"mock_scenarios":      mockScenarioNames,
				"limits":              limits,
				"template_variables":  []any{"first_name", "last_name", "city"},
				"first_name_fallback": "Değerli müşterimiz",
			},
		},
		{
			name:     "claude provider without scenario list",
			provider: llm.ProviderClaude,
			want: map[string]any{
				"llm_provider":        "claude",
				"limits":              limits,
				"template_variables":  []any{"first_name", "last_name", "city"},
				"first_name_fallback": "Değerli müşterimiz",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newRouter(t, tc.provider, okPinger())
			rec := do(t, h, http.MethodGet, "/api/config")
			assertJSON(t, rec, http.StatusOK, tc.want)
		})
	}
}

// REQ-TRK-03
// The router serves /healthz and /readyz (outside /api) with the health contract.
func TestRouter_HealthEndpoints(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		db         pingFunc
		path       string
		wantStatus int
		wantBody   map[string]any
	}{
		{
			name: "healthz is ok even when the database is down", db: failingPinger(), path: "/healthz",
			wantStatus: http.StatusOK, wantBody: map[string]any{"status": "ok"},
		},
		{
			name: "readyz is ready when the database answers", db: okPinger(), path: "/readyz",
			wantStatus: http.StatusOK, wantBody: map[string]any{"status": "ready", "checks": map[string]any{"database": "ok"}},
		},
		{
			name: "readyz is 503 when the database is down", db: failingPinger(), path: "/readyz",
			wantStatus: http.StatusServiceUnavailable, wantBody: map[string]any{"status": "not_ready", "checks": map[string]any{"database": "unavailable"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newRouter(t, llm.ProviderMock, tc.db)
			rec := do(t, h, http.MethodGet, tc.path)
			assertJSON(t, rec, tc.wantStatus, tc.wantBody)
			if strings.Contains(rec.Body.String(), "connection refused") {
				t.Fatalf("body leaks the database error: %s", rec.Body.String())
			}
		})
	}
}

// ---- fakes and helpers ----

type pingFunc func(ctx context.Context) error

func (f pingFunc) Ping(ctx context.Context) error { return f(ctx) }

func okPinger() pingFunc { return func(context.Context) error { return nil } }

func failingPinger() pingFunc {
	return func(context.Context) error { return errors.New("dial tcp 10.0.0.5:5432: connection refused") }
}

// fakeService satisfies campaign.Service; the system endpoints must not depend on it.
type fakeService struct{}

var errNotUsed = campaign.ErrCampaignNotFound

func (fakeService) Create(context.Context, string) (campaign.Campaign, error) {
	return campaign.Campaign{}, errNotUsed
}
func (fakeService) Get(context.Context, string) (campaign.Campaign, error) {
	return campaign.Campaign{}, errNotUsed
}
func (fakeService) List(context.Context, int, int) (campaign.CampaignPage, error) {
	return campaign.CampaignPage{}, errNotUsed
}
func (fakeService) GenerateSegment(context.Context, string) (campaign.SegmentResult, error) {
	return campaign.SegmentResult{}, errNotUsed
}
func (fakeService) StartContentGeneration(context.Context, string) (campaign.ContentRun, error) {
	return nil, errNotUsed
}
func (fakeService) EditContent(context.Context, string, string, []byte) (campaign.ContentVersion, error) {
	return campaign.ContentVersion{}, errNotUsed
}
func (fakeService) ListContentVersions(context.Context, string) ([]campaign.ContentVersion, error) {
	return nil, errNotUsed
}
func (fakeService) GetContentVersion(context.Context, string, string) (campaign.ContentVersion, error) {
	return campaign.ContentVersion{}, errNotUsed
}
func (fakeService) Approve(context.Context, string, string) (campaign.Campaign, error) {
	return campaign.Campaign{}, errNotUsed
}
func (fakeService) Send(context.Context, string, string) (campaign.SendResult, error) {
	return campaign.SendResult{}, errNotUsed
}
func (fakeService) Status(context.Context, string) (campaign.CampaignStatus, error) {
	return campaign.CampaignStatus{}, errNotUsed
}
func (fakeService) Preview(context.Context, string, campaign.PreviewQuery) (campaign.Preview, error) {
	return campaign.Preview{}, errNotUsed
}
func (fakeService) ListDeliveries(context.Context, string, campaign.DeliveryFilter) (campaign.DeliveryPage, error) {
	return campaign.DeliveryPage{}, errNotUsed
}
func (fakeService) CountRule(context.Context, []byte) (campaign.SegmentCount, error) {
	return campaign.SegmentCount{}, errNotUsed
}

var _ campaign.Service = fakeService{}

func newRouter(t *testing.T, provider llm.Provider, db pingFunc) http.Handler {
	t.Helper()
	return httpapi.New(httpapi.Deps{
		Service:     fakeService{},
		LLMProvider: provider,
		DB:          db,
		Metrics: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
			_, _ = io.WriteString(w, "# fake metrics\n")
		}),
		Clock:  clock.NewFake(refTime),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func do(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), method, path, nil))
	return rec
}

func assertJSON(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantBody map[string]any) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, wantStatus, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body is not JSON (%v): %s", err, rec.Body.String())
	}
	if diff := cmp.Diff(wantBody, got); diff != "" {
		t.Fatalf("body mismatch (-want +got):\n%s", diff)
	}
}
