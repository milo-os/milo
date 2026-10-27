package projectprovider

import (
	"context"
	"sync"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
)

// engagement is the sink's state for one Project name: the UID it was engaged
// under, and the function that ends that engagement's context.
type engagement struct {
	uid    types.UID
	cancel context.CancelFunc
}

// engagements keeps a sink engaged with the Projects an informer's store holds.
// The informer's handlers only queue a Project's name, and one worker brings the
// sink to what the store holds for it, so an engagement that waits never holds
// back another Project's events, and an add and a remove for one name cannot
// pass each other.
type engagements struct {
	ctx   context.Context
	p     *Provider
	store cache.Store
	queue workqueue.TypedRateLimitingInterface[string]

	mu      sync.Mutex
	current map[string]engagement // engaged, or being engaged
}

func newEngagements(ctx context.Context, p *Provider, store cache.Store) *engagements {
	return &engagements{
		ctx:     ctx,
		p:       p,
		store:   store,
		queue:   workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[string]()),
		current: map[string]engagement{},
	}
}

// handlers queues the name of every Project the informer reports. A delete also
// ends the wait of that Project's engagement in progress, so a sink waiting on
// the cache of a Project that has gone returns at once.
func (e *engagements) handlers() cache.ResourceEventHandlerFuncs {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    e.enqueue,
		UpdateFunc: func(_, obj interface{}) { e.enqueue(obj) },
		DeleteFunc: func(obj interface{}) {
			e.cancelDeleted(obj)
			e.enqueue(obj)
		},
	}
}

// enqueue queues obj's name; cache.DeletionHandlingMetaNamespaceKeyFunc reads a
// tombstone's name too.
func (e *engagements) enqueue(obj interface{}) {
	name, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		klog.ErrorS(err, "Failed to read a Project's name from an informer event")
		return
	}
	e.queue.Add(name)
}

// cancelDeleted ends the context of the engagement of the deleted Project, and
// of no other Project under its name.
func (e *engagements) cancelDeleted(obj interface{}) {
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tombstone.Obj
	}
	m, err := meta.Accessor(obj)
	if err != nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if cur, ok := e.current[m.GetName()]; ok && cur.uid == m.GetUID() {
		cur.cancel()
	}
}

// run reconciles queued names until the provider's context ends.
func (e *engagements) run() {
	go func() {
		<-e.ctx.Done()
		e.queue.ShutDown()
	}()
	for e.next() {
	}
}

// next reconciles one queued name, and queues it again after a delay when the
// sink failed to engage a Project that still exists.
func (e *engagements) next() bool {
	name, quit := e.queue.Get()
	if quit {
		return false
	}
	defer e.queue.Done(name)
	if err := e.reconcile(name); err != nil {
		klog.ErrorS(err, "Failed to engage a project; retrying", "project", name)
		e.queue.AddRateLimited(name)
		return true
	}
	e.queue.Forget(name)
	return true
}

// reconcile removes the engagement of a name the store no longer holds, or holds
// under another UID, and engages the Project the store holds.
func (e *engagements) reconcile(name string) error {
	uid, exists := e.stored(name)
	cur, engaged := e.lookup(name)
	if engaged && (!exists || cur.uid != uid) {
		e.disengage(name)
		engaged = false
	}
	if !exists || engaged {
		return nil
	}
	return e.engage(name, uid)
}

// stored returns the UID of the Project the store holds under name.
func (e *engagements) stored(name string) (types.UID, bool) {
	obj, exists, err := e.store.GetByKey(name)
	if err != nil || !exists {
		return "", false
	}
	m, err := meta.Accessor(obj)
	if err != nil {
		return "", false
	}
	return m.GetUID(), true
}

func (e *engagements) lookup(name string) (engagement, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	cur, ok := e.current[name]
	return cur, ok
}

// engage adds the Project to the sink under a context its delete can end. A
// failed add is removed, so the sink keeps no part of it. An add whose context
// was ended, by the Project's delete or the provider's stop, is not retried.
func (e *engagements) engage(name string, uid types.UID) error {
	ctx, cancel := context.WithCancel(e.ctx)
	e.mu.Lock()
	e.current[name] = engagement{uid: uid, cancel: cancel}
	e.mu.Unlock()
	err := e.p.sink.AddProject(ctx, name, e.p.cfgForProject(name))
	if err == nil {
		return nil
	}
	ended := ctx.Err() != nil // read before disengage ends ctx itself
	e.disengage(name)
	if ended {
		return nil
	}
	return err
}

// disengage removes the name's engagement from the sink and ends its context.
func (e *engagements) disengage(name string) {
	e.p.sink.RemoveProject(name)
	e.mu.Lock()
	defer e.mu.Unlock()
	if cur, ok := e.current[name]; ok {
		cur.cancel()
		delete(e.current, name)
	}
}
