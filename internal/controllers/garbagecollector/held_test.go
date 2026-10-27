package garbagecollector

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
)

// putVirtual adds an owner that no monitor of the builder observes, as addDependentToOwners does for an owner of a type
// the project does not watch.
func putVirtual(gb *GraphBuilder, r schema.GroupVersionResource, name string) *node {
	n := &node{
		identity: objectReference{
			OwnerReference: metav1.OwnerReference{APIVersion: r.GroupVersion().String(), Kind: kindOf(r), Name: name,
				UID: types.UID(gb.project + "/" + name)},
			Namespace: "ns1", Project: gb.project,
		},
		virtual:    true,
		dependents: map[*node]struct{}{},
	}
	gb.uidToNode.Write(n)
	return n
}

// newHeldGC builds a collector with narrowing on whose project p serves widgets only, each graph watching that set
// and synced, as narrowing leaves them.
func newHeldGC(t *testing.T, synced bool) *GarbageCollector {
	t.Helper()
	registerUnservedMetrics()
	resetUnserved()
	heldDependents.Reset()
	gc, _ := newHoldGC(newFakeServed(synced, gizmos, sprites), true)
	for _, gb := range gc.builders() {
		syncMonitorsOf(gb, true, widgets)
	}
	return gc
}

// TestHeldDependentsCountWhatNarrowingWouldHold counts, per owner type a project does not serve, the observed objects
// with such an owner (H2): the objects the collector would hold if it processed them. A dependent with two such owners
// of one type counts once (H4); a dependent of a served owner (H1) and the root's graph (H5) are not counted; a hidden
// type with nothing held reads 0, and the shadow gauges are absent while narrowing is on.
func TestHeldDependentsCountWhatNarrowingWouldHold(t *testing.T) {
	gc := newHeldGC(t, true)
	root, p := gc.builderForProject(""), gc.builderForProject("p")

	g1, g2 := putVirtual(p, gizmos, "g1"), putVirtual(p, gizmos, "g2")
	putNode(p, widgets, "held", g1)       // H2
	putNode(p, widgets, "twice", g1, g2)  // H4
	owner := putNode(p, widgets, "owner") // served
	putNode(p, widgets, "free", owner)    // H1
	putVirtual(p, sprites, "lonely")      // a hidden owner with no dependent
	rootOwner := putVirtual(root, gizmos, "root-gizmo")
	putNode(root, widgets, "root-dependent", rootOwner) // H5

	gc.reportUnserved(klog.Background(), testRoot)

	assertSeries(t, "garbagecollector_held_dependents", map[string]float64{"example.com/gizmos": 2, "example.com/sprites": 0})
	for _, name := range []string{"garbagecollector_unserved_objects", "garbagecollector_unserved_owned_objects",
		"garbagecollector_unserved_owners_with_dependents"} {
		assertSeries(t, name, map[string]float64{})
	}
}

// TestHeldDependentsAbsentWhileBlindOrNarrowingOff (H3): no series while the registry or a project graph has not
// synced, or while narrowing is off, when the shadow gauges report instead.
func TestHeldDependentsAbsentWhileBlindOrNarrowingOff(t *testing.T) {
	setUp := func(gc *GarbageCollector) {
		p := gc.builderForProject("p")
		putNode(p, widgets, "held", putVirtual(p, gizmos, "g1"))
	}
	t.Run("registry unsynced", func(t *testing.T) {
		gc := newHeldGC(t, false)
		setUp(gc)
		gc.reportUnserved(klog.Background(), testRoot)
		assertSeries(t, "garbagecollector_held_dependents", map[string]float64{})
	})
	t.Run("a project graph unsynced", func(t *testing.T) {
		gc := newHeldGC(t, true)
		syncMonitorsOf(gc.builderForProject("p"), false, widgets)
		setUp(gc)
		gc.reportUnserved(klog.Background(), testRoot)
		assertSeries(t, "garbagecollector_held_dependents", map[string]float64{})
	})
	t.Run("narrowing off", func(t *testing.T) {
		gc, _ := newUnservedGC(t)
		heldDependents.Reset()
		setUp(gc)
		gc.reportUnserved(klog.Background(), testRoot)
		assertSeries(t, "garbagecollector_held_dependents", map[string]float64{})
		assertSeries(t, "garbagecollector_unserved_objects", map[string]float64{"example.com/gizmos": 0, "example.com/sprites": 0})
	})
	t.Run("narrowing turned off after a report", func(t *testing.T) {
		gc := newHeldGC(t, true)
		setUp(gc)
		gc.reportUnserved(klog.Background(), testRoot)
		assertSeries(t, "garbagecollector_held_dependents", map[string]float64{"example.com/gizmos": 1, "example.com/sprites": 0})
		gc.SetServedTypes(newFakeServed(true, gizmos, sprites), false)
		syncMonitorsOf(gc.builderForProject("p"), true, widgets, gizmos, sprites)
		gc.reportUnserved(klog.Background(), testRoot)
		assertSeries(t, "garbagecollector_held_dependents", map[string]float64{})
	})
}

// TestGaugesDeleteATypeNoLongerHidden: a type a project comes to serve loses its series, and the other types keep
// theirs, so a scrape never sees a still-hidden type absent between two writes.
func TestGaugesDeleteATypeNoLongerHidden(t *testing.T) {
	gc, _ := newUnservedGC(t)
	gc.reportUnserved(klog.Background(), testRoot)
	assertSeries(t, "garbagecollector_unserved_objects", map[string]float64{"example.com/gizmos": 0, "example.com/sprites": 0})

	gc.SetServedTypes(newFakeServed(true, gizmos), false)
	gc.reportUnserved(klog.Background(), testRoot)
	assertSeries(t, "garbagecollector_unserved_objects", map[string]float64{"example.com/gizmos": 0})
}
