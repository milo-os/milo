package projectprovider

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sync/atomic"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic/fake"
	clientfeatures "k8s.io/client-go/features"
	clientfeaturestesting "k8s.io/client-go/features/testing"
	"k8s.io/client-go/rest"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
)

// TestTombstoneRemovesTheProject covers a delete the provider sees only at a
// relist: the informer hands DeleteFunc a DeletedFinalStateUnknown, and the
// Project's engagement is removed.
// fakeProvider runs a provider on a fake client holding objects, and returns the client and the provider's
// watches in the order the informer opens them, so a test can expire one and make the informer list again.
func fakeProvider(t *testing.T, sink Sink, objects ...runtime.Object) (*fake.FakeDynamicClient, <-chan *watch.FakeWatcher) {
	t.Helper()
	clientfeaturestesting.SetFeatureDuringTest(t, clientfeatures.WatchListClient, false)
	client := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{projectGVR: "ProjectList"}, objects...)
	watchers := make(chan *watch.FakeWatcher, 8)
	client.PrependWatchReactor("projects", func(clienttesting.Action) (bool, watch.Interface, error) {
		w := watch.NewFake()
		watchers <- w
		return true, w, nil
	})
	p := &Provider{root: &rest.Config{Host: "https://milo.example"}, dyn: client, sink: sink, projectGVR: projectGVR}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = p.Run(ctx) }()
	return client, watchers
}

// expire ends the provider's watch as storage does for a stale one, so the informer lists again.
func expire(w *watch.FakeWatcher) {
	w.Error(&metav1.Status{Status: metav1.StatusFailure, Code: http.StatusGone, Reason: metav1.StatusReasonExpired})
}

func TestTombstoneRemovesTheProject(t *testing.T) {
	sink := &recordingSink{}
	client, watchers := fakeProvider(t, sink, newProject("p"))
	eventually(t, "p to be engaged", func() bool { a, _ := sink.calls(); return slices.Contains(a, "p") })

	// The Project goes from storage with no event on the provider's watch, and the watch then expires, so the
	// informer lists again and learns of the delete only from the list.
	if err := client.Tracker().Delete(projectGVR, "", "p"); err != nil {
		t.Fatalf("delete p from the fake's storage: %v", err)
	}
	expire(<-watchers)
	eventually(t, "p's engagement to be removed", func() bool { _, r := sink.calls(); return slices.Contains(r, "p") })
}

// TestRecreationSeenAtARelistIsEngagedAgain covers a Project deleted and created
// again while the provider's watch was down: the relist reports an update with a
// new UID, and the old engagement is removed before the new one is engaged.
func TestRecreationSeenAtARelistIsEngagedAgain(t *testing.T) {
	sink := &recordingSink{}
	old := newProject("p")
	old.SetUID("uid-1")
	client, watchers := fakeProvider(t, sink, old)
	eventually(t, "p to be engaged", func() bool { a, _ := sink.calls(); return slices.Contains(a, "p") })

	again := newProject("p")
	again.SetUID("uid-2")
	if err := client.Tracker().Update(projectGVR, again, ""); err != nil {
		t.Fatalf("replace p in the fake's storage: %v", err)
	}
	expire(<-watchers)
	eventually(t, "p to be removed and engaged again", func() bool { a, r := sink.calls(); return len(r) == 1 && len(a) == 2 })
}

// failingSink fails AddProject for its first failures calls, then records adds.
type failingSink struct {
	recordingSink
	failures atomic.Int32
}

func (s *failingSink) AddProject(ctx context.Context, id string, cfg *rest.Config) error {
	if s.failures.Add(-1) >= 0 {
		return errors.New("the project's caches did not sync")
	}
	return s.recordingSink.AddProject(ctx, id, cfg)
}

// TestFailedEngagementIsRetriedWhileTheProjectExists covers a sink that fails to
// engage a live Project: the provider tries again until it succeeds.
func TestFailedEngagementIsRetriedWhileTheProjectExists(t *testing.T) {
	sink := &failingSink{}
	sink.failures.Store(3)
	fakeProvider(t, sink, newProject("p"))
	eventually(t, "p to be engaged after its failures", func() bool { a, _ := sink.calls(); return slices.Contains(a, "p") })
}

// TestDeleteEndsOnlyItsOwnProjectsEngagement covers a delete delivered late: the
// informer's store already holds a Project created again under the name, and
// the worker is engaging it. The old Project's delete must not end that wait.
func TestDeleteEndsOnlyItsOwnProjectsEngagement(t *testing.T) {
	e := newEngagements(context.Background(), &Provider{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e.current["p"] = engagement{uid: "uid-2", cancel: cancel}

	old := newProject("p")
	old.SetUID("uid-1")
	e.cancelDeleted(cache.DeletedFinalStateUnknown{Key: "p", Obj: old})
	if ctx.Err() != nil {
		t.Fatal("expected the old Project's delete to leave the new engagement running")
	}
	current := newProject("p")
	current.SetUID("uid-2")
	e.cancelDeleted(current)
	if ctx.Err() == nil {
		t.Fatal("expected the Project's own delete to end its engagement")
	}
}
