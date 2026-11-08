package projectsuspension

import (
	"io"

	"k8s.io/apiserver/pkg/admission"
	"k8s.io/klog/v2"
)

// PluginName is the name of the ProjectSuspensionEnforcement admission plugin.
const PluginName = "ProjectSuspensionEnforcement"

// Register registers the ProjectSuspensionEnforcement admission plugin with
// the provided plugin registry.
func Register(plugins *admission.Plugins) {
	plugins.Register(PluginName, func(_ io.Reader) (admission.Interface, error) {
		klog.InfoS("Registered project suspension enforcement plugin with Milo apiserver")
		return NewPlugin()
	})
}
