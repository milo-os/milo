package v1alpha1

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/net/publicsuffix"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"go.miloapis.com/milo/internal/emaildomain"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	"go.miloapis.com/milo/pkg/features"
	"go.miloapis.com/milo/pkg/webhook"
)

var organizationdomainlog = logf.Log.WithName("organizationdomain-resource")

const organizationNamespacePrefix = "organization-"

// +kubebuilder:webhook:path=/validate-resourcemanager-miloapis-com-v1alpha1-organizationdomain,mutating=false,failurePolicy=fail,sideEffects=None,groups=resourcemanager.miloapis.com,resources=organizationdomains,verbs=create;update,versions=v1alpha1,name=vorganizationdomain.datum.net,admissionReviewVersions={v1,v1beta1},serviceName=milo-controller-manager,servicePort=9443,serviceNamespace=milo-system

// SetupOrganizationDomainWebhooksWithManager sets up OrganizationDomain webhooks.
func SetupOrganizationDomainWebhooksWithManager(mgr ctrl.Manager) error {
	organizationdomainlog.Info("Setting up resourcemanager.miloapis.com organizationdomain webhooks")

	return ctrl.NewWebhookManagedBy(mgr, &resourcemanagerv1alpha1.OrganizationDomain{}).
		WithValidator(&OrganizationDomainValidator{}).
		Complete()
}

// OrganizationDomainValidator validates OrganizationDomains. A domain must live
// in an organization's namespace, be named after itself, and be one an
// organization can own: not a public suffix such as co.uk, and not a public
// email provider such as gmail.com.
type OrganizationDomainValidator struct{}

func (v *OrganizationDomainValidator) ValidateCreate(ctx context.Context, domain *resourcemanagerv1alpha1.OrganizationDomain) (admission.Warnings, error) {
	var errs field.ErrorList
	if !utilfeature.DefaultFeatureGate.Enabled(features.EmailDomainJoining) {
		errs = append(errs, field.Forbidden(field.NewPath("spec"), "organization domains are not available yet"))
		return nil, invalidOrganizationDomain(domain, errs)
	}

	orgErrs, err := validateOrganizationDomainNamespace(ctx, domain)
	if err != nil {
		return nil, err
	}
	errs = append(errs, orgErrs...)
	errs = append(errs, validateOrganizationDomainName(domain)...)
	return nil, invalidOrganizationDomain(domain, errs)
}

func (v *OrganizationDomainValidator) ValidateUpdate(ctx context.Context, _, domain *resourcemanagerv1alpha1.OrganizationDomain) (admission.Warnings, error) {
	// The CRD keeps spec.domainName from changing, so only the namespace needs
	// checking against the request organization.
	errs, err := validateOrganizationDomainNamespace(ctx, domain)
	if err != nil {
		return nil, err
	}
	return nil, invalidOrganizationDomain(domain, errs)
}

func (v *OrganizationDomainValidator) ValidateDelete(context.Context, *resourcemanagerv1alpha1.OrganizationDomain) (admission.Warnings, error) {
	return nil, nil
}

// validateOrganizationDomainNamespace checks that the domain lives in an
// organization's namespace and, for a request made in an organization's
// context, in that organization's namespace.
func validateOrganizationDomainNamespace(ctx context.Context, domain *resourcemanagerv1alpha1.OrganizationDomain) (field.ErrorList, error) {
	fldPath := field.NewPath("metadata", "namespace")
	orgID, isOrgContext, err := webhook.OrgContextFromRequest(ctx)
	if err != nil {
		return nil, err
	}
	if isOrgContext {
		if want := resourcemanagerv1alpha1.OrganizationNamespace(orgID); domain.Namespace != want {
			return field.ErrorList{field.Invalid(fldPath, domain.Namespace,
				fmt.Sprintf("an organization's domains must be in its namespace, %s", want))}, nil
		}
		return nil, nil
	}
	if !strings.HasPrefix(domain.Namespace, organizationNamespacePrefix) ||
		domain.Namespace == organizationNamespacePrefix {
		return field.ErrorList{field.Invalid(fldPath, domain.Namespace,
			"an organization's domains must be in its namespace, organization-<organization name>")}, nil
	}
	return nil, nil
}

// validateOrganizationDomainName checks the domain name an organization wants
// to verify.
func validateOrganizationDomainName(domain *resourcemanagerv1alpha1.OrganizationDomain) field.ErrorList {
	var errs field.ErrorList
	name := domain.Spec.DomainName
	namePath := field.NewPath("spec", "domainName")

	if domain.Name != name {
		errs = append(errs, field.Invalid(field.NewPath("metadata", "name"), domain.Name,
			fmt.Sprintf("an OrganizationDomain must be named after its domain; name it %s", name)))
	}
	if name != strings.ToLower(name) {
		errs = append(errs, field.Invalid(namePath, name, "use lower case, such as acme.com"))
		return errs
	}
	if _, err := publicsuffix.EffectiveTLDPlusOne(name); err != nil {
		errs = append(errs, field.Invalid(namePath, name, fmt.Sprintf(
			"%s is a public suffix that anyone can register names under. Use a domain your organization owns, such as acme.com",
			name)))
		return errs
	}
	if emaildomain.IsPublicProvider(name) {
		errs = append(errs, field.Invalid(namePath, name, fmt.Sprintf(
			"%s is a public email provider, where anyone can get an address. Use a domain your organization owns, such as your company's domain",
			name)))
	}
	return errs
}

func invalidOrganizationDomain(domain *resourcemanagerv1alpha1.OrganizationDomain, errs field.ErrorList) error {
	if len(errs) == 0 {
		return nil
	}
	return apierrors.NewInvalid(
		resourcemanagerv1alpha1.GroupVersion.WithKind("OrganizationDomain").GroupKind(), domain.Name, errs)
}
