package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// WorkloadIdentityIssuer registers, in one project, an issuer whose tokens workloads may present. A
// controller verifies the issuer's signing keys and publishes the usable ones in status; consumers read
// status only.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Project"
// +kubebuilder:printcolumn:name="Issuer",type="string",JSONPath=".status.issuer"
// +kubebuilder:printcolumn:name="Disabled",type="boolean",JSONPath=".spec.disabled"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Domain Verified",type="string",JSONPath=".status.conditions[?(@.type=='DomainVerified')].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type WorkloadIdentityIssuer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WorkloadIdentityIssuerSpec   `json:"spec"`
	Status WorkloadIdentityIssuerStatus `json:"status,omitempty"`
}

// WorkloadIdentityIssuerSpec registers an OpenID Connect issuer.
type WorkloadIdentityIssuerSpec struct {
	// OIDC is the OpenID Connect issuer and where its signing keys come from.
	// +required
	OIDC *OIDCIssuer `json:"oidc"`

	// RequiredClaims are claims a token must carry with exactly these string values.
	// +optional
	// +kubebuilder:validation:MaxProperties=16
	// +kubebuilder:validation:XValidation:rule="self.all(k, size(k) >= 1 && size(k) <= 128)",message="claim names must be 1 to 128 characters"
	// +kubebuilder:validation:XValidation:rule="self.all(k, !(k in ['iss', 'aud', 'exp', 'nbf', 'iat', 'jti']))",message="iss, aud, exp, nbf, iat and jti are checked by other fields and cannot be required here"
	RequiredClaims map[string]ClaimValue `json:"requiredClaims,omitempty"`

	// MaxTokenLifetime bounds the lifetime consumers accept for tokens from this issuer, measured as exp
	// minus iat. When unset it is one hour. At most 24 hours.
	// +optional
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:MaxLength=32
	// +kubebuilder:validation:XValidation:rule="duration(self) > duration('0s') && duration(self) <= duration('24h')",message="maxTokenLifetime must be more than zero and at most 24h"
	MaxTokenLifetime *metav1.Duration `json:"maxTokenLifetime,omitempty"`

	// Disabled stops every consumer from accepting tokens from this issuer.
	// +optional
	Disabled bool `json:"disabled,omitempty"`
}

// ClaimValue is the exact string value a required claim must have.
// +kubebuilder:validation:MaxLength=512
type ClaimValue string

// OIDCIssuer is an OpenID Connect issuer and where its signing keys come from.
type OIDCIssuer struct {
	// IssuerURI is the issuer's identifier, compared exactly with a token's iss claim. It must start with
	// "https://" in lower case, use port 443, name a DNS host rather than an IP address, and carry no query,
	// fragment or user information.
	// +kubebuilder:validation:MinLength=9
	// +kubebuilder:validation:MaxLength=512
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="issuerUri cannot be changed"
	// +kubebuilder:validation:XValidation:rule="self.startsWith('https://') && isURL(self) && url(self).getScheme() == 'https' && url(self).getPort() in ['', '443'] && size(url(self).getQuery()) == 0 && !self.contains('#') && !self.contains('@') && !self.startsWith('https://[') && !self.matches('^https://[0-9.]+([:/?].*)?$')",message="issuerUri must start with https:// in lower case, use port 443 and a DNS hostname rather than an IP address, and carry no query, fragment or user information"
	IssuerURI string `json:"issuerUri"`

	// JWKS says where the issuer's signing keys come from.
	JWKS JWKSSource `json:"jwks"`
}

// JWKSSource says where an issuer's signing keys come from.
//
// +kubebuilder:validation:XValidation:rule="self.source == 'ExplicitURL' ? (has(self.explicitUrl) && !has(self.inline)) : (self.source == 'Inline' ? (has(self.inline) && !has(self.explicitUrl)) : (!has(self.explicitUrl) && !has(self.inline)))",message="explicitUrl is set only for ExplicitURL, and inline only for Inline"
type JWKSSource struct {
	// Source is Discovery (the issuer's discovery document names the key set), ExplicitURL, or Inline.
	// +optional
	// +kubebuilder:default=Discovery
	Source JWKSSourceType `json:"source,omitempty"`

	// ExplicitURL is the key set's URL, under the same rules as issuerUri except that it may carry a query.
	// +optional
	// +kubebuilder:validation:MaxLength=512
	// +kubebuilder:validation:XValidation:rule="self.startsWith('https://') && isURL(self) && url(self).getScheme() == 'https' && url(self).getPort() in ['', '443'] && !self.contains('#') && !self.contains('@') && !self.startsWith('https://[') && !self.matches('^https://[0-9.]+([:/?].*)?$')",message="explicitUrl must start with https:// in lower case, use port 443 and a DNS hostname rather than an IP address, and carry no fragment or user information"
	ExplicitURL string `json:"explicitUrl,omitempty"`

	// Inline is a JSON Web Key Set document (RFC 7517), for issuers the control plane cannot reach. It holds
	// public keys only.
	// +optional
	// +kubebuilder:validation:MinLength=2
	// +kubebuilder:validation:MaxLength=32768
	// +kubebuilder:validation:XValidation:rule="!self.matches('\"(d|p|q|dp|dq|qi|oth|k)\"[[:space:]]*:')",message="inline must hold public keys only: remove the private key members (d, p, q, dp, dq, qi, oth, k) and rotate the key"
	Inline string `json:"inline,omitempty"`
}

// JWKSSourceType names where an issuer's signing keys come from.
// +kubebuilder:validation:Enum=Discovery;ExplicitURL;Inline
type JWKSSourceType string

const (
	// JWKSSourceDiscovery reads the key set's URL from the issuer's discovery document.
	JWKSSourceDiscovery JWKSSourceType = "Discovery"
	// JWKSSourceExplicitURL fetches the key set from explicitUrl.
	JWKSSourceExplicitURL JWKSSourceType = "ExplicitURL"
	// JWKSSourceInline takes the key set from inline, with no fetching.
	JWKSSourceInline JWKSSourceType = "Inline"
)

// WorkloadIdentityIssuerStatus is what consumers read.
type WorkloadIdentityIssuerStatus struct {
	// Issuer is the exact iss value tokens carry: spec.oidc.issuerUri.
	// +optional
	Issuer string `json:"issuer,omitempty"`

	// KeySet is the verified JSON Web Key Set: only the keys a token verifier can use, Ed25519 (EdDSA),
	// P-256 (ES256) and RSA of at least 2048 bits (RS256), with use "sig" or none and alg matching the key,
	// each with a kid unique in the set, and each with its public members only.
	// +optional
	KeySet string `json:"keySet,omitempty"`

	// KeySetGeneration increases each time KeySet changes.
	// +optional
	KeySetGeneration int64 `json:"keySetGeneration,omitempty"`

	// LastFetchTime is when the key set in KeySet was last obtained and verified: for an inline key set,
	// when KeySet last changed.
	// +optional
	LastFetchTime *metav1.Time `json:"lastFetchTime,omitempty"`

	// MaxTokenLifetime is the lifetime bound in effect: spec.maxTokenLifetime, or one hour when that is
	// unset.
	// +optional
	MaxTokenLifetime *metav1.Duration `json:"maxTokenLifetime,omitempty"`

	// RequiredClaims is spec.requiredClaims as of ObservedGeneration: the claims a token must carry with
	// exactly these string values.
	// +optional
	RequiredClaims map[string]ClaimValue `json:"requiredClaims,omitempty"`

	// ObservedGeneration is the spec generation this status describes.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions: Ready, DomainVerified and Disabled.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// WorkloadIdentityIssuer condition types and reasons, which consumers read from status.
const (
	// WorkloadIdentityIssuerReadyCondition is true when consumers may use the key set in status.
	WorkloadIdentityIssuerReadyCondition = "Ready"
	// WorkloadIdentityIssuerDomainVerifiedCondition is true when a Domain in the issuer's project names the
	// issuer's host exactly and is verified.
	WorkloadIdentityIssuerDomainVerifiedCondition = "DomainVerified"
	// WorkloadIdentityIssuerDisabledCondition is true when spec.disabled is set.
	WorkloadIdentityIssuerDisabledCondition = "Disabled"

	// WorkloadIdentityIssuerKeySetVerifiedReason sets Ready=True.
	WorkloadIdentityIssuerKeySetVerifiedReason = "KeySetVerified"
	// WorkloadIdentityIssuerKeysDroppedReason keeps Ready=True; the message names the unusable keys left out.
	WorkloadIdentityIssuerKeysDroppedReason = "KeysDropped"
	// WorkloadIdentityIssuerNoUsableKeysReason sets Ready=False.
	WorkloadIdentityIssuerNoUsableKeysReason = "NoUsableKeys"
	// WorkloadIdentityIssuerKeySourceNotSupportedReason sets Ready=False for a key source the controller does
	// not read.
	WorkloadIdentityIssuerKeySourceNotSupportedReason = "KeySourceNotSupported"
	// WorkloadIdentityIssuerDisabledReason sets Ready=False and Disabled=True.
	WorkloadIdentityIssuerDisabledReason = "Disabled"
	// WorkloadIdentityIssuerEnabledReason sets Disabled=False.
	WorkloadIdentityIssuerEnabledReason = "Enabled"
	// WorkloadIdentityIssuerDomainVerifiedReason sets DomainVerified=True.
	WorkloadIdentityIssuerDomainVerifiedReason = "DomainVerified"
	// WorkloadIdentityIssuerNoVerifiedDomainReason sets DomainVerified=False.
	WorkloadIdentityIssuerNoVerifiedDomainReason = "NoVerifiedDomain"
)

// +kubebuilder:object:root=true

// WorkloadIdentityIssuerList contains a list of WorkloadIdentityIssuer.
type WorkloadIdentityIssuerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WorkloadIdentityIssuer `json:"items"`
}
