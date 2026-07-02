package filters

import (
	"net/http"
	"net/http/httptest"
	"testing"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/install"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apiserver/pkg/authentication/user"
	genericapifilters "k8s.io/apiserver/pkg/endpoints/filters"
	"k8s.io/apiserver/pkg/endpoints/request"
	k8sapiserver "k8s.io/apiserver/pkg/server"

	"github.com/stretchr/testify/assert"
)

func TestOrganizationContextHandler(t *testing.T) {
	scheme := runtime.NewScheme()
	install.Install(scheme)

	tests := map[string]struct {
		path         string
		reqUser      *user.DefaultInfo
		expectedCode int
		// Custom handler to assert expectatations on request state in the end of
		// the request chain.
		assertRequest func(*testing.T, *http.Request)
	}{
		"bad request: missing org id": {
			path:         "/apis/resourcemanager.miloapis.com/v1alpha1/organizations/",
			expectedCode: http.StatusBadRequest,
		},
		"internal error: org request with no user": {
			path: "/apis/resourcemanager.miloapis.com/v1alpha1/organizations/some-org/control-plane",
			assertRequest: func(t *testing.T, req *http.Request) {
				assert.Equal(t, "", req.URL.Path)
			},
			expectedCode: http.StatusInternalServerError,
		},
		"direct organization request succeeds": {
			path:         "/apis/resourcemanager.miloapis.com/v1alpha1/organizations/some-org",
			reqUser:      &user.DefaultInfo{},
			expectedCode: http.StatusOK,
			assertRequest: func(t *testing.T, req *http.Request) {
				assert.Equal(t, "/apis/resourcemanager.miloapis.com/v1alpha1/organizations/some-org", req.URL.Path)
			},
		},
		"organization status subresource request succeeds": {
			path:         "/apis/resourcemanager.miloapis.com/v1alpha1/organizations/some-org/status",
			reqUser:      &user.DefaultInfo{},
			expectedCode: http.StatusOK,
			assertRequest: func(t *testing.T, req *http.Request) {
				assert.Equal(t, "/apis/resourcemanager.miloapis.com/v1alpha1/organizations/some-org/status", req.URL.Path)
			},
		},
		"org request succeeds": {
			path:         "/apis/resourcemanager.miloapis.com/v1alpha1/organizations/some-org/control-plane",
			reqUser:      &user.DefaultInfo{},
			expectedCode: http.StatusOK,
			assertRequest: func(t *testing.T, req *http.Request) {
				assert.Equal(t, "", req.URL.Path)
				reqUser, ok := request.UserFrom(req.Context())
				assert.True(t, ok, "user not found in request context")

				u, ok := reqUser.(*user.DefaultInfo)
				assert.True(t, ok, "user in request context is not *user.DefaultInfo")

				assert.Contains(t, u.Extra, iamv1alpha1.ParentAPIGroupExtraKey)
				assert.Contains(t, u.Extra, iamv1alpha1.ParentKindExtraKey)
				assert.Contains(t, u.Extra, iamv1alpha1.ParentNameExtraKey)
				assert.Equal(t, resourcemanagerv1alpha1.GroupVersion.Group, u.Extra[iamv1alpha1.ParentAPIGroupExtraKey][0])
				assert.Equal(t, "Organization", u.Extra[iamv1alpha1.ParentKindExtraKey][0])
				assert.Equal(t, "some-org", u.Extra[iamv1alpha1.ParentNameExtraKey][0])
			},
		},
		"org project list label selector injected": {
			path:         "/apis/resourcemanager.miloapis.com/v1alpha1/organizations/some-org/control-plane/apis/resourcemanager.miloapis.com/v1alpha/projects?labelSelector=resourcemanager.miloapis.com/organization-id=notvalid,other=value",
			reqUser:      &user.DefaultInfo{},
			expectedCode: http.StatusOK,
			assertRequest: func(t *testing.T, req *http.Request) {
				info, ok := request.RequestInfoFrom(req.Context())
				assert.True(t, ok, "request info not found in request context")
				if ok {
					assert.NotEmpty(t, info.LabelSelector, "label selector not found in request")

					selector, err := labels.Parse(info.LabelSelector)
					assert.NoError(t, err, "unexpected error parsing request label selectors")

					// Ensure that the org constraint exists and has the value in the URL
					// instead of the value provided in the query parameter.
					v, ok := selector.RequiresExactMatch(resourcemanagerv1alpha1.OrganizationNameLabel)
					assert.True(t, ok, "organization-name constraint not found")
					assert.Equal(t, "some-org", v)

					// Ensure other constraints still exist
					v, ok = selector.RequiresExactMatch("other")
					assert.True(t, ok, `constraint "other" not found`)
					assert.Equal(t, "value", v)
				}
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			req, err := http.NewRequest("GET", tt.path, nil)
			assert.NoError(t, err)

			rr := httptest.NewRecorder()

			handler := OrganizationContextHandler(
				http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					if tt.reqUser != nil {
						req = req.WithContext(request.WithUser(req.Context(), tt.reqUser))
					}

					genericapifilters.WithRequestInfo(
						OrganizationProjectListConstraintDecorator(
							OrganizationContextAuthorizationDecorator(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
								if tt.assertRequest != nil {
									tt.assertRequest(t, req)
								}
							})),
						),
						k8sapiserver.NewRequestInfoResolver(&k8sapiserver.Config{}),
					).ServeHTTP(w, req)

				}),
				serializer.NewCodecFactory(scheme),
			)

			handler.ServeHTTP(rr, req)

			assert.Equal(t, tt.expectedCode, rr.Code)
		})
	}

}
