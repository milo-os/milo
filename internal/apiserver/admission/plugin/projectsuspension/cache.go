package projectsuspension

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
)

const defaultResyncPeriod = 10 * time.Minute

// ProjectSuspensionCache maintains a thread-safe cache of suspended
// projects, kept in sync via a watch stream. Unlike a SharedIndexInformer,
// there is no separate full-object indexer behind it: suspendedProjectStore
// (below) IS the Reflector's store, and it only ever retains derived
// suspension state for projects that are actually suspended — never a
// full copy of every Project object.
type ProjectSuspensionCache struct {
	dynamicClient dynamic.Interface
	reflector     *cache.Reflector
	store         *suspendedProjectStore

	stopCh    chan struct{}
	startOnce sync.Once
	logger    logr.Logger
}

// NewProjectSuspensionCache creates and configures a new ProjectSuspensionCache.
func NewProjectSuspensionCache(dynamicClient dynamic.Interface, logger logr.Logger) *ProjectSuspensionCache {
	c := &ProjectSuspensionCache{
		dynamicClient: dynamicClient,
		store:         newSuspendedProjectStore(),
		stopCh:        make(chan struct{}),
		logger:        logger.WithName("project-suspension-cache"),
	}

	lw := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return c.dynamicClient.Resource(projectGVR).List(context.TODO(), options)
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return c.dynamicClient.Resource(projectGVR).Watch(context.TODO(), options)
		},
	}

	// client-go's WatchListClient feature (default-on as of client-go
	// v0.35 / Kubernetes 1.35, see k8s.io/client-go/features/known_features.go)
	// makes the Reflector skip the classic List-then-Watch sequence in
	// favor of a single streaming watch-with-initial-events call,
	// terminated by a server-sent bookmark. Our ListWatch doesn't
	// implement that protocol, and there's no meaningful benefit to it for
	// a low-cardinality resource like Project, so opt out explicitly via
	// the documented escape hatch and always get the well-tested classic
	// semantics — deterministically, in both production and tests (some
	// ListerWatcher implementations, e.g. fakes used in tests, don't
	// support the newer protocol at all).
	c.reflector = cache.NewReflector(noWatchListSemantics{lw}, &unstructured.Unstructured{}, c.store, defaultResyncPeriod)

	return c
}

// noWatchListSemantics tells the Reflector this ListerWatcher does not
// support the WatchList streaming protocol, forcing classic
// List-then-Watch semantics regardless of the ambient WatchListClient
// feature-gate default. See k8s.io/client-go/util/watchlist.
type noWatchListSemantics struct {
	cache.ListerWatcher
}

func (noWatchListSemantics) IsWatchListSemanticsUnSupported() bool { return true }

// suspendedProjectStore is the cache.ReflectorStore backing the Reflector
// directly — there is no separate indexer behind it. It only ever retains
// projects that are currently suspended, derived once per delta the
// Reflector processes rather than re-parsed on every admission check.
// Reflector calls Add/Update/Delete/Replace synchronously as it processes
// the watch stream (single consumer, no listener fan-out), so reads always
// see the latest processed state with no dispatch lag.
type suspendedProjectStore struct {
	mu        sync.RWMutex
	suspended map[string]*suspensionState
	synced    bool
}

func newSuspendedProjectStore() *suspendedProjectStore {
	return &suspendedProjectStore{suspended: make(map[string]*suspensionState)}
}

func (s *suspendedProjectStore) Add(obj interface{}) error    { return s.upsert(obj) }
func (s *suspendedProjectStore) Update(obj interface{}) error { return s.upsert(obj) }

func (s *suspendedProjectStore) upsert(obj interface{}) error {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return fmt.Errorf("projectsuspension cache: unexpected object type %T", obj)
	}
	state := parseSuspensionState(u)

	s.mu.Lock()
	defer s.mu.Unlock()
	if state == nil || !state.Suspended {
		delete(s.suspended, u.GetName())
		return nil
	}
	s.suspended[u.GetName()] = state
	return nil
}

func (s *suspendedProjectStore) Delete(obj interface{}) error {
	// obj may be a DeletedFinalStateUnknown tombstone if we missed the
	// actual delete event (e.g. after a watch reconnect).
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tombstone.Obj
	}
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return fmt.Errorf("projectsuspension cache: unexpected object type %T", obj)
	}
	s.mu.Lock()
	delete(s.suspended, u.GetName())
	s.mu.Unlock()
	return nil
}

// Replace performs a full resync from a fresh List: the suspended-projects
// map is rebuilt from scratch, so entries for projects that were
// unsuspended or deleted while disconnected are dropped.
func (s *suspendedProjectStore) Replace(list []interface{}, _ string) error {
	fresh := make(map[string]*suspensionState, len(list))
	for _, obj := range list {
		u, ok := obj.(*unstructured.Unstructured)
		if !ok {
			continue
		}
		if state := parseSuspensionState(u); state != nil && state.Suspended {
			fresh[u.GetName()] = state
		}
	}

	s.mu.Lock()
	s.suspended = fresh
	s.synced = true
	s.mu.Unlock()
	return nil
}

func (s *suspendedProjectStore) Resync() error { return nil }

func (s *suspendedProjectStore) hasSynced() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.synced
}

func (s *suspendedProjectStore) lookup(projectID string) (*suspensionState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, ok := s.suspended[projectID]
	return state, ok
}

// Start begins the reflector's List/Watch loop.
func (c *ProjectSuspensionCache) Start() {
	c.startOnce.Do(func() {
		go c.reflector.Run(c.stopCh)
	})
}

// HasSynced reports whether the reflector has completed its initial List.
func (c *ProjectSuspensionCache) HasSynced() bool {
	return c.store.hasSynced()
}

// Stop shuts down the reflector.
func (c *ProjectSuspensionCache) Stop() {
	select {
	case <-c.stopCh:
	default:
		close(c.stopCh)
	}
}

// GetSuspensionState returns the suspension state for the given projectID.
func (c *ProjectSuspensionCache) GetSuspensionState(_ context.Context, projectID string) *suspensionState {
	state, _ := c.store.lookup(projectID)
	return state
}
