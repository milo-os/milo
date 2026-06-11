package projectstorage

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8smetrics "k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"
)

// continueTokenRejections counts requests rejected because the continue token's
// start key pointed outside the requester's tenant subtree. Non-zero values
// indicate either client bugs or attempted cross-tenant access.
var continueTokenRejections = k8smetrics.NewCounterVec(
	&k8smetrics.CounterOpts{
		Name:           "projectstorage_continue_token_rejections_total",
		Help:           "Continue tokens rejected for pointing outside the requester's tenant subtree",
		StabilityLevel: k8smetrics.ALPHA,
	},
	[]string{"resource_group", "resource_kind"},
)

func init() {
	legacyregistry.MustRegister(continueTokenRejections)
}

func recordContinueTokenRejection(gr schema.GroupResource) {
	continueTokenRejections.WithLabelValues(gr.Group, gr.Resource).Inc()
}
