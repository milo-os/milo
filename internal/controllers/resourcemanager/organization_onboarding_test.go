package resourcemanager

import (
	"testing"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	resourcemanagerv1alpha "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestIsOrganizationContactInfoComplete(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		info     *resourcemanagerv1alpha.OrganizationContactInfo
		complete bool
	}{
		{
			name:     "nil",
			info:     nil,
			complete: false,
		},
		{
			name: "missing name",
			info: &resourcemanagerv1alpha.OrganizationContactInfo{
				Email: "a@example.com",
			},
			complete: false,
		},
		{
			name: "complete",
			info: &resourcemanagerv1alpha.OrganizationContactInfo{
				Email: "a@example.com",
				Name:  "Ada Lovelace",
			},
			complete: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := resourcemanagerv1alpha.IsOrganizationContactInfoComplete(tt.info); got != tt.complete {
				t.Fatalf("IsOrganizationContactInfoComplete() = %v, want %v", got, tt.complete)
			}
		})
	}
}

func TestReconcileOrganizationOnboarding(t *testing.T) {
	t.Parallel()

	scheme := getTestScheme()
	t.Run("contact info incomplete", func(t *testing.T) {
		t.Parallel()
		org := &resourcemanagerv1alpha.Organization{
			ObjectMeta: metav1.ObjectMeta{Name: "org-test"},
		}
		client := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(org).WithObjects(org).Build()

		changed, err := reconcileOrganizationOnboarding(t.Context(), client, org)
		if err != nil {
			t.Fatalf("reconcileOrganizationOnboarding() error = %v", err)
		}
		if !changed {
			t.Fatal("expected status update")
		}
		condition := org.Status.Conditions[0]
		if condition.Type != resourcemanagerv1alpha.OrganizationConditionOnboardingComplete {
			t.Fatalf("condition type = %q", condition.Type)
		}
		if condition.Reason != resourcemanagerv1alpha.OrganizationOnboardingCompleteReasonContactInfoIncomplete {
			t.Fatalf("condition reason = %q", condition.Reason)
		}
	})

	t.Run("ready when billing account has default payment method", func(t *testing.T) {
		t.Parallel()
		org := &resourcemanagerv1alpha.Organization{
			ObjectMeta: metav1.ObjectMeta{Name: "org-ready"},
			Spec: resourcemanagerv1alpha.OrganizationSpec{
				ContactInfo: &resourcemanagerv1alpha.OrganizationContactInfo{
					Email: "a@example.com",
					Name:  "Ada Lovelace",
				},
			},
		}
		account := &billingv1alpha1.BillingAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "default",
				Namespace: resourcemanagerv1alpha.OrganizationNamespace(org.Name),
			},
			Status: billingv1alpha1.BillingAccountStatus{
				Conditions: []metav1.Condition{
					{
						Type:   billingv1alpha1.BillingAccountConditionDefaultPaymentMethodReady,
						Status: metav1.ConditionTrue,
						Reason: "Ready",
					},
				},
			},
		}
		client := fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(org, account).
			WithObjects(org, account).
			Build()

		changed, err := reconcileOrganizationOnboarding(t.Context(), client, org)
		if err != nil {
			t.Fatalf("reconcileOrganizationOnboarding() error = %v", err)
		}
		if !changed {
			t.Fatal("expected status update")
		}
		for _, condition := range org.Status.Conditions {
			if condition.Type == resourcemanagerv1alpha.OrganizationConditionOnboardingComplete {
				if condition.Status != metav1.ConditionTrue {
					t.Fatalf("OnboardingComplete status = %q", condition.Status)
				}
				return
			}
		}
		t.Fatal("OnboardingComplete condition not found")
	})
}

func TestReconcileOrganizationOnboardingBillingSignals(t *testing.T) {
	t.Parallel()

	scheme := getTestScheme()
	condition := func(conditionType string, status metav1.ConditionStatus, reason string) metav1.Condition {
		return metav1.Condition{Type: conditionType, Status: status, Reason: reason}
	}

	tests := []struct {
		name       string
		conditions []metav1.Condition
		wantStatus metav1.ConditionStatus
		wantReason string
	}{
		{
			name: "ready with invoice terms and no card",
			conditions: []metav1.Condition{
				condition(billingv1alpha1.BillingAccountConditionDefaultPaymentMethodReady, metav1.ConditionFalse, "NotConfigured"),
				condition(billingv1alpha1.BillingAccountConditionPaymentReady, metav1.ConditionTrue, "InvoiceTerms"),
			},
			wantStatus: metav1.ConditionTrue,
			wantReason: resourcemanagerv1alpha.OrganizationOnboardingCompleteReasonReady,
		},
		{
			name: "ready with a card",
			conditions: []metav1.Condition{
				condition(billingv1alpha1.BillingAccountConditionDefaultPaymentMethodReady, metav1.ConditionTrue, "Ready"),
				condition(billingv1alpha1.BillingAccountConditionPaymentReady, metav1.ConditionTrue, "PaymentMethodReady"),
			},
			wantStatus: metav1.ConditionTrue,
			wantReason: resourcemanagerv1alpha.OrganizationOnboardingCompleteReasonReady,
		},
		{
			name: "ready from a card on an account an older billing controller reconciled",
			conditions: []metav1.Condition{
				condition(billingv1alpha1.BillingAccountConditionDefaultPaymentMethodReady, metav1.ConditionTrue, "Ready"),
			},
			wantStatus: metav1.ConditionTrue,
			wantReason: resourcemanagerv1alpha.OrganizationOnboardingCompleteReasonReady,
		},
		{
			name: "not ready when terms have ended and there is no card",
			conditions: []metav1.Condition{
				condition(billingv1alpha1.BillingAccountConditionDefaultPaymentMethodReady, metav1.ConditionFalse, "NotConfigured"),
				condition(billingv1alpha1.BillingAccountConditionPaymentReady, metav1.ConditionFalse, "ArrangementEnded"),
			},
			wantStatus: metav1.ConditionFalse,
			wantReason: resourcemanagerv1alpha.OrganizationOnboardingCompleteReasonPaymentMethodNotReady,
		},
		{
			name:       "not ready with no billing conditions yet",
			wantStatus: metav1.ConditionFalse,
			wantReason: resourcemanagerv1alpha.OrganizationOnboardingCompleteReasonPaymentMethodNotReady,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			org := &resourcemanagerv1alpha.Organization{
				ObjectMeta: metav1.ObjectMeta{Name: "org-billing"},
				Spec: resourcemanagerv1alpha.OrganizationSpec{
					ContactInfo: &resourcemanagerv1alpha.OrganizationContactInfo{
						Email: "a@example.com",
						Name:  "Ada Lovelace",
					},
				},
			}
			account := &billingv1alpha1.BillingAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "default",
					Namespace: resourcemanagerv1alpha.OrganizationNamespace(org.Name),
				},
				Status: billingv1alpha1.BillingAccountStatus{Conditions: tt.conditions},
			}
			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(org, account).
				WithObjects(org, account).
				Build()

			if _, err := reconcileOrganizationOnboarding(t.Context(), client, org); err != nil {
				t.Fatalf("reconcileOrganizationOnboarding() error = %v", err)
			}
			for _, c := range org.Status.Conditions {
				if c.Type != resourcemanagerv1alpha.OrganizationConditionOnboardingComplete {
					continue
				}
				if c.Status != tt.wantStatus || c.Reason != tt.wantReason {
					t.Fatalf("OnboardingComplete = %s/%s, want %s/%s", c.Status, c.Reason, tt.wantStatus, tt.wantReason)
				}
				return
			}
			t.Fatal("OnboardingComplete condition not found")
		})
	}
}

func TestMapBillingAccountToOrganization(t *testing.T) {
	t.Parallel()

	account := &billingv1alpha1.BillingAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default",
			Namespace: "organization-org-abc",
		},
	}
	keys := mapBillingAccountToOrganization(t.Context(), account)
	if len(keys) != 1 || keys[0].Name != "org-abc" {
		t.Fatalf("unexpected keys: %#v", keys)
	}
}
