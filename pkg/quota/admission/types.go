package admission

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apiserver/pkg/endpoints/request"
)

// ClaimResult represents the result of waiting for a ResourceClaim to be processed.
type ClaimResult struct {
	// Granted indicates whether the ResourceClaim was granted (true) or denied (false)
	Granted bool

	// Reason provides the reason for the result (e.g., "quota exceeded", "approved")
	Reason string

	// Error contains any error that occurred during processing
	Error error
}

// ClaimWatchManager provides an interface for watching ResourceClaim status changes
// and waiting for specific claim outcomes.
type ClaimWatchManager interface {
	// RegisterClaimWaiter registers a waiter for a specific ResourceClaim.
	// Returns a channel that will receive the result, a cancel function, and any error.
	RegisterClaimWaiter(ctx context.Context, claimName, namespace string, timeout time.Duration) (<-chan ClaimResult, context.CancelFunc, error)

	// UnregisterClaimWaiter unregisters a waiter for a specific ResourceClaim.
	UnregisterClaimWaiter(claimName, namespace string)

	// Start starts the watch manager's background operations.
	Start(ctx context.Context) error
}

// UserContext provides user information for template evaluation.
// This is a local copy of the engine.UserContext to avoid import cycles.
type UserContext struct {
	Name   string
	UID    string
	Groups []string
	Extra  map[string][]string
}

// EvaluationContext provides context for template evaluation in admission scenarios.
// This is a local copy of the engine.EvaluationContext to avoid import cycles.
type EvaluationContext struct {
	Object      *unstructured.Unstructured
	User        UserContext
	RequestInfo *request.RequestInfo
	Namespace   string
	GVK         struct {
		Group   string
		Version string
		Kind    string
	}
}
