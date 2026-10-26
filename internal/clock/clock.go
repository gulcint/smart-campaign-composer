// Package clock is the only source of time in the system. Production code uses
// Real; tests drive a Fake so that no test depends on the wall clock.
package clock

import (
	"context"
	"time"
)

// Clock is the only source of time in the system.
type Clock interface {
	Now() time.Time                         // always UTC
	After(d time.Duration) <-chan time.Time // fires once when Now() >= start+d
}

type realClock struct{}

// Real returns the wall clock.
func Real() Clock { return realClock{} }

func (realClock) Now() time.Time { return time.Now().UTC() }

func (realClock) After(d time.Duration) <-chan time.Time {
	return time.After(d)
}

// Sleep blocks for d on c or until ctx is done (returns ctx.Err()).
func Sleep(ctx context.Context, c Clock, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d <= 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.After(d):
		return nil
	}
}
