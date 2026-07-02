package validation

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
	"go.miloapis.com/milo/pkg/quota/templateutil"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// GrantTemplateValidator provides validation for CEL expressions used in GrantCreationPolicy templates.
type GrantTemplateValidator struct {
	// resourceTypeValidator validates resource types against ResourceRegistrations
	resourceTypeValidator ResourceTypeValidator
}

// grantTemplateAllowedVariables defines the allowed template variables for GrantCreationPolicy
var grantTemplateAllowedVariables = []string{"trigger"}

// NewGrantTemplateValidator creates a new grant template validator.
func NewGrantTemplateValidator(resourceTypeValidator ResourceTypeValidator) (*GrantTemplateValidator, error) {
	return &GrantTemplateValidator{
		resourceTypeValidator: resourceTypeValidator,
	}, nil
}

func (v *GrantTemplateValidator) ValidateGrantTemplate(ctx context.Context, grantTemplate quotav1alpha1.ResourceGrantTemplate, opts ValidationOptions) field.ErrorList {
	var allErrs field.ErrorList

	if errs := v.ValidateMetadataTemplate(grantTemplate.Metadata, field.NewPath("metadata")); len(errs) > 0 {
		allErrs = append(allErrs, errs...)
	}

	if errs := v.ValidateSpecTemplate(ctx, grantTemplate.Spec, field.NewPath("spec"), opts); len(errs) > 0 {
		allErrs = append(allErrs, errs...)
	}

	return allErrs
}

func (v *GrantTemplateValidator) ValidateMetadataTemplate(metadata quotav1alpha1.ObjectMetaTemplate, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList

	if errs := v.ValidateNameTemplate(metadata.Name, fldPath.Child("name")); len(errs) > 0 {
		allErrs = append(allErrs, errs...)
	}

	// Validate namespace if provided
	if metadata.Namespace != "" {
		// If namespace contains template variables, validate as template, otherwise validate as Kubernetes name
		if errs := validateTemplateOrKubernetesName(metadata.Namespace, grantTemplateAllowedVariables, false, fldPath.Child("namespace")); len(errs) > 0 {
			allErrs = append(allErrs, errs...)
		}
	}

	// No template variables allowed in keys or values for labels
	for key, value := range metadata.Labels {
		if errs := v.validateLabelKey(key, fldPath.Child("labels").Key(key)); len(errs) > 0 {
			allErrs = append(allErrs, errs...)
		}
		if errs := v.validateLabelValue(value, fldPath.Child("labels").Key(key)); len(errs) > 0 {
			allErrs = append(allErrs, errs...)
		}
	}

	// Keys must be valid, values can contain template variables
	for key, value := range metadata.Annotations {
		if errs := v.validateAnnotationKey(key, fldPath.Child("annotations").Key(key)); len(errs) > 0 {
			allErrs = append(allErrs, errs...)
		}
		if errs := validateTemplateOrLiteral(value, grantTemplateAllowedVariables, true, fldPath.Child("annotations").Key(key)); len(errs) > 0 {
			allErrs = append(allErrs, errs...)
		}
	}

	return allErrs
}

func (v *GrantTemplateValidator) ValidateSpecTemplate(ctx context.Context, spec quotav1alpha1.ResourceGrantSpec, fldPath *field.Path, opts ValidationOptions) field.ErrorList {
	var allErrs field.ErrorList

	if errs := v.ValidateConsumerRefTemplate(spec.ConsumerRef, fldPath.Child("consumerRef")); len(errs) > 0 {
		allErrs = append(allErrs, errs...)
	}

	// Skip resource type validation when configured because it queries API server state
	if !opts.SkipAPIStateValidation {
		for i, allowance := range spec.Allowances {
			ifldPath := fldPath.Child("allowances").Index(i)

			if err := v.resourceTypeValidator.ValidateResourceType(ctx, allowance.ResourceType); err != nil {
				allErrs = append(allErrs, field.Invalid(ifldPath.Child("resourceType"), allowance.ResourceType, fmt.Sprintf("resource type validation failed: %v", err)))
			}
		}
	}

	return allErrs
}

func (v *GrantTemplateValidator) ValidateConsumerRefTemplate(consumerRef quotav1alpha1.ConsumerRef, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList

	if consumerRef.APIGroup != "" {
		hasExpr, err := templateutil.ContainsExpression(consumerRef.APIGroup)
		if err != nil {
			allErrs = append(allErrs, field.Invalid(fldPath.Child("apiGroup"), consumerRef.APIGroup, err.Error()))
		} else if hasExpr {
			if errs := v.ValidateTemplate(consumerRef.APIGroup, fldPath.Child("apiGroup")); len(errs) > 0 {
				allErrs = append(allErrs, errs...)
			}
		} else {
			if errs := v.validateAPIGroup(consumerRef.APIGroup, fldPath.Child("apiGroup")); len(errs) > 0 {
				allErrs = append(allErrs, errs...)
			}
		}
	}

	hasExpr, err := templateutil.ContainsExpression(consumerRef.Kind)
	if err != nil {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("kind"), consumerRef.Kind, err.Error()))
	} else if hasExpr {
		if errs := v.ValidateTemplate(consumerRef.Kind, fldPath.Child("kind")); len(errs) > 0 {
			allErrs = append(allErrs, errs...)
		}
	} else {
		if errs := v.validateKind(consumerRef.Kind, fldPath.Child("kind")); len(errs) > 0 {
			allErrs = append(allErrs, errs...)
		}
	}

	// For ConsumerRef, we allow templates that start with variables since they reference existing resources
	if errs := v.ValidateConsumerRefNameTemplate(consumerRef.Name, fldPath.Child("name")); len(errs) > 0 {
		allErrs = append(allErrs, errs...)
	}

	return allErrs
}

func (v *GrantTemplateValidator) ValidateNameTemplate(nameTemplate string, fldPath *field.Path) field.ErrorList {
	return validateTemplateOrKubernetesName(nameTemplate, grantTemplateAllowedVariables, false, fldPath)
}

// ValidateConsumerRefNameTemplate validates a consumer reference name template.
// This is more permissive than regular name templates since consumer refs reference existing resources.
func (v *GrantTemplateValidator) ValidateConsumerRefNameTemplate(nameTemplate string, fldPath *field.Path) field.ErrorList {
	return validateTemplateOrKubernetesName(nameTemplate, grantTemplateAllowedVariables, false, fldPath)
}

// ValidateTemplate validates a string that may contain CEL expressions in {{ }} delimiters.
func (v *GrantTemplateValidator) ValidateTemplate(templateStr string, fldPath *field.Path) field.ErrorList {
	return validateCELTemplate(templateStr, grantTemplateAllowedVariables, fldPath)
}

// validateLabelKey validates a label key using official Kubernetes validation.
func (v *GrantTemplateValidator) validateLabelKey(key string, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList

	if len(key) == 0 {
		allErrs = append(allErrs, field.Required(fldPath, "label key cannot be empty"))
	}

	// Kubernetes metadata expects labels to support the prefix/name format (e.g.,
	// "quota.miloapis.com/auto-created")
	if errs := validation.IsQualifiedName(key); len(errs) > 0 {
		allErrs = append(allErrs, field.Invalid(fldPath, key, fmt.Sprintf("label key must be a valid qualified name: %s", strings.Join(errs, "; "))))
	}

	return allErrs
}

// validateLabelValue validates a Kubernetes label value using official Kubernetes validation.
func (v *GrantTemplateValidator) validateLabelValue(value string, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList

	// Use Kubernetes official validation for label values
	if errs := validation.IsValidLabelValue(value); len(errs) > 0 {
		allErrs = append(allErrs, field.Invalid(fldPath, value, fmt.Sprintf("label value must be valid: %s", strings.Join(errs, "; "))))
	}

	return allErrs
}

// validateAnnotationKey validates a Kubernetes annotation key.
func (v *GrantTemplateValidator) validateAnnotationKey(key string, fldPath *field.Path) field.ErrorList {
	// Similar to label key validation but more permissive
	return v.validateLabelKey(key, fldPath)
}

// validateAPIGroup validates a Kubernetes API group.
func (v *GrantTemplateValidator) validateAPIGroup(apiGroup string, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList

	if len(apiGroup) == 0 {
		// Empty API group is valid (core group)
		return allErrs
	}
	if len(apiGroup) > 253 {
		allErrs = append(allErrs, field.Invalid(fldPath, apiGroup, "API group cannot be longer than 253 characters"))
	}

	// API groups are DNS names
	re := regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)
	if !re.MatchString(apiGroup) {
		allErrs = append(allErrs, field.Invalid(fldPath, apiGroup, "API group must be a valid DNS name"))
	}

	return allErrs
}

// validateKind validates a Kubernetes Kind.
func (v *GrantTemplateValidator) validateKind(kind string, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList

	if len(kind) == 0 {
		allErrs = append(allErrs, field.Required(fldPath, "kind cannot be empty"))
	}
	if len(kind) > 63 {
		allErrs = append(allErrs, field.Invalid(fldPath, kind, "kind cannot be longer than 63 characters"))
	}

	re := regexp.MustCompile(`^[A-Z][a-zA-Z0-9]*$`)
	if !re.MatchString(kind) {
		allErrs = append(allErrs, field.Invalid(fldPath, kind, "kind must start with uppercase letter and contain only alphanumeric characters"))
	}

	return allErrs
}
