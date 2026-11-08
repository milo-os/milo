package projectpurge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
)

var allVerbs = metav1.Verbs{"create", "delete", "deletecollection", "get", "list", "patch", "update", "watch"}

type projectPlaneStub struct {
	mu      sync.Mutex
	deletes []string
	docs    map[string]any
	broken  map[string]bool
}

func newProjectPlaneStub() *projectPlaneStub {
	group := func(name, version string) metav1.APIGroup {
		gv := metav1.GroupVersionForDiscovery{GroupVersion: name + "/" + version, Version: version}
		return metav1.APIGroup{Name: name, Versions: []metav1.GroupVersionForDiscovery{gv}, PreferredVersion: gv}
	}
	resources := func(gv string, rs ...metav1.APIResource) *metav1.APIResourceList {
		return &metav1.APIResourceList{TypeMeta: metav1.TypeMeta{Kind: "APIResourceList", APIVersion: "v1"}, GroupVersion: gv, APIResources: rs}
	}
	activityService := &apiregistrationv1.ServiceReference{Namespace: "activity-system", Name: "activity-apiserver"}

	return &projectPlaneStub{docs: map[string]any{
		"/api": &metav1.APIVersions{TypeMeta: metav1.TypeMeta{Kind: "APIVersions"}, Versions: []string{"v1"}},
		"/apis": &metav1.APIGroupList{TypeMeta: metav1.TypeMeta{Kind: "APIGroupList", APIVersion: "v1"}, Groups: []metav1.APIGroup{
			group("apiregistration.k8s.io", "v1"),
			group("activity.miloapis.com", "v1alpha1"),
			group("services.miloapis.com", "v1alpha1"),
		}},
		"/api/v1": resources("v1",
			metav1.APIResource{Name: "namespaces", Kind: "Namespace", Verbs: allVerbs},
			metav1.APIResource{Name: "configmaps", Namespaced: true, Kind: "ConfigMap", Verbs: allVerbs},
		),
		"/apis/apiregistration.k8s.io/v1": resources("apiregistration.k8s.io/v1",
			metav1.APIResource{Name: "apiservices", Kind: "APIService", Verbs: allVerbs},
			metav1.APIResource{Name: "apiservices/status", Kind: "APIService", Verbs: metav1.Verbs{"get", "patch", "update"}},
		),
		"/apis/activity.miloapis.com/v1alpha1": resources("activity.miloapis.com/v1alpha1",
			metav1.APIResource{Name: "activitypolicies", Kind: "ActivityPolicy", Verbs: allVerbs},
		),
		"/apis/services.miloapis.com/v1alpha1": resources("services.miloapis.com/v1alpha1",
			metav1.APIResource{Name: "serviceentitlements", Kind: "ServiceEntitlement", Verbs: allVerbs},
		),
		"/api/v1/namespaces": &corev1.NamespaceList{TypeMeta: metav1.TypeMeta{Kind: "NamespaceList", APIVersion: "v1"}},
		"/apis/apiregistration.k8s.io/v1/apiservices": &apiregistrationv1.APIServiceList{
			TypeMeta: metav1.TypeMeta{Kind: "APIServiceList", APIVersion: "apiregistration.k8s.io/v1"},
			Items: []apiregistrationv1.APIService{
				{
					TypeMeta:   metav1.TypeMeta{Kind: "APIService", APIVersion: "apiregistration.k8s.io/v1"},
					ObjectMeta: metav1.ObjectMeta{Name: "v1.apiregistration.k8s.io"},
					Spec:       apiregistrationv1.APIServiceSpec{Group: "apiregistration.k8s.io", Version: "v1"},
				},
				{
					TypeMeta:   metav1.TypeMeta{Kind: "APIService", APIVersion: "apiregistration.k8s.io/v1"},
					ObjectMeta: metav1.ObjectMeta{Name: "v1alpha1.activity.miloapis.com"},
					Spec:       apiregistrationv1.APIServiceSpec{Group: "activity.miloapis.com", Version: "v1alpha1", Service: activityService},
				},
				{
					TypeMeta:   metav1.TypeMeta{Kind: "APIService", APIVersion: "apiregistration.k8s.io/v1"},
					ObjectMeta: metav1.ObjectMeta{Name: "v1alpha1.services.miloapis.com"},
					Spec:       apiregistrationv1.APIServiceSpec{Group: "services.miloapis.com", Version: "v1alpha1"},
				},
			},
		},
		"/apis/activity.miloapis.com/v1alpha1/activitypolicies": map[string]any{
			"apiVersion": "activity.miloapis.com/v1alpha1",
			"kind":       "ActivityPolicyList",
			"metadata":   map[string]any{},
			"items": []any{map[string]any{
				"apiVersion": "activity.miloapis.com/v1alpha1",
				"kind":       "ActivityPolicy",
				"metadata":   map[string]any{"name": "dns-zones", "finalizers": []any{"activity.miloapis.com/policy"}},
			}},
		},
		"/apis/services.miloapis.com/v1alpha1/serviceentitlements": map[string]any{
			"apiVersion": "services.miloapis.com/v1alpha1",
			"kind":       "ServiceEntitlementList",
			"metadata":   map[string]any{},
			"items":      []any{},
		},
	}}
}

// serve adds gv to the stub's discovery with the given resources.
func (s *projectPlaneStub) serve(gv schema.GroupVersion, rs ...metav1.APIResource) {
	groups := s.docs["/apis"].(*metav1.APIGroupList)
	version := metav1.GroupVersionForDiscovery{GroupVersion: gv.String(), Version: gv.Version}
	groups.Groups = append(groups.Groups, metav1.APIGroup{Name: gv.Group, Versions: []metav1.GroupVersionForDiscovery{version}, PreferredVersion: version})
	s.docs["/apis/"+gv.String()] = &metav1.APIResourceList{
		TypeMeta:     metav1.TypeMeta{Kind: "APIResourceList", APIVersion: "v1"},
		GroupVersion: gv.String(),
		APIResources: rs,
	}
}

// storeLocally adds the APIService the aggregator registers for a group
// version Milo stores itself.
func (s *projectPlaneStub) storeLocally(gv schema.GroupVersion) {
	list := s.docs["/apis/apiregistration.k8s.io/v1/apiservices"].(*apiregistrationv1.APIServiceList)
	list.Items = append(list.Items, apiregistrationv1.APIService{
		TypeMeta:   metav1.TypeMeta{Kind: "APIService", APIVersion: "apiregistration.k8s.io/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: gv.Version + "." + gv.Group},
		Spec:       apiregistrationv1.APIServiceSpec{Group: gv.Group, Version: gv.Version},
	})
}

func (s *projectPlaneStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodDelete {
		s.mu.Lock()
		s.deletes = append(s.deletes, r.URL.Path)
		s.mu.Unlock()
		_ = json.NewEncoder(w).Encode(&metav1.Status{TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"}, Status: metav1.StatusSuccess})
		return
	}
	if s.broken[r.URL.Path] {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(&metav1.Status{
			TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
			Status:   metav1.StatusFailure, Reason: metav1.StatusReasonInternalError, Code: http.StatusInternalServerError,
		})
		return
	}
	doc, ok := s.docs[r.URL.Path]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(&metav1.Status{
			TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
			Status:   metav1.StatusFailure, Reason: metav1.StatusReasonNotFound, Code: http.StatusNotFound,
		})
		return
	}
	_ = json.NewEncoder(w).Encode(doc)
}

func (s *projectPlaneStub) deleted() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.deletes...)
}

func TestStartPurge_LeavesKindsSharedWithOtherProjectsAlone(t *testing.T) {
	stub := newProjectPlaneStub()
	srv := httptest.NewServer(stub)
	defer srv.Close()

	cfg := &rest.Config{Host: srv.URL}
	err := New(cfg).StartPurge(context.Background(), cfg, "doomed", Options{})
	if err != nil {
		t.Fatalf("StartPurge: %v", err)
	}

	for _, path := range stub.deleted() {
		if strings.Contains(path, "/apis/apiregistration.k8s.io/") {
			t.Errorf("purge of one project deleted the APIServices every project shares: DELETE %s", path)
		}
		if strings.Contains(path, "/apis/activity.miloapis.com/") {
			t.Errorf("purge of one project deleted a kind served by another API server: DELETE %s", path)
		}
	}
}

func TestStatus_IgnoresHeldObjectsOfKindsServedElsewhere(t *testing.T) {
	srv := httptest.NewServer(newProjectPlaneStub())
	defer srv.Close()

	cfg := &rest.Config{Host: srv.URL}
	status, err := New(cfg).Status(context.Background(), cfg, "doomed")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Complete {
		t.Fatalf("expected an empty project to be complete, got blockers: %s", status.Message())
	}
}

func TestStartPurge_DeletesOnlyClusterScopedKindsTheProjectHolds(t *testing.T) {
	stub := newProjectPlaneStub()
	srv := httptest.NewServer(stub)
	defer srv.Close()

	cfg := &rest.Config{Host: srv.URL}
	if err := New(cfg).StartPurge(context.Background(), cfg, "doomed", Options{}); err != nil {
		t.Fatalf("StartPurge: %v", err)
	}

	got := stub.deleted()
	want := "/apis/services.miloapis.com/v1alpha1/serviceentitlements"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("expected only DELETE %s, got %v", want, got)
	}
}

func TestStartPurge_DeletesNoClusterScopedKindWhenAPIServicesCannotBeRead(t *testing.T) {
	stub := newProjectPlaneStub()
	stub.broken = map[string]bool{"/apis/apiregistration.k8s.io/v1/apiservices": true}
	srv := httptest.NewServer(stub)
	defer srv.Close()

	cfg := &rest.Config{Host: srv.URL}
	if err := New(cfg).StartPurge(context.Background(), cfg, "doomed", Options{}); err == nil {
		t.Fatal("expected StartPurge to fail when it cannot tell which kinds are served elsewhere")
	}
	if got := stub.deleted(); len(got) != 0 {
		t.Fatalf("expected no deletes, got %v", got)
	}
}
