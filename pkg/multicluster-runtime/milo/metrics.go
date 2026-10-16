package milo

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// Registration stages reported by registrationFailures.
const (
	stageCreateCluster = "create_cluster"
	stageIndex         = "index"
	stageCacheSync     = "cache_sync"
	stageEngage        = "engage"
)

// Provider metrics are registered with controller-runtime's registry, so they
// are served on a consumer's existing manager /metrics endpoint without extra
// wiring. Every series is labelled with the provider's controller name, because
// one process can run several providers (see Options.ControllerName).
var (
	engagedProjects = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "milo_cluster_provider_engaged_projects",
		Help: "Number of projects the provider has engaged with its multicluster manager.",
	}, []string{"provider"})

	initialSyncComplete = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "milo_cluster_provider_initial_sync_complete",
		Help: "1 once every project in the provider's initial project list has been processed, 0 until then. Set to 0 when the provider starts.",
	}, []string{"provider"})

	initialSyncDuration = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "milo_cluster_provider_initial_sync_duration_seconds",
		Help: "Seconds from provider start until every project in the initial project list was processed.",
	}, []string{"provider"})

	registrationFailures = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "milo_cluster_provider_registration_failures_total",
		Help: "Project registrations that failed, by the stage that failed. Failed registrations are retried.",
	}, []string{"provider", "stage"})
)

func init() {
	metrics.Registry.MustRegister(engagedProjects, initialSyncComplete, initialSyncDuration, registrationFailures)
}
