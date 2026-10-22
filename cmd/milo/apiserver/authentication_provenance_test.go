package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	datumfilters "go.miloapis.com/milo/pkg/server/filters"
	"k8s.io/apiserver/pkg/authentication/authenticator"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	"k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/server"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/kubernetes/scheme"
)

// Use Milo's complete chain, including the actual Kubernetes authentication and
// impersonation middleware. Permitting generic impersonation ensures this tests
// provenance capture itself rather than relying on an RBAC denial.
func TestDefaultChainPreservesAuthenticationProvenance(t *testing.T) {
	original := &user.DefaultInfo{
		Name: "installer", UID: "installer-uid", Groups: []string{"customers", "system:authenticated"},
		Extra: map[string][]string{"issuer": {"authentication"}},
	}
	for _, tc := range []struct {
		name              string
		path              string
		headers           http.Header
		wantUser          *user.DefaultInfo
		wantImpersonation bool
		wantStatus        int
	}{
		{name: "direct", wantUser: original, wantStatus: http.StatusNoContent},
		{
			name: "project scope decorator",
			path: "/apis/resourcemanager.miloapis.com/v1alpha1/projects/project-a/control-plane/healthz",
			wantUser: &user.DefaultInfo{Name: original.Name, UID: original.UID, Groups: original.Groups, Extra: map[string][]string{
				"issuer":                            {"authentication"},
				"iam.miloapis.com/parent-api-group": {"resourcemanager.miloapis.com"},
				"iam.miloapis.com/parent-type":      {"Project"},
				"iam.miloapis.com/parent-name":      {"project-a"},
			}},
			wantStatus: http.StatusNoContent,
		},
		{
			name: "user UID groups and extras",
			headers: http.Header{
				"Impersonate-User": {"apps-controller"}, "Impersonate-Uid": {"apps-uid"},
				"Impersonate-Group": {"apps-writers"}, http.CanonicalHeaderKey("Impersonate-Extra-example.com%2Frole"): {"writer"},
			},
			wantUser:          &user.DefaultInfo{Name: "apps-controller", UID: "apps-uid", Groups: []string{"apps-writers", "system:authenticated"}, Extra: map[string][]string{"example.com/role": {"writer"}}},
			wantImpersonation: true, wantStatus: http.StatusNoContent,
		},
		{
			name: "self impersonation",
			headers: http.Header{
				"Impersonate-User": {original.Name}, "Impersonate-Uid": {original.UID},
				"Impersonate-Group": {"customers", "system:authenticated"}, http.CanonicalHeaderKey("Impersonate-Extra-issuer"): {"authentication"},
			},
			wantUser:          &user.DefaultInfo{Name: original.Name, UID: original.UID, Groups: []string{"customers", "system:authenticated"}, Extra: original.Extra},
			wantImpersonation: true, wantStatus: http.StatusNoContent,
		},
		{
			name: "empty header is still recorded", headers: http.Header{"Impersonate-User": {""}},
			wantUser: original, wantImpersonation: true, wantStatus: http.StatusNoContent,
		},
		{
			name: "noncanonical header key is still recorded", headers: http.Header{"iMpErSoNaTe-uSeR": {"apps-controller"}},
			wantUser: original, wantImpersonation: true, wantStatus: http.StatusNoContent,
		},
		{
			name: "malformed impersonation still rejected by Kubernetes", headers: http.Header{"Impersonate-Group": {"apps-writers"}},
			wantStatus: http.StatusBadRequest,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := server.NewConfig(scheme.Codecs)
			c.FeatureGate = utilfeature.DefaultFeatureGate
			c.RequestInfoResolver = &request.RequestInfoFactory{}
			c.Authentication.Authenticator = authenticator.RequestFunc(func(*http.Request) (*authenticator.Response, bool, error) {
				return &authenticator.Response{User: original}, true, nil
			})
			c.Authorization.Authorizer = authorizer.AuthorizerFunc(func(_ context.Context, a authorizer.Attributes) (authorizer.Decision, string, error) {
				if a.GetVerb() == "impersonate" && !reflect.DeepEqual(a.GetUser(), original) {
					t.Errorf("impersonation authorized as %v, want original actor", a.GetUser())
				}
				return authorizer.DecisionAllow, "test permits impersonation", nil
			})
			called := false
			h := DefaultBuildHandlerChain(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				called = true
				p, ok := datumfilters.AuthenticationProvenanceFrom(req.Context())
				if !ok || !reflect.DeepEqual(p.User, original) || p.ImpersonationRequested != tc.wantImpersonation {
					t.Errorf("provenance=%#v, present=%v; want original identity, impersonation=%v", p, ok, tc.wantImpersonation)
				}
				effective, _ := request.UserFrom(req.Context())
				if !reflect.DeepEqual(effective, tc.wantUser) {
					t.Errorf("effective user=%#v, want %#v", effective, tc.wantUser)
				}
				if tc.wantUser != original {
					for name := range req.Header {
						if strings.HasPrefix(strings.ToLower(name), "impersonate-") {
							t.Errorf("Kubernetes did not strip %s", name)
						}
					}
				}
				w.WriteHeader(http.StatusNoContent)
			}), c, nil)
			h = datumfilters.ProjectRouterWithRequestInfo(h, c.RequestInfoResolver)
			path := tc.path
			if path == "" {
				path = "/healthz"
			}
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header = tc.headers.Clone()
			if req.Header == nil {
				req.Header = make(http.Header)
			}
			req.Header.Set("X-Remote-User", "forged-user")
			req.Header.Set("X-Remote-Uid", "forged-uid")
			response := httptest.NewRecorder()
			h.ServeHTTP(response, req)
			if response.Code != tc.wantStatus || called != (tc.wantStatus == http.StatusNoContent) {
				t.Fatalf("status=%d, handler called=%v, body=%s; want %d", response.Code, called, response.Body.String(), tc.wantStatus)
			}
		})
	}
}
