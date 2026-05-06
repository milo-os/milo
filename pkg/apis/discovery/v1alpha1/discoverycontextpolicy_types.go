// +kubebuilder:object:generate=true
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DiscoveryContextPolicySpec defines the desired state of DiscoveryContextPolicy
type DiscoveryContextPolicySpec struct {
	// Rules define which resources are visible in which parent contexts.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Rules []DiscoveryContextPolicyRule `json:"rules"`
}

// DiscoveryContextPolicyRule defines context visibility for a set of resources in a group.
type DiscoveryContextPolicyRule struct {
	// Group is the API group. Empty string means the core group. Use "*" to match all groups.
	// +kubebuilder:validation:Required
	Group string `json:"group"`

	// Resources is the list of resource plural names. Use ["*"] to match all resources in the group.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Resources []string `json:"resources"`

	// Contexts lists the parent contexts where these resources are visible.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +listType=set
	Contexts []string `json:"contexts"`
}

// DiscoveryContextPolicyStatus defines the observed state of DiscoveryContextPolicy
type DiscoveryContextPolicyStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=dcp,categories=milo
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Platform"

// DiscoveryContextPolicy defines the parent contexts in which API resources are visible
// in discovery responses.
type DiscoveryContextPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              DiscoveryContextPolicySpec   `json:"spec,omitempty"`
	Status            DiscoveryContextPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DiscoveryContextPolicyList contains a list of DiscoveryContextPolicy
type DiscoveryContextPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DiscoveryContextPolicy `json:"items"`
}
