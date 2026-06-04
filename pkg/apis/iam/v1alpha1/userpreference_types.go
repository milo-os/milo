package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// UserPreference is the Schema for the userpreferences API
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="User",type="string",JSONPath=".spec.userRef.name"
// +kubebuilder:printcolumn:name="Theme",type="string",JSONPath=".spec.theme"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:resource:path=userpreferences,scope=Cluster
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=User"
type UserPreference struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   UserPreferenceSpec   `json:"spec,omitempty"`
	Status UserPreferenceStatus `json:"status,omitempty"`
}

// UserPreferenceSpec defines the desired state of UserPreference
type UserPreferenceSpec struct {
	// Reference to the user these preferences belong to.
	// +kubebuilder:validation:Required
	UserRef UserReference `json:"userRef"`

	// The user's theme preference.
	// +kubebuilder:validation:Enum=light;dark;system
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=system
	Theme string `json:"theme,omitempty"`
}

// UserPreferenceStatus defines the observed state of UserPreference
type UserPreferenceStatus struct {
	// Conditions provide conditions that represent the current status of the UserPreference.
	// +kubebuilder:default={{type: "Ready", status: "Unknown", reason: "Unknown", message: "Waiting for control plane to reconcile", lastTransitionTime: "1970-01-01T00:00:00Z"}}
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// UserPreferenceList contains a list of UserPreference
type UserPreferenceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []UserPreference `json:"items"`
}
