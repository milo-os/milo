package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Create conditions
const (
	// BroadcastReadyCondition is the condition Type that tracks Broadcast creation status.
	BroadcastReadyCondition = "Ready"
	// BroadcastCreatePendingReason is used when Broadcast creation is in progress.
	BroadcastCreatePendingReason = "CreatePending"
	// BroadcastCreatedReason is used when Broadcast creation succeeds.
	BroadcastCreatedReason = "CreateSuccessful"
)

// Delete conditions
const (
	// BroadcastDeletedCondition is the condition Type that tracks Broadcast deletion status.
	BroadcastDeletedCondition = "Delete"
	// BroadcastDeletePendingReason is used when Broadcast deletion is in progress.
	BroadcastDeletePendingReason = "DeletePending"
	// BroadcastDeletedReason is used when Broadcast deletion succeeds.
	BroadcastDeletedReason = "DeleteSuccessful"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// EmailBroadcast is the Schema for the emailbroadcasts API.
// It represents a broadcast of an email to a set of contacts (ContactGroup).
// If the broadcast needs to be updated, delete and recreate the resource.
// +kubebuilder:printcolumn:name="DisplayName",type="string",JSONPath=".spec.displayName"
// +kubebuilder:printcolumn:name="ContactGroup",type="string",JSONPath=".spec.contactGroupRef.name"
// +kubebuilder:printcolumn:name="Template",type="string",JSONPath=".spec.templateRef.name"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Platform,User"
type EmailBroadcast struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   EmailBroadcastSpec   `json:"spec,omitempty"`
	Status EmailBroadcastStatus `json:"status,omitempty"`
}

// EmailBroadcastSpec defines the desired state of EmailBroadcast.
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable"
// +kubebuilder:validation:Type=object
type EmailBroadcastSpec struct {
	// DisplayName is the display name of the email broadcast.
	// +kubebuilder:validation:Optional
	DisplayName string `json:"displayName,omitempty"`

	// ContactGroupRef is a reference to the ContactGroup that the email broadcast is for.
	// +kubebuilder:validation:Required
	ContactGroupRef ContactGroupReference `json:"contactGroupRef"`

	// TemplateRef references the EmailTemplate to render the broadcast message.
	// When using the Resend provider you can include the following placeholders
	// in HTMLBody or TextBody; they will be substituted by the provider at send time:
	//   {{{FIRST_NAME}}} {{{LAST_NAME}}} {{{EMAIL}}}
	// +kubebuilder:validation:Required
	TemplateRef TemplateReference `json:"templateRef"`

	// ScheduledAt optionally specifies the time at which the broadcast should be executed.
	// If omitted, the message is sent as soon as the controller reconciles the resource.
	// Example: "2024-08-05T11:52:01.858Z"
	// +kubebuilder:validation:Optional
	ScheduledAt *metav1.Time `json:"scheduledAt,omitempty"`
}

// +kubebuilder:object:root=true

// EmailBroadcastList contains a list of EmailBroadcast.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type EmailBroadcastList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EmailBroadcast `json:"items"`
}

type EmailBroadcastStatus struct {
	// Conditions represent the latest available observations of an object's current state.
	// Standard condition is "Ready" which tracks email broadcast status and sync to the email broadcast provider.
	// +kubebuilder:default={{type: "Ready", status: "Unknown", reason: "CreatePending", message: "Waiting for email broadcast to be created", lastTransitionTime: "1970-01-01T00:00:00Z"}}
	// +kubebuilder:validation:Optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ProviderID is the identifier returned by the underlying email broadcast provider
	// (e.g. Resend) when the email broadcast is created. It is usually
	// used to track the email broadcast creation status (e.g. provider webhooks).
	// +optional
	ProviderID string `json:"providerID,omitempty"`
}
