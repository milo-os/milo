package validation

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/client-go/dynamic"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
)

// ResourceClaimValidator provides validation capabilities for ResourceClaim objects.
type ResourceClaimValidator interface {
	// Validate performs complete validation of a ResourceClaim including field validation and claiming permissions.
	Validate(ctx context.Context, claim *quotav1alpha1.ResourceClaim) field.ErrorList
}

// resourceClaimValidator implements ResourceClaimValidator using dynamic client for resource access
// and ResourceTypeValidator for fast cached resource type validation.
type resourceClaimValidator struct {
	dynamicClient         dynamic.Interface
	resourceTypeValidator ResourceTypeValidator
}

// NewResourceClaimValidator creates a new ResourceClaim validator with dynamic client support and
// a required ResourceTypeValidator for optimized resource type validation.
func NewResourceClaimValidator(dynamicClient dynamic.Interface, resourceTypeValidator ResourceTypeValidator) ResourceClaimValidator {
	return &resourceClaimValidator{
		dynamicClient:         dynamicClient,
		resourceTypeValidator: resourceTypeValidator,
	}
}

// Validate performs complete validation of a ResourceClaim including field validation and claiming rules.
func (v *resourceClaimValidator) Validate(ctx context.Context, claim *quotav1alpha1.ResourceClaim) field.ErrorList {
	var errs field.ErrorList
	resourceRefPath := field.NewPath("spec", "resourceRef")

	if requestErrs := v.validateResourceRequests(ctx, claim); len(requestErrs) > 0 {
		errs = append(errs, requestErrs...)
	}

	if claim.Spec.ResourceRef == nil || claim.Spec.ResourceRef.Kind == "" {
		errs = append(errs, field.Required(resourceRefPath.Child("kind"), "resourceRef.kind is required"))
	}
	if claim.Spec.ResourceRef == nil || claim.Spec.ResourceRef.Name == "" {
		errs = append(errs, field.Required(resourceRefPath.Child("name"), "resourceRef.name is required"))
	}

	return errs
}

// validateResourceRequests validates all resource requests including field validation,
// duplicates, resource type registration, and claiming rules (when resourceRef is complete).
func (v *resourceClaimValidator) validateResourceRequests(ctx context.Context, claim *quotav1alpha1.ResourceClaim) field.ErrorList {
	var errs field.ErrorList
	requestsPath := field.NewPath("spec", "requests")
	seenResourceTypes := make(map[string]int)
	resourceRefComplete := claim.Spec.ResourceRef != nil && claim.Spec.ResourceRef.Kind != "" && claim.Spec.ResourceRef.Name != ""

	for i, request := range claim.Spec.Requests {
		requestPath := requestsPath.Index(i)

		if request.ResourceType == "" {
			errs = append(errs, field.Required(requestPath.Child("resourceType"), "resource type is required"))
			continue
		}

		if firstIndex, exists := seenResourceTypes[request.ResourceType]; exists {
			errs = append(errs, field.Duplicate(
				requestPath.Child("resourceType"),
				fmt.Sprintf("resource type '%s' is already specified in request %d", request.ResourceType, firstIndex)),
			)
		} else {
			seenResourceTypes[request.ResourceType] = i
		}

		if err := v.resourceTypeValidator.ValidateResourceType(ctx, request.ResourceType); err != nil {
			errs = append(errs, field.Invalid(
				requestPath.Child("resourceType"),
				request.ResourceType,
				err.Error(),
			))
		}

		if request.Amount <= 0 {
			errs = append(errs, field.Invalid(requestPath.Child("amount"), request.Amount, "amount must be greater than 0"))
		}

		if resourceRefComplete {
			if claimingErr := v.validateClaimingRulesForRequest(ctx, claim, request, requestPath); claimingErr != nil {
				errs = append(errs, claimingErr)
			}
		}
	}

	return errs
}

// validateClaimingRulesForRequest validates that the claim's resourceRef satisfies
// the claiming rules defined in the ResourceRegistration for the requested resource type.
func (v *resourceClaimValidator) validateClaimingRulesForRequest(
	ctx context.Context,
	claim *quotav1alpha1.ResourceClaim,
	request quotav1alpha1.ResourceRequest,
	requestPath *field.Path,
) *field.Error {
	allowed, allowedList, err := v.resourceTypeValidator.IsClaimingResourceAllowed(
		ctx,
		request.ResourceType,
		claim.Spec.ConsumerRef,
		claim.Spec.ResourceRef.APIGroup,
		claim.Spec.ResourceRef.Kind,
	)
	if err != nil {
		return field.InternalError(
			requestPath.Child("resourceType"),
			fmt.Errorf("failed to check claiming rules for %s: %w", request.ResourceType, err),
		)
	}

	if !allowed {
		claimingResourceStr := claim.Spec.ResourceRef.Kind // ResourceRef is non-nil; checked by resourceRefComplete guard
		if claim.Spec.ResourceRef.APIGroup != "" {
			claimingResourceStr = fmt.Sprintf("%s/%s", claim.Spec.ResourceRef.APIGroup, claim.Spec.ResourceRef.Kind)
		}

		var message string
		if len(allowedList) == 0 {
			message = fmt.Sprintf("resource type %s does not satisfy claiming rules for %s. No claimingResources configured in ResourceRegistration",
				claimingResourceStr, request.ResourceType)
		} else {
			message = fmt.Sprintf("resource type %s does not satisfy claiming rules for %s. Allowed claiming resources: [%s]",
				claimingResourceStr, request.ResourceType, strings.Join(allowedList, ", "))
		}

		return field.Forbidden(
			requestPath.Child("resourceType"),
			message,
		)
	}

	return nil
}
