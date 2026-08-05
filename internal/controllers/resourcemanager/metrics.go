package resourcemanager

import (
	"strconv"
	"sync"
	"time"

	"k8s.io/component-base/metrics"
	legacyregistry "k8s.io/component-base/metrics/legacyregistry"
)

var (
	activeSuspensionsTotal = metrics.NewGaugeVec(
		&metrics.GaugeOpts{
			Subsystem:      "milo_resourcemanager",
			Name:           "project_suspensions_active_total",
			Help:           "Total number of active project suspensions, categorized by reason.",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"reason"},
	)

	transitionDurationSeconds = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      "milo_resourcemanager",
			Name:           "project_suspension_transition_duration_seconds",
			Help:           "Duration (latency) in seconds for project suspension state transitions.",
			Buckets:        []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60, 120, 300},
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"target_state"}, // "suspended", "reinstated"
	)

	deletionPendingSeconds = metrics.NewGaugeVec(
		&metrics.GaugeOpts{
			Subsystem:      "milo_resourcemanager",
			Name:           "project_deletion_pending_seconds",
			Help:           "Seconds a deleted project has been waiting for its resources to drain. Absent once the project is gone.",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"project"},
	)

	deletionBlockingResources = metrics.NewGaugeVec(
		&metrics.GaugeOpts{
			Subsystem:      "milo_resourcemanager",
			Name:           "project_deletion_blocking_resources",
			Help:           "Number of resources still holding a deleted project's control plane open.",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"project"},
	)

	// eventEmitFailedTotal exists because event emission is best-effort: a
	// failure is logged and the reconcile continues. Scope is now carried
	// solely by impersonation, with no fallback, so a silent emit failure
	// means suspension events stop reaching tenant feeds with nothing else
	// to catch it. This counter is what makes that visible.
	eventEmitFailedTotal = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      "milo_resourcemanager",
			Name:           "project_lifecycle_event_emit_failed_total",
			Help:           "Total number of project lifecycle events that could not be emitted, by reason.",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"reason"}, // "Suspended", "Reinstated"
	)

	pauseFailedTotal = metrics.NewCounter(
		&metrics.CounterOpts{
			Subsystem:      "milo_resourcemanager",
			Name:           "project_suspension_pause_failed_total",
			Help:           "Total number of project suspension pause/patch failures.",
			StabilityLevel: metrics.ALPHA,
		},
	)

	suspensionEscalatedTotal = metrics.NewCounter(
		&metrics.CounterOpts{
			Subsystem:      "milo_resourcemanager",
			Name:           "project_suspension_escalated_total",
			Help:           "Total number of projects deleted after their suspension retention window elapsed.",
			StabilityLevel: metrics.ALPHA,
		},
	)

	suspensionWarningEmailsTotal = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      "milo_resourcemanager",
			Name:           "project_suspension_warning_emails_total",
			Help:           "Total number of suspension deletion warning e-mails sent, categorized by days remaining until deletion.",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"days_remaining"},
	)

	metricsMu            sync.Mutex
	activeSuspensionsMap = make(map[string]map[string]bool) // projectName -> active reasons
)

func init() {
	legacyregistry.MustRegister(activeSuspensionsTotal)
	legacyregistry.MustRegister(transitionDurationSeconds)
	legacyregistry.MustRegister(pauseFailedTotal)
	legacyregistry.MustRegister(eventEmitFailedTotal)
	legacyregistry.MustRegister(deletionPendingSeconds)
	legacyregistry.MustRegister(deletionBlockingResources)
	legacyregistry.MustRegister(suspensionEscalatedTotal)
	legacyregistry.MustRegister(suspensionWarningEmailsTotal)
}

// recordSuspensionEscalated counts a project that was deleted after its
// suspension retention window elapsed.
func recordSuspensionEscalated() {
	suspensionEscalatedTotal.Inc()
}

// recordSuspensionWarningEmailSent counts a suspension deletion warning
// e-mail sent at the given number of days remaining until deletion.
func recordSuspensionWarningEmailSent(daysRemaining int32) {
	suspensionWarningEmailsTotal.WithLabelValues(strconv.Itoa(int(daysRemaining))).Inc()
}

// recordProjectDeletionProgress publishes how long a deleted project has been
// waiting for its resources to drain, so a project that is never going to
// finish on its own can be alerted on.
func recordProjectDeletionProgress(projectName string, waiting time.Duration, blockers int) {
	deletionPendingSeconds.WithLabelValues(projectName).Set(waiting.Seconds())
	deletionBlockingResources.WithLabelValues(projectName).Set(float64(blockers))
}

// clearProjectDeletionProgress drops the deletion series for a project that has
// finished draining.
func clearProjectDeletionProgress(projectName string) {
	deletionPendingSeconds.DeleteLabelValues(projectName)
	deletionBlockingResources.DeleteLabelValues(projectName)
}

func reportActiveSuspensions(projectName string, reasons []string) {
	metricsMu.Lock()
	defer metricsMu.Unlock()

	// Decrement existing reasons for this project
	if oldReasons, exists := activeSuspensionsMap[projectName]; exists {
		for reason := range oldReasons {
			activeSuspensionsTotal.WithLabelValues(reason).Dec()
		}
	}

	// Increment new reasons
	newReasons := make(map[string]bool)
	for _, reason := range reasons {
		newReasons[reason] = true
	}
	activeSuspensionsMap[projectName] = newReasons

	for reason := range newReasons {
		activeSuspensionsTotal.WithLabelValues(reason).Inc()
	}
}

func clearActiveSuspensions(projectName string) {
	metricsMu.Lock()
	defer metricsMu.Unlock()

	if oldReasons, exists := activeSuspensionsMap[projectName]; exists {
		for reason := range oldReasons {
			activeSuspensionsTotal.WithLabelValues(reason).Dec()
		}
		delete(activeSuspensionsMap, projectName)
	}
}

func recordPauseFailure() {
	pauseFailedTotal.Inc()
}
