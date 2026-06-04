package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Condition and reason constants for UserDeactivation
const (
	// UserDeactivationReadyCondition is set to True when the deactivation has been processed.
	UserDeactivationReadyCondition = "Ready"
	// UserDeactivationReadyReason is the typical reason used when reconciliation succeeds.
	UserDeactivationReadyReason = "Reconciled"
)

// UserDeactivationSpec defines the desired state of UserDeactivation
type UserDeactivationSpec struct {
	// UserRef is a reference to the User being deactivated.
	// User is a cluster-scoped resource.
	// +kubebuilder:validation:Required
	UserRef UserReference `json:"userRef"`

	// Reason is the internal reason for deactivation.
	// +kubebuilder:validation:Required
	Reason string `json:"reason"`

	// Description provides detailed internal description for the deactivation.
	// +kubebuilder:validation:Optional
	Description string `json:"description,omitempty"`

	// DeactivatedBy indicates who initiated the deactivation.
	// +kubebuilder:validation:Required
	DeactivatedBy string `json:"deactivatedBy"`
}

// UserDeactivationStatus defines the observed state of UserDeactivation
type UserDeactivationStatus struct {
	// Conditions represent the latest available observations of an object's current state.
	// +kubebuilder:default={{type: "Ready", status: "Unknown", reason: "Unknown", message: "Waiting for control plane to reconcile", lastTransitionTime: "1970-01-01T00:00:00Z"}}
	// +kubebuilder:validation:Optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// UserDeactivation is the Schema for the userdeactivations API
// +kubebuilder:printcolumn:name="User",type="string",JSONPath=".spec.userRef.name"
// +kubebuilder:printcolumn:name="Reason",type="string",JSONPath=".spec.reason"
// +kubebuilder:printcolumn:name="Deactivated By",type="string",JSONPath=".spec.deactivatedBy"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:selectablefield:JSONPath=".spec.userRef.name"
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Platform"
type UserDeactivation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   UserDeactivationSpec   `json:"spec,omitempty"`
	Status UserDeactivationStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// UserDeactivationList contains a list of UserDeactivation
type UserDeactivationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []UserDeactivation `json:"items"`
}
