package resourcemanager

import (
	"context"
	"fmt"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	resourcemanagerv1alpha "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func reconcileOrganizationOnboarding(
	ctx context.Context,
	c client.Client,
	organization *resourcemanagerv1alpha.Organization,
) (bool, error) {
	originalStatus := organization.Status.DeepCopy()

	organization.Status.ObservedGeneration = organization.Generation

	onboardingCondition := apimeta.FindStatusCondition(organization.Status.Conditions, resourcemanagerv1alpha.OrganizationConditionOnboardingComplete)
	if onboardingCondition == nil {
		onboardingCondition = &metav1.Condition{
			Type:               resourcemanagerv1alpha.OrganizationConditionOnboardingComplete,
			Status:             metav1.ConditionFalse,
			Reason:             resourcemanagerv1alpha.OrganizationOnboardingCompleteReasonContactInfoIncomplete,
			Message:            "Organization contact information is incomplete",
			ObservedGeneration: organization.Generation,
		}
	} else {
		onboardingCondition = onboardingCondition.DeepCopy()
		onboardingCondition.ObservedGeneration = organization.Generation
	}

	if !resourcemanagerv1alpha.IsOrganizationContactInfoComplete(organization.Spec.ContactInfo) {
		onboardingCondition.Status = metav1.ConditionFalse
		onboardingCondition.Reason = resourcemanagerv1alpha.OrganizationOnboardingCompleteReasonContactInfoIncomplete
		onboardingCondition.Message = "Organization contact information requires email and name"
		apimeta.SetStatusCondition(&organization.Status.Conditions, *onboardingCondition)
		return statusChanged(originalStatus, &organization.Status), nil
	}

	namespaceName := resourcemanagerv1alpha.OrganizationNamespace(organization.Name)
	var billingAccounts billingv1alpha1.BillingAccountList
	if err := c.List(ctx, &billingAccounts, client.InNamespace(namespaceName)); err != nil {
		if apimeta.IsNoMatchError(err) {
			onboardingCondition.Status = metav1.ConditionFalse
			onboardingCondition.Reason = resourcemanagerv1alpha.OrganizationOnboardingCompleteReasonBillingAccountMissing
			onboardingCondition.Message = "Organization does not have a billing account"
			apimeta.SetStatusCondition(&organization.Status.Conditions, *onboardingCondition)
			return statusChanged(originalStatus, &organization.Status), nil
		}
		return false, fmt.Errorf("failed to list billing accounts for organization: %w", err)
	}

	if len(billingAccounts.Items) == 0 {
		onboardingCondition.Status = metav1.ConditionFalse
		onboardingCondition.Reason = resourcemanagerv1alpha.OrganizationOnboardingCompleteReasonBillingAccountMissing
		onboardingCondition.Message = "Organization does not have a billing account"
		apimeta.SetStatusCondition(&organization.Status.Conditions, *onboardingCondition)
		return statusChanged(originalStatus, &organization.Status), nil
	}

	for i := range billingAccounts.Items {
		if billingAccountCanBeBilled(&billingAccounts.Items[i]) {
			onboardingCondition.Status = metav1.ConditionTrue
			onboardingCondition.Reason = resourcemanagerv1alpha.OrganizationOnboardingCompleteReasonReady
			onboardingCondition.Message = "Organization onboarding is complete"
			apimeta.SetStatusCondition(&organization.Status.Conditions, *onboardingCondition)
			return statusChanged(originalStatus, &organization.Status), nil
		}
	}

	onboardingCondition.Status = metav1.ConditionFalse
	onboardingCondition.Reason = resourcemanagerv1alpha.OrganizationOnboardingCompleteReasonPaymentMethodNotReady
	onboardingCondition.Message = "Organization billing account does not have an active payment method or payment terms"
	apimeta.SetStatusCondition(&organization.Status.Conditions, *onboardingCondition)
	return statusChanged(originalStatus, &organization.Status), nil
}

// billingAccountCanBeBilled reports whether a billing account has a way to
// pay. Billing's PaymentReady condition is the signal: it's True for an
// active default payment method or for payment terms staff have granted.
//
// DefaultPaymentMethodReady is still accepted so that milo and billing can
// deploy in either order: billing accounts reconciled by an older billing
// controller don't have PaymentReady yet. Remove the fallback once every
// environment runs billing v0.3.10 or later.
func billingAccountCanBeBilled(account *billingv1alpha1.BillingAccount) bool {
	return apimeta.IsStatusConditionTrue(account.Status.Conditions, billingv1alpha1.BillingAccountConditionPaymentReady) ||
		apimeta.IsStatusConditionTrue(account.Status.Conditions, billingv1alpha1.BillingAccountConditionDefaultPaymentMethodReady)
}

func statusChanged(before, after *resourcemanagerv1alpha.OrganizationStatus) bool {
	if before.ObservedGeneration != after.ObservedGeneration {
		return true
	}
	beforeOnboarding := apimeta.FindStatusCondition(before.Conditions, resourcemanagerv1alpha.OrganizationConditionOnboardingComplete)
	afterOnboarding := apimeta.FindStatusCondition(after.Conditions, resourcemanagerv1alpha.OrganizationConditionOnboardingComplete)
	if beforeOnboarding == nil && afterOnboarding == nil {
		return false
	}
	if beforeOnboarding == nil || afterOnboarding == nil {
		return true
	}
	return beforeOnboarding.Status != afterOnboarding.Status ||
		beforeOnboarding.Reason != afterOnboarding.Reason ||
		beforeOnboarding.Message != afterOnboarding.Message
}

func mapBillingAccountToOrganization(_ context.Context, obj client.Object) []client.ObjectKey {
	account, ok := obj.(*billingv1alpha1.BillingAccount)
	if !ok {
		return nil
	}

	orgName, ok := organizationNameFromNamespace(account.Namespace)
	if !ok {
		return nil
	}

	return []client.ObjectKey{{Name: orgName}}
}

func organizationNameFromNamespace(namespace string) (string, bool) {
	const prefix = "organization-"
	if len(namespace) <= len(prefix) || namespace[:len(prefix)] != prefix {
		return "", false
	}
	return namespace[len(prefix):], true
}
