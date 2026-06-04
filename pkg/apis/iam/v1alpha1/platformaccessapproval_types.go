package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// Create conditions
const (
	// PlatformAccessApprovalReadyCondition is the condition Type that tracks platform access approval creation status.
	PlatformAccessApprovalReadyCondition = "Ready"
	// PlatformAccessApprovalReconciledReason is used when platform access approval reconciliation succeeds.
	PlatformAccessApprovalReconciledReason = "Reconciled"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// PlatformAccessApproval is the Schema for the platformaccessapprovals API.
// It represents a platform access approval for a user. Once the platform access approval is created, an email will be sent to the user.
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:selectablefield:JSONPath=".spec.subjectRef.email"
// +kubebuilder:selectablefield:JSONPath=".spec.subjectRef.userRef.name"
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Platform"
type PlatformAccessApproval struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec PlatformAccessApprovalSpec `json:"spec,omitempty"`
}

// PlatformAccessApprovalSpec defines the desired state of PlatformAccessApproval.
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable"
// +kubebuilder:validation:Type=object
type PlatformAccessApprovalSpec struct {
	// SubjectRef is the reference to the subject being approved.
	// +kubebuilder:validation:Required
	SubjectRef SubjectReference `json:"subjectRef"`
	// ApproverRef is the reference to the approver being approved.
	// If not specified, the approval was made by the system.
	// +kubebuilder:validation:Optional
	ApproverRef *UserReference `json:"approverRef,omitempty"`
}

// +kubebuilder:object:root=true

// PlatformAccessApprovalList contains a list of PlatformAccessApproval.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type PlatformAccessApprovalList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PlatformAccessApproval `json:"items"`
}

// +kubebuilder:validation:XValidation:rule="(has(self.email) && !has(self.userRef)) || (!has(self.email) && has(self.userRef))",message="Exactly one of email or userRef must be specified"
type SubjectReference struct {
	// Email is the email of the user being approved.
	// Use Email to approve an email address that is not associated with a created user. (e.g. when using PlatformInvitation)
	// UserRef and Email are mutually exclusive. Exactly one of them must be specified.
	// +kubebuilder:validation:Optional
	Email string `json:"email,omitempty"`
	// UserRef is the reference to the user being approved.
	// UserRef and Email are mutually exclusive. Exactly one of them must be specified.
	// +kubebuilder:validation:Optional
	UserRef *UserReference `json:"userRef,omitempty"`
}
