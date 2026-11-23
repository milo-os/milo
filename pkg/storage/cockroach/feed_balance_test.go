package cockroach

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCrowded(t *testing.T) {
	nodes := []feedNode{{ID: 1, Feeds: 3}, {ID: 2, Feeds: 1}, {ID: 3, Feeds: 2}}
	require.True(t, crowded(nodes, 1), "two more than another")
	require.False(t, crowded(nodes, 3), "one more")
	require.False(t, crowded(nodes, 2), "least loaded")
	require.True(t, crowded(nodes, 4), "node gone")
}

// The feed checks soon after it starts, then less often, and right away when
// it lags.
func TestFeedBalancerChecks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var feeds, checks atomic.Int64
		feeds.Store(1)
		b := newFeedBalancer(nil, nil)
		b.nodes = func(context.Context) ([]feedNode, error) {
			checks.Add(1)
			return []feedNode{{ID: 1, Feeds: int(feeds.Load())}, {ID: 2, Feeds: 1}}, nil
		}
		var moved atomic.Bool
		go b.watch(t.Context(), 1, func() { moved.Store(true) })

		// Doubling from a minute to ten, with jitter, makes 6 to 10 checks in
		// an hour rather than 60.
		time.Sleep(time.Hour)
		synctest.Wait()
		require.InDelta(t, 8, checks.Load(), 2)

		feeds.Store(3)
		b.lagging()
		synctest.Wait()
		require.True(t, moved.Load())
	})
}

// A feed lands on a node, counted under its name, and moves off it once the
// node is crowded, resuming from its cursor.
func TestFeedMovesOffACrowdedNode(t *testing.T) {
	b := testBackend(t)
	ctx := t.Context()
	balancer := newFeedBalancer(b.pool, b.pool.Config().ConnConfig)
	list := balancer.nodes
	var placed atomic.Bool
	balancer.nodes = func(ctx context.Context) ([]feedNode, error) {
		if placed.Swap(true) {
			return []feedNode{{ID: 99}}, nil
		}
		return list(ctx)
	}
	f := &feed{pool: b.pool, source: sinkless{balancer: balancer}, handler: memoryHubWith(defaultHubConfig())}
	done := make(chan error, 1)
	go func() {
		_, _, err := f.follow(ctx, hlc{}, 0)
		done <- err
	}()
	_, err := f.waitRunning(ctx)
	require.NoError(t, err)

	nodes, err := list(ctx)
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	require.GreaterOrEqual(t, nodes[0].Feeds, 2, "the test server's feed and this one")

	balancer.lagging()
	require.ErrorIs(t, <-done, errFeedRebalanced)
}
