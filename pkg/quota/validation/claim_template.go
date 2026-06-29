package validation

import (
	"strings"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// claimTemplateAllowedVariables defines the allowed template variables for ClaimCreationPolicy
var claimTemplateAllowedVariables = []string{"trigger", "user", "requestInfo"}

// validateClaimTemplate validates a ResourceClaimTemplate including name/generateName
// mutual exclusivity and CEL expressions in template fields.
func validateClaimTemplate(t quotav1alpha1.ResourceClaimTemplate) field.ErrorList {
	var allErrs field.ErrorList
	fldPath := field.NewPath("metadata")

	nameSet := strings.TrimSpace(t.Metadata.Name) != ""
	genSet := strings.TrimSpace(t.Metadata.GenerateName) != ""
	if nameSet && genSet {
		allErrs = append(allErrs, field.Invalid(fldPath, t.Metadata, "metadata.name and metadata.generateName are mutually exclusive"))
	}

	if t.Metadata.Name != "" {
		if errs := validateTemplateOrKubernetesName(t.Metadata.Name, claimTemplateAllowedVariables, false, fldPath.Child("name")); len(errs) > 0 {
			allErrs = append(allErrs, errs...)
		}
	}

	if t.Metadata.GenerateName != "" {
		if errs := validateTemplateOrGenerateName(t.Metadata.GenerateName, claimTemplateAllowedVariables, false, fldPath.Child("generateName")); len(errs) > 0 {
			allErrs = append(allErrs, errs...)
		}
	}

	if t.Metadata.Namespace != "" {
		if errs := validateTemplateOrKubernetesName(t.Metadata.Namespace, claimTemplateAllowedVariables, false, fldPath.Child("namespace")); len(errs) > 0 {
			allErrs = append(allErrs, errs...)
		}
	}

	for k, vStr := range t.Metadata.Annotations {
		if errs := validateTemplateOrLiteral(vStr, claimTemplateAllowedVariables, true, fldPath.Child("annotations").Key(k)); len(errs) > 0 {
			allErrs = append(allErrs, errs...)
		}
	}

	return allErrs
}
