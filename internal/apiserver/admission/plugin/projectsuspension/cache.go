package projectsuspension

import (
	"context"
	"sync"
	"time"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
)

const defaultResyncPeriod = 10 * time.Minute

// ProjectSuspensionCache maintains an in-memory, real-time cache of projects
// backed by a dynamic SharedIndexInformer store.
type ProjectSuspensionCache struct {
	dynamicClient dynamic.Interface
	informer      cache.SharedIndexInformer
	stopCh        chan struct{}
	startOnce     sync.Once
	logger        logr.Logger
}

// NewProjectSuspensionCache creates and configures a new ProjectSuspensionCache.
func NewProjectSuspensionCache(dynamicClient dynamic.Interface, logger logr.Logger) *ProjectSuspensionCache {
	c := &ProjectSuspensionCache{
		dynamicClient: dynamicClient,
		stopCh:        make(chan struct{}),
		logger:        logger.WithName("project-suspension-cache"),
	}

	lw := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			options.SendInitialEvents = nil
			return c.dynamicClient.Resource(projectGVR).List(context.TODO(), options)
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			options.SendInitialEvents = nil
			return c.dynamicClient.Resource(projectGVR).Watch(context.TODO(), options)
		},
	}

	c.informer = cache.NewSharedIndexInformer(
		lw,
		&unstructured.Unstructured{},
		defaultResyncPeriod,
		cache.Indexers{},
	)

	_, _ = c.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{})

	return c
}

// Start begins the shared informer worker loops.
func (c *ProjectSuspensionCache) Start() {
	c.startOnce.Do(func() {
		go c.informer.Run(c.stopCh)
	})
}

// HasSynced reports whether the underlying informer has completed its initial list/sync.
func (c *ProjectSuspensionCache) HasSynced() bool {
	return c.informer.HasSynced()
}

// Stop shuts down the informer.
func (c *ProjectSuspensionCache) Stop() {
	select {
	case <-c.stopCh:
	default:
		close(c.stopCh)
	}
}

// GetSuspensionState returns the suspension state for the given projectID.
// If the item exists in the informer cache, it performs a fast in-memory store lookup.
// If the item is absent and the cache isSynced, it returns nil (not suspended).
// If the item is absent and the cache is not yet synced, it falls back to a live Get.
func (c *ProjectSuspensionCache) GetSuspensionState(ctx context.Context, projectID string) (*suspensionState, error) {
	item, exists, err := c.informer.GetStore().GetByKey(projectID)
	if err == nil && exists {
		if unstr, ok := item.(*unstructured.Unstructured); ok {
			return parseSuspensionState(unstr), nil
		}
	}

	if c.informer.HasSynced() {
		// Store is synced and projectID is not present -> project is not suspended (or doesn't exist)
		return nil, nil
	}

	// Defensive fallback during initial boot before informer cache sync completes
	c.logger.V(2).Info("Informer not yet synced; executing live fallback Get", "projectID", projectID)
	obj, err := c.dynamicClient.Resource(projectGVR).Get(ctx, projectID, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		return nil, nil
	case err != nil:
		c.logger.V(2).Info("failed to get project for suspension check, failing open",
			"projectID", projectID, "error", err)
		return nil, nil
	}

	return parseSuspensionState(obj), nil
}
