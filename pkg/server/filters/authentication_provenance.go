package filters

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/endpoints/request"
)

type authenticationProvenanceKey struct{}

// AuthenticationProvenance records the user established by authentication before
// impersonation or scope decorators modify the effective request user. It is not
// a trusted-writer classification or an authorization decision.
type AuthenticationProvenance struct {
	User user.Info
	// ImpersonationRequested records the presence of any Impersonate-* header,
	// including empty, unsupported, and differently cased headers. It does not
	// imply that Kubernetes accepted the impersonation request.
	ImpersonationRequested bool
}

// AuthenticationProvenanceFrom returns an independent copy of the captured
// provenance. A false result means provenance was not captured, not that the
// request was authenticated without impersonation. Mutating the returned user,
// groups, or extras cannot change the stored provenance.
func AuthenticationProvenanceFrom(ctx context.Context) (AuthenticationProvenance, bool) {
	p, ok := ctx.Value(authenticationProvenanceKey{}).(AuthenticationProvenance)
	if !ok {
		return AuthenticationProvenance{}, false
	}
	p.User = copyAuthenticatedUser(p.User)
	return p, true
}

// WithAuthenticationProvenance must execute after authentication and before
// Kubernetes' impersonation filter, which replaces user.Info and strips headers.
// Only the authenticated context user is captured; headers and user extras cannot
// supply provenance. Requests without an authenticated context user pass through
// without provenance so consumers can distinguish an incorrectly wired chain.
func WithAuthenticationProvenance(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		u, ok := request.UserFrom(req.Context())
		if !ok || u == nil {
			next.ServeHTTP(w, req)
			return
		}
		p := AuthenticationProvenance{User: copyAuthenticatedUser(u)}
		for name := range req.Header {
			if strings.HasPrefix(strings.ToLower(name), "impersonate-") {
				p.ImpersonationRequested = true
				break
			}
		}
		ctx := context.WithValue(req.Context(), authenticationProvenanceKey{}, p)
		next.ServeHTTP(w, req.WithContext(ctx))
	})
}

func copyAuthenticatedUser(u user.Info) *user.DefaultInfo {
	copy := &user.DefaultInfo{
		Name:   u.GetName(),
		UID:    u.GetUID(),
		Groups: slices.Clone(u.GetGroups()),
	}
	if extra := u.GetExtra(); extra != nil {
		copy.Extra = make(map[string][]string, len(extra))
		for key, values := range extra {
			copy.Extra[key] = slices.Clone(values)
		}
	}
	return copy
}
