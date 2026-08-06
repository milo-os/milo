package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PluginEntitlementRequirement controls whether a project must have an
// active entitlement for the plugin's service before cloud-portal shows it.
//
// +kubebuilder:validation:Enum=Required;None
type PluginEntitlementRequirement string

const (
	// PluginEntitlementRequired means a project is only entitled to see the
	// plugin if it has an Active ServiceEntitlement for the owning service.
	PluginEntitlementRequired PluginEntitlementRequirement = "Required"
	// PluginEntitlementNone means the plugin is visible to every project,
	// regardless of entitlement state.
	PluginEntitlementNone PluginEntitlementRequirement = "None"
)

// PluginVisibility gates whether cloud-portal shows a plugin's extensions
// for a given project. Provider plugins (staff-portal) have no equivalent —
// staff users get whatever blanket access staff-portal already grants.
type PluginVisibility struct {
	// Entitlement controls project-level gating. See PluginEntitlementRequirement.
	//
	// +kubebuilder:validation:Required
	Entitlement PluginEntitlementRequirement `json:"entitlement"`

	// FeatureFlag, when set, additionally gates visibility on an OpenFeature
	// flag key evaluated by cloud-portal.
	//
	// +kubebuilder:validation:Optional
	FeatureFlag string `json:"featureFlag,omitempty"`
}

// ConsumerPortalPluginSpec defines the desired state of ConsumerPortalPlugin.
type ConsumerPortalPluginSpec struct {
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

	// Visibility gates whether a project sees this plugin's extensions.
	//
	// +kubebuilder:validation:Required
	Visibility PluginVisibility `json:"visibility"`
}

// Condition type constants for ConsumerPortalPlugin/ProviderPortalPlugin status.
// Mirrors the portal host's own PluginEntryStatus (see cloud-portal/staff-portal
// app/modules/plugins/types.ts) so `kubectl describe` surfaces the same health
// picture the portal's own registry computes.
const (
	// PluginDiscovered reports whether the manifest was fetched successfully.
	PluginDiscovered = "Discovered"
	// PluginCompatible reports whether the manifest's declared SDK range is
	// satisfied by the portal's host SDK version.
	PluginCompatible = "Compatible"
	// PluginReady aggregates Discovered, Compatible, and the Suspend kill
	// switch into a single servability signal.
	PluginReady = "Ready"
)

// ConsumerPortalPluginStatus reports the portal's most recent manifest
// resolution for this plugin. Written by cloud-portal (the consuming
// host), not by the services-operator that writes Spec.
type ConsumerPortalPluginStatus struct {
	// ObservedGeneration is the most recent spec generation the writing
	// portal has processed.
	//
	// +kubebuilder:validation:Optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions communicate manifest health. See PluginDiscovered,
	// PluginCompatible, PluginReady.
	//
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Manifest is a snapshot of the most recently resolved manifest, when
	// discovery has succeeded at least once.
	//
	// +kubebuilder:validation:Optional
	Manifest *PluginManifestSnapshot `json:"manifest,omitempty"`
}

// PluginManifestSnapshot is a portal-resolved snapshot of a live
// plugin-manifest.json.
type PluginManifestSnapshot struct {
	// Version is the manifest's own declared version.
	//
	// +kubebuilder:validation:Required
	Version string `json:"version"`

	// SDKRange is the manifest's declared sdk.range.
	//
	// +kubebuilder:validation:Required
	SDKRange string `json:"sdkRange"`

	// Digest is a "sha256:..." digest of the fetched manifest bytes.
	//
	// +kubebuilder:validation:Required
	Digest string `json:"digest"`

	// FetchedAt is when the portal last fetched this manifest.
	//
	// +kubebuilder:validation:Required
	FetchedAt metav1.Time `json:"fetchedAt"`

	// Extensions counts declared extensions by type, e.g. {"portal.nav/project": 1}.
	//
	// +kubebuilder:validation:Optional
	Extensions map[string]int32 `json:"extensions,omitempty"`
}

// ConsumerPortalPlugin registers a service's portal plugin for cloud-portal,
// the customer-facing portal. Service teams do not create these directly —
// they are fanned out by the services-operator from a ServiceConfiguration's
// spec.userInterface.consumer block.
//
// ### How It Works
// - A service team sets spec.userInterface.consumer on their ServiceConfiguration
// - The services-operator fans that out into a ConsumerPortalPlugin here
// - cloud-portal watches ConsumerPortalPlugin, fetches the manifest at
//   spec.assets, and writes back Status reporting what it found
// - Extensions declared in the manifest (portal.nav/project, portal.page/project,
//   portal.card/project-home) render inside a project, gated by spec.visibility
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
type ConsumerPortalPlugin struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +kubebuilder:validation:Required
	Spec   ConsumerPortalPluginSpec   `json:"spec"`
	Status ConsumerPortalPluginStatus `json:"status,omitempty"`
}

// ConsumerPortalPluginList contains a list of ConsumerPortalPlugin.
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
type ConsumerPortalPluginList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ConsumerPortalPlugin `json:"items"`
}
