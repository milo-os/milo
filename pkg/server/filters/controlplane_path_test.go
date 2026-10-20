package filters

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apiserver/pkg/endpoints/request"
)

func TestWithControlPlanePathNotFound(t *testing.T) {
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
		"project status passes": {
			path:       "/apis/resourcemanager.miloapis.com/v1alpha1/projects/datum-cloud/status",
			wantPassed: true,
		},
		"namespace read passes": {
			path:       "/api/v1/namespaces/datum-net",
			wantPassed: true,
		},
		"same subresource name in another group passes": {
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
			WithControlPlanePathNotFound(next, codecs).ServeHTTP(rec, req)

			assert.Equal(t, tc.wantPassed, passed)
			if tc.wantPassed {
				assert.Equal(t, http.StatusOK, rec.Code)
				return
			}

			assert.Equal(t, http.StatusNotFound, rec.Code)
			var status metav1.Status
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &status))
			assert.Equal(t, metav1.StatusReasonNotFound, status.Reason)
			assert.Contains(t, status.Message, "control-plane path prefix")
		})
	}
}
