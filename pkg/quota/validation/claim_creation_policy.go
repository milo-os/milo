package validation

import (
	"context"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// ClaimCreationPolicyValidator validates ClaimCreationPolicy resources including
// claim template structure/syntax and resource type registration.
type ClaimCreationPolicyValidator struct {
	CELValidator          *CELValidator
	ResourceTypeValidator ResourceTypeValidator
}

// NewClaimCreationPolicyValidator creates a new ClaimCreationPolicyValidator.
func NewClaimCreationPolicyValidator(celValidator *CELValidator, resourceTypeValidator ResourceTypeValidator) *ClaimCreationPolicyValidator {
	return &ClaimCreationPolicyValidator{
		CELValidator:          celValidator,
		ResourceTypeValidator: resourceTypeValidator,
	}
}

// Validate validates a ClaimCreationPolicy.
func (v *ClaimCreationPolicyValidator) Validate(ctx context.Context, policy *quotav1alpha1.ClaimCreationPolicy, opts ValidationOptions) field.ErrorList {
	var allErrs field.ErrorList

	templatePath := field.NewPath("spec", "target", "resourceClaimTemplate")
	if errs := validateClaimTemplate(policy.Spec.Target.ResourceClaimTemplate); len(errs) > 0 {
		for _, err := range errs {
			allErrs = append(allErrs, &field.Error{
				Type:     err.Type,
				Field:    templatePath.Child(err.Field).String(),
				BadValue: err.BadValue,
				Detail:   err.Detail,
			})
		}
	}

	allErrs = append(allErrs, v.validateRequestConditions(policy)...)

	// Skip resource type validation when configured because it queries API server state
	if !opts.SkipAPIStateValidation {
		if errs := v.validateResourceTypes(ctx, policy); len(errs) > 0 {
			allErrs = append(allErrs, errs...)
		}
	}

	return allErrs
}

// validateResourceTypes validates that all resource types correspond to active ResourceRegistrations.
// Deduplicates resource types to avoid redundant validation calls.
func (v *ClaimCreationPolicyValidator) validateResourceTypes(ctx context.Context, policy *quotav1alpha1.ClaimCreationPolicy) field.ErrorList {
	var allErrs field.ErrorList
	requestsPath := field.NewPath("spec", "target", "resourceClaimTemplate", "spec", "requests")
	seen := make(map[string]bool)

	for i, requestTemplate := range policy.Spec.Target.ResourceClaimTemplate.Spec.Requests {
		resourceType := requestTemplate.ResourceType
		if !seen[resourceType] {
			seen[resourceType] = true
			if err := v.ResourceTypeValidator.ValidateResourceType(ctx, resourceType); err != nil {
				allErrs = append(allErrs, field.Invalid(
					requestsPath.Index(i).Child("resourceType"),
					resourceType,
					err.Error(),
				))
			}
		}
	}
	return allErrs
}

// validateRequestConditions compile-checks each claim template request
// condition as a boolean CEL expression.
func (v *ClaimCreationPolicyValidator) validateRequestConditions(policy *quotav1alpha1.ClaimCreationPolicy) field.ErrorList {
	var allErrs field.ErrorList
	requestsPath := field.NewPath("spec", "target", "resourceClaimTemplate", "spec", "requests")

	for i, request := range policy.Spec.Target.ResourceClaimTemplate.Spec.Requests {
		if request.Condition == "" {
			continue
		}
		if err := v.CELValidator.ValidateBoolExpression(request.Condition); err != nil {
			allErrs = append(allErrs, field.Invalid(
				requestsPath.Index(i).Child("condition"),
				request.Condition,
				err.Error(),
			))
		}
	}
	return allErrs
}
