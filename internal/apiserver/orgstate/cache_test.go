package orgstate_test

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic/fake"

	"go.miloapis.com/milo/internal/apiserver/orgstate"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
)

func newOrganization(name string, onboarding *metav1.Condition) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "resourcemanager.miloapis.com/v1alpha1",
		"kind":       "Organization",
		"metadata":   map[string]interface{}{"name": name},
	}}
	if onboarding != nil {
		u.Object["status"] = map[string]interface{}{
			"conditions": []interface{}{
				map[string]interface{}{"type": "Ready", "status": "True", "reason": "Ready"},
				map[string]interface{}{"type": onboarding.Type, "status": string(onboarding.Status), "reason": onboarding.Reason},
			},
		}
	}
	return u
}

func onboarding(status metav1.ConditionStatus, reason string) *metav1.Condition {
	return &metav1.Condition{Type: resourcemanagerv1alpha1.OrganizationConditionOnboardingComplete, Status: status, Reason: reason}
}

func startCache(t *testing.T, objects ...runtime.Object) (*orgstate.Cache, *fake.FakeDynamicClient) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := resourcemanagerv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	client := fake.NewSimpleDynamicClient(scheme, objects...)
	c := orgstate.NewCache(client)
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	c.Start(stop)
	eventually(t, c.HasSynced)
	return c, client
}

func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("condition not met in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestOnboarding_ReadsTheCondition(t *testing.T) {
	c, _ := startCache(t,
		newOrganization("org-paid", onboarding(metav1.ConditionTrue, "Ready")),
		newOrganization("org-unpaid", onboarding(metav1.ConditionFalse, "PaymentMethodNotReady")),
		newOrganization("org-new", nil),
	)

	cases := map[string]orgstate.Onboarding{
		"org-paid":   {Complete: true, Reason: "Ready"},
		"org-unpaid": {Complete: false, Reason: "PaymentMethodNotReady"},
		"org-new":    {},
	}
	for name, want := range cases {
		got, ok := c.Onboarding(name)
		if !ok || got != want {
			t.Errorf("Onboarding(%q) = %+v, %v; want %+v, true", name, got, ok, want)
		}
	}
	if _, ok := c.Onboarding("org-missing"); ok {
		t.Errorf("Onboarding(org-missing) found, want not found")
	}
}

func TestOnboarding_FollowsUpdates(t *testing.T) {
	org := newOrganization("org-1", onboarding(metav1.ConditionFalse, "PaymentMethodNotReady"))
	c, client := startCache(t, org)

	updated := newOrganization("org-1", onboarding(metav1.ConditionTrue, "Ready"))
	eventually(t, func() bool {
		_, err := client.Resource(orgstate.OrganizationGVR).Update(context.Background(), updated, metav1.UpdateOptions{})
		return err == nil
	})
	eventually(t, func() bool {
		o, _ := c.Onboarding("org-1")
		return o.Complete
	})

	if err := client.Resource(orgstate.OrganizationGVR).Delete(context.Background(), "org-1", metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	eventually(t, func() bool {
		_, ok := c.Onboarding("org-1")
		return !ok
	})
}
