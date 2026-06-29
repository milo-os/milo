package validation

import (
	"fmt"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// ResourceRegistrationValidator validates ResourceRegistration resources.
type ResourceRegistrationValidator struct {
	resourceTypeValidator ResourceTypeValidator
}

// NewResourceRegistrationValidator creates a new ResourceRegistrationValidator.
func NewResourceRegistrationValidator(resourceTypeValidator ResourceTypeValidator) *ResourceRegistrationValidator {
	return &ResourceRegistrationValidator{
		resourceTypeValidator: resourceTypeValidator,
	}
}

// Validate performs complete validation of a ResourceRegistration.
// This includes both self-contained validation (duplicate claimingResources)
// and cluster-wide validation (resourceType uniqueness).
func (v *ResourceRegistrationValidator) Validate(registration *quotav1alpha1.ResourceRegistration) field.ErrorList {
	var allErrs field.ErrorList

	if errs := v.validateClaimingResourcesDuplicates(registration); len(errs) > 0 {
		allErrs = append(allErrs, errs...)
	}

	if errs := v.validateResourceTypeUniqueness(registration); len(errs) > 0 {
		allErrs = append(allErrs, errs...)
	}

	return allErrs
}

// validateClaimingResourcesDuplicates checks for duplicate entries in the claimingResources array.
// Moved from CEL validation due to cost limits with nested loops.
func (v *ResourceRegistrationValidator) validateClaimingResourcesDuplicates(registration *quotav1alpha1.ResourceRegistration) field.ErrorList {
	var allErrs field.ErrorList

	if len(registration.Spec.ClaimingResources) <= 1 {
		return nil
	}

	claimingResourcesPath := field.NewPath("spec", "claimingResources")
	seen := make(map[string]int)

	for i, cr := range registration.Spec.ClaimingResources {
		key := fmt.Sprintf("%s/%s", cr.APIGroup, cr.Kind)
		if firstIndex, exists := seen[key]; exists {
			allErrs = append(allErrs, field.Duplicate(
				claimingResourcesPath.Index(i),
				fmt.Sprintf("duplicate claiming resource '%s' (first occurrence at index %d)", key, firstIndex),
			))
		}
		seen[key] = i
	}

	return allErrs
}

// validateResourceTypeUniqueness checks that the resourceType is not already registered.
func (v *ResourceRegistrationValidator) validateResourceTypeUniqueness(registration *quotav1alpha1.ResourceRegistration) field.ErrorList {
	var allErrs field.ErrorList

	if v.resourceTypeValidator.IsResourceTypeRegistered(registration.Spec.ResourceType) {
		allErrs = append(allErrs, field.Duplicate(
			field.NewPath("spec", "resourceType"),
			fmt.Sprintf("resource type '%s' is already registered", registration.Spec.ResourceType),
		))
	}

	return allErrs
}
