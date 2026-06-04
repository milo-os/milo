// Package discovery implements parent-context-aware filtering of the
// Kubernetes API discovery responses served by the Milo control plane.
//
// Resources opt in to a set of "parent contexts" they are visible in
// (Organization, Project, User, or Platform). Discovery responses are filtered
// per request based on the URL prefix the client used (e.g.
// /apis/resourcemanager.miloapis.com/v1alpha1/organizations/{id}/control-plane/...).
//
// This is a discovery hint, NOT an enforcement boundary. See
// docs/discovery-contexts.md for the rationale and the companion admission
// check.
package discovery

import (
	"context"
	"slices"
	"strings"

	"go.miloapis.com/milo/pkg/request"
	"go.miloapis.com/milo/pkg/server/filters"
)

// ParentContextsAnnotation is the CRD annotation that lists the parent
// contexts a resource is visible in. Comma-separated, e.g.
//
//	discovery.miloapis.com/parent-contexts: "Organization,Project"
//
// Missing or empty annotation means "visible in all contexts" — chosen so
// that pre-existing CRDs and external CRDs that have not adopted the marker
// continue to behave exactly as before.
const ParentContextsAnnotation = "discovery.miloapis.com/parent-contexts"

// AllContextsWildcard, when present in the annotation value, is equivalent to
// omitting the annotation: visible in all parent contexts.
const AllContextsWildcard = "*"

// ParentContext identifies the platform context a request is being made in.
type ParentContext string

const (
	// ContextPlatform is requests against the platform root (no
	// /organizations/{id} or /projects/{id}/control-plane prefix). This is
	// where platform-wide resources like Organizations and Users live.
	ContextPlatform ParentContext = "Platform"

	// ContextOrganization is requests routed through
	// /apis/resourcemanager.miloapis.com/v1alpha1/organizations/{id}/control-plane/...
	ContextOrganization ParentContext = "Organization"

	// ContextProject is requests routed through .../projects/{id}/control-plane/...
	ContextProject ParentContext = "Project"

	// ContextUser is requests routed through
	// /apis/iam.miloapis.com/v1alpha1/users/{id}/control-plane/...
	ContextUser ParentContext = "User"
)

// FromRequest returns the parent context of the current request based on the
// values stashed on the context by the existing path-routing handlers.
//
// Project takes precedence over Organization (matches the existing
// authorization decorator order in cmd/milo/apiserver/config.go).
func FromRequest(ctx context.Context) ParentContext {
	if _, ok := request.ProjectID(ctx); ok {
		return ContextProject
	}
	if _, ok := filters.OrganizationID(ctx); ok {
		return ContextOrganization
	}
	if _, ok := filters.UserID(ctx); ok {
		return ContextUser
	}
	return ContextPlatform
}

// ParseContexts parses the annotation value into a set of ParentContexts.
// Comma OR semicolon separated — semicolons let Go-defined types use
// `+kubebuilder:metadata:annotations` markers without escaping commas, which
// controller-gen treats as its own field delimiter.
//
// An empty input or one containing the wildcard returns nil, which callers
// should treat as "visible in all contexts".
func ParseContexts(annotation string) []ParentContext {
	annotation = strings.TrimSpace(annotation)
	if annotation == "" {
		return nil
	}
	out := make([]ParentContext, 0, 4)
	for _, raw := range strings.FieldsFunc(annotation, func(r rune) bool { return r == ',' || r == ';' }) {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		if v == AllContextsWildcard {
			return nil
		}
		out = append(out, ParentContext(v))
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Matches reports whether a resource tagged with `allowed` should be visible
// in the given current context. A nil/empty `allowed` is the "all contexts"
// wildcard.
func Matches(allowed []ParentContext, current ParentContext) bool {
	if len(allowed) == 0 {
		return true
	}
	return slices.Contains(allowed, current)
}
