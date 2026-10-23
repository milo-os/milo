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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilcache "k8s.io/apimachinery/pkg/util/cache"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/server"
	"k8s.io/apiserver/pkg/server/healthz"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/clock"

	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
)

const defaultResyncPeriod = 10 * time.Minute

// The force-live list's bounds are upstream NamespaceLifecycle's
// (k8s.io/apiserver v0.37.1,
// pkg/admission/plugin/namespace/lifecycle/admission.go lines 45 and
// 199): a name stays live for 30 s after its delete, and the list keeps 100
// names.
const (
	forceLiveLookupTTL  = 30 * time.Second
	forceLiveLookupSize = 100
)

// ProjectGVR identifies the cluster-scoped Project resource.
var ProjectGVR = schema.GroupVersionResource{
	Group:    "resourcemanager.miloapis.com",
	Version:  "v1alpha1",
	Resource: "projects",
}

// Project is what this server knows about one Project: which object its name
// refers to now, and whether that object is being deleted.
type Project struct {
	UID      types.UID
	Deleting bool
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
	client    dynamic.Interface
	reflector *cache.Reflector
	store     *projectStore
	startOnce sync.Once

	// forceLive holds the names of Projects deleted in the last
	// forceLiveLookupTTL, which the reflector may still show live.
	forceLive *utilcache.LRUExpireCache
}

// NewCache creates a Cache that watches Projects through client. Start runs it.
func NewCache(client dynamic.Interface) *Cache {
	return NewCacheWithClock(client, clock.RealClock{})
}

// NewCacheWithClock creates a Cache whose force-live list keeps time by clk.
func NewCacheWithClock(client dynamic.Interface, clk clock.Clock) *Cache {
	c := &Cache{
		client:    client,
		store:     newProjectStore(),
		forceLive: utilcache.NewLRUExpireCacheWithClock(forceLiveLookupSize, clk),
	}

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
// there is no separate indexer behind it. It retains, per Project name, an
// entry derived once per delta the Reflector processes rather than re-parsed
// on every request. Reflector calls Add/Update/Delete/Replace synchronously
// as it processes the watch stream (single consumer, no listener fan-out), so
// reads always see the latest processed state with no dispatch lag.
type projectStore struct {
	mu       sync.RWMutex
	projects map[string]entry
	synced   bool
}

// entry is what the store keeps of one Project object: its identity and
// deletion mark, and its suspension only while it is suspended.
type entry struct {
	project    Project
	suspension *Suspension
}

func newProjectStore() *projectStore {
	return &projectStore{projects: make(map[string]entry)}
}

func newEntry(u *unstructured.Unstructured) entry {
	return entry{project: projectOf(u), suspension: parseSuspension(u)}
}

func projectOf(u *unstructured.Unstructured) Project {
	return Project{UID: u.GetUID(), Deleting: u.GetDeletionTimestamp() != nil}
}

func (s *projectStore) Add(obj interface{}) error    { return s.upsert(obj) }
func (s *projectStore) Update(obj interface{}) error { return s.upsert(obj) }

func (s *projectStore) upsert(obj interface{}) error {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return fmt.Errorf("projectstate cache: unexpected object type %T", obj)
	}
	e := newEntry(u)

	s.mu.Lock()
	s.projects[u.GetName()] = e
	s.mu.Unlock()
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
	delete(s.projects, u.GetName())
	s.mu.Unlock()
	return nil
}

// Replace performs a full resync from a fresh List: the map is rebuilt from
// scratch, so entries for projects that were deleted, re-created or
// unsuspended while disconnected are replaced or dropped.
func (s *projectStore) Replace(list []interface{}, _ string) error {
	fresh := make(map[string]entry, len(list))
	for _, obj := range list {
		u, ok := obj.(*unstructured.Unstructured)
		if !ok {
			continue
		}
		fresh[u.GetName()] = newEntry(u)
	}

	s.mu.Lock()
	s.projects = fresh
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

func (s *projectStore) lookup(name string) (entry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.projects[name]
	return e, ok
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

// Lookup returns the Project a name refers to now. The reflector answers,
// unless it holds no Project of that name, or the name was deleted in the
// last forceLiveLookupTTL and the reflector still shows it live: then one GET
// asks storage. That is upstream NamespaceLifecycle's rule for namespaces: a
// view that lags storage does not decide alone for a name it has not seen,
// or for one just deleted. A name storage does not hold is NotFound; any
// other failure of the GET is an InternalError, as upstream returns it.
func (c *Cache) Lookup(ctx context.Context, name string) (Project, error) {
	if e, ok := c.store.lookup(name); ok && !c.liveLookupForced(name, e.project) {
		return e.project, nil
	}
	u, err := c.client.Resource(ProjectGVR).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return Project{}, err
	}
	if err != nil {
		return Project{}, apierrors.NewInternalError(err)
	}
	return projectOf(u), nil
}

// liveLookupForced reports whether a Project the reflector holds must be read
// from storage. A deletion the reflector already shows stands, as upstream
// forces a live read only while its cache says Active, so the purge's own
// requests into the plane cost no read each.
func (c *Cache) liveLookupForced(name string, cached Project) bool {
	if cached.Deleting {
		return false
	}
	_, forced := c.forceLive.Get(name)
	return forced
}

// ForceLiveLookup makes Lookup read name from storage for the next
// forceLiveLookupTTL. The admission chain calls it when it admits a
// Project's DELETE, before the reflector can have seen the deletion.
func (c *Cache) ForceLiveLookup(name string) {
	c.forceLive.Add(name, true, forceLiveLookupTTL)
}

// Project returns the named Project's state, and false when the cache holds no
// Project of that name.
func (c *Cache) Project(name string) (Project, bool) {
	e, ok := c.store.lookup(name)
	return e.project, ok
}

// Suspension returns the suspension state of the named Project, or nil when
// the Project is not suspended or not known.
func (c *Cache) Suspension(name string) *Suspension {
	e, _ := c.store.lookup(name)
	return e.suspension
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
