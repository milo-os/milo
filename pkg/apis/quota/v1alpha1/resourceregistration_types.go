package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ConsumerType identifies the resource type that consumes quota.
// The consumer receives **ResourceGrants** and creates **ResourceClaims** for the registered resource.
// For example, when registering "Projects per Organization", **Organization** is the consumer type.
type ConsumerType struct {
	// APIGroup specifies the API group of the quota consumer resource type.
	// Use empty string for Kubernetes core resources (**Secret**, **ConfigMap**, etc.).
	// Use full group name for custom resources (for example, `resourcemanager.miloapis.com`).
	// Must follow DNS subdomain format with lowercase letters, numbers, and hyphens.
	//
	// Examples:
	// - `resourcemanager.miloapis.com` (**Organizations**, **Projects**)
	// - `iam.miloapis.com` (**Users**, **Groups**)
	// - `infrastructure.miloapis.com` (custom infrastructure resources)
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	APIGroup string `json:"apiGroup"`

	// Kind specifies the resource type that receives quota grants and creates quota claims.
	// Must match an existing Kubernetes resource type (core or custom).
	// Use the exact Kind name as defined in the resource's schema.
	//
	// Examples:
	// - **Organization** (receives **Project** quotas)
	// - **Project** (receives **User** quotas)
	// - **User** (receives resource quotas within projects)
	//
	// +kubebuilder:validation:Required
	Kind string `json:"kind"`
}

// ResourceRegistrationSpec defines the desired state of ResourceRegistration.
type ResourceRegistrationSpec struct {
	// ConsumerType specifies which resource type receives grants and creates claims for this registration.
	// The consumer type must exist in the cluster before creating the registration.
	//
	// Example: When registering "Projects per Organization", set `ConsumerType` to **Organization**
	// (apiGroup: `resourcemanager.miloapis.com`, kind: `Organization`). **Organizations** then
	// receive **ResourceGrants** allocating **Project** quota and create **ResourceClaims** when **Projects** are created.
	//
	// +kubebuilder:validation:Required
	ConsumerType ConsumerType `json:"consumerType"`

	// Type specifies the measurement method for quota tracking.
	// This field is immutable after creation.
	//
	// Valid values:
	// - `Entity`: Counts discrete resource instances. Use for resources where each instance
	//   consumes exactly 1 quota unit (for example, **Projects**, **Users**, **Databases**).
	//   Claims always request integer quantities.
	// - `Allocation`: Measures numeric capacity or resource amounts. Use for resources
	//   with variable consumption (for example, CPU millicores, memory bytes, storage capacity).
	//   Claims can request fractional amounts based on resource specifications.
	// - `Feature`: A boolean entitlement grant used for org-level feature flags. No admission
	//   enforcement or claim machinery is used — the registration simply signals that a feature
	//   is available to an organization. Grants convey on/off entitlement rather than a numeric
	//   capacity.
	//
	// +kubebuilder:validation:Enum=Entity;Allocation;Feature
	// +kubebuilder:validation:Required
	Type string `json:"type"`

	// ResourceType identifies the resource to track with quota.
	// Platform administrators define resource type identifiers that make sense for their
	// quota system usage. This field is immutable after creation.
	//
	// The identifier format is flexible to accommodate various naming conventions
	// and organizational needs. Service providers can use any meaningful identifier.
	//
	// Examples:
	// - "resourcemanager.miloapis.com/projects"
	// - "iam.miloapis.com/users"
	// - "compute_cpu"
	// - "storage.volumes"
	// - "custom-service-quota"
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	ResourceType string `json:"resourceType"`

	// Description provides human-readable context about what this registration tracks.
	// Use clear, specific language that explains the resource type and measurement approach.
	// Maximum 500 characters.
	//
	// Examples:
	// - "Projects created within Organizations"
	// - "CPU millicores allocated to workloads"
	// - "Storage bytes claimed by volume requests"
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=500
	// +kubebuilder:validation:MinLength=1
	Description string `json:"description,omitempty"`

	// BaseUnit defines the internal measurement unit for all quota calculations.
	// The system stores and processes all quota amounts using this unit.
	// Use singular form with lowercase letters. Maximum 50 characters.
	//
	// Examples:
	// - "project" (for Entity type tracking Projects)
	// - "millicore" (for CPU allocation)
	// - "byte" (for storage or memory)
	// - "user" (for Entity type tracking Users)
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=50
	BaseUnit string `json:"baseUnit"`

	// DisplayUnit defines the unit shown in user interfaces and API responses.
	// Should be more human-readable than BaseUnit. Use singular form. Maximum 50 characters.
	//
	// Examples:
	// - "project" (same as BaseUnit when no conversion needed)
	// - "core" (for displaying CPU instead of millicores)
	// - "GiB" (for displaying memory/storage instead of bytes)
	// - "TB" (for large storage volumes)
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=50
	DisplayUnit string `json:"displayUnit"`

	// UnitConversionFactor converts BaseUnit values to DisplayUnit values for presentation.
	// Must be a positive integer. Minimum value is 1 (no conversion).
	//
	// Formula: displayValue = baseValue / unitConversionFactor
	//
	// Examples:
	// - 1 (no conversion: "project" to "project")
	// - 1000 (millicores to cores: 2000 millicores displays as 2 cores)
	// - 1073741824 (bytes to GiB: 2147483648 bytes displays as 2 GiB)
	// - 1000000000000 (bytes to TB: 2000000000000 bytes displays as 2 TB)
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	UnitConversionFactor int64 `json:"unitConversionFactor"`

	// ClaimingResources specifies which resource types can create ResourceClaims for this registration.
	// Only resources listed here can trigger quota consumption for this resource type.
	// At least one claiming resource must be specified.
	// Maximum 20 entries.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=20
	ClaimingResources []ClaimingResource `json:"claimingResources"`
}

// ClaimingResource identifies a resource type that can create **ResourceClaims**
// for this registration. Uses unversioned references to remain valid across API version changes.
type ClaimingResource struct {
	// APIGroup specifies the API group of the resource that can create claims.
	// Use empty string for Kubernetes core resources (**Secret**, **ConfigMap**, etc.).
	// Use full group name for custom resources.
	//
	// Examples:
	// - `""` (core resources like **Secret**, **ConfigMap**)
	// - `resourcemanager.miloapis.com` (custom resource group)
	// - `iam.miloapis.com` (Milo IAM resources)
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Pattern=`^$|^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	APIGroup string `json:"apiGroup,omitempty"`

	// Kind specifies the resource type that can create **ResourceClaims** for this registration.
	// Must match an existing resource type. Maximum 63 characters.
	//
	// Examples:
	// - `Project` (**Project** resource creating claims for **Project** quota)
	// - `User` (**User** resource creating claims for **User** quota)
	// - `Organization` (**Organization** resource creating claims for **Organization** quota)
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Kind string `json:"kind"`
}

// ResourceRegistrationStatus reports the registration's operational state and processing status.
// The system updates status conditions to indicate whether the registration is active and
// usable for quota operations.
type ResourceRegistrationStatus struct {
	// ObservedGeneration indicates the most recent spec generation that the system has processed.
	// When ObservedGeneration matches metadata.generation, the status reflects the current spec.
	// When ObservedGeneration is lower, the system is still processing recent changes.
	//
	// +kubebuilder:validation:Optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions represents the latest available observations of the registration's state.
	// The system sets these conditions to communicate operational status.
	//
	// Standard condition types:
	// - "Active": Indicates whether the registration is operational. When True, ResourceGrants
	//   and ResourceClaims can reference this registration. When False, quota operations are blocked.
	//
	// Standard condition reasons for "Active":
	// - "RegistrationActive": Registration is validated and operational
	// - "ValidationFailed": Specification contains errors (see message for details)
	// - "RegistrationPending": Registration is being processed
	//
	// +kubebuilder:validation:XValidation:rule="self.all(c, c.type == 'Active' ? c.reason in ['RegistrationActive', 'ValidationFailed', 'RegistrationPending'] : true)",message="Active condition reason must be valid"
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Condition type constants for ResourceRegistration
const (
	// Indicates that the resource registration is active and ResourceGrants and
	// ResourceClaims can be created to set limits and claim resources.
	ResourceRegistrationActive = "Active"
)

// Condition reason constants for ResourceRegistration
const (
	// Indicates that the registration has been validated and is active.
	ResourceRegistrationActiveReason = "RegistrationActive"
	// Indicates that the registration validation failed.
	ResourceRegistrationValidationFailedReason = "ValidationFailed"
	// Indicates that the registration was not found.
	RegistrationNotFoundReason = "RegistrationNotFound"
	// Indicates that the registration is pending validation.
	ResourceRegistrationPendingReason = "RegistrationPending"
)

// ResourceRegistration enables quota tracking for a specific resource type.
// Administrators create registrations to define measurement units, consumer relationships,
// and claiming permissions.
//
// ### How It Works
// - Administrators create registrations to enable quota tracking for specific resource types
// - The system validates the registration and sets the "Active" condition when ready
// - ResourceGrants can then allocate capacity for the registered resource type
// - ResourceClaims can consume capacity when allowed resources are created
//
// ### Core Relationships
// - **ResourceGrant.spec.allowances[].resourceType** must match this registration's **spec.resourceType**
// - **ResourceClaim.spec.requests[].resourceType** must match this registration's **spec.resourceType**
// - **ResourceClaim.spec.consumerRef** must match this registration's **spec.consumerType** type
// - **ResourceClaim.spec.resourceRef** kind must be listed in this registration's **spec.claimingResources**
//
// ### Registration Lifecycle
// 1. **Creation**: Administrator creates **ResourceRegistration** with resource type and consumer type
// 2. **Validation**: System validates that referenced resource types exist and are accessible
// 3. **Activation**: System sets `Active=True` condition when validation passes
// 4. **Operation**: **ResourceGrants** and **ResourceClaims** can reference the active registration
// 5. **Updates**: Only mutable fields (`description`, `claimingResources`) can be changed
//
// ### Status Conditions
// - **Active=True**: Registration is validated and operational; grants and claims can use it
// - **Active=False, reason=ValidationFailed**: Configuration errors prevent activation (check message)
// - **Active=False, reason=RegistrationPending**: Quota system is processing the registration
//
// ### Measurement Types
// - **Entity registrations** (`spec.type=Entity`): Count discrete resource instances (**Projects**, **Users**)
// - **Allocation registrations** (`spec.type=Allocation`): Measure capacity amounts (CPU, memory, storage)
//
// ### Field Constraints and Limits
// - Maximum 20 entries in **spec.claimingResources**
// - **spec.resourceType**, **spec.consumerType**, and **spec.type** are immutable after creation
// - **spec.description** maximum 500 characters
// - **spec.baseUnit** and **spec.displayUnit** maximum 50 characters each
// - **spec.unitConversionFactor** minimum value is 1
//
// ### Selectors and Filtering
// - **Field selectors**: spec.consumerType.kind, spec.consumerType.apiGroup, spec.resourceType
// - **Recommended labels** (add manually):
//   - quota.miloapis.com/resource-kind: Project
//   - quota.miloapis.com/resource-apigroup: resourcemanager.miloapis.com
//   - quota.miloapis.com/consumer-kind: Organization
//
// ### Security Considerations
// - Only include trusted resource types in **spec.claimingResources**
// - Registrations are cluster-scoped and affect quota system-wide
// - Consumer types must have appropriate RBAC permissions to create claims
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:subresource:status
// +k8s:openapi-gen=true
// +kubebuilder:printcolumn:name="Type",type="string",JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="Active",type="string",JSONPath=".status.conditions[?(@.type=='Active')].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:selectablefield:JSONPath=".spec.consumerType.kind"
// +kubebuilder:selectablefield:JSONPath=".spec.consumerType.apiGroup"
// +kubebuilder:selectablefield:JSONPath=".spec.resourceType"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.spec.resourceType) || self.spec.resourceType == oldSelf.spec.resourceType",message="spec.resourceType is immutable"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.spec.consumerType) || self.spec.consumerType == oldSelf.spec.consumerType",message="spec.consumerType is immutable"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.spec.type) || self.spec.type == oldSelf.spec.type",message="spec.type is immutable"
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Platform"
type ResourceRegistration struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +kubebuilder:validation:Required
	Spec   ResourceRegistrationSpec   `json:"spec"`
	Status ResourceRegistrationStatus `json:"status,omitempty"`
}

// ResourceRegistrationList contains a list of ResourceRegistration.
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
type ResourceRegistrationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ResourceRegistration `json:"items"`
}
