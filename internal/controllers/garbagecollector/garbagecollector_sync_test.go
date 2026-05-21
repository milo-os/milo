package garbagecollector

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/controller-manager/pkg/informerfactory"
)

// ---------------------------------------------------------------------------
// Test resources used across test cases
// ---------------------------------------------------------------------------

var (
	gvrPods        = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}
	gvrServices    = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"}
	gvrDeployments = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	gvrSecrets     = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}
	gvrConfigMaps  = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}

	allResources = map[schema.GroupVersionResource]struct{}{
		gvrPods:        {},
		gvrServices:    {},
		gvrDeployments: {},
		gvrSecrets:     {},
		gvrConfigMaps:  {},
	}

	gvkPods        = schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"}
	gvkServices    = schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Service"}
	gvkDeployments = schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
	gvkSecrets     = schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Secret"}
	gvkConfigMaps  = schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}

	gvrToGVK = map[schema.GroupVersionResource]schema.GroupVersionKind{
		gvrPods:        gvkPods,
		gvrServices:    gvkServices,
		gvrDeployments: gvkDeployments,
		gvrSecrets:     gvkSecrets,
		gvrConfigMaps:  gvkConfigMaps,
	}
)

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

// fakeServerResources implements discovery.ServerResourcesInterface.
type fakeServerResources struct {
	resources []*metav1.APIResourceList
	err       error
}

func (f *fakeServerResources) ServerResourcesForGroupVersion(string) (*metav1.APIResourceList, error) {
	return nil, nil
}
func (f *fakeServerResources) ServerGroupsAndResources() ([]*metav1.APIGroup, []*metav1.APIResourceList, error) {
	return nil, f.resources, f.err
}
func (f *fakeServerResources) ServerPreferredResources() ([]*metav1.APIResourceList, error) {
	return f.resources, f.err
}
func (f *fakeServerResources) ServerPreferredNamespacedResources() ([]*metav1.APIResourceList, error) {
	return f.resources, f.err
}

func discoveryForResources(resources map[schema.GroupVersionResource]struct{}) *fakeServerResources {
	byGV := map[schema.GroupVersion]*metav1.APIResourceList{}
	for gvr := range resources {
		gv := gvr.GroupVersion()
		rl, ok := byGV[gv]
		if !ok {
			rl = &metav1.APIResourceList{GroupVersion: gv.String()}
			byGV[gv] = rl
		}
		rl.APIResources = append(rl.APIResources, metav1.APIResource{
			Name:    gvr.Resource,
			Verbs:   metav1.Verbs{"delete", "list", "watch", "get"},
			Group:   gvr.Group,
			Version: gvr.Version,
		})
	}
	var lists []*metav1.APIResourceList
	for _, rl := range byGV {
		lists = append(lists, rl)
	}
	return &fakeServerResources{resources: lists}
}

// fakeResettableRESTMapper satisfies meta.ResettableRESTMapper using a static
// GVR→GVK map. Only KindFor and Reset are needed by the GC sync path.
type fakeResettableRESTMapper struct {
	kinds map[schema.GroupVersionResource]schema.GroupVersionKind
}

func (f *fakeResettableRESTMapper) Reset() {}
func (f *fakeResettableRESTMapper) KindFor(r schema.GroupVersionResource) (schema.GroupVersionKind, error) {
	if gvk, ok := f.kinds[r]; ok {
		return gvk, nil
	}
	return schema.GroupVersionKind{}, fmt.Errorf("no mapping for %v", r)
}
func (f *fakeResettableRESTMapper) KindsFor(r schema.GroupVersionResource) ([]schema.GroupVersionKind, error) {
	gvk, err := f.KindFor(r)
	if err != nil {
		return nil, err
	}
	return []schema.GroupVersionKind{gvk}, nil
}
func (f *fakeResettableRESTMapper) ResourceFor(schema.GroupVersionResource) (schema.GroupVersionResource, error) {
	return schema.GroupVersionResource{}, fmt.Errorf("not implemented")
}
func (f *fakeResettableRESTMapper) ResourcesFor(schema.GroupVersionResource) ([]schema.GroupVersionResource, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *fakeResettableRESTMapper) RESTMapping(schema.GroupKind, ...string) (*meta.RESTMapping, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *fakeResettableRESTMapper) RESTMappings(schema.GroupKind, ...string) ([]*meta.RESTMapping, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *fakeResettableRESTMapper) ResourceSingularizer(string) (string, error) {
	return "", fmt.Errorf("not implemented")
}

var _ meta.ResettableRESTMapper = (*fakeResettableRESTMapper)(nil)

// fakeInformerFactory returns stub informers for any resource.
type fakeInformerFactory struct{}

func (f *fakeInformerFactory) ForResource(schema.GroupVersionResource) (informers.GenericInformer, error) {
	return &fakeGenericInformer{}, nil
}
func (f *fakeInformerFactory) Start(<-chan struct{}) {}

var _ informerfactory.InformerFactory = (*fakeInformerFactory)(nil)

type fakeGenericInformer struct {
	inf *fakeSharedIndexInformer
}

func (f *fakeGenericInformer) Informer() cache.SharedIndexInformer {
	if f.inf == nil {
		f.inf = newFakeSharedIndexInformer()
	}
	return f.inf
}
func (f *fakeGenericInformer) Lister() cache.GenericLister { return &fakeGenericLister{} }

type fakeGenericLister struct{}

func (f *fakeGenericLister) List(labels.Selector) ([]runtime.Object, error) { return nil, nil }
func (f *fakeGenericLister) Get(string) (runtime.Object, error)             { return nil, nil }
func (f *fakeGenericLister) ByNamespace(string) cache.GenericNamespaceLister { return nil }

// fakeSharedIndexInformer satisfies cache.SharedIndexInformer.
type fakeSharedIndexInformer struct {
	store cache.Store
	ctrl  *fakeController
}

func newFakeSharedIndexInformer() *fakeSharedIndexInformer {
	return &fakeSharedIndexInformer{
		store: cache.NewStore(cache.MetaNamespaceKeyFunc),
		ctrl:  &fakeController{synced: true},
	}
}

type fakeHandlerRegistration struct{ synced bool }

func (r *fakeHandlerRegistration) HasSynced() bool { return r.synced }

func (f *fakeSharedIndexInformer) AddEventHandler(cache.ResourceEventHandler) (cache.ResourceEventHandlerRegistration, error) {
	return &fakeHandlerRegistration{synced: true}, nil
}
func (f *fakeSharedIndexInformer) AddEventHandlerWithResyncPeriod(cache.ResourceEventHandler, time.Duration) (cache.ResourceEventHandlerRegistration, error) {
	return &fakeHandlerRegistration{synced: true}, nil
}
func (f *fakeSharedIndexInformer) RemoveEventHandler(cache.ResourceEventHandlerRegistration) error {
	return nil
}
func (f *fakeSharedIndexInformer) GetStore() cache.Store          { return f.store }
func (f *fakeSharedIndexInformer) GetController() cache.Controller { return f.ctrl }
func (f *fakeSharedIndexInformer) Run(<-chan struct{})             {}
func (f *fakeSharedIndexInformer) HasSynced() bool                 { return f.ctrl.synced }
func (f *fakeSharedIndexInformer) LastSyncResourceVersion() string { return "" }
func (f *fakeSharedIndexInformer) SetWatchErrorHandler(cache.WatchErrorHandler) error {
	return nil
}
func (f *fakeSharedIndexInformer) SetTransform(cache.TransformFunc) error { return nil }
func (f *fakeSharedIndexInformer) IsStopped() bool                         { return false }
func (f *fakeSharedIndexInformer) AddIndexers(cache.Indexers) error        { return nil }
func (f *fakeSharedIndexInformer) GetIndexer() cache.Indexer               { return cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{}) }

type fakeController struct {
	synced bool
}

func (f *fakeController) Run(<-chan struct{})         {}
func (f *fakeController) HasSynced() bool              { return f.synced }
func (f *fakeController) LastSyncResourceVersion() string { return "" }

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newTestGC creates a minimal GarbageCollector suitable for sync tests.
func newTestGC(mapper *fakeResettableRESTMapper) *GarbageCollector {
	atd := workqueue.NewTypedRateLimitingQueueWithConfig(
		workqueue.DefaultTypedControllerRateLimiter[*node](),
		workqueue.TypedRateLimitingQueueConfig[*node]{Name: "test_atd"},
	)
	ato := workqueue.NewTypedRateLimitingQueueWithConfig(
		workqueue.DefaultTypedControllerRateLimiter[*node](),
		workqueue.TypedRateLimitingQueueConfig[*node]{Name: "test_ato"},
	)
	return &GarbageCollector{
		restMapper:      mapper,
		attemptToDelete: atd,
		attemptToOrphan: ato,
		absentOwnerCache: NewReferenceCache(100),
		eventBroadcaster: record.NewBroadcaster(record.WithContext(context.Background())),
	}
}

// newTestGraphBuilder creates a GraphBuilder with pre-populated monitors for
// the given resources. Monitors are created as already-started (non-nil stopCh)
// so startMonitors is a no-op.
func newTestGraphBuilder(
	project string,
	resources map[schema.GroupVersionResource]struct{},
	mapper meta.RESTMapper,
	factory informerfactory.InformerFactory,
	atd workqueue.TypedRateLimitingInterface[*node],
	ato workqueue.TypedRateLimitingInterface[*node],
	absent *ReferenceCache,
	broadcaster record.EventBroadcaster,
) *GraphBuilder {
	informersStarted := make(chan struct{})
	close(informersStarted)

	gb := &GraphBuilder{
		restMapper:       mapper,
		project:          project,
		monitors:         monitors{},
		informersStarted: informersStarted,
		running:          true,
		metadataClient:   nil,
		graphChanges: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[*event](),
			workqueue.TypedRateLimitingQueueConfig[*event]{Name: "test_gc_" + project},
		),
		uidToNode:        &concurrentUIDToNode{uidToNode: make(map[types.UID]*node)},
		attemptToDelete:  atd,
		attemptToOrphan:  ato,
		absentOwnerCache: absent,
		sharedInformers:  factory,
		ignoredResources: map[schema.GroupResource]struct{}{},
		eventRecorder:    broadcaster.NewRecorder(runtime.NewScheme(), v1.EventSource{Component: "test"}),
		eventBroadcaster: broadcaster,
	}

	// Pre-populate monitors with already-started dummy monitors
	for gvr := range resources {
		stopCh := make(chan struct{})
		gb.monitors[gvr] = &monitor{
			store:      cache.NewStore(cache.MetaNamespaceKeyFunc),
			controller: &fakeController{synced: true},
			stopCh:     stopCh,
		}
	}

	return gb
}

func monitorCount(gb *GraphBuilder) int {
	gb.monitorLock.RLock()
	defer gb.monitorLock.RUnlock()
	return len(gb.monitors)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestSyncOnce_SkipsResyncWhenResourcesUnchanged verifies that the Sync loop
// short-circuits (does not call resyncMonitors) when root discovery reports the
// same resources as the previous tick and no new builders have been added.
func TestSyncOnce_SkipsResyncWhenResourcesUnchanged(t *testing.T) {
	mapper := &fakeResettableRESTMapper{kinds: gvrToGVK}
	gc := newTestGC(mapper)

	disc := discoveryForResources(allResources)
	factory := &fakeInformerFactory{}

	rootGB := newTestGraphBuilder("", allResources, mapper, factory,
		gc.attemptToDelete, gc.attemptToOrphan, gc.absentOwnerCache, gc.eventBroadcaster)
	gc.dependencyGraphBuilders = []*GraphBuilder{rootGB}

	ctx := context.Background()

	// First syncOnce: oldResources is empty, so it resyncs
	newOld, ok := gc.syncOnce(ctx, disc, map[schema.GroupVersionResource]struct{}{}, 0)
	if !ok {
		t.Fatal("first syncOnce should have performed a resync")
	}
	if len(newOld) != len(allResources) {
		t.Fatalf("expected %d resources, got %d", len(allResources), len(newOld))
	}

	// Second syncOnce: oldResources == newResources, should short-circuit
	_, ok = gc.syncOnce(ctx, disc, newOld, 0)
	if ok {
		t.Fatal("second syncOnce should have short-circuited (resources unchanged, no new builders)")
	}
}

// TestSyncOnce_ResyncsWhenResyncNeeded_BugRepro verifies the original bug:
// a per-project builder added with incomplete monitors is never resynced
// because the Sync loop short-circuits on unchanged root resources.
//
// Without the fix (resyncNeeded flag), the second syncOnce returns false
// and the project builder keeps its incomplete monitor set.
func TestSyncOnce_ResyncsWhenResyncNeeded_BugRepro(t *testing.T) {
	mapper := &fakeResettableRESTMapper{kinds: gvrToGVK}
	gc := newTestGC(mapper)

	disc := discoveryForResources(allResources)
	factory := &fakeInformerFactory{}

	rootGB := newTestGraphBuilder("", allResources, mapper, factory,
		gc.attemptToDelete, gc.attemptToOrphan, gc.absentOwnerCache, gc.eventBroadcaster)

	// Simulate a project builder that was seeded with only 2 of 5 resources
	// (PCP API wasn't fully ready when AddProject ran).
	incompleteResources := map[schema.GroupVersionResource]struct{}{
		gvrPods:     {},
		gvrServices: {},
	}
	projectGB := newTestGraphBuilder("zachs-project", incompleteResources, mapper, factory,
		gc.attemptToDelete, gc.attemptToOrphan, gc.absentOwnerCache, gc.eventBroadcaster)

	gc.dependencyGraphBuilders = []*GraphBuilder{rootGB, projectGB}

	ctx := context.Background()

	// First syncOnce: seeds oldResources
	newOld, ok := gc.syncOnce(ctx, disc, map[schema.GroupVersionResource]struct{}{}, 0)
	if !ok {
		t.Fatal("first syncOnce should have performed a resync")
	}

	// At this point both builders have been resynced with allResources.
	// Reset project builder to incomplete state to simulate what would
	// happen if AddProject ran AFTER the first sync tick.
	projectGB.monitorLock.Lock()
	// Close excess monitors' stopCh
	for gvr, m := range projectGB.monitors {
		if _, inOriginal := incompleteResources[gvr]; !inOriginal {
			if m.stopCh != nil {
				close(m.stopCh)
			}
			delete(projectGB.monitors, gvr)
		}
	}
	projectGB.monitorLock.Unlock()

	if monitorCount(projectGB) != 2 {
		t.Fatalf("project builder should have 2 monitors, got %d", monitorCount(projectGB))
	}

	// Simulate AddProject setting the flag (as our fix does)
	gc.mu.Lock()
	gc.resyncNeeded = true
	gc.mu.Unlock()

	// Second syncOnce: root resources haven't changed, but resyncNeeded is true.
	// With the fix, this should resync and bring the project builder up to date.
	_, ok = gc.syncOnce(ctx, disc, newOld, 0)
	if !ok {
		t.Fatal("syncOnce should have resynced because resyncNeeded was true")
	}

	if got := monitorCount(projectGB); got != len(allResources) {
		t.Fatalf("after resync, project builder should have %d monitors, got %d", len(allResources), got)
	}
}

// TestSyncOnce_ClearsResyncNeeded verifies that after a successful resync,
// the resyncNeeded flag is cleared so subsequent ticks short-circuit normally.
func TestSyncOnce_ClearsResyncNeeded(t *testing.T) {
	mapper := &fakeResettableRESTMapper{kinds: gvrToGVK}
	gc := newTestGC(mapper)

	disc := discoveryForResources(allResources)
	factory := &fakeInformerFactory{}

	rootGB := newTestGraphBuilder("", allResources, mapper, factory,
		gc.attemptToDelete, gc.attemptToOrphan, gc.absentOwnerCache, gc.eventBroadcaster)
	gc.dependencyGraphBuilders = []*GraphBuilder{rootGB}

	gc.mu.Lock()
	gc.resyncNeeded = true
	gc.mu.Unlock()

	ctx := context.Background()

	// First syncOnce: resyncNeeded forces resync
	newOld, ok := gc.syncOnce(ctx, disc, map[schema.GroupVersionResource]struct{}{}, 0)
	if !ok {
		t.Fatal("first syncOnce should have resynced")
	}

	gc.mu.RLock()
	if gc.resyncNeeded {
		t.Fatal("resyncNeeded should be false after successful resync")
	}
	gc.mu.RUnlock()

	// Second syncOnce: resources unchanged, resyncNeeded cleared → should skip
	_, ok = gc.syncOnce(ctx, disc, newOld, 0)
	if ok {
		t.Fatal("second syncOnce should have short-circuited after resyncNeeded was cleared")
	}
}

// TestSyncOnce_WithoutFix_BugDemo demonstrates the pre-fix behavior: without
// the resyncNeeded flag, a project builder with incomplete monitors is never
// resynced when root resources remain stable.
func TestSyncOnce_WithoutFix_BugDemo(t *testing.T) {
	mapper := &fakeResettableRESTMapper{kinds: gvrToGVK}
	gc := newTestGC(mapper)

	disc := discoveryForResources(allResources)
	factory := &fakeInformerFactory{}

	rootGB := newTestGraphBuilder("", allResources, mapper, factory,
		gc.attemptToDelete, gc.attemptToOrphan, gc.absentOwnerCache, gc.eventBroadcaster)
	gc.dependencyGraphBuilders = []*GraphBuilder{rootGB}

	ctx := context.Background()

	// First syncOnce: seeds oldResources with the full set
	newOld, ok := gc.syncOnce(ctx, disc, map[schema.GroupVersionResource]struct{}{}, 0)
	if !ok {
		t.Fatal("first syncOnce should have performed a resync")
	}

	// A new project arrives with only 2 monitors (PCP API was incomplete)
	incompleteResources := map[schema.GroupVersionResource]struct{}{
		gvrPods:     {},
		gvrServices: {},
	}
	projectGB := newTestGraphBuilder("zachs-project", incompleteResources, mapper, factory,
		gc.attemptToDelete, gc.attemptToOrphan, gc.absentOwnerCache, gc.eventBroadcaster)

	gc.mu.Lock()
	gc.dependencyGraphBuilders = append(gc.dependencyGraphBuilders, projectGB)
	// NOTE: NOT setting gc.resyncNeeded - simulating old code without the fix
	gc.mu.Unlock()

	// Second syncOnce: root resources unchanged, resyncNeeded is false
	// → short-circuits, project builder stays incomplete
	_, ok = gc.syncOnce(ctx, disc, newOld, 0)
	if ok {
		t.Fatal("without fix: syncOnce should short-circuit (resources unchanged)")
	}

	// Verify the project builder still only has incomplete monitors
	if got := monitorCount(projectGB); got != 2 {
		t.Fatalf("without fix: project builder should still have 2 monitors, got %d", got)
	}
}

// TestConcurrentAddProjectAndSync verifies that concurrent AddProject calls
// and Sync ticks don't race on the resyncNeeded flag.
func TestConcurrentAddProjectAndSync(t *testing.T) {
	mapper := &fakeResettableRESTMapper{kinds: gvrToGVK}
	gc := newTestGC(mapper)

	disc := discoveryForResources(allResources)
	factory := &fakeInformerFactory{}

	rootGB := newTestGraphBuilder("", allResources, mapper, factory,
		gc.attemptToDelete, gc.attemptToOrphan, gc.absentOwnerCache, gc.eventBroadcaster)
	gc.dependencyGraphBuilders = []*GraphBuilder{rootGB}

	ctx := context.Background()

	// Seed oldResources
	newOld, _ := gc.syncOnce(ctx, disc, map[schema.GroupVersionResource]struct{}{}, 0)

	var wg sync.WaitGroup
	const workers = 10

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			// Simulate setting resyncNeeded (what AddProject does)
			gc.mu.Lock()
			gc.resyncNeeded = true
			gc.mu.Unlock()
		}(i)
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			gc.syncOnce(ctx, disc, newOld, 0)
		}()
	}

	wg.Wait()
}
