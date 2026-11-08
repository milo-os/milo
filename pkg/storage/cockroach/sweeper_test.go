package cockroach

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/apis/example"
	"k8s.io/apiserver/pkg/storage"
)

// Tombstones go once their retention passes, and the feed and watches carry
// on past the removal, because the hub ignores a row that is removed rather
// than written.
func TestTombstonesArePurged(t *testing.T) {
	ctx, s := testSetup(t)
	w := watchFrom(ctx, t, s, createPod(ctx, t, s, "foo").ResourceVersion)
	require.NoError(t, s.Delete(ctx, "/pods/test-ns/foo", &example.Pod{}, nil, storage.ValidateAllObjectFunc, nil, storage.DeleteOptions{}))
	require.Equal(t, watch.Deleted, nextEvent(t, w).Type)

	tombstone := func() error {
		var deleted bool
		return s.b.pool.QueryRow(ctx, `SELECT deleted FROM objects WHERE project = $1 AND key = '/pods/test-ns/foo'`, s.project).Scan(&deleted)
	}
	require.NoError(t, s.b.sweeper.purge(ctx))
	require.NoError(t, tombstone(), "a fresh tombstone was purged")

	_, err := s.b.pool.Exec(ctx, `UPDATE objects SET deleted_at = now() - INTERVAL '1 day'
		WHERE project = $1 AND key = '/pods/test-ns/foo'`, s.project)
	require.NoError(t, err)
	require.NoError(t, s.b.sweeper.purge(ctx))
	require.ErrorIs(t, tombstone(), pgx.ErrNoRows)

	requireAdded(t, w, createPod(ctx, t, s, "foo").ResourceVersion)
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
