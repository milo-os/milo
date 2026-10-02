package webhook

import (
	"context"
	"fmt"
	"slices"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
)

// parentContext holds the parent-context extras that the Milo API server's
// decorators inject into the request user's Extra when a request reaches a
// resource through a parent control plane (an organization or a project).
type parentContext struct {
	hasParent bool
	apiGroup  string
	kind      string
	name      string
}

// parentContextFromRequest reads the parent-context extras from the admission
// request in ctx. It returns:
//   - (zero, nil) when the request carries no parent extras (platform scope or
//     an internal controller).
//   - (zero, nil) when the request user is in system:masters, which bypasses
//     the parent-context validation entirely (matching the organization, user,
//     and policybinding webhooks); treating superusers as carrying no parent
//     context lets every caller pass their request through unchanged.
//   - (pc, nil) when the request carries well-formed parent extras.
//   - (zero, err) on webhook-infrastructure failures: a missing admission
//     request, or malformed parent extras.
func parentContextFromRequest(ctx context.Context) (parentContext, error) {
	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		return parentContext{}, apierrors.NewInternalError(fmt.Errorf("failed to get request from context: %w", err))
	}

	if slices.Contains(req.UserInfo.Groups, "system:masters") {
		return parentContext{}, nil
	}

	parentName, parentNameOk := req.UserInfo.Extra[iamv1alpha1.ParentNameExtraKey]
	parentKind, parentKindOk := req.UserInfo.Extra[iamv1alpha1.ParentKindExtraKey]
	parentAPIGroup, parentAPIGroupOk := req.UserInfo.Extra[iamv1alpha1.ParentAPIGroupExtraKey]

	// Not a parent-scoped request (e.g. platform scope or an internal
	// controller).
	if !parentNameOk || !parentKindOk || !parentAPIGroupOk {
		return parentContext{}, nil
	}

	if len(parentKind) != 1 || len(parentName) != 1 || len(parentAPIGroup) != 1 {
		return parentContext{}, apierrors.NewInternalError(fmt.Errorf("request context has malformed parent information"))
	}

	return parentContext{
		hasParent: true,
		apiGroup:  parentAPIGroup[0],
		kind:      parentKind[0],
		name:      parentName[0],
	}, nil
}

// OrgContextFromRequest extracts the request organization id when the request
// is made in an organization parent context.
//
// It returns:
//   - (orgID, true, nil) when the request carries the parent-context extras for
//     an Organization.
//   - ("", false, nil) when the request is not in an organization context
//     (platform scope, an internal controller, a project or user parent scope,
//     or system:masters, which bypasses the parent-context validation), so
//     callers pass the object through unchanged.
//   - ("", false, err) on webhook-infrastructure failures (a missing admission
//     request or malformed parent extras), which callers surface as an internal
//     error rather than a validation denial.
func OrgContextFromRequest(ctx context.Context) (string, bool, error) {
	pc, err := parentContextFromRequest(ctx)
	if err != nil {
		return "", false, err
	}
	if !pc.hasParent || pc.kind != "Organization" || pc.apiGroup != resourcemanagerv1alpha1.GroupVersion.Group {
		return "", false, nil
	}
	return pc.name, true, nil
}

// ProjectContextFromRequest reports whether the request is made in a project
// parent context (the scope a project control plane would inject today), and
// returns the project id when it is. Callers that do not support project scope
// should fail closed on it rather than pass the object through. See
// OrgContextFromRequest for the return conventions.
func ProjectContextFromRequest(ctx context.Context) (string, bool, error) {
	pc, err := parentContextFromRequest(ctx)
	if err != nil {
		return "", false, err
	}
	if !pc.hasParent || pc.kind != "Project" || pc.apiGroup != resourcemanagerv1alpha1.GroupVersion.Group {
		return "", false, nil
	}
	return pc.name, true, nil
}
