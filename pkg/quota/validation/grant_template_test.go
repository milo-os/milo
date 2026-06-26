package validation

import (
	"context"
	"fmt"
	"testing"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// MockResourceTypeValidator for testing
type MockResourceTypeValidator struct {
	validResourceTypes map[string]bool
}

func (m *MockResourceTypeValidator) ValidateResourceType(ctx context.Context, resourceType string) error {
	if m.validResourceTypes[resourceType] {
		return nil
	}
	return fmt.Errorf("resource type '%s' is not registered", resourceType)
}

func (m *MockResourceTypeValidator) IsClaimingResourceAllowed(ctx context.Context, resourceType string, consumerRef quotav1alpha1.ConsumerRef, claimingAPIGroup, claimingKind string) (bool, []string, error) {
	// Simple mock implementation - allow all claiming resources for valid resource types
	if m.validResourceTypes[resourceType] {
		return true, []string{fmt.Sprintf("%s/%s", claimingAPIGroup, claimingKind)}, nil
	}
	return false, nil, fmt.Errorf("resource type '%s' is not registered", resourceType)
}

func (m *MockResourceTypeValidator) IsResourceTypeRegistered(resourceType string) bool {
	return false
}

func (m *MockResourceTypeValidator) HasSynced() bool { return true }

func TestValidateLabelKey(t *testing.T) {
	validator, err := NewGrantTemplateValidator(&MockResourceTypeValidator{
		validResourceTypes: map[string]bool{
			"test.example.com/projects": true,
		},
	})
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	tests := []struct {
		name        string
		key         string
		expectError bool
		description string
	}{
		{
			name:        "valid simple key",
			key:         "environment",
			expectError: false,
			description: "Simple alphanumeric label key should be valid",
		},
		{
			name:        "valid key with hyphens",
			key:         "app-name",
			expectError: false,
			description: "Label key with hyphens should be valid",
		},
		{
			name:        "valid key with dots",
			key:         "app.name",
			expectError: false,
			description: "Label key with dots should be valid",
		},
		{
			name:        "valid key with underscores",
			key:         "app_name",
			expectError: false,
			description: "Label key with underscores should be valid",
		},
		{
			name:        "valid prefixed key with slash",
			key:         "quota.miloapis.com/auto-created",
			expectError: false,
			description: "Prefixed label key with forward slash should be valid (this was the bug)",
		},
		{
			name:        "valid kubernetes.io prefix",
			key:         "kubernetes.io/arch",
			expectError: false,
			description: "Kubernetes.io prefixed label should be valid",
		},
		{
			name:        "valid app.kubernetes.io prefix",
			key:         "app.kubernetes.io/name",
			expectError: false,
			description: "app.kubernetes.io prefixed label should be valid",
		},
		{
			name:        "valid prefixed key with subdomain",
			key:         "example.com/component",
			expectError: false,
			description: "Prefixed label with subdomain should be valid",
		},
		{
			name:        "empty key",
			key:         "",
			expectError: true,
			description: "Empty label key should be invalid",
		},
		{
			name:        "key starting with hyphen",
			key:         "-invalid",
			expectError: true,
			description: "Label key starting with hyphen should be invalid",
		},
		{
			name:        "key ending with hyphen",
			key:         "invalid-",
			expectError: true,
			description: "Label key ending with hyphen should be invalid",
		},
		{
			name:        "key with invalid characters",
			key:         "invalid@key",
			expectError: true,
			description: "Label key with @ symbol should be invalid",
		},
		{
			name:        "key too long",
			key:         "this-is-a-very-long-label-key-that-exceeds-the-maximum-length-allowed-by-kubernetes-and-should-fail-validation",
			expectError: true,
			description: "Label key longer than 63 characters should be invalid",
		},
		{
			name:        "key with multiple slashes",
			key:         "invalid/multiple/slashes",
			expectError: true,
			description: "Label key with multiple slashes should be invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validator.validateLabelKey(tt.key, field.NewPath("labels").Key(tt.key))
			if tt.expectError && len(errs) == 0 {
				t.Errorf("Expected error for key '%s', but got none. %s", tt.key, tt.description)
			}
			if !tt.expectError && len(errs) > 0 {
				t.Errorf("Unexpected error for key '%s': %v. %s", tt.key, errs, tt.description)
			}
		})
	}
}

func TestValidateLabelValue(t *testing.T) {
	validator, err := NewGrantTemplateValidator(&MockResourceTypeValidator{
		validResourceTypes: map[string]bool{
			"test.example.com/projects": true,
		},
	})
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	tests := []struct {
		name        string
		value       string
		expectError bool
		description string
	}{
		{
			name:        "valid simple value",
			value:       "production",
			expectError: false,
			description: "Simple alphanumeric label value should be valid",
		},
		{
			name:        "valid value with hyphens",
			value:       "my-app",
			expectError: false,
			description: "Label value with hyphens should be valid",
		},
		{
			name:        "valid value with dots",
			value:       "1.0.0",
			expectError: false,
			description: "Label value with dots should be valid",
		},
		{
			name:        "valid value with underscores",
			value:       "my_app",
			expectError: false,
			description: "Label value with underscores should be valid",
		},
		{
			name:        "valid empty value",
			value:       "",
			expectError: false,
			description: "Empty label value should be valid",
		},
		{
			name:        "valid numeric value",
			value:       "123",
			expectError: false,
			description: "Numeric label value should be valid",
		},
		{
			name:        "valid boolean-like value",
			value:       "true",
			expectError: false,
			description: "Boolean-like label value should be valid",
		},
		{
			name:        "value starting with hyphen",
			value:       "-invalid",
			expectError: true,
			description: "Label value starting with hyphen should be invalid",
		},
		{
			name:        "value ending with hyphen",
			value:       "invalid-",
			expectError: true,
			description: "Label value ending with hyphen should be invalid",
		},
		{
			name:        "value with invalid characters",
			value:       "invalid@value",
			expectError: true,
			description: "Label value with @ symbol should be invalid",
		},
		{
			name:        "value too long",
			value:       "this-is-a-very-long-label-value-that-exceeds-the-maximum-length-allowed-by-kubernetes-and-should-fail",
			expectError: true,
			description: "Label value longer than 63 characters should be invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validator.validateLabelValue(tt.value, field.NewPath("labels").Key("test-key"))
			if tt.expectError && len(errs) == 0 {
				t.Errorf("Expected error for value '%s', but got none. %s", tt.value, tt.description)
			}
			if !tt.expectError && len(errs) > 0 {
				t.Errorf("Unexpected error for value '%s': %v. %s", tt.value, errs, tt.description)
			}
		})
	}
}

func TestValidateMetadataTemplateLabels(t *testing.T) {
	validator, err := NewGrantTemplateValidator(&MockResourceTypeValidator{
		validResourceTypes: map[string]bool{
			"test.example.com/projects": true,
		},
	})
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	tests := []struct {
		name        string
		metadata    quotav1alpha1.ObjectMetaTemplate
		expectError bool
		description string
	}{
		{
			name: "valid labels with prefixed keys",
			metadata: quotav1alpha1.ObjectMetaTemplate{
				Name:      "test-grant",
				Namespace: "test-namespace",
				Labels: map[string]string{
					"quota.miloapis.com/auto-created": "true",
					"quota.miloapis.com/policy":       "test-policy",
					"app.kubernetes.io/name":          "milo",
					"app.kubernetes.io/component":     "quota-system",
					"environment":                     "production",
					"version":                         "1.0.0",
				},
			},
			expectError: false,
			description: "All production-like labels should be valid",
		},
		{
			name: "invalid label key",
			metadata: quotav1alpha1.ObjectMetaTemplate{
				Name:      "test-grant",
				Namespace: "test-namespace",
				Labels: map[string]string{
					"invalid@key": "value",
				},
			},
			expectError: true,
			description: "Label with invalid key should fail validation",
		},
		{
			name: "invalid label value",
			metadata: quotav1alpha1.ObjectMetaTemplate{
				Name:      "test-grant",
				Namespace: "test-namespace",
				Labels: map[string]string{
					"valid-key": "invalid@value",
				},
			},
			expectError: true,
			description: "Label with invalid value should fail validation",
		},
		{
			name: "valid annotations with templating",
			metadata: quotav1alpha1.ObjectMetaTemplate{
				Name:      "test-grant",
				Namespace: "test-namespace",
				Annotations: map[string]string{
					"quota.miloapis.com/description":  "Auto-generated grant for test-resource",
					"quota.miloapis.com/created-by":   "grant-creation-policy",
					"quota.miloapis.com/organization": "test-org",
				},
			},
			expectError: false,
			description: "Valid annotations with CEL template expressions should be valid",
		},
		{
			name: "invalid CEL expression syntax",
			metadata: quotav1alpha1.ObjectMetaTemplate{
				Name:      "test-grant",
				Namespace: "test-namespace",
				Annotations: map[string]string{
					"quota.miloapis.com/description": "Invalid CEL {{invalid..syntax}}",
				},
			},
			expectError: true,
			description: "Invalid CEL syntax should fail validation",
		},
		{
			name: "invalid template variable",
			metadata: quotav1alpha1.ObjectMetaTemplate{
				Name:      "test-grant",
				Namespace: "test-namespace",
				Annotations: map[string]string{
					"quota.miloapis.com/description": "Invalid variable {{invalid + '.field'}}",
				},
			},
			expectError: true,
			description: "Invalid template variable should fail validation",
		},
		{
			name: "mixed literal and CEL content",
			metadata: quotav1alpha1.ObjectMetaTemplate{
				Name:      "test-grant",
				Namespace: "test-namespace",
				Annotations: map[string]string{
					"quota.miloapis.com/description": "Grant for test-resource in production",
				},
			},
			expectError: false,
			description: "Mixed literal and CEL content should be valid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validator.ValidateMetadataTemplate(tt.metadata, field.NewPath("metadata"))
			if tt.expectError && len(errs) == 0 {
				t.Errorf("Expected error for metadata, but got none. %s", tt.description)
			}
			if !tt.expectError && len(errs) > 0 {
				t.Errorf("Unexpected error for metadata: %v. %s", errs, tt.description)
			}
		})
	}
}

func TestGrantTemplateValidator_ValidateGrantTemplate(t *testing.T) {
	validator, err := NewGrantTemplateValidator(&MockResourceTypeValidator{
		validResourceTypes: map[string]bool{
			"test.example.com/projects": true,
		},
	})
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	tests := []struct {
		name        string
		template    quotav1alpha1.ResourceGrantTemplate
		expectError bool
		description string
	}{
		{
			name: "valid template with CEL expressions",
			template: quotav1alpha1.ResourceGrantTemplate{
				Metadata: quotav1alpha1.ObjectMetaTemplate{
					Name:      "{{trigger.metadata.name + '-grant'}}",
					Namespace: "{{trigger.metadata.namespace}}",
					Annotations: map[string]string{
						"created-for":  "{{trigger.metadata.name}}",
						"trigger-kind": "{{trigger.kind}}",
						"description":  "{{trigger.metadata.name + ' quota grant'}}",
					},
				},
				Spec: quotav1alpha1.ResourceGrantSpec{
					ConsumerRef: quotav1alpha1.ConsumerRef{
						APIGroup: "resourcemanager.miloapis.com",
						Kind:     "Organization",
						Name:     "test-org",
					},
					Allowances: []quotav1alpha1.Allowance{
						{
							ResourceType: "test.example.com/projects",
							Buckets: []quotav1alpha1.Bucket{
								{Amount: 10},
							},
						},
					},
				},
			},
			expectError: false,
			description: "Valid template with CEL expressions should pass",
		},
		{
			name: "template with mixed literal and CEL expressions",
			template: quotav1alpha1.ResourceGrantTemplate{
				Metadata: quotav1alpha1.ObjectMetaTemplate{
					Name:      "{{trigger.metadata.name}}-grant",
					Namespace: "{{trigger.metadata.namespace}}",
					Annotations: map[string]string{
						"description": "Grant for {{trigger.metadata.name}} in production environment",
						"source":      "policy-{{trigger.kind}}",
					},
				},
				Spec: quotav1alpha1.ResourceGrantSpec{
					ConsumerRef: quotav1alpha1.ConsumerRef{
						APIGroup: "resourcemanager.miloapis.com",
						Kind:     "Organization",
						Name:     "test-org",
					},
					Allowances: []quotav1alpha1.Allowance{
						{
							ResourceType: "test.example.com/projects",
							Buckets: []quotav1alpha1.Bucket{
								{Amount: 5},
							},
						},
					},
				},
			},
			expectError: false,
			description: "Template with mixed literal and CEL expressions should pass",
		},
		{
			name: "template with invalid CEL expression",
			template: quotav1alpha1.ResourceGrantTemplate{
				Metadata: quotav1alpha1.ObjectMetaTemplate{
					Name: "{{invalid..syntax}}",
				},
				Spec: quotav1alpha1.ResourceGrantSpec{
					ConsumerRef: quotav1alpha1.ConsumerRef{
						APIGroup: "resourcemanager.miloapis.com",
						Kind:     "Organization",
						Name:     "test-org",
					},
					Allowances: []quotav1alpha1.Allowance{
						{
							ResourceType: "test.example.com/projects",
							Buckets: []quotav1alpha1.Bucket{
								{Amount: 5},
							},
						},
					},
				},
			},
			expectError: true,
			description: "Template with invalid CEL syntax should fail",
		},
		{
			name: "template with invalid variable",
			template: quotav1alpha1.ResourceGrantTemplate{
				Metadata: quotav1alpha1.ObjectMetaTemplate{
					Name: "{{user.name + '-grant'}}",
				},
				Spec: quotav1alpha1.ResourceGrantSpec{
					ConsumerRef: quotav1alpha1.ConsumerRef{
						APIGroup: "resourcemanager.miloapis.com",
						Kind:     "Organization",
						Name:     "test-org",
					},
					Allowances: []quotav1alpha1.Allowance{
						{
							ResourceType: "test.example.com/projects",
							Buckets: []quotav1alpha1.Bucket{
								{Amount: 5},
							},
						},
					},
				},
			},
			expectError: true,
			description: "Template with invalid variable (user not allowed in grant templates) should fail",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validator.ValidateGrantTemplate(context.Background(), tt.template, ControllerValidationOptions())
			if tt.expectError && len(errs) == 0 {
				t.Errorf("Expected error for template, but got none. %s", tt.description)
			}
			if !tt.expectError && len(errs) > 0 {
				t.Errorf("Unexpected error for template: %v. %s", errs, tt.description)
			}
		})
	}
}
