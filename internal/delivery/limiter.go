package delivery

import (
	"context"
	"sync"
	"time"

	"github.com/gulcint/smart-campaign-composer/internal/clock"
)

// RateLimiter gates provider request STARTS for one channel.
type RateLimiter interface {
	Wait(ctx context.Context) error // returns nil when the caller may start one request; ctx.Err() otherwise
}

// TokenBucket is a burst-1 limiter: consecutive permits are at least interval apart on its clock.
type TokenBucket struct {
	clock    clock.Clock
	interval time.Duration

	mu   sync.Mutex
	next time.Time // earliest time of the next permit; zero before the first one
}

// NewTokenBucket returns a burst-1 limiter: consecutive permits are at least interval apart on c
// (interval = time.Second / DISPATCH_RATE_PER_SECOND). The first permit is immediate. In every
// half-open window [t, t+1s) at most 1s/interval permits are granted. Safe for concurrent use;
// waiting uses c.After.
func NewTokenBucket(c clock.Clock, interval time.Duration) *TokenBucket {
	return &TokenBucket{clock: c, interval: interval}
}

// Wait blocks until the caller may start one request. A permit is granted only when the clock has
// reached the earliest allowed time at the moment of granting (no reservation ahead of time), so
// grants stay interval apart even if several waiters wake at once.
func (b *TokenBucket) Wait(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		b.mu.Lock()
		now := b.clock.Now()
		if b.next.IsZero() || !now.Before(b.next) {
			b.next = now.Add(b.interval)
			b.mu.Unlock()
			return nil
		}
		wait := b.next.Sub(now)
		b.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-b.clock.After(wait):
		}
	}
}
