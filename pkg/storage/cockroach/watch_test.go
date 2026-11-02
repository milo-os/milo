package cockroach

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/apis/example"
	"k8s.io/apiserver/pkg/storage"
)

// The hub against a real feed: its readiness, lag and restarts, and exact
// lists on one replica right after another replica's writes.

// An exact list on one replica right after a write on another waits for the
// feed to deliver the write's timestamp instead of failing with 410.
func TestExactListRightAfterOtherReplicaWrites(t *testing.T) {
	ctx, a := testSetup(t)
	b := otherReplica(t, a)
	for i := range 3 {
		// With the feeds down, b cannot have the write's timestamp when the
		// list arrives.
		killFeed(ctx, t, a)
		pod := createPod(ctx, t, a, fmt.Sprintf("pod-%02d", i))
		// A later write makes pod's revision a past one, which only the feed's
		// timestamp lets b read exactly.
		increaseRV(a)(ctx, t)
		list := &example.PodList{}
		require.NoError(t, b.GetList(ctx, "/pods/", storage.ListOptions{
			ResourceVersion: pod.ResourceVersion, ResourceVersionMatch: metav1.ResourceVersionMatchExact,
			Predicate: storage.Everything, Recursive: true,
		}, list))
		require.Len(t, list.Items, i+1)
	}
}

// A replica that no longer knows an idle project's last commit time still
// serves the next page and an exact list there once a later write moves the
// project on, by reading just before that write committed.
func TestListAtRevisionWhoseTimestampExpired(t *testing.T) {
	ctx, a := testSetup(t)
	b := otherReplica(t, a)
	createPod(ctx, t, a, "pod-a")
	last := createPod(ctx, t, a, "pod-b")
	rev, err := a.versioner.ParseResourceVersion(last.ResourceVersion)
	require.NoError(t, err)
	for _, s := range []testStore{a, b} {
		_, err := s.b.hub.waitReleased(ctx, s.project, int64(rev))
		require.NoError(t, err)
		s.b.revTS.forget(s.project, int64(rev))
	}
	first := &example.PodList{}
	require.NoError(t, a.GetList(ctx, "/pods/", storage.ListOptions{Predicate: withLimit(storage.Everything, 1), Recursive: true}, first))
	require.NotEmpty(t, first.Continue)
	createPod(ctx, t, a, "pod-c")

	page := withLimit(storage.Everything, 1)
	page.Continue = first.Continue
	next := &example.PodList{}
	require.NoError(t, b.GetList(ctx, "/pods/", storage.ListOptions{Predicate: page, Recursive: true}, next))
	require.Len(t, next.Items, 1)
	require.Equal(t, "pod-b", next.Items[0].Name)

	exact := &example.PodList{}
	require.NoError(t, b.GetList(ctx, "/pods/", storage.ListOptions{
		ResourceVersion: last.ResourceVersion, ResourceVersionMatch: metav1.ResourceVersionMatchExact,
		Predicate: storage.Everything, Recursive: true,
	}, exact))
	require.Len(t, exact.Items, 2)
}

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

// killFeed cancels every changefeed query from the server side, and waits
// for s's backend to notice.
func killFeed(ctx context.Context, t *testing.T, s testStore) {
	t.Helper()
	// Some versions list a sinkless CREATE CHANGEFEED under its older name,
	// EXPERIMENTAL CHANGEFEED. The anchor keeps this statement from matching
	// itself.
	const feeds = `WITH x AS (SHOW CLUSTER STATEMENTS) SELECT query_id FROM x WHERE query ~ '^(CREATE|EXPERIMENTAL) CHANGEFEED FOR TABLE objects '`
	stopped, err := s.b.feed.waitRunning(ctx)
	require.NoError(t, err)
	_, err = s.b.pool.Exec(ctx, `CANCEL QUERIES (`+feeds+`)`)
	require.NoError(t, err)
	<-stopped
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
