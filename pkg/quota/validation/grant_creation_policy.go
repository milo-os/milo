package validation

import (
	"context"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// GrantCreationPolicyValidator validates GrantCreationPolicy resources.
type GrantCreationPolicyValidator struct {
	CELValidator      *CELValidator
	TemplateValidator *GrantTemplateValidator
}

// NewGrantCreationPolicyValidator creates a new GrantCreationPolicyValidator.
func NewGrantCreationPolicyValidator(
	celValidator *CELValidator,
	templateValidator *GrantTemplateValidator,
) *GrantCreationPolicyValidator {
	return &GrantCreationPolicyValidator{
		CELValidator:      celValidator,
		TemplateValidator: templateValidator,
	}
}

// Validate validates a GrantCreationPolicy including CEL expressions,
// parent context configuration, and grant template structure.
func (v *GrantCreationPolicyValidator) Validate(ctx context.Context, policy *quotav1alpha1.GrantCreationPolicy, opts ValidationOptions) field.ErrorList {
	var allErrs field.ErrorList

	if err := v.CELValidator.ValidateConstraints(policy.Spec.Trigger.Constraints); err != nil {
		allErrs = append(allErrs, field.Invalid(
			field.NewPath("spec", "trigger", "constraints"),
			policy.Spec.Trigger.Constraints,
			err.Error(),
		))
	}

	if policy.Spec.Target.ParentContext != nil {
		if err := v.CELValidator.ValidateTemplateExpression(policy.Spec.Target.ParentContext.NameExpression); err != nil {
			allErrs = append(allErrs, field.Invalid(
				field.NewPath("spec", "target", "parentContext", "nameExpression"),
				policy.Spec.Target.ParentContext.NameExpression,
				err.Error(),
			))
		}
	}

	templatePath := field.NewPath("spec", "target", "resourceGrantTemplate")
	if errs := v.TemplateValidator.ValidateGrantTemplate(ctx, policy.Spec.Target.ResourceGrantTemplate, opts); len(errs) > 0 {
		for _, err := range errs {
			allErrs = append(allErrs, &field.Error{
				Type:     err.Type,
				Field:    templatePath.Child(err.Field).String(),
				BadValue: err.BadValue,
				Detail:   err.Detail,
			})
		}
	}

	return allErrs
}
