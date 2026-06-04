package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Create conditions
const (
	// ContactGroupMembershipRemovalReadyCondition is the condition Type that tracks contact group membership removal creation status.
	ContactGroupMembershipRemovalReadyCondition = "Ready"
	// ContactGroupMembershipRemovalCreatedReason is used when contact group membership removal creation succeeds.
	ContactGroupMembershipRemovalCreatedReason = "CreateSuccessful"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ContactGroupMembershipRemoval is the Schema for the contactgroupmembershipremovals API.
// It represents a removal of a Contact from a ContactGroup, it also prevents the Contact from being added to the ContactGroup.
// +kubebuilder:printcolumn:name="Contact",type="string",JSONPath=".spec.contactRef.name"
// +kubebuilder:printcolumn:name="ContactGroup",type="string",JSONPath=".spec.contactGroupRef.name"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:validation:Type=object
// +kubebuilder:selectablefield:JSONPath=".spec.contactRef.name"
// +kubebuilder:selectablefield:JSONPath=".status.username"
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Platform,User"
type ContactGroupMembershipRemoval struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ContactGroupMembershipRemovalSpec   `json:"spec,omitempty"`
	Status ContactGroupMembershipRemovalStatus `json:"status,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable"
type ContactGroupMembershipRemovalSpec struct {
	// ContactRef is a reference to the Contact that prevents the Contact from being part of the ContactGroup.
	// +kubebuilder:validation:Required
	ContactRef ContactReference `json:"contactRef"`

	// ContactGroupRef is a reference to the ContactGroup that the Contact does not want to be a member of.
	// +kubebuilder:validation:Required
	ContactGroupRef ContactGroupReference `json:"contactGroupRef"`
}

// +kubebuilder:object:root=true

// ContactGroupMembershipRemovalList contains a list of ContactGroupMembershipRemoval.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ContactGroupMembershipRemovalList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ContactGroupMembershipRemoval `json:"items"`
}

type ContactGroupMembershipRemovalStatus struct {
	// Conditions represent the latest available observations of an object's current state.
	// Standard condition is "Ready" which tracks contact group membership removal creation status.
	// +kubebuilder:default={{type: "Ready", status: "Unknown", reason: "CreatePending", message: "Waiting for contact group membership removal to be created", lastTransitionTime: "1970-01-01T00:00:00Z"}}
	// +kubebuilder:validation:Optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Username is the username of the user that owns the ContactGroupMembershipRemoval.
	// This is populated by the controller based on the referenced Contact's subject.
	// +optional
	Username string `json:"username,omitempty"`
}
