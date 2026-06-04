package projectstorage

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/klog/v2"
	"k8s.io/utils/clock"
)

const (
	watchRetryInitial = time.Second
	watchRetryCap     = 30 * time.Second
	watchRetryReset   = 2 * time.Minute
)

const (
	// reconcileInterval is how often the reconciler sweeps tenantMap for
	// entries whose UIDs no longer appear in the cacher's store. Also the
	// upper bound on staleness if the Watch fast path is broken.
	reconcileInterval = 5 * time.Minute

	// reconcileGrace is the minimum age of a tenantMap entry before the
	// reconciler will consider pruning it. Protects against the race where
	// an object has just been decoded (record) but has not yet entered the
	// cacher's btree (still in DeltaFIFO).
	reconcileGrace = 30 * time.Second
)

// deletionTracker keeps tenantMap aligned with the cacher's live set via two
// independent goroutines:
//
//  1. observeDeletions: best-effort fast path. Subscribes to the cacher's
//     Watch, forgets UIDs on Delete events. On any error or close, retries
//     with backoff. No RV tracking — re-receiving initial events on reconnect
//     is cheap relative to the rarity of cacher Watch failures, and dropping
//     RV state eliminates the ResourceExpired loop.
//
//  2. reconcileLoop: authoritative backstop. Every reconcileInterval, lists
//     the cacher's contents and prunes tenantMap entries that aren't in the
//     live set and were recorded before reconcileGrace ago. Catches anything
//     observeDeletions missed (reconnect gaps, raw-etcd Gets that never
//     landed in the cache).
//
// Cleanup is bound to the natural lifecycle: the cacher dispatches Delete
// events to subscribers only after watchCache.Delete has completed, so by
// the time observeDeletions sees a Delete the UID will no longer be looked
// up by keyFunc.
type deletionTracker struct {
	inner          storage.Interface
	resourcePrefix string
	gr             schema.GroupResource
	tm             *tenantMap
	newListFunc    func() runtime.Object
}

func startDeletionTracker(
	inner storage.Interface,
	resourcePrefix string,
	gr schema.GroupResource,
	tm *tenantMap,
	newListFunc func() runtime.Object,
) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	t := &deletionTracker{
		inner:          inner,
		resourcePrefix: resourcePrefix,
		gr:             gr,
		tm:             tm,
		newListFunc:    newListFunc,
	}
	go t.observeDeletions(ctx)
	go t.reconcileLoop(ctx)
	return cancel
}

func (t *deletionTracker) observeDeletions(ctx context.Context) {
	delayFn := wait.Backoff{
		Duration: watchRetryInitial,
		Cap:      watchRetryCap,
		Steps:    30,
		Factor:   2.0,
		Jitter:   0.1,
	}.DelayWithReset(clock.RealClock{}, watchRetryReset)
	_ = delayFn.Until(ctx, true, true, func(ctx context.Context) (bool, error) {
		w, err := t.inner.Watch(ctx, t.resourcePrefix, storage.ListOptions{
			Predicate: storage.Everything,
			Recursive: true,
		})
		if err != nil {
			klog.V(4).InfoS("tenant-tracker: watch failed, will retry",
				"group", t.gr.Group, "resource", t.gr.Resource, "err", err)
			return false, nil
		}
		t.drainWatch(ctx, w)
		return false, nil
	})
}

func (t *deletionTracker) drainWatch(ctx context.Context, w watch.Interface) {
	defer w.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.ResultChan():
			if !ok {
				return
			}
			if ev.Type != watch.Deleted || ev.Object == nil {
				continue
			}
			if accessor, err := meta.Accessor(ev.Object); err == nil {
				t.tm.forget(accessor.GetUID())
			}
		}
	}
}

func (t *deletionTracker) reconcileLoop(ctx context.Context) {
	// First reconcile fires immediately — safe because reconcileGrace protects
	// recent entries from being pruned. Jitter spreads subsequent ticks across
	// per-resource reconcilers instead of aligning them.
	wait.JitterUntilWithContext(ctx, t.reconcile, reconcileInterval, 0.1, false)
}

func (t *deletionTracker) reconcile(ctx context.Context) {
	if t.newListFunc == nil {
		return
	}
	cutoff := time.Now().Add(-reconcileGrace)
	listObj := t.newListFunc()
	err := t.inner.GetList(ctx, t.resourcePrefix, storage.ListOptions{
		Predicate:       storage.Everything,
		Recursive:       true,
		ResourceVersion: "0", // serve from cache, no etcd round-trip
	}, listObj)
	if err != nil {
		klog.V(4).InfoS("tenant-tracker: reconcile list failed",
			"group", t.gr.Group, "resource", t.gr.Resource, "err", err)
		return
	}
	items, err := meta.ExtractList(listObj)
	if err != nil {
		return
	}
	live := make(map[types.UID]struct{}, len(items))
	for _, item := range items {
		if accessor, err := meta.Accessor(item); err == nil {
			live[accessor.GetUID()] = struct{}{}
		}
	}
	t.tm.retainPredicate(func(uid types.UID, entry tenantEntry) bool {
		_, isLive := live[uid]
		return isLive || entry.recordedAt.After(cutoff)
	})
}
