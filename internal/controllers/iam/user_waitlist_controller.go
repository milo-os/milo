package iam

import (
	"context"
	"fmt"
	"strings"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	notificationv1alpha1 "go.miloapis.com/milo/pkg/apis/notification/v1alpha1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// UserWaitlistController watches User resources and ensures waitlist emails are sent exactly once per approval state.
type UserWaitlistController struct {
	Client                    client.Client
	SystemNamespace           string
	PendingEmailTemplateName  string
	ApprovedEmailTemplateName string
	RejectedEmailTemplateName string
}

// +kubebuilder:rbac:groups=iam.miloapis.com,resources=users,verbs=get;list;watch
// +kubebuilder:rbac:groups=iam.miloapis.com,resources=users/status,verbs=update
// +kubebuilder:rbac:groups=notification.miloapis.com,resources=emails,verbs=get;list;watch;create

// Reconcile is the main reconciliation loop for UserWaitlistController.
func (r *UserWaitlistController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx).WithName("user-waitlist-controller").WithValues("user", req.Name)
	log.Info("Starting reconciliation")

	user := &iamv1alpha1.User{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: req.Name}, user); err != nil {
		if errors.IsNotFound(err) {
			log.Info("User not found, probably deleted")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get User: %w", err)
	}

	if !user.DeletionTimestamp.IsZero() {
		log.Info("User is being deleted, skipping email reconciliation")
		return ctrl.Result{}, nil
	}

	statusCondition := getEmailStatusCondition(user)
	if statusCondition == "" {
		log.Info("User has empty or unknown registration approval state, skipping waitlist email")
		return ctrl.Result{}, nil
	}
	if meta.IsStatusConditionTrue(user.Status.Conditions, string(statusCondition)) {
		log.Info("Waitlist email already sent, skipping")
		return ctrl.Result{}, nil
	}

	originalStatus := user.Status.DeepCopy()

	// Send the waitlist email
	mailError := false
	errMsg := ""
	emailVariables := r.getEmailVariables(statusCondition, user)
	emailTemplateName := r.getEmailTemplateName(statusCondition)
	if err := r.ensureWaitlistEmailSent(ctx, user, statusCondition, emailTemplateName, emailVariables); err != nil {
		mailError = true
		errMsg = err.Error()
	}

	var condition metav1.Condition
	if mailError {
		condition = metav1.Condition{
			Type:               string(statusCondition),
			Status:             metav1.ConditionFalse,
			LastTransitionTime: metav1.Now(),
			Reason:             iamv1alpha1.UserWaitlistEmailSentReason,
			Message:            errMsg,
		}
	} else {
		condition = metav1.Condition{
			Type:               string(statusCondition),
			Status:             metav1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
			Reason:             iamv1alpha1.UserWaitlistEmailSentReason,
			Message:            "Waitlist email sent successfully",
		}
	}

	meta.SetStatusCondition(&user.Status.Conditions, condition)

	if !equality.Semantic.DeepEqual(originalStatus, &user.Status) {
		log.Info("Updating User status with waitlist email conditions")
		if err := r.Client.Status().Update(ctx, user); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update User status: %w", err)
		}
	} else {
		log.Info("User status unchanged, skipping status update")
	}

	if mailError {
		return ctrl.Result{}, fmt.Errorf("failed to send waitlist email: %s", errMsg)
	}

	return ctrl.Result{}, nil
}

// SetupWithManager wires the controller into the manager.
func (r *UserWaitlistController) SetupWithManager(mgr ctrl.Manager) error {
	// TODO: If User quantity increases significantly, consider
	// adding a predicate to only reconcile when the registration approval field has changed.
	return ctrl.NewControllerManagedBy(mgr).
		For(&iamv1alpha1.User{}).
		Named("user-waitlist").
		Complete(r)
}

// ensureWaitlistEmailSent ensures that the waitlist email is sent for the given user and condition.
func (r *UserWaitlistController) ensureWaitlistEmailSent(ctx context.Context, user *iamv1alpha1.User, condition iamv1alpha1.UserWaitlistEmailSentCondition, templateName string, emailVariables []notificationv1alpha1.EmailVariable) error {
	log := log.FromContext(ctx).WithName("ensure-waitlist-email-sent").WithValues("user", user.Name, "condition", condition)

	emailName := getDeterministicWaitlistEmailName(user, condition)
	log.Info("Email name", "emailName", emailName)

	// Check if the Email already exists (idempotency)
	// This should not be neccessary, but if the status update fails, we might end up creating the email again.
	existingEmail := &notificationv1alpha1.Email{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: emailName, Namespace: r.SystemNamespace}, existingEmail); err == nil {
		log.Info("Email already exists, skipping creation", "email", emailName)
		return nil
	} else if !errors.IsNotFound(err) {
		return fmt.Errorf("failed to check existing Email: %w", err)
	}

	email := &notificationv1alpha1.Email{
		TypeMeta: metav1.TypeMeta{
			Kind: "Email",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      emailName,
			Namespace: r.SystemNamespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: iamv1alpha1.SchemeGroupVersion.String(),
					Kind:       "User",
					Name:       user.Name,
					UID:        user.UID,
				},
			},
		},
		Spec: notificationv1alpha1.EmailSpec{
			TemplateRef: notificationv1alpha1.TemplateReference{
				Name: templateName,
			},
			Recipient: notificationv1alpha1.EmailRecipient{
				EmailAddress: user.Spec.Email,
			},
			Variables: emailVariables,
			Priority:  notificationv1alpha1.EmailPriorityNormal,
		},
	}

	// Create the Email resource
	if err := r.Client.Create(ctx, email); err != nil {
		log.Error(err, "failed to create Email resource", "email", email)
		return fmt.Errorf("failed to create Email resource: %w", err)
	}

	return nil
}

func getDeterministicWaitlistEmailName(user *iamv1alpha1.User, condition iamv1alpha1.UserWaitlistEmailSentCondition) string {
	return fmt.Sprintf("%s-%s-%s", string(user.GetUID()), user.GetName(), strings.ToLower(string(condition)))
}

// getEmailStatusCondition returns the status condition for the email based on the user's registration approval state.
func getEmailStatusCondition(user *iamv1alpha1.User) iamv1alpha1.UserWaitlistEmailSentCondition {
	switch user.Status.RegistrationApproval {
	case iamv1alpha1.RegistrationApprovalStatePending:
		return "" // No email for pending users, as waitilist is disabled
	case iamv1alpha1.RegistrationApprovalStateApproved:
		return iamv1alpha1.UserWaitlistApprovedEmailSentCondition
	case iamv1alpha1.RegistrationApprovalStateRejected:
		return iamv1alpha1.UserWaitlistRejectedEmailSentCondition
	default:
		return ""
	}
}

func (r *UserWaitlistController) getEmailTemplateName(condition iamv1alpha1.UserWaitlistEmailSentCondition) string {
	switch condition {
	case iamv1alpha1.UserWaitlistPendingEmailSentCondition:
		return r.PendingEmailTemplateName
	case iamv1alpha1.UserWaitlistApprovedEmailSentCondition:
		return r.ApprovedEmailTemplateName
	case iamv1alpha1.UserWaitlistRejectedEmailSentCondition:
		return r.RejectedEmailTemplateName
	}
	return ""
}

func (r *UserWaitlistController) getEmailVariables(condition iamv1alpha1.UserWaitlistEmailSentCondition, user *iamv1alpha1.User) []notificationv1alpha1.EmailVariable {
	userName := user.Spec.GivenName
	if userName == "" {
		userName = user.Spec.Email
	}

	switch condition {
	case iamv1alpha1.UserWaitlistApprovedEmailSentCondition:
		return []notificationv1alpha1.EmailVariable{
			{
				Name:  "UserName",
				Value: userName,
			},
		}
	default:
		return []notificationv1alpha1.EmailVariable{
			{
				Name:  "UserName",
				Value: userName,
			},
		}
	}
}
