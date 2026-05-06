package discovery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"go.miloapis.com/milo/pkg/request"
)

func TestParseContexts(t *testing.T) {
	cases := []struct {
		in   string
		want []ParentContext
	}{
		{"", nil},
		{"   ", nil},
		{"*", nil},
		{"Organization", []ParentContext{ContextOrganization}},
		{"Organization,User", []ParentContext{ContextOrganization, ContextUser}},
		{"Organization;User", []ParentContext{ContextOrganization, ContextUser}},
		{"  Platform , Organization  ", []ParentContext{ContextPlatform, ContextOrganization}},
		{"Organization,*,Project", nil}, // wildcard short-circuits
	}
	for _, tc := range cases {
		got := ParseContexts(tc.in)
		if !equalContexts(got, tc.want) {
			t.Errorf("ParseContexts(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestRegistryStaticAndCRD(t *testing.T) {
	r := NewRegistry()
	r.RegisterStatic(schema.GroupResource{Group: "core", Resource: "configmaps"}, ContextPlatform)
	// Simulate a CRD entry by writing directly (informer path is exercised
	// implicitly elsewhere; this keeps the test hermetic).
	r.crd[schema.GroupResource{Group: "resourcemanager.miloapis.com", Resource: "projects"}] = []ParentContext{ContextOrganization}

	if !r.IsVisible(schema.GroupResource{Group: "core", Resource: "configmaps"}, ContextPlatform) {
		t.Error("static registration should be visible in matching context")
	}
	if r.IsVisible(schema.GroupResource{Group: "core", Resource: "configmaps"}, ContextProject) {
		t.Error("static registration should NOT be visible in non-matching context")
	}
	if !r.IsVisible(schema.GroupResource{Group: "resourcemanager.miloapis.com", Resource: "projects"}, ContextOrganization) {
		t.Error("CRD registration should be visible in matching context")
	}
	if r.IsVisible(schema.GroupResource{Group: "resourcemanager.miloapis.com", Resource: "projects"}, ContextPlatform) {
		t.Error("CRD registration should NOT be visible at root")
	}
	// Unregistered resource → visible everywhere (backwards-compatible).
	if !r.IsVisible(schema.GroupResource{Group: "x", Resource: "y"}, ContextProject) {
		t.Error("unregistered resource should be visible by default")
	}
}

func TestFilterAPIResourceListByContext(t *testing.T) {
	registry := NewRegistry()
	// Mark "projects" as Organization-only.
	registry.crd[schema.GroupResource{Group: "resourcemanager.miloapis.com", Resource: "projects"}] = []ParentContext{ContextOrganization}
	// Mark "organizations" as Platform-only.
	registry.crd[schema.GroupResource{Group: "resourcemanager.miloapis.com", Resource: "organizations"}] = []ParentContext{ContextPlatform}
	registry.hasInit = true
	registry.hasPolicyInit = true

	// Inner handler that returns a synthetic APIResourceList.
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		body, _ := json.Marshal(metav1.APIResourceList{
			TypeMeta:     metav1.TypeMeta{Kind: "APIResourceList", APIVersion: "v1"},
			GroupVersion: "resourcemanager.miloapis.com/v1alpha1",
			APIResources: []metav1.APIResource{
				{Name: "organizations", Namespaced: false, Kind: "Organization"},
				{Name: "organizations/status", Namespaced: false, Kind: "Organization"},
				{Name: "projects", Namespaced: false, Kind: "Project"},
				{Name: "untagged", Namespaced: false, Kind: "Untagged"},
			},
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})
	handler := DiscoveryContextFilter(inner, registry)

	cases := []struct {
		name      string
		ctxSetter func(context.Context) context.Context
		wantNames []string // expected resource names (in order kept)
	}{
		{
			name:      "platform context passes through unfiltered",
			ctxSetter: func(c context.Context) context.Context { return c },
			wantNames: []string{"organizations", "organizations/status", "projects", "untagged"},
		},
		{
			name:      "project context hides Platform-only and Organization-only",
			ctxSetter: func(c context.Context) context.Context { return request.WithProject(c, "p1") },
			wantNames: []string{"untagged"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/apis/resourcemanager.miloapis.com/v1alpha1", nil)
			req = req.WithContext(tc.ctxSetter(req.Context()))
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
			}
			var out metav1.APIResourceList
			if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			got := make([]string, len(out.APIResources))
			for i, r := range out.APIResources {
				got[i] = r.Name
			}
			if strings.Join(got, ",") != strings.Join(tc.wantNames, ",") {
				t.Errorf("filtered names = %v, want %v", got, tc.wantNames)
			}
		})
	}
}

func equalContexts(a, b []ParentContext) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
