package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// Create conditions
const (
	// PlatformInvitationReadyCondition is the condition Type that tracks platform invitation creation status.
	PlatformInvitationReadyCondition = "Ready"
	// PlatformInvitationReconciledReason is used when platform invitation reconciliation succeeds.
	PlatformInvitationReconciledReason = "Reconciled"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// PlatformInvitation is the Schema for the platforminvitations API
// It represents a platform invitation for a user. Once the platform invitation is created, an email will be sent to the user to invite them to the platform.
// The invited user will have access to the platform after they create an account using the asociated email.
// +kubebuilder:printcolumn:name="Email",type=string,JSONPath=".spec.email"
// +kubebuilder:printcolumn:name="Schedule At",type="string",JSONPath=".spec.scheduleAt"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
// It represents a platform invitation for a user.
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Platform"
type PlatformInvitation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PlatformInvitationSpec   `json:"spec,omitempty"`
	Status PlatformInvitationStatus `json:"status,omitempty"`
}

// PlatformInvitationSpec defines the desired state of PlatformInvitation.
// +kubebuilder:validation:Type=object
type PlatformInvitationSpec struct {
	// The email of the user being invited.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="type(oldSelf) == null_type || self == oldSelf",message="email type is immutable"
	Email string `json:"email"`

	// The given name of the user being invited.
	// +kubebuilder:validation:Optional
	GivenName string `json:"givenName,omitempty"`

	// The family name of the user being invited.
	// +kubebuilder:validation:Optional
	FamilyName string `json:"familyName,omitempty"`

	// The schedule at which the platform invitation will be sent.
	// It can only be updated before the platform invitation is sent.
	// +kubebuilder:validation:Optional
	ScheduleAt *metav1.Time `json:"scheduleAt,omitempty"`

	// The user who created the platform invitation. A mutation webhook will default this field to the user who made the request.
	// +kubebuilder:validation:XValidation:rule="type(oldSelf) == null_type || self == oldSelf",message="invitedBy type is immutable"
	InvitedBy UserReference `json:"invitedBy,omitempty"`
}

// +kubebuilder:object:root=true

// PlatformInvitationList contains a list of PlatformInvitation.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type PlatformInvitationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PlatformInvitation `json:"items"`
}

// PlatformInvitationStatus defines the observed state of PlatformInvitation.
// +kubebuilder:validation:Type=object
type PlatformInvitationStatus struct {
	// Conditions provide conditions that represent the current status of the PlatformInvitation.
	// +kubebuilder:default={{type: "Ready", status: "Unknown", reason: "ReconcilePending", message: "Platform invitation reconciliation is pending", lastTransitionTime: "1970-01-01T00:00:00Z"}}
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// The email resource that was created for the platform invitation.
	// +kubebuilder:validation:Optional
	Email PlatformInvitationEmailStatus `json:"email,omitempty"`
}

type PlatformInvitationEmailStatus struct {
	// The name of the email resource that was created for the platform invitation.
	// +kubebuilder:validation:Optional
	Name string `json:"name,omitempty"`
	// The namespace of the email resource that was created for the platform invitation.
	// +kubebuilder:validation:Optional
	Namespace string `json:"namespace,omitempty"`
}
