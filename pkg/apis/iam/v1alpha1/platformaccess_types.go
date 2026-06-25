package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PlatformAccessState represents the access lifecycle state of a user on the platform.
// +kubebuilder:validation:Enum=Pending;Approved;Rejected;Suspended
type PlatformAccessState string

const (
	PlatformAccessStatePending   PlatformAccessState = "Pending"
	PlatformAccessStateApproved  PlatformAccessState = "Approved"
	PlatformAccessStateRejected  PlatformAccessState = "Rejected"
	PlatformAccessStateSuspended PlatformAccessState = "Suspended"
)

const (
	// PlatformAccessReadyCondition is set to True when the PlatformAccess has been reconciled.
	PlatformAccessReadyCondition = "Ready"
	// PlatformAccessReconciledReason is the typical reason used when reconciliation succeeds.
	PlatformAccessReconciledReason = "Reconciled"
)

// PlatformAccessSpec defines the desired access state for a user on the platform.
type PlatformAccessSpec struct {
	// UserRef is a reference to the User this resource governs.
	// User is a cluster-scoped resource.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="oldSelf == null || self == oldSelf",message="userRef is immutable"
	UserRef UserReference `json:"userRef"`

	// State is the desired platform access state for the user.
	// Valid transitions:
	//   Pending  → Approved  (fraud accepts, or admin approves)
	//   Pending  → Rejected  (fraud or admin rejects)
	//   Approved → Suspended (fraud deactivates, or admin suspends)
	//   Approved → Rejected  (admin disapproves)
	//   Suspended → Approved (admin reactivates)
	// +kubebuilder:validation:Required
	// +kubebuilder:default=Pending
	// +kubebuilder:validation:Enum=Pending;Approved;Rejected;Suspended
	State PlatformAccessState `json:"state"`

	// Reason is a human-readable explanation for the current state.
	// +kubebuilder:validation:Optional
	Reason string `json:"reason,omitempty"`
}

// PlatformAccessStatus defines the observed state of PlatformAccess.
type PlatformAccessStatus struct {
	// Conditions represent the latest available observations of the resource's current state.
	// +kubebuilder:default={{type: "Ready", status: "Unknown", reason: "Unknown", message: "Waiting for control plane to reconcile", lastTransitionTime: "1970-01-01T00:00:00Z"}}
	// +kubebuilder:validation:Optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// PlatformAccess is the Schema for the platformaccesses API.
// It is the single mutable resource governing whether a user can access the platform,
// replacing UserDeactivation, PlatformAccessApproval, and PlatformAccessRejection.
// There is at most one PlatformAccess per user; by convention it is named after the user.
// The UserController derives User.status.accessState from this resource.
// +kubebuilder:printcolumn:name="User",type="string",JSONPath=".spec.userRef.name"
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".spec.state"
// +kubebuilder:printcolumn:name="Reason",type="string",JSONPath=".spec.reason"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:selectablefield:JSONPath=".spec.userRef.name"
// +kubebuilder:selectablefield:JSONPath=".spec.state"
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Platform"
type PlatformAccess struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PlatformAccessSpec   `json:"spec,omitempty"`
	Status PlatformAccessStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// PlatformAccessList contains a list of PlatformAccess.
type PlatformAccessList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PlatformAccess `json:"items"`
}
