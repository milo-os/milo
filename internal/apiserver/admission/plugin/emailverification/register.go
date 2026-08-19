package emailverification

import (
	"io"

	"k8s.io/apiserver/pkg/admission"
	"k8s.io/klog/v2"
)

// PluginName is the name of the EmailVerificationEnforcement admission plugin.
// It is also the handle for the kill switch:
// --disable-admission-plugins=EmailVerificationEnforcement.
const PluginName = "EmailVerificationEnforcement"

// Register registers the EmailVerificationEnforcement admission plugin with the
// provided plugin registry.
//
// Deliberately thinner than plugin/projectsuspension's Register, which keeps a
// package-level singleton so a readyz check can reach the instance. This plugin
// holds no cache and makes no API calls, so it has nothing to be ready for and
// nothing to expose.
func Register(plugins *admission.Plugins) {
	plugins.Register(PluginName, func(_ io.Reader) (admission.Interface, error) {
		klog.InfoS("Registered email verification enforcement plugin with Milo apiserver")
		return NewPlugin()
	})
}
