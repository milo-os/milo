package cockroach

import (
	"context"
	"math/rand/v2"
	"time"
)

// broadcast wakes everything waiting on it at once. The zero value is ready to
// use. Its owner's lock guards it.
type broadcast struct {
	ch chan struct{}
}

func (b *broadcast) wait() <-chan struct{} {
	if b.ch == nil {
		b.ch = make(chan struct{})
	}
	return b.ch
}

func (b *broadcast) fire() {
	if b.ch != nil {
		close(b.ch)
		b.ch = nil
	}
}

// every calls fn after each wait that next returns, until ctx ends.
func every(ctx context.Context, next func() time.Duration, fn func(now time.Time)) {
	for {
		select {
		case now := <-time.After(next()):
			fn(now)
		case <-ctx.Done():
			return
		}
	}
}

// fixed returns a wait of interval every time.
func fixed(interval time.Duration) func() time.Duration {
	return func() time.Duration { return interval }
}

// jittered returns waits between half an interval and one and a half, so
// replicas that start together drift apart.
func jittered(interval time.Duration) func() time.Duration {
	return func() time.Duration { return interval/2 + rand.N(interval) }
}
