package projectstorage

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/storagebackend"
	"k8s.io/apiserver/pkg/storage/storagebackend/factory"
	"k8s.io/client-go/tools/cache"
)

var claimsGR = schema.GroupResource{Group: "quota.miloapis.com", Resource: "resourceclaims"}

// view is a project view the test sets: synced or not, and its names.
type view struct {
	mu     sync.Mutex
	synced bool
	names  []string
}

func (v *view) HasSynced() bool { v.mu.Lock(); defer v.mu.Unlock(); return v.synced }
func (v *view) Names() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]string(nil), v.names...)
}

// claimChildren stands in for etcd as each claim child's storage. It counts the
// children built per project, and a project the test marks cold answers its
// readiness check with storage.ErrStorageNotReady, as an unready cacher does.
type claimChildren struct {
	mu    sync.Mutex
	built map[string]int
	cold  map[string]bool
}

func newClaimChildren() *claimChildren {
	return &claimChildren{built: map[string]int{}, cold: map[string]bool{}}
}

func (c *claimChildren) storage(cfg *storagebackend.ConfigForResource, _ string, _ func(runtime.Object) (string, error),
	_ func() runtime.Object, _ func() runtime.Object, _ storage.AttrFunc, _ storage.IndexerFuncs,
	_ *cache.Indexers) (storage.Interface, factory.DestroyFunc, error) {
	project := path.Base(cfg.Prefix) // "/projects/<name>"; the root plane's prefix is empty
	c.mu.Lock()
	defer c.mu.Unlock()
	c.built[project]++
	return readinessStorage{c: c, project: project}, func() {}, nil
}

func (c *claimChildren) builtFor(project string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.built[project]
}

func (c *claimChildren) setCold(project string, cold bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cold[project] = cold
}

func (c *claimChildren) projectsBuilt() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for p, k := range c.built {
		if p != "." && k > 0 {
			n++
		}
	}
	return n
}

type readinessStorage struct {
	fakeStorage
	c       *claimChildren
	project string
}

func (s readinessStorage) ReadinessCheck() error {
	s.c.mu.Lock()
	defer s.c.mu.Unlock()
	if s.c.cold[s.project] {
		return storage.ErrStorageNotReady
	}
	return nil
}

// buildClaims builds the claims resource's storage as apiextensions does on
// the first request for the definition: through the decorator.
func buildClaims(t *testing.T, c *claimChildren) {
	t.Helper()
	var inner generic.StorageDecorator = c.storage
	_, destroy, err := ProjectAwareDecorator(claimsGR, inner, everyProjectKnown(), nil, testDrain)(
		&storagebackend.ConfigForResource{}, "", nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("build the claims storage: %v", err)
	}
	t.Cleanup(destroy)
}

func served() error { return nil }

func wantReason(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("want a failure naming %q, got %v", want, err)
	}
}

func TestReadinessWaitsForTheProjectView(t *testing.T) {
	c := newClaimChildren()
	buildClaims(t, c)
	r := NewChildrenReadiness(claimsGR, &view{names: []string{"p1", "p2"}}, served, 50)
	for i := 0; i < 5; i++ {
		wantReason(t, r.ReadinessCheck(), "view")
	}
	if n := c.projectsBuilt(); n != 0 {
		t.Fatalf("built %d project children before the view synced", n)
	}
}

func TestReadinessNamesAColdProjectUntilItServes(t *testing.T) {
	c := newClaimChildren()
	c.setCold("p2", true)
	buildClaims(t, c)
	r := NewChildrenReadiness(claimsGR, &view{synced: true, names: []string{"p1", "p2", "p3"}}, served, 50)
	wantReason(t, r.ReadinessCheck(), "p2")
	wantReason(t, r.ReadinessCheck(), "p2")
	for _, p := range []string{"p1", "p2", "p3"} {
		if n := c.builtFor(p); n != 1 {
			t.Fatalf("project %s: %d children built, want 1", p, n)
		}
	}
	c.setCold("p2", false)
	if err := r.ReadinessCheck(); err != nil {
		t.Fatalf("every child serves: want ready, got %v", err)
	}
}

// A cold boot: bootstrap-crds runs beside the hook, and until the claims
// definition is served a list of claims answers 404 for the resource. The
// check must keep waiting and build nothing, never read every project as gone.
func TestReadinessWaitsForTheClaimsResourceToBeServed(t *testing.T) {
	c := newClaimChildren()
	lists := 0
	root := func() error {
		lists++
		if lists <= 3 {
			return apierrors.NewNotFound(claimsGR, "")
		}
		buildClaims(t, c)
		return nil
	}
	r := NewChildrenReadiness(claimsGR, &view{synced: true, names: []string{"p1", "p2"}}, root, 50)
	r.every = 0
	for i := 0; i < 3; i++ {
		wantReason(t, r.ReadinessCheck(), "not served")
	}
	if n := c.projectsBuilt(); n != 0 {
		t.Fatalf("built %d project children before the resource was served", n)
	}
	_ = r.ReadinessCheck() // the root list succeeds and builds the storage
	if err := r.ReadinessCheck(); err != nil {
		t.Fatalf("the resource is served and every child serves: want ready, got %v", err)
	}
	if lists != 4 || c.builtFor("p1") != 1 || c.builtFor("p2") != 1 {
		t.Fatalf("root lists %d (want 4), p1 %d, p2 %d children (want 1 each)", lists, c.builtFor("p1"), c.builtFor("p2"))
	}
}

func TestReadinessListsTheRootAtMostOnceASecond(t *testing.T) {
	lists := 0
	r := NewChildrenReadiness(schema.GroupResource{Group: "x", Resource: "unbuilt"}, &view{synced: true}, func() error {
		lists++
		return apierrors.NewNotFound(claimsGR, "")
	}, 50)
	now := time.Unix(0, 0)
	r.now = func() time.Time { return now }
	for i := 0; i < 10; i++ { // ten calls inside one second
		_ = r.ReadinessCheck()
		now = now.Add(100 * time.Millisecond)
	}
	if lists != 1 {
		t.Fatalf("root lists in one second: %d, want 1", lists)
	}
}

func TestReadinessRetriesARootListThatFails(t *testing.T) {
	c := newClaimChildren()
	lists := 0
	root := func() error {
		lists++
		if lists <= 2 {
			return apierrors.NewInternalError(errors.New("etcd unavailable"))
		}
		buildClaims(t, c)
		return nil
	}
	r := NewChildrenReadiness(claimsGR, &view{synced: true, names: []string{"p1"}}, root, 50)
	r.every = 0
	wantReason(t, r.ReadinessCheck(), "etcd unavailable")
	wantReason(t, r.ReadinessCheck(), "etcd unavailable")
	_ = r.ReadinessCheck()
	if err := r.ReadinessCheck(); err != nil || lists != 3 {
		t.Fatalf("after two failed root lists: err %v, root lists %d (want ready and 3)", err, lists)
	}
}

func TestReadinessBuildsAtMostABatchPerCall(t *testing.T) {
	c := newClaimChildren()
	buildClaims(t, c)
	names := make([]string, 667)
	for i := range names {
		names[i] = fmt.Sprintf("p%03d", i)
	}
	r := NewChildrenReadiness(claimsGR, &view{synced: true, names: names}, served, 50)
	for call := 1; call <= 14; call++ {
		_ = r.ReadinessCheck()
		want := min(50*call, 667)
		if n := c.projectsBuilt(); n != want {
			t.Fatalf("after call %d: %d children built, want %d", call, n, want)
		}
	}
	if err := r.ReadinessCheck(); err != nil {
		t.Fatalf("all 667 built and serving: want ready, got %v", err)
	}
	for _, p := range names {
		if n := c.builtFor(p); n != 1 {
			t.Fatalf("project %s: %d children built, want exactly 1", p, n)
		}
	}
}

func TestReadinessForgetsAProjectThatLeftTheView(t *testing.T) {
	c := newClaimChildren()
	c.setCold("gone", true)
	buildClaims(t, c)
	v := &view{synced: true, names: []string{"p1", "gone"}}
	r := NewChildrenReadiness(claimsGR, v, served, 50)
	wantReason(t, r.ReadinessCheck(), "gone")
	v.mu.Lock()
	v.names = []string{"p1"}
	v.mu.Unlock()
	if err := r.ReadinessCheck(); err != nil {
		t.Fatalf("the cold project left the view: want ready, got %v", err)
	}
}

// A Project being deleted stays in the view until it is gone, and a read into
// it is served, so its child is built and checked like any other.
func TestReadinessChecksADeletingProjectLikeAnyOther(t *testing.T) {
	c := newClaimChildren()
	c.setCold("deleting", true)
	buildClaims(t, c)
	r := NewChildrenReadiness(claimsGR, &view{synced: true, names: []string{"deleting"}}, served, 50)
	wantReason(t, r.ReadinessCheck(), "deleting")
	if n := c.builtFor("deleting"); n != 1 {
		t.Fatalf("deleting project: %d children built, want 1", n)
	}
}

type registrar struct{ got []metav1.GroupVersionResource }

func (r *registrar) RegisterStorage(gvr metav1.GroupVersionResource, _ rest.StorageWithReadiness) {
	r.got = append(r.got, gvr)
}

func TestTheFlagDecidesTheRegistration(t *testing.T) {
	gvr := metav1.GroupVersionResource{Group: claimsGR.Group, Version: "v1alpha1", Resource: claimsGR.Resource}
	for _, enabled := range []bool{true, false} {
		reg := &registrar{}
		RegisterChildrenReadiness(reg, enabled, gvr, NewChildrenReadiness(claimsGR, &view{}, served, 50))
		if want := map[bool]int{true: 1, false: 0}[enabled]; len(reg.got) != want || (want == 1 && reg.got[0] != gvr) {
			t.Fatalf("enabled=%v: registered %v, want %d registration of %v", enabled, reg.got, want, gvr)
		}
	}
}
