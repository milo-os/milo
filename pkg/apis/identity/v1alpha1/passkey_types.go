package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PasskeyState represents the activation state of a passkey authentication
// factor, as reported by the auth provider (AuthFactorState in Zitadel).
// One of: "Active", "Inactive".
type PasskeyState string

const (
	PasskeyStateActive   PasskeyState = "Active"
	PasskeyStateInactive PasskeyState = "Inactive"
)

// Passkey represents a WebAuthn passkey credential registered by a user with
// the external authentication provider (e.g., Zitadel).
//
// This is a read-only, virtual resource: milo does not persist passkeys and
// does not accept create/update/delete requests for this kind. Enrollment
// and removal are performed by auth-ui directly against the authentication
// provider; this API exists so other Milo-aware surfaces (cloud-portal,
// staff-support tooling) can list/display a user's enrolled passkeys
// without embedding a provider-specific client.
//
// metadata.name is the passkey ID assigned by the authentication provider.
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type Passkey struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Status PasskeyStatus `json:"status,omitempty"`
}

// PasskeyStatus contains the details of a passkey credential. All fields
// are read-only and populated by the authentication provider.
type PasskeyStatus struct {
	// UserUID is the unique identifier of the Milo user who owns this
	// passkey. Used as a field-selector target (status.userUID=<uid>) for
	// cross-user reads by staff-support callers — see the field-selector
	// registration in pkg/apis/identity/scheme.go and the Session/
	// UserIdentity precedent it mirrors.
	UserUID string `json:"userUID"`

	// DisplayName is the human-readable name of the passkey (Zitadel
	// `name`), either user-supplied at enrollment or defaulted from the
	// authenticator AAGUID / user agent by auth-ui.
	DisplayName string `json:"displayName"`

	// State is the current activation state of the passkey, derived from
	// the provider's AuthFactorState.
	State PasskeyState `json:"state"`
}

// PasskeyList is a list of Passkey resources.
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type PasskeyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Passkey `json:"items"`
}
