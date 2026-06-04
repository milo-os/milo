package events

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apirequest "k8s.io/apiserver/pkg/endpoints/request"
)

const (
	// ScopeTypeAnnotation indicates the type of scope (Project, Organization, Platform)
	ScopeTypeAnnotation = "platform.miloapis.com/scope.type"
	// ScopeNameAnnotation indicates the name of the scoped resource
	ScopeNameAnnotation = "platform.miloapis.com/scope.name"

	// ExtraKeyParentType is the user.Extra key containing the parent resource type
	ExtraKeyParentType = "iam.miloapis.com/parent-type"
	// ExtraKeyParentName is the user.Extra key containing the parent resource name
	ExtraKeyParentName = "iam.miloapis.com/parent-name"
)

// injectScopeAnnotations adds scope annotations to a core/v1 Event based on user context
func injectScopeAnnotations(ctx context.Context, event *corev1.Event) error {
	userInfo, ok := apirequest.UserFrom(ctx)
	if !ok {
		return fmt.Errorf("no user in context")
	}

	extras := userInfo.GetExtra()

	parentType := getFirstExtra(extras, ExtraKeyParentType)
	parentName := getFirstExtra(extras, ExtraKeyParentName)

	// Events without scope context are normal (e.g., system components, platform-level operations)
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
