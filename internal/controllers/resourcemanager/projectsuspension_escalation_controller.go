package resourcemanager

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"time"

	equality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	notificationv1alpha1 "go.miloapis.com/milo/pkg/apis/notification/v1alpha1"
	resourcemanagerv1alpha "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	"go.miloapis.com/milo/pkg/features"
)

// minEscalationRequeue and maxEscalationRequeue bound how often the
// escalation controller wakes up while a project counts down its retention
// window. Nothing else changes on the Project while it stays suspended, so
// RequeueAfter is the only thing that drives the next check.
const (
	minEscalationRequeue = 30 * time.Second
	maxEscalationRequeue = 24 * time.Hour
	hoursPerDay          = 24 * time.Hour
)

// ProjectSuspensionEscalationController escalates a project that has been
// suspended past its configured retention window to deletion, sending
// countdown warning e-mails to the project's organization contact along the
// way. Escalation is the only sanctioned path from suspension to deletion:
// suspension itself never destroys data (see
// ProjectSuspensionPropagatorController); this controller only acts on the
// derived Suspended condition it produces.
type ProjectSuspensionEscalationController struct {
	Client        client.Client
	EventRecorder record.EventRecorder

	// RetentionWindowDays is how many days a project may remain suspended
	// before it is automatically deleted.
	RetentionWindowDays int

	// NotificationDaysRemaining lists the "days until deletion" thresholds
	// at which a warning e-mail is sent, in addition to the notice sent
	// immediately when the project becomes suspended.
	NotificationDaysRemaining []int

	// EmailTemplateName is the EmailTemplate used for the countdown warning.
	EmailTemplateName string

	// EmailNamespace is the namespace warning Email resources are created
	// in. Kept separate from the organization's own namespace so the sent
	// e-mails remain a stable, centralized audit trail regardless of
	// per-organization namespace lifecycle.
	EmailNamespace string

	// WarningExcludedReasons lists the suspension reasons for which countdown
	// deletion warning e-mails are NOT sent. Projects suspended for these
	// reasons still count down to auto-deletion after the retention window;
	// only the customer-facing warning e-mail is suppressed.
	WarningExcludedReasons []resourcemanagerv1alpha.ProjectSuspensionReason

	// warningExcludedReasonsSet is the O(1) lookup form of
	// WarningExcludedReasons, built once in SetupWithManager.
	warningExcludedReasonsSet map[resourcemanagerv1alpha.ProjectSuspensionReason]struct{}

	// notificationCheckpoints is the sorted-descending, deduplicated set of
	// "days until deletion" thresholds at which a warning e-mail is sent:
	// NotificationDaysRemaining plus RetentionWindowDays itself so that a
	// notice always goes out as soon as the project becomes suspended.
	// Computed once in SetupWithManager since RetentionWindowDays and
	// NotificationDaysRemaining never change after the controller starts.
	notificationCheckpoints []int32
}

// +kubebuilder:rbac:groups=resourcemanager.miloapis.com,resources=projects,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups=resourcemanager.miloapis.com,resources=projects/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=resourcemanager.miloapis.com,resources=organizations,verbs=get;list;watch
// +kubebuilder:rbac:groups=notification.miloapis.com,resources=emails,verbs=get;list;watch;create

func (r *ProjectSuspensionEscalationController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if !utilfeature.DefaultFeatureGate.Enabled(features.ProjectSuspension) {
		return ctrl.Result{}, nil
	}

	logger := log.FromContext(ctx)

	var project resourcemanagerv1alpha.Project
	if err := r.Client.Get(ctx, req.NamespacedName, &project); apierrors.IsNotFound(err) {
		return ctrl.Result{}, nil
	} else if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get project: %w", err)
	}

	if !project.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	suspendedCond := apimeta.FindStatusCondition(project.Status.Conditions, resourcemanagerv1alpha.ProjectSuspended)
	if suspendedCond == nil || suspendedCond.Status != metav1.ConditionTrue {
		return r.cancelEscalation(ctx, &project)
	}

	before := project.DeepCopy()

	// Determine whether countdown warning e-mails should be sent for this
	// suspension episode. A project can carry multiple suspension reasons;
	// warnings go out if ANY reason is not excluded (see
	// shouldSendWarningEmails). The reasons themselves are not copied here —
	// they already live in project.status.suspensions.
	sendWarningEmails := r.shouldSendWarningEmails(&project)

	if project.Status.SuspensionEscalation == nil {
		deletionAt := suspendedCond.LastTransitionTime.Add(time.Duration(r.RetentionWindowDays) * hoursPerDay)
		project.Status.SuspensionEscalation = &resourcemanagerv1alpha.ProjectSuspensionEscalationStatus{
			DeletionAt: metav1.NewTime(deletionAt),
		}
		apimeta.SetStatusCondition(&project.Status.Conditions, metav1.Condition{
			Type:               resourcemanagerv1alpha.ProjectPendingDeletion,
			Status:             metav1.ConditionTrue,
			Reason:             resourcemanagerv1alpha.ProjectPendingDeletionReason,
			Message:            fmt.Sprintf("Project is suspended and will be deleted at %s unless reinstated", deletionAt.UTC().Format(time.RFC3339)),
			ObservedGeneration: project.Generation,
		})
		if r.EventRecorder != nil {
			r.EventRecorder.Eventf(&project, "Warning", "SuspensionEscalationScheduled",
				"Project %s will be deleted at %s if suspension is not lifted", project.Name, deletionAt.UTC().Format(time.RFC3339))
		}
		if err := r.Client.Status().Patch(ctx, &project, client.MergeFrom(before)); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to schedule suspension escalation: %w", err)
		}
		before = project.DeepCopy()
	}

	deletionAt := project.Status.SuspensionEscalation.DeletionAt.Time
	remaining := time.Until(deletionAt)

	if remaining <= 0 {
		recordSuspensionEscalated()
		if r.EventRecorder != nil {
			r.EventRecorder.Eventf(&project, "Warning", "SuspensionEscalatedToDeletion",
				"Project %s remained suspended past its retention window and is being deleted", project.Name)
		}
		if err := r.Client.Delete(ctx, &project); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to delete project after suspension retention window elapsed: %w", err)
		}
		logger.Info("project suspension escalated to deletion", "project", project.Name, "deletionAt", deletionAt)
		return ctrl.Result{}, nil
	}

	daysRemaining := int32(math.Ceil(remaining.Hours() / 24))

	if sendWarningEmails {
		for _, checkpoint := range r.notificationCheckpoints {
			if daysRemaining > checkpoint || containsInt32(project.Status.SuspensionEscalation.NotifiedDaysRemaining, checkpoint) {
				continue
			}

			if err := r.sendEscalationWarningEmail(ctx, &project, checkpoint, deletionAt); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to send suspension deletion warning email: %w", err)
			}
		}
	}

	if !equality.Semantic.DeepEqual(project.Status, before.Status) {
		if err := r.Client.Status().Patch(ctx, &project, client.MergeFrom(before)); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update suspension escalation status: %w", err)
		}
	}

	return ctrl.Result{RequeueAfter: nextRequeue(deletionAt, r.notificationCheckpoints, project.Status.SuspensionEscalation.NotifiedDaysRemaining)}, nil
}

// cancelEscalation clears any pending escalation state when a project is no
// longer suspended. Reinstatement is always non-destructive, so a project
// that is un-suspended must never carry over a scheduled deletion.
func (r *ProjectSuspensionEscalationController) cancelEscalation(ctx context.Context, project *resourcemanagerv1alpha.Project) (ctrl.Result, error) {
	if project.Status.SuspensionEscalation == nil &&
		apimeta.FindStatusCondition(project.Status.Conditions, resourcemanagerv1alpha.ProjectPendingDeletion) == nil {
		return ctrl.Result{}, nil
	}

	before := project.DeepCopy()
	project.Status.SuspensionEscalation = nil
	apimeta.SetStatusCondition(&project.Status.Conditions, metav1.Condition{
		Type:               resourcemanagerv1alpha.ProjectPendingDeletion,
		Status:             metav1.ConditionFalse,
		Reason:             resourcemanagerv1alpha.ProjectPendingDeletionClearedReason,
		Message:            "Project is no longer suspended; scheduled deletion was cancelled",
		ObservedGeneration: project.Generation,
	})

	if equality.Semantic.DeepEqual(project.Status, before.Status) {
		return ctrl.Result{}, nil
	}

	if r.EventRecorder != nil {
		r.EventRecorder.Eventf(project, "Normal", "SuspensionEscalationCancelled",
			"Project %s was reinstated; scheduled deletion was cancelled", project.Name)
	}

	if err := r.Client.Status().Patch(ctx, project, client.MergeFrom(before)); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to cancel suspension escalation: %w", err)
	}
	return ctrl.Result{}, nil
}

// sendEscalationWarningEmail notifies the project's organization contact that
// the project will be deleted in daysRemaining days, then records the
// checkpoint as notified so it is not sent again.
func (r *ProjectSuspensionEscalationController) sendEscalationWarningEmail(ctx context.Context, project *resourcemanagerv1alpha.Project, daysRemaining int32, deletionAt time.Time) error {
	logger := log.FromContext(ctx)

	var org resourcemanagerv1alpha.Organization
	if err := r.Client.Get(ctx, client.ObjectKey{Name: project.Spec.OwnerRef.Name}, &org); err != nil {
		return fmt.Errorf("failed to get organization %q: %w", project.Spec.OwnerRef.Name, err)
	}

	if !resourcemanagerv1alpha.IsOrganizationContactInfoComplete(org.Spec.ContactInfo) {
		if r.EventRecorder != nil {
			r.EventRecorder.Eventf(project, "Warning", "SuspensionNotificationSkipped",
				"Organization %s has no contact e-mail; skipping suspension deletion warning (%d days remaining)", org.Name, daysRemaining)
		}
		logger.Info("skipping suspension deletion warning email, organization has no contact info", "project", project.Name, "organization", org.Name)
		return nil
	}

	organizationDisplayName := org.Annotations["kubernetes.io/display-name"]
	if organizationDisplayName == "" {
		organizationDisplayName = org.Name
	}

	emailName := getDeterministicEscalationEmailName(project, deletionAt, daysRemaining)

	existingEmail := &notificationv1alpha1.Email{}
	err := r.Client.Get(ctx, client.ObjectKey{Name: emailName, Namespace: r.EmailNamespace}, existingEmail)
	if err == nil {
		// Already sent; just record the checkpoint.
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to check existing suspension deletion warning email: %w", err)
	} else {
		email := &notificationv1alpha1.Email{
			ObjectMeta: metav1.ObjectMeta{
				Name:      emailName,
				Namespace: r.EmailNamespace,
				Labels: map[string]string{
					resourcemanagerv1alpha.OrganizationNameLabel: project.Spec.OwnerRef.Name,
					resourcemanagerv1alpha.ProjectNameLabel:      project.Name,
					resourcemanagerv1alpha.ProjectUIDLabel:       string(project.UID),
					notificationv1alpha1.NotificationKindLabel:   notificationv1alpha1.NotificationKindProjectSuspensionWarning,
				},
			},
			Spec: notificationv1alpha1.EmailSpec{
				TemplateRef: notificationv1alpha1.TemplateReference{
					Name: r.EmailTemplateName,
				},
				Recipient: notificationv1alpha1.EmailRecipient{
					EmailAddress: org.Spec.ContactInfo.Email,
				},
				Variables: []notificationv1alpha1.EmailVariable{
					{Name: "ProjectName", Value: project.Name},
					{Name: "OrganizationName", Value: organizationDisplayName},
					{Name: "DaysUntilDeletion", Value: strconv.Itoa(int(daysRemaining))},
					{Name: "DeletionDate", Value: deletionAt.UTC().Format(time.RFC3339)},
				},
				Priority: notificationv1alpha1.EmailPriorityHigh,
			},
		}
		if err := r.Client.Create(ctx, email); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create suspension deletion warning email: %w", err)
		}
	}

	project.Status.SuspensionEscalation.NotifiedDaysRemaining = append(project.Status.SuspensionEscalation.NotifiedDaysRemaining, daysRemaining)
	sort.Slice(project.Status.SuspensionEscalation.NotifiedDaysRemaining, func(i, j int) bool {
		return project.Status.SuspensionEscalation.NotifiedDaysRemaining[i] > project.Status.SuspensionEscalation.NotifiedDaysRemaining[j]
	})

	recordSuspensionWarningEmailSent(daysRemaining)

	if r.EventRecorder != nil {
		r.EventRecorder.Eventf(project, "Normal", "SuspensionDeletionWarningSent",
			"Sent suspension deletion warning to %s: %d days remaining", org.Name, daysRemaining)
	}

	return nil
}

// shouldSendWarningEmails reports whether countdown warning e-mails should be
// sent for a suspended project. A project can carry multiple suspension reasons
// (status.suspensions is a list); warnings go out if ANY reason is not in
// WarningExcludedReasons. An empty reason list (reason not yet propagated, or a
// status that predates this field) defaults to sending, so a race between the
// propagator and this controller never silently drops a notification.
func (r *ProjectSuspensionEscalationController) shouldSendWarningEmails(project *resourcemanagerv1alpha.Project) bool {
	if len(project.Status.Suspensions) == 0 {
		return true
	}
	for _, s := range project.Status.Suspensions {
		if _, excluded := r.warningExcludedReasonsSet[s.Reason]; !excluded {
			return true
		}
	}
	return false
}

// computeNotificationCheckpoints returns the sorted-descending, deduplicated
// set of "days until deletion" thresholds at which a warning e-mail is sent:
// the configured notificationDays, plus retentionWindowDays itself so that a
// notice always goes out as soon as the project becomes suspended.
func computeNotificationCheckpoints(retentionWindowDays int, notificationDays []int) []int32 {
	seen := map[int32]bool{}
	var checkpoints []int32

	add := func(days int32) {
		if days <= 0 || seen[days] {
			return
		}
		seen[days] = true
		checkpoints = append(checkpoints, days)
	}

	add(int32(retentionWindowDays))
	for _, days := range notificationDays {
		add(int32(days))
	}

	sort.Slice(checkpoints, func(i, j int) bool { return checkpoints[i] > checkpoints[j] })
	return checkpoints
}

// nextRequeue computes when the controller should next reconcile a suspended
// project: either the deletion deadline itself, or the moment the next
// not-yet-sent checkpoint is crossed, whichever comes first.
func nextRequeue(deletionAt time.Time, checkpoints []int32, notified []int32) time.Duration {
	next := deletionAt

	for _, checkpoint := range checkpoints {
		if containsInt32(notified, checkpoint) {
			continue
		}
		crossingTime := deletionAt.Add(-time.Duration(checkpoint) * hoursPerDay)
		if crossingTime.After(time.Now()) && crossingTime.Before(next) {
			next = crossingTime
		}
	}

	requeue := max(min(time.Until(next), maxEscalationRequeue), minEscalationRequeue)
	return requeue
}

func containsInt32(haystack []int32, needle int32) bool {
	return slices.Contains(haystack, needle)
}

// getDeterministicEscalationEmailName generates a deterministic name for the
// warning Email resource so repeated reconciles do not send duplicate
// e-mails for the same checkpoint. deletionAt (fixed once per suspension
// episode) is folded in so that a project reinstated and later re-suspended
// gets a fresh set of warning e-mails instead of colliding with — and being
// silently swallowed by — the Email resources from its previous suspension,
// which share the same project UID and "days remaining" checkpoints.
func getDeterministicEscalationEmailName(project *resourcemanagerv1alpha.Project, deletionAt time.Time, daysRemaining int32) string {
	return fmt.Sprintf("project-suspension-deletion-warning-%s-%d-%dd", project.GetUID(), deletionAt.UTC().Unix(), daysRemaining)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ProjectSuspensionEscalationController) SetupWithManager(mgr ctrl.Manager) error {
	if err := r.validateConfig(); err != nil {
		return fmt.Errorf("invalid project suspension escalation configuration: %w", err)
	}

	r.EventRecorder = mgr.GetEventRecorderFor("project-suspension-escalation-controller")
	r.notificationCheckpoints = computeNotificationCheckpoints(r.RetentionWindowDays, r.NotificationDaysRemaining)
	r.warningExcludedReasonsSet = make(map[resourcemanagerv1alpha.ProjectSuspensionReason]struct{}, len(r.WarningExcludedReasons))
	for _, reason := range r.WarningExcludedReasons {
		r.warningExcludedReasonsSet[reason] = struct{}{}
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&resourcemanagerv1alpha.Project{}).
		Named("project-suspension-escalation").
		Complete(r)
}

// validateConfig rejects non-positive configuration values before the
// controller starts. A non-positive RetentionWindowDays would put deletionAt
// at or before the suspension itself — skipping the time-boxed, reversible
// window entirely — and a non-positive NotificationDaysRemaining entry would
// be silently dropped by computeNotificationCheckpoints, leaving an operator
// to believe a warning is configured when none will ever be sent.
func (r *ProjectSuspensionEscalationController) validateConfig() error {
	if r.RetentionWindowDays <= 0 {
		return fmt.Errorf("retention window days must be positive, got %d", r.RetentionWindowDays)
	}
	for _, days := range r.NotificationDaysRemaining {
		if days <= 0 {
			return fmt.Errorf("notification days must all be positive, got %d in %v", days, r.NotificationDaysRemaining)
		}
	}
	for _, reason := range r.WarningExcludedReasons {
		switch reason {
		case resourcemanagerv1alpha.ReasonFraud,
			resourcemanagerv1alpha.ReasonAbuse,
			resourcemanagerv1alpha.ReasonBilling,
			resourcemanagerv1alpha.ReasonCompliance,
			resourcemanagerv1alpha.ReasonAdministrative:
			// valid
		default:
			return fmt.Errorf("unknown excluded warning reason %q (must be one of Fraud, Abuse, Billing, Compliance, Administrative)", reason)
		}
	}
	return nil
}
