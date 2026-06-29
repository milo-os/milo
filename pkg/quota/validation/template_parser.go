package validation

import (
	"fmt"
	"regexp"
	"strings"

	"go.miloapis.com/milo/pkg/quota/templateutil"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

var variableRegexp = regexp.MustCompile(`\b(trigger|user|requestInfo)\b`)

// extractVariablesFromCEL extracts variable references from a CEL expression.
// e.g., trigger.metadata.name -> "trigger"
func extractVariablesFromCEL(expression string) []string {
	matches := variableRegexp.FindAllString(expression, -1)

	var variables []string
	seen := make(map[string]bool)
	for _, match := range matches {
		if !seen[match] {
			variables = append(variables, match)
			seen[match] = true
		}
	}

	return variables
}

// validateCELTemplate validates a string that contains CEL expressions with the given allowed variables.
func validateCELTemplate(templateStr string, allowedVariables []string, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList

	if templateStr == "" {
		return allErrs
	}

	segments, err := templateutil.Split(templateStr)
	if err != nil {
		allErrs = append(allErrs, field.Invalid(fldPath, templateStr, err.Error()))
		return allErrs
	}

	celValidator, err := NewCELValidator()
	if err != nil {
		allErrs = append(allErrs, field.InternalError(fldPath, fmt.Errorf("failed to create CEL validator: %w", err)))
		return allErrs
	}

	exprIndex := 0
	for _, segment := range segments {
		if !segment.Expression {
			continue
		}
		if errs := validateCELExpression(segment.Value, allowedVariables, celValidator, fldPath.Index(exprIndex)); len(errs) > 0 {
			allErrs = append(allErrs, errs...)
		}
		exprIndex++
	}

	return allErrs
}

// validateCELExpression validates a single CEL expression with the given allowed variables.
func validateCELExpression(expression string, allowedVariables []string, celValidator *CELValidator, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList

	if err := celValidator.ValidateTemplateExpression(expression); err != nil {
		allErrs = append(allErrs, field.Invalid(fldPath, expression, fmt.Sprintf("CEL syntax validation failed: %v", err)))
	}

	variables := extractVariablesFromCEL(expression)
	allowedSet := make(map[string]bool)
	for _, v := range allowedVariables {
		allowedSet[v] = true
	}

	for _, variable := range variables {
		if !allowedSet[variable] {
			allErrs = append(allErrs, field.Invalid(fldPath, variable, fmt.Sprintf("invalid template variable '%s', valid variables are: %v", variable, allowedVariables)))
		}
	}

	return allErrs
}

// validateTemplateOrKubernetesName validates a string that either contains CEL expressions OR is a literal Kubernetes name.
func validateTemplateOrKubernetesName(str string, allowedVariables []string, allowEmpty bool, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList

	if strings.TrimSpace(str) == "" {
		if !allowEmpty {
			allErrs = append(allErrs, field.Required(fldPath, "value cannot be empty"))
		}
		return allErrs
	}

	hasExpression, err := templateutil.ContainsExpression(str)
	if err != nil {
		allErrs = append(allErrs, field.Invalid(fldPath, str, err.Error()))
		return allErrs
	}

	if hasExpression {
		allErrs = append(allErrs, validateCELTemplate(str, allowedVariables, fldPath)...)
		return allErrs
	}

	if errs := validation.IsDNS1123Subdomain(str); len(errs) > 0 {
		allErrs = append(allErrs, field.Invalid(fldPath, str, fmt.Sprintf("invalid Kubernetes name: %s", strings.Join(errs, "; "))))
	}

	return allErrs
}

// validateTemplateOrGenerateName validates values intended for metadata.generateName.
// Literal generateName values must end with '-' and have a DNS-compliant prefix.
func validateTemplateOrGenerateName(str string, allowedVariables []string, allowEmpty bool, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList

	if strings.TrimSpace(str) == "" {
		if !allowEmpty {
			allErrs = append(allErrs, field.Required(fldPath, "value cannot be empty"))
		}
		return allErrs
	}

	hasExpression, err := templateutil.ContainsExpression(str)
	if err != nil {
		allErrs = append(allErrs, field.Invalid(fldPath, str, err.Error()))
		return allErrs
	}

	if hasExpression {
		allErrs = append(allErrs, validateCELTemplate(str, allowedVariables, fldPath)...)
		return allErrs
	}

	if !strings.HasSuffix(str, "-") {
		allErrs = append(allErrs, field.Invalid(fldPath, str, "generateName must end with '-'"))
		return allErrs
	}

	prefix := strings.TrimSpace(str[:len(str)-1])
	if prefix == "" {
		allErrs = append(allErrs, field.Invalid(fldPath, str, "generateName prefix cannot be empty"))
		return allErrs
	}

	if errs := validation.IsDNS1123Subdomain(prefix); len(errs) > 0 {
		allErrs = append(allErrs, field.Invalid(fldPath, str, fmt.Sprintf("invalid Kubernetes generateName prefix: %s", strings.Join(errs, "; "))))
	}

	return allErrs
}

// validateTemplateOrLiteral validates strings that allow arbitrary literal values when not templated.
func validateTemplateOrLiteral(str string, allowedVariables []string, allowEmpty bool, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList

	if strings.TrimSpace(str) == "" {
		if !allowEmpty {
			allErrs = append(allErrs, field.Required(fldPath, "value cannot be empty"))
		}
		return allErrs
	}

	hasExpression, err := templateutil.ContainsExpression(str)
	if err != nil {
		allErrs = append(allErrs, field.Invalid(fldPath, str, err.Error()))
		return allErrs
	}

	if hasExpression {
		allErrs = append(allErrs, validateCELTemplate(str, allowedVariables, fldPath)...)
	}

	return allErrs
}
