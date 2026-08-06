package events

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apirequest "k8s.io/apiserver/pkg/endpoints/request"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
)

const (
	// ScopeTypeAnnotation indicates the type of scope (Project, Organization, Platform)
	ScopeTypeAnnotation = "platform.miloapis.com/scope.type"
	// ScopeNameAnnotation indicates the name of the scoped resource
	ScopeNameAnnotation = "platform.miloapis.com/scope.name"

	// ExtraKeyParentType is the user.Extra key containing the parent resource type.
	// Aliased to the canonical constant so emitters and readers of this key
	// cannot drift apart.
	ExtraKeyParentType = iamv1alpha1.ParentKindExtraKey
	// ExtraKeyParentName is the user.Extra key containing the parent resource name.
	ExtraKeyParentName = iamv1alpha1.ParentNameExtraKey
)

// injectScopeAnnotations adds scope annotations to a core/v1 Event based on the
// authenticated request's parent context.
//
// Scope comes only from the request's parent-type/parent-name context — set by
// the parent-context auth filters for end-user requests, or asserted by an
// impersonating caller. It always overwrites any scope annotations already on
// the event, and nothing carried on the event body, the involved object
// included, can influence it. Event fields are client-supplied and therefore
// untrusted for a tenant-isolation decision.
//
// Controllers whose events must reach a tenant's activity feed impersonate that
// parent context rather than relying on inference here; see ProjectEventEmitter
// in internal/controllers/resourcemanager.
//
// Requests carrying no parent context, or only half of it, leave the event
// unannotated. That is normal for system components and platform-level
// operations.
func injectScopeAnnotations(ctx context.Context, event *corev1.Event) error {
	userInfo, ok := apirequest.UserFrom(ctx)
	if !ok {
		return fmt.Errorf("no user in context")
	}

	extras := userInfo.GetExtra()

	parentType := getFirstExtra(extras, ExtraKeyParentType)
	parentName := getFirstExtra(extras, ExtraKeyParentName)

	if parentType == "" || parentName == "" {
		return nil
	}

	if event.Annotations == nil {
		event.Annotations = make(map[string]string)
	}

	event.Annotations[ScopeTypeAnnotation] = parentType
	event.Annotations[ScopeNameAnnotation] = parentName

	return nil
}

func getFirstExtra(extras map[string][]string, key string) string {
	if values, ok := extras[key]; ok && len(values) > 0 {
		return values[0]
	}
	return ""
}
