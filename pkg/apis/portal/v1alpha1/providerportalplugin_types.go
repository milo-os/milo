package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ProviderPortalPluginSpec defines the desired state of ProviderPortalPlugin.
type ProviderPortalPluginSpec struct {
	// Slug is the unique DNS label identifying this plugin. It is the URL
	// segment and the same-origin asset-proxy segment
	// (/api/plugins/<slug>/...). Immutable after creation.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=63
	Slug string `json:"slug"`

	// DisplayName is the human-readable name shown in the portal UI (e.g. a
	// "dev plugin" badge, error states).
	//
	// +kubebuilder:validation:Required
	DisplayName string `json:"displayName"`

	// Deprecated marks the winning ServiceConfiguration as deprecated. The
	// portal may use this to warn operators without hiding the plugin.
	//
	// +kubebuilder:validation:Optional
	Deprecated bool `json:"deprecated,omitempty"`

	// Suspend is a platform-operator kill switch. A suspended plugin is
	// never served, regardless of manifest health.
	//
	// +kubebuilder:validation:Optional
	Suspend bool `json:"suspend,omitempty"`

	// Assets locates the plugin's built Module Federation bundle.
	//
	// +kubebuilder:validation:Required
	Assets PluginAssets `json:"assets"`

	// No Visibility block: staff-portal has no per-project entitlement
	// concept. Staff users get whatever blanket access staff-portal already
	// grants them — see ConsumerPortalPluginSpec.Visibility for the
	// cloud-portal equivalent.
}

// ProviderPortalPluginStatus reports the portal's most recent manifest
// resolution for this plugin. Written by staff-portal (the consuming
// host), not by the services-operator that writes Spec. Shape mirrors
// ConsumerPortalPluginStatus — see PluginDiscovered/PluginCompatible/
// PluginReady and PluginManifestSnapshot in consumerportalplugin_types.go.
type ProviderPortalPluginStatus struct {
	// +kubebuilder:validation:Optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// +kubebuilder:validation:Optional
	Manifest *PluginManifestSnapshot `json:"manifest,omitempty"`
}

// ProviderPortalPlugin registers a service's portal plugin for staff-portal,
// the internal operator portal. Service teams do not create these directly —
// they are fanned out by the services-operator from a ServiceConfiguration's
// spec.userInterface.provider block.
//
// ### How It Works
// - A service team sets spec.userInterface.provider on their ServiceConfiguration
// - The services-operator fans that out into a ProviderPortalPlugin here
// - staff-portal watches ProviderPortalPlugin, fetches the manifest at
//   spec.assets, and writes back Status reporting what it found
// - Extensions declared in the manifest render platform-wide, with no
//   project/organization scoping: portal.nav/platform (a top-level nav
//   item), portal.page/platform (a platform-wide routed page), or
//   portal.resource/platform (a resource type staff-portal's own Resources
//   list queries and renders itself — no plugin code runs to produce those
//   rows)
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:subresource:status
// +k8s:openapi-gen=true
// +kubebuilder:printcolumn:name="Slug",type="string",JSONPath=".spec.slug"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:selectablefield:JSONPath=".spec.slug"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.spec.slug) || self.spec.slug == oldSelf.spec.slug",message="spec.slug is immutable"
// +kubebuilder:metadata:annotations="discovery.miloapis.com/parent-contexts=Platform"
type ProviderPortalPlugin struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +kubebuilder:validation:Required
	Spec   ProviderPortalPluginSpec   `json:"spec"`
	Status ProviderPortalPluginStatus `json:"status,omitempty"`
}

// ProviderPortalPluginList contains a list of ProviderPortalPlugin.
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
type ProviderPortalPluginList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ProviderPortalPlugin `json:"items"`
}
