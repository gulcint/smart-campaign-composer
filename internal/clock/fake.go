package clock

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Fake is a manually driven clock for tests. Safe for concurrent use.
type Fake struct {
	mu      sync.Mutex
	now     time.Time
	waiters []*fakeWaiter
	changed chan struct{} // closed and replaced whenever the waiter set changes
	seq     uint64        // insertion order of waiters with equal deadlines
}

type fakeWaiter struct {
	deadline time.Time
	seq      uint64
	ch       chan time.Time
}

// NewFake returns a Fake whose Now() is start (in UTC).
func NewFake(start time.Time) *Fake {
	return &Fake{now: start.UTC(), changed: make(chan struct{})}
}

// Now returns the fake current time.
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// After returns a channel that fires once Now() >= Now()+d; d <= 0 fires immediately.
func (f *Fake) After(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- f.now
		return ch
	}
	f.seq++
	f.waiters = append(f.waiters, &fakeWaiter{deadline: f.now.Add(d), seq: f.seq, ch: ch})
	f.notifyLocked()
	return ch
}

// Advance moves time forward by d and fires due waiters in deadline order.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setLocked(f.now.Add(d))
}

// Set moves time to t; t must be >= Now(), otherwise Set panics.
func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t = t.UTC()
	if t.Before(f.now) {
		panic("clock: Fake.Set moves time backwards")
	}
	f.setLocked(t)
}

func (f *Fake) setLocked(t time.Time) {
	f.now = t
	sort.SliceStable(f.waiters, func(i, j int) bool {
		a, b := f.waiters[i], f.waiters[j]
		if a.deadline.Equal(b.deadline) {
			return a.seq < b.seq
		}
		return a.deadline.Before(b.deadline)
	})
	kept := f.waiters[:0]
	fired := false
	for _, w := range f.waiters {
		if !w.deadline.After(t) {
			w.ch <- t
			fired = true
			continue
		}
		kept = append(kept, w)
	}
	for i := len(kept); i < len(f.waiters); i++ {
		f.waiters[i] = nil
	}
	f.waiters = kept
	if fired {
		f.notifyLocked()
	}
}

// Waiters returns the number of pending After channels.
func (f *Fake) Waiters() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.waiters)
}

// BlockUntilWaiters returns nil once Waiters() >= n, or ctx.Err().
func (f *Fake) BlockUntilWaiters(ctx context.Context, n int) error {
	for {
		f.mu.Lock()
		count, changed := len(f.waiters), f.changed
		f.mu.Unlock()
		if count >= n {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

func (f *Fake) notifyLocked() {
	close(f.changed)
	f.changed = make(chan struct{})
}
