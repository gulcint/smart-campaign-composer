package delivery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gulcint/smart-campaign-composer/internal/clock"
	"github.com/gulcint/smart-campaign-composer/internal/content"
	"github.com/gulcint/smart-campaign-composer/internal/provider"
)

type DispatcherConfig struct {
	Channel           Channel
	BatchSize         int           // 50
	Concurrency       int           // 32 in-flight requests max
	PollInterval      time.Duration // 200ms when nothing is due
	Lease             time.Duration // 60s
	LockRetryInterval time.Duration // 1s between lock attempts when not leader
}

type DispatcherDeps struct {
	Store    Store
	Provider provider.Client
	// Limiter gates request starts. The per-channel limit holds across dispatchers only if they
	// SHARE one RateLimiter (unit tests) or if only one dispatcher sends (production: advisory lock).
	Limiter  RateLimiter
	Policy   *RetryPolicy
	Clock    clock.Clock
	Logger   *slog.Logger
	Recorder Recorder // optional (nil => no metrics); *metrics.WorkerMetrics implements it
}

// Recorder receives dispatcher observations (worker /metrics, metrics.md §2).
type Recorder interface {
	SetLeader(channel string, leader bool)                                // scc_dispatcher_leader{channel}
	ObserveProviderResponse(channel string, code string, d time.Duration) // scc_provider_requests_total, duration
	ObserveLimiterWait(channel string, d time.Duration)                   // scc_rate_limiter_wait_seconds
}

// Dispatcher sends one channel's deliveries.
type Dispatcher struct {
	cfg    DispatcherConfig
	deps   DispatcherDeps
	leader atomic.Bool
}

// Defaults applied to zero DispatcherConfig fields.
const (
	defaultBatchSize         = 50
	defaultConcurrency       = 32
	defaultPollInterval      = 200 * time.Millisecond
	defaultLease             = 60 * time.Second
	defaultLockRetryInterval = time.Second
)

// NewDispatcher validates cfg and deps (ErrInvalidConfig). Zero numeric fields take the
// documented defaults; a nil Logger discards logs.
func NewDispatcher(cfg DispatcherConfig, deps DispatcherDeps) (*Dispatcher, error) {
	if cfg.Channel != ChannelSMS && cfg.Channel != ChannelEmail {
		return nil, fmt.Errorf("%w: channel %q", ErrInvalidConfig, cfg.Channel)
	}
	if cfg.BatchSize < 0 || cfg.Concurrency < 0 || cfg.PollInterval < 0 || cfg.Lease < 0 || cfg.LockRetryInterval < 0 {
		return nil, fmt.Errorf("%w: negative value", ErrInvalidConfig)
	}
	if deps.Store == nil || deps.Provider == nil || deps.Limiter == nil || deps.Policy == nil || deps.Clock == nil {
		return nil, fmt.Errorf("%w: missing dependency", ErrInvalidConfig)
	}
	cfg.BatchSize = orDefault(cfg.BatchSize, defaultBatchSize)
	cfg.Concurrency = orDefault(cfg.Concurrency, defaultConcurrency)
	cfg.PollInterval = orDefault(cfg.PollInterval, defaultPollInterval)
	cfg.Lease = orDefault(cfg.Lease, defaultLease)
	cfg.LockRetryInterval = orDefault(cfg.LockRetryInterval, defaultLockRetryInterval)
	if deps.Logger == nil {
		deps.Logger = slog.New(slog.DiscardHandler)
	}
	return &Dispatcher{cfg: cfg, deps: deps}, nil
}

func orDefault[T int | time.Duration](v, def T) T {
	if v == 0 {
		return def
	}
	return v
}

// Leader reports whether this dispatcher currently holds the channel's advisory lock.
func (d *Dispatcher) Leader() bool { return d.leader.Load() }

func (d *Dispatcher) setLeader(v bool) {
	d.leader.Store(v)
	if d.deps.Recorder != nil {
		d.deps.Recorder.SetLeader(string(d.cfg.Channel), v)
	}
}

// Run loops until ctx is done (returns ctx.Err()): acquires the channel lock (only the holder
// sends), reclaims expired leases, claims due jobs and processes them.
func (d *Dispatcher) Run(ctx context.Context) error {
	d.setLeader(false)
	for {
		release, acquired, err := d.deps.Store.TryLockChannel(ctx, d.cfg.Channel)
		if err != nil && ctx.Err() == nil {
			d.deps.Logger.Error("dispatcher lock failed", slog.String("channel", string(d.cfg.Channel)), slog.String("error", err.Error()))
		}
		if err == nil && acquired {
			d.setLeader(true)
			d.deps.Logger.Info("dispatcher leader", slog.String("channel", string(d.cfg.Channel)))
			err = d.lead(ctx)
			d.setLeader(false)
			release()
			return err
		}
		if err := clock.Sleep(ctx, d.deps.Clock, d.cfg.LockRetryInterval); err != nil {
			return err
		}
	}
}

// lead runs the send loop while holding the channel lock.
func (d *Dispatcher) lead(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := d.deps.Store.ReclaimExpired(ctx, d.cfg.Channel, d.deps.Clock.Now()); err != nil && ctx.Err() == nil {
			d.deps.Logger.Error("reclaim expired leases failed", slog.String("channel", string(d.cfg.Channel)), slog.String("error", err.Error()))
		}
		n, err := d.ProcessOnce(ctx)
		if err != nil && ctx.Err() == nil {
			d.deps.Logger.Error("dispatch batch failed", slog.String("channel", string(d.cfg.Channel)), slog.String("error", err.Error()))
		}
		if n == 0 || err != nil {
			if err := clock.Sleep(ctx, d.deps.Clock, d.cfg.PollInterval); err != nil {
				return err
			}
		}
	}
}

// ProcessOnce claims one batch at Clock.Now() and processes it to completion WITHOUT taking the
// channel lock (test seam). For each job, in this order:
//  1. !job.Consent || job.To == "" => MarkFailed(recipient_unavailable), no provider call, no permit;
//  2. render + content.CheckRendered fails => MarkFailed(content_too_long), no provider call, no permit;
//  3. Limiter.Wait -> Provider.Send -> Policy.Next -> Store.MarkSent / ScheduleRetry / MarkFailed.
//
// Returns the number of jobs processed. Once a job's provider request has started, it is finished
// and recorded even if ctx is cancelled meanwhile (graceful shutdown); jobs still waiting for a
// permit are left in sending and come back when their lease expires.
func (d *Dispatcher) ProcessOnce(ctx context.Context) (int, error) {
	now := d.deps.Clock.Now()
	jobs, err := d.deps.Store.Claim(ctx, d.cfg.Channel, now, d.cfg.BatchSize, now.Add(d.cfg.Lease))
	if err != nil {
		return 0, fmt.Errorf("delivery: claim: %w", err)
	}
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
		sem  = make(chan struct{}, d.cfg.Concurrency)
	)
	for _, job := range jobs {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			if err := d.process(ctx, job); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	return len(jobs), errors.Join(errs...)
}

func (d *Dispatcher) process(ctx context.Context, job Job) error {
	if !job.Consent || job.To == "" {
		return d.fail(ctx, job, FailureRecipientUnavailable, CountNone, 0, string(FailureRecipientUnavailable))
	}
	rendered, err := content.RenderContent(job.Content, job.Recipient)
	if err == nil {
		err = content.CheckRendered(rendered)
	}
	if err != nil {
		return d.fail(ctx, job, FailureContentTooLong, CountNone, 0, string(FailureContentTooLong))
	}
	msg := provider.Message{DeliveryID: job.ID, Channel: provider.Channel(job.Channel), To: job.To}
	if job.Channel == ChannelSMS {
		msg.SMSBody = rendered.SMSText
	} else {
		msg.Subject = rendered.EmailSubject
		msg.Text = rendered.EmailText
		msg.HTML = content.RenderEmailHTML(rendered.EmailSubject, rendered.EmailText)
	}

	waitStart := d.deps.Clock.Now()
	if err := d.deps.Limiter.Wait(ctx); err != nil {
		return err // not started: the lease brings the job back
	}
	start := d.deps.Clock.Now()
	d.observeLimiterWait(start.Sub(waitStart))

	// The request has started: finish and record it even if ctx is cancelled now.
	sendCtx := context.WithoutCancel(ctx)
	resp, sendErr := d.deps.Provider.Send(sendCtx, msg)
	now := d.deps.Clock.Now()
	d.observeResponse(resp.StatusCode, sendErr, now.Sub(start))

	dec := d.deps.Policy.Next(Attempt{
		StatusCode:     resp.StatusCode,
		RetryAfter:     resp.RetryAfter,
		HasRetryAfter:  resp.HasRetryAfter,
		Err:            sendErr,
		FailedAttempts: job.FailedAttempts,
		RateLimited:    job.RateLimited,
		FirstAttemptAt: job.FirstAttemptAt,
		Now:            now,
	})
	status := resp.StatusCode
	if sendErr != nil {
		status = 0
	}
	lastError := lastErrorOf(status, sendErr)

	switch dec.Action {
	case ActionMarkSent:
		err := d.deps.Store.MarkSent(sendCtx, job.ID, SentUpdate{At: now, HTTPStatus: status, ProviderMessageID: resp.ProviderMessageID})
		if err != nil {
			return d.storeError(job, "mark sent", err)
		}
		d.deps.Logger.Info("delivery sent", d.attrs(job, status)...)
	case ActionRetry:
		err := d.deps.Store.ScheduleRetry(sendCtx, job.ID, RetryUpdate{
			At: now, NextAttemptAt: dec.NextAttemptAt, Count: dec.Count, HTTPStatus: status, LastError: lastError,
		})
		if err != nil {
			return d.storeError(job, "schedule retry", err)
		}
		d.deps.Logger.Warn("delivery retry scheduled", append(d.attrs(job, status),
			slog.Time("next_attempt_at", dec.NextAttemptAt), slog.String("reason", lastError))...)
	default:
		return d.fail(sendCtx, job, dec.FailureReason, dec.Count, status, lastError)
	}
	return nil
}

// fail marks job failed and logs it.
func (d *Dispatcher) fail(ctx context.Context, job Job, reason FailureReason, count CountKind, status int, lastError string) error {
	err := d.deps.Store.MarkFailed(context.WithoutCancel(ctx), job.ID, FailedUpdate{
		At: d.deps.Clock.Now(), Reason: reason, Count: count, HTTPStatus: status, LastError: lastError,
	})
	if err != nil {
		return d.storeError(job, "mark failed", err)
	}
	d.deps.Logger.Error("delivery failed", append(d.attrs(job, status), slog.String("failure_reason", string(reason)))...)
	return nil
}

// storeError logs a lost lease (another dispatcher owns the row now) and returns other errors.
func (d *Dispatcher) storeError(job Job, op string, err error) error {
	if errors.Is(err, ErrLeaseLost) {
		d.deps.Logger.Warn("delivery lease lost", slog.String("delivery_id", job.ID), slog.String("channel", string(job.Channel)))
		return nil
	}
	return fmt.Errorf("delivery %s: %s: %w", job.ID, op, err)
}

// attrs are the REQ-SEC-01 fields shared by every delivery log record. They never carry the
// recipient address, name or message text.
func (d *Dispatcher) attrs(job Job, status int) []any {
	return []any{
		slog.String("delivery_id", job.ID),
		slog.String("campaign_id", job.CampaignID),
		slog.String("customer_id", job.CustomerID),
		slog.String("channel", string(job.Channel)),
		slog.Int("attempt", job.Attempts),
		slog.Int("http_status", status),
	}
}

func (d *Dispatcher) observeLimiterWait(w time.Duration) {
	if d.deps.Recorder != nil {
		d.deps.Recorder.ObserveLimiterWait(string(d.cfg.Channel), w)
	}
}

func (d *Dispatcher) observeResponse(status int, err error, dur time.Duration) {
	if d.deps.Recorder != nil {
		d.deps.Recorder.ObserveProviderResponse(string(d.cfg.Channel), responseCode(status, err), dur)
	}
}

// responseCode is the closed code label of scc_provider_requests_total.
func responseCode(status int, err error) string {
	switch {
	case err != nil && errors.Is(err, provider.ErrTimeout):
		return "timeout"
	case err != nil:
		return "connection_error"
	case status >= 200 && status <= 299:
		return "2xx"
	case status == 408, status == 429:
		return strconv.Itoa(status)
	case status >= 500:
		return "5xx"
	default:
		return "4xx"
	}
}

// lastErrorOf is the closed deliveries.last_error value of a provider response.
func lastErrorOf(status int, err error) string {
	switch {
	case err != nil && errors.Is(err, provider.ErrTimeout):
		return "timeout"
	case err != nil:
		return "connection_error"
	case status >= 200 && status <= 299:
		return ""
	default:
		return "http_" + strconv.Itoa(status)
	}
}
