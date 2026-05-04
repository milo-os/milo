package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type Session struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Status SessionStatus `json:"status,omitempty"`
}

// SessionStatus contains session metadata exposed for display and management.
// All fields except those required for identity are optional and populated by the authentication provider.
type SessionStatus struct {
	// UserUID is the unique identifier of the user who owns this session.
	UserUID string `json:"userUID"`

	// Provider is the authentication provider for this session (e.g. "zitadel").
	Provider string `json:"provider"`

	// IP is the client IP address associated with the session, if known.
	IP string `json:"ip,omitempty"`

	// FingerprintID is an optional device or client fingerprint from the provider.
	FingerprintID string `json:"fingerprintID,omitempty"`

	// CreatedAt is when the session was created.
	CreatedAt metav1.Time `json:"createdAt"`

	// LastUpdatedAt is the last time the provider updated this session (e.g. Zitadel change_date).
	LastUpdatedAt *metav1.Time `json:"lastUpdatedAt,omitempty"`

	// UserAgent is the client User-Agent string for this session, if the provider supplies it.
	UserAgent string `json:"userAgent,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type SessionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Session `json:"items"`
}
