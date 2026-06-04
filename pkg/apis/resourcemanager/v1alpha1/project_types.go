package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ProjectSpec defines the desired state of Project.
type ProjectSpec struct {
	// OwnerRef is a reference to the owner of the project. Must be a valid
	// resource.
	// +kubebuilder:validation:Required
	OwnerRef OwnerReference `json:"ownerRef"`
}

// ProjectStatus defines the observed state of Project.
type ProjectStatus struct {
	// Represents the observations of a project's current state.
	// Known condition types are: "Ready"
	// +kubebuilder:default={{type: "Ready", status: "Unknown", reason: "Unknown", message: "Waiting for control plane to reconcile", lastTransitionTime: "1970-01-01T00:00:00Z"}}
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

const (
	// ProjectReady indicates that the project has been provisioned and is ready
	// for use.
	ProjectReady = "Ready"

	// ProjectResourceCleanup indicates that project resources are being deleted
	// as part of project teardown.
	ProjectResourceCleanup = "ResourceCleanup"
)

const (
	// ProjectReadyReason indicates that the project is ready for use.
	ProjectReadyReason = "Ready"

	// ProjectProvisioningReason indicates that the project is provisioning.
	ProjectProvisioningReason = "Provisioning"

	// ProjectNameConflict indicates that the project name already exists
	ProjectNameConflictReason = "ProjectNameConflict"

	// ProjectCleanupStartedReason indicates that resource cleanup has been
	// initiated and delete commands are being issued.
	ProjectCleanupStartedReason = "CleanupStarted"

	// ProjectCleanupAwaitingCompletionReason indicates that delete commands
	// have been issued and the controller is waiting for resources to be removed.
	ProjectCleanupAwaitingCompletionReason = "CleanupAwaitingCompletion"

	// ProjectCleanupCompleteReason indicates that all project resources have
	// been deleted.
	ProjectCleanupCompleteReason = "CleanupComplete"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// Project is the Schema for the projects API.
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Organization"
type Project struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec   ProjectSpec   `json:"spec,omitempty"`
	Status ProjectStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ProjectList contains a list of Project.
type ProjectList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Project `json:"items"`
}

// OwnerReference is a reference to the owner of the project.
type OwnerReference struct {
	// Kind is the kind of the resource.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Organization
	Kind string `json:"kind"`

	// Name is the name of the resource.
	//
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}
