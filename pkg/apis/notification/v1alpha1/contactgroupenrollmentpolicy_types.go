package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// EnrollmentTriggerContactCreated triggers enrollment when a Contact is created.
	EnrollmentTriggerContactCreated = "ContactCreated"

	// EnrollmentPolicyAnnotationPrefix is the annotation key prefix used on Contact resources
	// to record which ContactGroupEnrollmentPolicy resources have already been evaluated.
	// The full annotation key is: {EnrollmentPolicyAnnotationPrefix}/{policyName}
	// The value is always "true".
	EnrollmentPolicyAnnotationPrefix = "enrollment.notification.miloapis.com"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// ContactGroupEnrollmentPolicy defines which ContactGroup a new Contact is automatically
// enrolled in when a trigger condition is met.
// +kubebuilder:printcolumn:name="ContactGroup",type="string",JSONPath=".spec.contactGroupRef.name"
// +kubebuilder:printcolumn:name="Trigger",type="string",JSONPath=".spec.trigger.type"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:resource:scope=Cluster
type ContactGroupEnrollmentPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec ContactGroupEnrollmentPolicySpec `json:"spec,omitempty"`
}

// ContactGroupEnrollmentPolicySpec defines the desired enrollment behavior.
// +kubebuilder:validation:Type=object
type ContactGroupEnrollmentPolicySpec struct {
	// ContactGroupRef references the ContactGroup that matching Contacts are enrolled in.
	// +kubebuilder:validation:Required
	ContactGroupRef EnrollmentContactGroupRef `json:"contactGroupRef"`

	// Trigger defines when enrollment happens.
	// +kubebuilder:validation:Required
	Trigger EnrollmentTrigger `json:"trigger"`

	// ContactSelector filters which Contacts this policy applies to.
	// If omitted, the policy applies to all Contacts.
	// +kubebuilder:validation:Optional
	ContactSelector *EnrollmentContactSelector `json:"contactSelector,omitempty"`
}

// EnrollmentContactGroupRef references a ContactGroup by name and namespace.
type EnrollmentContactGroupRef struct {
	// Name is the name of the ContactGroup.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Namespace is the namespace of the ContactGroup.
	// +kubebuilder:validation:Required
	Namespace string `json:"namespace"`
}

// EnrollmentTrigger defines the event that activates an enrollment policy.
// +kubebuilder:validation:Type=object
type EnrollmentTrigger struct {
	// Type is the event that triggers enrollment.
	// ContactCreated fires when a new Contact resource is created.
	// +kubebuilder:validation:Enum=ContactCreated
	// +kubebuilder:validation:Required
	Type string `json:"type"`
}

// EnrollmentContactSelector filters which Contacts a policy applies to.
// +kubebuilder:validation:Type=object
type EnrollmentContactSelector struct {
	// SubjectKind restricts enrollment to Contacts whose SubjectRef.Kind matches this value.
	// +kubebuilder:validation:Enum=User
	// +kubebuilder:validation:Optional
	SubjectKind string `json:"subjectKind,omitempty"`
}

// +kubebuilder:object:root=true

// ContactGroupEnrollmentPolicyList contains a list of ContactGroupEnrollmentPolicy.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ContactGroupEnrollmentPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ContactGroupEnrollmentPolicy `json:"items"`
}
