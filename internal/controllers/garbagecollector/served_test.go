package garbagecollector

import (
	"context"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"

	discoveryctx "go.miloapis.com/milo/pkg/server/discovery"
)

// fakeServed stands in for the discovery-context registry: every type is served in a project except the hidden ones.
type fakeServed struct {
	mu     sync.Mutex
	synced bool
	hidden map[schema.GroupResource]bool
}

func newFakeServed(synced bool, hidden ...schema.GroupVersionResource) *fakeServed {
	f := &fakeServed{synced: synced, hidden: map[schema.GroupResource]bool{}}
	f.hide(hidden...)
	return f
}

func (f *fakeServed) HasSynced() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.synced
}

func (f *fakeServed) IsVisible(gr schema.GroupResource, current discoveryctx.ParentContext) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return current != discoveryctx.ContextProject || !f.hidden[gr]
}

func (f *fakeServed) sync() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.synced = true
}

func (f *fakeServed) hide(resources ...schema.GroupVersionResource) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range resources {
		f.hidden[r.GroupResource()] = true
	}
}

// runSync starts the collector's Run and Sync, as the controller manager does.
func (g *testGC) runSync(period time.Duration) {
	go g.Run(g.ctx, 1, period)
	go g.Sync(g.ctx, g.discovery, period)
}

// waitTicks waits until Sync has read the root's discovery n more times.
func (g *testGC) waitTicks(t *testing.T, n int) {
	t.Helper()
	start := g.discovery.usedCount()
	if err := wait.PollUntilContextTimeout(g.ctx, 10*time.Millisecond, wait.ForeverTestTimeout, true,
		func(context.Context) (bool, error) { return g.discovery.usedCount() >= start+n, nil }); err != nil {
		t.Fatalf("Sync did not tick %d times", n)
	}
}

const testPeriod = 50 * time.Millisecond

// TestProjectMonitorsWithNarrowingOff pins main's monitor sets while the feature gate is off (S1): a project engaged
// before Sync's first pass is widened to the root set, and one engaged after it keeps its own discovery's set.
func TestProjectMonitorsWithNarrowingOff(t *testing.T) {
	g := newTestGC(t, widgets, gizmos)
	g.SetServedTypes(newFakeServed(true, gizmos), false)
	g.addProject(t, "early", widgets)
	g.runSync(testPeriod)

	eventuallyMonitors(t, g.builderForProject(""), "widgets", "gizmos")
	eventuallyMonitors(t, g.builderForProject("early"), "widgets", "gizmos")

	g.addProject(t, "late", widgets)
	g.waitTicks(t, 3)
	eventuallyMonitors(t, g.builderForProject("late"), "widgets")
}

// TestProjectMonitorsFollowServedTypes turns narrowing on (milo#834): every project watches the root's deletable set
// as the project serves it, whatever its own discovery shows, and the root keeps the whole set.
func TestProjectMonitorsFollowServedTypes(t *testing.T) {
	g := newTestGC(t, widgets, gizmos)
	served := newFakeServed(true, gizmos)
	g.SetServedTypes(served, true)
	g.addProject(t, "early", widgets, gizmos)
	g.runSync(testPeriod)

	eventuallyMonitors(t, g.builderForProject(""), "widgets", "gizmos")
	eventuallyMonitors(t, g.builderForProject("early"), "widgets")

	g.addProject(t, "late", widgets, gizmos)
	eventuallyMonitors(t, g.builderForProject("late"), "widgets")

	g.discovery.setPreferredResources(testResources(widgets, gizmos, sprites), nil)
	eventuallyMonitors(t, g.builderForProject(""), "widgets", "gizmos", "sprites")
	eventuallyMonitors(t, g.builderForProject("early"), "widgets", "sprites")
	eventuallyMonitors(t, g.builderForProject("late"), "widgets", "sprites")

	// A declaration narrows a type with no change in the root's discovery (S5).
	served.hide(sprites)
	eventuallyMonitors(t, g.builderForProject("early"), "widgets")
	eventuallyMonitors(t, g.builderForProject("late"), "widgets")
	eventuallyMonitors(t, g.builderForProject(""), "widgets", "gizmos", "sprites")
}

// TestUnsyncedRegistryNeverWidensAProject holds every project at its set until the registry has synced (F5): Sync
// gives the root the whole set and leaves projects alone, and a new project seeds from its own discovery, as on
// main. Once the registry syncs, the next tick narrows with no change in the root's discovery (S5).
func TestUnsyncedRegistryNeverWidensAProject(t *testing.T) {
	g := newTestGC(t, widgets, gizmos, sprites)
	served := newFakeServed(false, gizmos)
	g.SetServedTypes(served, true)
	g.addProject(t, "early", widgets)
	g.runSync(testPeriod)

	eventuallyMonitors(t, g.builderForProject(""), "widgets", "gizmos", "sprites")
	g.addProject(t, "late", widgets, gizmos)
	g.waitTicks(t, 3)
	eventuallyMonitors(t, g.builderForProject("early"), "widgets")
	eventuallyMonitors(t, g.builderForProject("late"), "widgets", "gizmos")

	served.sync()
	eventuallyMonitors(t, g.builderForProject("early"), "widgets", "sprites")
	eventuallyMonitors(t, g.builderForProject("late"), "widgets", "sprites")
}
