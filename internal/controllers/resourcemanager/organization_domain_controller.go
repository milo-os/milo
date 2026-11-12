package resourcemanager

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	"go.miloapis.com/milo/pkg/features"
)

const (
	// DefaultOrganizationDomainRecordPrefix is the label in front of the domain
	// in an OrganizationDomain's TXT record name.
	DefaultOrganizationDomainRecordPrefix = "_milo-verification"

	organizationDomainLookupTimeout = 5 * time.Second
)

// OrganizationDomainController verifies OrganizationDomains. It publishes a
// TXT record for each one, looks for that record at the domain, and sets the
// Verified condition once it finds it. A verified domain stays verified, so
// the record can be removed afterwards.
type OrganizationDomainController struct {
	Client client.Client

	// RecordPrefix is the label in front of the domain in the TXT record name.
	// Defaults to DefaultOrganizationDomainRecordPrefix.
	RecordPrefix string

	// LookupTXT looks up TXT records. Defaults to net.DefaultResolver.
	LookupTXT func(ctx context.Context, name string) ([]string, error)

	// Now returns the current time. Defaults to time.Now.
	Now func() time.Time
}

// +kubebuilder:rbac:groups=resourcemanager.miloapis.com,resources=organizationdomains,verbs=get;list;watch
// +kubebuilder:rbac:groups=resourcemanager.miloapis.com,resources=organizationdomains/status,verbs=get;update;patch

func (r *OrganizationDomainController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if !utilfeature.DefaultFeatureGate.Enabled(features.EmailDomainJoining) {
		return ctrl.Result{}, nil
	}
	log := logf.FromContext(ctx).WithName("organizationdomain-reconciler")

	domain := &resourcemanagerv1alpha1.OrganizationDomain{}
	if err := r.Client.Get(ctx, req.NamespacedName, domain); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if domain.DeletionTimestamp != nil ||
		apimeta.IsStatusConditionTrue(domain.Status.Conditions, resourcemanagerv1alpha1.OrganizationDomainConditionVerified) {
		return ctrl.Result{}, nil
	}

	now := r.now()
	status := domain.Status.DeepCopy()
	recordName := r.recordName(domain.Spec.DomainName)

	// Publish the record to add. Keep an existing token so a record the owner
	// already added stays valid.
	if status.Verification == nil || status.Verification.DNSRecord.Content == "" {
		token, err := newVerificationToken()
		if err != nil {
			return ctrl.Result{}, err
		}
		status.Verification = &resourcemanagerv1alpha1.OrganizationDomainVerification{
			DNSRecord: resourcemanagerv1alpha1.OrganizationDomainDNSRecord{Content: token},
		}
	}
	status.Verification.DNSRecord.Name = recordName
	status.Verification.DNSRecord.Type = "TXT"

	// Wait for the next attempt unless the record has just been published.
	published := domain.Status.Verification != nil &&
		domain.Status.Verification.DNSRecord == status.Verification.DNSRecord
	if next := status.Verification.NextAttemptTime; published && next != nil && next.After(now) {
		return ctrl.Result{RequeueAfter: next.Sub(now)}, nil
	}

	condition := r.check(ctx, status.Verification.DNSRecord)
	condition.ObservedGeneration = domain.Generation
	apimeta.SetStatusCondition(&status.Conditions, condition)

	result := ctrl.Result{}
	if condition.Status == metav1.ConditionTrue {
		status.Verification = nil
		log.Info("Verified OrganizationDomain", "domain", domain.Spec.DomainName)
	} else {
		interval := verificationInterval(now.Sub(domain.CreationTimestamp.Time))
		status.Verification.LastAttemptTime = &metav1.Time{Time: now}
		status.Verification.NextAttemptTime = &metav1.Time{Time: now.Add(interval)}
		result.RequeueAfter = interval
	}

	domain.Status = *status
	if err := r.Client.Status().Update(ctx, domain); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to update OrganizationDomain status: %w", err)
	}
	return result, nil
}

// check looks for the TXT record and returns the Verified condition.
func (r *OrganizationDomainController) check(ctx context.Context, record resourcemanagerv1alpha1.OrganizationDomainDNSRecord) metav1.Condition {
	condition := metav1.Condition{
		Type:   resourcemanagerv1alpha1.OrganizationDomainConditionVerified,
		Status: metav1.ConditionFalse,
	}

	lookupCtx, cancel := context.WithTimeout(ctx, organizationDomainLookupTimeout)
	defer cancel()
	values, err := r.lookupTXT(lookupCtx, record.Name)

	var dnsErr *net.DNSError
	switch {
	case err == nil && slices.ContainsFunc(values, func(v string) bool { return strings.TrimSpace(v) == record.Content }):
		condition.Status = metav1.ConditionTrue
		condition.Reason = resourcemanagerv1alpha1.OrganizationDomainReasonVerified
		condition.Message = fmt.Sprintf("Found the TXT record %s", record.Name)
	case (err == nil && len(values) == 0) || (errors.As(err, &dnsErr) && dnsErr.IsNotFound):
		condition.Reason = resourcemanagerv1alpha1.OrganizationDomainReasonRecordNotFound
		condition.Message = fmt.Sprintf(
			"Add a TXT record named %s with the value %s at your DNS provider. "+
				"Milo checks for it regularly, and new records can take a while to appear.",
			record.Name, record.Content)
	case err == nil:
		condition.Reason = resourcemanagerv1alpha1.OrganizationDomainReasonRecordMismatch
		condition.Message = fmt.Sprintf(
			"The TXT record %s doesn't hold the value %s yet. Add a TXT record with that value; "+
				"other values at the same name can stay.",
			record.Name, record.Content)
	default:
		condition.Reason = resourcemanagerv1alpha1.OrganizationDomainReasonLookupFailed
		condition.Message = fmt.Sprintf("Couldn't look up the TXT record %s, so Milo will try again: %v", record.Name, err)
	}
	return condition
}

func (r *OrganizationDomainController) recordName(domainName string) string {
	prefix := r.RecordPrefix
	if prefix == "" {
		prefix = DefaultOrganizationDomainRecordPrefix
	}
	return prefix + "." + domainName
}

func (r *OrganizationDomainController) lookupTXT(ctx context.Context, name string) ([]string, error) {
	if r.LookupTXT != nil {
		return r.LookupTXT(ctx, name)
	}
	return net.DefaultResolver.LookupTXT(ctx, name)
}

func (r *OrganizationDomainController) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// verificationInterval is how long to wait before looking for the record
// again. Owners usually add the record soon after adding the domain, so look
// often at first and back off for domains that stay unverified.
func verificationInterval(age time.Duration) time.Duration {
	switch {
	case age < time.Hour:
		return time.Minute
	case age < 24*time.Hour:
		return 5 * time.Minute
	case age < 7*24*time.Hour:
		return 30 * time.Minute
	default:
		return 6 * time.Hour
	}
}

func newVerificationToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate verification token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func (r *OrganizationDomainController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&resourcemanagerv1alpha1.OrganizationDomain{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("organization-domain").
		Complete(r)
}
