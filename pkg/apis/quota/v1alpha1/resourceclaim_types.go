package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ResourceRequest defines a single resource request within a ResourceClaim.
// Each request specifies a resource type and the amount of quota being claimed.
type ResourceRequest struct {
	// ResourceType identifies the specific resource type being claimed. Must
	// exactly match a ResourceRegistration.spec.resourceType that is currently
	// active. The quota system validates this reference during claim processing.
	//
	// The format is defined by platform administrators when creating ResourceRegistrations.
	// Service providers can use any identifier that makes sense for their quota system usage.
	//
	// Examples:
	//
	//   - "resourcemanager.miloapis.com/projects"
	//   - "compute_cpu"
	//   - "storage.volumes"
	//   - "custom-service-quota"
	//
	// +kubebuilder:validation:Required
	ResourceType string `json:"resourceType"`

	// Amount specifies how much quota to claim for this resource type. Must be
	// measured in the BaseUnit defined by the corresponding ResourceRegistration.
	// Must be a positive integer (minimum value is 0, but 0 means no quota
	// requested).
	//
	// For Entity registrations: Use 1 for single resource instances (1 Project, 1
	// User) For Allocation registrations: Use actual capacity amounts (2048 for
	// 2048 MB, 1000 for 1000 millicores)
	//
	// Examples:
	//
	//   - 1 (claiming 1 Project)
	//   - 2048 (claiming 2048 bytes of storage)
	//   - 1000 (claiming 1000 CPU millicores)
	//
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Required
	Amount int64 `json:"amount"`
}

// ResourceClaimSpec defines the desired state of ResourceClaim.
type ResourceClaimSpec struct {
	// ConsumerRef identifies the quota consumer making this claim. The consumer
	// must match the ConsumerType defined in the ResourceRegistration for each
	// requested resource type. The system validates this relationship during
	// claim processing.
	//
	// When creating ResourceClaims via ClaimCreationPolicy, this field can be
	// omitted and the admission plugin will automatically fill it based on the
	// authenticated user's context (organization or project).
	//
	// Examples:
	//
	//   - Organization consuming Project quota
	//   - Project consuming User quota
	//   - Organization consuming storage quota
	//
	// +kubebuilder:validation:Optional
	ConsumerRef ConsumerRef `json:"consumerRef,omitempty"`

	// Requests specifies the resource types and amounts being claimed from quota.
	// Each resource type can appear only once in the requests array. Minimum 1
	// request, maximum 20 requests per claim.
	//
	// The system processes all requests as a single atomic operation: either all
	// requests are granted or all are denied.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=20
	Requests []ResourceRequest `json:"requests"`

	// ResourceRef identifies the actual Kubernetes resource that triggered this
	// claim. ClaimCreationPolicy automatically populates this field during
	// admission. Uses unversioned reference (apiGroup + kind + name + namespace)
	// to remain valid across API version changes.
	//
	// The referenced resource's kind must be listed in the ResourceRegistration's
	// spec.claimingResources for the claim to be valid.
	//
	// Examples:
	//
	//   - Project resource triggering Project quota claim
	//   - User resource triggering User quota claim
	//   - Organization resource triggering storage quota claim
	//
	// +optional
	ResourceRef *UnversionedObjectReference `json:"resourceRef,omitempty"`
}

// ResourceClaimAllocationStatus tracks the allocation status for a specific resource
// request within a claim. The system creates one allocation entry for each
// request in the claim specification.
type ResourceClaimAllocationStatus struct {
	// ResourceType identifies which resource request this allocation status
	// describes. Must exactly match one of the resourceType values in
	// spec.requests.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ResourceType string `json:"resourceType"`

	// Status indicates the allocation result for this specific resource request.
	//
	// Valid values:
	//
	//   - "Granted": Quota was available and the request was approved
	//   - "Denied": Insufficient quota or validation failure prevented allocation
	//   - "Pending": Request is being evaluated (initial state)
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Granted;Denied;Pending
	Status string `json:"status"`

	// Reason provides a machine-readable explanation for the current status.
	// Standard reasons include "QuotaAvailable", "QuotaExceeded",
	// "ValidationFailed".
	//
	// +kubebuilder:validation:Optional
	Reason string `json:"reason,omitempty"`

	// Message provides a human-readable explanation of the allocation result.
	// Includes specific details about quota availability or validation errors.
	//
	// Examples:
	//
	//   - "Allocated 1 project from bucket organization-acme-projects"
	//   - "Insufficient quota: need 2048 bytes, only 1024 available"
	//   - "ResourceRegistration not found for resourceType"
	//
	// +kubebuilder:validation:Optional
	Message string `json:"message,omitempty"`

	// AllocatedAmount specifies how much quota was actually allocated for this
	// request. Measured in the BaseUnit defined by the ResourceRegistration.
	// Currently always equals the requested amount or 0 (partial allocations not
	// supported).
	//
	// Set to the requested amount when Status=Granted, 0 when Status=Denied or
	// Pending.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=0
	AllocatedAmount int64 `json:"allocatedAmount,omitempty"`

	// AllocatingBucket identifies the AllowanceBucket that provided the quota for
	// this request. Set only when Status=Granted. Used for tracking and debugging
	// quota consumption.
	//
	// Format: bucket name (generated as:
	// consumer-kind-consumer-name-resource-type-hash)
	//
	// +kubebuilder:validation:Optional
	AllocatingBucket string `json:"allocatingBucket,omitempty"`

	// LastTransitionTime records when this allocation status last changed.
	// Updates whenever Status, Reason, or Message changes.
	//
	// +kubebuilder:validation:Required
	LastTransitionTime metav1.Time `json:"lastTransitionTime"`
}

// ResourceClaimStatus reports the claim's processing state and allocation
// results. The system updates this status to communicate whether quota was
// granted and provide detailed allocation information for each requested
// resource type.
type ResourceClaimStatus struct {
	// ObservedGeneration indicates the most recent spec generation the system has
	// processed. When ObservedGeneration matches metadata.generation, the status
	// reflects the current spec. When ObservedGeneration is lower, the system is
	// still processing recent changes.
	//
	// +kubebuilder:validation:Optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Allocations provides detailed status for each resource request in the
	// claim. The system creates one allocation entry for each request in
	// spec.requests. Use this field to understand which specific requests were
	// granted or denied.
	//
	// List is indexed by ResourceType for efficient lookups.
	//
	// +kubebuilder:validation:Optional
	// +listType=map
	// +listMapKey=resourceType
	Allocations []ResourceClaimAllocationStatus `json:"allocations,omitempty"`

	// Conditions represents the overall status of the claim evaluation.
	// Controllers set these conditions to provide a high-level view of claim
	// processing.
	//
	// Standard condition types:
	//
	//   - "Granted": Indicates whether the claim was approved and quota allocated
	//
	// Standard condition reasons for "Granted":
	//
	//   - "QuotaAvailable": All requested quota was available and allocated
	//   - "QuotaExceeded": Insufficient quota prevented allocation (claim denied)
	//   - "ValidationFailed": Configuration errors prevented evaluation (claim denied)
	//   - "PendingEvaluation": Claim is still being processed (initial state)
	//
	// Claim Lifecycle:
	//
	//   1. Created: Granted=False, reason=PendingEvaluation
	//   2. Processed: Granted=True/False based on quota availability and validation
	//   3. Updated: Granted condition changes only when allocation results change
	//
	// +kubebuilder:validation:XValidation:rule="self.all(c, c.type == 'Granted' ? c.reason in ['QuotaAvailable', 'QuotaExceeded', 'ValidationFailed', 'PendingEvaluation'] : true)",message="Granted condition reason must be valid"
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Condition type constants for ResourceClaim
const (
	// Indicates whether the ResourceClaim was granted after evaluation
	ResourceClaimGranted = "Granted"
)

// Condition reason constants for ResourceClaim status updates
const (
	// Granted due to quota being available
	ResourceClaimGrantedReason = "QuotaAvailable"
	// Denied due to it exceeding the quota limit
	ResourceClaimDeniedReason = "QuotaExceeded"
	// Indicates that status update validation failed.
	ResourceClaimValidationFailedReason = "ValidationFailed"
	// Indicates that the ResourceClaim has not finished being evaluated against
	// the total effective quota limit
	ResourceClaimPendingReason = "PendingEvaluation"
)

// ResourceClaimAllocationStatus status constants
const (
	// Request allocation is granted and resources are reserved
	ResourceClaimAllocationStatusGranted = "Granted"
	// Request allocation is denied due to insufficient quota
	ResourceClaimAllocationStatusDenied = "Denied"
	// Request allocation is pending evaluation
	ResourceClaimAllocationStatusPending = "Pending"
)

// ResourceClaim requests quota allocation during resource creation. Claims
// consume quota capacity from AllowanceBuckets and link to the triggering
// Kubernetes resource for lifecycle management and auditing.
//
// ### How It Works
//
// **ResourceClaims** follow a straightforward lifecycle from creation to
// resolution. When a **ClaimCreationPolicy** triggers during admission, it
// creates a **ResourceClaim** that immediately enters the quota evaluation
// pipeline. The quota system first validates that the consumer type matches the
// expected `ConsumerType` from the **ResourceRegistration**, then verifies
// that the triggering resource kind is authorized to claim the requested
// resource types.
//
// Once validation passes, the quota system checks quota availability by
// consulting the relevant **AllowanceBuckets**, one for each (consumer,
// resourceType) combination in the claim's requests. The quota system treats
// all requests in a claim as an atomic unit: either sufficient quota exists for
// every request and the entire claim is granted, or any shortage results in
// denying the complete claim. This atomic approach ensures consistency and
// prevents partial resource allocations that could leave the system in an
// inconsistent state.
//
// When a claim is granted, it permanently reserves the requested quota amounts
// until the claim is deleted. This consumption immediately reduces the
// available quota in the corresponding **AllowanceBuckets**, preventing other
// claims from accessing that capacity. The quota system updates the claim's
// status with detailed results for each resource request, including which
// **AllowanceBucket** provided the quota and any relevant error messages.
//
// ### Core Relationships
//
//   - **Created by**: **ClaimCreationPolicy** during admission (automatically) or
//     administrators (manually)
//   - **Consumes from**: **AllowanceBucket** matching
//     (`spec.consumerRef`, `spec.requests[].resourceType`)
//   - **Capacity sourced from**: **ResourceGrant** objects aggregated by the bucket
//   - **Linked to**: Triggering resource via `spec.resourceRef` for lifecycle management
//   - **Validated against**: **ResourceRegistration** for each `spec.requests[].resourceType`
//
// ### Claim Lifecycle States
//
//   - **Initial**: `Granted=False`, `reason=PendingEvaluation` (claim created, awaiting processing)
//   - **Granted**: `Granted=True`, `reason=QuotaAvailable` (all requests allocated successfully)
//   - **Denied**: `Granted=False`, `reason=QuotaExceeded` or `ValidationFailed` (requests could not be satisfied)
//
// ### Automatic vs Manual Claims
//
// **Automatic Claims** (created by **ClaimCreationPolicy**):
//
//   - Include standard labels and annotations for tracking
//   - Set owner references to triggering resource when possible
//   - Automatically cleaned up when denied to prevent accumulation
//   - Marked with `quota.miloapis.com/auto-created=true` label
//
// **Manual Claims** (created by administrators):
//
//   - Require explicit metadata and references
//   - Not automatically cleaned up when denied
//   - Used for testing or special allocation scenarios
//
// ### Status Information
//
//   - **Overall Status**: `status.conditions[type=Granted]` indicates claim approval
//   - **Detailed Results**: `status.allocations[]` provides per-request allocation details
//   - **Bucket References**: `status.allocations[].allocatingBucket` identifies quota sources
//
// ### Field Constraints and Validation
//
//   - Maximum 20 resource requests per claim
//   - Each resource type can appear only once in requests
//   - Consumer type must match `ResourceRegistration.spec.consumerType` for each requested type
//   - Triggering resource kind must be listed in `ResourceRegistration.spec.claimingResources`
//
// ### Selectors and Filtering
//
//   - **Field selectors**: spec.consumerRef.kind, spec.consumerRef.name, spec.resourceRef.apiGroup, spec.resourceRef.kind, spec.resourceRef.name, spec.resourceRef.namespace
//   - **Auto-created labels**: quota.miloapis.com/auto-created, quota.miloapis.com/policy, quota.miloapis.com/gvk
//   - **Auto-created annotations**: quota.miloapis.com/created-by, quota.miloapis.com/created-at,  quota.miloapis.com/resource-name
//
// ### Common Queries
//
//   - All claims for a consumer: field selector spec.consumerRef.kind + spec.consumerRef.name
//   - Claims from a specific policy: label selector quota.miloapis.com/policy=<policy-name>
//   - Claims for a resource type: add custom labels via policy template
//   - Failed claims: field  selector on status conditions
//
// ### Troubleshooting
//
//   - **Denied claims**: Check status.allocations[].message for specific quota or validation errors
//   - **Pending claims**: Verify ResourceRegistration is Active and AllowanceBucket exists
//   - **Missing claims**: Check ClaimCreationPolicy conditions and trigger expressions
//
// ### Performance Considerations
//
//   - Claims are processed synchronously during admission (affects API latency)
//   - Large numbers of claims can impact bucket aggregation performance
//   - Consider batch processing for bulk resource creation
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Granted",type="string",JSONPath=".status.conditions[?(@.type=='Granted')].status"
// +kubebuilder:printcolumn:name="Resource",type="string",JSONPath=".spec.requests[0].resourceType",priority=1
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +k8s:openapi-gen=true
// +kubebuilder:selectablefield:JSONPath=".spec.consumerRef.kind"
// +kubebuilder:selectablefield:JSONPath=".spec.consumerRef.name"
// +kubebuilder:selectablefield:JSONPath=".spec.resourceRef.apiGroup"
// +kubebuilder:selectablefield:JSONPath=".spec.resourceRef.kind"
// +kubebuilder:selectablefield:JSONPath=".spec.resourceRef.name"
// +kubebuilder:selectablefield:JSONPath=".spec.resourceRef.namespace"
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Organization,Project"
type ResourceClaim struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +kubebuilder:validation:Required
	Spec ResourceClaimSpec `json:"spec"`
	// +kubebuilder:default={conditions: {{type:"Granted",status:"False",reason:"PendingEvaluation",message:"Awaiting capacity evaluation", lastTransitionTime: "1970-01-01T00:00:00Z"}}}
	Status ResourceClaimStatus `json:"status,omitempty"`
}

// ResourceClaimList contains a list of ResourceClaim.
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
type ResourceClaimList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ResourceClaim `json:"items"`
}
