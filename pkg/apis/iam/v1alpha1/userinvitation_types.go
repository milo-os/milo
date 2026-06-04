package v1alpha1

import (
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type UserInvitationStateType string
type UserInvitationConditionType string
type UserInvitationReasonType string

const (
	UserInvitationStatePending  UserInvitationStateType = "Pending"
	UserInvitationStateAccepted UserInvitationStateType = "Accepted"
	UserInvitationStateDeclined UserInvitationStateType = "Declined"
)

const (
	UserInvitationStateExpiredReason  UserInvitationReasonType = "Expired"
	UserInvitationStateDeclinedReason UserInvitationReasonType = "Declined"
	UserInvitationStateAcceptedReason UserInvitationReasonType = "Accepted"
	UserInvitationStatePendingReason  UserInvitationReasonType = "Pending"
)

const (
	UserInvitationReadyCondition   UserInvitationConditionType = "Ready"
	UserInvitationExpiredCondition UserInvitationConditionType = "Expired"
	UserInvitationPendingCondition UserInvitationConditionType = "Pending"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// UserInvitation is the Schema for the userinvitations API
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Email",type=string,JSONPath=".spec.email"
// +kubebuilder:printcolumn:name="Expiration Date",type="string",JSONPath=".spec.expirationDate"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
// +kubebuilder:resource:path=userinvitations,scope=Namespaced
// +kubebuilder:selectablefield:JSONPath=".status.inviteeUser.name"
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Organization,User"
type UserInvitation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   UserInvitationSpec   `json:"spec,omitempty"`
	Status UserInvitationStatus `json:"status,omitempty"`
}

// UserInvitationSpec defines the desired state of UserInvitation
type UserInvitationSpec struct {
	// OrganizationRef is a reference to the Organization that the user is invoted to.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="type(oldSelf) == null_type || self == oldSelf",message="organizationRef type is immutable"
	OrganizationRef resourcemanagerv1alpha1.OrganizationReference `json:"organizationRef"`

	// The email of the user being invited.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="type(oldSelf) == null_type || self == oldSelf",message="email type is immutable"
	Email string `json:"email"`

	// The first name of the user being invited.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="type(oldSelf) == null_type || self == oldSelf",message="givenName type is immutable"
	GivenName string `json:"givenName,omitempty"`

	// The last name of the user being invited.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="type(oldSelf) == null_type || self == oldSelf",message="familyName type is immutable"
	FamilyName string `json:"familyName,omitempty"`

	// The roles that will be assigned to the user when they accept the invitation.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=100
	// +kubebuilder:validation:XValidation:rule="type(oldSelf) == null_type || self == oldSelf",message="roles type is immutable"
	Roles []RoleReference `json:"roles,omitempty"`

	// InvitedBy is the user who invited the user. A mutation webhook will default this field to the user who made the request.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="type(oldSelf) == null_type || self == oldSelf",message="invitedBy type is immutable"
	InvitedBy UserReference `json:"invitedBy,omitempty"`

	// ExpirationDate is the date and time when the UserInvitation will expire.
	// If not specified, the UserInvitation will never expire.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="type(oldSelf) == null_type || self == oldSelf",message="expirationDate type is immutable"
	ExpirationDate *metav1.Time `json:"expirationDate,omitempty"`

	// State is the state of the UserInvitation. In order to accept the invitation, the invited user
	// must set the state to Accepted.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Pending;Accepted;Declined
	// +kubebuilder:validation:XValidation:rule="type(oldSelf) == null_type || oldSelf == 'Pending' || self == oldSelf",message="state can only transition from Pending to another state and is immutable afterwards"
	State UserInvitationStateType `json:"state"`
}

// UserInvitationStatus defines the observed state of UserInvitation
type UserInvitationStatus struct {
	// Conditions provide conditions that represent the current status of the UserInvitation.
	// +kubebuilder:default={{type: "Unknown", status: "Unknown", reason: "Unknown", message: "Waiting for control plane to reconcile", lastTransitionTime: "1970-01-01T00:00:00Z"}}
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Organization contains information about the organization in the invitation.
	// +kubebuilder:validation:Optional
	Organization UserInvitationOrganizationStatus `json:"organization,omitempty"`

	// InviterUser contains information about the user who invited the user in the invitation.
	// +kubebuilder:validation:Optional
	InviterUser UserInvitationUserStatus `json:"inviterUser,omitempty"`

	// InviteeUser contains information about the invitee user in the invitation.
	// This value may be nil if the invitee user has not been created yet.
	// +kubebuilder:validation:Optional
	InviteeUser *UserInvitationInviteeUserStatus `json:"inviteeUser,omitempty"`
}

// UserInvitationOrganizationStatus contains information about the organization in the invitation.
type UserInvitationOrganizationStatus struct {
	// DisplayName is the display name of the organization in the invitation.
	// +kubebuilder:validation:Optional
	DisplayName string `json:"displayName,omitempty"`
}

// UserInvitationInviterUserStatus contains information about the user who invited the user in the invitation.
type UserInvitationUserStatus struct {
	// DisplayName is the display name of the user who invited the user in the invitation.
	// +kubebuilder:validation:Optional
	DisplayName string `json:"displayName,omitempty"`

	// EmailAddress is the email address of the user who invited the user in the invitation.
	// +kubebuilder:validation:Optional
	EmailAddress string `json:"emailAddress,omitempty"`
}

// UserInvitationInviteeUserStatus contains information about the invitee user in the invitation.
type UserInvitationInviteeUserStatus struct {
	// Name is the name of the invitee user in the invitation.
	// Name is a cluster-scoped resource, so Namespace is not needed.
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// UserInvitationList contains a list of UserInvitation
type UserInvitationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []UserInvitation `json:"items"`
}
