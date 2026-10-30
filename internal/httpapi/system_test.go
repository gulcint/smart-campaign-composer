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

// REQ-AI-03
// GET /api/config exposes the effective provider (mock | claude).
func TestGetConfig_ReportsEffectiveProvider(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		provider llm.Provider
		want     string
	}{
		{name: "mock provider", provider: llm.ProviderMock, want: "mock"},
		{name: "claude provider", provider: llm.ProviderClaude, want: "claude"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newRouter(t, tc.provider, okPinger())
			rec := do(t, h, http.MethodGet, "/api/config")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
			}
			var got map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("body is not JSON (%v): %s", err, rec.Body.String())
			}
			if got["llm_provider"] != tc.want {
				t.Fatalf("llm_provider = %v, want %q", got["llm_provider"], tc.want)
			}
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
func (fakeService) Schedule(context.Context, string, string, time.Time) (campaign.SendResult, error) {
	return campaign.SendResult{}, errNotUsed
}
func (fakeService) CancelSchedule(context.Context, string) (campaign.CancelScheduleResult, error) {
	return campaign.CancelScheduleResult{}, errNotUsed
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
