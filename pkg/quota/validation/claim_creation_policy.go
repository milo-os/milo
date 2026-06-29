package validation

import (
	"context"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// ClaimCreationPolicyValidator validates ClaimCreationPolicy resources including
// claim template structure/syntax and resource type registration.
type ClaimCreationPolicyValidator struct {
	ResourceTypeValidator ResourceTypeValidator
}

// NewClaimCreationPolicyValidator creates a new ClaimCreationPolicyValidator.
func NewClaimCreationPolicyValidator(resourceTypeValidator ResourceTypeValidator) *ClaimCreationPolicyValidator {
	return &ClaimCreationPolicyValidator{
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
