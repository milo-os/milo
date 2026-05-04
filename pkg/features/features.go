// Package features defines feature gates for the Milo API server.
//
// Feature gates follow the Kubernetes pattern for managing feature lifecycle:
//   - Alpha: Disabled by default, may be removed without notice
//   - Beta: Enabled by default, API may change
//   - GA: Enabled by default, stable
//
// Usage:
//
//	import (
//	    utilfeature "k8s.io/apiserver/pkg/util/feature"
//	    "go.miloapis.com/milo/pkg/features"
//	)
//
//	if utilfeature.DefaultFeatureGate.Enabled(features.EventsProxy) {
//	    // feature is enabled
//	}
package features

import (
	"k8s.io/apimachinery/pkg/util/runtime"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/component-base/featuregate"
)

const (
	// EventsProxy enables forwarding Kubernetes Events (core/v1.Event) to the
	// Activity service instead of storing them in etcd. This provides multi-tenant
	// event storage with automatic scope injection.
	//
	// owner: @datum-cloud/platform
	// alpha: v0.1.0
	EventsProxy featuregate.Feature = "EventsProxy"

	// Sessions enables the identity.miloapis.com/v1alpha1 Session virtual API
	// that proxies to an external identity provider for session management.
	//
	// owner: @datum-cloud/platform
	// alpha: v0.1.0
	// ga: v0.2.0
	Sessions featuregate.Feature = "Sessions"

	// UserIdentities enables the identity.miloapis.com/v1alpha1 UserIdentity
	// virtual API that proxies to an external identity provider.
	//
	// owner: @datum-cloud/platform
	// alpha: v0.1.0
	// ga: v0.2.0
	UserIdentities featuregate.Feature = "UserIdentities"

	// DiscoveryContextFilter enables parent-context-aware filtering of API
	// discovery responses. When enabled, resources tagged with the
	// discovery.miloapis.com/parent-contexts CRD annotation are hidden from
	// discovery in contexts where they don't apply (e.g. Organization-only
	// resources are hidden at the User context). Platform context requests
	// are never filtered.
	//
	// owner: @datum-cloud/platform
	// alpha: v0.1.0
	DiscoveryContextFilter featuregate.Feature = "DiscoveryContextFilter"

	// ServiceAccountKeys enables the identity.miloapis.com/v1alpha1 ServiceAccountKey
	// virtual API that proxies to an external identity provider for service account key management.
	//
	// owner: @datum-cloud/platform
	// alpha: v0.1.0
	ServiceAccountKeys featuregate.Feature = "ServiceAccountKeys"
)

func init() {
	runtime.Must(utilfeature.DefaultMutableFeatureGate.Add(defaultFeatureGates))
}

// defaultFeatureGates defines the default state of Milo feature gates.
// Features are listed in alphabetical order.
var defaultFeatureGates = map[featuregate.Feature]featuregate.FeatureSpec{
	DiscoveryContextFilter: {
		Default:    false,
		PreRelease: featuregate.Alpha,
	},
	EventsProxy: {
		Default:    false,
		PreRelease: featuregate.Alpha,
	},
	ServiceAccountKeys: {
		Default:    false,
		PreRelease: featuregate.Alpha,
	},
	Sessions: {
		Default:    true,
		PreRelease: featuregate.GA,
	},
	UserIdentities: {
		Default:    true,
		PreRelease: featuregate.GA,
	},
}
