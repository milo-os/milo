package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ContributingGrantRef tracks a ResourceGrant that contributes capacity to this bucket.
// The quota system maintains these references to provide visibility into quota sources
// and to detect when grants change.
type ContributingGrantRef struct {
	// Name identifies the ResourceGrant that contributes to this bucket's limit.
	// Used for tracking quota sources and debugging allocation issues.
	//
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// LastObservedGeneration records the ResourceGrant's generation when the bucket
	// quota system last processed it. Used to detect when grants have been updated
	// and the bucket needs to recalculate its aggregated limit.
	//
	// +kubebuilder:validation:Required
	LastObservedGeneration int64 `json:"lastObservedGeneration"`

	// Amount specifies how much quota capacity this grant contributes to the bucket.
	// Represents the sum of all buckets within all allowances for the matching
	// resource type in the referenced grant. Measured in BaseUnit.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	Amount int64 `json:"amount"`
}

// AllowanceBucketSpec defines the desired state of AllowanceBucket.
// The system automatically creates buckets for each unique (consumer, resourceType) combination
// found in active ResourceGrants.
type AllowanceBucketSpec struct {
	// ConsumerRef identifies the quota consumer tracked by this bucket.
	// Must match the ConsumerRef from ResourceGrants that contribute to this bucket.
	// Only one bucket exists per unique (ConsumerRef, ResourceType) combination.
	//
	// Examples:
	// - Organization "acme-corp" consuming Project quota
	// - Project "web-app" consuming User quota
	// - Organization "enterprise-corp" consuming storage quota
	//
	// +kubebuilder:validation:Required
	ConsumerRef ConsumerRef `json:"consumerRef"`

	// ResourceType specifies which resource type this bucket aggregates quota for.
	// Must exactly match a ResourceRegistration.spec.resourceType that is currently active.
	// The quota system validates this reference and only creates buckets for registered types.
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
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	ResourceType string `json:"resourceType"`
}

// AllowanceBucketStatus contains the quota system-computed quota aggregation for a specific
// (consumer, resourceType) combination. The quota system continuously updates this status
// by aggregating capacity from active ResourceGrants and consumption from granted ResourceClaims.
type AllowanceBucketStatus struct {
	// ObservedGeneration indicates the most recent spec generation the quota system has processed.
	// When ObservedGeneration matches metadata.generation, the status reflects the current spec.
	// When ObservedGeneration is lower, the quota system is still processing recent changes.
	//
	// +kubebuilder:validation:Optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Limit represents the total quota capacity available for this (consumer, resourceType) combination.
	// Calculated by summing all bucket amounts from active ResourceGrants that match the bucket's
	// spec.consumerRef and spec.resourceType. Measured in BaseUnit from the ResourceRegistration.
	//
	// Aggregation logic:
	// - Only ResourceGrants with status.conditions[type=Active]=True contribute to the limit
	// - All allowances matching spec.resourceType are included from contributing grants
	// - All bucket amounts within matching allowances are summed
	//
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Required
	Limit int64 `json:"limit"`

	// Allocated represents the total quota currently consumed by granted ResourceClaims.
	// Calculated by summing all allocation amounts from ResourceClaims with status.conditions[type=Granted]=True
	// that match the bucket's spec.consumerRef and have requests for spec.resourceType.
	//
	// Aggregation logic:
	// - Only ResourceClaims with Granted=True contribute to allocated amount
	// - Only requests matching spec.resourceType are included
	// - All allocated amounts from matching requests are summed
	//
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Required
	Allocated int64 `json:"allocated"`

	// Available represents the quota capacity remaining for new ResourceClaims.
	// Always calculated as: Available = Limit - Allocated (never negative).
	// The system uses this value to determine whether new ResourceClaims can be granted.
	//
	// Decision logic:
	// - ResourceClaim is granted if requested amount <= Available
	// - ResourceClaim is denied if requested amount > Available
	// - Multiple concurrent claims may race; first to be processed wins
	//
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Required
	Available int64 `json:"available"`

	// ClaimCount indicates the total number of granted ResourceClaims consuming quota from this bucket.
	// Includes all ResourceClaims with status.conditions[type=Granted]=True that have requests
	// matching spec.resourceType and spec.consumerRef.
	//
	// Used for monitoring quota usage patterns and identifying potential issues.
	//
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Required
	ClaimCount int32 `json:"claimCount"`

	// GrantCount indicates the total number of active ResourceGrants contributing to this bucket's limit.
	// Includes all ResourceGrants with status.conditions[type=Active]=True that have allowances
	// matching spec.resourceType and spec.consumerRef.
	//
	// Used for understanding quota source distribution and debugging capacity issues.
	//
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Required
	GrantCount int32 `json:"grantCount"`

	// ContributingGrantRefs provides detailed information about each ResourceGrant that contributes
	// to this bucket's limit. Includes grant names, amounts, and last observed generations for
	// tracking and debugging quota sources.
	//
	// This field provides visibility into:
	// - Which grants are providing quota capacity
	// - How much each grant contributes
	// - Whether grants have been updated since last bucket calculation
	//
	// Grants are tracked individually because they are typically few in number compared to claims.
	//
	// +kubebuilder:validation:Optional
	ContributingGrantRefs []ContributingGrantRef `json:"contributingGrantRefs,omitempty"`

	// LastReconciliation records when the quota system last recalculated this status.
	// Used for monitoring quota system health and understanding how fresh the aggregated data is.
	//
	// The quota system updates this timestamp every time it processes the bucket, regardless of
	// whether the aggregated values changed.
	//
	// +kubebuilder:validation:Optional
	LastReconciliation *metav1.Time `json:"lastReconciliation,omitempty"`
}

// **AllowanceBucket** aggregates quota limits and usage for a single (consumer, resourceType) combination.
// The system automatically creates buckets to provide real-time quota availability information
// for **ResourceClaim** evaluation during admission.
//
// ### How It Works
// 1. **Auto-Creation**: Quota system creates buckets automatically for each unique (consumer, resourceType) pair found in active **ResourceGrants**
// 2. **Aggregation**: Quota system continuously aggregates capacity from active **ResourceGrants** and consumption from granted **ResourceClaims**
// 3. **Decision Support**: Quota system uses bucket `status.available` to determine if **ResourceClaims** can be granted
// 4. **Updates**: Quota system updates bucket status whenever contributing grants or claims change
//
// ### Aggregation Logic
// **AllowanceBuckets** serve as the central aggregation point where quota capacity meets quota consumption.
// The quota system continuously scans for **ResourceGrants** that match both the bucket's consumer
// and resource type, but only considers grants with an `Active` status condition. For each qualifying
// grant, the quota system examines all allowances targeting the bucket's resource type and sums the
// amounts from every bucket within those allowances. This sum becomes the bucket's limit - the total
// quota capacity available to the consumer for that specific resource type.
//
// Simultaneously, the quota system tracks quota consumption by finding all **ResourceClaims** with matching
// consumer and resource type specifications. However, only claims that have been successfully granted
// contribute to the allocated total. The quota system sums the allocated amounts from all granted
// requests, creating a running total of consumed quota capacity.
//
// The available quota emerges from this simple relationship: Available = Limit - Allocated. The
// system ensures this value never goes negative, treating any calculated negative as zero. This
// available amount represents the quota capacity remaining for new **ResourceClaims** and drives
// real-time admission decisions throughout the cluster.
//
// ### Real-Time Admission Decisions
// When a **ResourceClaim** is created:
// 1. Quota system identifies the relevant bucket (matching consumer and resource type)
// 2. Compares requested amount with bucket's `status.available`
// 3. Grants claim if requested amount <= available capacity
// 4. Denies claim if requested amount > available capacity
// 5. Updates bucket status to reflect the new allocation (if granted)
//
// ### Bucket Lifecycle
// 1. **Auto-Created**: When first ResourceGrant creates allowance for (consumer, resourceType)
// 2. **Active**: Continuously aggregated while ResourceGrants or ResourceClaims exist
// 3. **Updated**: Status refreshed whenever contributing resources change
// 4. **Persistent**: Buckets remain even when limit drops to 0 (for monitoring)
//
// ### Consistency and Performance
// **Eventual Consistency:**
// - Status may lag briefly after ResourceGrant or ResourceClaim changes
// - Controller processes updates asynchronously for performance
// - LastReconciliation timestamp indicates data freshness
//
// **Scale Optimization:**
// - Stores aggregates (limit, allocated, available) rather than individual entries
// - ContributingGrantRefs tracks grants (few) but not claims (many)
// - Single bucket per (consumer, resourceType) regardless of claim count
//
// ### Status Information
// - **Limit**: Total quota capacity from all contributing ResourceGrants
// - **Allocated**: Total quota consumed by all granted ResourceClaims
// - **Available**: Remaining quota capacity (Limit - Allocated)
// - **ClaimCount**: Number of granted claims consuming from this bucket
// - **GrantCount**: Number of active grants contributing to this bucket
// - **ContributingGrantRefs**: Detailed information about contributing grants
//
// ### Monitoring and Troubleshooting
// **Quota Monitoring:**
// - Monitor status.available to track quota usage trends
// - Check status.allocated vs status.limit for utilization ratios
// - Use status.claimCount to understand resource creation patterns
//
// **Troubleshooting Issues:**
// When investigating quota problems, start with the bucket's limit value. A limit of zero typically
// indicates that no ResourceGrants are contributing capacity for this consumer and resource type
// combination. Verify that ResourceGrants exist with matching consumer and resource type specifications,
// and confirm their status conditions show Active=True. Grants with validation failures or pending
// states won't contribute to bucket limits.
//
// High allocation values relative to limits suggest quota consumption issues. Review the ResourceClaims
// that match this bucket's consumer and resource type to identify which resources are consuming large
// amounts of quota. Check the claim allocation details to understand consumption patterns and identify
// potential quota leaks where claims aren't being cleaned up properly.
//
// Stale bucket data manifests as allocation or limit values that don't reflect recent changes to
// grants or claims. Check the lastReconciliation timestamp to determine data freshness, then examine
// quota system logs for aggregation errors or performance issues. The quota system should process
// changes within seconds under normal conditions.
//
// ### System Architecture
// - **Single Writer**: Only the quota system updates bucket status (prevents races)
// - **Dedicated Processing**: Separate components focus solely on bucket aggregation
// - **Event-Driven**: Responds to ResourceGrant and ResourceClaim changes
// - **Efficient Queries**: Uses indexes and field selectors for fast aggregation
//
// ### Selectors and Filtering
// - **Field selectors**: spec.consumerRef.kind, spec.consumerRef.name, spec.resourceType
// - **System labels** (set automatically by quota system):
//   - quota.miloapis.com/consumer-kind: Organization
//   - quota.miloapis.com/consumer-name: acme-corp
//
// ### Common Queries
// - All buckets for a consumer: label selector quota.miloapis.com/consumer-kind + quota.miloapis.com/consumer-name
// - All buckets for a resource type: field selector spec.resourceType=<value>
// - Specific bucket: field selector spec.consumerRef.name + spec.resourceType
// - Overutilized buckets: filter by status.available < threshold
// - Empty buckets: filter by status.limit = 0
//
// ### Performance Considerations
// - Bucket status updates are asynchronous and may lag resource changes
// - Large numbers of ResourceClaims can impact aggregation performance
// - Controller uses efficient aggregation queries to handle scale
// - Status updates are batched to reduce API server load
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +k8s:openapi-gen=true
// +kubebuilder:printcolumn:name="Resource Type",type="string",JSONPath=".spec.resourceType"
// +kubebuilder:printcolumn:name="Limit",type="integer",JSONPath=".status.limit"
// +kubebuilder:printcolumn:name="Allocated",type="integer",JSONPath=".status.allocated"
// +kubebuilder:printcolumn:name="Available",type="integer",JSONPath=".status.available"
// +kubebuilder:printcolumn:name="Claims",type="integer",JSONPath=".status.claimCount"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:selectablefield:JSONPath=".spec.consumerRef.kind"
// +kubebuilder:selectablefield:JSONPath=".spec.consumerRef.name"
// +kubebuilder:selectablefield:JSONPath=".spec.resourceType"
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Organization,Project"
type AllowanceBucket struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +kubebuilder:validation:Required
	Spec   AllowanceBucketSpec   `json:"spec"`
	Status AllowanceBucketStatus `json:"status,omitempty"`
}

// AllowanceBucketList contains a list of AllowanceBucket.
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
type AllowanceBucketList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AllowanceBucket `json:"items"`
}
