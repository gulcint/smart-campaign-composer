package delivery_test

import (
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/gulcint/smart-campaign-composer/internal/delivery"
	"github.com/gulcint/smart-campaign-composer/internal/provider"
)

// seed of every policy and of its reference source; the reference replays the same draws.
const seed1, seed2 = 20260115, 42

func seeded() *rand.Rand { return rand.New(rand.NewPCG(seed1, seed2)) } //nolint:gosec // seeded jitter for deterministic tests

// capFor is the full-jitter upper bound of the n-th retry: min(MaxDelay, BaseDelay << (n-1)).
func capFor(cfg delivery.RetryConfig, n int) time.Duration {
	return min(cfg.MaxDelay, cfg.BaseDelay<<(n-1))
}

// draw is the delay Jitter(n) must return for the next draw of ref.
func draw(ref *rand.Rand, cfg delivery.RetryConfig, n int) time.Duration {
	return time.Duration(ref.Int64N(int64(capFor(cfg, n))))
}

// in describes one provider outcome; since is Now - FirstAttemptAt (FirstAttemptAt = t0).
type in struct {
	status     int
	retryAfter time.Duration
	hasRA      bool
	err        error
	failed     int
	limited    int
	since      time.Duration
}

func attempt(t *testing.T, a in) delivery.Attempt {
	t.Helper()
	x := delivery.Attempt{
		StatusCode:     a.status,
		RetryAfter:     a.retryAfter,
		HasRetryAfter:  a.hasRA,
		Err:            a.err,
		FailedAttempts: a.failed,
		RateLimited:    a.limited,
		Now:            t0.Add(a.since),
	}
	setTime(t, &x.FirstAttemptAt, t0)
	return x
}

// checkDecision compares d with want. For ActionRetry the delay is wantDelay and NextAttemptAt is
// Now + Delay; for the other actions Delay and NextAttemptAt are empty. FailureReason is set only
// for ActionFail.
func checkDecision(t *testing.T, a delivery.Attempt, d, want delivery.Decision, wantDelay time.Duration) {
	t.Helper()
	if d.Action != want.Action {
		t.Fatalf("Action = %v, want %v (decision %+v)", d.Action, want.Action, d)
	}
	if d.Count != want.Count {
		t.Fatalf("Count = %v, want %v (decision %+v)", d.Count, want.Count, d)
	}
	if d.FailureReason != want.FailureReason {
		t.Fatalf("FailureReason = %v, want %v (decision %+v)", d.FailureReason, want.FailureReason, d)
	}
	next, hasNext := timeOf(t, d.NextAttemptAt)
	if want.Action != delivery.ActionRetry {
		if d.Delay != 0 || hasNext {
			t.Fatalf("Delay = %v, NextAttemptAt = %v; want both empty outside ActionRetry", d.Delay, d.NextAttemptAt)
		}
		return
	}
	if d.Delay != wantDelay {
		t.Fatalf("Delay = %v, want %v", d.Delay, wantDelay)
	}
	if !next.Equal(a.Now.Add(d.Delay)) {
		t.Fatalf("NextAttemptAt = %v, want Now + Delay = %v", next, a.Now.Add(d.Delay))
	}
}

var (
	sent      = delivery.Decision{Action: delivery.ActionMarkSent, Count: delivery.CountNone}
	retryFail = delivery.Decision{Action: delivery.ActionRetry, Count: delivery.CountFailure}
	retry429  = delivery.Decision{Action: delivery.ActionRetry, Count: delivery.CountRateLimited}
	maxedOut  = delivery.Decision{Action: delivery.ActionFail, Count: delivery.CountFailure, FailureReason: delivery.FailureMaxAttempts}
	budget429 = delivery.Decision{Action: delivery.ActionFail, Count: delivery.CountRateLimited, FailureReason: delivery.FailureRateLimitBudget}
	rejected  = delivery.Decision{Action: delivery.ActionFail, Count: delivery.CountNone, FailureReason: delivery.FailureProviderRejected}
)

// REQ-SND-04
// delivery.DefaultRetryConfig() holds the catalogue constants: 5 attempts including the first,
// 1 s base, 60 s cap, 20 x 429 or a 10 minute window.
func TestDefaultRetryConfig(t *testing.T) {
	t.Parallel()
	want := delivery.RetryConfig{
		MaxAttempts:     5,
		BaseDelay:       time.Second,
		MaxDelay:        60 * time.Second,
		MaxRateLimited:  20,
		RateLimitWindow: 10 * time.Minute,
	}
	if diff := cmp.Diff(want, delivery.DefaultRetryConfig()); diff != "" {
		t.Fatalf("DefaultRetryConfig() mismatch (-want +got):\n%s", diff)
	}
}

// REQ-SND-04
// The retry table, one provider outcome per row, with the default configuration. jitterN > 0 means
// the delay must be the first draw of Jitter(jitterN) from the seeded source.
func TestRetryPolicy_Next(t *testing.T) {
	t.Parallel()
	cfg := delivery.DefaultRetryConfig()
	tests := []struct {
		name    string
		in      in
		want    delivery.Decision
		delay   time.Duration
		jitterN int
	}{
		// 2xx
		{name: "200 is sent", in: in{status: 200}, want: sent},
		{name: "201 is sent", in: in{status: 201}, want: sent},
		{name: "202 is sent", in: in{status: 202}, want: sent},
		{name: "204 is sent", in: in{status: 204}, want: sent},
		{name: "299 is sent", in: in{status: 299}, want: sent},
		{name: "202 after four failures and nineteen 429s is sent", in: in{status: 202, failed: 4, limited: 19}, want: sent},

		// 429
		{name: "429 with Retry-After 3s waits exactly 3s", in: in{status: 429, retryAfter: 3 * time.Second, hasRA: true}, want: retry429, delay: 3 * time.Second},
		{name: "429 with Retry-After 0 retries at once", in: in{status: 429, hasRA: true}, want: retry429, delay: 0},
		{name: "429 with a negative Retry-After waits 0", in: in{status: 429, retryAfter: -5 * time.Second, hasRA: true}, want: retry429, delay: 0},
		{name: "first 429 without Retry-After backs off with n = 1", in: in{status: 429}, want: retry429, jitterN: 1},
		{name: "third 429 without Retry-After backs off with n = 3", in: in{status: 429, limited: 2}, want: retry429, jitterN: 3},
		{name: "429 without Retry-After ignores failed attempts for n", in: in{status: 429, failed: 3}, want: retry429, jitterN: 1},
		{name: "19th 429 still retries", in: in{status: 429, limited: 18, retryAfter: time.Second, hasRA: true}, want: retry429, delay: time.Second},
		{name: "20th 429 fails with rate_limit_budget_exhausted", in: in{status: 429, limited: 19, retryAfter: time.Second, hasRA: true}, want: budget429},
		{name: "20th 429 without Retry-After fails too", in: in{status: 429, limited: 19}, want: budget429},
		{name: "429 after four failed attempts still retries", in: in{status: 429, failed: 4, retryAfter: time.Second, hasRA: true}, want: retry429, delay: time.Second},
		{name: "429 exactly 10 minutes after the first attempt fails", in: in{status: 429, hasRA: true, since: 10 * time.Minute}, want: budget429},
		{name: "429 11 minutes after the first attempt fails", in: in{status: 429, limited: 1, retryAfter: time.Second, hasRA: true, since: 11 * time.Minute}, want: budget429},
		{name: "429 whose wait ends exactly at the window edge retries", in: in{status: 429, retryAfter: time.Second, hasRA: true, since: 10*time.Minute - time.Second}, want: retry429, delay: time.Second},
		{name: "429 whose Retry-After ends after the window fails", in: in{status: 429, retryAfter: 2 * time.Second, hasRA: true, since: 10*time.Minute - time.Second}, want: budget429},
		{name: "429 with a Retry-After longer than the whole window fails", in: in{status: 429, retryAfter: 11 * time.Minute, hasRA: true}, want: budget429},

		// 5xx, 408, timeout, connection error
		{name: "500 retries with backoff n = 1", in: in{status: 500}, want: retryFail, jitterN: 1},
		{name: "502 retries with backoff n = 1", in: in{status: 502}, want: retryFail, jitterN: 1},
		{name: "503 retries with backoff n = 1", in: in{status: 503}, want: retryFail, jitterN: 1},
		{name: "504 retries with backoff n = 1", in: in{status: 504}, want: retryFail, jitterN: 1},
		{name: "408 retries with backoff n = 1", in: in{status: 408}, want: retryFail, jitterN: 1},
		{name: "timeout retries with backoff n = 1", in: in{err: provider.ErrTimeout}, want: retryFail, jitterN: 1},
		{name: "connection error retries with backoff n = 1", in: in{err: provider.ErrConnection}, want: retryFail, jitterN: 1},
		{name: "second failure backs off with n = 2", in: in{status: 500, failed: 1}, want: retryFail, jitterN: 2},
		{name: "third failure backs off with n = 3", in: in{status: 503, failed: 2}, want: retryFail, jitterN: 3},
		{name: "fourth failure backs off with n = 4", in: in{err: provider.ErrTimeout, failed: 3}, want: retryFail, jitterN: 4},
		{name: "fifth failure fails with max_attempts_exceeded", in: in{status: 500, failed: 4}, want: maxedOut},
		{name: "fifth failure by timeout fails", in: in{err: provider.ErrTimeout, failed: 4}, want: maxedOut},
		{name: "fifth failure by connection error fails", in: in{err: provider.ErrConnection, failed: 4}, want: maxedOut},
		{name: "fifth failure by 408 fails", in: in{status: 408, failed: 4}, want: maxedOut},
		{name: "503 with Retry-After still uses backoff", in: in{status: 503, retryAfter: 30 * time.Second, hasRA: true}, want: retryFail, jitterN: 1},
		{name: "5xx after nineteen 429s still retries", in: in{status: 500, limited: 19}, want: retryFail, jitterN: 1},
		{name: "5xx after the 429 window still retries", in: in{status: 500, since: 11 * time.Minute}, want: retryFail, jitterN: 1},

		// other status codes
		{name: "400 is provider_rejected", in: in{status: 400}, want: rejected},
		{name: "401 is provider_rejected", in: in{status: 401}, want: rejected},
		{name: "404 is provider_rejected", in: in{status: 404}, want: rejected},
		{name: "409 is provider_rejected", in: in{status: 409}, want: rejected},
		{name: "422 is provider_rejected", in: in{status: 422}, want: rejected},
		{name: "400 after failures is still provider_rejected", in: in{status: 400, failed: 3, limited: 5}, want: rejected},
		{name: "1xx is provider_rejected", in: in{status: 100}, want: rejected},
		{name: "3xx is provider_rejected", in: in{status: 302}, want: rejected},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := delivery.NewRetryPolicy(cfg, seeded())
			a := attempt(t, tc.in)
			d := p.Next(a)
			wantDelay := tc.delay
			if tc.jitterN > 0 {
				wantDelay = draw(seeded(), cfg, tc.jitterN)
				if wantDelay < 0 || wantDelay >= capFor(cfg, tc.jitterN) {
					t.Fatalf("reference draw %v outside [0, %v)", wantDelay, capFor(cfg, tc.jitterN))
				}
			}
			checkDecision(t, a, d, tc.want, wantDelay)
		})
	}
}

// REQ-SND-04
// Consecutive outcomes of one delivery: the 5th failed attempt (the first attempt included) fails
// and the 20th 429 fails; every earlier one retries. The two budgets are counted separately.
func TestRetryPolicy_Budgets(t *testing.T) {
	t.Parallel()
	t.Run("failures 1-4 retry, the 5th fails", func(t *testing.T) {
		t.Parallel()
		p := delivery.NewRetryPolicy(delivery.DefaultRetryConfig(), seeded())
		for failed := 0; failed < 5; failed++ {
			d := p.Next(attempt(t, in{status: 503, failed: failed, limited: 7}))
			want := delivery.ActionRetry
			if failed == 4 {
				want = delivery.ActionFail
			}
			if d.Action != want {
				t.Fatalf("failure #%d: Action = %v, want %v", failed+1, d.Action, want)
			}
		}
	})
	t.Run("429s 1-19 retry, the 20th fails", func(t *testing.T) {
		t.Parallel()
		p := delivery.NewRetryPolicy(delivery.DefaultRetryConfig(), seeded())
		for limited := 0; limited < 20; limited++ {
			d := p.Next(attempt(t, in{status: 429, limited: limited, failed: 3, hasRA: true, retryAfter: time.Second, since: time.Duration(limited) * time.Second}))
			want := delivery.ActionRetry
			if limited == 19 {
				want = delivery.ActionFail
			}
			if d.Action != want {
				t.Fatalf("429 #%d: Action = %v, want %v", limited+1, d.Action, want)
			}
		}
	})
	t.Run("custom limits are honoured", func(t *testing.T) {
		t.Parallel()
		cfg := delivery.DefaultRetryConfig()
		cfg.MaxAttempts = 3
		cfg.MaxRateLimited = 2
		cfg.RateLimitWindow = time.Minute
		p := delivery.NewRetryPolicy(cfg, seeded())
		cases := []struct {
			name string
			in   in
			want delivery.Decision
		}{
			{name: "2nd failure retries", in: in{status: 500, failed: 1}, want: retryFail},
			{name: "3rd failure fails", in: in{status: 500, failed: 2}, want: maxedOut},
			{name: "1st 429 retries", in: in{status: 429, hasRA: true}, want: retry429},
			{name: "2nd 429 fails", in: in{status: 429, limited: 1, hasRA: true}, want: budget429},
			{name: "429 at one minute fails", in: in{status: 429, hasRA: true, since: time.Minute}, want: budget429},
		}
		for _, c := range cases {
			d := p.Next(attempt(t, c.in))
			if d.Action != c.want.Action || d.Count != c.want.Count || d.FailureReason != c.want.FailureReason {
				t.Fatalf("%s: decision %+v, want action %v count %v reason %v", c.name, d, c.want.Action, c.want.Count, c.want.FailureReason)
			}
		}
	})
}

// REQ-SND-04
// Jitter(n) is full jitter in [0, min(MaxDelay, BaseDelay * 2^(n-1))): with a seeded source it is
// exactly rng.Int64N(cap), one draw per call, so the same seed always gives the same delays.
func TestRetryPolicy_Jitter(t *testing.T) {
	t.Parallel()
	custom := delivery.DefaultRetryConfig()
	custom.BaseDelay = 250 * time.Millisecond
	custom.MaxDelay = time.Second
	tests := []struct {
		name string
		cfg  delivery.RetryConfig
		caps []time.Duration // cap for n = 1, 2, ...
	}{
		{
			name: "default 1s base, 60s cap",
			cfg:  delivery.DefaultRetryConfig(),
			caps: []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, 60 * time.Second, 60 * time.Second, 60 * time.Second},
		},
		{
			name: "250ms base, 1s cap",
			cfg:  custom,
			caps: []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, time.Second, time.Second},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := delivery.NewRetryPolicy(tc.cfg, seeded())
			ref := seeded()
			for round := 0; round < 50; round++ {
				for i, c := range tc.caps {
					n := i + 1
					if got := capFor(tc.cfg, n); got != c {
						t.Fatalf("cap(%d) = %v, want %v", n, got, c)
					}
					want := time.Duration(ref.Int64N(int64(c)))
					got := p.Jitter(n)
					if got < 0 || got >= c {
						t.Fatalf("Jitter(%d) = %v, want in [0, %v)", n, got, c)
					}
					if got != want {
						t.Fatalf("round %d: Jitter(%d) = %v, want the seeded draw %v", round, n, got, want)
					}
				}
			}
		})
	}
	t.Run("the same seed gives the same delays", func(t *testing.T) {
		t.Parallel()
		a := delivery.NewRetryPolicy(delivery.DefaultRetryConfig(), seeded())
		b := delivery.NewRetryPolicy(delivery.DefaultRetryConfig(), seeded())
		for i := 0; i < 200; i++ {
			n := i%6 + 1
			if x, y := a.Jitter(n), b.Jitter(n); x != y {
				t.Fatalf("call %d: Jitter(%d) = %v and %v for the same seed", i, n, x, y)
			}
		}
	})
}

// REQ-SND-04
// The source is drawn only by Jitter and exactly once per call: never for 2xx, other 4xx, a 429 with
// Retry-After or an outcome that fails the delivery before a delay is needed. A mixed sequence on
// one policy therefore matches a reference that draws only for the backoff steps.
func TestRetryPolicy_DrawsOnlyForBackoff(t *testing.T) {
	t.Parallel()
	cfg := delivery.DefaultRetryConfig()
	p := delivery.NewRetryPolicy(cfg, seeded())
	ref := seeded()
	steps := []struct {
		name    string
		in      in
		want    delivery.Decision
		jitterN int // 0 = no draw
		delay   time.Duration
	}{
		{name: "202", in: in{status: 202}, want: sent},
		{name: "400", in: in{status: 400}, want: rejected},
		{name: "429 with Retry-After", in: in{status: 429, retryAfter: 2 * time.Second, hasRA: true}, want: retry429, delay: 2 * time.Second},
		{name: "500, first failure", in: in{status: 500}, want: retryFail, jitterN: 1},
		{name: "429 without Retry-After", in: in{status: 429, limited: 1}, want: retry429, jitterN: 2},
		{name: "202 again", in: in{status: 202, failed: 1, limited: 2}, want: sent},
		{name: "503, third failure", in: in{status: 503, failed: 2}, want: retryFail, jitterN: 3},
		{name: "500, fifth failure", in: in{status: 500, failed: 4}, want: maxedOut},
		{name: "20th 429", in: in{status: 429, limited: 19}, want: budget429},
		{name: "404", in: in{status: 404}, want: rejected},
		{name: "timeout, second failure", in: in{err: provider.ErrTimeout, failed: 1}, want: retryFail, jitterN: 2},
		{name: "connection error, fourth failure", in: in{err: provider.ErrConnection, failed: 3}, want: retryFail, jitterN: 4},
	}
	for _, s := range steps {
		a := attempt(t, s.in)
		d := p.Next(a)
		want := s.delay
		if s.jitterN > 0 {
			want = draw(ref, cfg, s.jitterN)
		}
		t.Run(s.name, func(t *testing.T) { checkDecision(t, a, d, s.want, want) })
	}
}

// REQ-SND-04
// A 429 without Retry-After whose backoff would end after FirstAttemptAt + RateLimitWindow fails
// with rate_limit_budget_exhausted; one whose backoff ends inside the window retries.
func TestRetryPolicy_BackoffPastWindow(t *testing.T) {
	t.Parallel()
	cfg := delivery.DefaultRetryConfig()
	// The draw is replayed from the reference so the expected action is known exactly.
	d1 := draw(seeded(), cfg, 1)
	tests := []struct {
		name  string
		since time.Duration
		want  delivery.Decision
	}{
		{name: "backoff ends inside the window", since: 10*time.Minute - d1, want: retry429},
		{name: "backoff ends after the window", since: 10*time.Minute - d1 + time.Nanosecond, want: budget429},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if d1 == 0 {
				t.Fatal("reference draw is 0, so both rows coincide: pick another seed")
			}
			p := delivery.NewRetryPolicy(cfg, seeded())
			a := attempt(t, in{status: 429, since: tc.since})
			checkDecision(t, a, p.Next(a), tc.want, d1)
		})
	}
}

// REQ-SND-04
// RetryPolicy is safe for concurrent use: many goroutines share one policy (run with -race) and
// every backoff stays inside its bound.
func TestRetryPolicy_ConcurrentUse(t *testing.T) {
	t.Parallel()
	p := delivery.NewRetryPolicy(delivery.DefaultRetryConfig(), seeded())
	var wg sync.WaitGroup
	errs := make(chan string, 8)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				d := p.Next(delivery.Attempt{StatusCode: 500, Now: t0})
				if d.Action != delivery.ActionRetry || d.Delay < 0 || d.Delay >= time.Second {
					errs <- "Next(500, first failure) gave an action or delay outside the retry table"
					return
				}
				if j := p.Jitter(3); j < 0 || j >= 4*time.Second {
					errs <- "Jitter(3) outside [0, 4s)"
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}
