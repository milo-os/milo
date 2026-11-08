package cockroach

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/apis/example"
	"k8s.io/apiserver/pkg/storage"
)

// groupCommitSetup is a store with GroupCommit on, on a backend of its own,
// so the batching tests run even when the suite runs without it.
func groupCommitSetup(t *testing.T) (context.Context, testStore) {
	t.Helper()
	ctx, s := testSetup(t)
	return ctx, otherReplica(t, s, GroupCommit(true))
}

// Concurrent writes in one project share batches, get gapless revisions, and
// reach a watch in revision order.
func TestGroupCommitBatchesConcurrentWrites(t *testing.T) {
	ctx, s := groupCommitSetup(t)
	start := createPod(ctx, t, s, "start")
	w := watchFrom(ctx, t, s, start.ResourceVersion)

	const writers = 64
	revs := make([]string, writers)
	var wg sync.WaitGroup
	for i := range writers {
		wg.Go(func() { revs[i] = createPod(ctx, t, s, fmt.Sprintf("pod-%02d", i)).ResourceVersion })
	}
	wg.Wait()

	sorted := slices.SortedFunc(slices.Values(revs), func(a, b string) int { return int(parseRV(t, a) - parseRV(t, b)) })
	first := parseRV(t, start.ResourceVersion)
	for i, rv := range sorted {
		require.Equal(t, first+1+int64(i), parseRV(t, rv), "revisions %v are not gapless after %d", sorted, first)
	}
	for _, rv := range sorted {
		requireAdded(t, w, rv)
	}
}

// Concurrent updates of one key lose none. A later write to a key waits for
// the next batch, and conflicts send GuaranteedUpdate round again.
func TestGroupCommitSameKeyUpdates(t *testing.T) {
	ctx, s := groupCommitSetup(t)
	createPod(ctx, t, s, "counter")
	const updates = 32
	var wg sync.WaitGroup
	for range updates {
		wg.Go(func() {
			err := s.GuaranteedUpdate(ctx, "/pods/test-ns/counter", &example.Pod{}, false, nil,
				storage.SimpleUpdate(func(obj runtime.Object) (runtime.Object, error) {
					pod := obj.(*example.Pod)
					n, _ := strconv.Atoi(pod.Annotations["n"])
					pod.Annotations = map[string]string{"n": strconv.Itoa(n + 1)}
					return pod, nil
				}), nil)
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	got := &example.Pod{}
	require.NoError(t, s.Get(ctx, "/pods/test-ns/counter", storage.GetOptions{}, got))
	require.Equal(t, strconv.Itoa(updates), got.Annotations["n"])
}

// Two replicas with group commit writing into one project at once keep its revisions
// gapless, as a third replica's watch sees them.
func TestGroupCommitAcrossReplicas(t *testing.T) {
	ctx, base := testSetup(t)
	a, b := otherReplica(t, base, GroupCommit(true)), otherReplica(t, base, GroupCommit(true))
	start := createPod(ctx, t, base, "start")
	w := watchFrom(ctx, t, base, start.ResourceVersion)

	const perReplica = 32
	var wg sync.WaitGroup
	for i := range 2 * perReplica {
		s := []testStore{a, b}[i%2]
		wg.Go(func() { createPod(ctx, t, s, fmt.Sprintf("pod-%02d", i)) })
	}
	wg.Wait()
	first := parseRV(t, start.ResourceVersion)
	for i := range int64(2 * perReplica) {
		e := nextEvent(t, w)
		require.Equal(t, strconv.FormatInt(first+1+i, 10), e.Object.(*example.Pod).ResourceVersion)
	}
}

type podWrite struct {
	pod     *example.Pod
	deleted bool
}

// commitPodBatch commits writes in one batch, as a committer would, and
// returns their revisions.
func commitPodBatch(ctx context.Context, t *testing.T, s testStore, writes ...podWrite) []string {
	t.Helper()
	var batch []write
	for _, w := range writes {
		pod := w.pod.DeepCopy()
		expected, err := s.versioner.ObjectResourceVersion(pod)
		require.NoError(t, err)
		require.NoError(t, s.versioner.PrepareObjectForStorage(pod))
		key, err := s.prepareKey("/pods/"+pod.Namespace+"/"+pod.Name, false)
		require.NoError(t, err)
		data, err := runtime.Encode(s.codec, pod)
		require.NoError(t, err)
		data, err = s.transformer.TransformToStorage(ctx, data, s.authData(key))
		require.NoError(t, err)
		next := createWrite(key, data, 0)
		if expected > 0 {
			next = updateWrite(key, int64(expected), data, 0)
		}
		if w.deleted {
			next = deleteWrite(key, int64(expected))
		}
		batch = append(batch, next)
	}
	b, err := newWriteBatch(batch...)
	require.NoError(t, err)
	results, err := retry(ctx, "write", func() ([]writeResult, error) { return s.b.tryCommitBatch(ctx, s.project, b) })
	require.NoError(t, err)
	var revs []string
	for _, res := range results {
		require.True(t, res.written)
		revs = append(revs, strconv.FormatInt(res.rev, 10))
	}
	// Exact reads at the batch's revisions need their timestamp from the feed.
	_, err = s.b.hub.waitReleased(ctx, s.project, results[len(results)-1].rev)
	require.NoError(t, err)
	return revs
}

// Exact lists at revisions inside one batch see the state at that revision,
// not at the batch's end. A key written later in the batch shows its value
// from before the batch, and so does a key deleted later.
// An exact list at a revision inside a batch, on a replica that knows only
// the next revision's commit time, can't read just before that commit, which
// is before the whole batch. It reports the revision expired, never too large.
func TestGroupCommitExactListWithOnlyTheSuccessorsTime(t *testing.T) {
	ctx, s := testSetup(t)
	revs := commitPodBatch(ctx, t, s, podWrite{pod: newPod("a")}, podWrite{pod: newPod("b")})
	createPod(ctx, t, s, "later")
	rev := parseRV(t, revs[0])
	_, err := s.b.hub.waitReleased(ctx, s.project, rev+1)
	require.NoError(t, err)
	s.b.revTS.forget(s.project, rev)
	err = s.GetList(ctx, "/pods/", storage.ListOptions{
		ResourceVersion: revs[0], ResourceVersionMatch: metav1.ResourceVersionMatchExact,
		Predicate: storage.Everything, Recursive: true,
	}, &example.PodList{})
	require.True(t, apierrors.IsResourceExpired(err), "%v", err)
}

func TestGroupCommitExactListInsideBatch(t *testing.T) {
	ctx, s := testSetup(t)
	a, b := createPod(ctx, t, s, "a"), createPod(ctx, t, s, "b")
	createPod(ctx, t, s, "first")
	a.Annotations = map[string]string{"v": "new"}
	revs := commitPodBatch(ctx, t, s, podWrite{pod: a}, podWrite{pod: b, deleted: true}, podWrite{pod: newPod("c")})
	first, _ := s.b.revTS.get(s.project, parseRV(t, revs[0]))
	last, _ := s.b.revTS.get(s.project, parseRV(t, revs[2]))
	require.Equal(t, first, last, "revisions %v did not commit in one batch", revs)

	list := func(rv string, limit int64, continueToken string) *example.PodList {
		t.Helper()
		opts := storage.ListOptions{Predicate: storage.Everything, Recursive: true}
		opts.Predicate.Limit, opts.Predicate.Continue = limit, continueToken
		if continueToken == "" {
			opts.ResourceVersion, opts.ResourceVersionMatch = rv, metav1.ResourceVersionMatchExact
		}
		out := &example.PodList{}
		require.NoError(t, s.GetList(ctx, "/pods/", opts, out), "exact list at %s", rv)
		return out
	}
	names := func(l *example.PodList) string {
		var out []string
		for _, p := range l.Items {
			out = append(out, p.Name+"="+p.Annotations["v"])
		}
		return fmt.Sprint(out)
	}
	// first committed in the batch before, so every revision here has it.
	for i, want := range []string{"[a=new b= first=]", "[a=new first=]", "[a=new c= first=]"} {
		require.Equal(t, want, names(list(revs[i], 0, "")), "exact list at %s, write %d of the batch", revs[i], i+1)
	}

	// Paged, one item at a time, at the first revision of the batch.
	page := list(revs[0], 1, "")
	require.Equal(t, "[a=new]", names(page))
	require.NotEmpty(t, page.Continue)
	require.Equal(t, int64(2), *page.RemainingItemCount, "b and first remain")
	require.Equal(t, "[b=]", names(list(revs[0], 1, page.Continue)))
}

// Exact lists inside a batch use one connection each, so more of them at once
// than the pool has connections still finish.
func TestGroupCommitInteriorListsShareTheirConnection(t *testing.T) {
	ctx, base := testSetup(t)
	s := otherReplica(t, base, MaxConns(2))
	createPod(ctx, t, s, "seed")
	revs := commitPodBatch(ctx, t, s, podWrite{pod: newPod("x")}, podWrite{pod: newPod("y")}, podWrite{pod: newPod("z")})

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			err := s.GetList(ctx, "/pods/", storage.ListOptions{
				ResourceVersion: revs[0], ResourceVersionMatch: metav1.ResourceVersionMatchExact,
				Predicate: storage.Everything, Recursive: true,
			}, &example.PodList{})
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
}

func testWrite(ctx context.Context, key string, size int) *queuedWrite {
	return newQueuedWrite(ctx, createWrite(key, make([]byte, size), 0))
}

// batches splits pending into the batches nextBatch takes, one after another.
func batches(pending ...*queuedWrite) [][]*queuedWrite {
	var out [][]*queuedWrite
	for batch, rest := nextBatch(pending); batch != nil; batch, rest = nextBatch(rest) {
		out = append(out, batch)
	}
	return out
}

func TestNextBatch(t *testing.T) {
	ctx := t.Context()
	t.Run("one write per key in arrival order", func(t *testing.T) {
		a1, a2, b, a3 := testWrite(ctx, "a", 0), testWrite(ctx, "a", 0), testWrite(ctx, "b", 0), testWrite(ctx, "a", 0)
		require.Equal(t, [][]*queuedWrite{{a1, b}, {a2}, {a3}}, batches(a1, a2, b, a3))
	})
	t.Run("write limit", func(t *testing.T) {
		var pending []*queuedWrite
		for i := range maxBatchWrites + 1 {
			pending = append(pending, testWrite(ctx, fmt.Sprint(i), 0))
		}
		require.Equal(t, [][]*queuedWrite{pending[:maxBatchWrites], pending[maxBatchWrites:]}, batches(pending...))
	})
	t.Run("byte limit admits an oversized first write", func(t *testing.T) {
		large, small := testWrite(ctx, "large", maxBatchBytes+1), testWrite(ctx, "small", 1)
		require.Equal(t, [][]*queuedWrite{{large}, {small}}, batches(large, small))
	})
	t.Run("byte limit fits smaller later keys", func(t *testing.T) {
		a, b, c := testWrite(ctx, "a", maxBatchBytes-1), testWrite(ctx, "b", 2), testWrite(ctx, "c", 1)
		require.Equal(t, [][]*queuedWrite{{a, c}, {b}}, batches(a, b, c))
	})
	t.Run("byte limit preserves same-key order", func(t *testing.T) {
		a, first, second := testWrite(ctx, "a", maxBatchBytes-1), testWrite(ctx, "b", 2), testWrite(ctx, "b", 1)
		require.Equal(t, [][]*queuedWrite{{a}, {first}, {second}}, batches(a, first, second))
	})
	t.Run("cancelled write does not reserve its key", func(t *testing.T) {
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		a, b := testWrite(cancelled, "a", 0), testWrite(ctx, "a", 0)
		require.Equal(t, [][]*queuedWrite{{b}}, batches(a, b))
		require.ErrorIs(t, (<-a.done).err, context.Canceled)
	})
}

func TestGroupCommitBatchesQueuedWrites(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		next := newGate()
		g := newGroupCommitter(next)
		defer g.stop()
		first := commitAsync(t.Context(), g, "project", "first")
		require.Len(t, <-next.started, 1)
		second, third := commitAsync(t.Context(), g, "project", "second"), commitAsync(t.Context(), g, "project", "third")
		synctest.Wait()
		next.proceed <- struct{}{}
		require.NoError(t, (<-first).err)

		batch := <-next.started
		require.Len(t, batch, 2)
		next.proceed <- struct{}{}
		// Each write gets its own result.
		for key, done := range map[string]<-chan writeOutcome{"second": second, "third": third} {
			r := <-done
			require.NoError(t, r.err)
			require.Equal(t, key, batch[r.result.rev].key)
		}
	})
}

// A project's goroutine ends once its queue is empty.
func TestGroupCommitEndsIdleProjects(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		next := newGate()
		g := newGroupCommitter(next)
		defer g.stop()
		done := commitAsync(t.Context(), g, "project", "a")
		<-next.started
		next.proceed <- struct{}{}
		require.NoError(t, (<-done).err)
		synctest.Wait()
		g.mu.Lock()
		defer g.mu.Unlock()
		require.Empty(t, g.queues)
	})
}

// Callers that give up while their writes wait behind a stalled batch leave
// the queue at once, so retries in their place cannot grow it.
func TestGroupCommitWithdrawsCancelledWrites(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		next := newGate()
		g := newGroupCommitter(next)
		defer g.stop()
		stalled := commitAsync(t.Context(), g, "project", "stalled")
		<-next.started
		for i := range 3 {
			ctx, cancel := context.WithCancel(t.Context())
			cancelled := commitAsync(ctx, g, "project", fmt.Sprintf("cancelled-%d", i))
			synctest.Wait()
			cancel()
			require.ErrorIs(t, (<-cancelled).err, context.Canceled)
		}
		retried := commitAsync(t.Context(), g, "project", "retried")
		synctest.Wait()
		g.mu.Lock()
		require.Len(t, g.queues["project"], 1)
		g.mu.Unlock()

		next.proceed <- struct{}{}
		require.NoError(t, (<-stalled).err)
		require.Equal(t, []write{createWrite("retried", nil, 0)}, <-next.started)
		next.proceed <- struct{}{}
		require.NoError(t, (<-retried).err)
	})
}

// A caller that gives up while its write is in a batch returns at once, with
// its project's slot free, and the batch still finishes.
func TestGroupCommitReturnsWhenCancelledMidBatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		next := newGate()
		g := newGroupCommitter(next)
		defer g.stop()
		a := newAdmission("project", 1, time.Second)
		ctx, cancel := context.WithCancel(t.Context())
		done := commitAsync(ctx, admittedWrites{a, g}, "project", "active")
		<-next.started
		cancel()
		require.ErrorIs(t, (<-done).err, context.Canceled)
		require.Empty(t, a.keys)
		next.proceed <- struct{}{}
		synctest.Wait()
	})
}

// Closing the backend answers writes still waiting instead of leaving them
// hanging, and turns new ones away.
func TestGroupCommitStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		next := newGate()
		g := newGroupCommitter(next)
		active := commitAsync(t.Context(), g, "project", "active")
		<-next.started
		waiting := commitAsync(t.Context(), g, "project", "queued")
		synctest.Wait()
		g.stop()
		require.ErrorIs(t, (<-active).err, context.Canceled)
		require.ErrorIs(t, (<-waiting).err, errBackendClosed)
		require.Empty(t, g.queues)
		require.ErrorIs(t, (<-commitAsync(t.Context(), g, "project", "late")).err, errBackendClosed)
	})
}
