package access

import (
	k8smetrics "k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"
)

var (
	// invalidDeclarations is 1 for each problem in a CRD's access-rule
	// declaration. Requests for the resource are refused while it is set.
	invalidDeclarations = k8smetrics.NewGaugeVec(
		&k8smetrics.GaugeOpts{
			Name: "milo_access_rule_invalid",
			Help: "1 for each problem that makes a CRD's access-rule declaration invalid, by group, resource and reason. " +
				"Requests for the resource are refused outside system:masters until the CRD is fixed.",
			StabilityLevel: k8smetrics.ALPHA,
		},
		[]string{"group", "resource", "reason"},
	)

	// staleCache counts the CRDs whose cached copy lags storage.
	staleCache = k8smetrics.NewGauge(
		&k8smetrics.GaugeOpts{
			Name: "milo_access_rules_stale",
			Help: "Number of CRDs whose copy in the API server's CRD cache has not caught up with storage, so their access " +
				"rules may be out of date. Their resources are refused outside system:masters; other resources are served.",
			StabilityLevel: k8smetrics.ALPHA,
		},
	)
)

func init() {
	legacyregistry.MustRegister(invalidDeclarations, staleCache)
}
