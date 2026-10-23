package projectstate_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/server"
	clienttesting "k8s.io/client-go/testing"

	"go.miloapis.com/milo/internal/apiserver/projectstate"
	"go.miloapis.com/milo/internal/apiserver/projectstate/projectstatetest"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
)

// waitForSuspension polls the cache until the named Project's suspension
// matches wantSuspended or the timeout elapses, since watch-event delivery
// through the fake dynamic client is asynchronous with respect to the
// calling goroutine.
func waitForSuspension(t *testing.T, c *projectstate.Cache, name string, wantSuspended bool) *projectstate.Suspension {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		state := c.Suspension(name)
		suspended := state != nil && state.Suspended
		if suspended == wantSuspended {
			return state
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for suspended=%v, last state=%+v", wantSuspended, state)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestCache_RealtimeUpdatesNoRequestGetCalls(t *testing.T) {
	c, client := projectstatetest.NewCache(t, projectstatetest.NewProject("proj-1", true, resourcemanagerv1alpha1.ReasonFraud))

	// Suspension has no live API fallback at all — this reactor is a
	// structural regression guard: it fails the test immediately if that
	// ever changes, rather than merely counting calls.
	client.PrependReactor("get", "projects", func(action clienttesting.Action) (bool, runtime.Object, error) {
		t.Fatalf("Suspension must never call the API server directly")
		return false, nil, nil
	})

	// 1. Steady-state check: project is suspended in cache.
	state := waitForSuspension(t, c, "proj-1", true)
	if len(state.Suspensions) != 1 || state.Suspensions[0].Reason != resourcemanagerv1alpha1.ReasonFraud {
		t.Errorf("expected one %q suspension, got %+v", resourcemanagerv1alpha1.ReasonFraud, state.Suspensions)
	}

	// 2. Unsuspend the project via the fake dynamic client, so a real watch
	// event flows through the reflector's Update path into the cache map
	// (rather than poking the map directly, which would not exercise that
	// wiring at all).
	activeProj := projectstatetest.NewProject("proj-1", false)
	activeProj.SetResourceVersion("999")
	if _, err := client.Resource(projectstate.ProjectGVR).Update(context.Background(), activeProj, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("failed to update project via fake client: %v", err)
	}

	// 3. Verify real-time cache update: project is no longer suspended.
	waitForSuspension(t, c, "proj-1", false)
}

func TestCache_TerminatingProjectAndFinalizerRemoval(t *testing.T) {
	proj := projectstatetest.NewProject("proj-terminating", true, resourcemanagerv1alpha1.ReasonFraud)
	c, client := projectstatetest.NewCache(t, proj)

	// 1. Verify project is initially cached as suspended.
	waitForSuspension(t, c, "proj-terminating", true)

	// 2. Simulate setting a deletionTimestamp and finalizer on the suspended project (terminating state).
	terminatingProj := proj.DeepCopy()
	terminatingProj.SetDeletionTimestamp(&metav1.Time{Time: time.Now()})
	terminatingProj.SetFinalizers([]string{"example.com/finalizer"})
	terminatingProj.SetResourceVersion("1001")
	if _, err := client.Resource(projectstate.ProjectGVR).Update(context.Background(), terminatingProj, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("failed to update terminating project: %v", err)
	}

	// While still suspended, it remains cached as suspended during termination updates.
	waitForSuspension(t, c, "proj-terminating", true)

	// 3. Simulate finalizer removal leading to actual Delete event.
	if err := client.Resource(projectstate.ProjectGVR).Delete(context.Background(), "proj-terminating", metav1.DeleteOptions{}); err != nil {
		t.Fatalf("failed to delete project: %v", err)
	}

	// 4. Verify Delete event flows through reflector.Delete and evicts the project from cache.
	waitForSuspension(t, c, "proj-terminating", false)
}

// TestPostStartHookStartsCacheAndReadinessWaitsForFirstList covers the
// server's start-up order: the cache starts only when the post-start hook
// runs, and the readiness check fails until its first List has completed.
func TestPostStartHookStartsCacheAndReadinessWaitsForFirstList(t *testing.T) {
	client := projectstatetest.NewClient(t, projectstatetest.NewProject("proj-1", true, resourcemanagerv1alpha1.ReasonFraud))
	var lists atomic.Int32
	release := make(chan struct{})
	client.PrependReactor("list", "projects", func(clienttesting.Action) (bool, runtime.Object, error) {
		lists.Add(1)
		<-release
		return false, nil, nil
	})

	c := projectstate.NewCache(client)
	check := c.ReadinessCheck()
	if err := check.Check(nil); err == nil {
		t.Fatal("readiness check passed before the cache started")
	}
	if got := lists.Load(); got != 0 {
		t.Fatalf("cache listed %d times before the post-start hook ran", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := c.PostStartHook()(server.PostStartHookContext{Context: ctx}); err != nil {
		t.Fatalf("post-start hook: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for lists.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the post-start hook did not start the cache")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := check.Check(nil); err == nil {
		t.Fatal("readiness check passed while the first List was in flight")
	}

	close(release)
	for check.Check(nil) != nil {
		if time.Now().After(deadline) {
			t.Fatalf("readiness check still failing after the first List: %v", check.Check(nil))
		}
		time.Sleep(5 * time.Millisecond)
	}
	waitForSuspension(t, c, "proj-1", true)
	if got := lists.Load(); got != 1 {
		t.Errorf("cache listed %d times, want 1", got)
	}
}

// waitForProject polls the cache until the named Project reads as want, or
// as unknown when known is false.
func waitForProject(t *testing.T, c *projectstate.Cache, name string, want projectstate.Project, known bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		got, ok := c.Project(name)
		if ok == known && (!known || got == want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s known=%v %+v, last known=%v %+v", name, known, want, ok, got)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestCache_ProjectFollowsUIDAndDeletion covers what the namespace bootstrap
// reads: every Project, suspended or not, by its UID and its deletion mark,
// through a deletion and a re-creation under the same name.
func TestCache_ProjectFollowsUIDAndDeletion(t *testing.T) {
	live := projectstatetest.NewProject("p", false)
	live.SetUID("uid-1")
	c, client := projectstatetest.NewCache(t, live)
	projects := client.Resource(projectstate.ProjectGVR)
	ctx := context.Background()

	waitForProject(t, c, "p", projectstate.Project{UID: "uid-1"}, true)
	if _, ok := c.Project("missing"); ok {
		t.Fatal("a project that does not exist reads as known")
	}

	deleting := live.DeepCopy()
	deleting.SetDeletionTimestamp(&metav1.Time{Time: time.Now()})
	deleting.SetResourceVersion("2")
	if _, err := projects.Update(ctx, deleting, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("mark project deleting: %v", err)
	}
	waitForProject(t, c, "p", projectstate.Project{UID: "uid-1", Deleting: true}, true)

	if err := projects.Delete(ctx, "p", metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete project: %v", err)
	}
	waitForProject(t, c, "p", projectstate.Project{}, false)

	recreated := projectstatetest.NewProject("p", true, resourcemanagerv1alpha1.ReasonFraud)
	recreated.SetUID("uid-2")
	if _, err := projects.Create(ctx, recreated, metav1.CreateOptions{}); err != nil {
		t.Fatalf("re-create project: %v", err)
	}
	waitForProject(t, c, "p", projectstate.Project{UID: "uid-2"}, true)
	waitForSuspension(t, c, "p", true)
}

// TestCache_ResyncKeepsEveryProject covers the initial List and every
// re-list: a Project that is not suspended is still known by its UID.
func TestCache_ResyncKeepsEveryProject(t *testing.T) {
	active := projectstatetest.NewProject("active", false)
	active.SetUID("uid-a")
	suspended := projectstatetest.NewProject("suspended", true, resourcemanagerv1alpha1.ReasonFraud)
	suspended.SetUID("uid-s")
	c, _ := projectstatetest.NewCache(t, active, suspended)

	if got, ok := c.Project("active"); !ok || got != (projectstate.Project{UID: "uid-a"}) {
		t.Errorf("active project after the initial List: known=%v %+v", ok, got)
	}
	if got, ok := c.Project("suspended"); !ok || got != (projectstate.Project{UID: "uid-s"}) {
		t.Errorf("suspended project after the initial List: known=%v %+v", ok, got)
	}
	if c.Suspension("active") != nil {
		t.Error("an active project reads as suspended")
	}
	if c.Suspension("suspended") == nil {
		t.Error("a suspended project reads as not suspended")
	}
}
