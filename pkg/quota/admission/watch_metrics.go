package admission

import (
	"k8s.io/component-base/metrics"
	legacyregistry "k8s.io/component-base/metrics/legacyregistry"
)

// Metrics for watch manager
// All metrics are aggregated at the system level to avoid cardinality explosion.
// Use Prometheus queries to derive per-project insights from admission plugin metrics instead.
var (
	// Lifecycle metrics
	watchManagersCreated = metrics.NewCounter(
		&metrics.CounterOpts{
			Subsystem:      "milo_quota_admission",
			Name:           "watch_managers_created_total",
			Help:           "Total number of watch managers created.",
			StabilityLevel: metrics.ALPHA,
		},
	)

	watchManagersStopped = metrics.NewCounter(
		&metrics.CounterOpts{
			Subsystem:      "milo_quota_admission",
			Name:           "watch_managers_stopped_total",
			Help:           "Total number of watch managers stopped.",
			StabilityLevel: metrics.ALPHA,
		},
	)

	watchManagersActive = metrics.NewGauge(
		&metrics.GaugeOpts{
			Subsystem:      "milo_quota_admission",
			Name:           "watch_managers_active",
			Help:           "Current number of active watch managers.",
			StabilityLevel: metrics.ALPHA,
		},
	)

	// Watch stream health metrics
	watchStreamsDesired = metrics.NewGauge(
		&metrics.GaugeOpts{
			Subsystem:      "milo_quota_admission",
			Name:           "watch_streams_desired",
			Help:           "Number of watch streams that should be running (one per active watch manager).",
			StabilityLevel: metrics.ALPHA,
		},
	)

	watchStreamsConnected = metrics.NewGauge(
		&metrics.GaugeOpts{
			Subsystem:      "milo_quota_admission",
			Name:           "watch_streams_connected",
			Help:           "Number of watch streams currently connected and processing events.",
			StabilityLevel: metrics.ALPHA,
		},
	)

	watchRestarts = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      "milo_quota_admission",
			Name:           "watch_restarts_total",
			Help:           "Total number of watch stream restarts, labeled by HTTP status code (or 'unknown' for non-HTTP errors).",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"status_code"},
	)

	watchRestartDuration = metrics.NewHistogram(
		&metrics.HistogramOpts{
			Subsystem:      "milo_quota_admission",
			Name:           "watch_restart_duration_seconds",
			Help:           "Duration of watch stream restart attempts.",
			Buckets:        []float64{0.001, 0.01, 0.1, 0.5, 1, 2, 5, 10, 30, 60},
			StabilityLevel: metrics.ALPHA,
		},
	)

	watchStreamLifetimeSeconds = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      "milo_quota_admission",
			Name:           "watch_stream_lifetime_seconds",
			Help:           "Total lifetime of watch streams from connection to disconnect, labeled by disconnect reason.",
			Buckets:        []float64{1, 10, 30, 60, 300, 600, 1800, 3600, 7200, 14400, 28800, 86400},
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"reason"}, // reason: error, shutdown
	)

	watchStreamMaxUptimeSeconds = metrics.NewGauge(
		&metrics.GaugeOpts{
			Subsystem:      "milo_quota_admission",
			Name:           "watch_stream_max_uptime_seconds",
			Help:           "Maximum uptime across all currently connected watch streams.",
			StabilityLevel: metrics.ALPHA,
		},
	)

	watchBookmarkMaxAgeSeconds = metrics.NewGauge(
		&metrics.GaugeOpts{
			Subsystem:      "milo_quota_admission",
			Name:           "watch_bookmark_max_age_seconds",
			Help:           "Maximum bookmark age across all watch streams (time since last bookmark update).",
			StabilityLevel: metrics.ALPHA,
		},
	)

	// Event processing metrics
	watchEventsReceived = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      "milo_quota_admission",
			Name:           "watch_events_received_total",
			Help:           "Total number of watch events received, labeled by event type.",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"event_type"}, // event_type: Added, Modified, Deleted, Bookmark, Error
	)

	watchEventsProcessed = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      "milo_quota_admission",
			Name:           "watch_events_processed_total",
			Help:           "Total number of watch events processed, labeled by result.",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"result"}, // result: granted, denied, pending, ignored, error
	)

	// Waiter metrics
	waiterRegistrations = metrics.NewCounter(
		&metrics.CounterOpts{
			Subsystem:      "milo_quota_admission",
			Name:           "waiter_registrations_total",
			Help:           "Total number of claim waiter registrations.",
			StabilityLevel: metrics.ALPHA,
		},
	)

	waiterCompletions = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      "milo_quota_admission",
			Name:           "waiter_completions_total",
			Help:           "Total number of claim waiter completions, labeled by result.",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"result"}, // result: granted, denied, timeout, deleted, unregistered
	)

	waitersCurrent = metrics.NewGauge(
		&metrics.GaugeOpts{
			Subsystem:      "milo_quota_admission",
			Name:           "waiters_current",
			Help:           "Current number of active claim waiters.",
			StabilityLevel: metrics.ALPHA,
		},
	)

	waiterDuration = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      "milo_quota_admission",
			Name:           "waiter_duration_seconds",
			Help:           "Duration from waiter registration to completion, labeled by result.",
			Buckets:        []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 30, 60},
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"result"},
	)

	// TTL metrics
	ttlResets = metrics.NewCounter(
		&metrics.CounterOpts{
			Subsystem:      "milo_quota_admission",
			Name:           "ttl_resets_total",
			Help:           "Total number of TTL timer resets.",
			StabilityLevel: metrics.ALPHA,
		},
	)

	ttlExpirations = metrics.NewCounter(
		&metrics.CounterOpts{
			Subsystem:      "milo_quota_admission",
			Name:           "ttl_expirations_total",
			Help:           "Total number of TTL expirations (watch manager shutdowns due to inactivity).",
			StabilityLevel: metrics.ALPHA,
		},
	)
)

func init() {
	// Register watch manager metrics
	legacyregistry.MustRegister(watchManagersCreated)
	legacyregistry.MustRegister(watchManagersStopped)
	legacyregistry.MustRegister(watchManagersActive)
	legacyregistry.MustRegister(watchStreamsDesired)
	legacyregistry.MustRegister(watchStreamsConnected)
	legacyregistry.MustRegister(watchRestarts)
	legacyregistry.MustRegister(watchRestartDuration)
	legacyregistry.MustRegister(watchStreamLifetimeSeconds)
	legacyregistry.MustRegister(watchStreamMaxUptimeSeconds)
	legacyregistry.MustRegister(watchBookmarkMaxAgeSeconds)
	legacyregistry.MustRegister(watchEventsReceived)
	legacyregistry.MustRegister(watchEventsProcessed)
	legacyregistry.MustRegister(waiterRegistrations)
	legacyregistry.MustRegister(waiterCompletions)
	legacyregistry.MustRegister(waitersCurrent)
	legacyregistry.MustRegister(waiterDuration)
	legacyregistry.MustRegister(ttlResets)
	legacyregistry.MustRegister(ttlExpirations)
}
