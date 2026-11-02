package cockroach

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// admit takes a slot and returns its release.
func admit(t *testing.T, a *admission, key string) func() {
	t.Helper()
	require.NoError(t, a.admit(t.Context(), key))
	return func() { a.release(key) }
}

func TestAdmissionTurnsAwayWorkPastItsCap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a := newAdmission("project", 2, time.Second)
		first := admit(t, a, "project")
		defer admit(t, a, "project")()

		start := time.Now()
		err := a.admit(t.Context(), "project")
		require.True(t, apierrors.IsTooManyRequests(err), "%v", err)
		require.ErrorContains(t, err, "storage for this project is busy")
		seconds, ok := apierrors.SuggestsClientDelay(err)
		require.True(t, ok)
		require.Positive(t, seconds)
		require.Equal(t, time.Second, time.Since(start))

		admit(t, a, "other")()
		first()
		admit(t, a, "project")()
	})
}

func TestAdmissionWaitsForRelease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a := newAdmission("server", 1, time.Second)
		release := admit(t, a, "")
		done := make(chan error, 1)
		go func() {
			err := a.admit(t.Context(), "")
			if err == nil {
				a.release("")
			}
			done <- err
		}()
		synctest.Wait()
		require.Empty(t, done)
		release()
		require.NoError(t, <-done)
		require.Empty(t, a.keys)
	})
}

func TestAdmissionCancellation(t *testing.T) {
	for name, full := range map[string]bool{"available": false, "full": true} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				a := newAdmission("project", 1, time.Second)
				if full {
					defer admit(t, a, "project")()
				}
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				require.ErrorIs(t, a.admit(ctx, "project"), context.Canceled)
			})
		})
	}
}

func TestAdmissionCancellationWhileWaiting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a := newAdmission("project", 1, time.Second)
		defer admit(t, a, "project")()
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- a.admit(ctx, "project") }()
		synctest.Wait()
		cancel()
		require.ErrorIs(t, <-done, context.Canceled)
		require.Zero(t, a.keys["project"].waiting, "the waiter left")
	})
}

// A write's slot in its project is held while the write runs below it.
func TestAdmittedWritesHoldASlotWhileTheWriteRuns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		next := newGate()
		a := newAdmission("project", 1, time.Second)
		done := commitAsync(t.Context(), admittedWrites{a, unbatched{next}}, "project", "a")
		<-next.started
		require.Equal(t, 1, a.keys["project"].inFlight)
		next.proceed <- struct{}{}
		require.NoError(t, (<-done).err)
		require.Empty(t, a.keys)
	})
}

// gate is a batchCommitter that reports each batch on started and commits it
// when the test sends on proceed. Each write's result revision is its index
// in the batch.
type gate struct {
	started chan []write
	proceed chan struct{}
}

func newGate() gate { return gate{started: make(chan []write), proceed: make(chan struct{})} }

func (g gate) commitBatch(ctx context.Context, _ string, b writeBatch) ([]writeResult, error) {
	select {
	case g.started <- b.writes:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case <-g.proceed:
		results := make([]writeResult, len(b.writes))
		for i := range results {
			results[i] = writeResult{rev: int64(i), written: true}
		}
		return results, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// commitAsync starts a write of key in project through c and returns where
// its result arrives.
func commitAsync(ctx context.Context, c committer, project, key string) <-chan writeOutcome {
	done := make(chan writeOutcome, 1)
	go func() {
		result, err := c.commit(ctx, project, createWrite(key, nil, 0))
		done <- writeOutcome{result, err}
	}()
	return done
}
