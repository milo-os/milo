package filters

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apiserver/pkg/endpoints/request"
	"sigs.k8s.io/yaml"
)

func TestWithUnservedParentSubresourceNotFound(t *testing.T) {
	resolver := &request.RequestInfoFactory{
		APIPrefixes:          sets.NewString("api", "apis"),
		GrouplessAPIPrefixes: sets.NewString("api"),
	}
	scheme := runtime.NewScheme()
	metav1.AddToGroupVersion(scheme, metav1.SchemeGroupVersion)
	codecs := serializer.NewCodecFactory(scheme)

	tests := map[string]struct {
		path       string
		wantPassed bool
	}{
		"project prefix repeated inside a project control plane": {
			path: "/apis/resourcemanager.miloapis.com/v1alpha1/projects/datum-cloud/control-plane/api/v1/namespaces/datum-net",
		},
		"bare project control-plane path": {
			path: "/apis/resourcemanager.miloapis.com/v1alpha1/projects/datum-cloud/control-plane",
		},
		"projects segment repeated before the project name": {
			path: "/apis/resourcemanager.miloapis.com/v1alpha1/projects/projects/datum-cloud/control-plane/apis/networking.datumapis.com/v1alpha/namespaces/default/domains",
		},
		"organization control-plane path": {
			path: "/apis/resourcemanager.miloapis.com/v1alpha1/organizations/datum/control-plane/apis",
		},
		"user control-plane path": {
			path: "/apis/iam.miloapis.com/v1alpha1/users/u-1/control-plane/api",
		},
		"project read passes": {
			path:       "/apis/resourcemanager.miloapis.com/v1alpha1/projects/datum-cloud",
			wantPassed: true,
		},
		"project list passes": {
			path:       "/apis/resourcemanager.miloapis.com/v1alpha1/projects",
			wantPassed: true,
		},
		"project status passes": {
			path:       "/apis/resourcemanager.miloapis.com/v1alpha1/projects/datum-cloud/status",
			wantPassed: true,
		},
		"organization status passes": {
			path:       "/apis/resourcemanager.miloapis.com/v1alpha1/organizations/datum/status",
			wantPassed: true,
		},
		"user status passes": {
			path:       "/apis/iam.miloapis.com/v1alpha1/users/u-1/status",
			wantPassed: true,
		},
		"namespace read passes": {
			path:       "/api/v1/namespaces/datum-net",
			wantPassed: true,
		},
		"subresource of another resource passes": {
			path:       "/apis/example.com/v1/widgets/w/control-plane",
			wantPassed: true,
		},
		"discovery passes": {
			path:       "/apis",
			wantPassed: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			passed := false
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				passed = true
				w.WriteHeader(http.StatusOK)
			})

			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			info, err := resolver.NewRequestInfo(req)
			require.NoError(t, err)
			req = req.WithContext(request.WithRequestInfo(req.Context(), info))

			rec := httptest.NewRecorder()
			WithUnservedParentSubresourceNotFound(next, codecs).ServeHTTP(rec, req)

			assert.Equal(t, tc.wantPassed, passed)
			if tc.wantPassed {
				assert.Equal(t, http.StatusOK, rec.Code)
				return
			}

			assert.Equal(t, http.StatusNotFound, rec.Code)
			var status metav1.Status
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &status))
			assert.Equal(t, metav1.StatusReasonNotFound, status.Reason)
			assert.Contains(t, status.Message, "is not an API path")
		})
	}
}

func TestParentSubresourcesMatchTheirCRDs(t *testing.T) {
	crdFiles := map[schema.GroupResource]string{
		{Group: "resourcemanager.miloapis.com", Resource: "projects"}:      "resourcemanager/resourcemanager.miloapis.com_projects.yaml",
		{Group: "resourcemanager.miloapis.com", Resource: "organizations"}: "resourcemanager/resourcemanager.miloapis.com_organizations.yaml",
		{Group: "iam.miloapis.com", Resource: "users"}:                     "iam/iam.miloapis.com_users.yaml",
	}
	require.Len(t, controlPlaneParentSubresources, len(crdFiles))

	for gr, file := range crdFiles {
		t.Run(gr.String(), func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("..", "..", "..", "config", "crd", "bases", file))
			require.NoError(t, err)

			var crd apiextensionsv1.CustomResourceDefinition
			require.NoError(t, yaml.Unmarshal(raw, &crd))
			require.Equal(t, gr.Group, crd.Spec.Group)
			require.Equal(t, gr.Resource, crd.Spec.Names.Plural)
			require.NotEmpty(t, crd.Spec.Versions)

			served := sets.New[string]()
			for _, version := range crd.Spec.Versions {
				if version.Subresources == nil {
					continue
				}
				if version.Subresources.Status != nil {
					served.Insert("status")
				}
				if version.Subresources.Scale != nil {
					served.Insert("scale")
				}
			}

			allowed, ok := controlPlaneParentSubresources[gr]
			require.True(t, ok)
			assert.ElementsMatch(t, sets.List(served), sets.List(allowed))
		})
	}
}
