package iam

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	featuregatetesting "k8s.io/component-base/featuregate/testing"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"go.miloapis.com/milo/internal/emaildomain"
	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	"go.miloapis.com/milo/pkg/features"
)

const domainJoiningOrgNamespace = "organization-acme"

func domainJoiningUser(name, email string) *iamv1alpha1.User {
	return &iamv1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       iamv1alpha1.UserSpec{Email: email, GivenName: "Given " + name, FamilyName: "Family"},
	}
}

func domainJoiningOrg(domains ...string) *resourcemanagerv1alpha1.Organization {
	org := &resourcemanagerv1alpha1.Organization{ObjectMeta: metav1.ObjectMeta{Name: "acme", Generation: 3}}
	if len(domains) > 0 {
		org.Spec.EmailDomainJoining = &resourcemanagerv1alpha1.OrganizationEmailDomainJoining{
			Domains: domains,
			Roles:   []resourcemanagerv1alpha1.RoleReference{{Name: "viewer", Namespace: "datum-cloud"}, {Name: "org-local"}},
		}
	}
	return org
}

func domainJoiningProject() *resourcemanagerv1alpha1.Project {
	return &resourcemanagerv1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "web",
			Labels: map[string]string{resourcemanagerv1alpha1.OrganizationNameLabel: "acme"},
		},
		Spec: resourcemanagerv1alpha1.ProjectSpec{
			OwnerRef: resourcemanagerv1alpha1.OwnerReference{Kind: "Organization", Name: "acme"},
		},
		Status: resourcemanagerv1alpha1.ProjectStatus{
			Conditions: []metav1.Condition{{Type: resourcemanagerv1alpha1.ProjectReady, Status: metav1.ConditionTrue}},
		},
	}
}

func verifiedDomain(name string) unstructured.Unstructured {
	return unstructured.Unstructured{Object: map[string]any{
		"spec":   map[string]any{"domainName": name},
		"status": map[string]any{"conditions": []any{map[string]any{"type": "Verified", "status": "True"}}},
	}}
}

func domainOffer(name, email, domain string, state iamv1alpha1.UserInvitationStateType) *iamv1alpha1.UserInvitation {
	return &iamv1alpha1.UserInvitation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: domainJoiningOrgNamespace,
			Labels: map[string]string{
				emaildomain.JoinMethodLabel:  emaildomain.JoinMethodEmailDomain,
				emaildomain.EmailDomainLabel: domain,
			},
		},
		Spec: iamv1alpha1.UserInvitationSpec{
			OrganizationRef: resourcemanagerv1alpha1.OrganizationReference{Name: "acme"},
			Email:           email,
			State:           state,
		},
	}
}

func newDomainJoiningTestController(t *testing.T, verified []string, listErr error, objs ...client.Object) (*EmailDomainJoiningController, client.Client) {
	t.Helper()
	c := fake.NewClientBuilder().
		WithScheme(getTestScheme()).
		WithObjects(objs...).
		WithStatusSubresource(&resourcemanagerv1alpha1.Organization{}).
		WithIndex(&iamv1alpha1.User{}, userEmailDomainIndexKey, func(obj client.Object) []string {
			return []string{emaildomain.Of(obj.(*iamv1alpha1.User).Spec.Email)}
		}).
		Build()
	return &EmailDomainJoiningController{
		Client: c,
		Verifier: &emaildomain.Verifier{
			Client: c,
			ListDomains: func(context.Context, string) ([]unstructured.Unstructured, error) {
				if listErr != nil {
					return nil, listErr
				}
				var domains []unstructured.Unstructured
				for _, name := range verified {
					domains = append(domains, verifiedDomain(name))
				}
				return domains, nil
			},
		},
		RecheckInterval: time.Minute,
	}, c
}

func reconcileDomainJoining(t *testing.T, r *EmailDomainJoiningController) (ctrl.Result, error) {
	t.Helper()
	return r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKey{Name: "acme"}})
}

func listOffers(t *testing.T, c client.Client) map[string]iamv1alpha1.UserInvitation {
	t.Helper()
	var list iamv1alpha1.UserInvitationList
	if err := c.List(context.Background(), &list, client.InNamespace(domainJoiningOrgNamespace)); err != nil {
		t.Fatalf("list invitations: %v", err)
	}
	offers := map[string]iamv1alpha1.UserInvitation{}
	for _, ui := range list.Items {
		offers[ui.Name] = ui
	}
	return offers
}

func emailDomainsCondition(t *testing.T, c client.Client) *metav1.Condition {
	t.Helper()
	org := &resourcemanagerv1alpha1.Organization{}
	if err := c.Get(context.Background(), client.ObjectKey{Name: "acme"}, org); err != nil {
		t.Fatalf("get organization: %v", err)
	}
	return meta.FindStatusCondition(org.Status.Conditions, resourcemanagerv1alpha1.OrganizationConditionEmailDomainsVerified)
}

func TestEmailDomainJoiningController_OffersVerifiedDomain(t *testing.T) {
	featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.EmailDomainJoining, true)

	member := &resourcemanagerv1alpha1.OrganizationMembership{
		ObjectMeta: metav1.ObjectMeta{Name: "member-member", Namespace: domainJoiningOrgNamespace},
		Spec: resourcemanagerv1alpha1.OrganizationMembershipSpec{
			OrganizationRef: resourcemanagerv1alpha1.OrganizationReference{Name: "acme"},
			UserRef:         resourcemanagerv1alpha1.MemberReference{Name: "member"},
		},
	}
	r, c := newDomainJoiningTestController(t, []string{"example.com"}, nil,
		domainJoiningOrg("eu.example.com"),
		domainJoiningProject(),
		domainJoiningUser("new", "New@EU.example.com"),
		domainJoiningUser("member", "member@eu.example.com"),
		domainJoiningUser("declined", "declined@eu.example.com"),
		domainJoiningUser("elsewhere", "someone@example.com"),
		member,
		domainOffer("email-domain-declined", "declined@eu.example.com", "eu.example.com", iamv1alpha1.UserInvitationStateDeclined),
	)

	result, err := reconcileDomainJoining(t, r)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.RequeueAfter != time.Minute {
		t.Errorf("RequeueAfter = %v, want %v", result.RequeueAfter, time.Minute)
	}

	offers := listOffers(t, c)
	if len(offers) != 2 {
		t.Fatalf("expected the declined offer and one new offer, got %v", offers)
	}
	offer, ok := offers["email-domain-new"]
	if !ok {
		t.Fatalf("no offer for the new user: %v", offers)
	}
	if offer.Labels[emaildomain.EmailDomainLabel] != "eu.example.com" || !isEmailDomainOffer(&offer) {
		t.Errorf("offer labels = %v", offer.Labels)
	}
	if offer.Spec.State != iamv1alpha1.UserInvitationStatePending || offer.Spec.ExpirationDate != nil || offer.Spec.InvitedBy.Name != "" {
		t.Errorf("offer spec = %+v, want a pending offer with no expiry or inviter", offer.Spec)
	}
	wantRoles := []iamv1alpha1.RoleReference{{Name: "viewer", Namespace: "datum-cloud"}, {Name: "org-local", Namespace: domainJoiningOrgNamespace}}
	if len(offer.Spec.Roles) != 2 || offer.Spec.Roles[0] != wantRoles[0] || offer.Spec.Roles[1] != wantRoles[1] {
		t.Errorf("offer roles = %v, want %v", offer.Spec.Roles, wantRoles)
	}

	cond := emailDomainsCondition(t, c)
	if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != resourcemanagerv1alpha1.OrganizationEmailDomainsVerifiedReasonVerified {
		t.Fatalf("condition = %+v, want Verified", cond)
	}

	// A second reconcile changes nothing.
	if _, err := reconcileDomainJoining(t, r); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if got := listOffers(t, c); len(got) != 2 {
		t.Errorf("second reconcile changed offers: %v", got)
	}
}

func TestEmailDomainJoiningController_UnverifiedDomain(t *testing.T) {
	featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.EmailDomainJoining, true)

	accepted := domainOffer("email-domain-accepted", "accepted@example.com", "example.com", iamv1alpha1.UserInvitationStateAccepted)
	r, c := newDomainJoiningTestController(t, []string{"www.example.com"}, nil,
		domainJoiningOrg("example.com"),
		domainJoiningProject(),
		domainJoiningUser("new", "new@example.com"),
		domainOffer("email-domain-pending", "pending@example.com", "example.com", iamv1alpha1.UserInvitationStatePending),
		accepted,
	)

	if _, err := reconcileDomainJoining(t, r); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	offers := listOffers(t, c)
	if _, ok := offers["email-domain-pending"]; ok {
		t.Error("pending offer for an unverified domain was not deleted")
	}
	if _, ok := offers["email-domain-accepted"]; !ok {
		t.Error("accepted offer was deleted; the UserInvitation controller owns it")
	}
	if _, ok := offers["email-domain-new"]; ok {
		t.Error("offer created for an unverified domain")
	}

	cond := emailDomainsCondition(t, c)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != resourcemanagerv1alpha1.OrganizationEmailDomainsVerifiedReasonNotVerified {
		t.Fatalf("condition = %+v, want DomainNotVerified", cond)
	}
	if !strings.Contains(cond.Message, "example.com") || !strings.Contains(cond.Message, "DNS TXT record") {
		t.Errorf("condition message lacks instructions: %q", cond.Message)
	}
}

func TestEmailDomainJoiningController_CheckFailedKeepsOffers(t *testing.T) {
	featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.EmailDomainJoining, true)

	r, c := newDomainJoiningTestController(t, nil, errors.New("connection refused"),
		domainJoiningOrg("example.com"),
		domainJoiningProject(),
		domainOffer("email-domain-pending", "pending@example.com", "example.com", iamv1alpha1.UserInvitationStatePending),
	)

	if _, err := reconcileDomainJoining(t, r); err == nil {
		t.Fatal("expected an error so the check is retried")
	}
	if _, ok := listOffers(t, c)["email-domain-pending"]; !ok {
		t.Error("pending offer deleted after a failed check")
	}
	if cond := emailDomainsCondition(t, c); cond == nil || cond.Reason != resourcemanagerv1alpha1.OrganizationEmailDomainsVerifiedReasonCheckFailed {
		t.Errorf("condition = %+v, want CheckFailed", cond)
	}
}

func TestEmailDomainJoiningController_TurnedOff(t *testing.T) {
	featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.EmailDomainJoining, true)

	org := domainJoiningOrg()
	org.Status.Conditions = []metav1.Condition{{
		Type:   resourcemanagerv1alpha1.OrganizationConditionEmailDomainsVerified,
		Status: metav1.ConditionTrue,
		Reason: resourcemanagerv1alpha1.OrganizationEmailDomainsVerifiedReasonVerified,
	}}
	r, c := newDomainJoiningTestController(t, []string{"example.com"}, nil,
		org,
		domainOffer("email-domain-pending", "pending@example.com", "example.com", iamv1alpha1.UserInvitationStatePending),
	)

	result, err := reconcileDomainJoining(t, r)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Errorf("RequeueAfter = %v, want none once turned off", result.RequeueAfter)
	}
	if len(listOffers(t, c)) != 0 {
		t.Error("pending offer kept after email domain joining was turned off")
	}
	if cond := emailDomainsCondition(t, c); cond != nil {
		t.Errorf("condition kept after turning off: %+v", cond)
	}
}

func TestEmailDomainJoiningController_GateOff(t *testing.T) {
	r, c := newDomainJoiningTestController(t, []string{"example.com"}, nil,
		domainJoiningOrg("example.com"),
		domainJoiningProject(),
		domainJoiningUser("new", "new@example.com"),
	)

	if _, err := reconcileDomainJoining(t, r); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(listOffers(t, c)) != 0 {
		t.Error("offer created with the feature gate off")
	}
}

func TestOrganizationForDomain(t *testing.T) {
	for namespace, want := range map[string]string{
		"organization-acme": "acme",
		"organization-":     "",
		"default":           "",
	} {
		domain := &resourcemanagerv1alpha1.OrganizationDomain{ObjectMeta: metav1.ObjectMeta{Name: "acme.com", Namespace: namespace}}
		requests := organizationForDomain(context.Background(), domain)
		got := ""
		if len(requests) == 1 {
			got = requests[0].Name
		}
		if got != want || len(requests) > 1 {
			t.Errorf("organizationForDomain(%q) = %v, want %q", namespace, requests, want)
		}
	}
}

func TestOrganizationDomainVerifiedChangedPredicate(t *testing.T) {
	domain := func(verified metav1.ConditionStatus) *resourcemanagerv1alpha1.OrganizationDomain {
		return &resourcemanagerv1alpha1.OrganizationDomain{
			Status: resourcemanagerv1alpha1.OrganizationDomainStatus{Conditions: []metav1.Condition{{
				Type:   resourcemanagerv1alpha1.OrganizationDomainConditionVerified,
				Status: verified,
			}}},
		}
	}
	if !organizationDomainVerifiedChangedPredicate.Update(event.UpdateEvent{
		ObjectOld: domain(metav1.ConditionFalse), ObjectNew: domain(metav1.ConditionTrue),
	}) {
		t.Error("a domain becoming verified should reconcile its organization")
	}
	if organizationDomainVerifiedChangedPredicate.Update(event.UpdateEvent{
		ObjectOld: domain(metav1.ConditionFalse), ObjectNew: domain(metav1.ConditionFalse),
	}) {
		t.Error("another failed lookup shouldn't reconcile the organization")
	}
	if !organizationDomainVerifiedChangedPredicate.Delete(event.DeleteEvent{Object: domain(metav1.ConditionTrue)}) {
		t.Error("removing a domain should reconcile its organization")
	}
}
