package campaign_test

// Scheduled sending (REQ-SCH-01..03) over the real Postgres of compose.test.yml (TEST_DATABASE_URL,
// scc_app), with the harness of service_integration_test.go; skips under -short (testdb.App).
//
// Isolation: campaign.Scheduler starts EVERY due scheduled campaign of the database, not only the
// ones of the calling test. Therefore
//   - the Scheduler tests (TestScheduler_*) are deliberately NOT parallel: Go runs this package's
//     parallel tests only after the sequential ones finished, so no other test of the package has a
//     scheduled campaign while a Scheduler turn runs;
//   - they work on a clock (schRef, mid-2025) far before svcRef, so a campaign that a parallel
//     Service.Schedule test planned relative to svcRef (and may leave scheduled) is never due for
//     them, not even in a later run on the same database;
//   - every campaign they schedule is canceled in t.Cleanup if it is still scheduled.
// TestScheduler_ConsentIsReadAtStart mutates one customer and restores it in t.Cleanup (same rule
// as TestService_ApproveRechecksWorstCaseWithCurrentCustomers).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gulcint/smart-campaign-composer/internal/campaign"
	"github.com/gulcint/smart-campaign-composer/internal/clock"
	"github.com/gulcint/smart-campaign-composer/internal/llm"
	"github.com/gulcint/smart-campaign-composer/internal/postgres"
	"github.com/gulcint/smart-campaign-composer/internal/validation"
	"github.com/gulcint/smart-campaign-composer/test/testdb"
)

// schRef is the start of the injected clock of the Scheduler tests (see the isolation note).
var schRef = time.Date(2025, 6, 1, 9, 0, 0, 0, time.UTC)

// newSchHarness builds the service like newHarness, with the clock starting at start and
// campaign.Deps.ScheduleMinLead = minLead (0 = no lead).
func newSchHarness(t *testing.T, c llm.Client, start time.Time, minLead time.Duration) *harness {
	t.Helper()
	pool := testdb.App(t)
	fc := clock.NewFake(start)
	ref := svcRef
	svc := campaign.NewService(campaign.Deps{
		Store:                postgres.NewCampaignStore(pool),
		Audience:             postgres.NewAudience(pool),
		LLM:                  c,
		Clock:                fc,
		SegmentReferenceTime: &ref,
		ScheduleMinLead:      minLead,
		Logger:               slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return &harness{svc: svc, pool: pool, clock: fc, m: readSvcManifest(t)}
}

// schApproved returns an approved campaign of the example brief and its approved version.
func (h *harness) schApproved(t *testing.T) (id, vid string) {
	t.Helper()
	id = h.audienceReady(t)
	vid = h.edit(t, id, campaign.ExpectedVersionNone, okContent()).ID
	if _, err := h.svc.Approve(context.Background(), id, vid); err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	return id, vid
}

// schApprovedEmpty returns an approved campaign whose segment matches nobody (mock scenario
// sql_in_value: the SQL text is only a value, REQ-AUD-02).
func (h *harness) schApprovedEmpty(t *testing.T) (id, vid string) {
	t.Helper()
	c, err := h.svc.Create(context.Background(), h.m.ExampleBrief.Brief)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := h.svc.GenerateSegment(scen(llm.ScenarioSQLInValue), c.ID); err != nil {
		t.Fatalf("GenerateSegment(sql_in_value) error = %v", err)
	}
	if a := h.get(t, c.ID).Segment.Audience; a.SMS != 0 || a.Email != 0 {
		t.Fatalf("setup: sql_in_value audience = %+v, want empty channels", a)
	}
	vid = h.edit(t, c.ID, campaign.ExpectedVersionNone, okContent()).ID
	if _, err := h.svc.Approve(context.Background(), c.ID, vid); err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	return c.ID, vid
}

// schSchedule plans the campaign at at, checks the SendResult and registers a cleanup that cancels
// the plan if the campaign is still scheduled when the test ends.
func (h *harness) schSchedule(t *testing.T, id, vid string, at time.Time) campaign.SendResult {
	t.Helper()
	res, err := h.svc.Schedule(context.Background(), id, vid, at)
	if err != nil {
		t.Fatalf("Schedule(%s) error = %v", at.Format(time.RFC3339Nano), err)
	}
	t.Cleanup(func() {
		if h.get(t, id).State == campaign.StateScheduled {
			if _, err := h.svc.CancelSchedule(context.Background(), id); err != nil {
				t.Errorf("cleanup: CancelSchedule(%s) error = %v", id, err)
			}
		}
	})
	return res
}

// scheduledAt reads campaigns.scheduled_at (NULL => nil).
func (h *harness) scheduledAt(t *testing.T, id string) *time.Time {
	t.Helper()
	var at *time.Time
	if err := h.pool.QueryRow(t.Context(), `SELECT scheduled_at FROM campaigns WHERE id = $1`, id).Scan(&at); err != nil {
		t.Fatalf("read campaigns.scheduled_at: %v", err)
	}
	return at
}

// assertPlan checks the campaign state, campaigns.scheduled_at and campaign.Campaign.ScheduledAt
// (nil => NULL / nil) and the delivery count.
func (h *harness) assertPlan(t *testing.T, id string, state campaign.State, at *time.Time, deliveries int) {
	t.Helper()
	c := h.get(t, id)
	if c.State != state {
		t.Fatalf("campaign state = %s, want %s", c.State, state)
	}
	for name, got := range map[string]*time.Time{"campaigns.scheduled_at": h.scheduledAt(t, id), "Campaign.ScheduledAt": c.ScheduledAt} {
		switch {
		case at == nil && got != nil:
			t.Fatalf("%s = %s, want NULL", name, got.Format(time.RFC3339Nano))
		case at != nil && (got == nil || !got.Equal(*at)):
			t.Fatalf("%s = %v, want %s", name, got, at.Format(time.RFC3339Nano))
		}
	}
	if n := h.deliveryCount(t, id); n != deliveries {
		t.Fatalf("deliveries = %d, want %d", n, deliveries)
	}
}

func tp(t time.Time) *time.Time { return &t }

// schCountingLLM wraps llm.Mock and counts the calls that reach the LLM.
type schCountingLLM struct {
	mock          *llm.Mock
	mu            sync.Mutex
	segs, streams int
}

func (c *schCountingLLM) GenerateSegment(ctx context.Context, req llm.SegmentRequest) (json.RawMessage, error) {
	c.mu.Lock()
	c.segs++
	c.mu.Unlock()
	return c.mock.GenerateSegment(ctx, req)
}

func (c *schCountingLLM) StreamContent(ctx context.Context, req llm.ContentRequest) (llm.ContentStream, error) {
	c.mu.Lock()
	c.streams++
	c.mu.Unlock()
	return c.mock.StreamContent(ctx, req)
}

func (c *schCountingLLM) calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.segs + c.streams
}

// ---- Service.Schedule / CancelSchedule / Send ----

// REQ-SCH-01 C2, C3, C9, C10; REQ-SCH-03 C3
// Schedule on an approved campaign: SendResult{State: scheduled, ScheduledAt: at in UTC, zero
// Deliveries}; the campaign is scheduled with campaigns.scheduled_at = at, keeps its approval and has
// no delivery. Scheduling again moves scheduled_at (rescheduling), still without deliveries.
func TestService_ScheduleApprovedCampaign(t *testing.T) {
	t.Parallel()
	h := newSchHarness(t, llm.NewMock(llm.MockOptions{}), svcRef, time.Minute)
	id, vid := h.schApproved(t)
	approval := h.get(t, id).Approval

	tsi := time.FixedZone("TSİ", 3*3600)
	at := time.Date(2026, 1, 15, 18, 30, 0, 0, tsi) // 15:30Z
	res, err := h.svc.Schedule(context.Background(), id, vid, at)
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	var zero campaign.SendResult
	if res.State != campaign.StateScheduled || res.Deliveries != zero.Deliveries {
		t.Fatalf("SendResult state/deliveries = %s/%+v, want scheduled and 0/0/0", res.State, res.Deliveries)
	}
	if res.CampaignID != id || res.ContentVersionID != vid {
		t.Fatalf("SendResult campaign/version = %s/%s, want %s/%s", res.CampaignID, res.ContentVersionID, id, vid)
	}
	if c := h.get(t, id); c.SendingStartedAt != nil {
		t.Fatalf("Campaign.SendingStartedAt while scheduled = %s, want nil", c.SendingStartedAt)
	}
	if res.ScheduledAt == nil || !res.ScheduledAt.Equal(at) || res.ScheduledAt.Location() != time.UTC {
		t.Fatalf("SendResult.ScheduledAt = %v, want %s in UTC", res.ScheduledAt, at.UTC().Format(time.RFC3339))
	}
	h.assertPlan(t, id, campaign.StateScheduled, tp(at), 0)
	c := h.get(t, id)
	if c.Approval == nil || approval == nil || c.Approval.ID != approval.ID || c.Approval.ContentVersionID != vid {
		t.Fatalf("approval while scheduled = %+v, want the unchanged approval of %s", c.Approval, vid)
	}

	later := svcRef.Add(48 * time.Hour)
	res, err = h.svc.Schedule(context.Background(), id, vid, later)
	if err != nil {
		t.Fatalf("Schedule() again (reschedule) error = %v", err)
	}
	if res.State != campaign.StateScheduled || res.ScheduledAt == nil || !res.ScheduledAt.Equal(later) {
		t.Fatalf("rescheduled SendResult = %+v, want scheduled at %s", res, later.Format(time.RFC3339))
	}
	h.assertPlan(t, id, campaign.StateScheduled, tp(later), 0)
	if _, err := h.svc.CancelSchedule(context.Background(), id); err != nil {
		t.Fatalf("CancelSchedule() error = %v", err)
	}
}

// REQ-SCH-01 C5, C6, C9
// Schedule check order: the window (service clock + campaign.Deps.ScheduleMinLead) comes before any
// database check, so an unknown campaign with a past instant is ErrScheduleTooSoon; then
// ErrCampaignNotFound › ErrContentVersionNotFound › ErrInvalidState › ErrStaleContentVersion. Both
// window bounds are inclusive. A refused Schedule changes nothing and creates no delivery.
func TestService_ScheduleChecks(t *testing.T) {
	t.Parallel()
	h := newSchHarness(t, llm.NewMock(llm.MockOptions{}), svcRef, time.Minute)
	bg := context.Background()
	apprID, apprV := h.schApproved(t)

	staleID := h.audienceReady(t)
	staleV1 := h.edit(t, staleID, campaign.ExpectedVersionNone, editContent("v1"))
	staleV2 := h.edit(t, staleID, staleV1.ID, okContent())
	if _, err := h.svc.Approve(bg, staleID, staleV2.ID); err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	readyID := h.audienceReady(t)
	readyV := h.edit(t, readyID, campaign.ExpectedVersionNone, okContent())
	sendingID, sendingV := h.schApproved(t)
	if _, err := h.svc.Send(bg, sendingID, sendingV); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	sendingRows := h.deliveryCount(t, sendingID)

	unknown := "00000000-0000-4000-8000-000000000000"
	ok := svcRef.Add(time.Hour)
	tests := []struct {
		name    string
		id, vid string
		at      time.Time
		wantErr error
		code    string // field code when the error is a window *validation.Error
	}{
		{name: "unknown campaign with a past instant is too soon", id: unknown, vid: apprV, at: svcRef.Add(-time.Hour), wantErr: campaign.ErrScheduleTooSoon, code: "schedule_too_soon"},
		{name: "unknown campaign beyond 720h is too far", id: unknown, vid: apprV, at: svcRef.Add(721 * time.Hour), wantErr: campaign.ErrScheduleTooFar, code: "schedule_too_far"},
		{name: "1s before now + min lead is too soon", id: apprID, vid: apprV, at: svcRef.Add(time.Minute - time.Second), wantErr: campaign.ErrScheduleTooSoon, code: "schedule_too_soon"},
		{name: "1s after now + 720h is too far", id: apprID, vid: apprV, at: svcRef.Add(720*time.Hour + time.Second), wantErr: campaign.ErrScheduleTooFar, code: "schedule_too_far"},
		{name: "too soon wins over a stale version", id: staleID, vid: staleV1.ID, at: svcRef, wantErr: campaign.ErrScheduleTooSoon, code: "schedule_too_soon"},
		{name: "unknown campaign", id: unknown, vid: apprV, at: ok, wantErr: campaign.ErrCampaignNotFound},
		{name: "unknown version", id: apprID, vid: "00000000-0000-4000-8000-000000000001", at: ok, wantErr: campaign.ErrContentVersionNotFound},
		{name: "another campaign's version", id: apprID, vid: readyV.ID, at: ok, wantErr: campaign.ErrContentVersionNotFound},
		{name: "content_ready campaign is not approved", id: readyID, vid: readyV.ID, at: ok, wantErr: campaign.ErrInvalidState},
		{name: "sending campaign", id: sendingID, vid: sendingV, at: ok, wantErr: campaign.ErrInvalidState},
		{name: "version older than the approved one", id: staleID, vid: staleV1.ID, at: ok, wantErr: campaign.ErrStaleContentVersion},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := h.svc.Schedule(bg, tc.id, tc.vid, tc.at)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Schedule() error = %v, want %v", err, tc.wantErr)
			}
			if tc.code != "" {
				var ve *validation.Error
				if !errors.As(err, &ve) || !hasFieldCode(ve.Fields, "scheduled_at", tc.code) {
					t.Fatalf("Schedule() error = %v, want a *validation.Error {scheduled_at %s}", err, tc.code)
				}
			}
		})
	}
	h.assertPlan(t, apprID, campaign.StateApproved, nil, 0)
	h.assertPlan(t, staleID, campaign.StateApproved, nil, 0)
	h.assertPlan(t, readyID, campaign.StateContentReady, nil, 0)
	h.assertPlan(t, sendingID, campaign.StateSending, nil, sendingRows)

	// Inclusive bounds: exactly now + min lead and exactly now + 720h are accepted.
	for _, at := range []time.Time{svcRef.Add(time.Minute), svcRef.Add(720 * time.Hour)} {
		if _, err := h.svc.Schedule(bg, apprID, apprV, at); err != nil {
			t.Fatalf("Schedule(%s) error = %v, want nil (bound is inclusive)", at.Format(time.RFC3339), err)
		}
		h.assertPlan(t, apprID, campaign.StateScheduled, tp(at), 0)
	}
	if _, err := h.svc.CancelSchedule(bg, apprID); err != nil {
		t.Fatalf("CancelSchedule() error = %v", err)
	}
}

// REQ-SCH-01 C9
// campaign.Deps.ScheduleMinLead zero means no lead: now itself is accepted, 1s before is too soon.
func TestService_ScheduleWithoutMinLead(t *testing.T) {
	t.Parallel()
	h := newSchHarness(t, llm.NewMock(llm.MockOptions{}), svcRef, 0)
	id, vid := h.schApproved(t)
	if _, err := h.svc.Schedule(context.Background(), id, vid, svcRef.Add(-time.Second)); !errors.Is(err, campaign.ErrScheduleTooSoon) {
		t.Fatalf("Schedule(now - 1s) error = %v, want %v", err, campaign.ErrScheduleTooSoon)
	}
	h.assertPlan(t, id, campaign.StateApproved, nil, 0)
	if _, err := h.svc.Schedule(context.Background(), id, vid, svcRef); err != nil {
		t.Fatalf("Schedule(now) error = %v, want nil with no lead", err)
	}
	h.assertPlan(t, id, campaign.StateScheduled, tp(svcRef), 0)
	if _, err := h.svc.CancelSchedule(context.Background(), id); err != nil {
		t.Fatalf("CancelSchedule() error = %v", err)
	}
}

// REQ-SCH-03 C1, C2, C4
// CancelSchedule: scheduled => approved with scheduled_at NULL, the approval and the approved
// version kept, no delivery; afterwards an instant Send works as before. Cancel on an approved,
// sending or unknown campaign is ErrInvalidState / ErrCampaignNotFound and changes nothing.
func TestService_CancelSchedule(t *testing.T) {
	t.Parallel()
	h := newSchHarness(t, llm.NewMock(llm.MockOptions{}), svcRef, time.Minute)
	bg := context.Background()
	id, vid := h.schApproved(t)
	approval := h.get(t, id).Approval

	if _, err := h.svc.CancelSchedule(bg, id); !errors.Is(err, campaign.ErrInvalidState) {
		t.Fatalf("CancelSchedule(approved) error = %v, want %v", err, campaign.ErrInvalidState)
	}
	if _, err := h.svc.CancelSchedule(bg, "00000000-0000-4000-8000-000000000000"); !errors.Is(err, campaign.ErrCampaignNotFound) {
		t.Fatalf("CancelSchedule(unknown) error = %v, want %v", err, campaign.ErrCampaignNotFound)
	}
	h.assertPlan(t, id, campaign.StateApproved, nil, 0)

	h.schSchedule(t, id, vid, svcRef.Add(2*time.Hour))
	res, err := h.svc.CancelSchedule(bg, id)
	if err != nil {
		t.Fatalf("CancelSchedule(scheduled) error = %v", err)
	}
	if res.CampaignID != id || res.State != campaign.StateApproved || res.ScheduledAt != nil {
		t.Fatalf("CancelScheduleResult = %+v, want {%s approved nil}", res, id)
	}
	h.assertPlan(t, id, campaign.StateApproved, nil, 0)
	c := h.get(t, id)
	if c.Approval == nil || c.Approval.ID != approval.ID || c.Approval.ContentVersionID != vid {
		t.Fatalf("approval after cancel = %+v, want the unchanged approval of %s", c.Approval, vid)
	}
	if _, err := h.svc.CancelSchedule(bg, id); !errors.Is(err, campaign.ErrInvalidState) {
		t.Fatalf("second CancelSchedule() error = %v, want %v", err, campaign.ErrInvalidState)
	}

	if _, err := h.svc.Send(bg, id, vid); err != nil {
		t.Fatalf("Send() after cancel error = %v, want nil", err)
	}
	want := h.audienceTotal(t, id)
	h.assertPlan(t, id, campaign.StateSending, nil, want)
	if _, err := h.svc.CancelSchedule(bg, id); !errors.Is(err, campaign.ErrInvalidState) {
		t.Fatalf("CancelSchedule(sending) error = %v, want %v", err, campaign.ErrInvalidState)
	}
	h.assertPlan(t, id, campaign.StateSending, nil, want)
}

// REQ-SCH-03 C4, C6
// "Send now" from scheduled: Send succeeds, the deliveries are the channel audiences, the plan drops
// in the same operation (scheduled_at NULL, state sending). Afterwards another Send and a Schedule
// with a valid future instant are ErrInvalidState and the row count is unchanged.
func TestService_SendNowFromScheduled(t *testing.T) {
	t.Parallel()
	h := newSchHarness(t, llm.NewMock(llm.MockOptions{}), svcRef, time.Minute)
	bg := context.Background()
	id, vid := h.schApproved(t)
	h.schSchedule(t, id, vid, svcRef.Add(3*time.Hour))

	res, err := h.svc.Send(bg, id, vid)
	if err != nil {
		t.Fatalf("Send(scheduled campaign) error = %v, want nil", err)
	}
	want := h.audienceTotal(t, id)
	if res.State != campaign.StateSending || res.ScheduledAt != nil || res.CampaignID != id || res.ContentVersionID != vid {
		t.Fatalf("SendResult = %+v, want sending with scheduled_at nil for %s/%s", res, id, vid)
	}
	if c := h.get(t, id); c.SendingStartedAt == nil {
		t.Fatal("Campaign.SendingStartedAt after send now = nil, want set")
	}
	h.assertPlan(t, id, campaign.StateSending, nil, want)

	if _, err := h.svc.Send(bg, id, vid); !errors.Is(err, campaign.ErrInvalidState) {
		t.Fatalf("second Send() error = %v, want %v", err, campaign.ErrInvalidState)
	}
	if _, err := h.svc.Schedule(bg, id, vid, svcRef.Add(time.Hour)); !errors.Is(err, campaign.ErrInvalidState) {
		t.Fatalf("Schedule() after the send started error = %v, want %v", err, campaign.ErrInvalidState)
	}
	if _, err := h.svc.Schedule(bg, id, vid, svcRef.Add(-time.Hour)); !errors.Is(err, campaign.ErrScheduleTooSoon) {
		t.Fatalf("Schedule(past) after the send started error = %v, want %v (window before 409)", err, campaign.ErrScheduleTooSoon)
	}
	h.assertPlan(t, id, campaign.StateSending, nil, want)
}

// REQ-SCH-03 C5
// While scheduled, EditContent, GenerateSegment, StartContentGeneration and Approve (even of the
// approved version) are ErrInvalidState: no version, approval or segment is stored, the LLM is not
// called, the campaign stays scheduled at the same instant. Preview works.
func TestService_ScheduledCampaignIsLocked(t *testing.T) {
	t.Parallel()
	llmc := &schCountingLLM{mock: llm.NewMock(llm.MockOptions{})}
	h := newSchHarness(t, llmc, svcRef, time.Minute)
	bg := context.Background()
	id, vid := h.schApproved(t)
	at := svcRef.Add(5 * time.Hour)
	h.schSchedule(t, id, vid, at)

	before := h.get(t, id)
	versions := h.versionRows(t, id)
	approvals := h.approvalRows(t, id)
	calls := llmc.calls()

	refused := map[string]error{}
	_, refused["EditContent"] = h.svc.EditContent(bg, id, vid, rawOf(t, editContent("planlı")))
	_, refused["GenerateSegment"] = h.svc.GenerateSegment(scen(llm.ScenarioOK), id)
	_, refused["StartContentGeneration"] = h.svc.StartContentGeneration(scen(llm.ScenarioOK), id)
	_, refused["Approve"] = h.svc.Approve(bg, id, vid)
	for op, err := range refused {
		if !errors.Is(err, campaign.ErrInvalidState) {
			t.Errorf("%s while scheduled error = %v, want %v", op, err, campaign.ErrInvalidState)
		}
	}
	if _, err := h.svc.Preview(bg, id, campaign.PreviewQuery{}); err != nil {
		t.Errorf("Preview while scheduled error = %v, want nil", err)
	}
	if t.Failed() {
		t.FailNow()
	}
	if n := llmc.calls(); n != calls {
		t.Fatalf("LLM calls while scheduled = %d, want 0", n-calls)
	}
	if n := len(h.versionRows(t, id)); n != len(versions) {
		t.Fatalf("version rows = %d, want %d", n, len(versions))
	}
	if n := len(h.approvalRows(t, id)); n != len(approvals) {
		t.Fatalf("approval rows = %d, want %d", n, len(approvals))
	}
	after := h.get(t, id)
	if after.Segment.Revision != before.Segment.Revision {
		t.Fatalf("segment revision = %d, want %d", after.Segment.Revision, before.Segment.Revision)
	}
	h.assertPlan(t, id, campaign.StateScheduled, tp(at), 0)
}

// REQ-SCH-01 C8
// While instance A holds a content generation on an approved campaign (stalled_stream), Schedule on a
// second campaign.NewService instance B over the same database is ErrGenerationInProgress; the
// campaign stays approved with scheduled_at NULL. After the release B can schedule.
func TestService_GenerationLockBlocksScheduleAcrossProcesses(t *testing.T) {
	t.Parallel()
	a := newMockHarness(t)
	b := newSchHarness(t, llm.NewMock(llm.MockOptions{}), svcRef, 0)
	bg := context.Background()
	id, vid := a.schApproved(t)
	at := svcRef.Add(time.Hour)

	stop := a.stall(t, id)
	_, err := b.svc.Schedule(bg, id, vid, at)
	stop()
	if !errors.Is(err, campaign.ErrGenerationInProgress) {
		t.Fatalf("Schedule() on the other process during a generation error = %v, want %v", err, campaign.ErrGenerationInProgress)
	}
	a.assertPlan(t, id, campaign.StateApproved, nil, 0)

	b.schSchedule(t, id, vid, at)
	a.assertPlan(t, id, campaign.StateScheduled, tp(at), 0)
}

// ---- campaign.Scheduler ----

// schLogs is a goroutine-safe sink for the scheduler's JSON log.
type schLogs struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *schLogs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

// started returns the "scheduled send started" records, in order.
func (l *schLogs) started(t *testing.T) []map[string]any {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(l.buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line is not JSON (%v): %s", err, line)
		}
		if rec["msg"] == "scheduled send started" {
			out = append(out, rec)
		}
	}
	return out
}

func (l *schLogs) raw() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// newScheduler builds a campaign.Scheduler on its own pool (like another worker process), the given
// clock and a JSON logger.
func newScheduler(t *testing.T, c clock.Clock, cfg campaign.SchedulerConfig) (*campaign.Scheduler, *schLogs) {
	t.Helper()
	logs := &schLogs{}
	s, err := campaign.NewScheduler(cfg, campaign.SchedulerDeps{
		Store:  postgres.NewCampaignStore(testdb.App(t)),
		Clock:  c,
		Logger: slog.New(slog.NewJSONHandler(logs, nil)),
	})
	if err != nil {
		t.Fatalf("NewScheduler(%+v) error = %v", cfg, err)
	}
	return s, logs
}

func runOnce(t *testing.T, s *campaign.Scheduler) int {
	t.Helper()
	n, err := s.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	return n
}

// assertStartedRows: the started campaign has exactly one queued delivery per (channel, customer),
// as many as want.
func (h *harness) assertStartedRows(t *testing.T, id string, want int) {
	t.Helper()
	if n := h.deliveryCount(t, id); n != want {
		t.Fatalf("deliveries = %d, want %d", n, want)
	}
	var notQueued, dups int
	if err := h.pool.QueryRow(t.Context(), `SELECT count(*) FROM deliveries WHERE campaign_id = $1 AND status::text <> 'queued'`, id).Scan(&notQueued); err != nil {
		t.Fatalf("count non-queued deliveries: %v", err)
	}
	if err := h.pool.QueryRow(t.Context(), `SELECT count(*) FROM (
			SELECT 1 FROM deliveries WHERE campaign_id = $1 GROUP BY channel, customer_id HAVING count(*) > 1
		) AS d`, id).Scan(&dups); err != nil {
		t.Fatalf("count duplicate deliveries: %v", err)
	}
	if notQueued != 0 || dups != 0 {
		t.Fatalf("started deliveries: %d not queued, %d duplicate (channel, customer) pairs; want 0 and 0", notQueued, dups)
	}
}

// REQ-SCH-02 C3
// NewScheduler refuses Interval <= 0 and BatchSize < 1 with campaign.ErrInvalidSchedulerConfig.
func TestNewScheduler_InvalidConfig(t *testing.T) {
	pool := testdb.App(t)
	tests := []struct {
		name    string
		cfg     campaign.SchedulerConfig
		wantErr error
	}{
		{name: "valid", cfg: campaign.SchedulerConfig{Interval: time.Second, BatchSize: 1}},
		{name: "zero interval", cfg: campaign.SchedulerConfig{Interval: 0, BatchSize: 10}, wantErr: campaign.ErrInvalidSchedulerConfig},
		{name: "negative interval", cfg: campaign.SchedulerConfig{Interval: -time.Second, BatchSize: 10}, wantErr: campaign.ErrInvalidSchedulerConfig},
		{name: "zero batch size", cfg: campaign.SchedulerConfig{Interval: time.Second, BatchSize: 0}, wantErr: campaign.ErrInvalidSchedulerConfig},
		{name: "negative batch size", cfg: campaign.SchedulerConfig{Interval: time.Second, BatchSize: -1}, wantErr: campaign.ErrInvalidSchedulerConfig},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := campaign.NewScheduler(tc.cfg, campaign.SchedulerDeps{
				Store:  postgres.NewCampaignStore(pool),
				Clock:  clock.NewFake(schRef),
				Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("NewScheduler(%+v) error = %v, want %v", tc.cfg, err, tc.wantErr)
			}
		})
	}
}

// REQ-SCH-02 C1, C2, C4, C5
// Fake clock: 1s before scheduled_at RunOnce starts nothing (0, scheduled, no delivery); at
// scheduled_at, or 2h late, it starts the campaign (1, sending, one queued delivery per channel
// audience member, scheduled_at unchanged). Each start writes one INFO "scheduled send started"
// record with campaign_id, scheduled_at, sending_started_at (= the clock, >= scheduled_at), delay_ms
// (= sending_started_at − scheduled_at; 7200000 for 2h) and deliveries, and no contact data.
func TestScheduler_StartsWhenDue(t *testing.T) {
	tests := []struct {
		name  string
		late  time.Duration
		delay float64
	}{
		{name: "exactly at scheduled_at", late: 0, delay: 0},
		{name: "two hours late after downtime", late: 2 * time.Hour, delay: 7200000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newSchHarness(t, llm.NewMock(llm.MockOptions{}), schRef, time.Minute)
			id, vid := h.schApproved(t)
			at := schRef.Add(time.Hour)
			h.schSchedule(t, id, vid, at)
			s, logs := newScheduler(t, h.clock, campaign.SchedulerConfig{Interval: time.Second, BatchSize: 10})

			h.clock.Set(at.Add(-time.Second))
			if n := runOnce(t, s); n != 0 {
				t.Fatalf("RunOnce() 1s before scheduled_at = %d, want 0", n)
			}
			h.assertPlan(t, id, campaign.StateScheduled, tp(at), 0)

			h.clock.Set(at.Add(tc.late))
			if n := runOnce(t, s); n != 1 {
				t.Fatalf("RunOnce() at scheduled_at + %s = %d, want 1", tc.late, n)
			}
			want := h.audienceTotal(t, id)
			if want == 0 {
				t.Fatal("test setup: the example audience is empty")
			}
			h.assertPlan(t, id, campaign.StateSending, tp(at), want)
			h.assertStartedRows(t, id, want)
			startedAt := h.get(t, id).SendingStartedAt
			if startedAt == nil || !startedAt.Equal(at.Add(tc.late)) || startedAt.Before(at) {
				t.Fatalf("Campaign.SendingStartedAt = %v, want the clock %s (>= scheduled_at)", startedAt, at.Add(tc.late).Format(time.RFC3339))
			}
			if n := runOnce(t, s); n != 0 {
				t.Fatalf("RunOnce() after the start = %d, want 0 (started once)", n)
			}

			recs := logs.started(t)
			if len(recs) != 1 {
				t.Fatalf("scheduled send started records = %d, want 1:\n%s", len(recs), logs.raw())
			}
			rec := recs[0]
			if rec["level"] != "INFO" || rec["campaign_id"] != id {
				t.Fatalf("record level/campaign_id = %v/%v, want INFO/%s", rec["level"], rec["campaign_id"], id)
			}
			for name, wantT := range map[string]time.Time{"scheduled_at": at, "sending_started_at": at.Add(tc.late)} {
				s, _ := rec[name].(string)
				got, err := time.Parse(time.RFC3339Nano, s)
				if err != nil || !strings.HasSuffix(s, "Z") || !got.Equal(wantT) {
					t.Fatalf("record %s = %v, want %s (RFC 3339 UTC)", name, rec[name], wantT.Format(time.RFC3339))
				}
			}
			if rec["delay_ms"] != tc.delay {
				t.Fatalf("record delay_ms = %v, want %v", rec["delay_ms"], tc.delay)
			}
			if rec["deliveries"] != float64(want) {
				t.Fatalf("record deliveries = %v, want %d", rec["deliveries"], want)
			}
			for _, k := range []string{"to", "phone", "email", "body", "first_name", "last_name"} {
				if _, ok := rec[k]; ok {
					t.Fatalf("record carries %q: %v", k, rec)
				}
			}
			for _, frag := range []string{"@example.", "+90500000", "Merhaba"} {
				if strings.Contains(logs.raw(), frag) {
					t.Fatalf("scheduler log contains %q:\n%s", frag, logs.raw())
				}
			}
		})
	}
}

// REQ-SCH-02 C1
// A due campaign whose channel audiences are both empty is started and completed in the same
// operation: RunOnce returns 1, state completed, no delivery, scheduled_at kept.
func TestScheduler_EmptyAudienceCompletes(t *testing.T) {
	h := newSchHarness(t, llm.NewMock(llm.MockOptions{}), schRef, 0)
	id, vid := h.schApprovedEmpty(t)
	at := schRef.Add(10 * time.Minute)
	h.schSchedule(t, id, vid, at)
	s, logs := newScheduler(t, h.clock, campaign.SchedulerConfig{Interval: time.Second, BatchSize: 10})

	h.clock.Set(at)
	if n := runOnce(t, s); n != 1 {
		t.Fatalf("RunOnce() = %d, want 1", n)
	}
	h.assertPlan(t, id, campaign.StateCompleted, tp(at), 0)
	if recs := logs.started(t); len(recs) != 1 || recs[0]["deliveries"] != float64(0) {
		t.Fatalf("scheduled send started records = %v, want one with deliveries 0", recs)
	}
}

// REQ-SCH-02 C1
// Consent is read when the plan starts, not when it was made: after scheduling, one customer of the
// example audience (manifest group istanbul_sms_only) loses SMS consent; the start creates no
// delivery for that customer and one SMS delivery fewer than segment.audience.sms.
// NOT parallel: mutates one customer and restores it in t.Cleanup.
func TestScheduler_ConsentIsReadAtStart(t *testing.T) {
	h := newSchHarness(t, llm.NewMock(llm.MockOptions{}), schRef, 0)
	ids := h.m.Groups["istanbul_sms_only"].CustomerIDs
	if len(ids) == 0 {
		t.Fatal("manifest group istanbul_sms_only is empty")
	}
	cid := ids[0]
	id, vid := h.schApproved(t)
	aud := h.get(t, id).Segment.Audience
	at := schRef.Add(30 * time.Minute)
	h.schSchedule(t, id, vid, at)

	var optIn bool
	var optInAt *time.Time
	if err := h.pool.QueryRow(t.Context(), `SELECT sms_opt_in, sms_opt_in_at FROM customers WHERE id = $1`, cid).Scan(&optIn, &optInAt); err != nil {
		t.Fatalf("read customer consent: %v", err)
	}
	if !optIn {
		t.Fatalf("test setup: customer %s of istanbul_sms_only has no SMS consent", cid)
	}
	t.Cleanup(func() {
		if _, err := h.pool.Exec(context.Background(), `UPDATE customers SET sms_opt_in = $1, sms_opt_in_at = $2 WHERE id = $3`, optIn, optInAt, cid); err != nil {
			t.Errorf("restore consent of %s: %v", cid, err)
		}
	})
	if tag, err := h.pool.Exec(t.Context(), `UPDATE customers SET sms_opt_in = false, sms_opt_in_at = NULL WHERE id = $1`, cid); err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("revoke SMS consent: rows %d, err %v", tag.RowsAffected(), err)
	}

	s, _ := newScheduler(t, h.clock, campaign.SchedulerConfig{Interval: time.Second, BatchSize: 10})
	h.clock.Set(at)
	if n := runOnce(t, s); n != 1 {
		t.Fatalf("RunOnce() = %d, want 1", n)
	}
	var sms, email, mine int
	if err := h.pool.QueryRow(t.Context(), `SELECT
			count(*) FILTER (WHERE channel::text = 'sms'),
			count(*) FILTER (WHERE channel::text = 'email'),
			count(*) FILTER (WHERE customer_id = $2)
		FROM deliveries WHERE campaign_id = $1`, id, cid).Scan(&sms, &email, &mine); err != nil {
		t.Fatalf("count deliveries: %v", err)
	}
	if sms != aud.SMS-1 || email != aud.Email || mine != 0 {
		t.Fatalf("deliveries sms/email/of %s = %d/%d/%d, want %d/%d/0 (consent read at start)", cid, sms, email, mine, aud.SMS-1, aud.Email)
	}
}

// REQ-SCH-02 C6
// Two Scheduler instances (own pools) running RunOnce at the same time on the same database start
// the due campaign once: the returned counts add up to 1 and the rows are those of one send.
func TestScheduler_ConcurrentRunOnceStartsOnce(t *testing.T) {
	h := newSchHarness(t, llm.NewMock(llm.MockOptions{}), schRef, 0)
	id, vid := h.schApproved(t)
	at := schRef.Add(time.Hour)
	h.schSchedule(t, id, vid, at)
	cfg := campaign.SchedulerConfig{Interval: time.Second, BatchSize: 10}
	s1, _ := newScheduler(t, h.clock, cfg)
	s2, _ := newScheduler(t, h.clock, cfg)
	h.clock.Set(at)

	var wg sync.WaitGroup
	start := make(chan struct{})
	ns := make([]int, 2)
	errs := make([]error, 2)
	for i, s := range []*campaign.Scheduler{s1, s2} {
		wg.Add(1)
		go func(i int, s *campaign.Scheduler) {
			defer wg.Done()
			<-start
			ns[i], errs[i] = s.RunOnce(context.Background())
		}(i, s)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("RunOnce() of scheduler %d error = %v", i+1, err)
		}
	}
	if ns[0]+ns[1] != 1 {
		t.Fatalf("RunOnce() counts = %v, want a sum of 1", ns)
	}
	want := h.audienceTotal(t, id)
	h.assertPlan(t, id, campaign.StateSending, tp(at), want)
	h.assertStartedRows(t, id, want)
}

// REQ-SCH-02 C7
// At most BatchSize campaigns start per turn, in scheduled_at order; the rest start next turn.
func TestScheduler_BatchSizeAndOrder(t *testing.T) {
	h := newSchHarness(t, llm.NewMock(llm.MockOptions{}), schRef, 0)
	type plan struct {
		id string
		at time.Time
	}
	// Created in an order different from scheduled_at.
	offsets := []time.Duration{3 * time.Minute, 1 * time.Minute, 2 * time.Minute}
	plans := make([]plan, len(offsets))
	for i, off := range offsets {
		id, vid := h.schApprovedEmpty(t)
		at := schRef.Add(off)
		h.schSchedule(t, id, vid, at)
		plans[i] = plan{id: id, at: at}
	}
	s, _ := newScheduler(t, h.clock, campaign.SchedulerConfig{Interval: time.Second, BatchSize: 2})
	h.clock.Set(schRef.Add(10 * time.Minute))

	if n := runOnce(t, s); n != 2 {
		t.Fatalf("first RunOnce() = %d, want BatchSize 2", n)
	}
	// The two earliest (offsets 1m and 2m) started; the 3m one waits.
	h.assertPlan(t, plans[1].id, campaign.StateCompleted, tp(plans[1].at), 0)
	h.assertPlan(t, plans[2].id, campaign.StateCompleted, tp(plans[2].at), 0)
	h.assertPlan(t, plans[0].id, campaign.StateScheduled, tp(plans[0].at), 0)

	if n := runOnce(t, s); n != 1 {
		t.Fatalf("second RunOnce() = %d, want 1", n)
	}
	h.assertPlan(t, plans[0].id, campaign.StateCompleted, tp(plans[0].at), 0)
	if n := runOnce(t, s); n != 0 {
		t.Fatalf("third RunOnce() = %d, want 0", n)
	}
}

// REQ-SCH-02 C9
// RunOnce with a canceled ctx starts nothing: its error matches context.Canceled, the campaign stays
// scheduled without deliveries; the next RunOnce with a valid ctx starts it.
func TestScheduler_CanceledContextStartsNothing(t *testing.T) {
	h := newSchHarness(t, llm.NewMock(llm.MockOptions{}), schRef, 0)
	id, vid := h.schApproved(t)
	at := schRef.Add(time.Hour)
	h.schSchedule(t, id, vid, at)
	s, _ := newScheduler(t, h.clock, campaign.SchedulerConfig{Interval: time.Second, BatchSize: 10})
	h.clock.Set(at)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.RunOnce(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("RunOnce(canceled ctx) error = %v, want %v", err, context.Canceled)
	}
	h.assertPlan(t, id, campaign.StateScheduled, tp(at), 0)

	if n := runOnce(t, s); n != 1 {
		t.Fatalf("RunOnce() after the canceled turn = %d, want 1", n)
	}
	h.assertPlan(t, id, campaign.StateSending, tp(at), h.audienceTotal(t, id))
}

// REQ-SCH-02 C10
// CancelSchedule racing RunOnce (bounded loop): exactly one wins each round. Either the cancel
// succeeds, RunOnce starts nothing and the campaign is approved without deliveries, or the send
// starts and the cancel is ErrInvalidState.
func TestScheduler_CancelRacingRunOnce(t *testing.T) {
	h := newSchHarness(t, llm.NewMock(llm.MockOptions{}), schRef, 0)
	s, _ := newScheduler(t, h.clock, campaign.SchedulerConfig{Interval: time.Second, BatchSize: 10})
	id, vid := h.schApprovedEmpty(t)

	const rounds = 15
	for i := range rounds {
		at := h.clock.Now()
		h.schSchedule(t, id, vid, at)

		var wg sync.WaitGroup
		start := make(chan struct{})
		var n int
		var runErr, cancelErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			n, runErr = s.RunOnce(context.Background())
		}()
		go func() {
			defer wg.Done()
			<-start
			_, cancelErr = h.svc.CancelSchedule(context.Background(), id)
		}()
		close(start)
		wg.Wait()

		if runErr != nil {
			t.Fatalf("round %d: RunOnce() error = %v", i, runErr)
		}
		switch {
		case cancelErr == nil:
			if n != 0 {
				t.Fatalf("round %d: cancel succeeded but RunOnce started %d campaigns", i, n)
			}
			h.assertPlan(t, id, campaign.StateApproved, nil, 0)
		case errors.Is(cancelErr, campaign.ErrInvalidState):
			if n != 1 {
				t.Fatalf("round %d: cancel lost but RunOnce returned %d, want 1", i, n)
			}
			h.assertPlan(t, id, campaign.StateCompleted, tp(at), 0)
			id, vid = h.schApprovedEmpty(t) // the started campaign is done; race on a fresh one
		default:
			t.Fatalf("round %d: CancelSchedule() error = %v, want nil or %v", i, cancelErr, campaign.ErrInvalidState)
		}
		h.clock.Advance(time.Second)
	}
}

// REQ-SCH-03 C7
// "Send now" racing the scheduler start (bounded loop, 20 rounds): on a due scheduled campaign,
// campaign.Service.Send (the harness service) and Scheduler.RunOnce (its own NewScheduler and pool)
// run at the same time on the same database. Exactly one wins each round: either Send succeeds and
// RunOnce returns 0, or RunOnce returns 1 and Send is campaign.ErrInvalidState. Either way the
// deliveries are those of one send (the channel audiences, no duplicate (channel, customer)) and the
// campaign is sending or completed, never left scheduled.
func TestScheduler_SendNowRacingRunOnce(t *testing.T) {
	h := newSchHarness(t, llm.NewMock(llm.MockOptions{}), schRef, 0)
	s, _ := newScheduler(t, h.clock, campaign.SchedulerConfig{Interval: time.Second, BatchSize: 10})

	const rounds = 20
	for i := range rounds {
		id, vid := h.schApproved(t)
		h.schSchedule(t, id, vid, h.clock.Now()) // due at once (no min lead)

		var wg sync.WaitGroup
		start := make(chan struct{})
		var n int
		var runErr, sendErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			n, runErr = s.RunOnce(context.Background())
		}()
		go func() {
			defer wg.Done()
			<-start
			_, sendErr = h.svc.Send(context.Background(), id, vid)
		}()
		close(start)
		wg.Wait()

		if runErr != nil {
			t.Fatalf("round %d: RunOnce() error = %v", i, runErr)
		}
		switch {
		case sendErr == nil:
			if n != 0 {
				t.Fatalf("round %d: Send succeeded but RunOnce started %d campaigns, want 0", i, n)
			}
		case errors.Is(sendErr, campaign.ErrInvalidState):
			if n != 1 {
				t.Fatalf("round %d: Send lost but RunOnce returned %d, want 1", i, n)
			}
		default:
			t.Fatalf("round %d: Send() error = %v, want nil or %v", i, sendErr, campaign.ErrInvalidState)
		}

		c := h.get(t, id)
		if c.State != campaign.StateSending && c.State != campaign.StateCompleted {
			t.Fatalf("round %d: campaign state = %s, want sending or completed", i, c.State)
		}
		want := h.audienceTotal(t, id)
		if want == 0 {
			t.Fatal("test setup: the example audience is empty")
		}
		h.assertStartedRows(t, id, want)
		h.clock.Advance(time.Second)
	}
}

// REQ-SCH-02 C3
// Run calls RunOnce at once (before waiting on the injected clock) and then every Interval; it
// returns ctx.Err() when ctx ends.
func TestScheduler_RunTicksOnTheInjectedClock(t *testing.T) {
	h := newSchHarness(t, llm.NewMock(llm.MockOptions{}), schRef, 0)
	first, firstV := h.schApprovedEmpty(t)
	second, secondV := h.schApprovedEmpty(t)
	h.schSchedule(t, first, firstV, schRef)
	secondAt := schRef.Add(time.Second)
	h.schSchedule(t, second, secondV, secondAt)
	s, _ := newScheduler(t, h.clock, campaign.SchedulerConfig{Interval: time.Second, BatchSize: 10})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	wait := func(what string) {
		t.Helper()
		wctx, wcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer wcancel()
		if err := h.clock.BlockUntilWaiters(wctx, 1); err != nil {
			t.Fatalf("Run did not wait on the injected clock %s: %v", what, err)
		}
	}
	wait("after the first turn")
	h.assertPlan(t, first, campaign.StateCompleted, tp(schRef), 0)
	h.assertPlan(t, second, campaign.StateScheduled, tp(secondAt), 0)

	h.clock.Advance(time.Second)
	wait("after the second turn")
	h.assertPlan(t, second, campaign.StateCompleted, tp(secondAt), 0)

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run() error = %v, want %v", err, context.Canceled)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return within 10s after ctx was canceled")
	}
}

// REQ-SCH-01 C12
// Database constraints (scc_app): campaigns_scheduled_at_check refuses a scheduled campaign without
// scheduled_at and an approved one with it; campaigns_approval_present_check refuses a scheduled
// campaign without approved_content_version_id; the state trigger refuses scheduled ->
// content_ready / audience_ready / completed via UPDATE campaigns SET state. All SQLSTATE 23514.
// Each statement runs in a transaction that is rolled back.
func TestCampaigns_ScheduleConstraints(t *testing.T) {
	t.Parallel()
	h := newSchHarness(t, llm.NewMock(llm.MockOptions{}), svcRef, 0)
	apprID, _ := h.schApproved(t)
	schedID, schedV := h.schApproved(t)
	h.schSchedule(t, schedID, schedV, svcRef.Add(time.Hour))

	tests := []struct {
		name       string
		sql        string
		id         string
		constraint string
	}{
		{name: "scheduled without scheduled_at", sql: `UPDATE campaigns SET scheduled_at = NULL WHERE id = $1`, id: schedID, constraint: "campaigns_scheduled_at_check"},
		{name: "approved with scheduled_at", sql: `UPDATE campaigns SET scheduled_at = now() WHERE id = $1`, id: apprID, constraint: "campaigns_scheduled_at_check"},
		{name: "scheduled without approved version", sql: `UPDATE campaigns SET approved_content_version_id = NULL WHERE id = $1`, id: schedID, constraint: "campaigns_approval_present_check"},
		// State trigger: the constraint name is not given, only the SQLSTATE.
		{name: "trigger refuses scheduled to content_ready", sql: `UPDATE campaigns SET state = 'content_ready' WHERE id = $1`, id: schedID},
		{name: "trigger refuses scheduled to audience_ready", sql: `UPDATE campaigns SET state = 'audience_ready' WHERE id = $1`, id: schedID},
		{name: "trigger refuses scheduled to completed", sql: `UPDATE campaigns SET state = 'completed' WHERE id = $1`, id: schedID},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := h.pool.Begin(t.Context())
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer func() { _ = tx.Rollback(context.Background()) }()
			_, err = tx.Exec(t.Context(), tc.sql, tc.id)
			pgErr := testdb.PgError(err)
			if pgErr == nil || pgErr.Code != "23514" || (tc.constraint != "" && pgErr.ConstraintName != tc.constraint) {
				t.Fatalf("%s: error = %v, want SQLSTATE 23514 (constraint %q)", tc.sql, err, tc.constraint)
			}
		})
	}
	h.assertPlan(t, schedID, campaign.StateScheduled, tp(svcRef.Add(time.Hour)), 0)
	h.assertPlan(t, apprID, campaign.StateApproved, nil, 0)
}
