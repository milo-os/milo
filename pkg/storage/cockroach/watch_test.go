package cockroach

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/apis/example"
	"k8s.io/apiserver/pkg/storage"
	storagetesting "k8s.io/apiserver/pkg/storage/testing"
	"k8s.io/apiserver/pkg/storage/value"
	"k8s.io/component-base/metrics/legacyregistry"
	"k8s.io/component-base/metrics/testutil"
)

// The upstream watch tests that etcd3's watcher_test.go runs. The tests after
// them cover what etcd3 has no counterpart for: watches of another replica's
// writes, feed restarts, readiness and lag, and the watch metrics.

func TestWatch(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestWatch(ctx, t, s)
}

func TestClusterScopedWatch(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestClusterScopedWatch(ctx, t, s)
}

func TestNamespaceScopedWatch(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestNamespaceScopedWatch(ctx, t, s)
}

func TestDeleteTriggerWatch(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestDeleteTriggerWatch(ctx, t, s)
}

func TestWatchFromZero(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestWatchFromZero(ctx, t, s, compaction(s))
}

func TestWatchFromNonZero(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestWatchFromNonZero(ctx, t, s)
}

func TestDelayedWatchDelivery(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestDelayedWatchDelivery(ctx, t, s)
}

func TestWatchError(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestWatchError(ctx, t, s)
}

func TestWatchContextCancel(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestWatchContextCancel(ctx, t, s)
}

func TestWatcherTimeout(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestWatcherTimeout(ctx, t, s)
}

func TestWatchDeleteEventObjectHaveLatestRV(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestWatchDeleteEventObjectHaveLatestRV(ctx, t, s)
}

func TestWatchInitializationSignal(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestWatchInitializationSignal(ctx, t, s)
}

func TestWatchDispatchBookmarkEvents(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestWatchDispatchBookmarkEvents(ctx, t, s, false)
}

func TestWatchDispatchPeriodicBookmarks(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestWatchDispatchBookmarkEvents(ctx, t, bookmarkReplica(t, s), true)
}

func TestWatchBookmarksWithCorrectResourceVersion(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestOptionalWatchBookmarksWithCorrectResourceVersion(ctx, t, bookmarkReplica(t, s))
}

func bookmarkReplica(t *testing.T, s testStore) testStore {
	t.Helper()
	return otherReplica(t, s, func(o *options) { o.hub.bookmarkInterval = 200 * time.Millisecond })
}

func TestSendInitialEventsBackwardCompatibility(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunSendInitialEventsBackwardCompatibility(ctx, t, s)
}

func TestWatchSemantics(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunWatchSemantics(ctx, t, s)
}

func TestWatchSemanticInitialEventsExtended(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunWatchSemanticInitialEventsExtended(ctx, t, s)
}

func TestWatchListMatchSingle(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunWatchListMatchSingle(ctx, t, s)
}

func TestWatchErrorIsBlockingFurtherEvents(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunWatchErrorIsBlockingFurtherEvents(ctx, t, s)
}

func TestKeySchema(t *testing.T) {
	ctx, s := testSetup(t)
	storagetesting.RunTestKeySchema(ctx, t, s)
}

// The hub against a real feed.

func watchFrom(ctx context.Context, t *testing.T, s testStore, rv string) watch.Interface {
	t.Helper()
	w, err := s.Watch(ctx, "/pods/", storage.ListOptions{ResourceVersion: rv, Predicate: storage.Everything, Recursive: true})
	require.NoError(t, err)
	t.Cleanup(w.Stop)
	return w
}

func requireAdded(t *testing.T, w watch.Interface, rv string) {
	t.Helper()
	e := nextEvent(t, w)
	pod, ok := e.Object.(*example.Pod)
	require.True(t, ok, "%s %#v", e.Type, e.Object)
	require.Equal(t, watch.Added, e.Type)
	require.Equal(t, rv, pod.ResourceVersion)
}

func histogramCount(t *testing.T, name string) uint64 {
	t.Helper()
	families, err := legacyregistry.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, f := range families {
		if f.GetName() == name && len(f.GetMetric()) > 0 {
			return f.GetMetric()[0].GetHistogram().GetSampleCount()
		}
	}
	return 0
}

func TestWatchMetrics(t *testing.T) {
	ctx, s := testSetup(t)
	before, err := testutil.GetCounterMetricValue(watchEvents)
	require.NoError(t, err)
	w := watchFrom(ctx, t, s, createPod(ctx, t, s, "start").ResourceVersion)
	createPod(ctx, t, s, "pod")
	nextEvent(t, w)

	after, err := testutil.GetCounterMetricValue(watchEvents)
	require.NoError(t, err)
	require.GreaterOrEqual(t, after, before+2)
	require.NoError(t, s.b.WatchReady())
	require.GreaterOrEqual(t, histogramCount(t, "cockroachstorage_watch_delivery_seconds"), uint64(2))
	s.b.hub.tick(time.Now())
	watchers, err := testutil.GetGaugeMetricValue(openWatchers)
	require.NoError(t, err)
	require.GreaterOrEqual(t, watchers, 1.0)
}

// A replica sees writes another replica made, in order, and reads exactly at
// their revisions, from the timestamps its own feed delivers.
func TestWatchAcrossReplicas(t *testing.T) {
	ctx, a := testSetup(t)
	b := otherReplica(t, a)
	w := watchFrom(ctx, t, b, createPod(ctx, t, a, "start").ResourceVersion)
	var want []string
	for i := range 20 {
		want = append(want, createPod(ctx, t, a, fmt.Sprintf("pod-%02d", i)).ResourceVersion)
	}
	for _, rv := range want {
		requireAdded(t, w, rv)
	}

	rv, err := a.versioner.ParseResourceVersion(want[9])
	require.NoError(t, err)
	_, err = a.b.hub.waitReleased(ctx, a.project, int64(rv))
	require.NoError(t, err)
	wantTS, ok := a.b.revTS.get(a.project, int64(rv))
	require.True(t, ok, "the writer's feed delivered no commit timestamp")
	gotTS, ok := b.b.revTS.get(b.project, int64(rv))
	require.True(t, ok)
	require.Equal(t, wantTS, gotTS)
	list := &example.PodList{}
	require.NoError(t, b.GetList(ctx, "/pods/", storage.ListOptions{
		ResourceVersion: want[9], ResourceVersionMatch: metav1.ResourceVersionMatchExact,
		Predicate: storage.Everything, Recursive: true,
	}, list))
	require.Len(t, list.Items, 11)
}

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

// A watch from a revision the hub has not reached starts once it arrives.
func TestWatchFromFutureRevision(t *testing.T) {
	ctx, a := testSetup(t)
	b := otherReplica(t, a)
	rv, err := a.versioner.ParseResourceVersion(createPod(ctx, t, a, "foo").ResourceVersion)
	require.NoError(t, err)
	w := watchFrom(ctx, t, b, fmt.Sprint(rv+1))
	createPod(ctx, t, a, "bar")
	requireAdded(t, w, createPod(ctx, t, a, "baz").ResourceVersion)
}

// A watch list can't wait for a future revision, so it fails with a retryable
// error instead.
//
// etcd3 checks this in TestTooLargeResourceVersionErrorForWatchList (outside
// the shared tests), so this is reimplemented here.
func TestWatchListFromFutureRevision(t *testing.T) {
	ctx, s := testSetup(t)
	rv, err := s.versioner.ParseResourceVersion(createPod(ctx, t, s, "foo").ResourceVersion)
	require.NoError(t, err)
	initialEvents := true
	pred := storage.Everything
	pred.AllowWatchBookmarks = true
	w, err := s.Watch(ctx, "/pods/", storage.ListOptions{
		ResourceVersion: fmt.Sprint(rv + 100), SendInitialEvents: &initialEvents,
		Predicate: pred, Recursive: true,
	})
	require.NoError(t, err)
	t.Cleanup(w.Stop)
	e := nextEvent(t, w)
	require.Equal(t, watch.Error, e.Type)
	status := e.Object.(*metav1.Status)
	require.True(t, storage.IsTooLargeResourceVersion(apierrors.FromObject(status)), "%v", status)
	require.Positive(t, status.Details.RetryAfterSeconds)
}

// A resource version past every revision is refused rather than taken as a
// revision long gone.
func TestWatchRejectsResourceVersionPastEveryRevision(t *testing.T) {
	ctx, s := testSetup(t)
	_, err := s.Watch(ctx, "/pods/", storage.ListOptions{ResourceVersion: "9223372036854775808", Predicate: storage.Everything, Recursive: true})
	require.True(t, apierrors.IsBadRequest(err), "%v", err)
}

// A feed that drops and resumes from its cursor loses and repeats nothing.
func TestWatchSurvivesFeedRestart(t *testing.T) {
	ctx, s := testSetup(t)
	w := watchFrom(ctx, t, s, createPod(ctx, t, s, "start").ResourceVersion)
	var want []string
	for i := range 30 {
		if i == 10 || i == 20 {
			killFeed(ctx, t, s)
		}
		want = append(want, createPod(ctx, t, s, fmt.Sprintf("pod-%02d", i)).ResourceVersion)
	}
	// A repeated event would arrive before this last write.
	want = append(want, createPod(ctx, t, s, "sentinel").ResourceVersion)
	for _, rv := range want {
		requireAdded(t, w, rv)
	}
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

// binaryTransformer stores values with bytes that are not valid UTF-8, as
// protobuf and encrypted values are.
type binaryTransformer struct{}

const binaryPrefix = "\x00\xff\x80\"\\"

func (binaryTransformer) TransformFromStorage(_ context.Context, data []byte, _ value.Context) ([]byte, bool, error) {
	out, ok := bytes.CutPrefix(data, []byte(binaryPrefix))
	if !ok {
		return nil, false, errors.New("value lacks the binary prefix")
	}
	return out, false, nil
}

func (binaryTransformer) TransformToStorage(_ context.Context, data []byte, _ value.Context) ([]byte, error) {
	return append([]byte(binaryPrefix), data...), nil
}

// Values reach watchers byte for byte through the feed's JSON encoding.
func TestWatchBinaryValues(t *testing.T) {
	ctx, s := testSetup(t, func(c *Config) { c.Transformer = binaryTransformer{} })
	w := watchFrom(ctx, t, s, createPod(ctx, t, s, "start").ResourceVersion)
	requireAdded(t, w, createPod(ctx, t, s, "binary").ResourceVersion)
}

// Destroying a project's store, as Milo does once the Project is gone, ends
// that store's watches, as stopping etcd3's watch cache would, and refuses
// new ones. Other stores on the shared backend carry on.
func TestDestroyEndsTheStoresWatches(t *testing.T) {
	ctx, gone := testSetup(t)
	_, kept := testSetup(t)
	created := createPod(ctx, t, gone, "before")
	w := watchFrom(ctx, t, gone, created.ResourceVersion)
	other := watchFrom(ctx, t, kept, "0")

	gone.destroy()
	_, open := <-w.ResultChan()
	require.False(t, open, "the destroyed store's watch stayed open")
	_, err := gone.Watch(ctx, "/pods/", storage.ListOptions{ResourceVersion: "0", Predicate: storage.Everything, Recursive: true})
	require.Error(t, err)

	requireAdded(t, other, createPod(ctx, t, kept, "after").ResourceVersion)
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
