package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// LocationSpec defines the desired state of a Location.
type LocationSpec struct {
	// LocationClassName categorizes the location's ownership model.
	//
	// +kubebuilder:validation:Required
	LocationClassName LocationClassName `json:"locationClassName"`

	// DisplayName is a human-readable label for the location.
	//
	// +kubebuilder:validation:Required
	DisplayName string `json:"displayName"`

	// City is the three-letter IATA city code (e.g. "ORD", "LHR").
	//
	// +optional
	City string `json:"city,omitempty"`

	// Region is the region this location belongs to (e.g. "us-central1").
	//
	// +optional
	Region string `json:"region,omitempty"`

	// OwnerProjectRef is set for provider-dedicated and self-managed locations.
	// It references the project that owns and manages this location.
	//
	// +optional
	OwnerProjectRef *LocalObjectReference `json:"ownerProjectRef,omitempty"`
}

// LocationStatus defines the observed state of a Location.
type LocationStatus struct {
	// Conditions represent the latest available observations of the location's state.
	//
	// +optional
	// +listType=map
	// +listMapKey=type
	// +patchStrategy=merge
	// +patchMergeKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// Location represents a physical point-of-presence (PoP) where platform services
// are deployed and reachable by consumer workloads.
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Class",type="string",JSONPath=".spec.locationClassName"
// +kubebuilder:printcolumn:name="Region",type="string",JSONPath=".spec.region"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type Location struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec   LocationSpec   `json:"spec"`
	Status LocationStatus `json:"status,omitempty"`
}

// LocationList contains a list of Location.
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
type LocationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Location `json:"items"`
}
