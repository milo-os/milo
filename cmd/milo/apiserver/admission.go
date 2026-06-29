package app

import (
	"go.miloapis.com/milo/internal/apiserver/admission/plugin/namespace/lifecycle"
	"k8s.io/apimachinery/pkg/util/sets"
	validatingadmissionpolicy "k8s.io/apiserver/pkg/admission/plugin/policy/validating"
	mutatingwebhook "k8s.io/apiserver/pkg/admission/plugin/webhook/mutating"
	validatingwebhook "k8s.io/apiserver/pkg/admission/plugin/webhook/validating"
	"k8s.io/kubernetes/pkg/kubeapiserver/options"

	admissionquota "go.miloapis.com/milo/pkg/quota/admission"
)

// GetMiloOrderedPlugins returns the complete ordered list of admission plugins,
// including both Kubernetes built-in plugins and our custom Milo plugins.
// Custom plugins are inserted before the webhook plugins as recommended.
func GetMiloOrderedPlugins() []string {
	// Start with Kubernetes' built-in ordered plugin list
	plugins := make([]string, 0, len(options.AllOrderedPlugins)+1)

	// Find where to insert our custom plugins
	for _, plugin := range options.AllOrderedPlugins {
		if plugin == "ValidatingAdmissionPolicy" {
			// Insert our custom validating plugins here, before ValidatingAdmissionPolicy
			// This ensures they run before the generic webhook validators
			plugins = append(plugins, admissionquota.PluginName)
		}
		plugins = append(plugins, plugin)
	}

	return plugins
}

// MiloAdmissionPlugins lists all Milo-specific admission plugins
var MiloAdmissionPlugins = sets.New[string](
	admissionquota.PluginName, // ResourceQuotaEnforcement
	// Add future Milo admission plugins here
)

// DefaultOffAdmissionPlugins returns admission plugins that should be disabled by default.
// This keeps only essential plugins and our custom Milo plugins enabled.
func DefaultOffAdmissionPlugins() sets.Set[string] {
	// Plugins we want ON by default
	defaultOnPlugins := sets.New(
		lifecycle.PluginName,                 // NamespaceLifecycle
		mutatingwebhook.PluginName,           // MutatingAdmissionWebhook
		validatingwebhook.PluginName,         // ValidatingAdmissionWebhook
		validatingadmissionpolicy.PluginName, // ValidatingAdmissionPolicy
	)

	// Add all Milo plugins to the enabled set
	defaultOnPlugins = defaultOnPlugins.Union(MiloAdmissionPlugins)

	// Get the complete plugin list including our custom plugins
	allPlugins := sets.New(GetMiloOrderedPlugins()...)

	// Return plugins to turn OFF (all plugins minus the ones we want ON)
	return allPlugins.Difference(defaultOnPlugins)
}
