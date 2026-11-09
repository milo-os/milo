package v1alpha1

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	admissionv1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"go.miloapis.com/milo/internal/emaildomain"
	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
)

func emailDomainOffer(state iamv1alpha1.UserInvitationStateType) *iamv1alpha1.UserInvitation {
	return &iamv1alpha1.UserInvitation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "email-domain-jane",
			Namespace: "organization-acme",
			Labels: map[string]string{
				emaildomain.JoinMethodLabel:  emaildomain.JoinMethodEmailDomain,
				emaildomain.EmailDomainLabel: "example.com",
			},
		},
		Spec: iamv1alpha1.UserInvitationSpec{
			OrganizationRef: resourcemanagerv1alpha1.OrganizationReference{Name: "acme"},
			Email:           "jane@example.com",
			State:           state,
		},
	}
}

func emailDomainVerifier(t *testing.T, verified ...string) *emaildomain.Verifier {
	t.Helper()
	scheme := runtime.NewScheme()
	_ = resourcemanagerv1alpha1.AddToScheme(scheme)
	project := &resourcemanagerv1alpha1.Project{
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
	return &emaildomain.Verifier{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(project).Build(),
		ListDomains: func(context.Context, string) ([]unstructured.Unstructured, error) {
			var domains []unstructured.Unstructured
			for _, name := range verified {
				domains = append(domains, unstructured.Unstructured{Object: map[string]any{
					"spec":   map[string]any{"domainName": name},
					"status": map[string]any{"conditions": []any{map[string]any{"type": "Verified", "status": "True"}}},
				}})
			}
			return domains, nil
		},
	}
}

func TestUserInvitationMutator_Default_SystemMasters(t *testing.T) {
	ui := emailDomainOffer(iamv1alpha1.UserInvitationStatePending)
	ctx := admission.NewContextWithRequest(context.Background(), admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			UserInfo: authenticationv1.UserInfo{Username: "controller", Groups: []string{systemMastersGroup}},
		},
	})
	// No User exists for the platform; the mutator must not look one up.
	mutator := &UserInvitationMutator{client: fake.NewClientBuilder().Build()}

	assert.NoError(t, mutator.Default(ctx, ui))
	assert.Empty(t, ui.Spec.InvitedBy.Name)
}

func TestUserInvitationValidator_EmailDomainOfferCreate(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = iamv1alpha1.AddToScheme(scheme)
	_ = resourcemanagerv1alpha1.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithIndex(&iamv1alpha1.UserInvitation{}, userInvitationCompositeKey, func(raw client.Object) []string {
			return []string{buildUserInvitationCompositeKey(*raw.(*iamv1alpha1.UserInvitation))}
		}).
		WithIndex(&iamv1alpha1.User{}, userEmailKey, func(raw client.Object) []string {
			return []string{raw.(*iamv1alpha1.User).Spec.Email}
		}).
		Build()
	validator := &UserInvitationValidator{client: c}

	for name, tc := range map[string]struct {
		groups    []string
		wantError bool
	}{
		"platform may create an offer":     {groups: []string{systemMastersGroup}},
		"an owner may not label an invite": {groups: []string{"system:authenticated"}, wantError: true},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := admission.NewContextWithRequest(context.Background(), admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Namespace: "organization-acme",
					UserInfo:  authenticationv1.UserInfo{Username: "requester", Groups: tc.groups},
				},
			})
			_, err := validator.ValidateCreate(ctx, emailDomainOffer(iamv1alpha1.UserInvitationStatePending))
			if tc.wantError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "only the platform can create email domain offers")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestUserInvitationValidator_EmailDomainOfferUpdate(t *testing.T) {
	stripped := emailDomainOffer(iamv1alpha1.UserInvitationStateAccepted)
	stripped.Labels = nil

	ordinary := emailDomainOffer(iamv1alpha1.UserInvitationStatePending)
	ordinary.Labels = nil
	ordinaryAccepted := ordinary.DeepCopy()
	ordinaryAccepted.Spec.State = iamv1alpha1.UserInvitationStateAccepted

	otherDomain := emailDomainOffer(iamv1alpha1.UserInvitationStatePending)
	otherDomain.Spec.Email = "jane@other.io"
	otherDomainAccepted := otherDomain.DeepCopy()
	otherDomainAccepted.Spec.State = iamv1alpha1.UserInvitationStateAccepted

	for name, tc := range map[string]struct {
		oldObj, newObj *iamv1alpha1.UserInvitation
		verified       []string
		userInfo       authenticationv1.UserInfo
		wantError      string
	}{
		"accept with a verified email while the domain is verified": {
			oldObj:   emailDomainOffer(iamv1alpha1.UserInvitationStatePending),
			newObj:   emailDomainOffer(iamv1alpha1.UserInvitationStateAccepted),
			verified: []string{"example.com"},
			userInfo: authenticationv1.UserInfo{Extra: map[string]authenticationv1.ExtraValue{emailVerifiedExtraKey: {"true"}}},
		},
		"accept without a verified email": {
			oldObj:    emailDomainOffer(iamv1alpha1.UserInvitationStatePending),
			newObj:    emailDomainOffer(iamv1alpha1.UserInvitationStateAccepted),
			verified:  []string{"example.com"},
			userInfo:  authenticationv1.UserInfo{Extra: map[string]authenticationv1.ExtraValue{emailVerifiedExtraKey: {"false"}}},
			wantError: "verify your email address",
		},
		"accept with no verification verdict": {
			oldObj:    emailDomainOffer(iamv1alpha1.UserInvitationStatePending),
			newObj:    emailDomainOffer(iamv1alpha1.UserInvitationStateAccepted),
			verified:  []string{"example.com"},
			wantError: "verify your email address",
		},
		"accept after the domain lost verification": {
			oldObj:    emailDomainOffer(iamv1alpha1.UserInvitationStatePending),
			newObj:    emailDomainOffer(iamv1alpha1.UserInvitationStateAccepted),
			userInfo:  authenticationv1.UserInfo{Extra: map[string]authenticationv1.ExtraValue{emailVerifiedExtraKey: {"true"}}},
			wantError: "no longer has a verified Domain",
		},
		"accept an offer for someone at another domain": {
			oldObj:    otherDomain,
			newObj:    otherDomainAccepted,
			verified:  []string{"example.com"},
			userInfo:  authenticationv1.UserInfo{Extra: map[string]authenticationv1.ExtraValue{emailVerifiedExtraKey: {"true"}}},
			wantError: "not for your email domain",
		},
		"strip the labels while accepting": {
			oldObj:    emailDomainOffer(iamv1alpha1.UserInvitationStatePending),
			newObj:    stripped,
			wantError: "labels cannot be changed",
		},
		"ordinary invitations need no verification": {
			oldObj: ordinary,
			newObj: ordinaryAccepted,
		},
	} {
		t.Run(name, func(t *testing.T) {
			validator := &UserInvitationValidator{emailDomains: emailDomainVerifier(t, tc.verified...)}
			ctx := admission.NewContextWithRequest(context.Background(), admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{Namespace: "organization-acme", UserInfo: tc.userInfo},
			})

			_, err := validator.ValidateUpdate(ctx, tc.oldObj, tc.newObj)
			if tc.wantError == "" {
				assert.NoError(t, err)
				return
			}
			assert.Error(t, err)
			assert.True(t, kerrors.IsInvalid(err), "expected an Invalid API error, got: %v", err)
			assert.Contains(t, err.Error(), tc.wantError)
		})
	}
}
