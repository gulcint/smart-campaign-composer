// Package delivery is the send queue: retry policy, rate limiter, dispatcher and the queue store
// port (docs/contracts/go-interfaces.md §delivery).
package delivery

import (
	"math/rand/v2"
	"sync"
	"time"
)

// RetryConfig bounds retries per delivery (REQ-SND-04, PLAN §0.4).
type RetryConfig struct {
	MaxAttempts     int           // 5: failed attempts incl. the first; the 5th failure => failed
	BaseDelay       time.Duration // 1s
	MaxDelay        time.Duration // 60s
	MaxRateLimited  int           // 20: the 20th 429 => failed
	RateLimitWindow time.Duration // 10m, measured from FirstAttemptAt
}

// DefaultRetryConfig returns the documented defaults.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts:     5,
		BaseDelay:       time.Second,
		MaxDelay:        60 * time.Second,
		MaxRateLimited:  20,
		RateLimitWindow: 10 * time.Minute,
	}
}

// Attempt describes one finished provider request.
type Attempt struct {
	StatusCode     int           // 0 when Err != nil
	RetryAfter     time.Duration // parsed Retry-After (see provider.ParseRetryAfter)
	HasRetryAfter  bool
	Err            error // transport error: provider.ErrTimeout / provider.ErrConnection
	FailedAttempts int   // failed (non-429) attempts BEFORE this one
	RateLimited    int   // 429 responses BEFORE this one
	FirstAttemptAt time.Time
	Now            time.Time
}

type Action int

const (
	ActionMarkSent Action = iota + 1
	ActionRetry
	ActionFail
)

// CountKind tells which per-delivery counter a provider response increments.
type CountKind int

const (
	CountNone        CountKind = iota // 2xx, other 4xx
	CountFailure                      // 5xx, 408, timeout, connection error => failed_attempts+1
	CountRateLimited                  // 429 => rate_limited_count+1
)

type Decision struct {
	Action        Action
	Count         CountKind     // which counter this response increments (also when Action == ActionFail)
	Delay         time.Duration // ActionRetry only
	NextAttemptAt time.Time     // ActionRetry only: Now + Delay
	FailureReason FailureReason // ActionFail only
}

// RetryPolicy decides what happens after each provider response. Safe for concurrent use.
type RetryPolicy struct {
	cfg RetryConfig
	mu  sync.Mutex // guards rng
	rng *rand.Rand
}

// NewRetryPolicy uses rng exclusively for jitter (deterministic for a fixed seed and call order).
func NewRetryPolicy(cfg RetryConfig, rng *rand.Rand) *RetryPolicy {
	return &RetryPolicy{cfg: cfg, rng: rng}
}

// Next decides what happens after a provider response (rules in go-interfaces.md §delivery).
func (p *RetryPolicy) Next(a Attempt) Decision {
	switch {
	case a.Err == nil && a.StatusCode >= 200 && a.StatusCode <= 299:
		return Decision{Action: ActionMarkSent, Count: CountNone}

	case a.Err == nil && a.StatusCode == 429:
		n := a.RateLimited + 1
		deadline := a.FirstAttemptAt.Add(p.cfg.RateLimitWindow)
		if n >= p.cfg.MaxRateLimited || !a.Now.Before(deadline) {
			return fail(CountRateLimited, FailureRateLimitBudget)
		}
		var delay time.Duration
		if a.HasRetryAfter {
			delay = max(a.RetryAfter, 0)
		} else {
			delay = p.Jitter(n)
		}
		next := a.Now.Add(delay)
		if next.After(deadline) {
			return fail(CountRateLimited, FailureRateLimitBudget)
		}
		return Decision{Action: ActionRetry, Count: CountRateLimited, Delay: delay, NextAttemptAt: next}

	case a.Err != nil || a.StatusCode == 408 || (a.StatusCode >= 500 && a.StatusCode <= 599):
		n := a.FailedAttempts + 1
		if n >= p.cfg.MaxAttempts {
			return fail(CountFailure, FailureMaxAttempts)
		}
		delay := p.Jitter(n)
		return Decision{Action: ActionRetry, Count: CountFailure, Delay: delay, NextAttemptAt: a.Now.Add(delay)}

	default:
		return fail(CountNone, FailureProviderRejected)
	}
}

func fail(c CountKind, r FailureReason) Decision {
	return Decision{Action: ActionFail, Count: c, FailureReason: r}
}

// Jitter returns full jitter for the n-th (1-based) retry: uniform in [0, min(MaxDelay,
// BaseDelay·2^(n-1))), one rng draw per call.
func (p *RetryPolicy) Jitter(n int) time.Duration {
	limit := p.cfg.BaseDelay
	for i := 1; i < n && limit < p.cfg.MaxDelay; i++ {
		limit *= 2
	}
	limit = min(limit, p.cfg.MaxDelay)
	if limit <= 0 {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return time.Duration(p.rng.Int64N(int64(limit)))
}
