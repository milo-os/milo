package accessowner

import (
	"io"

	"k8s.io/apiserver/pkg/admission"
)

// Register registers the AccessRuleOwnerEnforcement admission plugin.
func Register(plugins *admission.Plugins) {
	plugins.Register(PluginName, func(_ io.Reader) (admission.Interface, error) {
		return NewPlugin(), nil
	})
}
