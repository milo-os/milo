package iam

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"go.miloapis.com/milo/internal/emaildomain"
	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	"go.miloapis.com/milo/pkg/features"
)

const (
	userEmailDomainIndexKey         = "spec.email.domain"
	organizationEmailDomainIndexKey = "spec.emailDomainJoining.domains"

	emailDomainOfferNamePrefix = "email-domain-"

	defaultEmailDomainRecheckInterval = 10 * time.Minute
)

// EmailDomainJoiningController offers membership of an organization to every
// User at one of its spec.emailDomainJoining domains, as UserInvitations
// labelled emaildomain.JoinMethodLabel. It offers only domains that one of
// the organization's verified OrganizationDomains, or a verified Domain in one
// of its projects, covers, reports that
// in the EmailDomainsVerified condition, and removes pending offers for
// domains that are no longer listed or verified.
//
// Nobody is offered membership twice: a person who already has any
// invitation to the organization, including a declined one, or who is already
// a member, is skipped. Someone who leaves or is removed has no invitation
// left, so they are offered membership again.
type EmailDomainJoiningController struct {
	Client   client.Client
	Verifier *emaildomain.Verifier

	// RecheckInterval is how often an organization's Domains are checked
	// again, since Milo doesn't watch Domains in project control planes.
	RecheckInterval time.Duration
}

// +kubebuilder:rbac:groups=resourcemanager.miloapis.com,resources=organizations,verbs=get;list;watch
// +kubebuilder:rbac:groups=resourcemanager.miloapis.com,resources=organizations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=resourcemanager.miloapis.com,resources=organizationmemberships,verbs=get;list;watch
// +kubebuilder:rbac:groups=resourcemanager.miloapis.com,resources=projects,verbs=get;list;watch
// +kubebuilder:rbac:groups=resourcemanager.miloapis.com,resources=organizationdomains,verbs=get;list;watch
// +kubebuilder:rbac:groups=iam.miloapis.com,resources=users,verbs=get;list;watch
// +kubebuilder:rbac:groups=iam.miloapis.com,resources=userinvitations,verbs=get;list;watch;create;delete

func (r *EmailDomainJoiningController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithName("emaildomainjoining-reconciler")

	if !utilfeature.DefaultFeatureGate.Enabled(features.EmailDomainJoining) {
		return ctrl.Result{}, nil
	}

	org := &resourcemanagerv1alpha1.Organization{}
	if err := r.Client.Get(ctx, req.NamespacedName, org); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if org.DeletionTimestamp != nil {
		return ctrl.Result{}, nil
	}

	usable, verifyErr := r.reconcileCondition(ctx, org)
	if verifyErr != nil {
		// Keep the existing offers: a failed check says nothing about whether
		// the Domains are still verified.
		return ctrl.Result{}, verifyErr
	}

	namespace := fmt.Sprintf("organization-%s", org.Name)
	var invitations iamv1alpha1.UserInvitationList
	if err := r.Client.List(ctx, &invitations, client.InNamespace(namespace)); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list UserInvitations: %w", err)
	}

	invited := make(map[string]bool, len(invitations.Items))
	for i := range invitations.Items {
		ui := &invitations.Items[i]
		if isEmailDomainOffer(ui) &&
			ui.Spec.State == iamv1alpha1.UserInvitationStatePending &&
			!slices.Contains(usable, ui.Labels[emaildomain.EmailDomainLabel]) {
			if err := r.Client.Delete(ctx, ui); err != nil && !errors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("failed to delete email domain offer %q: %w", ui.Name, err)
			}
			log.Info("Deleted email domain offer", "userInvitation", ui.Name, "domain", ui.Labels[emaildomain.EmailDomainLabel])
			continue
		}
		invited[strings.ToLower(ui.Spec.Email)] = true
	}

	if len(usable) > 0 {
		var memberships resourcemanagerv1alpha1.OrganizationMembershipList
		if err := r.Client.List(ctx, &memberships, client.InNamespace(namespace)); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to list OrganizationMemberships: %w", err)
		}
		members := make(map[string]bool, len(memberships.Items))
		for _, m := range memberships.Items {
			members[m.Spec.UserRef.Name] = true
		}

		for _, domain := range usable {
			var users iamv1alpha1.UserList
			if err := r.Client.List(ctx, &users, client.MatchingFields{userEmailDomainIndexKey: domain}); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to list Users at %q: %w", domain, err)
			}
			for i := range users.Items {
				user := &users.Items[i]
				if user.DeletionTimestamp != nil || members[user.Name] || invited[strings.ToLower(user.Spec.Email)] {
					continue
				}
				if err := r.createOffer(ctx, org, namespace, domain, user); err != nil {
					return ctrl.Result{}, err
				}
				invited[strings.ToLower(user.Spec.Email)] = true
			}
		}
	}

	if org.Spec.EmailDomainJoining == nil {
		return ctrl.Result{}, nil
	}
	return ctrl.Result{RequeueAfter: r.recheckInterval()}, nil
}

// reconcileCondition checks the organization's Domains, records the result in
// the EmailDomainsVerified condition, and returns the listed domains that a
// verified Domain covers.
func (r *EmailDomainJoiningController) reconcileCondition(ctx context.Context, org *resourcemanagerv1alpha1.Organization) ([]string, error) {
	joining := org.Spec.EmailDomainJoining
	if joining == nil {
		if meta.RemoveStatusCondition(&org.Status.Conditions, resourcemanagerv1alpha1.OrganizationConditionEmailDomainsVerified) {
			if err := r.Client.Status().Update(ctx, org); err != nil {
				return nil, fmt.Errorf("failed to update Organization status: %w", err)
			}
		}
		return nil, nil
	}

	condition := metav1.Condition{
		Type:               resourcemanagerv1alpha1.OrganizationConditionEmailDomainsVerified,
		ObservedGeneration: org.Generation,
	}

	verified, verifyErr := r.Verifier.VerifiedDomains(ctx, org.Name)
	var usable, unverified []string
	switch {
	case verifyErr != nil:
		condition.Status = metav1.ConditionUnknown
		condition.Reason = resourcemanagerv1alpha1.OrganizationEmailDomainsVerifiedReasonCheckFailed
		condition.Message = "Could not check this organization's Domains; trying again"
	default:
		for _, domain := range joining.Domains {
			if emaildomain.Covers(verified, domain) {
				usable = append(usable, domain)
			} else {
				unverified = append(unverified, domain)
			}
		}
		if len(unverified) == 0 {
			condition.Status = metav1.ConditionTrue
			condition.Reason = resourcemanagerv1alpha1.OrganizationEmailDomainsVerifiedReasonVerified
			condition.Message = "Membership is offered to people at " + strings.Join(usable, ", ")
		} else {
			condition.Status = metav1.ConditionFalse
			condition.Reason = resourcemanagerv1alpha1.OrganizationEmailDomainsVerifiedReasonNotVerified
			condition.Message = "Membership is not offered: " + emaildomain.Instructions(unverified)
		}
	}

	if meta.SetStatusCondition(&org.Status.Conditions, condition) {
		if err := r.Client.Status().Update(ctx, org); err != nil {
			return nil, fmt.Errorf("failed to update Organization status: %w", err)
		}
	}
	if verifyErr != nil {
		return nil, fmt.Errorf("failed to check verified Domains: %w", verifyErr)
	}
	return usable, nil
}

func (r *EmailDomainJoiningController) createOffer(ctx context.Context, org *resourcemanagerv1alpha1.Organization, namespace, domain string, user *iamv1alpha1.User) error {
	roles := make([]iamv1alpha1.RoleReference, 0, len(org.Spec.EmailDomainJoining.Roles))
	for _, role := range org.Spec.EmailDomainJoining.Roles {
		roleNamespace := role.Namespace
		if roleNamespace == "" {
			roleNamespace = namespace
		}
		roles = append(roles, iamv1alpha1.RoleReference{Name: role.Name, Namespace: roleNamespace})
	}

	offer := &iamv1alpha1.UserInvitation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      emailDomainOfferNamePrefix + user.Name,
			Namespace: namespace,
			Labels: map[string]string{
				emaildomain.JoinMethodLabel:  emaildomain.JoinMethodEmailDomain,
				emaildomain.EmailDomainLabel: domain,
			},
		},
		Spec: iamv1alpha1.UserInvitationSpec{
			OrganizationRef: resourcemanagerv1alpha1.OrganizationReference{Name: org.Name},
			Email:           user.Spec.Email,
			GivenName:       user.Spec.GivenName,
			FamilyName:      user.Spec.FamilyName,
			Roles:           roles,
			State:           iamv1alpha1.UserInvitationStatePending,
		},
	}
	if err := r.Client.Create(ctx, offer); err != nil {
		if errors.IsAlreadyExists(err) {
			return nil
		}
		return fmt.Errorf("failed to create email domain offer for user %q: %w", user.Name, err)
	}
	logf.FromContext(ctx).Info("Created email domain offer", "userInvitation", offer.Name, "domain", domain)
	return nil
}

func (r *EmailDomainJoiningController) recheckInterval() time.Duration {
	if r.RecheckInterval > 0 {
		return r.RecheckInterval
	}
	return defaultEmailDomainRecheckInterval
}

func (r *EmailDomainJoiningController) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.Background(),
		&iamv1alpha1.User{}, userEmailDomainIndexKey,
		func(obj client.Object) []string {
			if domain := emaildomain.Of(obj.(*iamv1alpha1.User).Spec.Email); domain != "" {
				return []string{domain}
			}
			return nil
		}); err != nil {
		return fmt.Errorf("failed to set field index on User by email domain: %w", err)
	}

	if err := mgr.GetFieldIndexer().IndexField(context.Background(),
		&resourcemanagerv1alpha1.Organization{}, organizationEmailDomainIndexKey,
		func(obj client.Object) []string {
			if joining := obj.(*resourcemanagerv1alpha1.Organization).Spec.EmailDomainJoining; joining != nil {
				return joining.Domains
			}
			return nil
		}); err != nil {
		return fmt.Errorf("failed to set field index on Organization by email domain: %w", err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&resourcemanagerv1alpha1.Organization{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(
			&iamv1alpha1.User{},
			handler.EnqueueRequestsFromMapFunc(r.findOrganizationsForUser),
			builder.WithPredicates(userEmailChangedPredicate),
		).
		Watches(
			&resourcemanagerv1alpha1.OrganizationDomain{},
			handler.EnqueueRequestsFromMapFunc(organizationForDomain),
			builder.WithPredicates(organizationDomainVerifiedChangedPredicate),
		).
		Named("emaildomainjoining").
		Complete(r)
}

// findOrganizationsForUser enqueues the organizations offering membership at
// the user's email domain.
func (r *EmailDomainJoiningController) findOrganizationsForUser(ctx context.Context, obj client.Object) []reconcile.Request {
	user, ok := obj.(*iamv1alpha1.User)
	if !ok {
		return nil
	}
	domain := emaildomain.Of(user.Spec.Email)
	if domain == "" {
		return nil
	}

	var orgs resourcemanagerv1alpha1.OrganizationList
	if err := r.Client.List(ctx, &orgs, client.MatchingFields{organizationEmailDomainIndexKey: domain}); err != nil {
		logf.FromContext(ctx).Error(err, "failed to list Organizations by email domain", "domain", domain)
		return nil
	}
	requests := make([]reconcile.Request, 0, len(orgs.Items))
	for _, org := range orgs.Items {
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKey{Name: org.Name}})
	}
	return requests
}

// organizationForDomain enqueues the organization whose namespace holds the
// OrganizationDomain.
func organizationForDomain(_ context.Context, obj client.Object) []reconcile.Request {
	org, ok := strings.CutPrefix(obj.GetNamespace(), "organization-")
	if !ok || org == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: client.ObjectKey{Name: org}}}
}

// organizationDomainVerifiedChangedPredicate passes OrganizationDomains that
// are created or deleted, or whose Verified condition changed.
var organizationDomainVerifiedChangedPredicate = predicate.Funcs{
	CreateFunc: func(e event.CreateEvent) bool { return true },
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldDomain, okOld := e.ObjectOld.(*resourcemanagerv1alpha1.OrganizationDomain)
		newDomain, okNew := e.ObjectNew.(*resourcemanagerv1alpha1.OrganizationDomain)
		if !okOld || !okNew {
			return false
		}
		return meta.IsStatusConditionTrue(oldDomain.Status.Conditions, resourcemanagerv1alpha1.OrganizationDomainConditionVerified) !=
			meta.IsStatusConditionTrue(newDomain.Status.Conditions, resourcemanagerv1alpha1.OrganizationDomainConditionVerified) ||
			(oldDomain.DeletionTimestamp == nil) != (newDomain.DeletionTimestamp == nil)
	},
	DeleteFunc:  func(e event.DeleteEvent) bool { return true },
	GenericFunc: func(e event.GenericEvent) bool { return false },
}

// userEmailChangedPredicate passes new Users and Users whose email changed.
var userEmailChangedPredicate = predicate.Funcs{
	CreateFunc: func(e event.CreateEvent) bool { return true },
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldUser, okOld := e.ObjectOld.(*iamv1alpha1.User)
		newUser, okNew := e.ObjectNew.(*iamv1alpha1.User)
		return okOld && okNew && !strings.EqualFold(oldUser.Spec.Email, newUser.Spec.Email)
	},
	DeleteFunc:  func(e event.DeleteEvent) bool { return false },
	GenericFunc: func(e event.GenericEvent) bool { return false },
}
