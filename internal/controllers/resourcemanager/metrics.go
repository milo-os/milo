package resourcemanager

import (
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/component-base/metrics"
	legacyregistry "k8s.io/component-base/metrics/legacyregistry"

	resourcemanagerv1alpha "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
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

	pauseFailedTotal = metrics.NewCounter(
		&metrics.CounterOpts{
			Subsystem:      "milo_resourcemanager",
			Name:           "project_suspension_pause_failed_total",
			Help:           "Total number of project suspension pause/patch failures.",
			StabilityLevel: metrics.ALPHA,
		},
	)

	metricsMu            sync.Mutex
	activeSuspensionsMap = make(map[string]map[string]bool) // projectName -> active reasons
)

func init() {
	legacyregistry.MustRegister(activeSuspensionsTotal)
	legacyregistry.MustRegister(transitionDurationSeconds)
	legacyregistry.MustRegister(pauseFailedTotal)
	legacyregistry.MustRegister(deletionPendingSeconds)
	legacyregistry.MustRegister(deletionBlockingResources)
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

func recordTransition(eventRecorder record.EventRecorder, project *resourcemanagerv1alpha.Project, wasSuspended, isSuspendedNow bool, activeSuspensionNames []string, latestSuspensionTime metav1.Time) {
	if !wasSuspended && isSuspendedNow {
		if eventRecorder != nil {
			eventRecorder.Eventf(project, "Normal", "Suspended", "Project %s has been suspended due to active suspensions: %s", project.Name, strings.Join(activeSuspensionNames, ", "))
		}
		if !latestSuspensionTime.IsZero() {
			transitionDurationSeconds.WithLabelValues("suspended").Observe(time.Since(latestSuspensionTime.Time).Seconds())
		}
	} else if wasSuspended && !isSuspendedNow {
		if eventRecorder != nil {
			eventRecorder.Eventf(project, "Normal", "Reinstated", "Project %s has been reinstated", project.Name)
		}
		transitionDurationSeconds.WithLabelValues("reinstated").Observe(0)
	}
}
