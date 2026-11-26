package resourcemanager

import (
	"context"
	"fmt"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/log"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
)

// projectEventAccessCheckInterval is how often the controller manager asks
// Milo whether it can still emit Project lifecycle events. The Milo RBAC can
// land after the controller manager starts, and can change later, so one
// check at startup is not enough.
const projectEventAccessCheckInterval = 10 * time.Minute

// CheckAccess asks Milo whether it allows both halves of an emit: the
// controller manager impersonating the project event user with a Project
// parent context, and that user creating events. It returns an error naming
// the part that is refused.
//
// The --project-event-user flag ships with the controller manager to the
// hosting cluster, while the binding and the impersonator role's
// resourceNames ship to Milo in config/services. No single kustomize build
// sees both, so a changed flag cannot carry over to the RBAC on its own. This
// check catches the drift and reports it in the project_event_user_authorized
// gauge, which an alert watches.
func (e *ProjectEventEmitter) CheckAccess(ctx context.Context) error {
	cs, err := e.newClient(rest.CopyConfig(e.baseConfig))
	if err != nil {
		return fmt.Errorf("failed to build access review client: %w", err)
	}

	// Milo authorizes the impersonated user and each impersonated extra on its
	// own, so a grant that covers the user but not the extras still refuses
	// every emit. Ask about each value clientForProject sends.
	for _, review := range e.impersonationReviews() {
		result, err := cs.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, &authorizationv1.SelfSubjectAccessReview{
			Spec: authorizationv1.SelfSubjectAccessReviewSpec{ResourceAttributes: review.attributes},
		}, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to review impersonation of %s for %q: %w", review.what, e.impersonateUser, err)
		}
		if !result.Status.Allowed {
			return fmt.Errorf("the controller manager cannot impersonate %s for %q in Milo: bind its Milo identity to resourcemanager.miloapis.com:project-event-impersonator, whose users resourceNames must name the --project-event-user value", review.what, e.impersonateUser)
		}
	}

	// An impersonated request carries the impersonated user and the
	// system:authenticated group, and none of the caller's groups, so this
	// review asks about exactly that identity.
	create, err := cs.AuthorizationV1().SubjectAccessReviews().Create(ctx, &authorizationv1.SubjectAccessReview{
		Spec: authorizationv1.SubjectAccessReviewSpec{
			User:   e.impersonateUser,
			Groups: []string{"system:authenticated"},
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Namespace: clusterScopedEventNamespace,
				Verb:      "create",
				Resource:  "events",
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to review event create for %q: %w", e.impersonateUser, err)
	}
	if !create.Status.Allowed {
		return fmt.Errorf("%q cannot create events in Milo: the resourcemanager.miloapis.com:project-event-emitter binding's subject must match the --project-event-user value", e.impersonateUser)
	}

	return nil
}

// impersonationReview is one impersonated value CheckAccess asks about.
type impersonationReview struct {
	what       string
	attributes *authorizationv1.ResourceAttributes
}

// impersonationReviews lists the impersonation checks behind one emit: the
// user, then each parent-context extra as a userextras/<key> resource whose
// name is the asserted value. parent-name is asked with an empty name, which
// Milo allows only when every project is allowed, because the emitter sends
// whichever project changed.
func (e *ProjectEventEmitter) impersonationReviews() []impersonationReview {
	extra := func(key, value string) *authorizationv1.ResourceAttributes {
		return &authorizationv1.ResourceAttributes{
			Verb:        "impersonate",
			Group:       "authentication.k8s.io",
			Resource:    "userextras",
			Subresource: key,
			Name:        value,
		}
	}

	return []impersonationReview{
		{
			what:       "the user",
			attributes: &authorizationv1.ResourceAttributes{Verb: "impersonate", Resource: "users", Name: e.impersonateUser},
		},
		{
			what:       fmt.Sprintf("the %s extra %q", iamv1alpha1.ParentKindExtraKey, projectKind),
			attributes: extra(iamv1alpha1.ParentKindExtraKey, projectKind),
		},
		{
			what:       fmt.Sprintf("the %s extra for every project", iamv1alpha1.ParentNameExtraKey),
			attributes: extra(iamv1alpha1.ParentNameExtraKey, ""),
		},
	}
}

// projectEventAccessMonitor runs CheckAccess on an interval and publishes the
// result.
type projectEventAccessMonitor struct {
	emitter  *ProjectEventEmitter
	interval time.Duration
}

// Start implements manager.Runnable.
func (m *projectEventAccessMonitor) Start(ctx context.Context) error {
	logger := log.FromContext(ctx).WithName("project-event-access")

	wait.UntilWithContext(ctx, func(ctx context.Context) {
		err := m.emitter.CheckAccess(ctx)
		recordProjectEventAccess(m.emitter.impersonateUser, err)
		if err != nil {
			logger.Error(err, "Project lifecycle events will not reach Milo")
		}
	}, m.interval)

	return nil
}

// NeedLeaderElection implements manager.LeaderElectionRunnable. Every
// replica runs the check, so no replica reports a stale value after losing
// the lease.
func (m *projectEventAccessMonitor) NeedLeaderElection() bool {
	return false
}
