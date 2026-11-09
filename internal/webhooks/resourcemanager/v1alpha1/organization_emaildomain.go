package v1alpha1

import (
	"context"
	"fmt"
	"slices"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"go.miloapis.com/milo/internal/emaildomain"
	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	"go.miloapis.com/milo/pkg/features"
)

// validateEmailDomainJoining checks spec.emailDomainJoining when it changes:
// the feature gate must be on, each role must be one an invitation could
// grant, no newly added domain may be a public email provider such as
// gmail.com, and a verified Domain in one of the organization's projects must
// cover each other newly added domain. An unchanged setting is not re-checked, so a
// Domain that later loses verification doesn't block unrelated updates; the
// controller reports that through the EmailDomainsVerified condition instead.
// oldOrg is nil on create.
func (v *OrganizationValidator) validateEmailDomainJoining(ctx context.Context, oldOrg, newOrg *resourcemanagerv1alpha1.Organization) (field.ErrorList, error) {
	joining := newOrg.Spec.EmailDomainJoining
	if joining == nil {
		return nil, nil
	}
	var oldJoining *resourcemanagerv1alpha1.OrganizationEmailDomainJoining
	if oldOrg != nil {
		oldJoining = oldOrg.Spec.EmailDomainJoining
	}
	if oldJoining != nil &&
		slices.Equal(oldJoining.Domains, joining.Domains) &&
		slices.Equal(oldJoining.Roles, joining.Roles) {
		return nil, nil
	}

	fldPath := field.NewPath("spec", "emailDomainJoining")
	if !utilfeature.DefaultFeatureGate.Enabled(features.EmailDomainJoining) {
		return field.ErrorList{field.Forbidden(fldPath, "email domain joining is not available yet")}, nil
	}

	var errs field.ErrorList
	orgNamespace := fmt.Sprintf("organization-%s", newOrg.Name)
	allowedNamespaces := []string{orgNamespace, v.systemNamespace, v.assignableRolesNamespace}
	for i, role := range joining.Roles {
		rolePath := fldPath.Child("roles").Index(i)
		namespace := role.Namespace
		if namespace == "" {
			namespace = orgNamespace
		}
		if !slices.Contains(allowedNamespaces, namespace) {
			errs = append(errs, field.NotSupported(rolePath.Child("namespace"), role.Namespace, allowedNamespaces))
			continue
		}
		if err := v.client.Get(ctx, client.ObjectKey{Name: role.Name, Namespace: namespace}, &iamv1alpha1.Role{}); err != nil {
			if apierrors.IsNotFound(err) {
				errs = append(errs, field.NotFound(rolePath.Child("name"), fmt.Sprintf("%s/%s", namespace, role.Name)))
				continue
			}
			return nil, apierrors.NewInternalError(fmt.Errorf("get role %s/%s: %w", namespace, role.Name, err))
		}
	}

	var added, public []string
	for _, domain := range joining.Domains {
		if oldJoining != nil && slices.Contains(oldJoining.Domains, domain) {
			continue
		}
		if emaildomain.IsPublicProvider(domain) {
			public = append(public, domain)
			continue
		}
		added = append(added, domain)
	}
	if len(public) > 0 {
		errs = append(errs, field.Invalid(fldPath.Child("domains"), public, fmt.Sprintf(
			"%s is a public email provider, where anyone can get an address. "+
				"Email domain joining works only for a domain your organization owns, such as your company's domain",
			strings.Join(public, ", "))))
	}
	if len(added) == 0 {
		return errs, nil
	}

	verified, err := v.emailDomains.VerifiedDomains(ctx, newOrg.Name)
	if err != nil {
		return nil, apierrors.NewInternalError(fmt.Errorf("check verified Domains: %w", err))
	}
	var unverified []string
	for _, domain := range added {
		if !emaildomain.Covers(verified, domain) {
			unverified = append(unverified, domain)
		}
	}
	if len(unverified) > 0 {
		errs = append(errs, field.Invalid(fldPath.Child("domains"), unverified, emaildomain.Instructions(unverified)))
	}
	return errs, nil
}
