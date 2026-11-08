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
	// heldDependents is the narrowing's own reading (milo#834): F2, unservedOwnersWithDependents, reads 0 at turn-on,
	// so this starts at 0, and a rise is a new dependent of an owner whose type its project does not serve.
	heldDependents = metrics.NewGaugeVec(&metrics.GaugeOpts{
		Subsystem: "garbagecollector",
		Name:      "held_dependents",
		Help: "Objects in project graphs that the collector would hold if it processed them, by their owner's type, " +
			"which their project does not serve; not the holds it has met. Absent while narrowing is off or the " +
			"collector cannot see them.",
		StabilityLevel: metrics.ALPHA,
	}, []string{"group", "resource"})
	// dependentHolds counts the holds the collector met, beside heldDependents' level of the holds it would meet. A held
	// item comes back with the queue's backoff, so it counts attempts, not objects: read it as a rate.
	dependentHolds = metrics.NewCounterVec(&metrics.CounterOpts{
		Subsystem: "garbagecollector",
		Name:      "dependent_holds_total",
		Help: "Times the collector held a dependent instead of processing it, by its owner's type, which the " +
			"dependent's project does not serve. A held dependent is retried with backoff, so this counts attempts.",
		StabilityLevel: metrics.ALPHA,
	}, []string{"group", "resource"})

	registerUnservedOnce sync.Once
)

func registerUnservedMetrics() {
	registerUnservedOnce.Do(func() {
		legacyregistry.MustRegister(unservedObjects, unservedOwnedObjects, unservedOwnersWithDependents, heldDependents,
			dependentHolds)
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

// reportUnserved sets, from the project graphs, the three shadow gauges while narrowing is off and the held gauge
// while it is on, and logs each object the shadow counts once. It reports no series while it cannot see (S2). Sync
// calls it on every tick, from its own goroutine only.
func (gc *GarbageCollector) reportUnserved(logger klog.Logger, root map[schema.GroupVersionResource]struct{}) {
	hidden, narrow, ok := gc.unservedKinds(logger, root)
	if !ok || narrow {
		resetUnserved()
		gc.reportedUnserved = nil
	}
	if !ok || !narrow {
		heldDependents.Reset()
		gc.reportedHeld = nil
	}
	if !ok {
		return
	}
	if narrow {
		gc.reportedHeld = writeSeries(heldDependents, gc.reportedHeld, gc.countHeld(hidden))
		return
	}
	objects := gc.unservedObjects(hidden)
	gc.setUnserved(hidden, objects)
	gc.logUnserved(logger, objects)
}

// unservedKinds maps each kind the root's builder watches and a project does not serve to its resource, and says
// whether narrowing is on. It reports false while the collector cannot see: the registry unsynced, or a project graph
// unsynced, which would read too low. With narrowing off it leaves out a type some project graph does not watch, so
// that type's shadow series is absent, not 0; with it on no project graph watches these types, and the held gauge
// counts their served dependents.
func (gc *GarbageCollector) unservedKinds(logger klog.Logger, root map[schema.GroupVersionResource]struct{}) (
	map[schema.GroupKind]schema.GroupResource, bool, bool) {
	gc.mu.RLock()
	served, narrow := gc.served, gc.narrow
	gc.mu.RUnlock()
	if served == nil || !served.HasSynced() || !gc.projectsSynced(logger) {
		return nil, narrow, false
	}
	ignored := gc.builders()[0].ignoredResources
	hidden := map[schema.GroupKind]schema.GroupResource{}
	for r := range root {
		gr := r.GroupResource()
		if _, skip := ignored[gr]; skip || served.IsVisible(gr, discoveryctx.ContextProject) ||
			(!narrow && !gc.projectsWatch(r)) {
			continue
		}
		kind, err := gc.restMapper.KindFor(r)
		if err != nil {
			logger.V(2).Info("Not counting a type the REST mapper cannot name", "resource", r, "err", err)
			continue
		}
		hidden[kind.GroupKind()] = gr
	}
	return hidden, narrow, true
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

// setUnserved writes the three shadow gauges, one series per hidden type, 0 included.
func (gc *GarbageCollector) setUnserved(hidden map[schema.GroupKind]schema.GroupResource, objects map[types.UID]*unservedObject) {
	all, owned, withDependents := zeroCounts(hidden), zeroCounts(hidden), zeroCounts(hidden)
	for _, o := range objects {
		all[o.resource]++
		if len(o.ownerKinds) > 0 {
			owned[o.resource]++
		}
		if len(o.dependentKinds) > 0 {
			withDependents[o.resource]++
		}
	}
	previous := gc.reportedUnserved
	writeSeries(unservedObjects, previous, all)
	writeSeries(unservedOwnedObjects, previous, owned)
	gc.reportedUnserved = writeSeries(unservedOwnersWithDependents, previous, withDependents)
}

// countHeld counts, per owner type a project does not serve, the observed project-graph objects with an owner of that
// type: with narrowing on, the objects the collector would hold if it processed them (holdUnservedOwner), not the
// holds it has met. Such an owner is mostly a virtual node, since its project's graph no longer watches its type. A
// dependent counts once per owner type.
func (gc *GarbageCollector) countHeld(hidden map[schema.GroupKind]schema.GroupResource) map[schema.GroupResource]float64 {
	held := map[schema.GroupResource]map[types.UID]struct{}{}
	for _, gb := range gc.builders() {
		if gb.project == "" {
			continue
		}
		for _, n := range gb.uidToNode.nodes() {
			if gr, ok := hidden[schema.FromAPIVersionAndKind(n.identity.APIVersion, n.identity.Kind).GroupKind()]; ok {
				addObserved(held, gr, n.getDependents())
			}
		}
	}
	counts := zeroCounts(hidden)
	for gr, uids := range held {
		counts[gr] = float64(len(uids))
	}
	return counts
}

// addObserved adds to held[gr] the UIDs of the observed nodes among dependents.
func addObserved(held map[schema.GroupResource]map[types.UID]struct{}, gr schema.GroupResource, dependents []*node) {
	for _, d := range dependents {
		if !d.isObserved() {
			continue
		}
		if held[gr] == nil {
			held[gr] = map[types.UID]struct{}{}
		}
		held[gr][d.identity.UID] = struct{}{}
	}
}

// zeroCounts returns a count of 0 for each hidden type, so a type with nothing to count reads 0, not absent.
func zeroCounts(hidden map[schema.GroupKind]schema.GroupResource) map[schema.GroupResource]float64 {
	counts := make(map[schema.GroupResource]float64, len(hidden))
	for _, gr := range hidden {
		counts[gr] = 0
	}
	return counts
}

// writeSeries sets one series per type in counts and deletes the series of a type it wrote before and not now, so a
// scrape never sees a hidden type absent between two writes. It returns the types it wrote.
func writeSeries(gauge *metrics.GaugeVec, previous map[schema.GroupResource]struct{},
	counts map[schema.GroupResource]float64) map[schema.GroupResource]struct{} {
	for gr := range previous {
		if _, still := counts[gr]; !still {
			gauge.DeleteLabelValues(gr.Group, gr.Resource)
		}
	}
	written := make(map[schema.GroupResource]struct{}, len(counts))
	for gr, n := range counts {
		gauge.WithLabelValues(gr.Group, gr.Resource).Set(n)
		written[gr] = struct{}{}
	}
	return written
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
