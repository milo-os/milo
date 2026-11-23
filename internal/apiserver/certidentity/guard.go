// Package certidentity decides whether a UID read from a client certificate
// may stand for the caller. Kubernetes reads a UID from the certificate
// subject, and Milo's authorizer trusts any UID it is given, so anyone who
// can get a certificate signed by the client CA could otherwise claim any
// user's or service account's identity. The guard keeps a certificate UID
// only when it names an active ServiceAccount in the request's control plane
// that has opted in to certificate authentication.
package certidentity

import (
	"context"
	"net/http"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilcache "k8s.io/apimachinery/pkg/util/cache"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apiserver/pkg/authentication/authenticator"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/klog/v2"

	projctx "go.miloapis.com/milo/pkg/request"
)

const (
	// OptInAnnotation marks a ServiceAccount whose UID a client certificate
	// may carry. Only the value OptInValue opts in.
	OptInAnnotation = "iam.miloapis.com/client-certificate-authentication"
	OptInValue      = "Enabled"

	// DefaultTTL bounds how long a decision is reused, and so how long a
	// deleted, deactivated or opted-out ServiceAccount's certificates keep
	// working.
	DefaultTTL = 30 * time.Second

	certificateCredentialPrefix = "X509SHA256="
	cacheSize                   = 4096
	lookupTimeout               = 5 * time.Second
)

// Reason is the outcome of checking a certificate UID.
type Reason string

const (
	ReasonAllowed      Reason = "Allowed"
	ReasonNotFound     Reason = "NotFound"
	ReasonNotOptedIn   Reason = "NotOptedIn"
	ReasonInactive     Reason = "Inactive"
	ReasonDeleting     Reason = "Deleting"
	ReasonLookupFailed Reason = "LookupFailed"
)

// Lookup lists the ServiceAccounts in a project's control plane, or in the
// root control plane when project is empty. A control plane that does not
// serve ServiceAccounts has none.
type Lookup func(ctx context.Context, project string) ([]unstructured.Unstructured, error)

// Guard checks certificate UIDs against ServiceAccounts and caches each
// decision for its TTL.
type Guard struct {
	lookup   Lookup
	ttl      time.Duration
	cache    *utilcache.LRUExpireCache
	inFlight singleflight.Group
}

// NewGuard returns a Guard that resolves UIDs with lookup and reuses each
// decision for ttl.
func NewGuard(lookup Lookup, ttl time.Duration) *Guard {
	return NewGuardWithClock(lookup, ttl, realClock{})
}

// NewGuardWithClock is NewGuard with the cache's clock supplied.
func NewGuardWithClock(lookup Lookup, ttl time.Duration, clock utilcache.Clock) *Guard {
	return &Guard{lookup: lookup, ttl: ttl, cache: utilcache.NewLRUExpireCacheWithClock(cacheSize, clock)}
}

// Authenticator wraps delegate so that a UID taken from a client certificate
// survives only when it names an opted-in, active ServiceAccount in the
// request's control plane. Otherwise the UID is removed and the request
// continues as the certificate's name and groups alone.
func (g *Guard) Authenticator(delegate authenticator.Request) authenticator.Request {
	if delegate == nil {
		return nil
	}
	return authenticator.RequestFunc(func(req *http.Request) (*authenticator.Response, bool, error) {
		resp, ok, err := delegate.AuthenticateRequest(req)
		if err != nil || !ok || resp == nil || resp.User == nil {
			return resp, ok, err
		}
		uid := resp.User.GetUID()
		if uid == "" || !fromCertificate(resp.User) {
			return resp, ok, err
		}
		project, _ := projctx.ProjectID(req.Context())
		reason := g.Check(req.Context(), project, uid)
		decisions.WithLabelValues(string(reason)).Inc()
		if reason == ReasonAllowed {
			return resp, ok, err
		}
		stripped := *resp
		stripped.User = withoutUID(resp.User)
		return &stripped, ok, err
	})
}

// Check reports whether uid may stand for a client certificate's holder in
// project's control plane.
func (g *Guard) Check(ctx context.Context, project, uid string) Reason {
	key := project + "/" + uid
	if cached, ok := g.cache.Get(key); ok {
		return cached.(Reason)
	}
	result, _, _ := g.inFlight.Do(key, func() (any, error) {
		reason := g.resolve(ctx, project, uid)
		if reason != ReasonLookupFailed {
			g.cache.Add(key, reason, g.ttl)
		}
		if reason == ReasonAllowed {
			klog.V(2).InfoS("Client certificate UID accepted", "project", project, "uid", uid)
		} else {
			klog.InfoS("Client certificate UID removed; the request continues without it", "project", project, "uid", uid, "reason", reason)
		}
		return reason, nil
	})
	return result.(Reason)
}

func (g *Guard) resolve(ctx context.Context, project, uid string) Reason {
	if project != "" && len(validation.IsDNS1123Subdomain(project)) > 0 {
		return ReasonNotFound
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lookupTimeout)
	defer cancel()
	accounts, err := g.lookup(ctx, project)
	if err != nil {
		klog.ErrorS(err, "Failed to look up ServiceAccounts for a client certificate UID", "project", project, "uid", uid)
		return ReasonLookupFailed
	}
	return Decide(accounts, uid)
}

// Decide reports whether uid names an opted-in, active ServiceAccount among
// accounts.
func Decide(accounts []unstructured.Unstructured, uid string) Reason {
	for i := range accounts {
		sa := &accounts[i]
		if string(sa.GetUID()) != uid {
			continue
		}
		if sa.GetDeletionTimestamp() != nil {
			return ReasonDeleting
		}
		if sa.GetAnnotations()[OptInAnnotation] != OptInValue {
			return ReasonNotOptedIn
		}
		specState, _, _ := unstructured.NestedString(sa.Object, "spec", "state")
		statusState, _, _ := unstructured.NestedString(sa.Object, "status", "state")
		if specState == "Inactive" || statusState == "Inactive" {
			return ReasonInactive
		}
		return ReasonAllowed
	}
	return ReasonNotFound
}

func fromCertificate(u user.Info) bool {
	for _, id := range u.GetExtra()[user.CredentialIDKey] {
		if strings.HasPrefix(id, certificateCredentialPrefix) {
			return true
		}
	}
	return false
}

func withoutUID(u user.Info) user.Info {
	return &user.DefaultInfo{
		Name:   u.GetName(),
		Groups: append([]string(nil), u.GetGroups()...),
		Extra:  u.GetExtra(),
	}
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
