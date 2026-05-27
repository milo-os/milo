package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// LocationBindingSpec defines the desired state of a LocationBinding.
type LocationBindingSpec struct {
	// LocationRef references the canonical cluster-scoped Location object.
	//
	// +kubebuilder:validation:Required
	LocationRef LocalObjectReference `json:"locationRef"`

	// LocationClassName mirrors spec.locationClassName from the referenced Location.
	//
	// +kubebuilder:validation:Required
	LocationClassName LocationClassName `json:"locationClassName"`

	// DisplayName mirrors spec.displayName from the referenced Location.
	//
	// +kubebuilder:validation:Required
	DisplayName string `json:"displayName"`

	// City mirrors spec.city from the referenced Location.
	//
	// +optional
	City string `json:"city,omitempty"`

	// Region mirrors spec.region from the referenced Location.
	//
	// +optional
	Region string `json:"region,omitempty"`
}

// LocationBindingStatus defines the observed state of a LocationBinding.
type LocationBindingStatus struct {
	// Conditions represent the latest available observations of the binding's state.
	//
	// +optional
	// +listType=map
	// +listMapKey=type
	// +patchStrategy=merge
	// +patchMergeKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// LocationBinding is a namespace-scoped projection of a Location into a project's
// namespace. It is the consumer-facing answer to "which locations does my project
// have access to?" and mirrors the relevant fields of the Location it references.
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Location",type="string",JSONPath=".spec.locationRef.name"
// +kubebuilder:printcolumn:name="Class",type="string",JSONPath=".spec.locationClassName"
// +kubebuilder:printcolumn:name="Region",type="string",JSONPath=".spec.region"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type LocationBinding struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec   LocationBindingSpec   `json:"spec"`
	Status LocationBindingStatus `json:"status,omitempty"`
}

// LocationBindingList contains a list of LocationBinding.
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
type LocationBindingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LocationBinding `json:"items"`
}
