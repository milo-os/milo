// Package orgstate keeps the API server's view of Organizations: whether each
// has finished onboarding, derived from one watch of the Organization objects
// and shared by every reader in the process.
package orgstate

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

// OrganizationGVR identifies the cluster-scoped Organization resource.
var OrganizationGVR = schema.GroupVersionResource{
	Group:    "resourcemanager.miloapis.com",
	Version:  "v1alpha1",
	Resource: "organizations",
}

// Onboarding is an Organization's OnboardingComplete condition as this server
// last saw it.
type Onboarding struct {
	Complete bool
	Reason   string
}

// Cache maintains a thread-safe view of Organizations' onboarding, kept in
// sync via a watch stream. Like projectstate.Cache, the store behind the
// Reflector retains only derived state, never a full Organization.
type Cache struct {
	reflector *cache.Reflector
	store     *orgStore
	startOnce sync.Once
}

// NewCache creates a Cache that watches Organizations through client. Start
// runs it.
func NewCache(client dynamic.Interface) *Cache {
	c := &Cache{store: &orgStore{orgs: map[string]Onboarding{}}}

	lw := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			return client.Resource(OrganizationGVR).List(ctx, options)
		},
		WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			return client.Resource(OrganizationGVR).Watch(ctx, options)
		},
	}
	c.reflector = cache.NewReflector(noWatchListSemantics{lw}, &unstructured.Unstructured{}, c.store, defaultResyncPeriod)
	return c
}

// noWatchListSemantics opts out of client-go's WatchList streaming protocol,
// for the reasons given on projectstate's type of the same name.
type noWatchListSemantics struct {
	cache.ListerWatcher
}

func (noWatchListSemantics) IsWatchListSemanticsUnSupported() bool { return true }

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

// ReadinessCheck keeps the server not ready until the cache has completed its
// initial List, so admission never reads an empty view as "nobody onboarded".
func (c *Cache) ReadinessCheck() healthz.HealthChecker {
	return healthz.NamedCheck("organization-state-cache", func(*http.Request) error {
		if !c.HasSynced() {
			return fmt.Errorf("organization state cache not synced")
		}
		return nil
	})
}

// Onboarding returns the named Organization's onboarding, and false when the
// cache holds no Organization of that name.
func (c *Cache) Onboarding(name string) (Onboarding, bool) {
	return c.store.lookup(name)
}

// orgStore is the cache.ReflectorStore backing the Reflector directly.
type orgStore struct {
	mu     sync.RWMutex
	orgs   map[string]Onboarding
	synced bool
}

func (s *orgStore) Add(obj interface{}) error    { return s.upsert(obj) }
func (s *orgStore) Update(obj interface{}) error { return s.upsert(obj) }

func (s *orgStore) upsert(obj interface{}) error {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return fmt.Errorf("orgstate cache: unexpected object type %T", obj)
	}
	o := onboardingOf(u)

	s.mu.Lock()
	s.orgs[u.GetName()] = o
	s.mu.Unlock()
	return nil
}

func (s *orgStore) Delete(obj interface{}) error {
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tombstone.Obj
	}
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return fmt.Errorf("orgstate cache: unexpected object type %T", obj)
	}
	s.mu.Lock()
	delete(s.orgs, u.GetName())
	s.mu.Unlock()
	return nil
}

func (s *orgStore) Replace(list []interface{}, _ string) error {
	fresh := make(map[string]Onboarding, len(list))
	for _, obj := range list {
		u, ok := obj.(*unstructured.Unstructured)
		if !ok {
			continue
		}
		fresh[u.GetName()] = onboardingOf(u)
	}

	s.mu.Lock()
	s.orgs = fresh
	s.synced = true
	s.mu.Unlock()
	return nil
}

func (s *orgStore) Resync() error { return nil }

func (s *orgStore) hasSynced() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.synced
}

func (s *orgStore) lookup(name string) (Onboarding, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	o, ok := s.orgs[name]
	return o, ok
}

// onboardingOf reads the Organization's OnboardingComplete condition. A
// missing or malformed condition reads as not complete.
func onboardingOf(u *unstructured.Unstructured) Onboarding {
	conditions, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	for _, c := range conditions {
		m, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		if t, _, _ := unstructured.NestedString(m, "type"); t != resourcemanagerv1alpha1.OrganizationConditionOnboardingComplete {
			continue
		}
		status, _, _ := unstructured.NestedString(m, "status")
		reason, _, _ := unstructured.NestedString(m, "reason")
		return Onboarding{Complete: status == string(metav1.ConditionTrue), Reason: reason}
	}
	return Onboarding{}
}
