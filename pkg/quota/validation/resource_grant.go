package validation

import (
	"context"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// ResourceGrantValidator validates ResourceGrant resources.
type ResourceGrantValidator struct {
	ResourceTypeValidator ResourceTypeValidator
}

// NewResourceGrantValidator creates a new ResourceGrantValidator.
func NewResourceGrantValidator(resourceTypeValidator ResourceTypeValidator) *ResourceGrantValidator {
	return &ResourceGrantValidator{
		ResourceTypeValidator: resourceTypeValidator,
	}
}

// Validate validates that all resource types in the grant's allowances correspond
// to active ResourceRegistrations. This method deduplicates resource types to avoid
// redundant validation calls.
func (v *ResourceGrantValidator) Validate(ctx context.Context, grant *quotav1alpha1.ResourceGrant, opts ValidationOptions) field.ErrorList {
	var allErrs field.ErrorList
	allowancesPath := field.NewPath("spec", "allowances")
	seen := make(map[string]bool)

	// Skip resource type validation when configured because it queries API server state
	if !opts.SkipAPIStateValidation {
		for i, allowance := range grant.Spec.Allowances {
			resourceType := allowance.ResourceType
			if !seen[resourceType] {
				seen[resourceType] = true
				if err := v.ResourceTypeValidator.ValidateResourceType(ctx, resourceType); err != nil {
					allErrs = append(allErrs, field.Invalid(
						allowancesPath.Index(i).Child("resourceType"),
						resourceType,
						err.Error(),
					))
				}
			}
		}
	}

	return allErrs
}
