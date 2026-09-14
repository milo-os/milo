package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PasskeyRegistrationLink asks the authentication provider to issue a single-use passkey
// registration code for a user and mails it, through the notification pipeline, to that
// user's VERIFIED email address. It is how support recovers an account whose passkeys are
// all lost (passkey program Phase C, the admin backstop).
//
// Create-only virtual resource served by zitadel-provider's identity apiserver: milo does
// not persist it, there is no update, and there is no delete because the provider cannot
// revoke an issued code — it expires. The durable record is the notification Email the
// create produces (see Status.EmailName), labeled with the user, requester and reason.
//
// metadata.name is assigned by the server.
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type PasskeyRegistrationLink struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PasskeyRegistrationLinkSpec   `json:"spec,omitempty"`
	Status PasskeyRegistrationLinkStatus `json:"status,omitempty"`
}

// PasskeyRegistrationLinkSpec is what support asks for.
type PasskeyRegistrationLinkSpec struct {
	// UserRef names the iam.miloapis.com User (metadata.name is the provider user ID)
	// who receives the link. The link only ever goes to that user's verified address.
	// +kubebuilder:validation:Required
	UserRef PasskeyRegistrationLinkUserReference `json:"userRef"`
	// RequestedBy is the metadata.name of the staff User asking. Recorded for audit;
	// the server rejects a value that does not match the authenticated caller.
	// +kubebuilder:validation:Required
	RequestedBy string `json:"requestedBy"`
	// Reason is why the link is being sent (ticket reference, customer request). Required.
	// +kubebuilder:validation:Required
	Reason string `json:"reason"`
}

// PasskeyRegistrationLinkUserReference points at a User by name.
type PasskeyRegistrationLinkUserReference struct {
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

// PasskeyRegistrationLinkStatus is filled by the server on create.
type PasskeyRegistrationLinkStatus struct {
	// UserUID is the UID of the User the link was sent to.
	// +kubebuilder:validation:Optional
	UserUID string `json:"userUID,omitempty"`
	// EmailName is the notification.miloapis.com Email resource that carries the link.
	// +kubebuilder:validation:Optional
	EmailName string `json:"emailName,omitempty"`
	// ExpiresAt is when the issued code stops working (the provider's configured lifetime).
	// +kubebuilder:validation:Optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`
}

// PasskeyRegistrationLinkList is a list of PasskeyRegistrationLink resources.
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type PasskeyRegistrationLinkList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PasskeyRegistrationLink `json:"items"`
}
