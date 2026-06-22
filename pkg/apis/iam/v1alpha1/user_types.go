package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type UserState string
type RegistrationApprovalState string
type UserWaitlistEmailSentCondition string

const (
	RegistrationApprovalStatePending  RegistrationApprovalState = "Pending"
	RegistrationApprovalStateApproved RegistrationApprovalState = "Approved"
	RegistrationApprovalStateRejected RegistrationApprovalState = "Rejected"
)

const (
	// UserWaitlistPendingEmailSentCondition tracks that the pending waitlist email was sent.
	UserWaitlistPendingEmailSentCondition UserWaitlistEmailSentCondition = "WaitlistPendingEmailSent"
	// UserWaitlistApprovedEmailSentCondition tracks that the approved waitlist email was sent.
	UserWaitlistApprovedEmailSentCondition UserWaitlistEmailSentCondition = "WaitlistApprovedEmailSent"
	// UserWaitlistRejectedEmailSentCondition tracks that the rejected waitlist email was sent.
	UserWaitlistRejectedEmailSentCondition UserWaitlistEmailSentCondition = "WaitlistRejectedEmailSent"
)

const (
	// UserWaitlistEmailSentReason is the condition reason used when a waitlist email was sent successfully.
	UserWaitlistEmailSentReason = "EmailSent"
)

const (
	UserStateActive   UserState = "Active"
	UserStateInactive UserState = "Inactive"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// User is the Schema for the users API
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Email",type="string",JSONPath=".spec.email"
// +kubebuilder:printcolumn:name="Given Name",type="string",JSONPath=".spec.givenName"
// +kubebuilder:printcolumn:name="Family Name",type="string",JSONPath=".spec.familyName"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state"
// +kubebuilder:printcolumn:name="Registration Approval",type="string",JSONPath=".status.registrationApproval"
// +kubebuilder:resource:path=users,scope=Cluster
// +kubebuilder:selectablefield:JSONPath=".status.registrationApproval"
// +kubebuilder:selectablefield:JSONPath=".spec.email"
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Platform,User"
type User struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   UserSpec   `json:"spec,omitempty"`
	Status UserStatus `json:"status,omitempty"`
}

// UserSpec defines the desired state of User
type UserSpec struct {
	// The email of the user.
	// +kubebuilder:validation:Required
	Email string `json:"email"`
	// The first name of the user.
	// +kubebuilder:validation:Optional
	GivenName string `json:"givenName,omitempty"`
	// The last name of the user.
	// +kubebuilder:validation:Optional
	FamilyName string `json:"familyName,omitempty"`
}

// UserStatus defines the observed state of User
type UserStatus struct {
	// Conditions provide conditions that represent the current status of the User.
	// +kubebuilder:default={{type: "Ready", status: "Unknown", reason: "Unknown", message: "Waiting for control plane to reconcile", lastTransitionTime: "1970-01-01T00:00:00Z"}}
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// State represents the current activation state of the user account from the
	// auth provider. This field is managed exclusively by the UserDeactivation CRD
	// and cannot be changed directly by the user. When a UserDeactivation resource
	// is created for the user, the user is deactivated in the auth provider; when
	// the UserDeactivation is deleted, the user is reactivated.
	// States:
	//   - Active: The user can be used to authenticate.
	//   - Inactive: The user is prohibited to be used to authenticate, and revokes all existing sessions.
	// +kubebuilder:default=Active
	// +kubebuilder:validation:Enum=Active;Inactive
	State UserState `json:"state,omitempty"`

	// RegistrationApproval represents the administrator’s decision on the user’s registration request.
	// States:
	//   - Pending:  The user is awaiting review by an administrator.
	//   - Approved: The user registration has been approved.
	//   - Rejected: The user registration has been rejected.
	// The User resource is always created regardless of this value, but the
	// ability for the person to sign into the platform and access resources is
	// governed by this status: only *Approved* users are granted access, while
	// *Pending* and *Rejected* users are prevented for interacting with resources.
	// +kubebuilder:validation:Enum=Pending;Approved;Rejected
	RegistrationApproval RegistrationApprovalState `json:"registrationApproval,omitempty"`

	// LastLoginProvider records the identity provider that was most recently used by the
	// user to log in (e.g., "github" or "google"). This field is set by the auth provider
	// based on authentication events.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=github;google
	LastLoginProvider AuthProvider `json:"lastLoginProvider,omitempty"`

	// LastLoginPerProvider tracks the most recent login timestamp for each identity provider
	// that the user has used to authenticate. The map key is the provider name (e.g., "github", "google")
	// and the value is the RFC3339 timestamp of the last successful login via that provider.
	// This field is updated by the auth provider when processing idpintent.succeeded events.
	// Note: This event is only triggered during actual IDP login, not on token refresh.
	// +kubebuilder:validation:Optional
	LastLoginPerProvider map[string]string `json:"lastLoginPerProvider,omitempty"`

	// LastTokenIntrospection records the timestamp of the most recent successful token introspection
	// for this user. This is updated during authentication webhook calls when validating access tokens,
	// which occurs more frequently than actual IDP logins (including token refreshes).
	// The value is an RFC3339 timestamp.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Format=date-time
	LastTokenIntrospection *metav1.Time `json:"lastTokenIntrospection,omitempty"`

	// AvatarURL points to the avatar image associated with the user. This value is
	// populated by the auth provider or any service that provides a user avatar URL.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Format=uri
	AvatarURL string `json:"avatarUrl,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// UserList contains a list of User
type UserList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []User `json:"items"`
}

// AuthProvider represents an external identity provider used for user authentication.
// +kubebuilder:validation:Enum=github;google
type AuthProvider string

const (
	AuthProviderGitHub AuthProvider = "github"
	AuthProviderGoogle AuthProvider = "google"
)

const (
	// UserNameReviewRequiredAnnotation is set on a User when givenName and familyName are
	// identical, which typically happens when the identity provider (e.g. GitHub) supplies
	// only a single display name and the system splits it across both fields.
	//
	// Presence of this annotation signals that the user has not yet provided distinct given
	// and family names. Front-end clients should prompt the user to review and update their
	// profile when this annotation is present.
	//
	// The annotation value is always "true". The annotation is removed automatically by the
	// user controller once givenName and familyName differ.
	//
	// Example:
	//
	//   metadata:
	//     annotations:
	//       iam.miloapis.com/name-review-required: "true"
	UserNameReviewRequiredAnnotation = "iam.miloapis.com/name-review-required"
)
