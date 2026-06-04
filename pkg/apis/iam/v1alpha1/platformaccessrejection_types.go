package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// Create conditions
const (
	// PlatformAccessRejectionReadyCondition is the condition Type that tracks platform access rejection creation status.
	PlatformAccessRejectionReadyCondition = "Ready"
	// PlatformAccessRejectionReconciledReason is used when platform access rejection reconciliation succeeds.
	PlatformAccessRejectionReconciledReason = "Reconciled"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// PlatformAccessRejection is the Schema for the platformaccessrejections API.
// It represents a formal denial of platform access for a user. Once the rejection is created, a notification can be sent to the user.
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:selectablefield:JSONPath=".spec.subjectRef.name"
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Platform"
type PlatformAccessRejection struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec PlatformAccessRejectionSpec `json:"spec,omitempty"`
}

// PlatformAccessRejectionSpec defines the desired state of PlatformAccessRejection.
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable"
// +kubebuilder:validation:Type=object
type PlatformAccessRejectionSpec struct {
	// UserRef is the reference to the user being rejected.
	// +kubebuilder:validation:Required
	UserRef UserReference `json:"subjectRef"`

	// Reason is the reason for the rejection.
	// +kubebuilder:validation:Required
	Reason string `json:"reason"`

	// RejecterRef is the reference to the actor who issued the rejection.
	// If not specified, the rejection was made by the system.
	// +kubebuilder:validation:Optional
	RejecterRef *UserReference `json:"rejecterRef,omitempty"`
}

// +kubebuilder:object:root=true

// PlatformAccessRejectionList contains a list of PlatformAccessRejection.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type PlatformAccessRejectionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PlatformAccessRejection `json:"items"`
}
