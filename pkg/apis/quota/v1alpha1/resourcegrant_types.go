package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Bucket represents a single allocation of quota capacity within an allowance.
// Each bucket contributes its amount to the total allowance for a resource type.
type Bucket struct {
	// Amount specifies the quota capacity provided by this bucket.
	// Must be measured in the BaseUnit defined by the corresponding ResourceRegistration.
	// Must be a non-negative integer (0 is valid but provides no quota).
	//
	// Examples:
	// - 100 (providing 100 projects)
	// - 2048000 (providing 2048000 bytes = 2GB)
	// - 5000 (providing 5000 CPU millicores = 5 cores)
	//
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Required
	Amount int64 `json:"amount"`
}

// Allowance defines quota allocation for a specific resource type within a ResourceGrant.
// Each allowance can contain multiple buckets that sum to provide total capacity.
type Allowance struct {
	// ResourceType identifies the specific resource type receiving quota allocation.
	// Must exactly match a ResourceRegistration.spec.resourceType that is currently active.
	// The quota system validates this reference when processing the grant.
	//
	// The identifier format is flexible, as defined by platform administrators
	// in their ResourceRegistrations.
	//
	// Examples:
	// - "resourcemanager.miloapis.com/projects"
	// - "compute_cpu"
	// - "storage.volumes"
	// - "custom-service-quota"
	//
	// +kubebuilder:validation:Required
	ResourceType string `json:"resourceType"`

	// Buckets contains the quota allocations for this resource type.
	// All bucket amounts are summed to determine the total allowance.
	// Minimum 1 bucket required per allowance.
	//
	// Multiple buckets can be used for:
	// - Separating quota from different sources or tiers
	// - Managing incremental quota increases over time
	// - Tracking quota attribution for billing or reporting
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Buckets []Bucket `json:"buckets"`
}

// ResourceGrantSpec defines the desired state of ResourceGrant.
type ResourceGrantSpec struct {
	// ConsumerRef identifies the quota consumer that receives these allowances.
	// The consumer type must match the ConsumerType defined in the ResourceRegistration
	// for each allowance resource type. The system validates this relationship.
	//
	// Examples:
	// - Organization receiving Project quota allowances
	// - Project receiving User quota allowances
	// - Organization receiving storage quota allowances
	//
	// +kubebuilder:validation:Required
	ConsumerRef ConsumerRef `json:"consumerRef"`

	// Allowances specifies the quota allocations provided by this grant.
	// Each allowance grants capacity for a specific resource type.
	// Minimum 1 allowance required, maximum 20 allowances per grant.
	//
	// All allowances in a single grant:
	// - Apply to the same consumer (spec.consumerRef)
	// - Contribute to the same AllowanceBucket for each resource type
	// - Activate and deactivate together based on the grant's status
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Allowances []Allowance `json:"allowances"`
}

// ResourceGrantStatus reports the grant's operational state and processing status.
// Controllers update status conditions to indicate whether the grant is active
// and contributing capacity to AllowanceBuckets.
type ResourceGrantStatus struct {
	// ObservedGeneration indicates the most recent spec generation the quota system has processed.
	// When ObservedGeneration matches metadata.generation, the status reflects the current spec.
	// When ObservedGeneration is lower, the quota system is still processing recent changes.
	//
	// +kubebuilder:validation:Optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions represents the latest available observations of the grant's state.
	// Controllers set these conditions to communicate operational status.
	//
	// Standard condition types:
	// - "Active": Indicates whether the grant is operational and contributing to quota buckets.
	//   When True, allowances are aggregated into AllowanceBuckets and available for claims.
	//   When False, allowances do not contribute to quota decisions.
	//
	// Standard condition reasons for "Active":
	// - "GrantActive": Grant is validated and contributing to quota buckets
	// - "ValidationFailed": Specification contains errors preventing activation (see message)
	// - "GrantPending": Grant is being processed by the quota system
	//
	// Grant Lifecycle:
	// 1. Created: Active=Unknown, reason=GrantPending
	// 2. Validated: Active=True, reason=GrantActive OR Active=False, reason=ValidationFailed
	// 3. Updated: Active condition changes only when validation results change
	//
	// +kubebuilder:validation:XValidation:rule="self.all(c, c.type == 'Active' ? c.reason in ['GrantActive', 'ValidationFailed', 'GrantPending'] : true)",message="Active condition reason must be valid"
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

const (
	// Indicates that the resource grant is active and available for usage.
	ResourceGrantActive = "Active"
)

const (
	// Indicates the ResourceGrant is active and its
	// allowances will be taken into account in claim evaluation.
	ResourceGrantActiveReason = "GrantActive"
	// Indicates that the status update validation failed.
	ResourceGrantValidationFailedReason = "ValidationFailed"
	// Indicates that the grant is pending activation.
	ResourceGrantPendingReason = "GrantPending"
)

// ResourceGrant allocates quota capacity to a consumer for specific resource types.
// Grants provide the allowances that AllowanceBuckets aggregate to determine
// available quota for ResourceClaim evaluation.
//
// ### How It Works
// **ResourceGrants** begin their lifecycle when either an administrator creates them manually or a
// **GrantCreationPolicy** generates them automatically in response to observed resource changes. Upon
// creation, the grant enters a validation phase where the quota system examines the consumer type
// to ensure it matches the expected `ConsumerType` from each **ResourceRegistration** targeted by
// the grant's allowances. The quota system also verifies that all specified resource types correspond
// to active registrations and that the allowance amounts are valid non-negative integers.
//
// When validation succeeds, the quota system marks the grant as `Active`, signaling to **AllowanceBucket**
// resources that this grant should contribute to quota calculations. The bucket resources
// continuously monitor for active grants and aggregate their allowance amounts into the appropriate
// buckets based on consumer and resource type matching. This aggregation process makes the granted
// quota capacity available for **ResourceClaim** consumption.
//
// **ResourceClaims** then consume the capacity that active grants provide, creating a flow from grants
// through buckets to claims. The grant's capacity remains reserved as long as claims reference it,
// ensuring that quota allocations persist until the consuming resources are removed. This creates
// a stable quota environment where capacity allocations remain consistent across resource lifecycles.
//
// ### Core Relationships
// - **Provides capacity to**: AllowanceBucket matching (spec.consumerRef, spec.allowances[].resourceType)
// - **Consumed by**: ResourceClaim objects processed against the aggregated buckets
// - **Validated against**: ResourceRegistration for each spec.allowances[].resourceType
// - **Created by**: Administrators manually or GrantCreationPolicy automatically
//
// ### Quota Aggregation Logic
// Multiple ResourceGrants for the same (consumer, resourceType) combination:
// - Aggregate into a single AllowanceBucket for that combination
// - All bucket amounts from all allowances are summed for total capacity
// - Only Active grants contribute to the aggregated limit
// - Inactive grants are excluded from quota calculations
//
// ### Grant vs Bucket Relationship
// - **ResourceGrant**: Specifies intended quota allocations
// - **AllowanceBucket**: Aggregates actual available quota from active grants
// - **ResourceClaim**: Consumes quota from buckets (which source from grants)
//
// ### Allowance Structure
// Each grant can contain multiple allowances for different resource types:
// - All allowances share the same consumer (spec.consumerRef)
// - Each allowance can have multiple buckets (for tracking, attribution, or incremental increases)
// - Bucket amounts within an allowance are summed for that resource type
//
// ### Manual vs Automated Grants
// **Manual Grants** (created by administrators):
// - Explicit quota allocations for specific consumers
// - Require direct management and updates
// - Useful for base quotas, special allocations, or testing
//
// **Automated Grants** (created by GrantCreationPolicy):
// - Generated based on resource lifecycle events
// - Include labels/annotations for tracking policy source
// - Automatically managed based on trigger conditions
//
// ### Validation Requirements
// - Consumer type must match ResourceRegistration.spec.consumerType for each resource type
// - All resource types must reference active ResourceRegistration objects
// - Maximum 20 allowances per grant
// - All amounts must be non-negative integers in BaseUnit
//
// ### Field Constraints and Limits
// - Maximum 20 allowances per grant
// - Each allowance must have at least 1 bucket
// - Bucket amounts must be non-negative (0 is allowed but provides no quota)
// - All amounts measured in BaseUnit from ResourceRegistration
//
// ### Status Information
// - **Active condition**: Indicates whether grant is contributing to quota buckets
// - **Validation errors**: Reported in condition message when Active=False
// - **Processing status**: ObservedGeneration tracks spec changes
//
// ### Selectors and Filtering
// - **Field selectors**: spec.consumerRef.kind, spec.consumerRef.name
// - **Recommended labels** (add manually for better organization):
//   - quota.miloapis.com/consumer-kind: Organization
//   - quota.miloapis.com/consumer-name: acme-corp
//   - quota.miloapis.com/source: policy-name or manual
//   - quota.miloapis.com/tier: basic, premium, enterprise
//
// ### Common Queries
// - All grants for a consumer: field selector spec.consumerRef.kind + spec.consumerRef.name
// - Grants by source policy: label selector quota.miloapis.com/source=<policy-name>
// - Grants by resource tier: label selector quota.miloapis.com/tier=<tier-name>
// - Active vs inactive grants: check status.conditions[type=Active].status
//
// ### Cross-Cluster Allocation
// GrantCreationPolicy can create grants in parent control planes for cross-cluster quota:
// - Policy running in child cluster creates grants in parent cluster
// - Grants provide capacity that spans multiple child clusters
// - Enables centralized quota management across cluster hierarchies
//
// ### Troubleshooting
// - **Inactive grants**: Check status.conditions[type=Active] for validation errors
// - **Missing quota**: Verify grants are Active and contributing to correct buckets
// - **Grant conflicts**: Multiple grants for same consumer+resourceType are aggregated, not conflicting
//
// ### Performance Considerations
// - Large numbers of grants can impact bucket aggregation performance
// - Consider consolidating grants where possible to reduce aggregation overhead
// - Grant status updates are asynchronous and may lag spec changes
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Active",type="string",JSONPath=".status.conditions[?(@.type=='Active')].status"
// +kubebuilder:printcolumn:name="Consumer Group",type="string",JSONPath=".spec.consumerRef.apiGroup",priority=1
// +kubebuilder:printcolumn:name="Consumer Type",type="string",JSONPath=".spec.consumerRef.kind",priority=1
// +kubebuilder:printcolumn:name="Consumer",type="string",JSONPath=".spec.consumerRef.name",priority=1
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +k8s:openapi-gen=true
// +kubebuilder:selectablefield:JSONPath=".spec.consumerRef.kind"
// +kubebuilder:selectablefield:JSONPath=".spec.consumerRef.name"
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Organization,Project"
type ResourceGrant struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +kubebuilder:validation:Required
	Spec   ResourceGrantSpec   `json:"spec"`
	Status ResourceGrantStatus `json:"status,omitempty"`
}

// ResourceGrantList contains a list of ResourceGrant.
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
type ResourceGrantList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ResourceGrant `json:"items"`
}
