package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// OrganizationDomain proves that an organization controls a domain name, such
// as the domain of its company's email addresses. It belongs to the
// organization rather than to one of its projects, so the domain doesn't have
// to be added to a project to be used.
//
// Milo publishes a DNS TXT record in status.verification.dnsRecord. Once the
// record is found at the domain, the Verified condition becomes True and stays
// True. The record can then be removed.
//
// The resource is named after its domain, so an organization has at most one
// for each domain. Verified domains, and their subdomains, can be used for
// email domain joining.
//
// Example:
//
//	apiVersion: resourcemanager.miloapis.com/v1alpha1
//	kind: OrganizationDomain
//	metadata:
//	  name: acme.com
//	  namespace: organization-acme-corp
//	spec:
//	  domainName: acme.com
//
// +k8s:openapi-gen=true
// +kubebuilder:printcolumn:name="Domain",type="string",JSONPath=".spec.domainName"
// +kubebuilder:printcolumn:name="Verified",type="string",JSONPath=".status.conditions[?(@.type=='Verified')].status"
// +kubebuilder:printcolumn:name="Record",type="string",JSONPath=".status.verification.dnsRecord.name",priority=1
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:resource:path=organizationdomains,scope=Namespaced,singular=organizationdomain
// +kubebuilder:selectablefield:JSONPath=".spec.domainName"
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Organization"
type OrganizationDomain struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec   OrganizationDomainSpec   `json:"spec"`
	Status OrganizationDomainStatus `json:"status,omitempty"`
}

// OrganizationDomainSpec defines the domain an organization wants to verify.
type OrganizationDomainSpec struct {
	// DomainName is the domain to verify, in lower case, such as acme.com or
	// eu.acme.com. It must match metadata.name and can't be changed.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=3
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)+$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="domainName is immutable"
	DomainName string `json:"domainName"`
}

// OrganizationDomainStatus reports how verification is going.
type OrganizationDomainStatus struct {
	// Verification holds the DNS record to add to the domain. It is cleared
	// once the domain is verified.
	//
	// +kubebuilder:validation:Optional
	Verification *OrganizationDomainVerification `json:"verification,omitempty"`

	// Conditions holds the Verified condition.
	//
	// +kubebuilder:default={{type: "Verified", status: "Unknown", reason: "Pending", message: "Waiting for the control plane to publish a verification record", lastTransitionTime: "1970-01-01T00:00:00Z"}}
	// +kubebuilder:validation:Optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// OrganizationDomainVerification describes how to prove control of a domain.
type OrganizationDomainVerification struct {
	// DNSRecord is the TXT record to add at the domain's DNS provider.
	DNSRecord OrganizationDomainDNSRecord `json:"dnsRecord"`

	// LastAttemptTime is when Milo last looked for the record.
	//
	// +kubebuilder:validation:Optional
	LastAttemptTime *metav1.Time `json:"lastAttemptTime,omitempty"`

	// NextAttemptTime is when Milo will look for the record again.
	//
	// +kubebuilder:validation:Optional
	NextAttemptTime *metav1.Time `json:"nextAttemptTime,omitempty"`
}

// OrganizationDomainDNSRecord is a DNS record that proves control of a domain.
type OrganizationDomainDNSRecord struct {
	// Name is the fully qualified record name, such as
	// _milo-verification.acme.com.
	Name string `json:"name"`

	// Type is the record type. It is always TXT.
	Type string `json:"type"`

	// Content is the value the record must hold.
	Content string `json:"content"`
}

const (
	// OrganizationDomainConditionVerified is True once Milo has found the
	// verification record at the domain.
	OrganizationDomainConditionVerified = "Verified"

	// OrganizationDomainReasonPending means the record hasn't been published
	// yet or hasn't been looked for.
	OrganizationDomainReasonPending = "Pending"
	// OrganizationDomainReasonRecordNotFound means the domain has no TXT
	// record at the record name yet.
	OrganizationDomainReasonRecordNotFound = "RecordNotFound"
	// OrganizationDomainReasonRecordMismatch means TXT records exist at the
	// record name, but none holds the expected content.
	OrganizationDomainReasonRecordMismatch = "RecordMismatch"
	// OrganizationDomainReasonLookupFailed means the DNS lookup failed for a
	// reason other than the record not existing.
	OrganizationDomainReasonLookupFailed = "LookupFailed"
	// OrganizationDomainReasonVerified means the record was found.
	OrganizationDomainReasonVerified = "Verified"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// OrganizationDomainList contains a list of OrganizationDomain.
type OrganizationDomainList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []OrganizationDomain `json:"items"`
}
