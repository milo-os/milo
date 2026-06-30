// +kubebuilder:object:generate=true
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// OrganizationTypePersonal is the legacy personal organization type.
	OrganizationTypePersonal = "Personal"
	// OrganizationTypeStandard is the legacy standard organization type.
	OrganizationTypeStandard = "Standard"

	// OrganizationConditionOnboardingComplete indicates whether the organization
	// has completed onboarding (contact info, billing account, payment method).
	OrganizationConditionOnboardingComplete = "OnboardingComplete"

	OrganizationOnboardingCompleteReasonReady                 = "Ready"
	OrganizationOnboardingCompleteReasonContactInfoIncomplete = "ContactInfoIncomplete"
	OrganizationOnboardingCompleteReasonBillingAccountMissing = "BillingAccountMissing"
	OrganizationOnboardingCompleteReasonPaymentMethodNotReady = "PaymentMethodNotReady"
)

// OrganizationSpec defines the desired state of Organization
// +k8s:protobuf=true
type OrganizationSpec struct {
	// Type distinguishes personal and standard organizations in legacy mode.
	//
	// Deprecated: This field is ignored when the UnifiedOrganizations feature
	// gate is enabled. Use unified organizations without a type distinction.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=Personal;Standard
	// +kubebuilder:validation:XValidation:rule="type(oldSelf) == null_type || self == oldSelf",message="organization type is immutable"
	Type string `json:"type,omitempty"`

	// ContactInfo describes who the organization is and how to reach them.
	// Email and name are required for onboarding to complete.
	//
	// +kubebuilder:validation:Optional
	ContactInfo *OrganizationContactInfo `json:"contactInfo,omitempty"`
}

// OrganizationContactInfo defines tenancy-level contact details for an
// organization. This is separate from billing account contact information.
// +k8s:protobuf=true
type OrganizationContactInfo struct {
	// Email is the primary contact email for the organization.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	Email string `json:"email"`

	// Name is the display name of the primary contact.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	Name string `json:"name"`

	// BusinessName is the optional legal entity or company name.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=256
	BusinessName string `json:"businessName,omitempty"`

	// Address is the optional postal address for the organization.
	//
	// +kubebuilder:validation:Optional
	Address *OrganizationAddress `json:"address,omitempty"`
}

// OrganizationAddress is a postal address for an organization contact.
// +k8s:protobuf=true
type OrganizationAddress struct {
	// Country is the ISO 3166-1 alpha-2 country code (e.g. "GB", "US").
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[A-Z]{2}$`
	Country string `json:"country"`

	// Line1 is the first line of the street address.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=256
	Line1 string `json:"line1,omitempty"`

	// Line2 is the second line of the street address.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=256
	Line2 string `json:"line2,omitempty"`

	// City is the locality.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=128
	City string `json:"city,omitempty"`

	// Region is the state, province, or county.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=128
	Region string `json:"region,omitempty"`

	// PostalCode is the post or zip code.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=32
	PostalCode string `json:"postalCode,omitempty"`
}

// OrganizationStatus defines the observed state of Organization
// +k8s:protobuf=true
type OrganizationStatus struct {
	// ObservedGeneration is the most recent generation observed for this Organization by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions represents the observations of an organization's current state.
	// Known condition types are: "Ready", "OnboardingComplete"
	// +kubebuilder:default={{type: "Ready", status: "Unknown", reason: "Unknown", message: "Waiting for control plane to reconcile", lastTransitionTime: "1970-01-01T00:00:00Z"},{type: "OnboardingComplete", status: "False", reason: "ContactInfoIncomplete", message: "Organization contact information is incomplete", lastTransitionTime: "1970-01-01T00:00:00Z"}}
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey="type"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +k8s:protobuf=true

// +kubebuilder:subresource:status
// Use lowercase for path, which influences plural name. Ensure kind is Organization.
// +kubebuilder:resource:path=organizations,scope=Cluster,categories=datum,singular=organization
// +kubebuilder:printcolumn:name="Display Name",type="string",JSONPath=".metadata.annotations.kubernetes\\.io\\/display-name"
// +kubebuilder:printcolumn:name="Type",type="string",JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="Onboarding",type="string",JSONPath=".status.conditions[?(@.type=='OnboardingComplete')].status"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
// Organization is the Schema for the Organizations API
// +kubebuilder:object:root=true
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Platform"
type Organization struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   OrganizationSpec   `json:"spec,omitempty"`
	Status OrganizationStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +k8s:protobuf=true

// +kubebuilder:object:root=true
// OrganizationList contains a list of Organization
type OrganizationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Organization `json:"items"`
}
