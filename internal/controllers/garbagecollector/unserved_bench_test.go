package garbagecollector

import (
	"fmt"
	"testing"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
)

// productionProjects is production's project count when the walk was sized (milo#834, 2026-10-26).
const productionProjects = 652

// BenchmarkReportUnserved measures one tick of the shadow's gauges over production's project count, at three graph
// sizes per project. A tenth of the objects are of a hidden type, and every other object has an owner.
func BenchmarkReportUnserved(b *testing.B) {
	for _, perProject := range []int{30, 200, 1000} {
		b.Run(fmt.Sprintf("nodes-per-project-%d", perProject), func(b *testing.B) {
			gc := benchGC(perProject)
			logger := klog.Background().V(10) // the once-per-object line is not the subject here
			b.ResetTimer()
			for b.Loop() {
				gc.reportUnserved(logger, testRoot)
			}
		})
	}
}

// BenchmarkGraphSnapshot measures how long one project's walk holds the lock its graph builder writes under.
func BenchmarkGraphSnapshot(b *testing.B) {
	for _, perProject := range []int{30, 200, 1000} {
		b.Run(fmt.Sprintf("nodes-per-project-%d", perProject), func(b *testing.B) {
			gb := benchGC(perProject).builderForProject("p0")
			for b.Loop() {
				gb.uidToNode.nodes()
			}
		})
	}
}

func benchGC(perProject int) *GarbageCollector {
	registerUnservedMetrics()
	gc, _ := newHoldGC(newFakeServed(true, gizmos, sprites), false)
	builders := []*GraphBuilder{gc.builders()[0]}
	for p := range productionProjects {
		gb := &GraphBuilder{project: fmt.Sprintf("p%d", p), restMapper: gc.restMapper,
			uidToNode: &concurrentUIDToNode{uidToNode: map[types.UID]*node{}}}
		syncMonitorsOf(gb, true, widgets, gizmos, sprites)
		var owner *node
		for i := range perProject {
			kind := widgets
			if i%10 == 0 {
				kind = gizmos
			}
			if i%2 == 0 {
				owner = putNode(gb, kind, fmt.Sprintf("o%d", i))
				continue
			}
			putNode(gb, kind, fmt.Sprintf("o%d", i), owner)
		}
		builders = append(builders, gb)
	}
	gc.dependencyGraphBuilders = builders
	return gc
}
