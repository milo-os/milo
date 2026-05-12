package metrics

import (
	"sync"

	"k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"
)

const subsystem = "project_provider"

var (
	// ProjectAddTotal counts AddProject calls by outcome.
	// Labels: status = "success" | "error" | "abandoned"
	ProjectAddTotal = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      subsystem,
			Name:           "add_total",
			Help:           "Total number of AddProject attempts by outcome",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"status"},
	)

	// ProjectAddRetriesTotal counts the total number of retried AddProject calls.
	ProjectAddRetriesTotal = metrics.NewCounter(
		&metrics.CounterOpts{
			Subsystem:      subsystem,
			Name:           "add_retries_total",
			Help:           "Total number of AddProject retries due to transient errors",
			StabilityLevel: metrics.ALPHA,
		},
	)

	// ProjectAddDurationSeconds measures how long AddProject calls take.
	ProjectAddDurationSeconds = metrics.NewHistogram(
		&metrics.HistogramOpts{
			Subsystem:      subsystem,
			Name:           "add_duration_seconds",
			Help:           "Duration of AddProject calls in seconds",
			Buckets:        []float64{0.1, 0.5, 1, 2, 5, 10, 30},
			StabilityLevel: metrics.ALPHA,
		},
	)

	// QueueDepth reports the current number of projects waiting to be added.
	QueueDepth = metrics.NewGauge(
		&metrics.GaugeOpts{
			Subsystem:      subsystem,
			Name:           "queue_depth",
			Help:           "Current number of projects waiting in the add queue",
			StabilityLevel: metrics.ALPHA,
		},
	)
)

var registerOnce sync.Once

func Register() {
	registerOnce.Do(func() {
		legacyregistry.MustRegister(
			ProjectAddTotal,
			ProjectAddRetriesTotal,
			ProjectAddDurationSeconds,
			QueueDepth,
		)
	})
}
