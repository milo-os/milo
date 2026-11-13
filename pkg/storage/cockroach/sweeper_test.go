package cockroach

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/apis/example"
	"k8s.io/apiserver/pkg/storage"
)

// A replica removes the tombstones it wrote once their retention passes, and
// the feed and watches carry on past the removal, because the hub ignores a
// row that is removed rather than written.
func TestTombstonesArePurged(t *testing.T) {
	ctx, s := testSetup(t)
	r := otherReplica(t, s)
	// An object created again keeps its new row.
	createPod(ctx, t, r, "bar")
	require.NoError(t, r.Delete(ctx, "/pods/test-ns/bar", &example.Pod{}, nil, storage.ValidateAllObjectFunc, nil, storage.DeleteOptions{}))
	createPod(ctx, t, r, "bar")

	w := watchFrom(ctx, t, s, createPod(ctx, t, r, "foo").ResourceVersion)
	require.NoError(t, r.Delete(ctx, "/pods/test-ns/foo", &example.Pod{}, nil, storage.ValidateAllObjectFunc, nil, storage.DeleteOptions{}))
	require.Equal(t, watch.Deleted, nextEvent(t, w).Type)

	require.NoError(t, r.b.sweeper.purge(ctx, time.Now().Add(-time.Hour)))
	require.Equal(t, 1, tombstones(ctx, t, s), "a fresh tombstone was purged")
	require.NoError(t, r.b.sweeper.purge(ctx, time.Now().Add(time.Hour)))
	require.Zero(t, tombstones(ctx, t, s))
	require.NoError(t, s.Get(ctx, "/pods/test-ns/bar", storage.GetOptions{}, &example.Pod{}))

	requireAdded(t, w, createPod(ctx, t, s, "foo").ResourceVersion)
}

// A backlog of tombstones larger than a batch goes in one sweep.
func TestTombstoneBacklogIsPurged(t *testing.T) {
	ctx, s := testSetup(t)
	r := otherReplica(t, s)
	for i := range sweepBatch + 1 {
		name := fmt.Sprintf("pod-%d", i)
		createPod(ctx, t, r, name)
		require.NoError(t, r.Delete(ctx, "/pods/test-ns/"+name, &example.Pod{}, nil, storage.ValidateAllObjectFunc, nil, storage.DeleteOptions{}))
	}
	require.NoError(t, r.b.sweeper.purge(ctx, time.Now().Add(time.Hour)))
	require.Zero(t, tombstones(ctx, t, s))
}

// The backstop removes tombstones that no replica remembers, and leaves live
// objects alone.
func TestBackstopRemovesUnqueuedTombstones(t *testing.T) {
	ctx, s := testSetup(t)
	r := otherReplica(t, s)
	createPod(ctx, t, r, "gone")
	require.NoError(t, r.Delete(ctx, "/pods/test-ns/gone", &example.Pod{}, nil, storage.ValidateAllObjectFunc, nil, storage.DeleteOptions{}))
	createPod(ctx, t, r, "back")
	_, err := s.b.pool.Exec(ctx, `UPDATE objects SET deleted_at = now() - INTERVAL '1 day' WHERE project = $1 AND deleted`, s.project)
	require.NoError(t, err)

	// The backstop reads a follower-read snapshot, a few seconds behind, which
	// can predate the test's database.
	other := otherReplica(t, s)
	require.Eventually(t, func() bool {
		return other.b.sweeper.backstop(ctx, time.Now().Add(-time.Hour)) == nil && tombstones(ctx, t, s) == 0
	}, 30*time.Second, 500*time.Millisecond)
	require.NoError(t, s.Get(ctx, "/pods/test-ns/back", storage.GetOptions{}, &example.Pod{}))
}

// A tombstone queued by an earlier delete doesn't remove the tombstone of a
// later delete of the same key.
func TestPurgeChecksTheRevision(t *testing.T) {
	ctx, s := testSetup(t)
	r := otherReplica(t, s)
	rev := func() int64 {
		var rv int64
		require.NoError(t, s.b.pool.QueryRow(ctx, `SELECT rv FROM objects WHERE project = $1 AND key = '/pods/test-ns/foo'`, s.project).Scan(&rv))
		return rv
	}
	createPod(ctx, t, r, "foo")
	require.NoError(t, r.Delete(ctx, "/pods/test-ns/foo", &example.Pod{}, nil, storage.ValidateAllObjectFunc, nil, storage.DeleteOptions{}))
	first := rev()
	createPod(ctx, t, r, "foo")
	require.NoError(t, r.Delete(ctx, "/pods/test-ns/foo", &example.Pod{}, nil, storage.ValidateAllObjectFunc, nil, storage.DeleteOptions{}))
	require.NotEqual(t, first, rev())

	require.NoError(t, r.b.sweeper.remove(ctx, []tombstone{{project: s.project, key: "/pods/test-ns/foo", rev: first}}))
	require.Equal(t, 1, tombstones(ctx, t, s))
	require.NoError(t, r.b.sweeper.purge(ctx, time.Now().Add(time.Hour)))
	require.Zero(t, tombstones(ctx, t, s))
}

// The queue hands out its oldest tombstones first, at most n at a time, and
// lets go of its array once empty.
func TestTombstoneQueue(t *testing.T) {
	var q tombstoneQueue
	start := time.Now()
	for i := range 3 {
		q.push(tombstone{key: fmt.Sprint(i), at: start.Add(time.Duration(i) * time.Second)})
	}
	require.Empty(t, q.popBefore(start, 10))
	due := q.popBefore(start.Add(time.Hour), 2)
	require.Equal(t, []string{"0", "1"}, []string{due[0].key, due[1].key})
	require.Equal(t, "2", q.popBefore(start.Add(time.Hour), 2)[0].key)
	require.Nil(t, q.items)
}

// tombstones counts s's project's tombstones.
func tombstones(ctx context.Context, t *testing.T, s testStore) int {
	t.Helper()
	var n int
	require.NoError(t, s.b.pool.QueryRow(ctx, `SELECT count(*) FROM objects WHERE project = $1 AND deleted`, s.project).Scan(&n))
	return n
}

// createExpiring creates a pod with a TTL of an hour, then moves its expiry
// into the past.
func createExpiring(ctx context.Context, t *testing.T, s testStore, name string) *example.Pod {
	t.Helper()
	key := "/pods/test-ns/" + name
	created := &example.Pod{}
	require.NoError(t, s.Create(ctx, key, newPod(name), created, 3600))
	var remaining float64
	require.NoError(t, s.b.pool.QueryRow(ctx, `SELECT extract(epoch FROM expires_at - now())::FLOAT8
		FROM objects WHERE project = $1 AND key = $2`, s.project, key).Scan(&remaining))
	require.InDelta(t, 3600, remaining, 10)
	_, err := s.b.pool.Exec(ctx, `UPDATE objects SET expires_at = now() - INTERVAL '1 hour'
		WHERE project = $1 AND key = $2`, s.project, key)
	require.NoError(t, err)
	return created
}

func requireDeleted(t *testing.T, w watch.Interface, name, after string) string {
	t.Helper()
	e := nextEvent(t, w)
	pod, ok := e.Object.(*example.Pod)
	require.True(t, ok, "%s %#v", e.Type, e.Object)
	require.Equal(t, watch.Deleted, e.Type)
	require.Equal(t, name, pod.Name)
	require.NotEqual(t, after, pod.ResourceVersion, "the delete has no revision of its own")
	return pod.ResourceVersion
}

// An object whose TTL has passed stays visible until its delete commits, and
// the delete reaches every replica at a revision of its own.
func TestExpiryIsOrderedByRevision(t *testing.T) {
	ctx, s := testSetup(t)
	other := otherReplica(t, s)
	list := func(rv string) *example.PodList {
		t.Helper()
		opts := storage.ListOptions{Predicate: storage.Everything, Recursive: true}
		if rv != "" {
			opts.ResourceVersion, opts.ResourceVersionMatch = rv, metav1.ResourceVersionMatchExact
		}
		out := &example.PodList{}
		require.NoError(t, s.GetList(ctx, "/pods/", opts, out))
		return out
	}
	created := createExpiring(ctx, t, s, "short-lived")
	w := watchFrom(ctx, t, other, created.ResourceVersion)

	before := list("")
	require.Len(t, before.Items, 1, "listed past its TTL, before the sweep")
	require.Equal(t, created.ResourceVersion, before.ResourceVersion)
	require.NoError(t, s.Get(ctx, "/pods/test-ns/short-lived", storage.GetOptions{}, &example.Pod{}))

	require.NoError(t, s.b.sweeper.expire(ctx))
	deleted := requireDeleted(t, w, "short-lived", created.ResourceVersion)
	after := list("")
	require.Empty(t, after.Items)
	require.Equal(t, deleted, after.ResourceVersion)
	require.Len(t, list(before.ResourceVersion).Items, 1, "the exact list before the delete lost the object")
	require.True(t, storage.IsNotFound(other.Get(ctx, "/pods/test-ns/short-lived", storage.GetOptions{}, &example.Pod{})))
}
