package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:subresource:status
// +kubebuilder:object:root=true

// ServiceAccount is the Schema for the service accounts API
// +kubebuilder:printcolumn:name="Email",type="string",JSONPath=".status.email"
// +kubebuilder:printcolumn:name="Description",type="string",JSONPath=".metadata.annotations['kubernetes\\.io/description']"
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".spec.state"
// +kubebuilder:printcolumn:name="Access Token Type",type="string",JSONPath=".spec.accessTokenType"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Project"
type ServiceAccount struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ServiceAccountSpec   `json:"spec,omitempty"`
	Status ServiceAccountStatus `json:"status,omitempty"`
}

// ServiceAccountSpec defines the desired state of ServiceAccount
type ServiceAccountSpec struct {
	// The state of the service account. This state can be safely changed as needed.
	// States:
	//   - Active: The service account can be used to authenticate.
	//   - Inactive: The service account is prohibited to be used to authenticate, and revokes all existing sessions.
	// +kubebuilder:validation:Enum=Active;Inactive
	// +kubebuilder:default=Active
	// +kubebuilder:validation:Optional
	State string `json:"state,omitempty"`
}

// ServiceAccountStatus defines the observed state of ServiceAccount
type ServiceAccountStatus struct {
	// The computed email of the service account following the pattern:
	// {metadata.name}@{metadata.namespace}.{project.metadata.name}.{global-suffix}
	Email string `json:"email,omitempty"`

	// State represents the current activation state of the service account from the auth provider.
	// This field tracks the state from the previous generation and is updated when state changes
	// are successfully propagated to the auth provider. It helps optimize performance by only
	// updating the auth provider when a state change is detected.
	// +kubebuilder:validation:Enum=Active;Inactive
	State string `json:"state,omitempty"`

	// Conditions provide conditions that represent the current status of the ServiceAccount.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// ServiceAccountList contains a list of ServiceAccount
type ServiceAccountList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ServiceAccount `json:"items"`
}
