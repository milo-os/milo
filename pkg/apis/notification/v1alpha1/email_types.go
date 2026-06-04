package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Condition and reason constants for Email
const (
	// EmailDeliveredCondition is the condition Type that tracks email delivery status.
	EmailDeliveredCondition = "Delivered"
	// EmailDeliveredReason is used when email delivery succeeds.
	EmailDeliveredReason = "DeliverySuccessful"
	// EmailDeliveryFailedReason is used when email delivery fails.
	EmailDeliveryFailedReason = "DeliveryFailed"
	// EmailDeliveryPendingReason is used when email delivery is in progress.
	EmailDeliveryPendingReason = "DeliveryPending"
)

// EmailPriority defines the priority for sending an Email.
// +kubebuilder:validation:Enum=low;normal;high
// +kubebuilder:default=normal
type EmailPriority string

const (
	EmailPriorityLow    EmailPriority = "low"
	EmailPriorityNormal EmailPriority = "normal"
	EmailPriorityHigh   EmailPriority = "high"
)

// TemplateReference contains information that points to the EmailTemplate being used.
// EmailTemplate is a cluster-scoped resource, so Namespace is not required.
// +kubebuilder:validation:Type=object
type TemplateReference struct {
	// Name is the name of the EmailTemplate being referenced.
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

// EmailUserReference contains information about the recipient User resource.
// Users are cluster-scoped resources, hence Namespace is not included.
// +kubebuilder:validation:Type=object
type EmailUserReference struct {
	// Name contain the name of the User resource that will receive the email.
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

// EmailVariable represents a name/value pair that will be injected into the template.
// +kubebuilder:validation:Type=object
type EmailVariable struct {
	// Name of the variable as declared in the associated EmailTemplate.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Value provided for this variable.
	// +kubebuilder:validation:Required
	Value string `json:"value"`
}

// EmailRecipient contains information about the recipient of the email.
// +kubebuilder:validation:Type=object
type EmailRecipient struct {
	// UserRef references the User resource that will receive the message.
	// It is mutually exclusive with EmailAddress: exactly one of them must be specified.
	// +kubebuilder:validation:Optional
	UserRef EmailUserReference `json:"userRef,omitempty"`

	// EmailAddress allows specifying a literal e-mail address for the recipient instead of referencing a User resource.
	// It is mutually exclusive with UserRef: exactly one of them must be specified.
	// +kubebuilder:validation:Optional
	EmailAddress string `json:"emailAddress,omitempty"`
}

// EmailSpec defines the desired state of Email.
// It references a template, recipients, and any variables required to render the final message.
// +kubebuilder:validation:Type=object
type EmailSpec struct {
	// TemplateRef references the EmailTemplate that should be rendered.
	// +kubebuilder:validation:Required
	TemplateRef TemplateReference `json:"templateRef"`

	// Recipient contain the recipient of the email.
	// +kubebuilder:validation:Required
	Recipient EmailRecipient `json:"recipient"`

	// CC contains additional e-mail addresses that will receive a carbon copy of the message.
	// Maximum 10 addresses.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=10
	CC []string `json:"cc,omitempty"`

	// BCC contains e-mail addresses that will receive a blind-carbon copy of the message.
	// Maximum 10 addresses.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=10
	BCC []string `json:"bcc,omitempty"`

	// Variables supplies the values that will be substituted in the template.
	// +kubebuilder:validation:Optional
	Variables []EmailVariable `json:"variables,omitempty"`

	// Priority influences the order in which pending e-mails are processed.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=normal
	Priority EmailPriority `json:"priority,omitempty"`
}

// EmailStatus captures the observed state of an Email.
// Uses standard Kubernetes conditions to track both processing and delivery state.
// +kubebuilder:validation:Type=object
type EmailStatus struct {
	// Conditions represent the latest available observations of an object's current state.
	// Standard condition is "Delivered" which tracks email delivery status.
	// +kubebuilder:default={{type: "Delivered", status: "Unknown", reason: "DeliveryPending", message: "Waiting for email delivery", lastTransitionTime: "1970-01-01T00:00:00Z"}}
	// +kubebuilder:validation:Optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ProviderID is the identifier returned by the underlying email provider
	// (e.g. Resend) when the e-mail is accepted for delivery. It is usually
	// used to track the email delivery status (e.g. provider webhooks).
	// +optional
	ProviderID string `json:"providerID,omitempty"`

	// HTMLBody stores the rendered HTML content of the e-mail.
	// +optional
	HTMLBody string `json:"htmlBody,omitempty"`

	// TextBody stores the rendered plain-text content of the e-mail.
	// +optional
	TextBody string `json:"textBody,omitempty"`

	// Subject stores the subject line used for the e-mail.
	// +optional
	Subject string `json:"subject,omitempty"`

	// EmailAddress stores the final recipient address used for delivery,
	// after resolving any referenced User.
	// +optional
	EmailAddress string `json:"emailAddress,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Email is the Schema for the emails API.
// It represents a concrete e-mail that should be sent to the referenced users.
// For idempotency purposes, controllers can use metadata.uid as a unique identifier
// to prevent duplicate email delivery, since it's guaranteed to be unique per resource instance.
// +kubebuilder:printcolumn:name="Template",type="string",JSONPath=".spec.templateRef.name"
// +kubebuilder:printcolumn:name="Priority",type="string",JSONPath=".spec.priority"
// +kubebuilder:printcolumn:name="Delivered",type="string",JSONPath=".status.conditions[?(@.type=='Delivered')].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:selectablefield:JSONPath=".spec.recipient.userRef.name"
// +kubebuilder:selectablefield:JSONPath=".spec.recipient.emailAddress"
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Platform,User"
type Email struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   EmailSpec   `json:"spec,omitempty"`
	Status EmailStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// EmailList contains a list of Email.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type EmailList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Email `json:"items"`
}
