package resourcemanager

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	featuregatetesting "k8s.io/component-base/featuregate/testing"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	"go.miloapis.com/milo/pkg/features"
)

var organizationDomainKey = client.ObjectKey{Namespace: "organization-acme", Name: "acme.com"}

// dnsZone fakes the TXT records at each name. A name that isn't listed
// doesn't exist.
type dnsZone struct {
	records map[string][]string
	err     error
	lookups int
}

func (z *dnsZone) lookupTXT(_ context.Context, name string) ([]string, error) {
	z.lookups++
	if z.err != nil {
		return nil, z.err
	}
	values, ok := z.records[name]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
	}
	return values, nil
}

func newOrganizationDomainTest(t *testing.T, zone *dnsZone, now time.Time, objs ...client.Object) (*OrganizationDomainController, client.Client) {
	t.Helper()
	featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.EmailDomainJoining, true)

	scheme := runtime.NewScheme()
	utilruntime.Must(resourcemanagerv1alpha1.AddToScheme(scheme))
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&resourcemanagerv1alpha1.OrganizationDomain{}).
		Build()
	return &OrganizationDomainController{
		Client:    c,
		LookupTXT: zone.lookupTXT,
		Now:       func() time.Time { return now },
	}, c
}

func newOrganizationDomain(created time.Time) *resourcemanagerv1alpha1.OrganizationDomain {
	return &resourcemanagerv1alpha1.OrganizationDomain{
		ObjectMeta: metav1.ObjectMeta{
			Name:              organizationDomainKey.Name,
			Namespace:         organizationDomainKey.Namespace,
			CreationTimestamp: metav1.NewTime(created),
		},
		Spec: resourcemanagerv1alpha1.OrganizationDomainSpec{DomainName: "acme.com"},
	}
}

func reconcileOrganizationDomain(t *testing.T, r *OrganizationDomainController, c client.Client) (ctrl.Result, *resourcemanagerv1alpha1.OrganizationDomain) {
	t.Helper()
	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: organizationDomainKey})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	got := &resourcemanagerv1alpha1.OrganizationDomain{}
	if err := c.Get(context.Background(), organizationDomainKey, got); err != nil {
		t.Fatalf("get OrganizationDomain: %v", err)
	}
	return result, got
}

func verifiedCondition(t *testing.T, d *resourcemanagerv1alpha1.OrganizationDomain) *metav1.Condition {
	t.Helper()
	c := apimeta.FindStatusCondition(d.Status.Conditions, resourcemanagerv1alpha1.OrganizationDomainConditionVerified)
	if c == nil {
		t.Fatal("no Verified condition")
	}
	return c
}

func TestOrganizationDomainController_PublishesRecordAndLooksForIt(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	zone := &dnsZone{}
	r, c := newOrganizationDomainTest(t, zone, now, newOrganizationDomain(now))

	result, got := reconcileOrganizationDomain(t, r, c)

	v := got.Status.Verification
	if v == nil {
		t.Fatal("no verification record published")
	}
	if v.DNSRecord.Name != "_milo-verification.acme.com" || v.DNSRecord.Type != "TXT" || len(v.DNSRecord.Content) != 32 {
		t.Errorf("record = %+v, want a 32 character TXT value at _milo-verification.acme.com", v.DNSRecord)
	}
	cond := verifiedCondition(t, got)
	if cond.Status != metav1.ConditionFalse || cond.Reason != resourcemanagerv1alpha1.OrganizationDomainReasonRecordNotFound {
		t.Errorf("condition = %s/%s, want False/RecordNotFound", cond.Status, cond.Reason)
	}
	if !strings.Contains(cond.Message, v.DNSRecord.Name) || !strings.Contains(cond.Message, v.DNSRecord.Content) {
		t.Errorf("message %q doesn't say which record to add", cond.Message)
	}
	if zone.lookups != 1 {
		t.Errorf("lookups = %d, want 1", zone.lookups)
	}
	if result.RequeueAfter != time.Minute {
		t.Errorf("RequeueAfter = %v, want 1m", result.RequeueAfter)
	}
	if v.NextAttemptTime == nil || !v.NextAttemptTime.Time.Equal(now.Add(time.Minute)) {
		t.Errorf("NextAttemptTime = %v, want %v", v.NextAttemptTime, now.Add(time.Minute))
	}
}

func TestOrganizationDomainController_VerifiesOnceTheRecordExists(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	zone := &dnsZone{}
	r, c := newOrganizationDomainTest(t, zone, now, newOrganizationDomain(now))
	_, pending := reconcileOrganizationDomain(t, r, c)
	token := pending.Status.Verification.DNSRecord.Content

	// The owner adds the record next to an unrelated one; the next attempt is due.
	zone.records = map[string][]string{"_milo-verification.acme.com": {"something-else", " " + token + " "}}
	r.Now = func() time.Time { return now.Add(time.Minute) }

	result, got := reconcileOrganizationDomain(t, r, c)

	cond := verifiedCondition(t, got)
	if cond.Status != metav1.ConditionTrue || cond.Reason != resourcemanagerv1alpha1.OrganizationDomainReasonVerified {
		t.Errorf("condition = %s/%s, want True/Verified", cond.Status, cond.Reason)
	}
	if got.Status.Verification != nil {
		t.Errorf("verification = %+v, want it cleared once verified", got.Status.Verification)
	}
	if result.RequeueAfter != 0 {
		t.Errorf("RequeueAfter = %v, want none once verified", result.RequeueAfter)
	}

	// A verified domain stays verified without looking again.
	zone.records = nil
	lookups := zone.lookups
	_, got = reconcileOrganizationDomain(t, r, c)
	if verifiedCondition(t, got).Status != metav1.ConditionTrue || zone.lookups != lookups {
		t.Errorf("a verified domain was checked again (lookups %d -> %d)", lookups, zone.lookups)
	}
}

func TestOrganizationDomainController_WaitsForTheNextAttempt(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	zone := &dnsZone{}
	r, c := newOrganizationDomainTest(t, zone, now, newOrganizationDomain(now))
	_, first := reconcileOrganizationDomain(t, r, c)

	r.Now = func() time.Time { return now.Add(20 * time.Second) }
	result, got := reconcileOrganizationDomain(t, r, c)

	if zone.lookups != 1 {
		t.Errorf("lookups = %d, want 1: the next attempt isn't due", zone.lookups)
	}
	if result.RequeueAfter != 40*time.Second {
		t.Errorf("RequeueAfter = %v, want 40s", result.RequeueAfter)
	}
	if got.Status.Verification.DNSRecord != first.Status.Verification.DNSRecord {
		t.Errorf("record changed from %+v to %+v", first.Status.Verification.DNSRecord, got.Status.Verification.DNSRecord)
	}
}

func TestOrganizationDomainController_ReportsWhyTheRecordWasNotAccepted(t *testing.T) {
	for _, tc := range []struct {
		name       string
		zone       *dnsZone
		wantReason string
	}{
		{
			name:       "other values at the record name",
			zone:       &dnsZone{records: map[string][]string{"_milo-verification.acme.com": {"wrong"}}},
			wantReason: resourcemanagerv1alpha1.OrganizationDomainReasonRecordMismatch,
		},
		{
			name:       "lookup failure",
			zone:       &dnsZone{err: errors.New("i/o timeout")},
			wantReason: resourcemanagerv1alpha1.OrganizationDomainReasonLookupFailed,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
			r, c := newOrganizationDomainTest(t, tc.zone, now, newOrganizationDomain(now))
			_, got := reconcileOrganizationDomain(t, r, c)
			cond := verifiedCondition(t, got)
			if cond.Status != metav1.ConditionFalse || cond.Reason != tc.wantReason {
				t.Errorf("condition = %s/%s, want False/%s", cond.Status, cond.Reason, tc.wantReason)
			}
		})
	}
}

func TestOrganizationDomainController_UsesTheConfiguredRecordPrefix(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	r, c := newOrganizationDomainTest(t, &dnsZone{}, now, newOrganizationDomain(now))
	r.RecordPrefix = "_datum-verification"

	_, got := reconcileOrganizationDomain(t, r, c)

	if name := got.Status.Verification.DNSRecord.Name; name != "_datum-verification.acme.com" {
		t.Errorf("record name = %q, want _datum-verification.acme.com", name)
	}
}

func TestOrganizationDomainController_DoesNothingWithTheGateOff(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	zone := &dnsZone{}
	r, c := newOrganizationDomainTest(t, zone, now, newOrganizationDomain(now))
	featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.EmailDomainJoining, false)

	_, got := reconcileOrganizationDomain(t, r, c)

	if got.Status.Verification != nil || zone.lookups != 0 {
		t.Errorf("verification = %+v, lookups = %d; want nothing done", got.Status.Verification, zone.lookups)
	}
}

func TestVerificationInterval(t *testing.T) {
	for age, want := range map[time.Duration]time.Duration{
		0:                   time.Minute,
		59 * time.Minute:    time.Minute,
		2 * time.Hour:       5 * time.Minute,
		3 * 24 * time.Hour:  30 * time.Minute,
		30 * 24 * time.Hour: 6 * time.Hour,
	} {
		if got := verificationInterval(age); got != want {
			t.Errorf("verificationInterval(%v) = %v, want %v", age, got, want)
		}
	}
}
