package cockroach

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEvery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		start := time.Now()
		var calls []time.Duration
		done := make(chan struct{})
		go func() {
			defer close(done)
			every(ctx, fixed(time.Minute), func(now time.Time) { calls = append(calls, now.Sub(start)) })
		}()
		time.Sleep(3*time.Minute + time.Second)
		cancel()
		<-done
		require.Equal(t, []time.Duration{time.Minute, 2 * time.Minute, 3 * time.Minute}, calls)
	})
}
