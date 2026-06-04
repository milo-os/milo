package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Create conditions
const (
	// ContactGroupMembershipReadyCondition is the condition Type that tracks contact-group membership creation status.
	ContactGroupMembershipReadyCondition = "Ready"
	// ContactGroupMembershipCreatePendingReason is used when membership creation is in progress.
	ContactGroupMembershipCreatePendingReason = "CreatePending"
	// ContactGroupMembershipCreatedReason is used when membership creation succeeds.
	ContactGroupMembershipCreatedReason = "CreateSuccessful"
)

// Delete conditions
const (
	// ContactGroupMembershipDeletedCondition is the condition Type that tracks contact deletion status.
	ContactGroupMembershipDeletedCondition = "Delete"
	// ContactGroupMembershipDeletePendingReason is used when contact deletion is in progress.
	ContactGroupMembershipDeletePendingReason = "DeletePending"
	// ContactGroupMembershipDeletedReason is used when contact deletion succeeds.
	ContactGroupMembershipDeletedReason = "DeleteSuccessful"
)

// Update conditions
const (
	// ContactGroupMembershipUpdatedCondition is the condition Type that tracks contact update status.
	ContactGroupMembershipUpdatedCondition = "Update"
	// ContactGroupMembershipUpdatePendingReason is used when contact update is in progress against
	// the external provider.
	ContactGroupMembershipUpdatePendingReason = "UpdatePending"
	// ContactGroupMembershipUpdatePendingReason is used when contact update has been
	// requested internally (e.g. when a contact group is updated, an the contact group membership is updated accordingly).
	ContactGroupMembershipUpdateRequestedReason = "UpdateRequested"
	// ContactGroupMembershipUpdatedReason is used when contact update succeeds.
	ContactGroupMembershipUpdatedReason = "UpdateSuccessful"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ContactGroupMembership is the Schema for the contactgroupmemberships API.
// It represents a membership of a Contact in a ContactGroup.
// +kubebuilder:printcolumn:name="Contact",type="string",JSONPath=".spec.contactRef.name"
// +kubebuilder:printcolumn:name="ContactGroup",type="string",JSONPath=".spec.contactGroupRef.name"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:selectablefield:JSONPath=".spec.contactRef.name"
// +kubebuilder:selectablefield:JSONPath=".spec.contactGroupRef.name"
// +kubebuilder:selectablefield:JSONPath=".status.username"
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Platform,User"
type ContactGroupMembership struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ContactGroupMembershipSpec   `json:"spec,omitempty"`
	Status ContactGroupMembershipStatus `json:"status,omitempty"`
}

// ContactGroupMembershipSpec defines the desired state of ContactGroupMembership.
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable"
// +kubebuilder:validation:Type=object
type ContactGroupMembershipSpec struct {
	// ContactRef is a reference to the Contact that is a member of the ContactGroup.
	// +kubebuilder:validation:Required
	ContactRef ContactReference `json:"contactRef"`

	// ContactGroupRef is a reference to the ContactGroup that the Contact is a member of.
	// +kubebuilder:validation:Required
	ContactGroupRef ContactGroupReference `json:"contactGroupRef"`
}

// ContactReference contains information that points to the Contact being referenced.
type ContactReference struct {
	// Name is the name of the Contact being referenced.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Namespace is the namespace of the Contact being referenced.
	// +kubebuilder:validation:Required
	Namespace string `json:"namespace"`
}

// ContactGroupReference contains information that points to the ContactGroup being referenced.
type ContactGroupReference struct {
	// Name is the name of the ContactGroup being referenced.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Namespace is the namespace of the ContactGroup being referenced.
	// +kubebuilder:validation:Required
	Namespace string `json:"namespace"`
}

// +kubebuilder:object:root=true

// ContactGroupMembershipList contains a list of ContactGroupMembership.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ContactGroupMembershipList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ContactGroupMembership `json:"items"`
}

type ContactGroupMembershipStatus struct {
	// Conditions represent the latest available observations of an object's current state.
	// Standard condition is "Ready" which tracks contact group membership creation status and sync to the contact group membership provider.
	// +kubebuilder:default={{type: "Ready", status: "Unknown", reason: "CreatePending", message: "Waiting for contact group membership to be created", lastTransitionTime: "1970-01-01T00:00:00Z"}}
	// +kubebuilder:validation:Optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Providers contains the per-provider status for this contact group membership.
	// This enables tracking multiple provider backends simultaneously.
	// +kubebuilder:validation:Optional
	Providers []ContactProviderStatus `json:"providers,omitempty"`

	// ProviderID is the identifier returned by the underlying contact provider
	// (e.g. Resend) when the membership is created in the associated audience. It is usually
	// used to track the contact-group membership creation status (e.g. provider webhooks).
	// Deprecated: Use Providers instead.
	// +optional
	ProviderID string `json:"providerID,omitempty"`

	// Username is the username of the user that owns the ContactGroupMembership.
	// This is populated by the controller based on the referenced Contact's subject.
	// +optional
	Username string `json:"username,omitempty"`
}
