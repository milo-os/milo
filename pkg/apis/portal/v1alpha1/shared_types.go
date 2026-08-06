package v1alpha1

// PluginAssets locates a plugin's built Module Federation bundle. The portal
// host fetches the manifest and every asset server-side through its own
// same-origin asset proxy — a plugin's real origin (BaseURL) is never
// exposed to the browser.
type PluginAssets struct {
	// BaseURL is the HTTPS origin, operated by the service team, serving the
	// plugin's built assets (remoteEntry.js, chunks, and the manifest at
	// ManifestPath).
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^https://`
	BaseURL string `json:"baseURL"`

	// ManifestPath is the path to plugin-manifest.json under BaseURL.
	// Defaults to "/plugin-manifest.json".
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:default="/plugin-manifest.json"
	ManifestPath string `json:"manifestPath,omitempty"`

	// CABundle is an optional PEM-encoded CA certificate bundle for an
	// internal CA fronting BaseURL. Server-side only — never sent to the
	// browser.
	//
	// +kubebuilder:validation:Optional
	CABundle string `json:"caBundle,omitempty"`
}
