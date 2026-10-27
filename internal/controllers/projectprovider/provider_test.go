package projectprovider

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

var projectGVR = schema.GroupVersionResource{Group: "resourcemanager.miloapis.com", Version: "v1alpha1", Resource: "projects"}

// recordingSink records each AddProject and RemoveProject. An AddProject for a
// name in hold waits until that name's channel closes or its context ends, as a
// sink waiting on a cache that does not sync does.
type recordingSink struct {
	mu      sync.Mutex
	added   []string
	removed []string
	hold    map[string]chan struct{}
}

func (s *recordingSink) AddProject(ctx context.Context, id string, _ *rest.Config) error {
	s.mu.Lock()
	s.added = append(s.added, id)
	ch := s.hold[id]
	s.mu.Unlock()
	if ch == nil {
		return nil
	}
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *recordingSink) RemoveProject(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removed = append(s.removed, id)
}

// calls returns copies of the names added and removed so far.
func (s *recordingSink) calls() (added, removed []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.added), slices.Clone(s.removed)
}

// eventually fails the test unless cond holds within 10 s.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// startEnv starts an envtest API server serving Milo's Project CRD and returns
// its config and a dynamic client.
func startEnv(t *testing.T) (*rest.Config, dynamic.Interface) {
	t.Helper()
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS is not set; run setup-envtest to provide an envtest API server")
	}
	env := &envtest.Environment{CRDInstallOptions: envtest.CRDInstallOptions{Paths: []string{
		filepath.Join("..", "..", "..", "config", "crd", "bases", "resourcemanager", "resourcemanager.miloapis.com_projects.yaml"),
	}}}
	cfg, err := env.Start()
	if err != nil {
		t.Fatalf("start envtest: %v", err)
	}
	t.Cleanup(func() { _ = env.Stop() })
	return cfg, dynamic.NewForConfigOrDie(cfg)
}

// runProvider starts a provider on cfg with sink, stopped when the test ends.
func runProvider(t *testing.T, cfg *rest.Config, sink Sink) {
	t.Helper()
	p, err := New(cfg, sink)
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = p.Run(ctx) }()
}

func newProject(name string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion("resourcemanager.miloapis.com/v1alpha1")
	u.SetKind("Project")
	u.SetName(name)
	_ = unstructured.SetNestedMap(u.Object, map[string]any{"kind": "Organization", "name": "org"}, "spec", "ownerRef")
	return u
}

func create(t *testing.T, dyn dynamic.Interface, name string) {
	t.Helper()
	if _, err := dyn.Resource(projectGVR).Create(context.Background(), newProject(name), metav1.CreateOptions{}); err != nil {
		t.Fatalf("create Project %s: %v", name, err)
	}
}

func remove(t *testing.T, dyn dynamic.Interface, name string) {
	t.Helper()
	if err := dyn.Resource(projectGVR).Delete(context.Background(), name, metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete Project %s: %v", name, err)
	}
}

// TestDeletedProjectsWaitDoesNotFreezeLaterProjects covers one engagement that
// cannot finish, as a sink waiting on the cache of a Project that has gone: its
// Project's delete ends the wait, and the next Project is engaged.
func TestDeletedProjectsWaitDoesNotFreezeLaterProjects(t *testing.T) {
	cfg, dyn := startEnv(t)
	sink := &recordingSink{hold: map[string]chan struct{}{"stuck": make(chan struct{})}}
	runProvider(t, cfg, sink)

	create(t, dyn, "stuck")
	eventually(t, "stuck's engagement to start", func() bool { a, _ := sink.calls(); return slices.Contains(a, "stuck") })
	remove(t, dyn, "stuck")
	create(t, dyn, "later")
	eventually(t, "the later Project to be engaged", func() bool { a, _ := sink.calls(); return slices.Contains(a, "later") })
}

// TestProjectGoneBeforeItsAddIsNotEngaged covers a Project created and deleted
// while the provider is busy with another: when the provider reaches it, it has
// gone, so it is never engaged.
func TestProjectGoneBeforeItsAddIsNotEngaged(t *testing.T) {
	cfg, dyn := startEnv(t)
	release := make(chan struct{})
	sink := &recordingSink{hold: map[string]chan struct{}{"busy": release}}
	runProvider(t, cfg, sink)

	create(t, dyn, "busy")
	eventually(t, "busy's engagement to start", func() bool { a, _ := sink.calls(); return slices.Contains(a, "busy") })
	create(t, dyn, "gone")
	remove(t, dyn, "gone")
	create(t, dyn, "sentinel")
	time.Sleep(500 * time.Millisecond) // the informer sees all three before the provider is free
	close(release)
	eventually(t, "the sentinel to be engaged", func() bool { a, _ := sink.calls(); return slices.Contains(a, "sentinel") })
	if added, _ := sink.calls(); slices.Contains(added, "gone") {
		t.Fatalf("expected the gone Project never to be engaged, got adds %v", added)
	}
}

// TestProjectCreatedAgainIsEngagedAgain covers a name deleted and created again:
// the old engagement is removed and the new Project is engaged.
func TestProjectCreatedAgainIsEngagedAgain(t *testing.T) {
	cfg, dyn := startEnv(t)
	sink := &recordingSink{}
	runProvider(t, cfg, sink)

	create(t, dyn, "p")
	eventually(t, "p to be engaged", func() bool { a, _ := sink.calls(); return slices.Contains(a, "p") })
	remove(t, dyn, "p")
	create(t, dyn, "p")
	eventually(t, "p to be removed and engaged again", func() bool {
		a, r := sink.calls()
		return len(r) == 1 && len(a) == 2
	})
}
