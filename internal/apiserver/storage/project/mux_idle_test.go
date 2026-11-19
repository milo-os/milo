package projectstorage

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/goleak"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	generic "k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/storage"
	storagebackend "k8s.io/apiserver/pkg/storage/storagebackend"
	factory "k8s.io/apiserver/pkg/storage/storagebackend/factory"
	"k8s.io/client-go/tools/cache"
	"k8s.io/component-base/metrics/testutil"
	clocktesting "k8s.io/utils/clock/testing"
)

// testIdle is the idle time of the tests here.
const testIdle = time.Minute

// cacheStorage is a child's storage with a watch and a ready state, as a
// watch cache has.
type cacheStorage struct {
	fakeStorage
	unready *atomic.Bool
}

func (s cacheStorage) Watch(context.Context, string, storage.ListOptions) (watch.Interface, error) {
	return watch.NewFake(), nil
}

func (s cacheStorage) ReadinessCheck() error {
	if s.unready != nil && s.unready.Load() {
		return errors.New("storage is (re)initializing")
	}
	return nil
}

// cachesOf builds cacheStorages as children, counted in children, each
// unready while unready holds true.
func cachesOf(children *countingChildren, unready *atomic.Bool) generic.StorageDecorator {
	return func(cfg *storagebackend.ConfigForResource, p string, k func(runtime.Object) (string, error),
		n func() runtime.Object, l func() runtime.Object, a storage.AttrFunc, t storage.IndexerFuncs,
		i *cache.Indexers) (storage.Interface, factory.DestroyFunc, error) {
		_, destroy, _ := children.storage(cfg, p, k, n, l, a, t, i)
		return cacheStorage{unready: unready}, destroy, nil
	}
}

// newIdleMux is a configmaps mux whose children are cacheStorages, counted in
// children, whose idle time is testIdle, and whose clock is clk. No readiness
// check holds configmaps.
func newIdleMux(children *countingChildren, unready *atomic.Bool) (*projectMux, *clocktesting.FakeClock) {
	m := newTestMux(nil, everyProjectKnown())
	clk := clocktesting.NewFakeClock(time.Now())
	m.args = decoratorArgs{resourceKind: "configmaps"}
	m.idleTimeout, m.drain, m.clock = testIdle, testDrain, clk
	m.children[""].clock = clk
	m.inner = cachesOf(children, unready)
	return m, clk
}

// refusingWatches wraps inner so each child refuses every watch, as a cache
// that is still starting does.
func refusingWatches(inner generic.StorageDecorator) generic.StorageDecorator {
	return func(cfg *storagebackend.ConfigForResource, p string, k func(runtime.Object) (string, error),
		n func() runtime.Object, l func() runtime.Object, a storage.AttrFunc, t storage.IndexerFuncs,
		i *cache.Indexers) (storage.Interface, factory.DestroyFunc, error) {
		s, destroy, err := inner(cfg, p, k, n, l, a, t, i)
		return refusingWatch{s}, destroy, err
	}
}

type refusingWatch struct{ storage.Interface }

func (refusingWatch) Watch(context.Context, string, storage.ListOptions) (watch.Interface, error) {
	return nil, errors.New("storage is (re)initializing")
}

func (m *projectMux) childCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.children)
}

// TestIdleChildIsRetiredAndRebuilt covers the idle retire: a child that no
// request used for the idle time is removed and destroyed after the drain,
// counted with reason idle, and the next request into its Project builds it
// again.
func TestIdleChildIsRetiredAndRebuilt(t *testing.T) {
	var children countingChildren
	m, clk := newIdleMux(&children, nil)
	idleBefore := removals(t, m, removedIdle)
	get(t, m, "p")

	clk.Step(testIdle / 2)
	m.retireIdle()
	if n := m.childCount(); n != 2 {
		t.Fatalf("expected a child used within the idle time to stay, got %d children", n)
	}

	clk.Step(testIdle / 2)
	m.retireIdle()
	if n := m.childCount(); n != 1 {
		t.Fatalf("expected only the root plane's child after the idle retire, got %d children", n)
	}
	waitFor(t, "the idle child to be destroyed", func() bool { return children.destroyed.Load() == 1 })
	if got := removals(t, m, removedIdle) - idleBefore; got != 1 {
		t.Fatalf("expected one removal counted as idle, got %v", got)
	}

	get(t, m, "p")
	if n := children.built.Load(); n != 2 {
		t.Fatalf("expected the next request to build the child again, got %d built", n)
	}
}

// TestARetiredChildIsRetiredOnce covers the sweep and ProjectGone on one
// child, in either order: the first takes it out of the mux, so the second
// finds nothing, and the child is retired, and so destroyed, once.
func TestARetiredChildIsRetiredOnce(t *testing.T) {
	for _, sweepFirst := range []bool{true, false} {
		var children countingChildren
		m, clk := newIdleMux(&children, nil)
		idleBefore, goneBefore := removals(t, m, removedIdle), removals(t, m, removedProjectGone)
		get(t, m, "p")
		clk.Step(testIdle)
		if sweepFirst {
			m.retireIdle()
			m.ProjectGone("p")
		} else {
			m.ProjectGone("p")
			m.retireIdle()
		}
		idle, gone := removals(t, m, removedIdle)-idleBefore, removals(t, m, removedProjectGone)-goneBefore
		if want := map[bool][2]float64{true: {1, 0}, false: {0, 1}}[sweepFirst]; [2]float64{idle, gone} != want {
			t.Fatalf("sweep first=%v: expected %v retires (idle, project gone), got [%v %v]", sweepFirst, want, idle, gone)
		}
	}
}

// TestARequestRenewsTheIdleTime covers a child in use: each request starts
// its idle time again.
func TestARequestRenewsTheIdleTime(t *testing.T) {
	var children countingChildren
	m, clk := newIdleMux(&children, nil)
	get(t, m, "p")
	for range 3 {
		clk.Step(testIdle * 3 / 4)
		get(t, m, "p")
		m.retireIdle()
	}
	if n := m.childCount(); n != 2 {
		t.Fatalf("expected a child asked for within each idle time to stay, got %d children", n)
	}
	if n := children.built.Load(); n != 1 {
		t.Fatalf("expected the child built once and kept, got %d built", n)
	}
	clk.Step(testIdle)
	m.retireIdle()
	if n := m.childCount(); n != 1 {
		t.Fatalf("expected the child to go once idle, got %d children", n)
	}
}

// TestWatchedChildIsKept covers an open watch: its child is in use however
// long the watch lasts, and the idle time starts when the watch stops.
func TestWatchedChildIsKept(t *testing.T) {
	var children countingChildren
	m, clk := newIdleMux(&children, nil)
	w, err := m.Watch(requestFor("p", "watch"), "/k", storage.ListOptions{})
	if err != nil {
		t.Fatalf("watch: %v", err)
	}

	clk.Step(10 * testIdle)
	m.retireIdle()
	if n := m.childCount(); n != 2 {
		t.Fatalf("expected the watched child to stay, got %d children", n)
	}

	w.Stop()
	w.Stop() // a second Stop ends nothing more
	clk.Step(testIdle / 2)
	m.retireIdle()
	if n := m.childCount(); n != 2 {
		t.Fatalf("expected the child to stay for the idle time after its watch stopped, got %d children", n)
	}
	clk.Step(testIdle / 2)
	m.retireIdle()
	if n := m.childCount(); n != 1 {
		t.Fatalf("expected the child to go once idle after its watch stopped, got %d children", n)
	}
}

// TestFailedWatchHoldsNoChild covers a watch the child refused: it opened
// nothing, so it holds nothing.
func TestFailedWatchHoldsNoChild(t *testing.T) {
	var children countingChildren
	m, clk := newIdleMux(&children, nil)
	m.inner = refusingWatches(m.inner)
	if _, err := m.Watch(requestFor("p", "watch"), "/k", storage.ListOptions{}); err == nil {
		t.Fatal("expected the watch to be refused")
	}
	clk.Step(testIdle)
	m.retireIdle()
	if n := m.childCount(); n != 1 {
		t.Fatalf("expected a refused watch to hold no child, got %d children", n)
	}
}

// TestColdChildIsKept covers a child whose cache has not served yet: its
// first list is still running, so the sweep keeps it, as the kubelet keeps an
// unsynced idle watch. Once it serves, it goes when idle.
func TestColdChildIsKept(t *testing.T) {
	var children countingChildren
	var unready atomic.Bool
	unready.Store(true)
	m, clk := newIdleMux(&children, &unready)
	get(t, m, "p")

	clk.Step(10 * testIdle)
	m.retireIdle()
	if n := m.childCount(); n != 2 {
		t.Fatalf("expected the cold child to stay, got %d children", n)
	}
	unready.Store(false)
	m.retireIdle()
	if n := m.childCount(); n != 1 {
		t.Fatalf("expected the child to go once it serves, got %d children", n)
	}
}

// TestRootChildIsNeverIdle covers the root plane's child, which the mux's own
// readiness check and versioner read.
func TestRootChildIsNeverIdle(t *testing.T) {
	var children countingChildren
	m, clk := newIdleMux(&children, nil)
	m.children[""] = &child{s: cacheStorage{}, clock: clk}
	clk.Step(10 * testIdle)
	m.retireIdle()
	if n := m.childCount(); n != 1 {
		t.Fatalf("expected the root plane's child to stay, got %d children", n)
	}
}

// TestChildrenAReadinessCheckHoldsAreKept covers a resource whose children
// the server waits for at its start, the ResourceClaims that quota admission
// watches: the sweep keeps them warm too, and keeps retiring other kinds.
func TestChildrenAReadinessCheckHoldsAreKept(t *testing.T) {
	held := schema.GroupResource{Group: "idle.test.miloapis.com", Resource: "held"}
	// The enabled case runs first: the disabled one must pass after it too.
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "enabled", false: "disabled"}[enabled], func(t *testing.T) {
			gvr := metav1.GroupVersionResource{Group: held.Group, Version: "v1", Resource: held.Resource}
			RegisterChildrenReadiness(&registrar{}, enabled, gvr, NewChildrenReadiness(held, &view{}, served, 50))
			t.Cleanup(func() { forgetHeldWarm(held) })
			var children countingChildren
			m, clk := newIdleMux(&children, nil)
			m.args = decoratorArgs{resourceGroup: held.Group, resourceKind: held.Resource}
			get(t, m, "p")
			clk.Step(10 * testIdle)
			m.retireIdle()
			if want := map[bool]int{false: 1, true: 2}[enabled]; m.childCount() != want {
				t.Fatalf("readiness check enabled=%v: expected %d children, got %d", enabled, want, m.childCount())
			}
		})
	}

	var children countingChildren
	other, clk := newIdleMux(&children, nil)
	get(t, other, "p")
	clk.Step(10 * testIdle)
	other.retireIdle()
	if n := other.childCount(); n != 1 {
		t.Fatalf("expected another kind's idle child to go, got %d children", n)
	}
}

// sweptIdle is the idle time of a decorated storage under test: under a
// minute, so its sweep runs at the idle time, and short, so the idle child
// goes well within waitFor's deadline.
const sweptIdle = 50 * time.Millisecond

// TestDecoratedStorageRetiresIdleChildren covers the idle time end to end: a
// decorated storage sweeps on its own, at the idle time when it is under a
// minute, and 0 starts no sweep.
func TestDecoratedStorageRetiresIdleChildren(t *testing.T) {
	for _, idle := range []time.Duration{sweptIdle, 0} {
		var children countingChildren
		s, destroy, err := ProjectAwareDecorator(schema.GroupResource{Resource: "configmaps"}, cachesOf(&children, nil),
			everyProjectKnown(), nil, testDrain, idle)(&storagebackend.ConfigForResource{}, "", nil, nil, nil, nil, nil, nil)
		if err != nil {
			t.Fatalf("decorate configmaps: %v", err)
		}
		t.Cleanup(destroy)
		if err := s.Get(requestFor("p", "get"), "/k", storage.GetOptions{}, nil); err != nil {
			t.Fatalf("get: %v", err)
		}
		if n := children.built.Load(); n != 2 {
			t.Fatalf("expected the root plane's child and one Project's, got %d built", n)
		}
		if idle > 0 {
			waitFor(t, "the idle child to be destroyed", func() bool { return children.destroyed.Load() == 1 })
			continue
		}
		if s.(*projectMux).stopSweep != nil {
			t.Fatal("expected no sweep with the idle time at 0")
		}
	}
}

// TestDestroyStopsTheSweep covers a storage torn down, as apiextensions tears
// down a CRD's: its sweep ends with it.
func TestDestroyStopsTheSweep(t *testing.T) {
	running := goleak.IgnoreCurrent()
	var children countingChildren
	_, destroy, err := ProjectAwareDecorator(schema.GroupResource{Resource: "configmaps"}, cachesOf(&children, nil),
		everyProjectKnown(), nil, testDrain, time.Minute)(&storagebackend.ConfigForResource{}, "", nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("decorate configmaps: %v", err)
	}
	destroy()
	goleak.VerifyNone(t, running)
}

// forgetHeldWarm clears the mark that a readiness check's registration sets
// for the life of the process, so the next test starts without it.
func forgetHeldWarm(gr schema.GroupResource) {
	muxes.Lock()
	defer muxes.Unlock()
	delete(muxes.heldWarm, gr)
}

// get makes one get request into project through m.
func get(t *testing.T, m *projectMux, project string) {
	t.Helper()
	if err := m.Get(requestFor(project, "get"), "/k", storage.GetOptions{}, nil); err != nil {
		t.Fatalf("get in %s: %v", project, err)
	}
}

// removals reads the removal counter of m's kind for reason.
func removals(t *testing.T, m *projectMux, reason removalReason) float64 {
	t.Helper()
	got, err := testutil.GetCounterMetricValue(childRemovals.WithLabelValues(m.args.resourceGroup, m.args.resourceKind, string(reason)))
	if err != nil {
		t.Fatalf("read the removal counter: %v", err)
	}
	return got
}
