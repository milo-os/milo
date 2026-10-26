package projectpurge

import (
	"context"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

// clusterDeletes returns the DELETEs the purge sent to cluster-scoped kinds.
func clusterDeletes(paths []string) []string {
	return slices.DeleteFunc(slices.Clone(paths), func(p string) bool {
		return !strings.HasPrefix(p, "/apis/") || strings.Contains(p, "/namespaces/")
	})
}

func TestStartPurge_DeletesNoClusterScopedKindWhenAPIServicesAreNotFound(t *testing.T) {
	stub := newProjectPlaneStub()
	delete(stub.docs, "/apis/apiregistration.k8s.io/v1/apiservices")
	srv := httptest.NewServer(stub)
	defer srv.Close()

	err := New().StartPurge(context.Background(), &rest.Config{Host: srv.URL}, "doomed", Options{})
	if err == nil {
		t.Error("expected an error when the purge cannot tell which kinds the project stores")
	}
	if got := clusterDeletes(stub.deleted()); len(got) != 0 {
		t.Fatalf("expected no cluster-scoped deletes, got %v", got)
	}
}

func TestStartPurge_LeavesAGroupNoAPIServiceNames(t *testing.T) {
	stub := newProjectPlaneStub()
	groups := stub.docs["/apis"].(*metav1.APIGroupList)
	gv := metav1.GroupVersionForDiscovery{GroupVersion: "unregistered.example.com/v1", Version: "v1"}
	groups.Groups = append(groups.Groups, metav1.APIGroup{Name: "unregistered.example.com", Versions: []metav1.GroupVersionForDiscovery{gv}, PreferredVersion: gv})
	stub.docs["/apis/unregistered.example.com/v1"] = &metav1.APIResourceList{
		TypeMeta:     metav1.TypeMeta{Kind: "APIResourceList", APIVersion: "v1"},
		GroupVersion: "unregistered.example.com/v1",
		APIResources: []metav1.APIResource{{Name: "widgets", Kind: "Widget", Verbs: allVerbs}},
	}
	srv := httptest.NewServer(stub)
	defer srv.Close()

	if err := New().StartPurge(context.Background(), &rest.Config{Host: srv.URL}, "doomed", Options{}); err != nil {
		t.Fatalf("StartPurge: %v", err)
	}
	got := clusterDeletes(stub.deleted())
	if slices.ContainsFunc(got, func(p string) bool { return strings.HasPrefix(p, "/apis/unregistered.example.com/") }) {
		t.Errorf("a group no APIService names was purged: %v", got)
	}
	if !slices.Contains(got, "/apis/services.miloapis.com/v1alpha1/serviceentitlements") {
		t.Errorf("the project's own kind was not purged: %v", got)
	}
}

func TestStartPurge_DeletesNamespacesWhenAPIServicesCannotBeRead(t *testing.T) {
	stub := newProjectPlaneStub()
	stub.docs["/api/v1/namespaces"] = &corev1.NamespaceList{
		TypeMeta: metav1.TypeMeta{Kind: "NamespaceList", APIVersion: "v1"},
		Items:    []corev1.Namespace{{ObjectMeta: metav1.ObjectMeta{Name: "team-a"}}},
	}
	stub.broken = map[string]bool{"/apis/apiregistration.k8s.io/v1/apiservices": true}
	srv := httptest.NewServer(stub)
	defer srv.Close()

	if err := New().StartPurge(context.Background(), &rest.Config{Host: srv.URL}, "doomed", Options{}); err == nil {
		t.Error("expected the failed APIService read to be returned")
	}
	if !slices.Contains(stub.deleted(), "/api/v1/namespaces/team-a") {
		t.Fatalf("a failed APIService read kept the project's namespaces: deletes %v", stub.deleted())
	}
}
