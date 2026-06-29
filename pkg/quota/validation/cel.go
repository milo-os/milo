package validation

import (
	"fmt"
	"strings"

	"github.com/google/cel-go/cel"

	quotacel "go.miloapis.com/milo/pkg/quota/cel"
	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
)

// CELValidator provides compile-time validation for CEL expressions used in quota templates.
// It validates syntax, type safety, and security constraints but does not execute expressions.
type CELValidator struct {
	env *cel.Env
}

// NewCELValidator creates a new CEL validator with the shared quota CEL environment.
func NewCELValidator() (*CELValidator, error) {
	env, err := quotacel.NewQuotaEnvironment()
	if err != nil {
		return nil, fmt.Errorf("failed to create CEL environment: %w", err)
	}

	return &CELValidator{env: env}, nil
}

// ValidateConstraints validates CEL expressions in trigger constraints.
// These are pure CEL expressions (without {{ }} delimiters) that must return boolean values.
func (v *CELValidator) ValidateConstraints(constraints []quotav1alpha1.ConditionExpression) error {
	for i, constraint := range constraints {
		if err := v.validateExpression(constraint.Expression, cel.BoolType); err != nil {
			return fmt.Errorf("constraint %d: %w", i, err)
		}
	}
	return nil
}

// ValidateTemplateExpression validates a CEL expression extracted from {{ }} delimiters in template fields.
// The expression must return a string value or a dynamic value that will be converted to string during rendering.
func (v *CELValidator) ValidateTemplateExpression(expression string) error {
	return v.validateTemplateExpression(expression)
}

// validateTemplateExpression validates template expressions allowing both string and dynamic types.
// Template expressions are ultimately converted to strings during rendering, so we accept both.
func (v *CELValidator) validateTemplateExpression(expression string) error {
	if strings.TrimSpace(expression) == "" {
		return fmt.Errorf("expression cannot be empty")
	}

	ast, issues := v.env.Parse(expression)
	if issues != nil && issues.Err() != nil {
		return fmt.Errorf("parse error: %w", issues.Err())
	}

	checked, issues := v.env.Check(ast)
	if issues != nil && issues.Err() != nil {
		return fmt.Errorf("type check error: %w", issues.Err())
	}

	// Accept both string and dynamic types since templates convert to strings at runtime
	outputType := checked.OutputType()
	if !outputType.IsEquivalentType(cel.StringType) && !outputType.IsEquivalentType(cel.DynType) {
		return fmt.Errorf("expression must return string or dynamic type, got %s", outputType)
	}

	if err := v.validateSecurity(expression); err != nil {
		return fmt.Errorf("security validation failed: %w", err)
	}

	return nil
}

// validateExpression validates that a CEL expression is syntactically correct and returns the expected type.
func (v *CELValidator) validateExpression(expression string, expectedType *cel.Type) error {
	if strings.TrimSpace(expression) == "" {
		return fmt.Errorf("expression cannot be empty")
	}

	ast, issues := v.env.Parse(expression)
	if issues != nil && issues.Err() != nil {
		return fmt.Errorf("parse error: %w", issues.Err())
	}

	checked, issues := v.env.Check(ast)
	if issues != nil && issues.Err() != nil {
		return fmt.Errorf("type check error: %w", issues.Err())
	}

	if !checked.OutputType().IsEquivalentType(expectedType) {
		return fmt.Errorf("expression must return %s, got %s", expectedType, checked.OutputType())
	}
	if err := v.validateSecurity(expression); err != nil {
		return fmt.Errorf("security validation failed: %w", err)
	}

	return nil
}

func (v *CELValidator) validateSecurity(expression string) error {
	// Prevent potentially dangerous operations
	forbidden := []string{
		"system",
		"exec",
		"eval",
		"import",
		"file",
		"network",
		"subprocess",
	}

	lowerExpr := strings.ToLower(expression)
	for _, term := range forbidden {
		if strings.Contains(lowerExpr, term) {
			return fmt.Errorf("expression contains forbidden term: %s", term)
		}
	}

	if len(expression) > 1024 {
		return fmt.Errorf("expression exceeds maximum length of 1024 characters")
	}

	return nil
}
