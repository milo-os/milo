package iam

import (
	"context"
	"fmt"
	"strings"
	"time"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	notificationv1alpha1 "go.miloapis.com/milo/pkg/apis/notification/v1alpha1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type PlatformInvitationController struct {
	Client                              client.Client
	PlatformInvitationEmailTemplateName string
	WaitlistRelatedResourcesNamespace   string
	EmailVariables                      PlatformInvitationEmailVariables
}

type PlatformInvitationEmailVariables struct {
	ActionUrl string
}

const platformInvitationUserEmailIndexKey = "iam.miloapis.com/useremailkey"

// +kubebuilder:rbac:groups=iam.miloapis.com,resources=platforminvitations,verbs=get;list;watch;update
// +kubebuilder:rbac:groups=iam.miloapis.com,resources=platforminvitations/status,verbs=update
// +kubebuilder:rbac:groups=iam.miloapis.com,resources=users,verbs=get;list;watch
// +kubebuilder:rbac:groups=notification.miloapis.com,resources=emails,verbs=get;list;watch;create

func (r *PlatformInvitationController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("controller", "PlatformInvitationController", "trigger", req.NamespacedName)
	log.Info("Starting reconciliation", "name", req.Name)

	// Get the PlatformInvitation
	pi := &iamv1alpha1.PlatformInvitation{}
	if err := r.Client.Get(ctx, req.NamespacedName, pi); err != nil {
		if errors.IsNotFound(err) {
			log.Info("PlatformInvitation not found, probably deleted. Skipping reconciliation")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get PlatformInvitation")
		return ctrl.Result{}, fmt.Errorf("failed to get PlatformInvitation: %w", err)
	}

	oldStatus := pi.Status.DeepCopy()

	// Check if the PlatformInvitation is ready
	if meta.IsStatusConditionTrue(pi.Status.Conditions, iamv1alpha1.PlatformInvitationReadyCondition) {
		log.Info("PlatformInvitation is ready, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	// Check if the PlatformInvitation should be requeued based on the schedule at
	if pi.Spec.ScheduleAt != nil && pi.Spec.ScheduleAt.After(time.Now()) {
		log.Info("PlatformInvitation should be requeued based on the schedule at", "scheduleAt", pi.Spec.ScheduleAt)
		if err := r.updatePlatformInvitationStatus(ctx, pi, metav1.Condition{
			Type:               iamv1alpha1.PlatformInvitationReadyCondition,
			Status:             metav1.ConditionFalse,
			Reason:             iamv1alpha1.PlatformInvitationReconciledReason,
			Message:            fmt.Sprintf("PlatformInvitation %s is scheduled, will be sent at %s", pi.Name, pi.Spec.ScheduleAt.Time.Format(time.RFC3339)),
			LastTransitionTime: metav1.NewTime(time.Now()),
		}, oldStatus); err != nil {
			log.Error(err, "Failed to update PlatformInvitation status")
			return ctrl.Result{}, fmt.Errorf("failed to update PlatformInvitation status: %w", err)
		}
		return ctrl.Result{RequeueAfter: time.Until(pi.Spec.ScheduleAt.Time)}, nil
	}

	// Verify that the user does not exists, as the user has may been created before the PlatformInvitation was sent.
	// Likely to happen for scheduled PlatformInvitations.
	userExists := false
	users := &iamv1alpha1.UserList{}
	if err := r.Client.List(ctx, users, client.MatchingFields{platformInvitationUserEmailIndexKey: strings.ToLower(pi.Spec.Email)}); err != nil {
		log.Error(err, "Failed to list Users by email")
		return ctrl.Result{}, fmt.Errorf("failed to list Users by email: %w", err)
	}
	if len(users.Items) > 0 {
		log.Info("User already exists, skipping sending email")
		userExists = true
	}

	conditionMessage := "PlatformInvitation is ready. Email not created as user already exists."
	if !userExists {
		// If here, the PlatformInvitation should be sent
		conditionMessage = "PlatformInvitation is ready. Email sent."

		errMsg := ""
		invitationErr := false

		// Create the PlatformInvitation email
		if err := r.createPlatformInvitationEmail(ctx, pi); err != nil {
			log.Error(err, "Failed to create PlatformInvitation email")
			// append to errMsg
			errMsg = fmt.Sprintf("%s\nfailed to create PlatformInvitation email: %v", errMsg, err)
			invitationErr = true
		}

		if invitationErr {
			if err := r.updatePlatformInvitationStatus(ctx, pi, metav1.Condition{
				Type:               iamv1alpha1.PlatformInvitationReadyCondition,
				Status:             metav1.ConditionFalse,
				Reason:             iamv1alpha1.PlatformInvitationReconciledReason,
				Message:            errMsg,
				LastTransitionTime: metav1.NewTime(time.Now()),
			}, oldStatus); err != nil {
				log.Error(err, "Failed to update PlatformInvitation status")
				return ctrl.Result{}, fmt.Errorf("failed to update PlatformInvitation status: %w", err)
			}

			return ctrl.Result{}, fmt.Errorf("failed to create PlatformInvitation: %v", errMsg)
		}

	}

	// PlatformInvitation is ready, update the status
	if err := r.updatePlatformInvitationStatus(ctx, pi, metav1.Condition{
		Type:               iamv1alpha1.PlatformInvitationReadyCondition,
		Status:             metav1.ConditionTrue,
		Reason:             iamv1alpha1.PlatformInvitationReconciledReason,
		Message:            conditionMessage,
		LastTransitionTime: metav1.NewTime(time.Now()),
	}, oldStatus); err != nil {
		log.Error(err, "Failed to update PlatformInvitation status")
		return ctrl.Result{}, fmt.Errorf("failed to update PlatformInvitation status: %w", err)
	}

	// Check if the PlatformInvitation is ready
	return reconcile.Result{}, nil
}

func (r *PlatformInvitationController) SetupWithManager(mgr ctrl.Manager) error {
	log := logf.FromContext(context.Background()).WithName("platforminvitation-setup-with-manager")
	log.Info("Setting up PlatformInvitationController with Manager")

	// Register field indexer for User email for efficient lookup
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &iamv1alpha1.User{}, platformInvitationUserEmailIndexKey, func(obj client.Object) []string {
		user := obj.(*iamv1alpha1.User)
		return []string{strings.ToLower(user.Spec.Email)}
	}); err != nil {
		log.Error(err, "Failed to set field index on User by .spec.email")
		return fmt.Errorf("failed to set field index on User by .spec.email: %w", err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&iamv1alpha1.PlatformInvitation{}).
		Named("platforminvitation").
		Complete(r)
}

func (r *PlatformInvitationController) updatePlatformInvitationStatus(ctx context.Context, pi *iamv1alpha1.PlatformInvitation, condition metav1.Condition, oldStatus *iamv1alpha1.PlatformInvitationStatus) error {
	log := logf.FromContext(ctx).WithName("platforminvitation-update-status")
	log.Info("Updating PlatformInvitation status", "status", pi.Status)

	meta.SetStatusCondition(&pi.Status.Conditions, condition)

	if !equality.Semantic.DeepEqual(oldStatus, &pi.Status) {
		log.Info("Updating PlatformInvitation status", "name", pi.GetName())
		if err := r.Client.Status().Update(ctx, pi); err != nil {
			log.Error(err, "Failed to update PlatformInvitation status")
			return fmt.Errorf("failed to update PlatformInvitation status: %w", err)
		}
	} else {
		log.Info("PlatformInvitation status unchanged, skipping update", "platformInvitation", pi.GetName())
	}

	return nil
}

// getDeterministicPlatformInvitationResourceName generates a deterministic name for a resource to create based on the PlatformInvitation.
func getDeterministicPlatformInvitationResourceName(pi iamv1alpha1.PlatformInvitation) string {
	return fmt.Sprintf("%s-%s", string(pi.GetUID()), pi.GetName())
}

// createPlatformInvitationEmail creates a PlatformInvitation email for the PlatformInvitation
// This is an idempotent operation, so it will not create a new PlatformInvitation email if one already exists
func (r *PlatformInvitationController) createPlatformInvitationEmail(ctx context.Context, pi *iamv1alpha1.PlatformInvitation) error {
	log := logf.FromContext(ctx).WithName("platforminvitation-create-platforminvitation-email")
	log.Info("Creating PlatformInvitation email", "name", pi.Name, "email", pi.Spec.Email)

	emailName := getDeterministicPlatformInvitationResourceName(*pi)
	log.Info("Email name", "emailName", emailName)

	// Check if the Email already exists (idempotency)
	existingEmail := &notificationv1alpha1.Email{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: emailName, Namespace: r.WaitlistRelatedResourcesNamespace}, existingEmail); err == nil {
		log.Info("Email already exists, skipping creation", "email", emailName)
		return nil
	} else if !errors.IsNotFound(err) {
		return fmt.Errorf("failed to check existing Email: %w", err)
	}

	username := fmt.Sprintf("%s %s", pi.Spec.GivenName, pi.Spec.FamilyName)
	if username == "" {
		username = pi.Spec.Email
	}

	emailVariables := []notificationv1alpha1.EmailVariable{
		{
			Name:  "UserName",
			Value: username,
		},
		{
			Name:  "ActionUrl",
			Value: r.EmailVariables.ActionUrl,
		},
	}

	// Create the Email
	email := &notificationv1alpha1.Email{
		TypeMeta: metav1.TypeMeta{
			Kind: "Email",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      emailName,
			Namespace: r.WaitlistRelatedResourcesNamespace,
		},
		Spec: notificationv1alpha1.EmailSpec{
			TemplateRef: notificationv1alpha1.TemplateReference{
				Name: r.PlatformInvitationEmailTemplateName,
			},
			Recipient: notificationv1alpha1.EmailRecipient{
				EmailAddress: pi.Spec.Email,
			},
			Variables: emailVariables,
			Priority:  notificationv1alpha1.EmailPriorityNormal,
		},
	}
	if err := r.Client.Create(ctx, email); err != nil {
		log.Error(err, "Failed to create Email")
		return fmt.Errorf("failed to create Email: %w", err)
	}

	return nil
}
