// Package projectstate keeps the API server's view of Projects: the small
// state that request handling needs, derived from one watch of the Project
// objects and shared by every reader in the process.
package projectstate

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/server"
	"k8s.io/apiserver/pkg/server/healthz"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"

	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
)

const defaultResyncPeriod = 10 * time.Minute

// ProjectGVR identifies the cluster-scoped Project resource.
var ProjectGVR = schema.GroupVersionResource{
	Group:    "resourcemanager.miloapis.com",
	Version:  "v1alpha1",
	Resource: "projects",
}

// Suspension is the derived suspension state of a suspended Project.
type Suspension struct {
	Suspended   bool
	Suspensions []resourcemanagerv1alpha1.ProjectSuspensionInfo
}

// Cache maintains a thread-safe view of Projects, kept in sync via a watch
// stream. Unlike a SharedIndexInformer, there is no separate full-object
// indexer behind it: projectStore (below) IS the Reflector's store, and it
// only ever retains derived state — never a full copy of a Project object.
type Cache struct {
	reflector *cache.Reflector
	store     *projectStore
	startOnce sync.Once
}

// NewCache creates a Cache that watches Projects through client. Start runs it.
func NewCache(client dynamic.Interface) *Cache {
	c := &Cache{store: newProjectStore()}

	lw := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return client.Resource(ProjectGVR).List(context.TODO(), options)
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			return client.Resource(ProjectGVR).Watch(context.TODO(), options)
		},
	}

	// client-go's WatchListClient feature (default-on since Kubernetes 1.35, and
	// still in client-go v0.37.1: k8s.io/client-go/features/known_features.go)
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

// projectStore is the cache.ReflectorStore backing the Reflector directly —
// there is no separate indexer behind it. It only ever retains projects that
// are currently suspended, derived once per delta the Reflector processes
// rather than re-parsed on every admission check. Reflector calls
// Add/Update/Delete/Replace synchronously as it processes the watch stream
// (single consumer, no listener fan-out), so reads always see the latest
// processed state with no dispatch lag.
type projectStore struct {
	mu        sync.RWMutex
	suspended map[string]*Suspension
	synced    bool
}

func newProjectStore() *projectStore {
	return &projectStore{suspended: make(map[string]*Suspension)}
}

func (s *projectStore) Add(obj interface{}) error    { return s.upsert(obj) }
func (s *projectStore) Update(obj interface{}) error { return s.upsert(obj) }

func (s *projectStore) upsert(obj interface{}) error {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return fmt.Errorf("projectstate cache: unexpected object type %T", obj)
	}
	state := parseSuspension(u)

	s.mu.Lock()
	defer s.mu.Unlock()
	if state == nil || !state.Suspended {
		delete(s.suspended, u.GetName())
		return nil
	}
	s.suspended[u.GetName()] = state
	return nil
}

func (s *projectStore) Delete(obj interface{}) error {
	// obj may be a DeletedFinalStateUnknown tombstone if we missed the
	// actual delete event (e.g. after a watch reconnect).
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tombstone.Obj
	}
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return fmt.Errorf("projectstate cache: unexpected object type %T", obj)
	}
	s.mu.Lock()
	delete(s.suspended, u.GetName())
	s.mu.Unlock()
	return nil
}

// Replace performs a full resync from a fresh List: the suspended-projects
// map is rebuilt from scratch, so entries for projects that were
// unsuspended or deleted while disconnected are dropped.
func (s *projectStore) Replace(list []interface{}, _ string) error {
	fresh := make(map[string]*Suspension, len(list))
	for _, obj := range list {
		u, ok := obj.(*unstructured.Unstructured)
		if !ok {
			continue
		}
		if state := parseSuspension(u); state != nil && state.Suspended {
			fresh[u.GetName()] = state
		}
	}

	s.mu.Lock()
	s.suspended = fresh
	s.synced = true
	s.mu.Unlock()
	return nil
}

func (s *projectStore) Resync() error { return nil }

func (s *projectStore) hasSynced() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.synced
}

func (s *projectStore) suspension(name string) *Suspension {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.suspended[name]
}

// Start begins the reflector's List/Watch loop until stopCh closes.
func (c *Cache) Start(stopCh <-chan struct{}) {
	c.startOnce.Do(func() {
		go c.reflector.Run(stopCh)
	})
}

// PostStartHook starts the cache once the server serves, so its first List
// reaches a listening server, and stops it with the server.
func (c *Cache) PostStartHook() server.PostStartHookFunc {
	return func(ctx server.PostStartHookContext) error {
		c.Start(ctx.Done())
		return nil
	}
}

// HasSynced reports whether the reflector has completed its initial List.
func (c *Cache) HasSynced() bool {
	return c.store.hasSynced()
}

// ReadinessCheck keeps the server not ready until the cache has completed
// its initial List, so readers never act on an empty view of Projects.
func (c *Cache) ReadinessCheck() healthz.HealthChecker {
	return healthz.NamedCheck("project-suspension-validator", func(*http.Request) error {
		if !c.HasSynced() {
			return fmt.Errorf("project state cache not synced")
		}
		return nil
	})
}

// Suspension returns the suspension state of the named Project, or nil when
// the Project is not suspended or not known.
func (c *Cache) Suspension(name string) *Suspension {
	return c.store.suspension(name)
}

// parseSuspension extracts the Suspended condition and any active
// suspensions from a Project object retrieved via the dynamic client.
// Returns nil if the project is not suspended.
func parseSuspension(obj *unstructured.Unstructured) *Suspension {
	if !hasSuspendedCondition(obj) {
		return nil
	}
	return &Suspension{Suspended: true, Suspensions: activeSuspensions(obj)}
}

// hasSuspendedCondition reports whether the Project's Suspended condition is
// True. A missing or malformed conditions list reads as not suspended.
func hasSuspendedCondition(obj *unstructured.Unstructured) bool {
	conditions, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	for _, c := range conditions {
		m, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		if condType, _, _ := unstructured.NestedString(m, "type"); condType == resourcemanagerv1alpha1.ProjectSuspended {
			status, _, _ := unstructured.NestedString(m, "status")
			return status == string(metav1.ConditionTrue)
		}
	}
	return false
}

// activeSuspensions returns the Project's status.suspensions, skipping
// entries that do not convert.
func activeSuspensions(obj *unstructured.Unstructured) []resourcemanagerv1alpha1.ProjectSuspensionInfo {
	raw, _, _ := unstructured.NestedSlice(obj.Object, "status", "suspensions")
	var suspensions []resourcemanagerv1alpha1.ProjectSuspensionInfo
	for _, item := range raw {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		var info resourcemanagerv1alpha1.ProjectSuspensionInfo
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(m, &info); err != nil {
			continue
		}
		suspensions = append(suspensions, info)
	}
	return suspensions
}
