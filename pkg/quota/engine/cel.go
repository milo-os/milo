package engine

import (
	"fmt"
	"sync"

	"github.com/google/cel-go/cel"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
	quotacel "go.miloapis.com/milo/pkg/quota/cel"
	"go.miloapis.com/milo/pkg/quota/validation"
)

const (
	// runtimeCostLimit sets the maximum allowed runtime cost for CEL expression
	// evaluation. This prevents malicious or poorly-written expressions from
	// consuming excessive CPU resources.
	//
	// The limit matches Kubernetes' runtime CEL cost budget which specifies the
	// actual cost limit per CEL validation call.
	//
	// Reference: https://github.com/kubernetes/apiserver/blob/v0.32.9/pkg/apis/cel/config.go#L26
	runtimeCostLimit = 1000000
)

// CELEngine provides CEL expression evaluation capabilities for quota operations.
// It combines compile-time validation with runtime evaluation and program caching.
type CELEngine interface {
	// ValidateConstraints validates CEL expressions in trigger constraints.
	ValidateConstraints(constraints []quotav1alpha1.ConditionExpression) error

	// ValidateTemplateExpression validates a CEL template expression.
	ValidateTemplateExpression(expression string) error

	// EvaluateConditions evaluates all trigger conditions against a resource object.
	EvaluateConditions(conditions []quotav1alpha1.ConditionExpression, obj *unstructured.Unstructured) (bool, error)

	// EvaluateTemplateExpression evaluates a template expression with context variables (trigger, user, requestInfo).
	EvaluateTemplateExpression(expression string, variables map[string]interface{}) (string, error)
}

// celEngine implements CELEngine with program caching for performance.
type celEngine struct {
	env          *cel.Env
	validator    *validation.CELValidator
	programCache sync.Map // map[string]cel.Program - keyed by expression
}

// NewCELEngine creates a new CEL engine with validation and evaluation capabilities.
func NewCELEngine() (CELEngine, error) {
	// Create validator for compile-time checks
	validator, err := validation.NewCELValidator()
	if err != nil {
		return nil, err
	}

	// Create CEL environment for runtime evaluation using the shared quota environment
	env, err := quotacel.NewQuotaEnvironment()
	if err != nil {
		return nil, fmt.Errorf("failed to create CEL environment: %w", err)
	}

	return &celEngine{
		env:       env,
		validator: validator,
	}, nil
}

// ValidateConstraints validates CEL expressions in trigger constraints.
func (e *celEngine) ValidateConstraints(constraints []quotav1alpha1.ConditionExpression) error {
	return e.validator.ValidateConstraints(constraints)
}

// ValidateTemplateExpression validates a CEL template expression.
func (e *celEngine) ValidateTemplateExpression(expression string) error {
	return e.validator.ValidateTemplateExpression(expression)
}

// EvaluateConditions evaluates all trigger conditions against a resource object.
// Returns true if all conditions pass, false if any fail.
func (e *celEngine) EvaluateConditions(conditions []quotav1alpha1.ConditionExpression, obj *unstructured.Unstructured) (bool, error) {
	if len(conditions) == 0 {
		return true, nil // No conditions means always match
	}

	objData := obj.Object

	for i, condition := range conditions {
		result, err := e.evaluateCondition(condition.Expression, objData)
		if err != nil {
			return false, fmt.Errorf("condition %d evaluation failed: %w", i, err)
		}

		if !result {
			return false, nil // At least one condition failed
		}
	}

	return true, nil // All conditions passed
}

// EvaluateTemplateExpression evaluates a template expression with context variables.
// Returns the string result of the expression.
func (e *celEngine) EvaluateTemplateExpression(expression string, variables map[string]interface{}) (string, error) {
	// Get or create cached program
	program, err := e.getOrCompileProgram(expression)
	if err != nil {
		return "", err
	}

	// Evaluate with provided variables
	result, details, err := program.Eval(variables)
	if err != nil {
		// Check if this was a cost limit error and include cost information in error
		if details != nil && details.ActualCost() != nil {
			actualCost := *details.ActualCost()
			return "", fmt.Errorf("evaluation failed (cost: %d, limit: %d): %w", actualCost, runtimeCostLimit, err)
		}
		return "", fmt.Errorf("evaluation failed: %w", err)
	}

	// Convert result to string
	if str, ok := result.Value().(string); ok {
		return str, nil
	}

	return "", fmt.Errorf("expression did not return a string value")
}

// evaluateCondition evaluates a single condition expression.
func (e *celEngine) evaluateCondition(expression string, objData map[string]interface{}) (bool, error) {
	// Get or create cached program
	program, err := e.getOrCompileProgram(expression)
	if err != nil {
		return false, err
	}

	// Evaluate with the resource object
	vars := map[string]interface{}{
		"trigger": objData,
	}

	result, details, err := program.Eval(vars)
	if err != nil {
		// Check if this was a cost limit error and include cost information in error
		if details != nil && details.ActualCost() != nil {
			actualCost := *details.ActualCost()
			return false, fmt.Errorf("evaluation failed (cost: %d, limit: %d): %w", actualCost, runtimeCostLimit, err)
		}
		return false, fmt.Errorf("evaluation failed: %w", err)
	}

	// Convert result to boolean
	if b, ok := result.Value().(bool); ok {
		return b, nil
	}

	return false, fmt.Errorf("expression did not return a boolean value")
}

// getOrCompileProgram retrieves a cached program or compiles and caches a new one.
func (e *celEngine) getOrCompileProgram(expression string) (cel.Program, error) {
	// Check cache first
	if cached, ok := e.programCache.Load(expression); ok {
		return cached.(cel.Program), nil
	}

	// Parse the expression
	ast, issues := e.env.Parse(expression)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("parse error: %w", issues.Err())
	}

	// Type-check the expression
	checked, issues := e.env.Check(ast)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("type check error: %w", issues.Err())
	}

	// Create program with optimizations and cost tracking enabled
	// OptTrackCost enables runtime cost tracking to prevent runaway expressions
	program, err := e.env.Program(checked,
		cel.EvalOptions(cel.OptOptimize, cel.OptTrackCost),
		cel.CostLimit(runtimeCostLimit))
	if err != nil {
		return nil, fmt.Errorf("program creation failed: %w", err)
	}

	// Cache the program
	e.programCache.Store(expression, program)

	return program, nil
}
