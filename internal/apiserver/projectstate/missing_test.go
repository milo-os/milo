package projectstate_test

import (
	"context"
	"sync"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	"go.miloapis.com/milo/internal/apiserver/projectstate"
	"go.miloapis.com/milo/internal/apiserver/projectstate/projectstatetest"
)

// TestLookup_KeepsANotFoundForItsTTL covers a loop of invented names: a name
// storage answered NotFound for costs no second read until the TTL, upstream
// NamespaceLifecycle's force-live TTL of 30 s, has passed.
func TestLookup_KeepsANotFoundForItsTTL(t *testing.T) {
	clk := clocktesting.NewFakeClock(time.Now())
	c, client := projectstatetest.NewCacheWithClock(t, clk)
	gets := projectstatetest.StorageAnswers(client, nil, apierrors.NewNotFound(projectstate.ProjectGVR.GroupResource(), "x"))

	for range 3 {
		if _, err := c.Lookup(context.Background(), "x"); !apierrors.IsNotFound(err) {
			t.Fatalf("expected NotFound, got %v", err)
		}
	}
	if n := gets.Load(); n != 1 {
		t.Fatalf("expected one storage read within the TTL, got %d", n)
	}
	clk.Step(31 * time.Second)
	_, _ = c.Lookup(context.Background(), "x")
	if n := gets.Load(); n != 2 {
		t.Fatalf("expected a second storage read after the TTL, got %d", n)
	}
}

// TestLookup_AnswersANameCreatedAfterItsNotFound covers a Project created
// under a name just found missing: the reflector is asked first, so the new
// Project is answered as soon as it is seen, within the TTL.
func TestLookup_AnswersANameCreatedAfterItsNotFound(t *testing.T) {
	c, client := projectstatetest.NewCache(t)
	if _, err := c.Lookup(context.Background(), "x"); !apierrors.IsNotFound(err) {
		t.Fatalf("expected NotFound before the create, got %v", err)
	}
	if _, err := client.Resource(projectstate.ProjectGVR).Create(context.Background(),
		projectstatetest.NewProjectWithUID("x", "uid-1"), metav1.CreateOptions{}); err != nil {
		t.Fatalf("create the Project: %v", err)
	}
	waitForProject(t, c, "x", projectstate.Project{UID: "uid-1"}, true)

	if p, err := c.Lookup(context.Background(), "x"); err != nil || p.UID != "uid-1" {
		t.Fatalf("expected the new Project, got %+v, %v", p, err)
	}
}

// recorder is a Listener that records what it hears.
type recorder struct {
	mu       sync.Mutex
	gone     []string
	resynced int
}

func (r *recorder) ProjectGone(name string) {
	r.mu.Lock()
	r.gone = append(r.gone, name)
	r.mu.Unlock()
}
func (r *recorder) ViewResynced() { r.mu.Lock(); r.resynced++; r.mu.Unlock() }

func (r *recorder) heard() ([]string, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.gone...), r.resynced
}

// TestListeners_HearTheListAndEachDelete covers what a listener hears: the
// first list is a relist, a deleted Project is named, and a removed listener
// hears nothing more.
func TestListeners_HearTheListAndEachDelete(t *testing.T) {
	client := projectstatetest.NewClient(t, projectstatetest.NewProjectWithUID("a", "uid-a"),
		projectstatetest.NewProjectWithUID("b", "uid-b"))
	c := projectstate.NewCache(client)
	var r recorder
	remove := c.AddListener(&r)
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	c.Start(stop)
	waitForProject(t, c, "a", projectstate.Project{UID: "uid-a"}, true)

	if err := client.Resource(projectstate.ProjectGVR).Delete(context.Background(), "a", metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete a: %v", err)
	}
	waitForProject(t, c, "a", projectstate.Project{}, false)
	remove()
	if err := client.Resource(projectstate.ProjectGVR).Delete(context.Background(), "b", metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete b: %v", err)
	}
	waitForProject(t, c, "b", projectstate.Project{}, false)

	if gone, resynced := r.heard(); len(gone) != 1 || gone[0] != "a" || resynced != 1 {
		t.Fatalf("expected the first list and a's delete only, got gone=%v resynced=%d", gone, resynced)
	}
}
