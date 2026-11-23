package certidentity

import (
	k8smetrics "k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"
)

var decisions = k8smetrics.NewCounterVec(
	&k8smetrics.CounterOpts{
		Name: "milo_client_certificate_uid_decisions_total",
		Help: "Requests whose client certificate carried a UID, by reason. Allowed keeps the UID; every other reason " +
			"removes it, so the request is authorized by the certificate's name and groups alone.",
		StabilityLevel: k8smetrics.ALPHA,
	},
	[]string{"reason"},
)

func init() {
	legacyregistry.MustRegister(decisions)
}
