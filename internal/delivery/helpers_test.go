package delivery_test

// Shared helpers of the delivery unit tests. Only the names of docs/requirements.md "Worker ve mock
// provider birim test yüzeyi" are used; everything runs on clock.NewFake at the reference time and
// a seeded math/rand/v2 source (no time.Now, no time.Sleep, no network).

import (
	"context"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/gulcint/smart-campaign-composer/internal/clock"
)

// t0 is the test reference time of docs/requirements.md (clock.NewFake).
var t0 = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

// guard bounds every wait on another goroutine. It is a wall-clock safety net so a broken
// implementation fails the test instead of hanging it; it never drives the logic under test.
const guard = 30 * time.Second

// setTime stores tm in a time field. requirements.md names Attempt.FirstAttemptAt but not whether
// it is a time.Time or a *time.Time, so both are accepted.
func setTime(t *testing.T, field any, tm time.Time) {
	t.Helper()
	v := reflect.ValueOf(field).Elem()
	switch v.Type() {
	case reflect.TypeFor[time.Time]():
		v.Set(reflect.ValueOf(tm))
	case reflect.TypeFor[*time.Time]():
		v.Set(reflect.ValueOf(&tm))
	default:
		t.Fatalf("time field has type %s, want time.Time or *time.Time", v.Type())
	}
}

// timeOf reads a time.Time or *time.Time value (Decision.NextAttemptAt); ok is false for nil or zero.
func timeOf(t *testing.T, v any) (time.Time, bool) {
	t.Helper()
	switch x := v.(type) {
	case time.Time:
		return x, !x.IsZero()
	case *time.Time:
		if x == nil {
			return time.Time{}, false
		}
		return *x, !x.IsZero()
	default:
		t.Fatalf("time value has type %T, want time.Time or *time.Time", v)
		return time.Time{}, false
	}
}

// grant is one permit: which waiter got it and the clock time it was given at.
type grant struct {
	who int
	at  time.Time
}

// probe is the clock.Clock handed to the limiter under test, plus the bookkeeping of the waiter
// goroutines. Each waiter goroutine is "active" from before it starts until after its last permit;
// it stamps every permit with the clock's time right after Wait returns.
//
// The driver moves time only when the system is settled: every active waiter is parked on a fake
// timer (fake.Waiters() >= active). A waiter that has just been given a permit, or is between two
// Wait calls, or has been woken and is re-arming its timer, is not parked, so time never moves
// while a permit is unstamped or a waiter has not yet re-entered Wait. The driver does not assume
// how many permits follow a fired timer: woken waiters may re-arm for a later slot.
type probe struct {
	fake   *clock.Fake
	mu     sync.Mutex
	active int
	grants []grant
	errs   []error
	notify chan struct{}
}

func newProbe() *probe { return &probe{fake: clock.NewFake(t0), notify: make(chan struct{}, 1)} }

func (p *probe) signal() {
	select {
	case p.notify <- struct{}{}:
	default:
	}
}

func (p *probe) Now() time.Time { return p.fake.Now() }

func (p *probe) After(d time.Duration) <-chan time.Time {
	ch := p.fake.After(d)
	p.signal()
	return ch
}

var _ clock.Clock = (*probe)(nil)

func (p *probe) enter() {
	p.mu.Lock()
	p.active++
	p.mu.Unlock()
	p.signal()
}

func (p *probe) exit(err error) {
	p.mu.Lock()
	p.active--
	if err != nil {
		p.errs = append(p.errs, err)
	}
	p.mu.Unlock()
	p.signal()
}

func (p *probe) grant(who int) {
	p.mu.Lock()
	p.grants = append(p.grants, grant{who: who, at: p.fake.Now()})
	p.mu.Unlock()
	p.signal()
}

// startWaiters starts n goroutines that each call l.Wait perWaiter times.
func (p *probe) startWaiters(l interface{ Wait(context.Context) error }, n, perWaiter, idBase int) {
	for w := 0; w < n; w++ {
		p.enter()
		go func(who int) {
			ctx, cancel := context.WithTimeout(context.Background(), guard)
			defer cancel()
			for i := 0; i < perWaiter; i++ {
				if err := l.Wait(ctx); err != nil {
					p.exit(err)
					return
				}
				p.grant(who)
			}
			p.exit(nil)
		}(idBase + w)
	}
}

// drive advances the fake clock in steps of step, only while settled, until total permits have
// been given and every waiter has finished; it returns the permits.
func (p *probe) drive(t *testing.T, step time.Duration, total int) []grant {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), guard)
	defer cancel()
	for {
		p.mu.Lock()
		if len(p.errs) > 0 {
			err := p.errs[0]
			p.mu.Unlock()
			t.Fatalf("Wait() error = %v, want nil", err)
		}
		if len(p.grants) >= total && p.active == 0 {
			out := append([]grant(nil), p.grants...)
			p.mu.Unlock()
			if len(out) != total {
				t.Fatalf("permits = %d, want %d", len(out), total)
			}
			return out
		}
		settled := p.active > 0 && p.fake.Waiters() >= p.active
		if settled {
			// Held under p.mu: a waiter woken by this Advance stamps its permit only after the
			// lock is released, i.e. with the time just set.
			p.fake.Advance(step)
		}
		given, active := len(p.grants), p.active
		p.mu.Unlock()
		if settled {
			continue
		}
		select {
		case <-p.notify:
		case <-ctx.Done():
			t.Fatalf("stuck at %v: %d of %d permits, %d active waiters, %d parked on the clock", p.fake.Now(), given, total, active, p.fake.Waiters())
		}
	}
}

func times(gs []grant) []time.Time {
	out := make([]time.Time, len(gs))
	for i, g := range gs {
		out[i] = g.at
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

// maxPerWindow is the largest number of permits in any half-open window [t, t+w). The maximum is
// reached by a window that starts at a permit, so only those windows are checked.
func maxPerWindow(ts []time.Time, w time.Duration) (count int, start time.Time) {
	j := 0
	for i := range ts {
		if j < i {
			j = i
		}
		for j < len(ts) && ts[j].Before(ts[i].Add(w)) {
			j++
		}
		if n := j - i; n > count {
			count, start = n, ts[i]
		}
	}
	return count, start
}

// minGap is the smallest distance between two consecutive permits.
func minGap(ts []time.Time) (time.Duration, int) {
	gap, at := time.Duration(1<<62), -1
	for i := 1; i < len(ts); i++ {
		if d := ts[i].Sub(ts[i-1]); d < gap {
			gap, at = d, i
		}
	}
	return gap, at
}
