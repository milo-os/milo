package garbagecollector

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/discovery"
	"k8s.io/klog/v2"

	discoveryctx "go.miloapis.com/milo/pkg/server/discovery"
)

// ServedTypes tells which types a project plane serves. Milo's discovery-context registry implements it, so the
// collector reads the same declarations as a project's discovery (milo#834).
type ServedTypes interface {
	HasSynced() bool
	IsVisible(gr schema.GroupResource, current discoveryctx.ParentContext) bool
}

// SetServedTypes gives the collector the types each project serves. With narrow, every project's builder watches only
// the root's deletable types its project serves; without it, the monitor sets stay as they were and the collector
// only reports what a narrowing would drop. Call it before Run, Sync and the first AddProject.
func (gc *GarbageCollector) SetServedTypes(served ServedTypes, narrow bool) {
	gc.mu.Lock()
	defer gc.mu.Unlock()
	gc.served, gc.narrow = served, narrow
}

// narrowing reports whether each project watches only the types it serves.
func (gc *GarbageCollector) narrowing() bool {
	gc.mu.RLock()
	defer gc.mu.RUnlock()
	return gc.narrow
}

// narrowProjects gives every project's builder the root's deletable types its project serves. Sync calls it on every
// tick, so a project follows a declaration that changes with no change in discovery, and a project engaged while a
// resync ran catches up within a tick. A builder already at its set is left alone; while the registry has not synced,
// every builder keeps its set (F5).
func (gc *GarbageCollector) narrowProjects(logger klog.Logger, root map[schema.GroupVersionResource]struct{}) {
	if !gc.narrowing() {
		return
	}
	project, known := gc.projectResources(root)
	if !known {
		return
	}
	for _, gb := range gc.builders() {
		if gb.project == "" || gb.watchesExactly(project) {
			continue
		}
		if err := gb.syncMonitors(logger, project); err != nil {
			utilruntime.HandleError(fmt.Errorf("gc(%s): narrowing monitors: %w", gb.project, err))
		}
		gb.startMonitors(logger)
	}
}

// projectResources returns the monitor set of a project's builder for the root's deletable set. It returns false
// while narrowing is on and the registry has not synced: a project then keeps the set it has, because an unsynced
// registry would show every type and widen every project to the root (F5).
func (gc *GarbageCollector) projectResources(root map[schema.GroupVersionResource]struct{}) (
	map[schema.GroupVersionResource]struct{}, bool) {
	gc.mu.RLock()
	served, narrow := gc.served, gc.narrow
	gc.mu.RUnlock()
	if !narrow {
		return root, true
	}
	if !served.HasSynced() {
		return nil, false
	}
	project := make(map[schema.GroupVersionResource]struct{}, len(root))
	for r := range root {
		if served.IsVisible(r.GroupResource(), discoveryctx.ContextProject) {
			project[r] = struct{}{}
		}
	}
	return project, true
}

// projectSeed returns the monitor set of a project being engaged: the root's last deletable set as the project serves
// it once that is known, and otherwise the project's own discovery, as before narrowing.
func (gc *GarbageCollector) projectSeed(logger klog.Logger, own discovery.ServerResourcesInterface) map[schema.GroupVersionResource]struct{} {
	gc.mu.RLock()
	root, narrow := gc.rootResources, gc.narrow
	gc.mu.RUnlock()
	if narrow && root != nil {
		if project, known := gc.projectResources(root); known {
			return project
		}
	}
	seed, _ := GetDeletableResources(logger, own)
	return seed
}

// holdUnservedOwner returns an unservedOwnerError for an owner reference the collector must not read as gone: with
// narrowing on, one whose kind the dependent's project does not serve, and any while the registry has not synced
// (F4, F5). isDangling asks it first, before the absent-owner cache, because a virtual delete of an unwatched owner
// fills that cache. A kind the mapper cannot resolve is left to isDangling's own mapping error.
func (gc *GarbageCollector) holdUnservedOwner(reference metav1.OwnerReference, item *node) error {
	if item.identity.Project == "" || !gc.narrowing() {
		return nil
	}
	gb := gc.builderForProject(item.identity.Project)
	if gb == nil {
		return nil
	}
	resource, _, err := apiResourceUsing(gb.restMapper, reference.APIVersion, reference.Kind)
	if err != nil {
		return nil
	}
	gc.mu.RLock()
	served := gc.served
	gc.mu.RUnlock()
	if served.HasSynced() && served.IsVisible(resource.GroupResource(), discoveryctx.ContextProject) {
		return nil
	}
	return &unservedOwnerError{owner: resource.GroupResource()}
}
