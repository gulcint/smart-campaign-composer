package httpapi_test

// HTTP mapping of scheduled sending (REQ-SCH-01, REQ-SCH-03) with a fake campaign.Service:
// POST /api/campaigns/{campaign_id}/send with and without scheduled_at, and
// DELETE /api/campaigns/{campaign_id}/schedule. The window itself is the service's
// (campaign.Service.Schedule); here only the body parsing, the routing to Send / Schedule /
// CancelSchedule and the error mapping are checked.

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/gulcint/smart-campaign-composer/internal/campaign"
	"github.com/gulcint/smart-campaign-composer/internal/clock"
	"github.com/gulcint/smart-campaign-composer/internal/httpapi"
	"github.com/gulcint/smart-campaign-composer/internal/llm"
)

const schVersionID = "0b8f6c2e-6d0a-4a47-9c39-0f2d3b8e1a11"

type scheduleCall struct {
	id, versionID string
	at            time.Time
}

// scheduleService embeds fakeService (system_test.go) and records Send, Schedule and CancelSchedule.
type scheduleService struct {
	fakeService

	mu         sync.Mutex
	err        error                                           // returned by Send, Schedule and CancelSchedule
	scheduleFn func(at time.Time) (campaign.SendResult, error) // overrides Schedule's result when set
	sends      []string
	schedules  []scheduleCall
	cancels    []string
}

func (s *scheduleService) Send(_ context.Context, id, versionID string) (campaign.SendResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sends = append(s.sends, id+"/"+versionID)
	if s.err != nil {
		return campaign.SendResult{}, s.err
	}
	return campaign.SendResult{State: campaign.StateSending}, nil
}

func (s *scheduleService) Schedule(_ context.Context, id, versionID string, at time.Time) (campaign.SendResult, error) {
	s.mu.Lock()
	s.schedules = append(s.schedules, scheduleCall{id: id, versionID: versionID, at: at})
	fn, err := s.scheduleFn, s.err
	s.mu.Unlock()
	if fn != nil {
		return fn(at)
	}
	if err != nil {
		return campaign.SendResult{}, err
	}
	utc := at.UTC()
	return campaign.SendResult{CampaignID: id, State: campaign.StateScheduled, ContentVersionID: versionID, ScheduledAt: &utc}, nil
}

func (s *scheduleService) CancelSchedule(_ context.Context, id string) (campaign.CancelScheduleResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancels = append(s.cancels, id)
	if s.err != nil {
		return campaign.CancelScheduleResult{}, s.err
	}
	return campaign.CancelScheduleResult{CampaignID: id, State: campaign.StateApproved}, nil
}

func (s *scheduleService) calls() (sends []string, schedules []scheduleCall, cancels []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.sends...), append([]scheduleCall(nil), s.schedules...), append([]string(nil), s.cancels...)
}

var _ campaign.Service = (*scheduleService)(nil)

// REQ-SCH-01 C1, C2, C3
// scheduled_at is optional: without it the request goes to Send (instant, REQ-SND-01); with it to
// Schedule with the instant converted to UTC and truncated to microseconds; the 202 body carries
// state scheduled and scheduled_at as a UTC ("Z") timestamp of the same instant.
func TestSendEndpoint_RoutesScheduledAt(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		body   string
		wantAt *time.Time // nil => instant send
	}{
		{name: "without scheduled_at is an instant send", body: `{"content_version_id":"` + schVersionID + `"}`},
		{name: "offset +03:00 is converted to UTC", body: `{"content_version_id":"` + schVersionID + `","scheduled_at":"2026-01-15T15:30:00+03:00"}`,
			wantAt: ptrTime(time.Date(2026, 1, 15, 12, 30, 0, 0, time.UTC))},
		{name: "UTC with Z", body: `{"content_version_id":"` + schVersionID + `","scheduled_at":"2026-01-15T12:30:00Z"}`,
			wantAt: ptrTime(time.Date(2026, 1, 15, 12, 30, 0, 0, time.UTC))},
		{name: "a finer fraction is truncated to microseconds", body: `{"content_version_id":"` + schVersionID + `","scheduled_at":"2026-01-15T12:30:00.1234567Z"}`,
			wantAt: ptrTime(time.Date(2026, 1, 15, 12, 30, 0, 123456000, time.UTC))},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := &scheduleService{}
			h := newAudienceRouter(t, svc, llm.ProviderMock)
			rec := send(t, h, http.MethodPost, "/api/campaigns/"+campID+"/send", []byte(tc.body), nil)
			if rec.Code != http.StatusAccepted {
				t.Fatalf("status = %d, want 202; body: %s", rec.Code, rec.Body.String())
			}
			sends, schedules, _ := svc.calls()
			if tc.wantAt == nil {
				if len(sends) != 1 || len(schedules) != 0 {
					t.Fatalf("Send/Schedule calls = %d/%d, want 1/0", len(sends), len(schedules))
				}
				return
			}
			if len(sends) != 0 || len(schedules) != 1 {
				t.Fatalf("Send/Schedule calls = %d/%d, want 0/1", len(sends), len(schedules))
			}
			got := schedules[0]
			if got.id != campID || got.versionID != schVersionID {
				t.Fatalf("Schedule(id, versionID) = %s, %s; want %s, %s", got.id, got.versionID, campID, schVersionID)
			}
			if !got.at.Equal(*tc.wantAt) || got.at.Nanosecond() != tc.wantAt.Nanosecond() {
				t.Fatalf("Schedule at = %s, want %s", got.at.Format(time.RFC3339Nano), tc.wantAt.Format(time.RFC3339Nano))
			}
			body := decodeMap(t, rec.Body.Bytes())
			if body["state"] != "scheduled" || body["campaign_id"] != campID || body["content_version_id"] != schVersionID {
				t.Fatalf("202 state/campaign_id/content_version_id = %v/%v/%v, want scheduled/%s/%s", body["state"], body["campaign_id"], body["content_version_id"], campID, schVersionID)
			}
			s, _ := body["scheduled_at"].(string)
			at, err := time.Parse(time.RFC3339Nano, s)
			if err != nil || s[len(s)-1] != 'Z' || !at.Equal(*tc.wantAt) {
				t.Fatalf("202 scheduled_at = %v, want the UTC instant %s", body["scheduled_at"], tc.wantAt.Format(time.RFC3339Nano))
			}
		})
	}
}

// REQ-SCH-01 C5, C6
// Body errors on scheduled_at are 422 validation_failed before the service: an unparseable string
// is invalid_value, null or a non-string is schema_violation; all body field errors come in one 422
// in the order unknown fields, content_version_id, scheduled_at. A malformed path UUID is 404 first.
// Neither Send nor Schedule is called.
func TestSendEndpoint_ScheduledAtBodyErrors(t *testing.T) {
	t.Parallel()
	vid := `"content_version_id":"` + schVersionID + `"`
	tests := []struct {
		name   string
		path   string
		body   string
		status int
		code   string
		fields []fieldPC // in order; nil => no fields
	}{
		{name: "local time without offset", body: `{` + vid + `,"scheduled_at":"2026-01-15T15:30:00"}`, status: 422, code: "validation_failed", fields: []fieldPC{{"scheduled_at", "invalid_value"}}},
		{name: "date only", body: `{` + vid + `,"scheduled_at":"2026-01-15"}`, status: 422, code: "validation_failed", fields: []fieldPC{{"scheduled_at", "invalid_value"}}},
		{name: "invalid date", body: `{` + vid + `,"scheduled_at":"2026-02-30T10:00:00Z"}`, status: 422, code: "validation_failed", fields: []fieldPC{{"scheduled_at", "invalid_value"}}},
		{name: "lower-case t", body: `{` + vid + `,"scheduled_at":"2026-01-15t12:30:00Z"}`, status: 422, code: "validation_failed", fields: []fieldPC{{"scheduled_at", "invalid_value"}}},
		{name: "empty string", body: `{` + vid + `,"scheduled_at":""}`, status: 422, code: "validation_failed", fields: []fieldPC{{"scheduled_at", "invalid_value"}}},
		{name: "null", body: `{` + vid + `,"scheduled_at":null}`, status: 422, code: "validation_failed", fields: []fieldPC{{"scheduled_at", "schema_violation"}}},
		{name: "number", body: `{` + vid + `,"scheduled_at":1768480200}`, status: 422, code: "validation_failed", fields: []fieldPC{{"scheduled_at", "schema_violation"}}},
		{name: "object", body: `{` + vid + `,"scheduled_at":{"at":"2026-01-15T12:30:00Z"}}`, status: 422, code: "validation_failed", fields: []fieldPC{{"scheduled_at", "schema_violation"}}},
		{name: "all body errors in one 422, in order", body: `{"force":true,"content_version_id":"not-a-uuid","scheduled_at":"yarın"}`, status: 422, code: "validation_failed",
			fields: []fieldPC{{"force", "schema_violation"}, {"content_version_id", "invalid_value"}, {"scheduled_at", "invalid_value"}}},
		{name: "malformed path UUID comes before the body", path: "/api/campaigns/not-a-uuid/send", body: `{` + vid + `,"scheduled_at":"yarın"}`, status: 404, code: "campaign_not_found"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := &scheduleService{}
			h := newAudienceRouter(t, svc, llm.ProviderMock)
			path := tc.path
			if path == "" {
				path = "/api/campaigns/" + campID + "/send"
			}
			rec := send(t, h, http.MethodPost, path, []byte(tc.body), nil)
			e := assertErrorBody(t, rec, tc.status, tc.code, tc.fields != nil)
			if tc.fields != nil {
				if diff := cmp.Diff(tc.fields, fieldsPC(e)); diff != "" {
					t.Fatalf("error.fields (path, code) mismatch (-want +got):\n%s", diff)
				}
			}
			if sends, schedules, _ := svc.calls(); len(sends)+len(schedules) != 0 {
				t.Fatalf("Send/Schedule calls = %d/%d, want 0/0", len(sends), len(schedules))
			}
		})
	}
}

// REQ-SCH-01 C5, C6; REQ-SCH-03 C3
// Errors of campaign.Service.Schedule map like the send's: the window *validation.Error is 422
// validation_failed {scheduled_at schedule_too_soon|schedule_too_far}; then 404 campaign_not_found,
// 404 content_version_not_found, 409 generation_in_progress, 409 invalid_state, 409
// stale_content_version.
func TestSendEndpoint_ScheduleErrorMapping(t *testing.T) {
	t.Parallel()
	body := []byte(`{"content_version_id":"` + schVersionID + `","scheduled_at":"2026-01-15T12:30:00Z"}`)
	tests := []struct {
		name     string
		minLead  time.Duration
		now      time.Time
		err      error
		status   int
		code     string
		fieldErr *fieldPC
	}{
		{name: "too soon", now: refTime.Add(time.Hour), minLead: time.Minute, status: 422, code: "validation_failed", fieldErr: &fieldPC{"scheduled_at", "schedule_too_soon"}},
		{name: "too far", now: refTime.Add(-721 * time.Hour), minLead: time.Minute, status: 422, code: "validation_failed", fieldErr: &fieldPC{"scheduled_at", "schedule_too_far"}},
		{name: "unknown campaign", err: campaign.ErrCampaignNotFound, status: 404, code: "campaign_not_found"},
		{name: "unknown version", err: campaign.ErrContentVersionNotFound, status: 404, code: "content_version_not_found"},
		{name: "generation running", err: campaign.ErrGenerationInProgress, status: 409, code: "generation_in_progress"},
		{name: "not approved or scheduled", err: campaign.ErrInvalidState, status: 409, code: "invalid_state"},
		{name: "not the approved version", err: campaign.ErrStaleContentVersion, status: 409, code: "stale_content_version"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := &scheduleService{}
			if tc.fieldErr != nil {
				now, lead := tc.now, tc.minLead
				svc.scheduleFn = func(at time.Time) (campaign.SendResult, error) {
					return campaign.SendResult{}, campaign.ValidateScheduledAt(at, now, lead)
				}
			} else {
				svc.err = tc.err
			}
			h := newAudienceRouter(t, svc, llm.ProviderMock)
			rec := send(t, h, http.MethodPost, "/api/campaigns/"+campID+"/send", body, nil)
			e := assertErrorBody(t, rec, tc.status, tc.code, tc.fieldErr != nil)
			if tc.fieldErr != nil && !hasField(e, tc.fieldErr.Path, tc.fieldErr.Code) {
				t.Fatalf("error.fields = %v, want {%s %s}", e["fields"], tc.fieldErr.Path, tc.fieldErr.Code)
			}
		})
	}
}

// REQ-SCH-03 C1, C2
// DELETE /api/campaigns/{campaign_id}/schedule calls CancelSchedule (the body is not read) and
// answers 200; ErrCampaignNotFound is 404 campaign_not_found, ErrInvalidState 409 invalid_state; a
// malformed campaign id is 404 campaign_not_found without calling the service.
func TestCancelScheduleEndpoint(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		id         string
		body       []byte
		err        error
		status     int
		code       string // "" => success
		wantCalled bool
	}{
		{name: "scheduled campaign is canceled", id: campID, status: 200, wantCalled: true},
		{name: "the body is not read", id: campID, body: []byte(`{"bozuk`), status: 200, wantCalled: true},
		{name: "unknown campaign", id: unknownID, err: campaign.ErrCampaignNotFound, status: 404, code: "campaign_not_found", wantCalled: true},
		{name: "campaign not scheduled", id: campID, err: campaign.ErrInvalidState, status: 409, code: "invalid_state", wantCalled: true},
		{name: "malformed campaign id", id: "not-a-uuid", status: 404, code: "campaign_not_found"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := &scheduleService{err: tc.err}
			h := newAudienceRouter(t, svc, llm.ProviderMock)
			rec := send(t, h, http.MethodDelete, "/api/campaigns/"+tc.id+"/schedule", tc.body, nil)
			if tc.code == "" {
				if rec.Code != tc.status {
					t.Fatalf("status = %d, want %d; body: %s", rec.Code, tc.status, rec.Body.String())
				}
				// REQ-SCH-03 C1: the body is exactly {campaign_id, state approved, scheduled_at null}.
				assertBodyJSON(t, rec, map[string]any{"campaign_id": campID, "state": "approved", "scheduled_at": nil})
			} else {
				assertErrorBody(t, rec, tc.status, tc.code, false)
			}
			_, _, cancels := svc.calls()
			if !tc.wantCalled {
				if len(cancels) != 0 {
					t.Fatalf("CancelSchedule calls = %d, want 0", len(cancels))
				}
				return
			}
			if len(cancels) != 1 || cancels[0] != tc.id {
				t.Fatalf("CancelSchedule calls = %v, want one with %s", cancels, tc.id)
			}
		})
	}
}

// REQ-SCH-01 C7, C11
// GET /api/config gives the window to the UI: limits.schedule_min_lead_seconds =
// ceil(httpapi.Deps.ScheduleMinLead / 1s) and limits.schedule_max_ahead_seconds = 2592000.
func TestGetConfig_ScheduleLimits(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		minLead time.Duration
		want    float64
	}{
		{name: "zero is 0", minLead: 0, want: 0},
		{name: "1500ms rounds up to 2", minLead: 1500 * time.Millisecond, want: 2},
		{name: "1ms rounds up to 1", minLead: time.Millisecond, want: 1},
		{name: "the default 1m is 60", minLead: time.Minute, want: 60},
		{name: "a whole 24h stays 86400", minLead: 24 * time.Hour, want: 86400},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := httpapi.New(httpapi.Deps{
				Service:         fakeService{},
				LLMProvider:     llm.ProviderMock,
				DB:              okPinger(),
				Metrics:         http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "# fake\n") }),
				Clock:           clock.NewFake(refTime),
				Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
				ScheduleMinLead: tc.minLead,
			})
			rec := do(t, h, http.MethodGet, "/api/config")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
			}
			body := decodeMap(t, rec.Body.Bytes())
			limits, ok := body["limits"].(map[string]any)
			if !ok {
				t.Fatalf("limits = %v, want an object; body: %s", body["limits"], rec.Body.String())
			}
			if limits["schedule_min_lead_seconds"] != tc.want {
				t.Fatalf("limits.schedule_min_lead_seconds = %v, want %v", limits["schedule_min_lead_seconds"], tc.want)
			}
			if limits["schedule_max_ahead_seconds"] != float64(2592000) {
				t.Fatalf("limits.schedule_max_ahead_seconds = %v, want 2592000", limits["schedule_max_ahead_seconds"])
			}
		})
	}
}

type fieldPC struct{ Path, Code string }

func fieldsPC(e map[string]any) []fieldPC {
	fs, _ := e["fields"].([]any)
	out := make([]fieldPC, 0, len(fs))
	for _, f := range fs {
		fm, _ := f.(map[string]any)
		p, _ := fm["path"].(string)
		c, _ := fm["code"].(string)
		out = append(out, fieldPC{p, c})
	}
	return out
}

func decodeMap(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("body is not a JSON object (%v): %s", err, b)
	}
	return m
}

func ptrTime(t time.Time) *time.Time { return &t }
