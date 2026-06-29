package validation

// ValidationOptions configures validation behavior.
type ValidationOptions struct {
	// SkipAPIStateValidation, when true, skips validations that query API server state.
	//
	// When SkipAPIStateValidation is true, validators perform:
	// - Syntax validation (CEL expressions, template syntax)
	// - Structural validation (required fields, mutual exclusivity)
	// - Static schema validation
	//
	// When SkipAPIStateValidation is true, validators skip:
	// - Resource type existence checks against ResourceRegistrations
	// - Cross-resource reference validation
	// - Any validation requiring API server queries
	//
	// Admission webhooks always skip API state validation because they validate
	// incoming requests without querying API state. Controllers perform full
	// validation including API state checks.
	SkipAPIStateValidation bool
}

// AdmissionValidationOptions returns options for admission webhook validation.
// Admission webhooks validate request syntax and structure without querying API state.
func AdmissionValidationOptions() ValidationOptions {
	return ValidationOptions{
		SkipAPIStateValidation: true,
	}
}

// ControllerValidationOptions returns options for controller validation.
// Controllers perform full validation including API state checks.
func ControllerValidationOptions() ValidationOptions {
	return ValidationOptions{
		SkipAPIStateValidation: false,
	}
}
