package discovery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apidiscoveryv2 "k8s.io/api/apidiscovery/v2"
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

// TestFilterAPIIndex_FormatDetection covers the body-kind probe added to
// filterAPIIndex so that responses with a kind mismatch between the request
// Accept header and the actual body are not silently coerced to an empty
// list.
//
// Regression test for the bug where an upstream apiserver emitting legacy
// APIGroupList JSON in response to an aggregated-discovery request caused
// filterAPIIndex to unmarshal into APIGroupDiscoveryList (succeeds, but
// produces items: nil) and re-emit an empty list — dropping every group from
// discovery, including built-ins like coordination.k8s.io that no client had
// any way to register.
func TestFilterAPIIndex_FormatDetection(t *testing.T) {
	aggregatedBody := func() []byte {
		body := apidiscoveryv2.APIGroupDiscoveryList{
			TypeMeta: metav1.TypeMeta{Kind: "APIGroupDiscoveryList", APIVersion: "apidiscovery.k8s.io/v2"},
			Items: []apidiscoveryv2.APIGroupDiscovery{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "coordination.k8s.io"},
					Versions: []apidiscoveryv2.APIVersionDiscovery{
						{Version: "v1", Resources: []apidiscoveryv2.APIResourceDiscovery{{Resource: "leases"}}},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "resourcemanager.miloapis.com"},
					Versions: []apidiscoveryv2.APIVersionDiscovery{
						{Version: "v1alpha1", Resources: []apidiscoveryv2.APIResourceDiscovery{
							{Resource: "projects"}, {Resource: "untagged"},
						}},
					},
				},
			},
		}
		raw, _ := json.Marshal(body)
		return raw
	}()
	legacyBody := []byte(`{
		"kind":"APIGroupList","apiVersion":"v1",
		"groups":[
			{"name":"coordination.k8s.io","versions":[{"groupVersion":"coordination.k8s.io/v1","version":"v1"}]},
			{"name":"resourcemanager.miloapis.com","versions":[{"groupVersion":"resourcemanager.miloapis.com/v1alpha1","version":"v1alpha1"}]}
		]
	}`)

	cases := []struct {
		name         string
		body         []byte
		accept       string
		wantPassThru bool     // expect the body verbatim
		wantGroups   []string // for aggregated, the kept group names
		wantContains []string // for pass-through, substrings expected in body
	}{
		{
			name:       "aggregated body, aggregated Accept -> filtered, all visible kept",
			body:       aggregatedBody,
			accept:     "application/json;g=apidiscovery.k8s.io;v=v2;as=APIGroupDiscoveryList",
			wantGroups: []string{"coordination.k8s.io", "resourcemanager.miloapis.com"},
		},
		{
			name:         "legacy body, aggregated Accept -> pass through verbatim (no silent emptying)",
			body:         legacyBody,
			accept:       "application/json;g=apidiscovery.k8s.io;v=v2;as=APIGroupDiscoveryList",
			wantPassThru: true,
			wantContains: []string{`"kind":"APIGroupList"`, `"coordination.k8s.io"`},
		},
		{
			name:         "legacy body, legacy Accept -> pass through",
			body:         legacyBody,
			accept:       "application/json",
			wantPassThru: true,
			wantContains: []string{`"kind":"APIGroupList"`, `"coordination.k8s.io"`},
		},
		{
			name:       "aggregated body, legacy Accept -> still filtered (body kind wins)",
			body:       aggregatedBody,
			accept:     "application/json",
			wantGroups: []string{"coordination.k8s.io", "resourcemanager.miloapis.com"},
		},
	}

	registry := NewRegistry()
	// "projects" is Organization-tagged; in Project context it must be hidden.
	registry.crd[schema.GroupResource{Group: "resourcemanager.miloapis.com", Resource: "projects"}] = []ParentContext{ContextOrganization}
	registry.hasInit = true

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(tc.body)
			})
			handler := DiscoveryContextFilter(inner, registry)

			req := httptest.NewRequest(http.MethodGet, "/apis", nil)
			req.Header.Set("Accept", tc.accept)
			req = req.WithContext(request.WithProject(context.Background(), "p1"))

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
			}

			if tc.wantPassThru {
				body := rr.Body.String()
				for _, want := range tc.wantContains {
					if !strings.Contains(body, want) {
						t.Errorf("body missing %q\nbody=%s", want, body)
					}
				}
				return
			}

			var got apidiscoveryv2.APIGroupDiscoveryList
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode response: %v; body=%s", err, rr.Body.String())
			}
			names := make([]string, 0, len(got.Items))
			for _, g := range got.Items {
				// Inside resourcemanager, "projects" should be filtered out
				// in Project context. We only check the group survives at
				// all by including it here if any version has any resource.
				keepGroup := false
				for _, v := range g.Versions {
					if len(v.Resources) > 0 {
						keepGroup = true
					}
				}
				if keepGroup {
					names = append(names, g.Name)
				}
			}
			if strings.Join(names, ",") != strings.Join(tc.wantGroups, ",") {
				t.Errorf("groups = %v, want %v", names, tc.wantGroups)
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
