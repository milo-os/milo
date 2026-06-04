// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ProjectControlPlaneSpec defines the desired state of ProjectControlPlane.
type ProjectControlPlaneSpec struct {
}

// ProjectControlPlaneStatus defines the observed state of ProjectControlPlane.
type ProjectControlPlaneStatus struct {
	// Represents the observations of a project control plane's current state.
	// Known condition types are: "Ready"
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

const (
	// ProjectControlPlaneReady indicates that the project control plane has been
	// provisioned and is ready for use.
	ProjectControlPlaneReady = "ControlPlaneReady"
)

const (
	// ProjectControlPlaneReadyReason indicates that the project's control plane
	// is ready for use.
	ProjectControlPlaneReadyReason = "Ready"

	// ProjectControlPlaneCreatingReason indicates that the project's control plane
	// is being created.
	ProjectControlPlaneCreatingReason = "Creating"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// ProjectControlPlane is the Schema for the projectcontrolplanes API.
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Platform"
type ProjectControlPlane struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec ProjectControlPlaneSpec `json:"spec,omitempty"`

	// +kubebuilder:default={conditions:{{type:"ControlPlaneReady",status:"False",reason:"Creating",message:"Creating a new control plane for the project", lastTransitionTime: "1970-01-01T00:00:00Z"}}}
	Status ProjectControlPlaneStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ProjectControlPlaneList contains a list of ProjectControlPlane.
type ProjectControlPlaneList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ProjectControlPlane `json:"items"`
}
