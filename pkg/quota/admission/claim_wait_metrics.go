package admission

import (
	"k8s.io/component-base/metrics"
	legacyregistry "k8s.io/component-base/metrics/legacyregistry"
)

// Metrics of the claim wait. All are aggregated at the system level to avoid
// cardinality explosion. The claim watches themselves show in the API
// server's own series: apiserver_longrunning_requests and
// apiserver_request_total, resource="resourceclaims", verb="WATCH".
var (
	waiterRegistrations = metrics.NewCounter(
		&metrics.CounterOpts{
			Subsystem:      "milo_quota_admission",
			Name:           "waiter_registrations_total",
			Help:           "Total number of claim waits started.",
			StabilityLevel: metrics.ALPHA,
		},
	)

	waiterCompletions = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      "milo_quota_admission",
			Name:           "waiter_completions_total",
			Help:           "Total number of claim waits completed, labeled by result.",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"result"}, // result: granted, denied, timeout, deleted, watch_failed
	)

	waitersCurrent = metrics.NewGauge(
		&metrics.GaugeOpts{
			Subsystem:      "milo_quota_admission",
			Name:           "waiters_current",
			Help:           "Current number of claim waits, each holding one claim watch.",
			StabilityLevel: metrics.ALPHA,
		},
	)

	waiterDuration = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      "milo_quota_admission",
			Name:           "waiter_duration_seconds",
			Help:           "Duration of a claim wait, from the claim's create to its outcome, labeled by result.",
			Buckets:        []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 30, 60},
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"result"},
	)

	claimWaitGets = metrics.NewCounter(
		&metrics.CounterOpts{
			Subsystem:      "milo_quota_admission",
			Name:           "claim_wait_gets_after_gone_total",
			Help:           "Total number of claim reads a claim wait made because its watch answered 410 Gone.",
			StabilityLevel: metrics.ALPHA,
		},
	)
)

func init() {
	legacyregistry.MustRegister(waiterRegistrations)
	legacyregistry.MustRegister(waiterCompletions)
	legacyregistry.MustRegister(waitersCurrent)
	legacyregistry.MustRegister(waiterDuration)
	legacyregistry.MustRegister(claimWaitGets)
}
