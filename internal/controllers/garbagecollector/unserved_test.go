package garbagecollector

import (
	"fmt"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	"k8s.io/component-base/metrics/legacyregistry"
	"k8s.io/klog/v2"
	"k8s.io/klog/v2/ktesting"
)

// syncedController is a monitor's controller that has synced, so a builder holding it reports IsSynced.
type syncedController struct {
	cache.Controller
	synced bool
}

func (c syncedController) HasSynced() bool { return c.synced }

// syncMonitorsOf gives a builder one monitor per resource, each synced or not.
func syncMonitorsOf(gb *GraphBuilder, synced bool, resources ...schema.GroupVersionResource) {
	gb.monitorLock.Lock()
	defer gb.monitorLock.Unlock()
	gb.monitors = monitors{}
	for _, r := range resources {
		gb.monitors[r] = &monitor{controller: syncedController{synced: synced}}
	}
}

// putNode adds an object of a test resource to a builder's graph, as a dependent of its owners.
func putNode(gb *GraphBuilder, r schema.GroupVersionResource, name string, owners ...*node) *node {
	n := &node{
		identity: objectReference{
			OwnerReference: metav1.OwnerReference{APIVersion: r.GroupVersion().String(), Kind: kindOf(r), Name: name,
				UID: types.UID(gb.project + "/" + name)},
			Namespace: "ns1", Project: gb.project,
		},
		dependents: map[*node]struct{}{},
	}
	for _, o := range owners {
		n.owners = append(n.owners, o.identity.OwnerReference)
		o.addDependent(n)
	}
	gb.uidToNode.Write(n)
	return n
}

// unservedSeries returns every series of an unserved gauge, keyed "group/resource".
func unservedSeries(t *testing.T, name string) map[string]float64 {
	t.Helper()
	families, err := legacyregistry.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]float64{}
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, m := range f.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			out[labels["group"]+"/"+labels["resource"]] = m.GetGauge().GetValue()
		}
	}
	return out
}

func assertSeries(t *testing.T, name string, want map[string]float64) {
	t.Helper()
	got := unservedSeries(t, name)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("%s: got %v, want %v", name, got, want)
	}
}

// newUnservedGC builds a collector whose project p serves widgets only, with every graph synced.
func newUnservedGC(t *testing.T) (*GarbageCollector, *fakeServed) {
	t.Helper()
	registerUnservedMetrics()
	resetUnserved()
	served := newFakeServed(true, gizmos, sprites)
	gc, _ := newHoldGC(served, false)
	for _, gb := range gc.builders() {
		syncMonitorsOf(gb, true, widgets, gizmos, sprites)
	}
	return gc, served
}

var testRoot = map[schema.GroupVersionResource]struct{}{widgets: {}, gizmos: {}, sprites: {}}

// TestUnservedGaugesCountWhatANarrowingWouldDrop counts, per type a project does not serve, its observed objects
// (all), those with an owner (F1) and those with a dependent (F2). A served object, a virtual node and the root's
// own objects are never counted (S3), also when a project plane shows one of them (Milo serves the root's CRDs and
// APIServices in every project, and the root's graph keeps watching them), and a hidden type with no objects reads 0,
// not absent.
func TestUnservedGaugesCountWhatANarrowingWouldDrop(t *testing.T) {
	gc, _ := newUnservedGC(t)
	root, p := gc.builderForProject(""), gc.builderForProject("p")

	owner := putNode(p, widgets, "owner")
	putNode(p, gizmos, "owned", owner)     // F1
	parent := putNode(p, gizmos, "parent") // F2
	putNode(p, widgets, "child", parent)   // a served dependent of a hidden owner
	putNode(p, gizmos, "alone")            // neither
	putNode(p, widgets, "served")          // S3: served, never counted
	absent := &node{identity: objectReference{OwnerReference: metav1.OwnerReference{APIVersion: "example.com/v1",
		Kind: "Gizmo", Name: "gone", UID: "p/gone"}, Namespace: "ns1", Project: "p"}, virtual: true,
		dependents: map[*node]struct{}{}}
	p.uidToNode.Write(absent)
	putNode(p, widgets, "orphan", absent)     // its hidden owner is virtual: not an object
	putNode(root, gizmos, "root-gizmo")       // the root serves every type
	shared := putNode(root, gizmos, "shared") // the root's own object, which the project plane also shows
	p.uidToNode.Write(&node{identity: shared.identity, dependents: map[*node]struct{}{}})

	gc.reportUnserved(klog.Background(), testRoot)

	assertSeries(t, "garbagecollector_unserved_objects", map[string]float64{"example.com/gizmos": 3, "example.com/sprites": 0})
	assertSeries(t, "garbagecollector_unserved_owned_objects", map[string]float64{"example.com/gizmos": 1, "example.com/sprites": 0})
	assertSeries(t, "garbagecollector_unserved_owners_with_dependents", map[string]float64{"example.com/gizmos": 1, "example.com/sprites": 0})
}

// TestUnservedGaugesReportNothingWhenBlind drops every series where the collector cannot see what a narrowing would
// drop (S2): the registry unsynced, a project graph unsynced, or narrowing already on.
func TestUnservedGaugesReportNothingWhenBlind(t *testing.T) {
	blind := map[string]func(gc *GarbageCollector, served *fakeServed){
		"registry unsynced": func(_ *GarbageCollector, served *fakeServed) {
			served.mu.Lock()
			served.synced = false
			served.mu.Unlock()
		},
		"a project graph unsynced": func(gc *GarbageCollector, _ *fakeServed) {
			syncMonitorsOf(gc.builderForProject("p"), false, widgets, gizmos, sprites)
		},
		"narrowing on": func(gc *GarbageCollector, served *fakeServed) { gc.SetServedTypes(served, true) },
	}
	for name, makeBlind := range blind {
		t.Run(name, func(t *testing.T) {
			gc, served := newUnservedGC(t)
			putNode(gc.builderForProject("p"), gizmos, "alone")
			gc.reportUnserved(klog.Background(), testRoot)
			assertSeries(t, "garbagecollector_unserved_objects", map[string]float64{"example.com/gizmos": 1, "example.com/sprites": 0})

			makeBlind(gc, served)
			gc.reportUnserved(klog.Background(), testRoot)
			for _, name := range []string{"garbagecollector_unserved_objects", "garbagecollector_unserved_owned_objects",
				"garbagecollector_unserved_owners_with_dependents"} {
				assertSeries(t, name, map[string]float64{})
			}
		})
	}
}

// TestUnservedObjectsLoggedOnce logs each counted object once, with its project, kind, name and the kinds on the
// other end of its references, and again only after it left the graph and came back (S7).
func TestUnservedObjectsLoggedOnce(t *testing.T) {
	gc, _ := newUnservedGC(t)
	p := gc.builderForProject("p")
	owner := putNode(p, widgets, "owner")
	owned := putNode(p, gizmos, "owned", owner)

	logger := ktesting.NewLogger(t, ktesting.NewConfig(ktesting.BufferLogs(true)))
	buffer := logger.GetSink().(ktesting.Underlier).GetBuffer()
	lines := func() int { return strings.Count(buffer.String(), "Object of a type its project does not serve") }

	gc.reportUnserved(logger, testRoot)
	gc.reportUnserved(logger, testRoot)
	if got := lines(); got != 1 {
		t.Fatalf("logged %d times, want once:\n%s", got, buffer.String())
	}
	for _, want := range []string{`project="p"`, `resource="gizmos"`, `name="owned"`, `ownerKinds=["Widget"]`} {
		if !strings.Contains(buffer.String(), want) {
			t.Errorf("log line lacks %s:\n%s", want, buffer.String())
		}
	}

	p.removeNode(owned)
	gc.reportUnserved(logger, testRoot)
	p.uidToNode.Write(owned)
	owner.addDependent(owned)
	gc.reportUnserved(logger, testRoot)
	if got := lines(); got != 2 {
		t.Fatalf("logged %d times, want twice: once, and once after it came back", got)
	}
}

// TestUnservedGaugesNeverReadZeroForATypeAProjectDoesNotWatch engages one project before Sync's first pass, which
// widens it to the root's set, and one after, whose graph keeps its own discovery's set (S1), as a project does on an
// estate whose discovery filter hides the type. The late project's objects of that type are never in its graph, so a
// gauge for the type would read 0 while blind to them: a series may exist only for a type every project graph watches.
func TestUnservedGaugesNeverReadZeroForATypeAProjectDoesNotWatch(t *testing.T) {
	registerUnservedMetrics()
	resetUnserved()
	g := newTestGC(t, widgets, gizmos)
	g.SetServedTypes(newFakeServed(true, gizmos), false)
	g.addProject(t, "early", widgets)
	g.runSync(testPeriod)
	eventuallyMonitors(t, g.builderForProject("early"), "widgets", "gizmos")

	g.addProject(t, "late", widgets)
	g.waitTicks(t, 3)
	eventuallyMonitors(t, g.builderForProject("late"), "widgets")
	for _, name := range []string{"garbagecollector_unserved_objects", "garbagecollector_unserved_owned_objects",
		"garbagecollector_unserved_owners_with_dependents"} {
		if value, ok := unservedSeries(t, name)["example.com/gizmos"]; ok {
			t.Errorf("%s reads %v for gizmos, which project late does not watch", name, value)
		}
	}
}
