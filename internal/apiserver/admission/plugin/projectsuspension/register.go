package projectsuspension

import (
	"io"

	"k8s.io/apiserver/pkg/admission"
)

// PluginName is the name of the ProjectSuspensionEnforcement admission plugin.
const PluginName = "ProjectSuspensionEnforcement"

// Register registers the ProjectSuspensionEnforcement admission plugin with
// the provided plugin registry.
func Register(plugins *admission.Plugins) {
	plugins.Register(PluginName, func(_ io.Reader) (admission.Interface, error) {
		return NewPlugin()
	})
}
