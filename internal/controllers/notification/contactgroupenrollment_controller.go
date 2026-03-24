package notification

import (
	"context"
	"fmt"

	notificationv1alpha1 "go.miloapis.com/milo/pkg/apis/notification/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	// enrollmentFieldOwner is the field manager name used for server-side apply/patch operations.
	enrollmentFieldOwner = "contact-group-enrollment-controller"

	// removalContactRefNameIndexKey is the field index key for efficient lookups
	// of ContactGroupMembershipRemoval by spec.contactRef.name.
	removalContactRefNameIndexKey = "spec.contactRef.name"
)

// ContactGroupEnrollmentController watches Contact resources and automatically creates
// ContactGroupMembership records for each matching ContactGroupEnrollmentPolicy.
//
// Idempotency is achieved by annotating each Contact with the policy names that
// have already been evaluated, using the annotation key pattern:
//
//	{EnrollmentPolicyAnnotationPrefix}/{policyName} = "true"
//
// Once a policy annotation is present on a Contact, the controller skips that
// policy on future reconciliations, avoiding unnecessary API calls.
type ContactGroupEnrollmentController struct {
	Client client.Client
}

// +kubebuilder:rbac:groups=notification.miloapis.com,resources=contacts,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=notification.miloapis.com,resources=contactgroupenrollmentpolicies,verbs=get;list;watch
// +kubebuilder:rbac:groups=notification.miloapis.com,resources=contactgroupmemberships,verbs=get;list;watch;create
// +kubebuilder:rbac:groups=notification.miloapis.com,resources=contactgroupmembershipremovals,verbs=get;list;watch

// Reconcile evaluates all ContactGroupEnrollmentPolicy resources against the given Contact.
// For each policy that has not yet been evaluated (no annotation present), the controller
// checks whether the Contact matches the policy selector, then either creates a
// ContactGroupMembership or skips creation if an opt-out removal record exists.
func (r *ContactGroupEnrollmentController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx).WithName("contact-group-enrollment-controller").WithValues("contact", req.NamespacedName)
	log.Info("Starting reconciliation")

	contact := &notificationv1alpha1.Contact{}
	if err := r.Client.Get(ctx, req.NamespacedName, contact); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get Contact: %w", err)
	}

	if !contact.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	// List all enrollment policies.
	policyList := &notificationv1alpha1.ContactGroupEnrollmentPolicyList{}
	if err := r.Client.List(ctx, policyList); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list ContactGroupEnrollmentPolicies: %w", err)
	}

	if len(policyList.Items) == 0 {
		return ctrl.Result{}, nil
	}

	for i := range policyList.Items {
		policy := &policyList.Items[i]

		if err := r.evaluatePolicy(ctx, contact, policy); err != nil {
			log.Error(err, "Failed to evaluate enrollment policy", "policy", policy.Name)
			return ctrl.Result{}, err
		}
	}

	log.Info("Reconciliation completed successfully")
	return ctrl.Result{}, nil
}

// evaluatePolicy checks whether the given Contact should be enrolled according to policy.
// If the policy annotation is already present on the Contact, the evaluation is skipped.
// Otherwise, the Contact is matched against the policy selector, a pre-existing opt-out
// (ContactGroupMembershipRemoval) is checked, and a ContactGroupMembership is created
// if appropriate. The annotation is written to the Contact after evaluation.
func (r *ContactGroupEnrollmentController) evaluatePolicy(ctx context.Context, contact *notificationv1alpha1.Contact, policy *notificationv1alpha1.ContactGroupEnrollmentPolicy) error {
	log := log.FromContext(ctx).WithName("evaluate-policy").WithValues(
		"contact", types.NamespacedName{Name: contact.Name, Namespace: contact.Namespace},
		"policy", policy.Name,
	)

	annotationKey := enrollmentAnnotationKey(policy.Name)

	// Skip if this policy has already been evaluated for this Contact.
	if contact.Annotations != nil {
		if _, evaluated := contact.Annotations[annotationKey]; evaluated {
			log.V(1).Info("Policy already evaluated for contact, skipping")
			return nil
		}
	}

	defer func() {
		// Always annotate the Contact after evaluation so future reconciliations skip it.
		if err := r.markPolicyEvaluated(ctx, contact, annotationKey); err != nil {
			log.Error(err, "Failed to mark policy as evaluated on contact")
		}
	}()

	// Check that the trigger type is supported.
	if policy.Spec.Trigger.Type != notificationv1alpha1.EnrollmentTriggerContactCreated {
		log.V(1).Info("Unsupported trigger type, skipping", "trigger", policy.Spec.Trigger.Type)
		return nil
	}

	// Apply the contact selector if specified.
	if !contactMatchesSelector(contact, policy.Spec.ContactSelector) {
		log.V(1).Info("Contact does not match policy selector, skipping")
		return nil
	}

	// Check for a pre-existing opt-out record.
	hasOptOut, err := r.hasOptOut(ctx, contact, policy.Spec.ContactGroupRef)
	if err != nil {
		return fmt.Errorf("failed to check for opt-out: %w", err)
	}
	if hasOptOut {
		log.Info("Contact has opted out of this group, skipping enrollment",
			"group", policy.Spec.ContactGroupRef.Name)
		return nil
	}

	// Ensure the membership exists.
	if err := r.ensureMembership(ctx, contact, policy); err != nil {
		return fmt.Errorf("failed to ensure membership: %w", err)
	}

	return nil
}

// contactMatchesSelector returns true if the Contact satisfies the enrollment selector.
// A nil selector matches all contacts.
func contactMatchesSelector(contact *notificationv1alpha1.Contact, selector *notificationv1alpha1.EnrollmentContactSelector) bool {
	if selector == nil {
		return true
	}
	if selector.SubjectKind != "" {
		if contact.Spec.SubjectRef == nil || contact.Spec.SubjectRef.Kind != selector.SubjectKind {
			return false
		}
	}
	return true
}

// hasOptOut returns true if a ContactGroupMembershipRemoval exists that covers
// this contact + group combination.
func (r *ContactGroupEnrollmentController) hasOptOut(
	ctx context.Context,
	contact *notificationv1alpha1.Contact,
	groupRef notificationv1alpha1.EnrollmentContactGroupRef,
) (bool, error) {
	removalList := &notificationv1alpha1.ContactGroupMembershipRemovalList{}
	if err := r.Client.List(ctx, removalList,
		client.MatchingFields{removalContactRefNameIndexKey: contact.Name},
	); err != nil {
		return false, fmt.Errorf("failed to list ContactGroupMembershipRemovals: %w", err)
	}

	for _, removal := range removalList.Items {
		if removal.Spec.ContactRef.Name == contact.Name &&
			removal.Spec.ContactRef.Namespace == contact.Namespace &&
			removal.Spec.ContactGroupRef.Name == groupRef.Name &&
			removal.Spec.ContactGroupRef.Namespace == groupRef.Namespace {
			return true, nil
		}
	}

	return false, nil
}

// ensureMembership creates a ContactGroupMembership for the given Contact and policy
// if one does not already exist.
func (r *ContactGroupEnrollmentController) ensureMembership(
	ctx context.Context,
	contact *notificationv1alpha1.Contact,
	policy *notificationv1alpha1.ContactGroupEnrollmentPolicy,
) error {
	log := log.FromContext(ctx).WithName("ensure-membership")

	membershipName := membershipName(contact.Name, policy.Name)
	membershipNamespace := contact.Namespace

	existing := &notificationv1alpha1.ContactGroupMembership{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: membershipName, Namespace: membershipNamespace}, existing)
	if err == nil {
		log.V(1).Info("ContactGroupMembership already exists", "membership", membershipName)
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get ContactGroupMembership: %w", err)
	}

	membership := &notificationv1alpha1.ContactGroupMembership{
		ObjectMeta: metav1.ObjectMeta{
			Name:      membershipName,
			Namespace: membershipNamespace,
			Labels: map[string]string{
				"enrollment.notification.miloapis.com/policy": policy.Name,
			},
		},
		Spec: notificationv1alpha1.ContactGroupMembershipSpec{
			ContactRef: notificationv1alpha1.ContactReference{
				Name:      contact.Name,
				Namespace: contact.Namespace,
			},
			ContactGroupRef: notificationv1alpha1.ContactGroupReference{
				Name:      policy.Spec.ContactGroupRef.Name,
				Namespace: policy.Spec.ContactGroupRef.Namespace,
			},
		},
	}

	if err := r.Client.Create(ctx, membership, client.FieldOwner(enrollmentFieldOwner)); err != nil {
		if apierrors.IsAlreadyExists(err) {
			log.V(1).Info("ContactGroupMembership already exists (race condition)", "membership", membershipName)
			return nil
		}
		return fmt.Errorf("failed to create ContactGroupMembership: %w", err)
	}

	log.Info("Created ContactGroupMembership",
		"membership", membershipName,
		"contact", contact.Name,
		"group", policy.Spec.ContactGroupRef.Name,
		"policy", policy.Name,
	)
	return nil
}

// markPolicyEvaluated adds an annotation to the Contact recording that this policy
// has been evaluated, preventing redundant evaluations on future reconciliations.
func (r *ContactGroupEnrollmentController) markPolicyEvaluated(ctx context.Context, contact *notificationv1alpha1.Contact, annotationKey string) error {
	// Re-fetch to get the latest resourceVersion before patching.
	latest := &notificationv1alpha1.Contact{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: contact.Name, Namespace: contact.Namespace}, latest); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get Contact for annotation: %w", err)
	}

	// Already annotated (another goroutine may have beaten us).
	if latest.Annotations != nil {
		if _, ok := latest.Annotations[annotationKey]; ok {
			return nil
		}
	}

	before := latest.DeepCopy()
	if latest.Annotations == nil {
		latest.Annotations = make(map[string]string)
	}
	latest.Annotations[annotationKey] = "true"

	if err := r.Client.Patch(ctx, latest, client.MergeFrom(before), client.FieldOwner(enrollmentFieldOwner)); err != nil {
		return fmt.Errorf("failed to patch Contact annotations: %w", err)
	}

	// Keep the in-memory object up to date so the deferred call is correct
	// if called multiple times within the same reconcile loop.
	contact.Annotations = latest.Annotations

	return nil
}

// findContactsForPolicy returns reconcile.Request entries for all Contacts that have not yet
// been evaluated against the given ContactGroupEnrollmentPolicy. This is used to trigger
// reconciliation when a new policy is created or updated.
func (r *ContactGroupEnrollmentController) findContactsForPolicy(ctx context.Context, obj client.Object) []reconcile.Request {
	policy, ok := obj.(*notificationv1alpha1.ContactGroupEnrollmentPolicy)
	if !ok {
		return nil
	}

	log := log.FromContext(ctx).WithName("find-contacts-for-policy").WithValues("policy", policy.Name)
	annotationKey := enrollmentAnnotationKey(policy.Name)

	contactList := &notificationv1alpha1.ContactList{}
	if err := r.Client.List(ctx, contactList); err != nil {
		log.Error(err, "Failed to list Contacts")
		return nil
	}

	var requests []reconcile.Request
	for _, contact := range contactList.Items {
		if contact.Annotations != nil {
			if _, evaluated := contact.Annotations[annotationKey]; evaluated {
				continue
			}
		}
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      contact.Name,
				Namespace: contact.Namespace,
			},
		})
	}

	if len(requests) > 0 {
		log.Info("Queuing unevaluated contacts for new/updated policy", "count", len(requests))
	}

	return requests
}

// SetupWithManager registers the controller and required field indexes with the manager.
func (r *ContactGroupEnrollmentController) SetupWithManager(mgr ctrl.Manager) error {
	// Index ContactGroupMembershipRemoval by spec.contactRef.name for efficient opt-out checks.
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&notificationv1alpha1.ContactGroupMembershipRemoval{},
		removalContactRefNameIndexKey,
		func(obj client.Object) []string {
			removal, ok := obj.(*notificationv1alpha1.ContactGroupMembershipRemoval)
			if !ok {
				return nil
			}
			if removal.Spec.ContactRef.Name == "" {
				return nil
			}
			return []string{removal.Spec.ContactRef.Name}
		},
	); err != nil {
		return fmt.Errorf("failed to set field index on ContactGroupMembershipRemoval by spec.contactRef.name: %w", err)
	}

	if err := ctrl.NewControllerManagedBy(mgr).
		For(&notificationv1alpha1.Contact{}).
		Watches(
			&notificationv1alpha1.ContactGroupEnrollmentPolicy{},
			handler.EnqueueRequestsFromMapFunc(r.findContactsForPolicy),
		).
		Named("contact-group-enrollment").
		Complete(r); err != nil {
		return fmt.Errorf("failed to build contact-group-enrollment controller: %w", err)
	}

	return nil
}

// enrollmentAnnotationKey returns the annotation key for a given policy name.
func enrollmentAnnotationKey(policyName string) string {
	return fmt.Sprintf("%s/%s", notificationv1alpha1.EnrollmentPolicyAnnotationPrefix, policyName)
}

// membershipName returns a deterministic name for a ContactGroupMembership
// created by the enrollment controller for the given contact and policy.
func membershipName(contactName, policyName string) string {
	name := fmt.Sprintf("enrollment-%s-%s", policyName, contactName)
	// Kubernetes resource names must be <= 253 characters.
	if len(name) > 253 {
		name = name[:253]
	}
	return name
}
