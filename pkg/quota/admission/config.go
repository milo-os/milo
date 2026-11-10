package admission

import (
	"time"

	"go.miloapis.com/milo/pkg/quota"
)

// DefaultClusterScopedClaimNamespace is where ResourceClaims for cluster-scoped
// target resources are created. Cluster-scoped resources have no namespace to
// inherit, and ResourceClaims are namespaced, so they need a fixed home.
const DefaultClusterScopedClaimNamespace = "milo-system"

// AdmissionPluginConfig holds configuration for the ClaimCreationPlugin
type AdmissionPluginConfig struct {
	// ClaimWaitTimeout is how long an admission request waits for the outcome
	// of the ResourceClaim it creates.
	ClaimWaitTimeout time.Duration

	// ClusterScopedClaimNamespace is the namespace used for ResourceClaims when
	// the triggering resource is cluster-scoped and the ClaimCreationPolicy does
	// not pin spec.target.resourceClaimTemplate.metadata.namespace. Defaults to
	// DefaultClusterScopedClaimNamespace.
	ClusterScopedClaimNamespace string
}

// DefaultAdmissionPluginConfig returns the default configuration for the admission plugin
func DefaultAdmissionPluginConfig() *AdmissionPluginConfig {
	return &AdmissionPluginConfig{
		ClaimWaitTimeout:            quota.DefaultClaimWaitTimeout,
		ClusterScopedClaimNamespace: DefaultClusterScopedClaimNamespace,
	}
}
