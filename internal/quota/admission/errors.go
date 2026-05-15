package admission

import (
	"errors"
	"fmt"
)

// errAlreadyGranted is a sentinel returned by createResourceClaim when an
// existing ResourceClaim with the predetermined name already has a Granted
// condition set to True. Its capacity was reserved on a prior admission pass
// and still applies to this resource, so admission can allow the request
// without registering a watch waiter or issuing a new Create. Callers
// compare against this value with == (the sentinel is never wrapped).
var errAlreadyGranted = errors.New("ResourceClaim already granted")

// claimFailureKind classifies why a ResourceClaim could not be resolved to a
// Granted outcome during admission. It lets the admission plugin produce a
// distinct, actionable user-facing message for each root cause rather than
// collapsing every failure into "Insufficient quota".
type claimFailureKind int

const (
	// claimFailureDenied indicates the AllowanceBucketController rejected the
	// claim because there was not enough available capacity. This is the only
	// case where "Insufficient quota" is accurate.
	claimFailureDenied claimFailureKind = iota

	// claimFailureTimeout indicates the claim was created but did not reach a
	// final Granted condition within the admission plugin's watch timeout.
	// Typical causes: bucket controller still reconciling, grants still
	// propagating, or a slow control plane.
	claimFailureTimeout

	// claimFailureConflict indicates a ResourceClaim with the deterministic
	// name already exists in a non-usable state (e.g., a previously denied
	// claim that has not yet been garbage collected). The caller should retry
	// after the GC controller deletes the stale claim.
	claimFailureConflict

	// claimFailureInternal covers template render failures, watch manager
	// startup failures, dynamic client errors, and anything else that is not
	// attributable to quota capacity.
	claimFailureInternal
)

// claimFailure is the structured error returned from the claim creation/wait
// pipeline. It carries a classification so the admission plugin can surface a
// tailored message to the caller while still preserving the underlying error
// for logs and traces.
type claimFailure struct {
	kind    claimFailureKind
	reason  string // machine-readable reason (e.g. condition reason)
	message string // user-facing message fragment (e.g. denial message)
	cause   error  // wrapped error for logging/tracing
}

func (e *claimFailure) Error() string {
	switch {
	case e.message != "" && e.cause != nil:
		return fmt.Sprintf("%s: %v", e.message, e.cause)
	case e.message != "":
		return e.message
	case e.cause != nil:
		return e.cause.Error()
	default:
		return fmt.Sprintf("claim failure (kind=%d)", e.kind)
	}
}

func (e *claimFailure) Unwrap() error { return e.cause }

// asClaimFailure unwraps a claimFailure from err if present. It returns the
// claimFailure and true, or (nil, false) if err does not carry one.
func asClaimFailure(err error) (*claimFailure, bool) {
	var f *claimFailure
	if errors.As(err, &f) {
		return f, true
	}
	return nil, false
}

// newDeniedFailure constructs a claimFailure for an actual quota denial.
func newDeniedFailure(reason, message string) *claimFailure {
	return &claimFailure{kind: claimFailureDenied, reason: reason, message: message}
}

// newTimeoutFailure constructs a claimFailure for an admission watch timeout.
func newTimeoutFailure(cause error) *claimFailure {
	return &claimFailure{kind: claimFailureTimeout, cause: cause}
}

// newConflictFailure constructs a claimFailure for a pre-existing claim that
// blocks retry (typically a denied claim awaiting GC).
func newConflictFailure(message string, cause error) *claimFailure {
	return &claimFailure{kind: claimFailureConflict, message: message, cause: cause}
}

// newInternalFailure constructs a claimFailure for infrastructure errors
// (template render, watch manager, dynamic client, etc).
func newInternalFailure(cause error) *claimFailure {
	return &claimFailure{kind: claimFailureInternal, cause: cause}
}
