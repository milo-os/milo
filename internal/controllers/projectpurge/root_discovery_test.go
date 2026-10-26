package projectpurge

import (
	"context"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
)

var rbacV1 = schema.GroupVersion{Group: "rbac.authorization.k8s.io", Version: "v1"}

// hiddenKindStubs returns a root whose discovery lists clusterroles, and a
// project plane that stores them while its own discovery hides them, as
// DiscoveryContextFilter hides Platform-context kinds from a project.
func hiddenKindStubs() (root, project *projectPlaneStub) {
	root = newProjectPlaneStub()
	root.serve(rbacV1, metav1.APIResource{Name: "clusterroles", Kind: "ClusterRole", Verbs: allVerbs})
	project = newProjectPlaneStub()
	project.storeLocally(rbacV1)
	return root, project
}

func TestStartPurge_DeletesKindsTheProjectsDiscoveryHides(t *testing.T) {
	root, project := hiddenKindStubs()
	rootSrv, projectSrv := httptest.NewServer(root), httptest.NewServer(project)
	defer rootSrv.Close()
	defer projectSrv.Close()

	err := New(&rest.Config{Host: rootSrv.URL}).StartPurge(context.Background(), &rest.Config{Host: projectSrv.URL}, "doomed", Options{})
	if err != nil {
		t.Fatalf("StartPurge: %v", err)
	}
	if got := project.deleted(); !slices.Contains(got, "/apis/rbac.authorization.k8s.io/v1/clusterroles") {
		t.Errorf("the project's clusterroles were not purged: deletes %v", got)
	}
	if got := root.deleted(); len(got) != 0 {
		t.Errorf("the purge deleted at root: %v", got)
	}
}

func TestStatus_ReportsHeldObjectsOfKindsTheProjectsDiscoveryHides(t *testing.T) {
	root, project := hiddenKindStubs()
	project.docs["/apis/rbac.authorization.k8s.io/v1/clusterroles"] = map[string]any{
		"apiVersion": "rbac.authorization.k8s.io/v1",
		"kind":       "ClusterRoleList",
		"metadata":   map[string]any{},
		"items": []any{map[string]any{
			"apiVersion": "rbac.authorization.k8s.io/v1",
			"kind":       "ClusterRole",
			"metadata":   map[string]any{"name": "reader", "finalizers": []any{"example.com/hold"}},
		}},
	}
	rootSrv, projectSrv := httptest.NewServer(root), httptest.NewServer(project)
	defer rootSrv.Close()
	defer projectSrv.Close()

	status, err := New(&rest.Config{Host: rootSrv.URL}).Status(context.Background(), &rest.Config{Host: projectSrv.URL}, "doomed")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Complete || !strings.Contains(status.Message(), `clusterroles "reader"`) {
		t.Fatalf("expected the held clusterrole to block completion, got %q", status.Message())
	}
}
