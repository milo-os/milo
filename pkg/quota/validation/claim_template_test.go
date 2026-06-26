package validation

import (
	"testing"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
)

func TestValidateClaimTemplate(t *testing.T) {
	tests := []struct {
		name        string
		template    quotav1alpha1.ResourceClaimTemplate
		expectError bool
		description string
	}{
		{
			name: "valid template with CEL expressions",
			template: quotav1alpha1.ResourceClaimTemplate{
				Metadata: quotav1alpha1.ObjectMetaTemplate{
					Name:      "{{trigger.metadata.name + '-claim'}}",
					Namespace: "{{trigger.metadata.namespace}}",
					Annotations: map[string]string{
						"created-for":  "{{trigger.metadata.name}}",
						"trigger-kind": "{{trigger.kind}}",
						"description":  "{{trigger.metadata.name + ' quota claim'}}",
						"created-by":   "{{user.name}}",
						"request-verb": "{{requestInfo.verb}}",
					},
				},
				Spec: quotav1alpha1.ResourceClaimSpec{
					ConsumerRef: quotav1alpha1.ConsumerRef{
						APIGroup: "resourcemanager.miloapis.com",
						Kind:     "Organization",
						Name:     "test-org",
					},
					Requests: []quotav1alpha1.ResourceRequest{
						{
							ResourceType: "test.example.com/projects",
							Amount:       1,
						},
					},
				},
			},
			expectError: false,
			description: "Valid template with CEL expressions should pass",
		},
		{
			name: "valid template with literal values",
			template: quotav1alpha1.ResourceClaimTemplate{
				Metadata: quotav1alpha1.ObjectMetaTemplate{
					Name:      "test-claim",
					Namespace: "test-namespace",
					Annotations: map[string]string{
						"created-by": "test-user",
						"resource":   "Auto created for team A",
					},
				},
				Spec: quotav1alpha1.ResourceClaimSpec{
					ConsumerRef: quotav1alpha1.ConsumerRef{
						APIGroup: "resourcemanager.miloapis.com",
						Kind:     "Organization",
						Name:     "test-org",
					},
					Requests: []quotav1alpha1.ResourceRequest{
						{
							ResourceType: "test.example.com/projects",
							Amount:       1,
						},
					},
				},
			},
			expectError: false,
			description: "Valid template with literal values should pass",
		},
		{
			name: "valid template with literal generateName prefix",
			template: quotav1alpha1.ResourceClaimTemplate{
				Metadata: quotav1alpha1.ObjectMetaTemplate{
					GenerateName: "team-",
					Namespace:    "team-namespace",
					Annotations: map[string]string{
						"note": "Created for onboarding",
					},
				},
				Spec: quotav1alpha1.ResourceClaimSpec{
					ConsumerRef: quotav1alpha1.ConsumerRef{
						APIGroup: "resourcemanager.miloapis.com",
						Kind:     "Organization",
						Name:     "test-org",
					},
					Requests: []quotav1alpha1.ResourceRequest{
						{
							ResourceType: "test.example.com/projects",
							Amount:       1,
						},
					},
				},
			},
			expectError: false,
			description: "Valid generateName literal ending with hyphen should pass",
		},
		{
			name: "valid template with templated generateName prefix",
			template: quotav1alpha1.ResourceClaimTemplate{
				Metadata: quotav1alpha1.ObjectMetaTemplate{
					GenerateName: "{{trigger.metadata.name + '-'}}",
					Namespace:    "{{trigger.metadata.namespace}}",
					Annotations: map[string]string{
						"note": "Template driven generateName",
					},
				},
				Spec: quotav1alpha1.ResourceClaimSpec{
					ConsumerRef: quotav1alpha1.ConsumerRef{
						APIGroup: "resourcemanager.miloapis.com",
						Kind:     "Organization",
						Name:     "test-org",
					},
					Requests: []quotav1alpha1.ResourceRequest{
						{
							ResourceType: "test.example.com/projects",
							Amount:       1,
						},
					},
				},
			},
			expectError: false,
			description: "Templated generateName that appends hyphen should pass",
		},
		{
			name: "template with invalid generateName literal",
			template: quotav1alpha1.ResourceClaimTemplate{
				Metadata: quotav1alpha1.ObjectMetaTemplate{
					GenerateName: "invalid",
				},
				Spec: quotav1alpha1.ResourceClaimSpec{
					ConsumerRef: quotav1alpha1.ConsumerRef{
						APIGroup: "resourcemanager.miloapis.com",
						Kind:     "Organization",
						Name:     "test-org",
					},
					Requests: []quotav1alpha1.ResourceRequest{
						{
							ResourceType: "test.example.com/projects",
							Amount:       1,
						},
					},
				},
			},
			expectError: true,
			description: "GenerateName without trailing hyphen should fail",
		},
		{
			name: "template with both name and generateName",
			template: quotav1alpha1.ResourceClaimTemplate{
				Metadata: quotav1alpha1.ObjectMetaTemplate{
					Name:         "test-claim",
					GenerateName: "test-claim-",
				},
				Spec: quotav1alpha1.ResourceClaimSpec{
					ConsumerRef: quotav1alpha1.ConsumerRef{
						APIGroup: "resourcemanager.miloapis.com",
						Kind:     "Organization",
						Name:     "test-org",
					},
					Requests: []quotav1alpha1.ResourceRequest{
						{
							ResourceType: "test.example.com/projects",
							Amount:       1,
						},
					},
				},
			},
			expectError: true,
			description: "Template with both name and generateName should fail",
		},
		{
			name: "template with invalid CEL expression",
			template: quotav1alpha1.ResourceClaimTemplate{
				Metadata: quotav1alpha1.ObjectMetaTemplate{
					Name: "{{invalid..syntax}}",
				},
				Spec: quotav1alpha1.ResourceClaimSpec{
					ConsumerRef: quotav1alpha1.ConsumerRef{
						APIGroup: "resourcemanager.miloapis.com",
						Kind:     "Organization",
						Name:     "test-org",
					},
					Requests: []quotav1alpha1.ResourceRequest{
						{
							ResourceType: "test.example.com/projects",
							Amount:       1,
						},
					},
				},
			},
			expectError: true,
			description: "Template with invalid CEL syntax should fail",
		},
		{
			name: "template with invalid variable",
			template: quotav1alpha1.ResourceClaimTemplate{
				Metadata: quotav1alpha1.ObjectMetaTemplate{
					Name: "{{invalid + '.variable'}}",
				},
				Spec: quotav1alpha1.ResourceClaimSpec{
					ConsumerRef: quotav1alpha1.ConsumerRef{
						APIGroup: "resourcemanager.miloapis.com",
						Kind:     "Organization",
						Name:     "test-org",
					},
					Requests: []quotav1alpha1.ResourceRequest{
						{
							ResourceType: "test.example.com/projects",
							Amount:       1,
						},
					},
				},
			},
			expectError: true,
			description: "Template with invalid variable should fail",
		},
		{
			name: "template with CEL expressions using trigger variable",
			template: quotav1alpha1.ResourceClaimTemplate{
				Metadata: quotav1alpha1.ObjectMetaTemplate{
					Name:      "{{trigger.metadata.name + '-' + user.name}}",
					Namespace: "{{trigger.metadata.namespace}}",
					Annotations: map[string]string{
						"created-for":   "{{trigger.metadata.name}}",
						"trigger-kind":  "{{trigger.kind}}",
						"created-by":    "{{user.name}}",
						"request-verb":  "{{requestInfo.verb}}",
						"combined-info": "{{user.name + ':' + requestInfo.verb + ':' + trigger.metadata.name}}",
					},
				},
				Spec: quotav1alpha1.ResourceClaimSpec{
					ConsumerRef: quotav1alpha1.ConsumerRef{
						APIGroup: "resourcemanager.miloapis.com",
						Kind:     "Organization",
						Name:     "test-org",
					},
					Requests: []quotav1alpha1.ResourceRequest{
						{
							ResourceType: "test.example.com/projects",
							Amount:       1,
						},
					},
				},
			},
			expectError: false,
			description: "Template with CEL expressions using trigger variable should pass",
		},
		{
			name: "template with mixed literal and CEL expressions",
			template: quotav1alpha1.ResourceClaimTemplate{
				Metadata: quotav1alpha1.ObjectMetaTemplate{
					Name:      "{{trigger.metadata.name}}-claim",
					Namespace: "{{trigger.metadata.namespace}}",
					Annotations: map[string]string{
						"description": "Claim for {{trigger.metadata.name}} in production environment",
						"source":      "policy-{{user.name}}",
						"verb":        "action-{{requestInfo.verb}}",
					},
				},
				Spec: quotav1alpha1.ResourceClaimSpec{
					ConsumerRef: quotav1alpha1.ConsumerRef{
						APIGroup: "resourcemanager.miloapis.com",
						Kind:     "Organization",
						Name:     "test-org",
					},
					Requests: []quotav1alpha1.ResourceRequest{
						{
							ResourceType: "test.example.com/projects",
							Amount:       1,
						},
					},
				},
			},
			expectError: false,
			description: "Template with mixed literal and CEL expressions should pass",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validateClaimTemplate(tt.template)
			if tt.expectError && len(errs) == 0 {
				t.Errorf("Expected error for template, but got none. %s", tt.description)
			}
			if !tt.expectError && len(errs) > 0 {
				t.Errorf("Unexpected error for template: %v. %s", errs, tt.description)
			}
		})
	}
}
