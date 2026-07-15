// +kubebuilder:object:generate=true
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ProjectSuspensionReason specifies the reason for the project suspension.
// +kubebuilder:validation:Enum=Abuse;Billing;Compliance;Administrative
type ProjectSuspensionReason string

const (
	ReasonAbuse          ProjectSuspensionReason = "Abuse"
	ReasonBilling        ProjectSuspensionReason = "Billing"
	ReasonCompliance     ProjectSuspensionReason = "Compliance"
	ReasonAdministrative ProjectSuspensionReason = "Administrative"
)

// ProjectSuspensionReinstateAuthority specifies who may lift the suspension.
// +kubebuilder:validation:Enum=Operator;Consumer
type ProjectSuspensionReinstateAuthority string

const (
	AuthorityOperator ProjectSuspensionReinstateAuthority = "Operator"
	AuthorityConsumer ProjectSuspensionReinstateAuthority = "Consumer"
)

// ProjectSuspensionPhase specifies the current phase of the suspension.
// +kubebuilder:validation:Enum=Active;Lifted
type ProjectSuspensionPhase string

const (
	PhaseActive ProjectSuspensionPhase = "Active"
	PhaseLifted ProjectSuspensionPhase = "Lifted"
)

// ProjectSuspensionSpec defines the desired state of ProjectSuspension.
type ProjectSuspensionSpec struct {
	// ProjectRef is a reference to the project that is suspended.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="projectRef is immutable"
	ProjectRef ProjectReference `json:"projectRef"`

	// Reason is the category of suspension.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="reason is immutable"
	Reason ProjectSuspensionReason `json:"reason"`

	// ReinstateAuthority defines who can lift this suspension.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="reinstateAuthority is immutable"
	ReinstateAuthority ProjectSuspensionReinstateAuthority `json:"reinstateAuthority"`

	// RequestedBy identifies the operator or automated system that requested the suspension.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="requestedBy is immutable"
	RequestedBy string `json:"requestedBy"`

	// Description provides human-readable context or notes about the suspension.
	// +kubebuilder:validation:Optional
	Description string `json:"description,omitempty"`
}

// ProjectReference contains information that points to the Project being referenced.
type ProjectReference struct {
	// Name is the name of resource being referenced
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

// ProjectSuspensionStatus defines the observed state of ProjectSuspension.
type ProjectSuspensionStatus struct {
	// Phase is the current status of the suspension.
	// +kubebuilder:validation:Optional
	Phase ProjectSuspensionPhase `json:"phase,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Project",type=string,JSONPath=`.spec.projectRef.name`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.spec.reason`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// ProjectSuspension represents the intent/record of a project suspension.
type ProjectSuspension struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ProjectSuspensionSpec   `json:"spec,omitempty"`
	Status ProjectSuspensionStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// ProjectSuspensionList contains a list of ProjectSuspension.
type ProjectSuspensionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ProjectSuspension `json:"items"`
}
