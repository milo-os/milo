package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Condition and reason constants for EmailTemplate
const (
	// EmailTemplateReadyCondition is set to True when the template has been processed.
	EmailTemplateReadyCondition = "Ready"
	// EmailTemplateReadyReason is the typical reason used when reconciliation succeeds.
	EmailTemplateReadyReason = "Reconciled"
)

// EmailTemplateVariableType defines the set of supported variable kinds.
// +kubebuilder:validation:Enum=string;url
// +kubebuilder:default=string
type EmailTemplateVariableType string

const (
	EmailTemplateVariableTypeString EmailTemplateVariableType = "string"
	EmailTemplateVariableTypeURL    EmailTemplateVariableType = "url"
)

// TemplateVariable declares a variable that can be referenced in the template body or subject.
// Each variable must be listed here so that callers know which parameters are expected.
// +kubebuilder:validation:Required
// +kubebuilder:validation:Type=object
type TemplateVariable struct {
	// Name is the identifier of the variable as it appears inside the Go template (e.g. {{.UserName}}).
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Required indicates whether the variable must be provided when rendering the template.
	// +kubebuilder:validation:Required
	Required bool `json:"required"`

	// Type provides a hint about the expected value of this variable (e.g. plain string or URL).
	// +kubebuilder:validation:Required
	Type EmailTemplateVariableType `json:"type"`
}

// EmailTemplateSpec defines the desired state of EmailTemplate.
// It contains the subject, content, and declared variables.
type EmailTemplateSpec struct {
	// Subject is the string that composes the email subject line.
	// +kubebuilder:validation:Required
	Subject string `json:"subject"`

	// HTMLBody is the string for the HTML representation of the message.
	// +kubebuilder:validation:Required
	HTMLBody string `json:"htmlBody,omitempty"`

	// TextBody is the Go template string for the plain-text representation of the message.
	// +kubebuilder:validation:Required
	TextBody string `json:"textBody,omitempty"`

	// Variables enumerates all variables that can be referenced inside the template expressions.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=100
	Variables []TemplateVariable `json:"variables,omitempty"`
}

// EmailTemplateStatus captures the observed state of an EmailTemplate.
// Right now we only expose standard Kubernetes conditions so callers can
// determine whether the template is ready for use.
type EmailTemplateStatus struct {
	// Conditions represent the latest available observations of an object's current state.
	// +kubebuilder:default={{type: "Ready", status: "Unknown", reason: "Unknown", message: "Waiting for control plane to reconcile", lastTransitionTime: "1970-01-01T00:00:00Z"}}
	// +kubebuilder:validation:Optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// EmailTemplate is the Schema for the email templates API.
// It represents a reusable e-mail template that can be rendered by substituting
// the declared variables.
// +kubebuilder:printcolumn:name="Subject",type="string",JSONPath=".spec.subject"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Platform"
type EmailTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   EmailTemplateSpec   `json:"spec,omitempty"`
	Status EmailTemplateStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// EmailTemplateList contains a list of EmailTemplate.
type EmailTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EmailTemplate `json:"items"`
}
