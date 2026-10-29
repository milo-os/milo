package projectstate

import "sync"

// A Listener hears when Projects may have left the view. The Reflector calls
// it from its own goroutine after the view has changed, so a call must not
// block.
type Listener interface {
	// ProjectGone reports that the view no longer holds the named Project.
	ProjectGone(name string)
	// ViewResynced reports a relist or a periodic resync: the listener frees
	// what it holds for every name the view does not hold.
	ViewResynced()
}

// listeners is the set of Listeners one store calls.
type listeners struct {
	mu   sync.Mutex
	next int
	set  map[int]Listener
}

// add registers l and returns the function that removes it.
func (ls *listeners) add(l Listener) (remove func()) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	if ls.set == nil {
		ls.set = map[int]Listener{}
	}
	id := ls.next
	ls.next++
	ls.set[id] = l
	return func() {
		ls.mu.Lock()
		defer ls.mu.Unlock()
		delete(ls.set, id)
	}
}

// each calls f for every Listener registered now, outside the set's lock, so
// a Listener may remove itself during the call.
func (ls *listeners) each(f func(Listener)) {
	ls.mu.Lock()
	snapshot := make([]Listener, 0, len(ls.set))
	for _, l := range ls.set {
		snapshot = append(snapshot, l)
	}
	ls.mu.Unlock()
	for _, l := range snapshot {
		f(l)
	}
}

// AddListener registers l with the view and returns the function that removes
// it. A listener that holds state per Project removes itself when it stops.
func (c *Cache) AddListener(l Listener) (remove func()) {
	return c.store.listeners.add(l)
}
