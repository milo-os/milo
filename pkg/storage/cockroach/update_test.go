package cockroach

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/apis/example"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/component-base/metrics/testutil"
)

// An update or delete starts from the hub's newest version of its key and
// reads the key only when the hub has none, even when that version is stale.
func TestUpdateStartsFromTheHubsRecentWrite(t *testing.T) {
	ctx, s := testSetup(t)
	createPod(ctx, t, s, "a")
	key, err := s.prepareKey("/pods/test-ns/a", false)
	require.NoError(t, err)
	stored := func() *row {
		r, _, err := s.readKey(ctx, key, false)
		require.NoError(t, err)
		return r
	}

	h := memoryHub()
	cached := newTestStore(t, &Backend{pool: s.b.pool, committer: s.b.committer, revTS: s.b.revTS, hub: h}, testConfig(s.project))
	recent := func(r *row) {
		h.mu.Lock()
		defer h.mu.Unlock()
		p := h.newProjectStream(r.rev - 1)
		p.seq.log.push(&feedEvent{op: watch.Modified, key: r.key, rev: r.rev, value: r.value})
		h.projects[s.project] = p
	}
	reads := func(f func()) uint64 {
		before, err := testutil.GetHistogramMetricCount(dbOperations.WithLabelValues("get"))
		require.NoError(t, err)
		f()
		after, err := testutil.GetHistogramMetricCount(dbOperations.WithLabelValues("get"))
		require.NoError(t, err)
		return after - before
	}
	n := 0
	update := func() {
		n++
		require.NoError(t, cached.GuaranteedUpdate(ctx, "/pods/test-ns/a", &example.Pod{}, false, nil,
			func(obj runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
				pod := obj.(*example.Pod)
				pod.Labels = map[string]string{"n": strconv.Itoa(n)}
				return pod, nil, nil
			}, nil))
	}

	recent(stored())
	update()
	require.Zero(t, reads(update), "stale: the write's revision check returns the current row")
	recent(stored())
	require.Zero(t, reads(update), "current")
	h.mu.Lock()
	delete(h.projects, s.project)
	h.mu.Unlock()
	require.EqualValues(t, 1, reads(update), "unknown to the hub")

	recent(stored())
	require.Zero(t, reads(func() {
		require.NoError(t, cached.Delete(ctx, "/pods/test-ns/a", &example.Pod{}, nil, storage.ValidateAllObjectFunc, nil, storage.DeleteOptions{}))
	}))
	require.Nil(t, stored())
}
