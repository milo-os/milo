package projectsuspension

import (
	"fmt"
	"io"
	"net/http"
	"sync"

	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/server/healthz"
	"k8s.io/klog/v2"
)

// PluginName is the name of the ProjectSuspensionEnforcement admission plugin.
const PluginName = "ProjectSuspensionEnforcement"

var (
	// pluginInstance stores the singleton plugin instance for readiness checks.
	pluginInstance *Plugin
	pluginMutex    sync.RWMutex
)

// Register registers the ProjectSuspensionEnforcement admission plugin with
// the provided plugin registry.
func Register(plugins *admission.Plugins) {
	plugins.Register(PluginName, func(_ io.Reader) (admission.Interface, error) {
		klog.InfoS("Registered project suspension enforcement plugin with Milo apiserver")
		plugin, err := NewPlugin()
		if err != nil {
			return nil, err
		}

		// Store the plugin instance for access by API server readiness checks.
		pluginMutex.Lock()
		pluginInstance = plugin
		pluginMutex.Unlock()

		return plugin, nil
	})
}

// ReadinessCheck returns a readiness check confirming the suspension cache
// has completed its initial sync before the apiserver is marked ready to
// serve traffic.
func ReadinessCheck() healthz.HealthChecker {
	return healthz.NamedCheck("project-suspension-validator", func(r *http.Request) error {
		pluginMutex.RLock()
		plugin := pluginInstance
		pluginMutex.RUnlock()

		if plugin == nil {
			klog.V(4).Info("project suspension plugin not enabled for apiserver")
			return nil
		}

		if plugin.suspensionCache == nil {
			// Cache not initialized yet - this is okay during startup.
			return nil
		}

		if !plugin.suspensionCache.HasSynced() {
			return fmt.Errorf("project suspension cache not synced")
		}

		return nil
	})
}
