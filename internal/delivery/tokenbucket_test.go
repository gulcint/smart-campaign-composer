package delivery_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/gulcint/smart-campaign-composer/internal/delivery"
)

// REQ-SND-02
// delivery.NewTokenBucket gives its first permit at once: Wait returns nil without the clock moving.
func TestTokenBucket_FirstPermitIsImmediate(t *testing.T) {
	t.Parallel()
	for _, interval := range []time.Duration{10 * time.Millisecond, 25 * time.Millisecond} {
		t.Run("interval "+interval.String(), func(t *testing.T) {
			t.Parallel()
			p := newProbe()
			tb := delivery.NewTokenBucket(p, interval)
			ctx, cancel := context.WithTimeout(context.Background(), guard)
			defer cancel()
			if err := tb.Wait(ctx); err != nil {
				t.Fatalf("first Wait() error = %v, want nil without advancing the clock", err)
			}
			if got := p.Now(); !got.Equal(t0) {
				t.Fatalf("clock after the first permit = %v, want it unchanged at %v", got, t0)
			}
		})
	}
}

// REQ-SND-02
// One waiter asks for permits back to back: every half-open 1 s window [t, t+1s) holds at most
// 1s/interval permits (100 for DISPATCH_RATE_PER_SECOND=100, i.e. interval 10ms; 40 for 25ms),
// consecutive permits are at least interval apart on the clock and the first one is at t0.
func TestTokenBucket_AtMostRatePerWindow(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		interval time.Duration
		permits  int
		limit    int
	}{
		{name: "100 per second (10ms)", interval: 10 * time.Millisecond, permits: 250, limit: 100},
		{name: "40 per second (25ms)", interval: 25 * time.Millisecond, permits: 120, limit: 40},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := newProbe()
			tb := delivery.NewTokenBucket(p, tc.interval)
			p.startWaiters(tb, 1, tc.permits, 0)
			ts := times(p.drive(t, time.Millisecond, tc.permits))

			if !ts[0].Equal(t0) {
				t.Fatalf("first permit at %v, want %v (burst 1: the first permit is immediate)", ts[0], t0)
			}
			if n, start := maxPerWindow(ts, time.Second); n > tc.limit {
				t.Fatalf("%d permits in [%v, +1s), want at most %d", n, start, tc.limit)
			}
			if gap, i := minGap(ts); gap < tc.interval {
				t.Fatalf("permits %d and %d are %v apart, want at least %v", i-1, i, gap, tc.interval)
			}
		})
	}
}

// REQ-SND-02
// The limit does not depend on how many workers run: several goroutines (each standing for a worker
// or dispatcher) share one delivery.RateLimiter, and all their permits together still respect
// <= 100 per [t, t+1s) and >= 10ms between consecutive permits; every waiter gets all its permits.
func TestTokenBucket_SharedByConcurrentWaiters(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		waiters   int
		perWaiter int
	}{
		{name: "2 dispatchers", waiters: 2, perWaiter: 120},
		{name: "4 dispatchers", waiters: 4, perWaiter: 60},
		{name: "8 dispatchers", waiters: 8, perWaiter: 30},
	}
	const interval = 10 * time.Millisecond
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := newProbe()
			var shared delivery.RateLimiter = delivery.NewTokenBucket(p, interval)
			p.startWaiters(shared, tc.waiters, tc.perWaiter, 0)
			got := p.drive(t, time.Millisecond, tc.waiters*tc.perWaiter)

			ts := times(got)
			if n, start := maxPerWindow(ts, time.Second); n > 100 {
				t.Fatalf("%d permits across %d waiters in [%v, +1s), want at most 100", n, tc.waiters, start)
			}
			if gap, i := minGap(ts); gap < interval {
				t.Fatalf("permits %d and %d are %v apart across waiters, want at least %v", i-1, i, gap, interval)
			}
			perWho := map[int]int{}
			for _, g := range got {
				perWho[g.who]++
			}
			for w := 0; w < tc.waiters; w++ {
				if perWho[w] != tc.perWaiter {
					t.Fatalf("waiter %d got %d permits, want %d", w, perWho[w], tc.perWaiter)
				}
			}
		})
	}
}

// REQ-SND-02
// SMS and email limits do not affect each other: each channel has its own NewTokenBucket. The email
// permits are given at exactly the same clock times whether or not the SMS bucket is just as busy
// on the same clock, and each channel stays within 100 per [t, t+1s) and 10ms spacing.
func TestTokenBucket_ChannelsAreIndependent(t *testing.T) {
	t.Parallel()
	const (
		interval = 10 * time.Millisecond
		perChan  = 150
		sms      = 0
		email    = 1
	)
	run := func(withSMS bool) map[int][]time.Time {
		p := newProbe()
		total := perChan
		p.startWaiters(delivery.NewTokenBucket(p, interval), 1, perChan, email)
		if withSMS {
			p.startWaiters(delivery.NewTokenBucket(p, interval), 1, perChan, sms)
			total += perChan
		}
		byWho := map[int][]grant{}
		for _, g := range p.drive(t, time.Millisecond, total) {
			byWho[g.who] = append(byWho[g.who], g)
		}
		out := map[int][]time.Time{}
		for who, gs := range byWho {
			out[who] = times(gs)
		}
		return out
	}
	alone := run(false)
	both := run(true)

	if diff := cmp.Diff(alone[email], both[email]); diff != "" {
		t.Fatalf("email permit times change when SMS is busy (-email alone +with SMS):\n%s", diff)
	}
	for _, ch := range []struct {
		name string
		ts   []time.Time
	}{{"sms", both[sms]}, {"email", both[email]}} {
		if len(ch.ts) != perChan {
			t.Fatalf("%s permits = %d, want %d", ch.name, len(ch.ts), perChan)
		}
		if n, start := maxPerWindow(ch.ts, time.Second); n > 100 {
			t.Fatalf("%d %s permits in [%v, +1s), want at most 100", n, ch.name, start)
		}
		if gap, i := minGap(ch.ts); gap < interval {
			t.Fatalf("%s permits %d and %d are %v apart, want at least %v", ch.name, i-1, i, gap, interval)
		}
		// Without independence the two channels would share 100/s: about 50 each in the first second.
		inFirst := 0
		for _, at := range ch.ts {
			if at.Before(t0.Add(time.Second)) {
				inFirst++
			}
		}
		if inFirst < 99 {
			t.Fatalf("%s permits in [t0, t0+1s) = %d, want at least 99: the other channel must not take its share", ch.name, inFirst)
		}
	}
}

// REQ-SND-02
// A Wait blocked on the clock returns ctx.Err() when its context is cancelled.
func TestTokenBucket_WaitReturnsContextError(t *testing.T) {
	t.Parallel()
	p := newProbe()
	tb := delivery.NewTokenBucket(p, 10*time.Millisecond)
	if err := tb.Wait(context.Background()); err != nil {
		t.Fatalf("first Wait() error = %v, want nil", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	res := make(chan error, 1)
	go func() { res <- tb.Wait(ctx) }()

	bctx, bcancel := context.WithTimeout(context.Background(), guard)
	defer bcancel()
	if err := p.fake.BlockUntilWaiters(bctx, 1); err != nil {
		t.Fatalf("second Wait() never waited on the clock: %v", err)
	}
	cancel()
	select {
	case err := <-res:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Wait() after cancel error = %v, want context.Canceled", err)
		}
	case <-time.After(guard):
		t.Fatal("Wait() did not return after its context was cancelled")
	}
}
