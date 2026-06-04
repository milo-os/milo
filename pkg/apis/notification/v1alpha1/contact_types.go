package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Create conditions
const (
	// ContactReadyCondition is the condition Type that tracks contact creation status.
	ContactReadyCondition = "Ready"
	// ContactCreatePendingReason is used when contact creation is in progress.
	ContactCreatePendingReason = "CreatePending"
	// ContactCreatedReason is used when contact creation succeeds.
	ContactCreatedReason = "CreateSuccessful"
)

// Delete conditions
const (
	// ContactDeletedCondition is the condition Type that tracks contact deletion status.
	ContactDeletedCondition = "Delete"
	// ContactDeletePendingReason is used when contact deletion is in progress.
	ContactDeletePendingReason = "DeletePending"
	// ContactDeletedReason is used when contact deletion succeeds.
	ContactDeletedReason = "DeleteSuccessful"
)

// Update conditions
const (
	// ContactUpdatedCondition is the condition Type that tracks contact update status.
	ContactUpdatedCondition = "Update"
	// ContactUpdatePendingReason is used when contact update is in progress.
	ContactUpdatePendingReason = "UpdatePending"
	// ContactUpdatedReason is used when contact update succeeds.
	ContactUpdatedReason = "UpdateSuccessful"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Contact is the Schema for the contacts API.
// It represents a contact for a user.
// +kubebuilder:printcolumn:name="SubjectRef",type="string",JSONPath=".spec.subject.name"
// +kubebuilder:printcolumn:name="SubjectRef",type="string",JSONPath=".spec.subject.kind"
// +kubebuilder:printcolumn:name="EmailRef",type="string",JSONPath=".spec.email"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:selectablefield:JSONPath=".spec.email"
// +kubebuilder:selectablefield:JSONPath=".spec.subject.name"
// +kubebuilder:selectablefield:JSONPath=".spec.subject.kind"
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Platform,User"
type Contact struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ContactSpec   `json:"spec,omitempty"`
	Status ContactStatus `json:"status,omitempty"`
}

// ContactSpec defines the desired state of Contact.
// +kubebuilder:validation:Type=object
type ContactSpec struct {
	// Subject is a reference to the subject of the contact.
	// +kubebuilder:validation:Optional
	SubjectRef *SubjectReference `json:"subject,omitempty"`

	// +kubebuilder:validation:Optional
	FamilyName string `json:"familyName,omitempty"`

	// +kubebuilder:validation:Optional
	GivenName string `json:"givenName,omitempty"`

	// +kubebuilder:validation:Required
	Email string `json:"email,omitempty"`
}

// SubjectReference is a reference to the subject of the contact.
// +kubebuilder:validation:Type=object
type SubjectReference struct {
	// APIGroup is the group for the resource being referenced.
	// +kubebuilder:validation:Enum=iam.miloapis.com
	// +kubebuilder:validation:Required
	APIGroup string `json:"apiGroup,omitempty"`
	// Kind is the type of resource being referenced.
	// +kubebuilder:validation:Enum=User
	// +kubebuilder:validation:Required
	Kind string `json:"kind"`
	// Name is the name of resource being referenced.
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// Namespace is the namespace of resource being referenced.
	// Required for namespace-scoped resources. Omitted for cluster-scoped resources.
	// +kubebuilder:validation:Optional
	Namespace string `json:"namespace,omitempty"`
}

// +kubebuilder:object:root=true

// ContactList contains a list of Contact.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ContactList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Contact `json:"items"`
}

// ContactProviderStatus represents status information for a single contact provider.
// It allows tracking the provider name and the provider-specific identifier.
type ContactProviderStatus struct {
	// Name is the provider handling this contact.
	// Allowed values are Resend and Loops.
	// +kubebuilder:validation:Enum=Resend;Loops
	Name string `json:"name"`
	// ID is the identifier returned by the specific contact provider for this contact.
	// +kubebuilder:validation:Required
	ID string `json:"id"`
}

type ContactStatus struct {
	// Conditions represent the latest available observations of an object's current state.
	// Standard condition is "Ready" which tracks contact creation status and sync to the contact provider.
	// +kubebuilder:default={{type: "Ready", status: "Unknown", reason: "CreatePending", message: "Waiting for contact to be created", lastTransitionTime: "1970-01-01T00:00:00Z"}}
	// +kubebuilder:validation:Optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Providers contains the per-provider status for this contact.
	// This enables tracking multiple provider backends simultaneously.
	// +kubebuilder:validation:Optional
	Providers []ContactProviderStatus `json:"providers,omitempty"`

	// ProviderID is the identifier returned by the underlying contact provider
	// (e.g. Resend) when the contact is created. It is usually
	// used to track the contact creation status (e.g. provider webhooks).
	// Deprecated: Use Providers instead.
	// +optional
	ProviderID string `json:"providerID,omitempty"`
}
