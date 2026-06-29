// Package cel provides shared CEL infrastructure for the quota system.
// It defines the CEL environment configuration used by both validation and runtime evaluation.
package cel

import (
	"strings"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
)

// NewQuotaEnvironment creates a CEL environment with quota system variables and functions.
// This environment is shared between validation (compile-time checks) and engine (runtime evaluation)
// to ensure expressions validated at policy creation time work correctly during execution.
//
// Variables:
//   - trigger (dyn): The Kubernetes resource object that triggered the policy evaluation
//   - user (dyn): The user context for the request (ClaimCreationPolicy only)
//   - requestInfo (dyn): The admission request context (ClaimCreationPolicy only)
//
// Functions:
//   - has(obj, field): Check if a nested field exists using dot notation (e.g., has(trigger, "spec.tier"))
func NewQuotaEnvironment() (*cel.Env, error) {
	return cel.NewEnv(
		// Add variables as dynamic types for maximum flexibility
		// Template validation accepts both string and dynamic types
		cel.Variable("trigger", cel.DynType),
		cel.Variable("user", cel.DynType),
		cel.Variable("requestInfo", cel.DynType),

		// Add custom functions for Kubernetes operations
		cel.Function("has",
			cel.MemberOverload("has_field", []*cel.Type{cel.DynType, cel.StringType}, cel.BoolType,
				cel.BinaryBinding(func(obj, field ref.Val) ref.Val {
					if objMap, ok := obj.Value().(map[string]interface{}); ok {
						fieldStr := field.Value().(string)
						_, exists := GetNestedField(objMap, fieldStr)
						return types.Bool(exists)
					}
					return types.False
				}),
			),
		),
	)
}

// GetNestedField retrieves a nested field from a map using dot notation.
// For example, GetNestedField(obj, "metadata.name") retrieves obj["metadata"]["name"].
//
// This is a helper function exported for use by the custom CEL 'has' function.
func GetNestedField(obj map[string]interface{}, fieldPath string) (interface{}, bool) {
	parts := strings.Split(fieldPath, ".")
	current := obj

	for i, part := range parts {
		if current == nil {
			return nil, false
		}

		if i == len(parts)-1 {
			// Last part - return the value
			value, exists := current[part]
			return value, exists
		}

		// Intermediate part - must be a map
		if next, ok := current[part].(map[string]interface{}); ok {
			current = next
		} else {
			return nil, false
		}
	}

	return current, true
}
