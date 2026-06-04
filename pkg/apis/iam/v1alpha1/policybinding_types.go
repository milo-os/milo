package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RoleReference contains information that points to the Role being used
// +k8s:deepcopy-gen=true
type RoleReference struct {
	// Name is the name of resource being referenced
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// Namespace of the referenced Role. If empty, it is assumed to be in the PolicyBinding's namespace.
	// +kubebuilder:validation:Optional
	Namespace string `json:"namespace,omitempty"`
}

// Subject contains a reference to the object or user identities a role binding applies to.
// This can be a User, Group, or ServiceAccount.
// +k8s:deepcopy-gen=true
// +kubebuilder:validation:XValidation:rule="(self.kind == 'Group' && has(self.name) && self.name.startsWith('system:')) || (has(self.uid) && size(self.uid) > 0)",message="UID is required for all subjects except system groups (groups with names starting with 'system:')"
type Subject struct {
	// Kind of object being referenced. Values defined in Kind constants.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=User;Group;ServiceAccount
	Kind string `json:"kind"`
	// Name of the object being referenced. A special group name of
	// "system:authenticated-users" can be used to refer to all authenticated
	// users.
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// Namespace of the referenced object.
	// If not specified for a Group, User or ServiceAccount, it is ignored.
	// +kubebuilder:validation:Optional
	Namespace string `json:"namespace,omitempty"`
	// UID of the referenced object. Optional for system groups (groups with names starting with "system:").
	// +kubebuilder:validation:Optional
	UID string `json:"uid,omitempty"`
}

// ResourceReference contains enough information to let you identify a specific
// API resource instance.
// +k8s:deepcopy-gen=true
type ResourceReference struct {
	// APIGroup is the group for the resource being referenced.
	// If APIGroup is not specified, the specified Kind must be in the core API group.
	// For any other third-party types, APIGroup is required.
	// +kubebuilder:validation:Optional
	APIGroup string `json:"apiGroup,omitempty"`
	// Kind is the type of resource being referenced.
	// +kubebuilder:validation:Required
	Kind string `json:"kind"`
	// Name is the name of resource being referenced.
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// UID is the unique identifier of the resource being referenced.
	// +kubebuilder:validation:Required
	UID string `json:"uid"`
	// Namespace is the namespace of resource being referenced.
	// Required for namespace-scoped resources. Omitted for cluster-scoped resources.
	// +kubebuilder:validation:Optional
	Namespace string `json:"namespace,omitempty"`
}

// ResourceKind contains enough information to identify a resource type.
// +k8s:deepcopy-gen=true
type ResourceKind struct {
	// APIGroup is the group for the resource type being referenced. If APIGroup
	// is not specified, the specified Kind must be in the core API group.
	// +kubebuilder:validation:Optional
	APIGroup string `json:"apiGroup,omitempty"`

	// Kind is the type of resource being referenced.
	// +kubebuilder:validation:Required
	Kind string `json:"kind"`
}

// ResourceSelector defines which resources the policy binding applies to.
// Either resourceRef or resourceKind must be specified, but not both.
// +k8s:deepcopy-gen=true
// +kubebuilder:validation:XValidation:rule="has(self.resourceRef) != has(self.resourceKind)",message="exactly one of resourceRef or resourceKind must be specified, but not both"
type ResourceSelector struct {
	// ResourceRef provides a reference to a specific resource instance.
	// Mutually exclusive with resourceKind.
	// +kubebuilder:validation:Optional
	ResourceRef *ResourceReference `json:"resourceRef,omitempty"`

	// ResourceKind specifies that the policy binding should apply to all resources of a specific kind.
	// Mutually exclusive with resourceRef.
	// +kubebuilder:validation:Optional
	ResourceKind *ResourceKind `json:"resourceKind,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// PolicyBinding is the Schema for the policybindings API
// +kubebuilder:printcolumn:name="Role",type="string",JSONPath=".spec.roleRef.name"
// +kubebuilder:printcolumn:name="Resource Kind",type="string",JSONPath=".spec.resourceSelector.resourceRef.kind"
// +kubebuilder:printcolumn:name="Resource Name",type="string",JSONPath=".spec.resourceSelector.resourceRef.name"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:resource:path=policybindings,scope=Namespaced
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Organization,Platform"
type PolicyBinding struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Spec   PolicyBindingSpec   `json:"spec,omitempty" protobuf:"bytes,2,opt,name=spec"`
	Status PolicyBindingStatus `json:"status,omitempty" protobuf:"bytes,3,opt,name=status"`
}

// PolicyBindingSpec defines the desired state of PolicyBinding
// +k8s:deepcopy-gen=true
type PolicyBindingSpec struct {
	// RoleRef is a reference to the Role that is being bound.
	// This can be a reference to a Role custom resource.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="oldSelf == null || self == oldSelf",message="RoleRef is immutable and cannot be changed after creation"
	RoleRef RoleReference `json:"roleRef"`

	// Subjects holds references to the objects the role applies to.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Subjects []Subject `json:"subjects"`

	// ResourceSelector defines which resources the subjects in the policy binding
	// should have the role applied to. Options within this struct are mutually
	// exclusive.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="oldSelf == null || self == oldSelf",message="ResourceSelector is immutable and cannot be changed after creation"
	ResourceSelector ResourceSelector `json:"resourceSelector"`
}

// PolicyBindingStatus defines the observed state of PolicyBinding
// +k8s:deepcopy-gen=true
type PolicyBindingStatus struct {
	// ObservedGeneration is the most recent generation observed for this PolicyBinding by the controller.
	// +kubebuilder:validation:Optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions provide conditions that represent the current status of the PolicyBinding.
	// +kubebuilder:default={{type: "Ready", status: "Unknown", reason: "Unknown", message: "Waiting for control plane to reconcile", lastTransitionTime: "1970-01-01T00:00:00Z"}}
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// PolicyBindingList contains a list of PolicyBinding
type PolicyBindingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PolicyBinding `json:"items"`
}
