/*
Copyright 2016 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package garbagecollector

import (
	"context"
	goerrors "errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery"
	clientset "k8s.io/client-go/kubernetes" // import known versions
	v1core "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/controller-manager/controller"
	"k8s.io/controller-manager/pkg/informerfactory"
	"k8s.io/klog/v2"
	c "k8s.io/kubernetes/pkg/controller"
	"k8s.io/kubernetes/pkg/controller/garbagecollector/metrics"
)

// ResourceResyncTime defines the resync period of the garbage collector's informers.
const ResourceResyncTime time.Duration = 0

// GarbageCollector runs reflectors to watch for changes of managed API
// objects, funnels the results to a single-threaded dependencyGraphBuilder,
// which builds a graph caching the dependencies among objects. Triggered by the
// graph changes, the dependencyGraphBuilder enqueues objects that can
// potentially be garbage-collected to the `attemptToDelete` queue, and enqueues
// objects whose dependents need to be orphaned to the `attemptToOrphan` queue.
// The GarbageCollector has workers who consume these two queues, send requests
// to the API server to delete/update the objects accordingly.
// Note that having the dependencyGraphBuilder notify the garbage collector
// ensures that the garbage collector operates with a graph that is at least as
// up to date as the notification is sent.
type GarbageCollector struct {
	mu             sync.RWMutex
	restMapper     meta.ResettableRESTMapper
	metadataClient metadata.Interface
	// garbage collector attempts to delete the items in attemptToDelete queue when the time is ripe.
	attemptToDelete workqueue.TypedRateLimitingInterface[*node]
	// garbage collector attempts to orphan the dependents of the items in the attemptToOrphan queue, then deletes the items.
	attemptToOrphan workqueue.TypedRateLimitingInterface[*node]
	// GC caches the owners that do not exist according to the API server.
	absentOwnerCache *ReferenceCache

	kubeClient       clientset.Interface
	eventBroadcaster record.EventBroadcaster

	dependencyGraphBuilders []*GraphBuilder
	cancels                 map[string]context.CancelFunc

	// resyncNeeded is set when a new per-project builder is added via
	// AddProject. It forces the next Sync tick to resync all builders even
	// if root discovery reports the same resource set, covering the case
	// where the new builder was seeded with incomplete monitors.
	resyncNeeded bool
}

var _ controller.Interface = (*GarbageCollector)(nil)
var _ controller.Debuggable = (*GarbageCollector)(nil)

func (gc *GarbageCollector) AddProject(
	parent context.Context,
	project string,
	md metadata.Interface,
	mapper meta.ResettableRESTMapper,
	ignored map[schema.GroupResource]struct{},
	shared informerfactory.InformerFactory,
	informersStarted <-chan struct{},
	discover discovery.ServerResourcesInterface,
	initialSyncTimeout time.Duration,
) error {
	gc.mu.Lock()
	if gc.cancels == nil {
		gc.cancels = make(map[string]context.CancelFunc)
	}
	// Skip projects that are already registered (idempotent for retries).
	if _, exists := gc.cancels[project]; exists {
		gc.mu.Unlock()
		return nil
	}
	gc.mu.Unlock()

	logger := klog.FromContext(parent)

	// Reuse shared queues/cache from GC (created from the root GB).
	atd, ato, absent := gc.attemptToDelete, gc.attemptToOrphan, gc.absentOwnerCache

	gb := NewDependencyGraphBuilderWithShared(
		parent,
		md,
		mapper,
		ignored,
		shared,
		informersStarted,
		atd,
		ato,
		absent,
		gc.eventBroadcaster,
	)
	gb.SetProject(project)

	// Seed monitors BEFORE registering the builder. If discovery or monitor
	// creation fails (e.g. apiserver throttling during startup burst), the
	// builder is never registered and the caller can retry cleanly.
	newResources, err := GetDeletableResources(logger, discover)
	if err != nil {
		logger.V(2).Info("GC: partial discovery for project", "project", project, "error", err)
	}
	if len(newResources) == 0 {
		return fmt.Errorf("gc(%s): discovery returned no resources", project)
	}
	if err := gb.syncMonitors(logger, newResources); err != nil {
		return fmt.Errorf("gc(%s): syncMonitors: %w", project, err)
	}

	// Monitors created successfully — register the builder and start it.
	ctx, cancel := context.WithCancel(parent)

	gc.mu.Lock()
	gc.dependencyGraphBuilders = append(gc.dependencyGraphBuilders, gb)
	gc.cancels[project] = cancel
	gc.resyncNeeded = true
	gc.mu.Unlock()

	partitionCount.Inc()
	partitionMonitorCount.WithLabelValues(project).Set(float64(monitorCountForBuilder(gb)))

	go gb.Run(ctx)

	ok := cache.WaitForNamedCacheSync(
		"gc-"+project,
		waitForStopOrTimeout(ctx.Done(), initialSyncTimeout),
		func() bool { return gb.IsSynced(logger) },
	)
	if !ok {
		logger.Info("GC: partition monitors not fully synced; continuing", "project", project)
	}

	partitionSynced.WithLabelValues(project).Set(boolToFloat(ok))

	return nil
}

func (gc *GarbageCollector) RemoveProject(project string) {
	gc.mu.Lock()
	if cancel, ok := gc.cancels[project]; ok {
		cancel()
		delete(gc.cancels, project)
	}
	dst := gc.dependencyGraphBuilders[:0]
	for _, gb := range gc.dependencyGraphBuilders {
		if gb.project != project {
			dst = append(dst, gb)
		}
	}
	gc.dependencyGraphBuilders = dst
	gc.mu.Unlock()

	partitionCount.Dec()
	partitionMonitorCount.Delete(map[string]string{"project": project})
	partitionSynced.Delete(map[string]string{"project": project})
}

// NewGarbageCollector creates a new GarbageCollector.
func NewGarbageCollector(
	ctx context.Context,
	kubeClient clientset.Interface,
	metadataClient metadata.Interface,
	mapper meta.ResettableRESTMapper,
	ignoredResources map[schema.GroupResource]struct{},
	sharedInformers informerfactory.InformerFactory,
	informersStarted <-chan struct{},
) (*GarbageCollector, error) {
	graphBuilder := NewDependencyGraphBuilder(ctx, metadataClient, mapper, ignoredResources, sharedInformers, informersStarted)
	return NewComposedGarbageCollector(ctx, kubeClient, metadataClient, mapper, graphBuilder)
}

func NewComposedGarbageCollector(
	ctx context.Context,
	kubeClient clientset.Interface,
	metadataClient metadata.Interface,
	mapper meta.ResettableRESTMapper,
	graphBuilder *GraphBuilder,
) (*GarbageCollector, error) {
	return NewComposedGarbageCollectorMulti(ctx, kubeClient, metadataClient, mapper, graphBuilder)
}

func NewComposedGarbageCollectorMulti(
	ctx context.Context,
	kubeClient clientset.Interface,
	metadataClient metadata.Interface,
	mapper meta.ResettableRESTMapper,
	graphBuilders ...*GraphBuilder,
) (*GarbageCollector, error) {
	if len(graphBuilders) == 0 {
		return nil, fmt.Errorf("no graph builders provided")
	}

	// All builders must share these (caller ensures via WithShared).
	delQ, orphanQ, absent := graphBuilders[0].GetGraphResources()

	gc := &GarbageCollector{
		metadataClient:          metadataClient, // kept for legacy paths; live calls should route by node.identity.Project
		restMapper:              mapper,
		attemptToDelete:         delQ,
		attemptToOrphan:         orphanQ,
		absentOwnerCache:        absent,
		kubeClient:              kubeClient,
		eventBroadcaster:        graphBuilders[0].eventBroadcaster,
		dependencyGraphBuilders: graphBuilders, // multi-partition path
		// dependencyGraphBuilder left nil intentionally in multi mode
	}

	metrics.Register()
	registerPartitionMetricsOnce()
	return gc, nil
}

// resyncMonitors starts or stops resource monitors as needed to ensure that all
// (and only) those resources present in the map are monitored.
func (gc *GarbageCollector) resyncMonitors(
	logger klog.Logger,
	deletableResources map[schema.GroupVersionResource]struct{},
) error {
	if len(gc.dependencyGraphBuilders) == 0 {
		return fmt.Errorf("no dependency graph builders configured")
	}
	for _, gb := range gc.dependencyGraphBuilders {
		if err := gb.syncMonitors(logger, deletableResources); err != nil {
			return err
		}
		gb.startMonitors(logger)
		if gb.project != "" {
			partitionMonitorCount.WithLabelValues(gb.project).Set(float64(monitorCountForBuilder(gb)))
		}
	}
	return nil
}

// Run starts garbage collector workers.
// Run starts garbage collector workers.
func (gc *GarbageCollector) Run(ctx context.Context, workers int, initialSyncTimeout time.Duration) {
	defer utilruntime.HandleCrash()
	defer gc.attemptToDelete.ShutDown()
	defer gc.attemptToOrphan.ShutDown()

	// Stop all builders' graphChanges on exit.
	defer func() {
		for _, gb := range gc.dependencyGraphBuilders {
			gb.graphChanges.ShutDown()
		}
	}()

	// Events pipeline.
	gc.eventBroadcaster.StartStructuredLogging(3)
	gc.eventBroadcaster.StartRecordingToSink(&v1core.EventSinkImpl{Interface: gc.kubeClient.CoreV1().Events("")})
	defer gc.eventBroadcaster.Shutdown()

	logger := klog.FromContext(ctx)
	logger.Info("Starting controller", "controller", "garbagecollector")
	defer logger.Info("Shutting down controller", "controller", "garbagecollector")

	if len(gc.dependencyGraphBuilders) == 0 {
		logger.Error(nil, "no dependency graph builders configured")
		return
	}

	// Start all graph builders.
	for _, gb := range gc.dependencyGraphBuilders {
		go gb.Run(ctx)
	}

	// Wait for ALL builders to sync.
	synced := cache.WaitForNamedCacheSync(
		"garbage collector",
		waitForStopOrTimeout(ctx.Done(), initialSyncTimeout),
		func() bool {
			for _, gb := range gc.dependencyGraphBuilders {
				if !gb.IsSynced(logger) {
					return false
				}
			}
			return true
		},
	)
	if !synced {
		logger.Info("Garbage collector: not all resource monitors could be synced, proceeding anyways")
	} else {
		logger.Info("Garbage collector: all resource monitors have synced")
	}

	logger.Info("Proceeding to collect garbage")

	// Workers consume the shared queues.
	for i := 0; i < workers; i++ {
		go wait.UntilWithContext(ctx, gc.runAttemptToDeleteWorker, time.Second)
		go wait.Until(func() { gc.runAttemptToOrphanWorker(logger) }, time.Second, ctx.Done())
	}

	<-ctx.Done()
}

func (gc *GarbageCollector) anyBuilderResourceSynced(res schema.GroupVersionResource) bool {
	for _, gb := range gc.dependencyGraphBuilders {
		if gb.IsResourceSynced(res) {
			return true
		}
	}
	return false
}

// Sync periodically resyncs the garbage collector when new resources are
// observed from discovery. When new resources are detected, it will reset
// gc.restMapper, and resync the monitors.
//
// Note that discoveryClient should NOT be shared with gc.restMapper, otherwise
// the mapper's underlying discovery client will be unnecessarily reset during
// the course of detecting new resources.
func (gc *GarbageCollector) Sync(ctx context.Context, discoveryClient discovery.ServerResourcesInterface, period time.Duration) {
	oldResources := make(map[schema.GroupVersionResource]struct{})

	wait.UntilWithContext(ctx, func(ctx context.Context) {
		newOld, ok := gc.syncOnce(ctx, discoveryClient, oldResources, period)
		if ok {
			oldResources = newOld
		}
	}, period)
}

// syncOnce performs a single sync cycle. It returns the resource set to
// remember and true when a resync was performed successfully. If the tick
// was skipped or the resync failed, it returns nil/false so the caller
// retains the previous oldResources.
func (gc *GarbageCollector) syncOnce(
	ctx context.Context,
	discoveryClient discovery.ServerResourcesInterface,
	oldResources map[schema.GroupVersionResource]struct{},
	waitPeriod time.Duration,
) (map[schema.GroupVersionResource]struct{}, bool) {
	logger := klog.FromContext(ctx)

	// 1) Discover deletable resources
	newResources, err := GetDeletableResources(logger, discoveryClient)
	if len(newResources) == 0 {
		logger.V(2).Info("no resources reported by discovery, skipping garbage collector sync")
		metrics.GarbageCollectorResourcesSyncError.Inc()
		return nil, false
	}

	// 2) Handle partial discovery: keep already-synced monitors for failed groups
	if groupLookupFailures, isLookupFailure := discovery.GroupDiscoveryFailedErrorGroups(err); isLookupFailure {
		for k, v := range oldResources {
			if _, failed := groupLookupFailures[k.GroupVersion()]; failed && gc.anyBuilderResourceSynced(k) {
				newResources[k] = v
			}
		}
	}

	// 3) Short-circuit if nothing changed AND no new builders need resyncing
	gc.mu.RLock()
	forceResync := gc.resyncNeeded
	gc.mu.RUnlock()

	if !forceResync && reflect.DeepEqual(oldResources, newResources) {
		logger.V(5).Info("no resource updates from discovery, skipping garbage collector sync")
		return nil, false
	}

	logger.V(2).Info("syncing garbage collector with updated resources from discovery",
		"diff", printDiff(oldResources, newResources),
		"forceResync", forceResync)

	// 4) Reset REST mapper (invalidates its underlying discovery cache)
	gc.restMapper.Reset()
	logger.V(4).Info("reset restmapper")

	// 5) Resync monitors across ALL builders
	if err := gc.resyncMonitors(logger, newResources); err != nil {
		utilruntime.HandleError(fmt.Errorf("failed to sync resource monitors: %w", err))
		metrics.GarbageCollectorResourcesSyncError.Inc()
		return nil, false
	}
	logger.V(4).Info("resynced monitors")

	gc.mu.Lock()
	gc.resyncNeeded = false
	gc.mu.Unlock()

	// 6) Periodically check that ALL builders report cache synced (for logs/metrics)
	if waitPeriod > 0 {
		cacheSynced := cache.WaitForNamedCacheSync("garbage collector", waitForStopOrTimeout(ctx.Done(), waitPeriod), func() bool {
			for _, gb := range gc.dependencyGraphBuilders {
				if !gb.IsSynced(logger) {
					return false
				}
			}
			return true
		})
		if cacheSynced {
			logger.V(2).Info("synced garbage collector")
		} else {
			utilruntime.HandleError(fmt.Errorf("timed out waiting for dependency graph builder sync during GC sync"))
			metrics.GarbageCollectorResourcesSyncError.Inc()
		}
	}

	// 7) Remember current resource set
	return newResources, true
}

// printDiff returns a human-readable summary of what resources were added and removed
func printDiff(oldResources, newResources map[schema.GroupVersionResource]struct{}) string {
	removed := sets.NewString()
	for oldResource := range oldResources {
		if _, ok := newResources[oldResource]; !ok {
			removed.Insert(fmt.Sprintf("%+v", oldResource))
		}
	}
	added := sets.NewString()
	for newResource := range newResources {
		if _, ok := oldResources[newResource]; !ok {
			added.Insert(fmt.Sprintf("%+v", newResource))
		}
	}
	return fmt.Sprintf("added: %v, removed: %v", added.List(), removed.List())
}

// waitForStopOrTimeout returns a stop channel that closes when the provided stop channel closes or when the specified timeout is reached
func waitForStopOrTimeout(stopCh <-chan struct{}, timeout time.Duration) <-chan struct{} {
	stopChWithTimeout := make(chan struct{})
	go func() {
		select {
		case <-stopCh:
		case <-time.After(timeout):
		}
		close(stopChWithTimeout)
	}()
	return stopChWithTimeout
}

// IsSynced returns true if dependencyGraphBuilder is synced.
func (gc *GarbageCollector) IsSynced(logger klog.Logger) bool {
	if len(gc.dependencyGraphBuilders) == 0 {
		return false
	}
	for _, gb := range gc.dependencyGraphBuilders {
		if !gb.IsSynced(logger) {
			return false
		}
	}
	return true
}

func (gc *GarbageCollector) runAttemptToDeleteWorker(ctx context.Context) {
	for gc.processAttemptToDeleteWorker(ctx) {
	}
}

var errEnqueuedVirtualDeleteEvent = goerrors.New("enqueued virtual delete event")

var errNamespacedOwnerOfClusterScopedObject = goerrors.New("cluster-scoped objects cannot refer to namespaced owners")

func (gc *GarbageCollector) processAttemptToDeleteWorker(ctx context.Context) bool {
	item, quit := gc.attemptToDelete.Get()
	if quit {
		return false
	}
	defer gc.attemptToDelete.Done(item)

	action := gc.attemptToDeleteWorker(ctx, item)
	switch action {
	case forgetItem:
		gc.attemptToDelete.Forget(item)
	case requeueItem:
		gc.attemptToDelete.AddRateLimited(item)
	}

	return true
}

type workQueueItemAction int

const (
	requeueItem = iota
	forgetItem
)

// helper: find builder by project id
func (gc *GarbageCollector) builderForProject(project string) *GraphBuilder {
	for _, gb := range gc.dependencyGraphBuilders {
		if gb.project == project {
			return gb
		}
	}
	return nil
}

// helper: fallback — search any builder by UID (only used if project is empty)
func (gc *GarbageCollector) findNodeInAnyBuilder(uid types.UID) (*node, bool) {
	for _, gb := range gc.dependencyGraphBuilders {
		if n, ok := gb.uidToNode.Read(uid); ok {
			return n, true
		}
	}
	return nil, false
}

func (gc *GarbageCollector) attemptToDeleteWorker(ctx context.Context, item interface{}) workQueueItemAction {
	n, ok := item.(*node)
	if !ok {
		utilruntime.HandleError(fmt.Errorf("expect *node, got %#v", item))
		return forgetItem
	}
	logger := klog.FromContext(ctx)

	// choose the right graph for this node’s partition
	var gb *GraphBuilder
	if n.identity.Project != "" {
		gb = gc.builderForProject(n.identity.Project)
	} else {
		// legacy/defensive: try to find by UID in any builder
		if nf, found := gc.findNodeInAnyBuilder(n.identity.UID); found {
			// trust that node’s project for future lookups
			gb = gc.builderForProject(nf.identity.Project)
		}
	}
	if gb == nil {
		// No graph for that project (partition removed or not registered yet).
		// Requeue to give registration a chance to happen.
		logger.V(2).Info("no graphbuilder for node's project; requeuing",
			"project", n.identity.Project, "item", n.identity)
		return requeueItem
	}

	if !n.isObserved() {
		nodeFromGraph, existsInGraph := gb.uidToNode.Read(n.identity.UID)
		if !existsInGraph {
			// could have been removed due to a real deletion observed meanwhile
			logger.V(5).Info("item no longer in the graph, skipping attemptToDeleteItem", "item", n.identity)
			return forgetItem
		}
		if nodeFromGraph.isObserved() {
			// real object was observed while this virtual node was requeued
			logger.V(5).Info("item no longer virtual in the graph, skipping attemptToDeleteItem on virtual node", "item", n.identity)
			return forgetItem
		}
	}

	err := gc.attemptToDeleteItem(ctx, n)
	switch {
	case err == nil:
		// success path; if the node was virtual but not yet observed, keep nudging until observed
		if !n.isObserved() {
			logger.V(5).Info("item hasn't been observed via informer yet", "item", n.identity)
			return requeueItem
		}
		return forgetItem

	case err == errEnqueuedVirtualDeleteEvent:
		// virtual delete event will be handled by the per-partition graph builder; no need to requeue
		return forgetItem

	case err == errNamespacedOwnerOfClusterScopedObject:
		// unrecoverable for this item; don't requeue
		return forgetItem

	default:
		if _, mappingErr := err.(*restMappingError); mappingErr {
			logger.V(5).Error(err, "error syncing item", "item", n.identity)
		} else {
			utilruntime.HandleError(fmt.Errorf("error syncing item %s: %v", n, err))
		}
		return requeueItem
	}
}

// isDangling check if a reference is pointing to an object that doesn't exist.
// If isDangling looks up the referenced object at the API server, it also
// returns its latest state.
func (gc *GarbageCollector) isDangling(ctx context.Context, reference metav1.OwnerReference, item *node) (bool, *metav1.PartialObjectMetadata, error) {
	logger := klog.FromContext(ctx)

	absentOwnerCacheKey := objectReference{OwnerReference: ownerReferenceCoordinates(reference)}
	if gc.absentOwnerCache.Has(absentOwnerCacheKey) {
		logger.V(5).Info("according to the absentOwnerCache, item's owner does not exist", "item", item.identity, "owner", reference)
		return true, nil, nil
	}
	absentOwnerCacheKey.Namespace = item.identity.Namespace
	if gc.absentOwnerCache.Has(absentOwnerCacheKey) {
		logger.V(5).Info("according to the absentOwnerCache, item's owner does not exist in namespace", "item", item.identity, "owner", reference)
		return true, nil, nil
	}

	// >>> change: use RESTMapper (narrow) here
	var mapper meta.RESTMapper = meta.RESTMapper(gc.restMapper) // cast
	md := gc.metadataClient
	if gb := gc.builderForProject(item.identity.Project); gb != nil {
		mapper = gb.restMapper
		md = gb.metadataClient
	}

	resource, namespaced, err := apiResourceUsing(mapper, reference.APIVersion, reference.Kind)
	if err != nil {
		return false, nil, err
	}
	if !namespaced {
		absentOwnerCacheKey.Namespace = ""
	}

	if len(item.identity.Namespace) == 0 && namespaced {
		logger.V(2).Info("item is cluster-scoped, but refers to a namespaced owner", "item", item.identity, "owner", reference)
		return false, nil, errNamespacedOwnerOfClusterScopedObject
	}

	ns := resourceDefaultNamespace(namespaced, item.identity.Namespace)
	owner, err := md.Resource(resource).Namespace(ns).Get(ctx, reference.Name, metav1.GetOptions{})
	switch {
	case errors.IsNotFound(err):
		gc.absentOwnerCache.Add(absentOwnerCacheKey)
		logger.V(5).Info("item's owner is not found", "item", item.identity, "owner", reference)
		return true, nil, nil
	case err != nil:
		return false, nil, err
	}

	if owner.GetUID() != reference.UID {
		logger.V(5).Info("item's owner is not found, UID mismatch", "item", item.identity, "owner", reference)
		gc.absentOwnerCache.Add(absentOwnerCacheKey)
		return true, nil, nil
	}
	return false, owner, nil
}

// classify the latestReferences to three categories:
// solid: the owner exists, and is not "waitingForDependentsDeletion"
// dangling: the owner does not exist
// waitingForDependentsDeletion: the owner exists, its deletionTimestamp is non-nil, and it has
// FinalizerDeletingDependents
// This function communicates with the server.
func (gc *GarbageCollector) classifyReferences(ctx context.Context, item *node, latestReferences []metav1.OwnerReference) (
	solid, dangling, waitingForDependentsDeletion []metav1.OwnerReference, err error) {
	for _, reference := range latestReferences {
		isDangling, owner, err := gc.isDangling(ctx, reference, item)
		if err != nil {
			return nil, nil, nil, err
		}
		if isDangling {
			dangling = append(dangling, reference)
			continue
		}

		ownerAccessor, err := meta.Accessor(owner)
		if err != nil {
			return nil, nil, nil, err
		}
		if ownerAccessor.GetDeletionTimestamp() != nil && hasDeleteDependentsFinalizer(ownerAccessor) {
			waitingForDependentsDeletion = append(waitingForDependentsDeletion, reference)
		} else {
			solid = append(solid, reference)
		}
	}
	return solid, dangling, waitingForDependentsDeletion, nil
}

func ownerRefsToUIDs(refs []metav1.OwnerReference) []types.UID {
	var ret []types.UID
	for _, ref := range refs {
		ret = append(ret, ref.UID)
	}
	return ret
}

// attemptToDeleteItem looks up the live API object associated with the node,
// and issues a delete IFF the uid matches, the item is not blocked on deleting dependents,
// and all owner references are dangling.
//
// if the API get request returns a NotFound error, or the retrieved item's uid does not match,
// a virtual delete event for the node is enqueued and enqueuedVirtualDeleteEventErr is returned.
func (gc *GarbageCollector) attemptToDeleteItem(ctx context.Context, item *node) error {
	logger := klog.FromContext(ctx)

	// pick the correct graph (partition) for this node
	gb := gc.builderForProject(item.identity.Project)
	if gb == nil {
		// no graph registered for this project yet — cause a retry
		return fmt.Errorf("no graph builder for project %q", item.identity.Project)
	}

	logger.V(2).Info("Processing item",
		"item", item.identity,
		"virtual", !item.isObserved(),
	)

	// If already being deleted (but not deleting dependents), nothing to do.
	if item.isBeingDeleted() && !item.isDeletingDependents() {
		logger.V(5).Info("processing item returned at once, because its DeletionTimestamp is non-nil",
			"item", item.identity,
		)
		return nil
	}

	// Fetch latest live object for this node
	latest, err := gc.getObject(item.identity)
	switch {
	case errors.IsNotFound(err):
		// object doesn't exist; enqueue a virtual delete event on the correct graph
		logger.V(5).Info("item not found, generating a virtual delete event", "item", item.identity)
		gb.enqueueVirtualDeleteEvent(item.identity)
		return errEnqueuedVirtualDeleteEvent
	case err != nil:
		return err
	}

	if latest.GetUID() != item.identity.UID {
		logger.V(5).Info("UID doesn't match, item not found, generating a virtual delete event", "item", item.identity)
		gb.enqueueVirtualDeleteEvent(item.identity)
		return errEnqueuedVirtualDeleteEvent
	}

	// If the item itself is deleting dependents, continue that flow.
	if item.isDeletingDependents() {
		return gc.processDeletingDependentsItem(logger, item)
	}

	// Decide whether to delete the item
	ownerReferences := latest.GetOwnerReferences()
	if len(ownerReferences) == 0 {
		logger.V(2).Info("item doesn't have an owner, continue on next item", "item", item.identity)
		return nil
	}

	solid, dangling, waitingForDependentsDeletion, err := gc.classifyReferences(ctx, item, ownerReferences)
	if err != nil {
		return err
	}
	logger.V(5).Info("classify item's references",
		"item", item.identity,
		"solid", solid,
		"dangling", dangling,
		"waitingForDependentsDeletion", waitingForDependentsDeletion,
	)

	switch {
	case len(solid) != 0:
		logger.V(2).Info("item has at least one existing owner, will not garbage collect", "item", item.identity, "owner", solid)
		if len(dangling) == 0 && len(waitingForDependentsDeletion) == 0 {
			return nil
		}
		logger.V(2).Info("remove dangling references and waiting references for item",
			"item", item.identity, "dangling", dangling, "waitingForDependentsDeletion", waitingForDependentsDeletion)
		ownerUIDs := append(ownerRefsToUIDs(dangling), ownerRefsToUIDs(waitingForDependentsDeletion)...)
		p, err := c.GenerateDeleteOwnerRefStrategicMergeBytes(item.identity.UID, ownerUIDs)
		if err != nil {
			return err
		}
		_, err = gc.patch(item, p, func(n *node) ([]byte, error) {
			return gc.deleteOwnerRefJSONMergePatch(n, ownerUIDs...)
		})
		return err

	case len(waitingForDependentsDeletion) != 0 && item.dependentsLength() != 0:
		deps := item.getDependents()
		for _, dep := range deps {
			if dep.isDeletingDependents() {
				logger.V(2).Info("processing item, some of its owners and its dependent have FinalizerDeletingDependents; unblocking owner refs then deleting with Foreground",
					"item", item.identity, "dependent", dep.identity)
				patch, err := item.unblockOwnerReferencesStrategicMergePatch()
				if err != nil {
					return err
				}
				if _, err := gc.patch(item, patch, gc.unblockOwnerReferencesJSONMergePatch); err != nil {
					return err
				}
				break
			}
		}
		logger.V(2).Info("deleting in Foreground because an owner waits for dependents and this item has dependents",
			"item", item.identity)
		policy := metav1.DeletePropagationForeground
		return gc.deleteObject(item.identity, latest.ResourceVersion, latest.OwnerReferences, &policy)

	default:
		// No solid owners; choose policy based on existing finalizers.
		var policy metav1.DeletionPropagation
		switch {
		case hasOrphanFinalizer(latest):
			policy = metav1.DeletePropagationOrphan
		case hasDeleteDependentsFinalizer(latest):
			policy = metav1.DeletePropagationForeground
		default:
			policy = metav1.DeletePropagationBackground
		}
		logger.V(2).Info("Deleting item", "item", item.identity, "propagationPolicy", policy)
		return gc.deleteObject(item.identity, latest.ResourceVersion, latest.OwnerReferences, &policy)
	}
}

// process item that's waiting for its dependents to be deleted
func (gc *GarbageCollector) processDeletingDependentsItem(logger klog.Logger, item *node) error {
	blockingDependents := item.blockingDependents()
	if len(blockingDependents) == 0 {
		logger.V(2).Info("remove DeleteDependents finalizer for item", "item", item.identity)
		return gc.removeFinalizer(logger, item, metav1.FinalizerDeleteDependents)
	}
	for _, dep := range blockingDependents {
		if !dep.isDeletingDependents() {
			logger.V(2).Info("adding dependent to attemptToDelete, because its owner is deletingDependents",
				"item", item.identity,
				"dependent", dep.identity,
			)
			gc.attemptToDelete.Add(dep)
		}
	}
	return nil
}

// dependents are copies of pointers to the owner's dependents, they don't need to be locked.
func (gc *GarbageCollector) orphanDependents(logger klog.Logger, owner objectReference, dependents []*node) error {
	errCh := make(chan error, len(dependents))
	wg := sync.WaitGroup{}
	wg.Add(len(dependents))
	for i := range dependents {
		go func(dependent *node) {
			defer wg.Done()
			// the dependent.identity.UID is used as precondition
			p, err := c.GenerateDeleteOwnerRefStrategicMergeBytes(dependent.identity.UID, []types.UID{owner.UID})
			if err != nil {
				errCh <- fmt.Errorf("orphaning %s failed, %v", dependent.identity, err)
				return
			}
			_, err = gc.patch(dependent, p, func(n *node) ([]byte, error) {
				return gc.deleteOwnerRefJSONMergePatch(n, owner.UID)
			})
			// note that if the target ownerReference doesn't exist in the
			// dependent, strategic merge patch will NOT return an error.
			if err != nil && !errors.IsNotFound(err) {
				errCh <- fmt.Errorf("orphaning %s failed, %v", dependent.identity, err)
			}
		}(dependents[i])
	}
	wg.Wait()
	close(errCh)

	var errorsSlice []error
	for e := range errCh {
		errorsSlice = append(errorsSlice, e)
	}

	if len(errorsSlice) != 0 {
		return fmt.Errorf("failed to orphan dependents of owner %s, got errors: %s", owner, utilerrors.NewAggregate(errorsSlice).Error())
	}
	logger.V(5).Info("successfully updated all dependents", "owner", owner)
	return nil
}

func (gc *GarbageCollector) runAttemptToOrphanWorker(logger klog.Logger) {
	for gc.processAttemptToOrphanWorker(logger) {
	}
}

// processAttemptToOrphanWorker dequeues a node from the attemptToOrphan, then finds its
// dependents based on the graph maintained by the GC, then removes it from the
// OwnerReferences of its dependents, and finally updates the owner to remove
// the "Orphan" finalizer. The node is added back into the attemptToOrphan if any of
// these steps fail.
func (gc *GarbageCollector) processAttemptToOrphanWorker(logger klog.Logger) bool {
	item, quit := gc.attemptToOrphan.Get()
	if quit {
		return false
	}
	defer gc.attemptToOrphan.Done(item)

	action := gc.attemptToOrphanWorker(logger, item)
	switch action {
	case forgetItem:
		gc.attemptToOrphan.Forget(item)
	case requeueItem:
		gc.attemptToOrphan.AddRateLimited(item)
	}

	return true
}

func (gc *GarbageCollector) attemptToOrphanWorker(logger klog.Logger, item interface{}) workQueueItemAction {
	owner, ok := item.(*node)
	if !ok {
		utilruntime.HandleError(fmt.Errorf("expect *node, got %#v", item))
		return forgetItem
	}
	// we don't need to lock each element, because they never get updated
	owner.dependentsLock.RLock()
	dependents := make([]*node, 0, len(owner.dependents))
	for dependent := range owner.dependents {
		dependents = append(dependents, dependent)
	}
	owner.dependentsLock.RUnlock()

	err := gc.orphanDependents(logger, owner.identity, dependents)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("orphanDependents for %s failed with %v", owner.identity, err))
		return requeueItem
	}
	// update the owner, remove "orphaningFinalizer" from its finalizers list
	err = gc.removeFinalizer(logger, owner, metav1.FinalizerOrphanDependents)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("removeOrphanFinalizer for %s failed with %v", owner.identity, err))
		return requeueItem
	}
	return forgetItem
}

// *FOR TEST USE ONLY*
// GraphHasUID returns if the GraphBuilder has a particular UID store in its
// uidToNode graph. It's useful for debugging.
// This method is used by integration tests.
func (gc *GarbageCollector) GraphHasUID(u types.UID) bool {
	for _, gb := range gc.dependencyGraphBuilders {
		if _, ok := gb.uidToNode.Read(u); ok {
			return true
		}
	}
	return false
}

// GetDeletableResources returns all resources from discoveryClient that the
// garbage collector should recognize and work with. More specifically, all
// preferred resources which support the 'delete', 'list', and 'watch' verbs.
//
// If an error was encountered fetching resources from the server,
// it is included as well, along with any resources that were successfully resolved.
//
// All discovery errors are considered temporary. Upon encountering any error,
// GetDeletableResources will log and return any discovered resources it was
// able to process (which may be none).
func GetDeletableResources(logger klog.Logger, discoveryClient discovery.ServerResourcesInterface) (map[schema.GroupVersionResource]struct{}, error) {
	preferredResources, lookupErr := discoveryClient.ServerPreferredResources()
	if lookupErr != nil {
		if groupLookupFailures, isLookupFailure := discovery.GroupDiscoveryFailedErrorGroups(lookupErr); isLookupFailure {
			// Serialize groupLookupFailures here as map[schema.GroupVersion]error is not json encodable, otherwise the
			// logger would throw internal error.
			logger.Info("failed to discover some groups", "groups", fmt.Sprintf("%q", groupLookupFailures))
		} else {
			logger.Info("failed to discover preferred resources", "error", lookupErr)
		}
	}
	if preferredResources == nil {
		return map[schema.GroupVersionResource]struct{}{}, lookupErr
	}

	// This is extracted from discovery.GroupVersionResources to allow tolerating
	// failures on a per-resource basis.
	deletableResources := discovery.FilteredBy(discovery.SupportsAllVerbs{Verbs: []string{"delete", "list", "watch"}}, preferredResources)
	deletableGroupVersionResources := map[schema.GroupVersionResource]struct{}{}
	for _, rl := range deletableResources {
		gv, err := schema.ParseGroupVersion(rl.GroupVersion)
		if err != nil {
			logger.Info("ignoring invalid discovered resource", "groupversion", rl.GroupVersion, "error", err)
			continue
		}
		for i := range rl.APIResources {
			deletableGroupVersionResources[schema.GroupVersionResource{Group: gv.Group, Version: gv.Version, Resource: rl.APIResources[i].Name}] = struct{}{}
		}
	}

	return deletableGroupVersionResources, lookupErr
}

func (gc *GarbageCollector) Name() string {
	return "garbagecollector"
}

// GetDependencyGraphBuilder return graph builder which is particularly helpful for testing where controllerContext is not available
func (gc *GarbageCollector) GetDependencyGraphBuilder() *GraphBuilder {
	if len(gc.dependencyGraphBuilders) > 0 {
		return gc.dependencyGraphBuilders[0]
	}
	return nil
}
