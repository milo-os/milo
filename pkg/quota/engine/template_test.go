package engine

import (
	"testing"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apiserver/pkg/endpoints/request"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
)

// mockCELEngine implements CELEngine for testing
type mockCELEngine struct{}

func (m *mockCELEngine) ValidateConstraints(constraints []quotav1alpha1.ConditionExpression) error {
	return nil
}

func (m *mockCELEngine) ValidateTemplateExpression(expression string) error {
	return nil
}

func (m *mockCELEngine) EvaluateConditions(conditions []quotav1alpha1.ConditionExpression, obj *unstructured.Unstructured) (bool, error) {
	return true, nil
}

func (m *mockCELEngine) EvaluateTemplateExpression(expression string, variables map[string]interface{}) (string, error) {
	// Simple mock that evaluates expressions based on variables
	switch expression {
	case "trigger.metadata.name":
		if trigger, ok := variables["trigger"].(map[string]interface{}); ok {
			if metadata, ok := trigger["metadata"].(map[string]interface{}); ok {
				if name, ok := metadata["name"].(string); ok {
					return name, nil
				}
			}
		}
		return "test-resource", nil
	case "trigger.metadata.namespace":
		if trigger, ok := variables["trigger"].(map[string]interface{}); ok {
			if metadata, ok := trigger["metadata"].(map[string]interface{}); ok {
				if namespace, ok := metadata["namespace"].(string); ok {
					return namespace, nil
				}
			}
		}
		return "test-namespace", nil
	case "user.name":
		if user, ok := variables["user"].(map[string]interface{}); ok {
			if name, ok := user["name"].(string); ok {
				return name, nil
			}
		}
		return "test-user", nil
	case "requestInfo.verb":
		if requestInfo, ok := variables["requestInfo"].(map[string]interface{}); ok {
			if verb, ok := requestInfo["verb"].(string); ok {
				return verb, nil
			}
		}
		return "create", nil
	default:
		return expression, nil
	}
}

func TestCELTemplateRendering(t *testing.T) {
	engine := NewTemplateEngine(&mockCELEngine{}, logr.Discard())

	// Test object
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"name":      "test-resource",
				"namespace": "test-namespace",
			},
		},
	}

	tests := []struct {
		name     string
		template string
		expected string
	}{
		{
			name:     "literal template",
			template: "literal-value",
			expected: "literal-value",
		},
		{
			name:     "simple CEL expression",
			template: "{{trigger.metadata.name}}",
			expected: "test-resource",
		},
		{
			name:     "mixed literal and CEL",
			template: "{{trigger.metadata.name}}-claim",
			expected: "test-resource-claim",
		},
		{
			name:     "multiple CEL expressions",
			template: "{{trigger.metadata.namespace}}/{{trigger.metadata.name}}",
			expected: "test-namespace/test-resource",
		},
		{
			name:     "empty template",
			template: "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			templateEngine := engine.(*templateEngine)
			// Build simple variables map for testing
			variables := map[string]interface{}{
				"trigger": obj.Object,
			}
			result, err := templateEngine.renderCELTemplate(tt.template, variables)
			if err != nil {
				t.Fatalf("renderCELTemplate() error = %v", err)
			}
			if result != tt.expected {
				t.Errorf("renderCELTemplate() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestRenderClaim(t *testing.T) {
	engine := NewTemplateEngine(&mockCELEngine{}, logr.Discard())

	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"name":      "test-project",
				"namespace": "test-namespace",
			},
		},
	}

	evalContext := &EvaluationContext{
		Object:    obj,
		Namespace: "test-namespace",
	}

	policy := &quotav1alpha1.ClaimCreationPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-policy",
		},
		Spec: quotav1alpha1.ClaimCreationPolicySpec{
			Target: quotav1alpha1.ClaimTargetSpec{
				ResourceClaimTemplate: quotav1alpha1.ResourceClaimTemplate{
					Metadata: quotav1alpha1.ObjectMetaTemplate{
						Name: "{{trigger.metadata.name}}-claim",
					},
					Spec: quotav1alpha1.ResourceClaimSpec{
						Requests: []quotav1alpha1.ResourceRequest{
							{
								ResourceType: "{{trigger.metadata.name}}-type",
								Amount:       10,
							},
						},
						ConsumerRef: quotav1alpha1.ConsumerRef{
							Name: "{{trigger.metadata.name}}-consumer",
						},
					},
				},
			},
		},
	}

	result, err := engine.RenderClaim(policy, evalContext)
	if err != nil {
		t.Fatalf("RenderClaim() error = %v", err)
	}

	// Verify claim metadata
	if result.Name != "test-project-claim" {
		t.Errorf("Name = %v, want %v", result.Name, "test-project-claim")
	}

	if result.Namespace != "test-namespace" {
		t.Errorf("Namespace = %v, want %v", result.Namespace, "test-namespace")
	}

	if result.APIVersion != "quota.miloapis.com/v1alpha1" {
		t.Errorf("APIVersion = %v, want %v", result.APIVersion, "quota.miloapis.com/v1alpha1")
	}

	if result.Kind != "ResourceClaim" {
		t.Errorf("Kind = %v, want %v", result.Kind, "ResourceClaim")
	}

	// Verify claim spec
	if len(result.Spec.Requests) != 1 {
		t.Fatalf("Expected 1 request, got %d", len(result.Spec.Requests))
	}

	if result.Spec.Requests[0].ResourceType != "test-project-type" {
		t.Errorf("ResourceType = %v, want %v", result.Spec.Requests[0].ResourceType, "test-project-type")
	}

	if result.Spec.ConsumerRef.Name != "test-project-consumer" {
		t.Errorf("ConsumerRef.Name = %v, want %v", result.Spec.ConsumerRef.Name, "test-project-consumer")
	}
}

func TestRenderClaimWithUserContext(t *testing.T) {
	engine := NewTemplateEngine(&mockCELEngine{}, logr.Discard())

	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"name":      "test-project",
				"namespace": "test-namespace",
			},
		},
	}

	evalContext := &EvaluationContext{
		Object:    obj,
		Namespace: "test-namespace",
		User: UserContext{
			Name:   "test-user",
			UID:    "user-123",
			Groups: []string{"developers"},
		},
		RequestInfo: &request.RequestInfo{
			Verb:     "create",
			Resource: "projects",
		},
	}

	policy := &quotav1alpha1.ClaimCreationPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-user-policy",
		},
		Spec: quotav1alpha1.ClaimCreationPolicySpec{
			Target: quotav1alpha1.ClaimTargetSpec{
				ResourceClaimTemplate: quotav1alpha1.ResourceClaimTemplate{
					Metadata: quotav1alpha1.ObjectMetaTemplate{
						Name: "{{trigger.metadata.name}}-{{user.name}}-claim",
						Annotations: map[string]string{
							"created-by":   "{{user.name}}",
							"request-verb": "{{requestInfo.verb}}",
							"trigger-name": "{{trigger.metadata.name}}",
						},
					},
					Spec: quotav1alpha1.ResourceClaimSpec{
						Requests: []quotav1alpha1.ResourceRequest{
							{
								ResourceType: "{{trigger.metadata.name}}-type",
								Amount:       5,
							},
						},
						ConsumerRef: quotav1alpha1.ConsumerRef{
							Name: "{{user.name}}-consumer",
						},
					},
				},
			},
		},
	}

	result, err := engine.RenderClaim(policy, evalContext)
	if err != nil {
		t.Fatalf("RenderClaim() error = %v", err)
	}

	// Verify name includes both trigger and user context
	expectedName := "test-project-test-user-claim"
	if result.Name != expectedName {
		t.Errorf("Name = %v, want %v", result.Name, expectedName)
	}

	// Verify annotations include user and requestInfo context
	if result.Annotations["created-by"] != "test-user" {
		t.Errorf("Annotation 'created-by' = %v, want %v", result.Annotations["created-by"], "test-user")
	}

	if result.Annotations["request-verb"] != "create" {
		t.Errorf("Annotation 'request-verb' = %v, want %v", result.Annotations["request-verb"], "create")
	}

	if result.Annotations["trigger-name"] != "test-project" {
		t.Errorf("Annotation 'trigger-name' = %v, want %v", result.Annotations["trigger-name"], "test-project")
	}

	// Verify namespace
	if result.Namespace != "test-namespace" {
		t.Errorf("Namespace = %v, want %v", result.Namespace, "test-namespace")
	}

	// Verify generateName is empty when not specified
	if result.GenerateName != "" {
		t.Errorf("GenerateName = %v, want empty string", result.GenerateName)
	}

	// Verify labels are empty when not specified
	if len(result.Labels) != 0 {
		t.Errorf("Labels = %v, want empty map", result.Labels)
	}

	// Verify spec uses user context correctly
	if result.Spec.ConsumerRef.Name != "test-user-consumer" {
		t.Errorf("ConsumerRef.Name = %v, want %v", result.Spec.ConsumerRef.Name, "test-user-consumer")
	}
}
