package cockroach

import (
	"context"
	"errors"
	"fmt"
	"math"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/features"
	"k8s.io/apiserver/pkg/storage"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	utilflowcontrol "k8s.io/apiserver/pkg/util/flowcontrol"
)

// outgoingBuffer is how many decoded events wait for the client. The hub's
// per-watcher buffer sits in front of it, so it only smooths delivery.
const outgoingBuffer = 32

// Watch follows the semantics of etcd3's watcher. A watch from revision 0, or
// one that asks for initial events, first lists the current state as ADDED
// events and then streams the changes after it.
func (s *store) Watch(ctx context.Context, key string, opts storage.ListOptions) (watch.Interface, error) {
	preparedKey, err := s.prepareKey(key, opts.Recursive)
	if err != nil {
		return nil, err
	}
	rev, err := s.versioner.ParseResourceVersion(opts.ResourceVersion)
	if err != nil {
		return nil, err
	}
	if rev > math.MaxInt64 {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("invalid resource version %q: larger than any revision", opts.ResourceVersion))
	}
	initialEvents := areInitialEventsRequired(int64(rev), opts)
	from := int64(rev)
	if from == 0 && !initialEvents {
		// A stream of changes from the most recent revision.
		current, err := s.GetCurrentResourceVersion(ctx)
		if err != nil {
			return nil, err
		}
		from = int64(current)
	}
	if s.alive.Err() != nil {
		return nil, storage.NewInternalError(errors.New("the storage was removed"))
	}
	pred := opts.Predicate
	pred.Limit, pred.Continue = 0, ""
	if pred.Empty() {
		pred = storage.Everything
	}
	w := newWatcher(ctx, s, watchTarget{
		project:        s.project,
		key:            preparedKey,
		recursive:      opts.Recursive,
		allowBookmarks: opts.Predicate.AllowWatchBookmarks,
	}, pred)
	go w.run(from, initialEvents, isInitialEventsEndBookmarkRequired(opts))

	// As with etcd3, the hub cannot tell cheaply when a watch has caught up, so
	// the initialization signal goes out right away.
	utilflowcontrol.WatchInitialized(ctx)
	return w, nil
}

// areInitialEventsRequired reports whether a watch lists first, as etcd3
// decides it. A legacy watch from 0 lists first, and so does any watch that
// asks for initial events.
func areInitialEventsRequired(rev int64, opts storage.ListOptions) bool {
	if opts.SendInitialEvents == nil && rev == 0 {
		return true
	}
	if !utilfeature.DefaultFeatureGate.Enabled(features.WatchList) {
		return false
	}
	return opts.SendInitialEvents != nil && *opts.SendInitialEvents
}

// isInitialEventsEndBookmarkRequired reports whether a watch gets the
// bookmark that ends the initial events, as etcd3 decides it. Only watches
// that asked for both get one.
func isInitialEventsEndBookmarkRequired(opts storage.ListOptions) bool {
	if !utilfeature.DefaultFeatureGate.Enabled(features.WatchList) {
		return false
	}
	return opts.SendInitialEvents != nil && *opts.SendInitialEvents && opts.Predicate.AllowWatchBookmarks
}

// watcher is one watch. run turns the events of its subscription to the hub
// into watch events on result.
type watcher struct {
	s               *store
	target          watchTarget
	pred            storage.SelectionPredicate
	matchesAll      bool
	result          chan watch.Event
	ctx             context.Context
	cancel          context.CancelFunc
	detachFromStore func() bool
}

func newWatcher(ctx context.Context, s *store, target watchTarget, pred storage.SelectionPredicate) *watcher {
	w := &watcher{s: s, target: target, pred: pred, matchesAll: pred.Empty(), result: make(chan watch.Event, outgoingBuffer)}
	w.ctx, w.cancel = context.WithCancel(ctx)
	// Destroying the store ends its watches, as stopping etcd3's watch cache
	// does.
	w.detachFromStore = context.AfterFunc(s.alive, w.cancel)
	return w
}

var _ watch.Interface = (*watcher)(nil)

func (w *watcher) Stop()                          { w.cancel() }
func (w *watcher) ResultChan() <-chan watch.Event { return w.result }

func (w *watcher) run(from int64, initialEvents, endBookmark bool) {
	defer close(w.result)
	defer w.cancel()
	defer w.detachFromStore()
	hub := w.s.b.hub

	var items []runtime.Object
	if initialEvents {
		if from > 0 {
			current, err := w.s.GetCurrentResourceVersion(w.ctx)
			if err != nil {
				w.sendError(err)
				return
			}
			if uint64(from) > current {
				w.sendError(storage.NewTooLargeResourceVersionError(uint64(from), current, 1))
				return
			}
		}
		res, err := w.list()
		if err != nil {
			w.sendError(err)
			return
		}
		items, from = res.items, res.rev
	}
	released, sub, err := hub.subscribe(w.ctx, w.target, from)
	if err != nil {
		w.sendError(err)
		return
	}
	defer hub.unsubscribe(sub)
	for _, obj := range items {
		if !w.send(watch.Event{Type: watch.Added, Object: obj}) {
			return
		}
	}
	if endBookmark {
		obj, err := w.bookmark(from)
		if err == nil {
			err = storage.AnnotateInitialEventsEndBookmark(obj)
		}
		if err != nil {
			w.sendError(err)
			return
		}
		if !w.send(watch.Event{Type: watch.Bookmark, Object: obj}) {
			return
		}
	}
	for _, e := range released {
		if !w.process(e) {
			return
		}
	}
	for {
		e, err := sub.next(w.ctx)
		if err != nil {
			if w.ctx.Err() == nil && !errors.Is(err, errFellBehind) {
				w.sendError(err)
			}
			return
		}
		if !w.process(e) {
			return
		}
	}
}

// list reads the current state for the initial events.
func (w *watcher) list() (listResult, error) {
	return w.s.lister.list(w.ctx, listQuery{prefix: w.target.key, recursive: w.target.recursive, pred: w.pred, newItem: w.s.newFunc})
}

// process sends e if it passes the watch's filter, and reports whether the
// watch is still running.
func (w *watcher) process(e *feedEvent) bool {
	if e.op == watch.Bookmark {
		obj, err := w.bookmark(e.rev)
		if err != nil {
			w.sendError(err)
			return false
		}
		return w.send(watch.Event{Type: watch.Bookmark, Object: obj})
	}
	ev, err := w.transform(e)
	if err != nil {
		w.sendError(err)
		return false
	}
	if ev.Type == "" {
		return true
	}
	return w.send(ev)
}

// transform turns a write into the event this watch sees, as etcd3 does. An
// update that moves an object into or out of the filter becomes ADDED or
// DELETED, and an event the watch doesn't see has no type.
func (w *watcher) transform(e *feedEvent) (watch.Event, error) {
	var cur, old runtime.Object
	var err error
	if e.op != watch.Deleted {
		if cur, err = w.decode(e.key, e.value, e.rev); err != nil {
			return watch.Event{}, err
		}
	}
	// A delete of a key that wasn't live has nothing to decode, and no event.
	needOld := e.op == watch.Deleted || e.op == watch.Modified && !w.matchesAll
	if needOld && e.prevValue != nil {
		// The old object carries the revision of the write that replaced it.
		if old, err = w.decode(e.key, e.prevValue, e.rev); err != nil {
			return watch.Event{}, err
		}
	}
	switch {
	case e.op == watch.Deleted:
		if old == nil || !w.filter(old) {
			return watch.Event{}, nil
		}
		return watch.Event{Type: watch.Deleted, Object: old}, nil
	case e.op == watch.Added:
		if !w.filter(cur) {
			return watch.Event{}, nil
		}
		return watch.Event{Type: watch.Added, Object: cur}, nil
	case w.matchesAll:
		return watch.Event{Type: watch.Modified, Object: cur}, nil
	}
	curPasses, oldPasses := w.filter(cur), w.filter(old)
	switch {
	case curPasses && oldPasses:
		return watch.Event{Type: watch.Modified, Object: cur}, nil
	case curPasses:
		return watch.Event{Type: watch.Added, Object: cur}, nil
	case oldPasses:
		return watch.Event{Type: watch.Deleted, Object: old}, nil
	}
	return watch.Event{}, nil
}

// bookmark returns an empty object at rev, which is the body of a BOOKMARK
// event.
func (w *watcher) bookmark(rev int64) (runtime.Object, error) {
	obj := w.s.newFunc()
	if err := w.s.versioner.UpdateObject(obj, uint64(rev)); err != nil {
		return nil, fmt.Errorf("failed to propagate object resource version: %w", err)
	}
	return obj, nil
}

func (w *watcher) filter(obj runtime.Object) bool {
	if w.matchesAll {
		return true
	}
	matched, err := w.pred.Matches(obj)
	return err == nil && matched
}

func (w *watcher) decode(key string, value []byte, rev int64) (runtime.Object, error) {
	data, _, err := w.s.transformer.TransformFromStorage(w.ctx, value, w.s.authData(key))
	if err != nil {
		return nil, storage.NewInternalError(err)
	}
	obj := w.s.newFunc()
	if err := w.s.decoder.Decode(data, obj, rev); err != nil {
		return nil, err
	}
	return obj, nil
}

// send delivers ev unless the watch has ended, and reports whether the watch
// is still running.
func (w *watcher) send(ev watch.Event) bool {
	select {
	case w.result <- ev:
		return true
	case <-w.ctx.Done():
		return false
	}
}

// sendError ends the watch with err as an ERROR event. A watch that ended
// because its context was cancelled gets no event.
func (w *watcher) sendError(err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	var apiStatus apierrors.APIStatus
	if !errors.As(err, &apiStatus) {
		apiStatus = apierrors.NewInternalError(err)
	}
	status := apiStatus.Status()
	select {
	case w.result <- watch.Event{Type: watch.Error, Object: &status}:
	case <-w.ctx.Done():
	}
}
