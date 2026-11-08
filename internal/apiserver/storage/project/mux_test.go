package projectstorage

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apiserver/pkg/storage"
	storagebackend "k8s.io/apiserver/pkg/storage/storagebackend"
	factory "k8s.io/apiserver/pkg/storage/storagebackend/factory"
	"k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/tools/cache"
	k8smetrics "k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/testutil"

	"go.miloapis.com/milo/internal/apiserver/projectstate"
	"go.miloapis.com/milo/internal/apiserver/projectstate/projectstatetest"
)

// testDrain is how long a removed child serves its requests here: short, so a
// test sees the destroy.
const testDrain = 10 * time.Millisecond

// countingChildren stands in for etcd as every child's storage, and counts the
// children built and destroyed.
type countingChildren struct {
	built, destroyed atomic.Int32
}

func (c *countingChildren) storage(*storagebackend.ConfigForResource, string, func(runtime.Object) (string, error),
	func() runtime.Object, func() runtime.Object, storage.AttrFunc, storage.IndexerFuncs,
	*cache.Indexers) (storage.Interface, factory.DestroyFunc, error) {
	c.built.Add(1)
	return fakeStorage{}, func() { c.destroyed.Add(1) }, nil
}

// decorate builds the configmaps storage on children, as the API server does,
// and returns it with the number of children built so far: the root plane's.
func decorate(t *testing.T, children *countingChildren, projects ProjectLookup) (storage.Interface, int32) {
	t.Helper()
	s, destroy, err := ProjectAwareDecorator(schema.GroupResource{Resource: "configmaps"}, children.storage, projects, nil, testDrain)(
		&storagebackend.ConfigForResource{}, "", nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("decorate configmaps: %v", err)
	}
	t.Cleanup(destroy)
	return s, children.built.Load()
}

// TestRequestIntoAMissingProjectBuildsNoChild covers infra#7701's cost: a
// child is a watch cache kept until the process stops, so a request of any
// verb into a Project that does not exist gets NotFound and builds none.
func TestRequestIntoAMissingProjectBuildsNoChild(t *testing.T) {
	var children countingChildren
	s, root := decorate(t, &children, noProjectKnown())

	for _, verb := range []string{"get", "list", "watch", "delete", "create"} {
		if err := s.Get(requestFor("gone", verb), "/k", storage.GetOptions{}, nil); !apierrors.IsNotFound(err) {
			t.Fatalf("expected a %s into a missing Project to get NotFound, got %v", verb, err)
		}
	}
	if n := children.built.Load() - root; n != 0 {
		t.Fatalf("expected no child for a missing Project, got %d", n)
	}
}

// TestInventedNamesCostOneLookupEach covers a loop of invented names: the
// view does not hold them, storage is asked once per name, and the answer
// is kept, so asking again costs no read and never builds a child.
func TestInventedNamesCostOneLookupEach(t *testing.T) {
	c, client := projectstatetest.NewCache(t)
	gets := projectstatetest.StorageAnswers(client, nil,
		apierrors.NewNotFound(projectstate.ProjectGVR.GroupResource(), "invented"))
	var children countingChildren
	s, root := decorate(t, &children, c)

	for range 50 {
		if err := s.Get(requestFor("invented", "list"), "/k", storage.GetOptions{}, nil); !apierrors.IsNotFound(err) {
			t.Fatalf("expected a list of an invented Project to get NotFound, got %v", err)
		}
	}
	if n := gets.Load(); n != 1 {
		t.Fatalf("expected one storage read for the name, got %d", n)
	}
	if n := children.built.Load() - root; n != 0 {
		t.Fatalf("expected no child for an invented Project, got %d", n)
	}
}

// TestDeletedProjectsChildIsFreed covers the other half: a child built for a
// live Project is destroyed once the view sees the Project go, and the next
// request into the gone Project builds no new one.
func TestDeletedProjectsChildIsFreed(t *testing.T) {
	c, client := projectstatetest.NewCache(t, projectstatetest.NewProjectWithUID("p", "uid-1"))
	var children countingChildren
	s, root := decorate(t, &children, c)

	if err := s.Get(requestFor("p", "get"), "/k", storage.GetOptions{}, nil); err != nil {
		t.Fatalf("expected a get into a live Project to be served, got %v", err)
	}
	if n := children.built.Load() - root; n != 1 {
		t.Fatalf("expected one child for the live Project, got %d", n)
	}
	if err := client.Resource(projectstate.ProjectGVR).Delete(context.Background(), "p", metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete the Project: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for children.destroyed.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("expected the gone Project's child to be destroyed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := s.Get(requestFor("p", "get"), "/k", storage.GetOptions{}, nil); !apierrors.IsNotFound(err) {
		t.Fatalf("expected a get into the gone Project to get NotFound, got %v", err)
	}
	if n := children.built.Load() - root; n != 1 {
		t.Fatalf("expected no new child for the gone Project, got %d built", n)
	}
}

// waitFor fails the test unless cond holds within a second.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// deleteProject deletes the named Project from the view's storage.
func deleteProject(t *testing.T, client *fake.FakeDynamicClient, name string) {
	t.Helper()
	if err := client.Resource(projectstate.ProjectGVR).Delete(context.Background(), name, metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete Project %s: %v", name, err)
	}
}

// TestResyncFreesChildrenTheViewDoesNotHold covers the sweep: a child built
// for a name the view never showed is freed at the view's resync, and the
// root plane's child and a live Project's child stay.
func TestResyncFreesChildrenTheViewDoesNotHold(t *testing.T) {
	projects := everyProjectKnown()
	var children countingChildren
	s, root := decorate(t, &children, projects)
	for _, p := range []string{"live", "gone"} {
		if err := s.Get(requestFor(p, "get"), "/k", storage.GetOptions{}, nil); err != nil {
			t.Fatalf("get into %s: %v", p, err)
		}
	}
	projects.set("live", projectstate.Project{UID: "uid-live"})
	projects.all = false

	s.(projectstate.Listener).ViewResynced()
	waitFor(t, "the gone name's child to be destroyed", func() bool { return children.destroyed.Load() == 1 })
	_ = s.Get(requestFor("live", "get"), "/k", storage.GetOptions{}, nil)
	_ = s.Get(requestFor("", "get"), "/k", storage.GetOptions{}, nil)
	if n := children.built.Load() - root; n != 2 {
		t.Fatalf("expected the live and root children to stay, got %d children built", n)
	}
}

// TestRecreatedProjectGetsAFreshChild covers a name deleted and created again:
// the old child is destroyed, and the new Project's first request builds one.
func TestRecreatedProjectGetsAFreshChild(t *testing.T) {
	c, client := projectstatetest.NewCache(t, projectstatetest.NewProjectWithUID("p", "uid-1"))
	var children countingChildren
	s, root := decorate(t, &children, c)
	_ = s.Get(requestFor("p", "get"), "/k", storage.GetOptions{}, nil)

	deleteProject(t, client, "p")
	waitFor(t, "the old child to be destroyed", func() bool { return children.destroyed.Load() == 1 })
	if _, err := client.Resource(projectstate.ProjectGVR).Create(context.Background(),
		projectstatetest.NewProjectWithUID("p", "uid-2"), metav1.CreateOptions{}); err != nil {
		t.Fatalf("create the Project again: %v", err)
	}
	waitFor(t, "the view to hold the new Project", func() bool { p, ok := c.Project("p"); return ok && p.UID == "uid-2" })
	if err := s.Get(requestFor("p", "get"), "/k", storage.GetOptions{}, nil); err != nil {
		t.Fatalf("expected the new Project to be served, got %v", err)
	}
	if n := children.built.Load() - root; n != 2 {
		t.Fatalf("expected a fresh child for the new Project, got %d built", n)
	}
}

// TestDeletingProjectKeepsItsChild covers the purge: a Project being deleted
// is still held by the view, so its child serves the purge and stays.
func TestDeletingProjectKeepsItsChild(t *testing.T) {
	deleting := projectstatetest.NewProjectWithUID("p", "uid-1")
	deleting.SetDeletionTimestamp(&metav1.Time{Time: time.Now()})
	deleting.SetFinalizers([]string{"example.com/purge"})
	c, _ := projectstatetest.NewCache(t, deleting)
	var children countingChildren
	s, _ := decorate(t, &children, c)

	if err := s.Get(requestFor("p", "list"), "/k", storage.GetOptions{}, nil); err != nil {
		t.Fatalf("expected the purge's read to be served, got %v", err)
	}
	s.(projectstate.Listener).ViewResynced()
	time.Sleep(5 * testDrain)
	if n := children.destroyed.Load(); n != 0 {
		t.Fatalf("expected the deleting Project's child to stay, got %d destroyed", n)
	}
}

// TestEveryMuxFreesItsOwnChild covers a type served in two versions: each
// version's storage is a mux of its own, and each frees its own child.
func TestEveryMuxFreesItsOwnChild(t *testing.T) {
	c, client := projectstatetest.NewCache(t, projectstatetest.NewProjectWithUID("p", "uid-1"))
	var v1, v2 countingChildren
	s1, _ := decorate(t, &v1, c)
	s2, _ := decorate(t, &v2, c)
	_ = s1.Get(requestFor("p", "get"), "/k", storage.GetOptions{}, nil)
	_ = s2.Get(requestFor("p", "get"), "/k", storage.GetOptions{}, nil)

	deleteProject(t, client, "p")
	waitFor(t, "both versions' children to be destroyed", func() bool {
		return v1.destroyed.Load() == 1 && v2.destroyed.Load() == 1
	})
}

// TestRemovedChildServesUntilTheDrain covers a request already holding a
// child when its Project goes: the child leaves the map at once, and its
// storage stops only after the drain.
func TestRemovedChildServesUntilTheDrain(t *testing.T) {
	var children countingChildren
	projects := everyProjectKnown()
	m := newTestMux(nil, projects)
	m.inner, m.drain = children.storage, 200*time.Millisecond
	_ = m.Get(requestFor("p", "get"), "/k", storage.GetOptions{}, nil)

	m.ProjectGone("p")
	if n := children.destroyed.Load(); n != 0 {
		t.Fatalf("expected the child to serve until the drain, got %d destroyed at once", n)
	}
	waitFor(t, "the drained child to be destroyed", func() bool { return children.destroyed.Load() == 1 })
}

// listenerCount is a ProjectLookup that knows every Project and counts the
// listeners registered with it now.
type listenerCount struct {
	*fakeProjects
	active atomic.Int32
}

func (l *listenerCount) AddListener(projectstate.Listener) func() {
	l.active.Add(1)
	return func() { l.active.Add(-1) }
}

// TestDestroyAllStopsTheViewsReports covers a storage torn down with its CRD:
// it leaves the view's listeners, so no CRD rebuild leaves a dead mux behind.
func TestDestroyAllStopsTheViewsReports(t *testing.T) {
	projects := &listenerCount{fakeProjects: everyProjectKnown()}
	var children countingChildren
	_, destroy, err := ProjectAwareDecorator(schema.GroupResource{Resource: "configmaps"}, children.storage, projects, nil, testDrain)(
		&storagebackend.ConfigForResource{}, "", nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("decorate configmaps: %v", err)
	}
	if n := projects.active.Load(); n != 1 {
		t.Fatalf("expected the mux to listen to the view, got %d listeners", n)
	}
	destroy()
	if n := projects.active.Load(); n != 0 {
		t.Fatalf("expected the torn-down mux to stop listening, got %d listeners", n)
	}
}

// TestRefusalsAreCountedAsReadsOrWrites keeps both refusals visible: a read
// and a write into a missing Project each count under their own name.
func TestRefusalsAreCountedAsReadsOrWrites(t *testing.T) {
	m := newTestMux(nil, noProjectKnown())
	m.args.resourceKind = "refusal-classes"
	counters := map[string]*k8smetrics.CounterVec{"reads": unknownProjectReadsRefused, "writes": unknownProjectWritesRefused}
	before := map[string]float64{}
	for name, counter := range counters {
		before[name] = refusals(t, counter)
	}
	_ = m.Get(requestFor("p", "list"), "/k", storage.GetOptions{}, nil)
	_ = m.Create(requestFor("p", "create"), "/k", nil, nil, 0)

	for name, counter := range counters {
		if got := refusals(t, counter) - before[name]; got != 1 {
			t.Fatalf("expected one %s refusal counted, got %v", name, got)
		}
	}
}

// refusals reads one refusal counter's NotFound count for this test's kind.
func refusals(t *testing.T, counter *k8smetrics.CounterVec) float64 {
	t.Helper()
	got, err := testutil.GetCounterMetricValue(counter.WithLabelValues("quota.miloapis.com", "refusal-classes", "NotFound"))
	if err != nil {
		t.Fatalf("read the refusal counter: %v", err)
	}
	return got
}

// TestConcurrentRequestsAndDeletesLeaveNoChildForAGoneName runs reads into
// Projects while they are deleted. After the view's resync no child is left
// for a name the view does not hold, whatever the order was.
func TestConcurrentRequestsAndDeletesLeaveNoChildForAGoneName(t *testing.T) {
	names := []string{"a", "b", "c", "d"}
	var objects []runtime.Object
	for _, n := range names {
		objects = append(objects, projectstatetest.NewProjectWithUID(n, types.UID("uid-"+n)))
	}
	c, client := projectstatetest.NewCache(t, objects...)
	var children countingChildren
	s, _ := decorate(t, &children, c)

	done := make(chan struct{})
	for _, n := range names {
		go func() {
			for range 200 {
				_ = s.Get(requestFor(n, "get"), "/k", storage.GetOptions{}, nil)
			}
			done <- struct{}{}
		}()
		deleteProject(t, client, n)
	}
	for range names {
		<-done
	}
	waitFor(t, "the view to drop every Project", func() bool { return projectsHeld(c, names) == 0 })
	s.(projectstate.Listener).ViewResynced()
	m := s.(*projectMux)
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.children) != 1 {
		t.Fatalf("expected only the root plane's child, got %d children", len(m.children))
	}
}

// projectsHeld counts the names the view holds.
func projectsHeld(c *projectstate.Cache, names []string) int {
	n := 0
	for _, name := range names {
		if _, ok := c.Project(name); ok {
			n++
		}
	}
	return n
}
