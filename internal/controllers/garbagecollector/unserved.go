package garbagecollector

import (
	"sync"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"
	"k8s.io/klog/v2"

	discoveryctx "go.miloapis.com/milo/pkg/server/discovery"
)

// The shadow of milo#834's narrowing (DESIGN §7): before any project stops watching the types it does not serve,
// these gauges count what that would drop from the project graphs. The narrowing turns on in an estate only when all
// three read 0 for every such type.
var (
	unservedObjects = metrics.NewGaugeVec(&metrics.GaugeOpts{
		Subsystem:      "garbagecollector",
		Name:           "unserved_objects",
		Help:           "Objects in project graphs whose type the project does not serve. Absent while the collector cannot see them.",
		StabilityLevel: metrics.ALPHA,
	}, []string{"group", "resource"})
	unservedOwnedObjects = metrics.NewGaugeVec(&metrics.GaugeOpts{
		Subsystem:      "garbagecollector",
		Name:           "unserved_owned_objects",
		Help:           "Objects in project graphs whose type the project does not serve and that have an owner. Absent while the collector cannot see them.",
		StabilityLevel: metrics.ALPHA,
	}, []string{"group", "resource"})
	unservedOwnersWithDependents = metrics.NewGaugeVec(&metrics.GaugeOpts{
		Subsystem:      "garbagecollector",
		Name:           "unserved_owners_with_dependents",
		Help:           "Objects in project graphs whose type the project does not serve and that have a dependent. Absent while the collector cannot see them.",
		StabilityLevel: metrics.ALPHA,
	}, []string{"group", "resource"})

	registerUnservedOnce sync.Once
)

func registerUnservedMetrics() {
	registerUnservedOnce.Do(func() {
		legacyregistry.MustRegister(unservedObjects, unservedOwnedObjects, unservedOwnersWithDependents)
	})
}

func resetUnserved() {
	unservedObjects.Reset()
	unservedOwnedObjects.Reset()
	unservedOwnersWithDependents.Reset()
}

// unservedObject is one observed object of a type its project does not serve, with the kinds at the other end of its
// references.
type unservedObject struct {
	project        string
	resource       schema.GroupResource
	node           *node
	ownerKinds     []string
	dependentKinds []string
}

// reportUnserved sets the three gauges from the project graphs and logs each counted object once. It reports no
// series while it cannot see (S2). Sync calls it on every tick, from its own goroutine only.
func (gc *GarbageCollector) reportUnserved(logger klog.Logger, root map[schema.GroupVersionResource]struct{}) {
	hidden, ok := gc.unservedKinds(logger, root)
	if !ok {
		resetUnserved()
		gc.reportedUnserved = nil
		return
	}
	objects := gc.unservedObjects(hidden)
	gc.setUnserved(hidden, objects)
	gc.logUnserved(logger, objects)
}

// unservedKinds maps each kind the root's builder watches and a project does not serve to its resource. It reports
// false while the collector cannot see what a narrowing would drop: narrowing already on, the registry unsynced, or a
// project graph unsynced, which would read too low. It leaves out a type some project graph does not watch, so that
// type's series is absent, not 0.
func (gc *GarbageCollector) unservedKinds(logger klog.Logger, root map[schema.GroupVersionResource]struct{}) (
	map[schema.GroupKind]schema.GroupResource, bool) {
	gc.mu.RLock()
	served, narrow := gc.served, gc.narrow
	gc.mu.RUnlock()
	if served == nil || narrow || !served.HasSynced() || !gc.projectsSynced(logger) {
		return nil, false
	}
	ignored := gc.builders()[0].ignoredResources
	hidden := map[schema.GroupKind]schema.GroupResource{}
	for r := range root {
		gr := r.GroupResource()
		if _, skip := ignored[gr]; skip || served.IsVisible(gr, discoveryctx.ContextProject) || !gc.projectsWatch(r) {
			continue
		}
		kind, err := gc.restMapper.KindFor(r)
		if err != nil {
			logger.V(2).Info("Not counting a type the REST mapper cannot name", "resource", r, "err", err)
			continue
		}
		hidden[kind.GroupKind()] = gr
	}
	return hidden, true
}

// projectsWatch reports whether every project graph has a synced monitor for the resource. A project engaged after
// Sync's first pass keeps its own discovery's set (S1), so where the discovery filter hides a type, no such project
// graph holds that type's objects.
func (gc *GarbageCollector) projectsWatch(r schema.GroupVersionResource) bool {
	for _, gb := range gc.builders() {
		if gb.project != "" && !gb.IsResourceSynced(r) {
			return false
		}
	}
	return true
}

func (gc *GarbageCollector) projectsSynced(logger klog.Logger) bool {
	for _, gb := range gc.builders() {
		if gb.project != "" && !gb.IsSynced(logger) {
			return false
		}
	}
	return true
}

// unservedObjects walks every project graph once. An object is counted from its own node when its kind is hidden, and
// it is owned when it appears among another node's dependents: the dependents carry their own lock, while a node's
// owners are written without one. An object the root's graph also holds is the root's own, which a project plane
// shows (Milo serves the root's CRDs and APIServices in every project): the root keeps watching it, so narrowing
// drops nothing of it, and it is not counted.
func (gc *GarbageCollector) unservedObjects(hidden map[schema.GroupKind]schema.GroupResource) map[types.UID]*unservedObject {
	objects := map[types.UID]*unservedObject{}
	root := gc.builderForProject("")
	for _, gb := range gc.builders() {
		if gb.project == "" {
			continue
		}
		for _, n := range gb.uidToNode.nodes() {
			dependents := n.getDependents()
			if o := unservedOf(objects, hidden, root, n); o != nil {
				o.dependentKinds = kindsOf(dependents)
			}
			for _, d := range dependents {
				if o := unservedOf(objects, hidden, root, d); o != nil {
					o.ownerKinds = append(o.ownerKinds, n.identity.Kind)
				}
			}
		}
	}
	return objects
}

// unservedOf returns the entry of an observed node whose kind is hidden and that only its project holds, creating it,
// or nil for any other node.
func unservedOf(objects map[types.UID]*unservedObject, hidden map[schema.GroupKind]schema.GroupResource,
	root *GraphBuilder, n *node) *unservedObject {
	gr, ok := hidden[schema.FromAPIVersionAndKind(n.identity.APIVersion, n.identity.Kind).GroupKind()]
	if !ok || !n.isObserved() || root.holds(n.identity.UID) {
		return nil
	}
	if o, seen := objects[n.identity.UID]; seen {
		return o
	}
	o := &unservedObject{project: n.identity.Project, resource: gr, node: n}
	objects[n.identity.UID] = o
	return o
}

func kindsOf(nodes []*node) []string {
	var kinds []string
	for _, n := range nodes {
		kinds = append(kinds, n.identity.Kind)
	}
	return kinds
}

// setUnserved writes one series per hidden type, 0 included, so a type with nothing to drop reads 0 and not absent. It
// deletes only the series of types no longer hidden, so a scrape never sees a hidden type absent between two writes.
func (gc *GarbageCollector) setUnserved(hidden map[schema.GroupKind]schema.GroupResource, objects map[types.UID]*unservedObject) {
	type count struct{ objects, owned, ownersWithDependents float64 }
	counts := map[schema.GroupResource]*count{}
	for _, gr := range hidden {
		counts[gr] = &count{}
	}
	for _, o := range objects {
		c := counts[o.resource]
		c.objects++
		if len(o.ownerKinds) > 0 {
			c.owned++
		}
		if len(o.dependentKinds) > 0 {
			c.ownersWithDependents++
		}
	}
	for gr := range gc.reportedUnserved {
		if _, still := counts[gr]; !still {
			for _, gauge := range []*metrics.GaugeVec{unservedObjects, unservedOwnedObjects, unservedOwnersWithDependents} {
				gauge.DeleteLabelValues(gr.Group, gr.Resource)
			}
		}
	}
	gc.reportedUnserved = map[schema.GroupResource]struct{}{}
	for gr, c := range counts {
		unservedObjects.WithLabelValues(gr.Group, gr.Resource).Set(c.objects)
		unservedOwnedObjects.WithLabelValues(gr.Group, gr.Resource).Set(c.owned)
		unservedOwnersWithDependents.WithLabelValues(gr.Group, gr.Resource).Set(c.ownersWithDependents)
		gc.reportedUnserved[gr] = struct{}{}
	}
}

// logUnserved logs each counted object the first time it is counted. An object that leaves the graph is forgotten, so
// it is logged again if it comes back. A tick that cannot see forgets nothing.
func (gc *GarbageCollector) logUnserved(logger klog.Logger, objects map[types.UID]*unservedObject) {
	logged := make(map[types.UID]struct{}, len(objects))
	for uid, o := range objects {
		logged[uid] = struct{}{}
		if _, done := gc.loggedUnserved[uid]; done {
			continue
		}
		logger.Info("Object of a type its project does not serve", "project", o.project, "group", o.resource.Group,
			"resource", o.resource.Resource, "namespace", o.node.identity.Namespace, "name", o.node.identity.Name,
			"ownerKinds", o.ownerKinds, "dependentKinds", o.dependentKinds)
	}
	gc.loggedUnserved = logged
}

// holds reports whether the builder's graph has a node for the uid; a nil builder holds nothing.
func (gb *GraphBuilder) holds(uid types.UID) bool {
	if gb == nil {
		return false
	}
	_, ok := gb.uidToNode.Read(uid)
	return ok
}
