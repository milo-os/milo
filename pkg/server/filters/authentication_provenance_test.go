package filters

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/endpoints/request"
)

func TestAuthenticationProvenanceCopiesOnCaptureAndRead(t *testing.T) {
	original := &user.DefaultInfo{
		Name: "installer", UID: "installer-uid", Groups: []string{"customers"},
		Extra: map[string][]string{"scope": {"original"}, "nil": nil},
	}
	want := &user.DefaultInfo{
		Name: "installer", UID: "installer-uid", Groups: []string{"customers"},
		Extra: map[string][]string{"scope": {"original"}, "nil": nil},
	}
	h := WithAuthenticationProvenance(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		// Authenticators may reuse their user object. Neither subsequent changes
		// to it nor downstream consumers may mutate the captured snapshot.
		original.Name, original.UID = "changed", "changed"
		original.Groups[0] = "changed"
		original.Extra["scope"][0] = "changed"
		original.Extra["injected"] = []string{"changed"}
		first, ok := AuthenticationProvenanceFrom(req.Context())
		if !ok || !reflect.DeepEqual(first.User, want) || first.ImpersonationRequested {
			t.Fatalf("captured provenance = %#v, present=%v, want original snapshot", first, ok)
		}
		mutable := first.User.(*user.DefaultInfo)
		mutable.Name, mutable.UID = "reader", "reader"
		mutable.Groups[0] = "reader"
		mutable.Extra["scope"][0] = "reader"
		delete(mutable.Extra, "nil")
		second, ok := AuthenticationProvenanceFrom(req.Context())
		if !ok || !reflect.DeepEqual(second.User, want) {
			t.Fatalf("reader changed stored provenance: %#v", second)
		}
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(httptest.NewRecorder(), req.WithContext(request.WithUser(req.Context(), original)))
}

func TestAuthenticationProvenanceMissingIsDistinct(t *testing.T) {
	if _, ok := AuthenticationProvenanceFrom(context.Background()); ok {
		t.Fatal("absent context has provenance")
	}
	h := WithAuthenticationProvenance(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		if _, ok := AuthenticationProvenanceFrom(req.Context()); ok {
			t.Fatal("headers fabricated provenance without authenticated context")
		}
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Remote-User", "apps-controller")
	req.Header.Set("X-Remote-Uid", "apps-uid")
	req.Header.Set("Impersonate-User", "apps-controller")
	h.ServeHTTP(httptest.NewRecorder(), req)
}

func TestAuthenticationProvenanceHeaderPresence(t *testing.T) {
	for _, name := range []string{
		"Impersonate-User", "Impersonate-Group", "Impersonate-Uid",
		"Impersonate-Extra-example.com%2Frole", "Impersonate-Unknown",
	} {
		for _, spelling := range []string{name, strings.ToLower(name), strings.ToUpper(name), alternatingCase(name)} {
			for _, values := range [][]string{nil, {}, {""}, {"value"}} {
				t.Run(spelling+"/"+strings.Join(values, ","), func(t *testing.T) {
					var captured AuthenticationProvenance
					h := WithAuthenticationProvenance(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
						var ok bool
						captured, ok = AuthenticationProvenanceFrom(req.Context())
						if !ok {
							t.Fatal("authenticated request has no provenance")
						}
					}))
					req := httptest.NewRequest(http.MethodGet, "/", nil)
					// Assign directly: Header.Set would canonicalize case and mask
					// incorrectly cased keys supplied by other Go middleware.
					req.Header[spelling] = values
					req = req.WithContext(request.WithUser(req.Context(), &user.DefaultInfo{Name: "actor"}))
					h.ServeHTTP(httptest.NewRecorder(), req)
					if !captured.ImpersonationRequested {
						t.Fatal("header presence was not recorded")
					}
				})
			}
		}
	}
}

func TestAuthenticationProvenanceIgnoresUntrustedClaims(t *testing.T) {
	original := &user.DefaultInfo{Name: "customer", UID: "customer-uid", Extra: map[string][]string{
		"apps.miloapis.com/authenticated-user":      {"apps-controller"},
		"apps.miloapis.com/impersonation-requested": {"true"},
	}}
	h := WithAuthenticationProvenance(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		p, ok := AuthenticationProvenanceFrom(req.Context())
		if !ok || !reflect.DeepEqual(p.User, original) || p.ImpersonationRequested {
			t.Fatalf("untrusted claims changed provenance: %#v, present=%v", p, ok)
		}
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Remote-User", "apps-controller")
	req.Header.Set("X-Remote-Uid", "apps-uid")
	req.Header.Set("X-Impersonate-User", "apps-controller")
	h.ServeHTTP(httptest.NewRecorder(), req.WithContext(request.WithUser(req.Context(), original)))
}

func alternatingCase(s string) string {
	var result strings.Builder
	for i, r := range s {
		if i%2 == 0 {
			result.WriteString(strings.ToLower(string(r)))
		} else {
			result.WriteString(strings.ToUpper(string(r)))
		}
	}
	return result.String()
}
