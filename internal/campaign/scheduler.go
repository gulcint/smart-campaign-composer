package campaign

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/gulcint/smart-campaign-composer/internal/clock"
)

// ---- Scheduler: starts due plans (cmd/worker) ----

type SchedulerConfig struct {
	Interval  time.Duration // SCHEDULER_INTERVAL, default 1s; must be > 0
	BatchSize int           // max campaigns started per pass (cmd/worker: 10); must be >= 1
}

type SchedulerDeps struct {
	Store  Store        // postgres.NewCampaignStore(pool), the same store type as NewService
	Clock  clock.Clock  // "now" for due-ness and sending_started_at; nil => clock.Real()
	Logger *slog.Logger // nil => slog.Default()
}

var ErrInvalidSchedulerConfig = errors.New("campaign: invalid scheduler config")

// Scheduler starts the sends of due plans. It keeps no timer per campaign: the campaigns row is
// the only source of truth, so a restart loses nothing and a plan that became due while the worker
// was down is started by the first pass after it is back, however late (D62).
type Scheduler struct {
	cfg  SchedulerConfig
	deps SchedulerDeps
}

// NewScheduler validates cfg (Interval <= 0 or BatchSize < 1 => ErrInvalidSchedulerConfig).
func NewScheduler(cfg SchedulerConfig, deps SchedulerDeps) (*Scheduler, error) {
	if cfg.Interval <= 0 || cfg.BatchSize < 1 {
		return nil, fmt.Errorf("%w: interval %s, batch size %d", ErrInvalidSchedulerConfig, cfg.Interval, cfg.BatchSize)
	}
	if deps.Store == nil {
		return nil, fmt.Errorf("%w: missing store", ErrInvalidSchedulerConfig)
	}
	if deps.Clock == nil {
		deps.Clock = clock.Real()
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	return &Scheduler{cfg: cfg, deps: deps}, nil
}

// errNotDue and errLeaseHeld make StartDueCampaign skip a claimed campaign for this pass: a
// re-plan to a later time or a cancel won the row lock, or (defensively; no generation can start
// in scheduled) a generation lease is on the row.
var (
	errNotDue    = errors.New("campaign: plan no longer due")
	errLeaseHeld = errors.New("campaign: generation lease on a scheduled campaign")
)

// RunOnce is one pass at now = Clock.Now(): up to BatchSize times, one transaction claims one due
// campaign (FOR UPDATE SKIP LOCKED, excluding those failed or skipped in this pass), re-checks it
// under the row lock and starts it exactly like Service.Send, except that scheduled_at is kept. It
// returns the number of campaigns started; err is non-nil only when the pass could not run (ctx
// done, database unreachable). Per-campaign failures are logged and leave the campaign scheduled.
func (s *Scheduler) RunOnce(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	now := s.deps.Clock.Now().UTC().Truncate(time.Microsecond)
	check := func(lc LockedCampaign) error {
		if lc.State != StateScheduled || lc.ScheduledAt == nil || lc.ScheduledAt.After(now) {
			return errNotDue
		}
		if lc.CheckNoGeneration(now) != nil {
			return errLeaseHeld
		}
		_, err := Transition(lc.State, EventSendAccepted)
		return err
	}
	var exclude []string
	started := 0
	for range s.cfg.BatchSize {
		if err := ctx.Err(); err != nil {
			return started, err
		}
		st, err := s.deps.Store.StartDueCampaign(ctx, now, exclude, check)
		if err != nil && ctx.Err() != nil {
			return started, ctx.Err()
		}
		if st.CampaignID == "" {
			// Nothing was claimed: no due campaign left, or the claim itself failed.
			return started, err
		}
		if err != nil {
			exclude = append(exclude, st.CampaignID)
			if errors.Is(err, errNotDue) || errors.Is(err, errLeaseHeld) {
				s.deps.Logger.Debug("scheduled send skipped", slog.String("campaign_id", st.CampaignID))
				continue
			}
			s.deps.Logger.Error("scheduled send failed", slog.String("campaign_id", st.CampaignID),
				slog.String("error", "the send could not be started; the campaign stays scheduled"))
			continue
		}
		started++
		s.deps.Logger.Info("scheduled send started", slog.String("campaign_id", st.CampaignID),
			slog.String("scheduled_at", st.ScheduledAt.UTC().Format(time.RFC3339Nano)),
			slog.String("sending_started_at", st.StartedAt.UTC().Format(time.RFC3339Nano)),
			slog.Int64("delay_ms", max(st.StartedAt.Sub(st.ScheduledAt).Milliseconds(), 0)),
			slog.Int("deliveries", st.Deliveries.Total))
	}
	return started, nil
}

// Run calls RunOnce immediately and then every Interval until ctx is done; it returns ctx.Err().
// A RunOnce error is logged and the loop continues.
func (s *Scheduler) Run(ctx context.Context) error {
	for {
		if _, err := s.RunOnce(ctx); err != nil && ctx.Err() == nil {
			s.deps.Logger.Error("scheduler pass failed", slog.String("error", err.Error()))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.deps.Clock.After(s.cfg.Interval):
		}
	}
}
