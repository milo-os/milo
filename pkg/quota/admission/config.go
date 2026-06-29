package admission

import (
	"time"
)

// TTLConfig holds configuration for watch manager TTL-based lifecycle management
type TTLConfig struct {
	// DefaultTTL is the default time-to-live when no active waiters
	DefaultTTL time.Duration

	// MinTTL is the minimum allowed TTL to prevent thrashing
	MinTTL time.Duration

	// MaxTTL is the maximum allowed TTL
	MaxTTL time.Duration
}

// RetryConfig holds configuration for watch stream retry behavior
type RetryConfig struct {
	// InitialDelay is the initial backoff delay after a failure
	InitialDelay time.Duration

	// MaxDelay is the maximum backoff delay (cap for exponential growth)
	MaxDelay time.Duration

	// Multiplier is the backoff multiplier for exponential growth
	Multiplier float64

	// Jitter is the random jitter factor (0.0-1.0) to apply to backoff
	Jitter float64
}

// WatchManagerConfig holds configuration for the ClaimWatchManager
type WatchManagerConfig struct {
	// DefaultTimeout is the default timeout for waiting for ResourceClaim results
	DefaultTimeout time.Duration

	// MaxWaiters is the maximum number of concurrent waiters (0 = unlimited)
	MaxWaiters int

	// TTL configuration for watch manager lifecycle
	TTL TTLConfig

	// Retry configuration for watch stream failures
	Retry RetryConfig
}

// DefaultWatchManagerConfig returns the default configuration for the watch manager
func DefaultWatchManagerConfig() *WatchManagerConfig {
	return &WatchManagerConfig{
		DefaultTimeout: 30 * time.Second,
		MaxWaiters:     1000, // Reasonable default to prevent memory exhaustion
		TTL: TTLConfig{
			DefaultTTL: 5 * time.Minute,
			MinTTL:     30 * time.Second,
			MaxTTL:     30 * time.Minute,
		},
		Retry: RetryConfig{
			InitialDelay: 100 * time.Millisecond,
			MaxDelay:     30 * time.Second,
			Multiplier:   2.0,
			Jitter:       0.25,
		},
	}
}

// DefaultClusterScopedClaimNamespace is where ResourceClaims for cluster-scoped
// target resources are created. Cluster-scoped resources have no namespace to
// inherit, and ResourceClaims are namespaced, so they need a fixed home.
const DefaultClusterScopedClaimNamespace = "milo-system"

// AdmissionPluginConfig holds configuration for the ClaimCreationPlugin
type AdmissionPluginConfig struct {
	// WatchManager configuration
	WatchManager *WatchManagerConfig

	// ClusterScopedClaimNamespace is the namespace used for ResourceClaims when
	// the triggering resource is cluster-scoped and the ClaimCreationPolicy does
	// not pin spec.target.resourceClaimTemplate.metadata.namespace. Defaults to
	// DefaultClusterScopedClaimNamespace.
	ClusterScopedClaimNamespace string
}

// DefaultAdmissionPluginConfig returns the default configuration for the admission plugin
func DefaultAdmissionPluginConfig() *AdmissionPluginConfig {
	return &AdmissionPluginConfig{
		WatchManager:                DefaultWatchManagerConfig(),
		ClusterScopedClaimNamespace: DefaultClusterScopedClaimNamespace,
	}
}
