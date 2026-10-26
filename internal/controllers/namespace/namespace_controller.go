/*
Copyright 2015 The Kubernetes Authors.

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

package namespace

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/time/rate"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	informers "k8s.io/client-go/informers"
	coreinformers "k8s.io/client-go/informers/core/v1"
	clientset "k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/kubernetes/pkg/controller"
	"k8s.io/kubernetes/pkg/controller/namespace/deletion"

	"k8s.io/klog/v2"
)

const (
	// namespaceDeletionGracePeriod is the time period to wait before processing a received namespace event.
	// This allows time for the following to occur:
	// * lifecycle admission plugins on HA apiservers to also observe a namespace
	//   deletion and prevent new objects from being created in the terminating namespace
	// * non-leader etcd servers to observe last-minute object creations in a namespace
	//   so this controller's cleanup can actually clean up all objects
	namespaceDeletionGracePeriod = 5 * time.Second
)

// nsKey identifies a namespace in the controller's work queue by its cluster and name.
type nsKey struct {
	Cluster string // "root" or project ID
	Name    string // namespace name
}

// NamespaceController is responsible for performing actions dependent upon a namespace phase
type NamespaceController struct {
	mu sync.RWMutex

	// listers that can list namespaces from a specific cluster cache
	listers map[string]corelisters.NamespaceLister
	// returns true when a clusters namespace cache is ready
	listersSynced map[string]cache.InformerSynced
	// namespaces that have been queued up for processing by workers
	queue workqueue.TypedRateLimitingInterface[nsKey]
	// helper to delete all resources in the namespace when the cluster namespace is deleted.
	deleters map[string]deletion.NamespacedResourcesDeleterInterface

	cancels map[string]context.CancelFunc
}

func (nm *NamespaceController) AddProject(
	parent context.Context,
	project string,
	kube clientset.Interface,
	md metadata.Interface,
	discover func() ([]*metav1.APIResourceList, error),
	resync time.Duration,
	finalizer v1.FinalizerName,
	syncTimeout time.Duration,
) error {
	// klog

	klog.FromContext(parent).Info("Adding project", "project", project)
	// Build project-scoped deleter
	del := deletion.NewNamespacedResourcesDeleter(
		parent, kube.CoreV1().Namespaces(), md, kube.CoreV1(), discover, finalizer)

	// Build project-scoped informer
	factory := informers.NewSharedInformerFactory(kube, resync)
	nsInf := factory.Core().V1().Namespaces()

	// Enqueue terminating namespaces for this project
	nsInf.Informer().AddEventHandlerWithResyncPeriod(
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(o interface{}) {
				nm.enqueueNamespace(project, o)
			},
			UpdateFunc: func(_, newObj interface{}) {
				nm.enqueueNamespace(project, newObj)
			},
			DeleteFunc: func(o interface{}) { nm.enqueueNamespace(project, o) },
		},
		resync,
	)

	// Register state
	nm.mu.Lock()
	if nm.cancels == nil {
		nm.cancels = make(map[string]context.CancelFunc)
	}
	nm.listers[project] = nsInf.Lister()
	nm.listersSynced[project] = nsInf.Informer().HasSynced
	nm.deleters[project] = del
	nm.mu.Unlock()

	// Start and block until this project's cache is ready
	ctx, cancel := context.WithCancel(parent)
	nm.mu.Lock()
	nm.cancels[project] = cancel
	nm.mu.Unlock()

	go factory.Start(ctx.Done())
	// The wait is bounded: a Project's engagement must not hold back the others for a cache that never syncs.
	syncCtx, cancelSync := context.WithTimeout(ctx, syncTimeout)
	defer cancelSync()
	if !cache.WaitForNamedCacheSync("namespace-"+project, syncCtx.Done(), nsInf.Informer().HasSynced) {
		// failed to sync; clean up the partial registration
		nm.RemoveProject(project)
		return fmt.Errorf("namespace cache of project %s did not sync: %w", project, syncCtx.Err())
	}
	return nil
}

func (nm *NamespaceController) RemoveProject(project string) {
	nm.mu.Lock()
	if cancel, ok := nm.cancels[project]; ok {
		cancel()
		delete(nm.cancels, project)
	}
	delete(nm.listers, project)
	delete(nm.listersSynced, project)
	delete(nm.deleters, project)
	nm.mu.Unlock()
	// We don’t try to purge queued items; workers will skip keys whose project is gone.
}

// NewNamespaceController creates a multi-cluster-aware controller and
// wires the passed informer/client as the initial "root" cluster.
// If you want a different name, replace "root" with your desired ID.
func NewNamespaceController(
	ctx context.Context,
	kubeClient clientset.Interface,
	metadataClient metadata.Interface,
	discoverResourcesFn func() ([]*metav1.APIResourceList, error),
	namespaceInformer coreinformers.NamespaceInformer,
	resyncPeriod time.Duration,
	finalizerToken v1.FinalizerName,
) *NamespaceController {

	nm := &NamespaceController{
		listers:       make(map[string]corelisters.NamespaceLister),
		listersSynced: make(map[string]cache.InformerSynced),
		deleters:      make(map[string]deletion.NamespacedResourcesDeleterInterface),
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			nsControllerRateLimiter(),
			workqueue.TypedRateLimitingQueueConfig[nsKey]{
				Name: "namespace",
			},
		),
	}

	const cluster = "root" // change if you prefer another name

	// Same deleter wiring as upstream, but stored per cluster.
	nm.deleters[cluster] = deletion.NewNamespacedResourcesDeleter(
		ctx,
		kubeClient.CoreV1().Namespaces(),
		metadataClient,
		kubeClient.CoreV1(),
		discoverResourcesFn,
		finalizerToken,
	)

	// Same event handlers as upstream; enqueue with (cluster,name).
	inf := namespaceInformer.Informer()
	inf.AddEventHandlerWithResyncPeriod(
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				nm.enqueueNamespace(cluster, obj)
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				nm.enqueueNamespace(cluster, newObj)
			},
		},
		resyncPeriod,
	)

	// Store the lister and HasSynced for this cluster.
	nm.listers[cluster] = namespaceInformer.Lister()
	nm.listersSynced[cluster] = inf.HasSynced

	return nm
}

// nsControllerRateLimiter is tuned for a faster than normal recycle time with default backoff speed and default overall
// requeing speed.  We do this so that namespace cleanup is reliably faster and we know that the number of namespaces being
// deleted is smaller than total number of other namespace scoped resources in a cluster.
func nsControllerRateLimiter() workqueue.TypedRateLimiter[nsKey] {
	return workqueue.NewTypedMaxOfRateLimiter(
		// this ensures that we retry namespace deletion at least every minute, never longer.
		workqueue.NewTypedItemExponentialFailureRateLimiter[nsKey](5*time.Millisecond, 60*time.Second),
		// 10 qps, 100 bucket size.  This is only for retry speed and its only the overall factor (not per item)
		&workqueue.TypedBucketRateLimiter[nsKey]{Limiter: rate.NewLimiter(rate.Limit(10), 100)},
	)
}

// enqueueNamespace adds an object to the controller work queue
// obj could be an *v1.Namespace, or a DeletionFinalStateUnknown item.
func (nm *NamespaceController) enqueueNamespace(cluster string, obj interface{}) {
	key, err := controller.KeyFunc(obj)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("Couldn't get key for object %+v: %v", obj, err))
		return
	}

	// log enqueued namespace (either created or updated)
	klog.FromContext(context.TODO()).V(4).Info("Enqueuing namespace", "namespace", key, "cluster", cluster)

	namespace := obj.(*v1.Namespace)
	// don't queue if we aren't deleted
	if namespace.DeletionTimestamp == nil || namespace.DeletionTimestamp.IsZero() {
		return
	}

	// log namespace deletion
	klog.FromContext(context.TODO()).V(4).Info("Enqueuing namespace for deletion", "namespace", key, "cluster", cluster)

	// delay processing namespace events to allow HA api servers to observe namespace deletion,
	// and HA etcd servers to observe last minute object creations inside the namespace
	nm.queue.AddAfter(nsKey{Cluster: cluster, Name: key}, namespaceDeletionGracePeriod)
}

// worker processes the queue of namespace objects.
// Each namespace can be in the queue at most once.
// The system ensures that no two workers can process
// the same namespace at the same time.
func (nm *NamespaceController) worker(ctx context.Context) {
	// log worker start per cluster

	workFunc := func(ctx context.Context) bool {
		key, quit := nm.queue.Get()
		if quit {
			return true
		}
		// log
		klog.FromContext(ctx).V(4).Info("Processing namespace", "namespace", key)
		defer nm.queue.Done(key)

		err := nm.syncNamespaceFromKey(ctx, key)
		if err == nil {
			// no error, forget this entry and return
			nm.queue.Forget(key)
			return false
		}

		if estimate, ok := err.(*deletion.ResourcesRemainingError); ok {
			t := estimate.Estimate/2 + 1
			klog.FromContext(ctx).V(4).Info("Content remaining in namespace", "namespace", key, "waitSeconds", t)
			nm.queue.AddAfter(key, time.Duration(t)*time.Second)
		} else {
			// rather than wait for a full resync, re-add the namespace to the queue to be processed
			nm.queue.AddRateLimited(key)
			utilruntime.HandleError(fmt.Errorf("deletion of namespace %v failed: %v", key, err))
		}
		return false
	}
	for {
		quit := workFunc(ctx)

		if quit {
			return
		}
	}
}

// syncNamespaceFromKey looks for a namespace with the specified key in its store and synchronizes it
func (nm *NamespaceController) syncNamespaceFromKey(ctx context.Context, key nsKey) (err error) {
	startTime := time.Now()
	logger := klog.FromContext(ctx)
	defer func() {
		logger.V(4).Info("Finished syncing namespace", "namespace", key, "duration", time.Since(startTime))
	}()

	lister := nm.listers[key.Cluster]
	deleter := nm.deleters[key.Cluster]
	if lister == nil || deleter == nil {
		// The project was unregistered (e.g., after purge). Nothing left to do.
		logger.V(3).Info("Dropping key for unregistered cluster", "cluster", key.Cluster, "namespace", key.Name)
		return nil // <- important: no error => worker will Forget() and not requeue
	}

	namespace, err := lister.Get(key.Name)
	if errors.IsNotFound(err) {
		logger.Info("Namespace has been deleted", "namespace", key)
		return nil
	}
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("Unable to retrieve namespace %v from store: %v", key, err))
		return err
	}
	logger.Info("Calling deleter", "cluster", key.Cluster, "namespace", key.Name)

	return deleter.Delete(ctx, namespace.Name)
}

// Run starts observing the system with the specified number of workers.
// NOTE: this multi-cluster version waits for the caches of all clusters that
// are registered at start. If you add clusters dynamically later, ensure
// AddCluster waits for its informer(s) to sync before they begin enqueueing.
func (nm *NamespaceController) Run(ctx context.Context, workers int) {
	defer utilruntime.HandleCrash()
	defer nm.queue.ShutDown()

	logger := klog.FromContext(ctx)
	logger.Info("Starting multi-cluster namespace controller")
	defer logger.Info("Shutting down multi-cluster namespace controller")

	// Wait until at least one cluster has been registered (root or a project).
	for {
		nm.mu.RLock()
		hasAny := len(nm.listersSynced) > 0
		nm.mu.RUnlock()
		if hasAny {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(200 * time.Millisecond):
		}
	}

	// Snapshot the HasSynced funcs for all currently-registered clusters and wait.
	nm.mu.RLock()
	fns := make([]cache.InformerSynced, 0, len(nm.listersSynced))
	for _, fn := range nm.listersSynced {
		fns = append(fns, fn)
	}
	nm.mu.RUnlock()

	if !cache.WaitForNamedCacheSync("namespace", ctx.Done(), fns...) {
		return
	}

	logger.V(5).Info("Starting workers of namespace controller", "workers", workers)
	for i := 0; i < workers; i++ {
		go wait.UntilWithContext(ctx, nm.worker, time.Second)
	}

	<-ctx.Done()
}
