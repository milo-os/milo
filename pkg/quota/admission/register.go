package admission

import (
	"fmt"
	"io"
	"net/http"
	"sync"

	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/server/healthz"
	"k8s.io/klog/v2"
)

var (
	// pluginInstance stores the singleton plugin instance for readiness checks
	pluginInstance *ResourceQuotaEnforcementPlugin
	pluginMutex    sync.RWMutex
)

// Register registers the ResourceQuotaEnforcement admission plugin for custom plugin registries
func Register(plugins *admission.Plugins) {
	plugins.Register(PluginName, func(config io.Reader) (admission.Interface, error) {
		klog.InfoS("Registered resource quota enforcement plugin with Milo apiserver")
		plugin, err := NewResourceQuotaEnforcementPlugin()
		if err != nil {
			return nil, err
		}

		// Store the plugin instance for access by API server readiness checks
		pluginMutex.Lock()
		pluginInstance = plugin
		pluginMutex.Unlock()

		return plugin, nil
	})
}

// ReadinessCheck returns a readiness check for the quota validator integrated
// into the admission plugin to confirm it's synced before allowing requests to
// be processed by the apiserver.
func ReadinessCheck() healthz.HealthChecker {
	return healthz.NamedCheck("quota-validator", func(r *http.Request) error {
		// Get the plugin instance
		pluginMutex.RLock()
		plugin := pluginInstance
		pluginMutex.RUnlock()

		if plugin == nil {
			klog.V(4).Info("quota plugin not enabled for apiserver")
			return nil
		}

		// Check if the validator has been initialized and synced
		if plugin.resourceTypeValidator == nil {
			// Validator not initialized yet - this is okay during startup
			return nil
		}

		if !plugin.resourceTypeValidator.HasSynced() {
			return fmt.Errorf("quota validator cache not synced")
		}

		return nil
	})
}
