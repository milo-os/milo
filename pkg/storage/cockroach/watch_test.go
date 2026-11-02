package cockroach

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

// The hub against a real feed: its readiness, lag and restarts.

// failingHandler fails every write the feed hands it.
type failingHandler struct{}

func (failingHandler) reset(map[string]int64) {}

func (failingHandler) write(*feedRow, *feedRow, hlc) error { return errors.New("handler failed") }

// A row the handler can't take ends the changefeed at once, so the feed
// resets without waiting out the stall watchdog.
func TestFeedStopsPromptlyOnHandlerFailure(t *testing.T) {
	b := testBackend(t)
	ctx := t.Context()
	f := &feed{pool: b.pool, connConfig: b.pool.Config().ConnConfig, handler: failingHandler{}}
	done := make(chan feedFailure, 1)
	go func() {
		_, kind, _ := f.follow(ctx, hlc{}, 0)
		done <- kind
	}()
	_, err := f.waitRunning(ctx)
	require.NoError(t, err)
	_, err = b.committer.commit(ctx, "feed-failure", createWrite("/configmaps/default/a", []byte("v"), 0))
	require.NoError(t, err)
	start := time.Now()
	require.Equal(t, feedReset, <-done)
	require.Less(t, time.Since(start), feedStallTimeout/2)
}

// A replica whose changefeed never starts isn't ready, even after a baseline
// of the current state, which leaves nothing to replay.
func TestWatchNotReadyUntilTheFeedDelivers(t *testing.T) {
	b := testBackend(t)
	ctx := t.Context()
	broken := b.pool.Config().ConnConfig.Copy()
	broken.Database = "missing"
	h := memoryHubWith(defaultHubConfig())
	other := &Backend{hub: h, feed: &feed{pool: b.pool, connConfig: broken, handler: h}}
	_, kind, err := other.feed.follow(ctx, hlc{}, 0)
	require.Error(t, err)
	require.NotEqual(t, feedReset, kind)
	require.ErrorContains(t, other.WatchReady(), "hasn't caught up")
}

// Only the changefeed's resolved messages show progress, so a fresh baseline
// after the feed broke doesn't make its lag look small.
func TestBaselineKeepsTheFeedsLag(t *testing.T) {
	b := testBackend(t)
	h := memoryHubWith(defaultHubConfig())
	f := &feed{pool: b.pool, connConfig: b.pool.Config().ConnConfig, handler: h}
	f.resolved.Store(time.Now().Add(-time.Hour).UnixNano())
	_, err := f.loadBaseline(t.Context(), 0)
	require.NoError(t, err)
	require.Greater(t, f.lag(), 50*time.Minute)
}

// The baseline's seek from project to project finds the counters a scan of the
// whole table would, and reads them as they were at an earlier timestamp.
func TestReadCounters(t *testing.T) {
	ctx, a := testSetup(t)
	_, other := testSetup(t)
	first := parseRV(t, createPod(ctx, t, a, "first").ResourceVersion)
	_, err := a.b.hub.waitReleased(ctx, a.project, first)
	require.NoError(t, err)
	then, ok := a.b.revTS.get(a.project, first)
	require.True(t, ok)
	createPod(ctx, t, other, "other")
	createPod(ctx, t, a, "second")

	var now hlc
	require.NoError(t, a.b.pool.QueryRow(ctx, clusterTimeSQL).Scan(&now))
	counters, err := a.b.feed.readCounters(ctx, now)
	require.NoError(t, err)
	rows, err := a.b.pool.Query(ctx, `SELECT project, rv FROM objects AS OF SYSTEM TIME '`+now.String()+`' WHERE key = ''`)
	require.NoError(t, err)
	scanned := map[string]int64{}
	var project string
	var rev int64
	_, err = pgx.ForEachRow(rows, []any{&project, &rev}, func() error {
		scanned[project] = rev
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, scanned, counters)

	past, err := a.b.feed.readCounters(ctx, then)
	require.NoError(t, err)
	require.Equal(t, first, past[a.project])
	require.Greater(t, counters[a.project], first)
}
