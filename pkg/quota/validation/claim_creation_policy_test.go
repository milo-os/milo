package validation

import (
	"context"
	"strings"
	"testing"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
)

func TestClaimCreationPolicyValidatorRequestConditions(t *testing.T) {
	celValidator, err := NewCELValidator()
	if err != nil {
		t.Fatalf("NewCELValidator() error = %v", err)
	}
	validator := NewClaimCreationPolicyValidator(celValidator, nil)

	tests := []struct {
		name       string
		condition  string
		wantErr    bool
		wantDetail string
	}{
		{
			name:      "empty condition is accepted",
			condition: "",
		},
		{
			name:      "boolean condition on the trigger is accepted",
			condition: "trigger.spec.ipFamily == 'IPv4' && trigger.spec.addressScope == 'Public'",
		},
		{
			name:      "boolean condition on the user is accepted",
			condition: "user.groups.exists(g, g == 'admin')",
		},
		{
			name:       "invalid CEL is rejected",
			condition:  "trigger.spec.ipFamily ==",
			wantErr:    true,
			wantDetail: "parse error",
		},
		{
			name:       "undeclared variable is rejected",
			condition:  "object.spec.ipFamily == 'IPv4'",
			wantErr:    true,
			wantDetail: "type check error",
		},
		{
			name:       "non-boolean output is rejected",
			condition:  "'IPv4'",
			wantErr:    true,
			wantDetail: "must return bool",
		},
		{
			name:       "dynamic output is rejected",
			condition:  "trigger.spec.ipFamily",
			wantErr:    true,
			wantDetail: "must return bool",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := &quotav1alpha1.ClaimCreationPolicy{
				Spec: quotav1alpha1.ClaimCreationPolicySpec{
					Target: quotav1alpha1.ClaimTargetSpec{
						ResourceClaimTemplate: quotav1alpha1.ResourceClaimTemplate{
							Spec: quotav1alpha1.ResourceClaimTemplateSpec{
								Requests: []quotav1alpha1.ResourceRequestTemplate{
									{ResourceType: "ipam.miloapis.com/ipclaims", Amount: 1},
									{ResourceType: "ipam.miloapis.com/public-ipv4claims", Amount: 1, Condition: tt.condition},
								},
							},
						},
					},
				},
			}

			errs := validator.Validate(context.Background(), policy, AdmissionValidationOptions())
			if !tt.wantErr {
				if len(errs) != 0 {
					t.Fatalf("Validate() errors = %v, want none", errs)
				}
				return
			}

			if len(errs) != 1 {
				t.Fatalf("Validate() errors = %v, want exactly one", errs)
			}
			if got, want := errs[0].Field, "spec.target.resourceClaimTemplate.spec.requests[1].condition"; got != want {
				t.Errorf("Validate() field = %q, want %q", got, want)
			}
			if !strings.Contains(errs[0].Detail, tt.wantDetail) {
				t.Errorf("Validate() detail = %q, want it to contain %q", errs[0].Detail, tt.wantDetail)
			}
		})
	}
}
