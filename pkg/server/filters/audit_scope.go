package filters

import (
	"net/http"

	"k8s.io/apiserver/pkg/audit"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/klog/v2"
)

const (
	// PlatformNamespace is the namespace for platform-wide annotations
	PlatformNamespace = "platform.miloapis.com/"

	// Scope annotation keys - immediate scope only
	ScopeTypeKey = PlatformNamespace + "scope.type"
	ScopeNameKey = PlatformNamespace + "scope.name"

	// Scope type values - use PascalCase to match Kubernetes Kind naming convention
	// and align with how Milo sets parent-type in user.extra.
	// "global" remains lowercase as it's an internal default, not from user.extra.
	ScopeTypeGlobal       = "global"
	ScopeTypeOrganization = "Organization"
	ScopeTypeProject      = "Project"
	ScopeTypeUser         = "User"

	// User extra keys (from iam.miloapis.com/v1alpha1/doc.go)
	ParentTypeExtraKey = "iam.miloapis.com/parent-type"
	ParentNameExtraKey = "iam.miloapis.com/parent-name"
)

// AuditScopeAnnotationDecorator adds platform scope annotations to audit events
// based on the immediate parent context set in user.extra by earlier filters.
//
// This filter MUST run after:
//   - UserContextAuthorizationDecorator
//   - OrganizationContextAuthorizationDecorator
//   - ProjectContextAuthorizationDecorator
//
// And BEFORE:
//   - WithAudit (audit filter)
//
// This ensures user.extra is populated and annotations are added before
// audit events are generated.
func AuditScopeAnnotationDecorator(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()

		// Get authenticated user from request context
		userInfo, ok := request.UserFrom(ctx)
		if !ok {
			// No user info available, skip annotation (system requests, health checks)
			handler.ServeHTTP(w, req)
			return
		}

		// Determine immediate scope annotations from user.extra
		annotations := determineScopeAnnotations(userInfo)

		// Log for debugging
		if klog.V(4).Enabled() {
			klog.InfoS("AuditScopeAnnotationDecorator",
				"user", userInfo.GetName(),
				"scope.type", annotations[ScopeTypeKey],
				"scope.name", annotations[ScopeNameKey],
			)
		}

		// Add annotations to audit context
		audit.AddAuditAnnotationsMap(ctx, annotations)

		handler.ServeHTTP(w, req)
	})
}

// determineScopeAnnotations extracts immediate scope information from user.Info.Extra
// and returns platform scope annotations.
//
// This only captures the IMMEDIATE scope of the request (what's in user.extra),
// not any parent/hierarchical relationships. Hierarchical queries should be
// handled in the analytics layer by joining with resource metadata.
func determineScopeAnnotations(userInfo user.Info) map[string]string {
	annotations := make(map[string]string)

	extra := userInfo.GetExtra()
	parentType := getFirstExtraValue(extra, ParentTypeExtraKey)
	parentName := getFirstExtraValue(extra, ParentNameExtraKey)

	switch parentType {
	case "Project":
		annotations[ScopeTypeKey] = ScopeTypeProject
		if parentName != "" {
			annotations[ScopeNameKey] = parentName
		}

	case "Organization":
		annotations[ScopeTypeKey] = ScopeTypeOrganization
		if parentName != "" {
			annotations[ScopeNameKey] = parentName
		}

	case "User":
		annotations[ScopeTypeKey] = ScopeTypeUser
		if parentName != "" {
			annotations[ScopeNameKey] = parentName
		}

	default:
		// No parent context = global/platform scope
		annotations[ScopeTypeKey] = ScopeTypeGlobal
		// No scope.name for global requests
	}

	return annotations
}

// getFirstExtraValue returns the first value from user.Extra for the given key,
// or empty string if the key doesn't exist or has no values.
func getFirstExtraValue(extra map[string][]string, key string) string {
	values, ok := extra[key]
	if !ok || len(values) == 0 {
		return ""
	}
	return values[0]
}
