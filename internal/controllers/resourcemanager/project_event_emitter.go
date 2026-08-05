package resourcemanager

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"sigs.k8s.io/controller-runtime/pkg/log"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	resourcemanagerv1alpha "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
)

const (
	// projectKind is the Project Kind. It doubles as the parent-type value
	// denoting a Project scope, which is what the events proxy matches on.
	// The resourcemanager API package exposes no Kind constant to reference.
	projectKind = "Project"

	// clusterScopedEventNamespace is where events regarding a cluster-scoped
	// object are recorded. Project is cluster-scoped, so its events have no
	// natural namespace; client-go's recorder defaults these to "default" and
	// we keep that placement, so scope annotations rather than namespaces stay
	// the thing activity feeds filter on.
	//
	// The apiserver bootstraps this namespace — cmd/milo/apiserver/server.go
	// lists metav1.NamespaceDefault in SystemNamespaces — so it exists and is
	// safe to target.
	clusterScopedEventNamespace = metav1.NamespaceDefault
)

// projectEventEmitter emits consumer-facing Project lifecycle events.
// Implemented by ProjectEventEmitter; stubbed in tests.
type projectEventEmitter interface {
	Emit(ctx context.Context, project *resourcemanagerv1alpha.Project, reason, note string) error
}

// ProjectEventEmitter creates Project lifecycle events through a client that
// impersonates the project's parent context.
//
// Controllers authenticate as themselves and carry no
// parent-type/parent-name extras, so events they create through the shared
// manager EventRecorder are stored with no scope annotations and are invisible
// to tenant-scoped activity feed queries. Rather than teaching the events
// proxy a second way to derive scope, this emitter supplies the parent context
// the proxy already reads — the controller knows which Project it is acting
// on, so the parent is asserted rather than inferred from a request field.
//
// The manager's shared EventRecorder cannot do this: it is backed by one
// broadcaster over one rest.Config, i.e. one static identity, while parent
// context varies per Project.
//
// Events are created as events.k8s.io/v1 rather than core/v1 so the objects
// genuinely carry the Regarding/Note fields the ActivityPolicy eventRules read
// (config/services/activity/policies/resourcemanager/project-policy.yaml),
// with no dependence on a core/v1 -> events.k8s.io/v1 conversion in between.
//
// Deployment prerequisite: the controller-manager must hold `impersonate` on
// the configured impersonateUser and on
// userextras/iam.miloapis.com/parent-type and .../parent-name. No such RBAC
// exists under config/ today, and in test-infra the controller-manager
// authenticates as `admin` in system:masters, which bypasses authorization —
// so this works there and would fail anywhere with real RBAC until that
// binding is added.
type ProjectEventEmitter struct {
	// baseConfig is the controller-manager's own rest config; each emit
	// derives an impersonating copy from it.
	baseConfig *rest.Config

	// impersonateUser is the principal asserted in Impersonate-User. The API
	// server rejects impersonation extras unless a user is impersonated too,
	// so this must be set even though the parent extras are the part that
	// determines scope.
	impersonateUser string

	// reportingInstance identifies this controller instance in emitted
	// events. Kubernetes caps it at 128 characters.
	reportingInstance string

	// newClient builds a clientset from a config. Overridable in tests.
	newClient func(*rest.Config) (kubernetes.Interface, error)
}

// NewProjectEventEmitter returns an emitter that impersonates impersonateUser
// plus the target project's parent context.
func NewProjectEventEmitter(baseConfig *rest.Config, impersonateUser, reportingInstance string) *ProjectEventEmitter {
	return &ProjectEventEmitter{
		baseConfig:        baseConfig,
		impersonateUser:   impersonateUser,
		reportingInstance: reportingInstance,
		newClient: func(cfg *rest.Config) (kubernetes.Interface, error) {
			return kubernetes.NewForConfig(cfg)
		},
	}
}

// clientForProject builds a clientset whose requests carry the project's
// parent context as impersonation extras.
//
// A client is built per emit rather than cached per project. Suspension
// transitions are rare, so the allocation is not worth a cache that would need
// invalidation and a bound; revisit if this emitter is reused for a
// high-frequency event.
func (e *ProjectEventEmitter) clientForProject(projectName string) (kubernetes.Interface, error) {
	cfg := rest.CopyConfig(e.baseConfig)
	cfg.Impersonate = rest.ImpersonationConfig{
		UserName: e.impersonateUser,
		Extra: map[string][]string{
			iamv1alpha1.ParentKindExtraKey: {projectKind},
			iamv1alpha1.ParentNameExtraKey: {projectName},
		},
	}
	return e.newClient(cfg)
}

// Emit creates an events.k8s.io/v1 Event regarding project.
//
// note reaches tenant-facing activity feeds, so callers must keep it free of
// internal admin detail — see recordTransition.
func (e *ProjectEventEmitter) Emit(ctx context.Context, project *resourcemanagerv1alpha.Project, reason, note string) error {
	if project == nil {
		return fmt.Errorf("project is required")
	}

	cs, err := e.clientForProject(project.Name)
	if err != nil {
		return fmt.Errorf("failed to build impersonating client for project %q: %w", project.Name, err)
	}

	event := &eventsv1.Event{
		ObjectMeta: metav1.ObjectMeta{
			// Names are assigned client-side rather than via GenerateName,
			// matching how client-go's own event recorder builds them. Two
			// transitions for one project can't share a nanosecond, and this
			// keeps the emitter independent of server-side name generation.
			Name:      fmt.Sprintf("%s.%x", project.Name, time.Now().UnixNano()),
			Namespace: clusterScopedEventNamespace,
		},
		EventTime:           metav1.NowMicro(),
		ReportingController: controllerOwnerName,
		ReportingInstance:   e.reportingInstance,
		Action:              reason,
		Reason:              reason,
		Regarding: corev1.ObjectReference{
			APIVersion: resourcemanagerv1alpha.GroupVersion.String(),
			Kind:       projectKind,
			Name:       project.Name,
			UID:        project.UID,
		},
		Note: note,
		Type: corev1.EventTypeNormal,
	}

	if _, err := cs.EventsV1().Events(clusterScopedEventNamespace).Create(ctx, event, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create %q event for project %q: %w", reason, project.Name, err)
	}

	return nil
}

// recordTransition emits the consumer-facing Suspended/Reinstated events for
// a Project. The Suspended message carries only the suspension reason
// categories (e.g. "Abuse") — never ProjectSuspension resource names,
// requestedBy, or description, which are internal admin details that must not
// leak to tenant-facing activity feeds.
//
// Messages are kept bare — no project name, no verb, no field label — because
// the ActivityPolicy summary
// (config/services/activity/policies/resourcemanager/project-policy.yaml)
// reconstructs the sentence from the structured event.regarding.name field and
// supplies the "category:" label itself. Labelling here too would render it
// twice.
func recordTransition(ctx context.Context, emitter projectEventEmitter, project *resourcemanagerv1alpha.Project, wasSuspended, isSuspendedNow bool, activeSuspensionReasons []string, latestSuspensionTime metav1.Time) {
	logger := log.FromContext(ctx)

	emit := func(reason, note string) {
		if emitter == nil {
			return
		}
		// Event emission is best-effort: the status patch has already
		// succeeded, so failing the reconcile here would only re-run it. Log
		// and move on, matching the fire-and-forget behaviour of the manager
		// EventRecorder this replaced.
		if err := emitter.Emit(ctx, project, reason, note); err != nil {
			eventEmitFailedTotal.WithLabelValues(reason).Inc()
			logger.Error(err, "failed to emit project lifecycle event",
				"project", project.Name, "reason", reason)
		}
	}

	if !wasSuspended && isSuspendedNow {
		emit("Suspended", strings.Join(activeSuspensionReasons, ", "))
		if !latestSuspensionTime.IsZero() {
			transitionDurationSeconds.WithLabelValues("suspended").Observe(time.Since(latestSuspensionTime.Time).Seconds())
		}
	} else if wasSuspended && !isSuspendedNow {
		emit("Reinstated", "")
		transitionDurationSeconds.WithLabelValues("reinstated").Observe(0)
	}
}
