package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Role is the Schema for the roles API
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Display Name",type="string",JSONPath=".spec.displayName"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Launch Stage",type="string",JSONPath=".spec.launchStage"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Organization,Platform"
type Role struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec RoleSpec `json:"spec,omitempty"`

	// +kubebuilder:default={conditions: {{type: "Ready", status: "Unknown", reason: "Unknown", message: "Waiting for control plane to reconcile", lastTransitionTime: "1970-01-01T00:00:00Z"}}}
	Status RoleStatus `json:"status,omitempty"`
}

// RoleSpec defines the desired state of Role
type RoleSpec struct {
	// The names of the permissions this role grants when bound in an IAM policy.
	// All permissions must be in the format: `{service}.{resource}.{action}`
	// (e.g. compute.workloads.create).
	// +kubebuilder:validation:Optional
	IncludedPermissions []string `json:"includedPermissions,omitempty"`

	// Defines the launch stage of the IAM Role. Must be one of: Early Access,
	// Alpha, Beta, Stable, Deprecated.
	// +kubebuilder:validation:Required
	LaunchStage string `json:"launchStage"`

	// The list of roles from which this role inherits permissions.
	// Each entry must be a valid role resource name.
	// +kubebuilder:validation:Optional
	// +listType=map
	// +listMapKey=name
	InheritedRoles []ScopedRoleReference `json:"inheritedRoles,omitempty"`
}

// RoleStatus defines the observed state of Role
type RoleStatus struct {
	// The resource name of the parent the role was created under.
	// +kubebuilder:validation:Optional
	Parent string `json:"parent,omitempty"`

	// EffectivePermissions is the complete flattened list of all permissions
	// granted by this role, including permissions from inheritedRoles and
	// directly specified includedPermissions. This is computed by the controller
	// and provides a single source of truth for all permissions this role grants.
	// +kubebuilder:validation:Optional
	EffectivePermissions []string `json:"effectivePermissions,omitempty"`

	// Conditions provide conditions that represent the current status of the Role.
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// ObservedGeneration is the most recent generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// RoleList contains a list of Role
type RoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Role `json:"items"`
}
