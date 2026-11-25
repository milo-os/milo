package organizationonboarding

import (
	"io"

	"k8s.io/apiserver/pkg/admission"
	"k8s.io/klog/v2"
)

// PluginName is the name of the OrganizationOnboardingEnforcement admission
// plugin.
const PluginName = "OrganizationOnboardingEnforcement"

// Register registers the OrganizationOnboardingEnforcement admission plugin
// with the provided plugin registry.
func Register(plugins *admission.Plugins) {
	plugins.Register(PluginName, func(_ io.Reader) (admission.Interface, error) {
		klog.InfoS("Registered organization onboarding enforcement plugin with Milo apiserver")
		return NewPlugin()
	})
}
