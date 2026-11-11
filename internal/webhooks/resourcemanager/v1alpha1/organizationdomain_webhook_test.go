package v1alpha1

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	featuregatetesting "k8s.io/component-base/featuregate/testing"

	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	"go.miloapis.com/milo/pkg/features"
)

func organizationDomainFor(namespace, name, domainName string) *resourcemanagerv1alpha1.OrganizationDomain {
	return &resourcemanagerv1alpha1.OrganizationDomain{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec:       resourcemanagerv1alpha1.OrganizationDomainSpec{DomainName: domainName},
	}
}

func TestOrganizationDomainValidator_ValidateCreate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ctx     context.Context
		gateOff bool
		domain  *resourcemanagerv1alpha1.OrganizationDomain
		wantErr string
	}{
		{
			name:   "an organization's domain",
			ctx:    membershipValidationContext(),
			domain: organizationDomainFor("organization-acme", "acme.com", "acme.com"),
		},
		{
			name:   "a subdomain in the organization's own context",
			ctx:    orgContextRequest("acme"),
			domain: organizationDomainFor("organization-acme", "eu.acme.com", "eu.acme.com"),
		},
		{
			name:   "a site under a private public suffix",
			ctx:    membershipValidationContext(),
			domain: organizationDomainFor("organization-acme", "acme.github.io", "acme.github.io"),
		},
		{
			name:    "the gate is off",
			ctx:     membershipValidationContext(),
			gateOff: true,
			domain:  organizationDomainFor("organization-acme", "acme.com", "acme.com"),
			wantErr: "organization domains are not available yet",
		},
		{
			name:    "another organization's namespace",
			ctx:     orgContextRequest("acme"),
			domain:  organizationDomainFor("organization-other", "acme.com", "acme.com"),
			wantErr: "must be in its namespace, organization-acme",
		},
		{
			name:    "a namespace that isn't an organization's",
			ctx:     membershipValidationContext(),
			domain:  organizationDomainFor("default", "acme.com", "acme.com"),
			wantErr: "must be in its namespace",
		},
		{
			name:    "a name that isn't the domain",
			ctx:     membershipValidationContext(),
			domain:  organizationDomainFor("organization-acme", "company", "acme.com"),
			wantErr: "name it acme.com",
		},
		{
			name:    "a public suffix",
			ctx:     membershipValidationContext(),
			domain:  organizationDomainFor("organization-acme", "co.uk", "co.uk"),
			wantErr: "co.uk is a public suffix",
		},
		{
			name:    "a public email provider",
			ctx:     membershipValidationContext(),
			domain:  organizationDomainFor("organization-acme", "gmail.com", "gmail.com"),
			wantErr: "gmail.com is a public email provider",
		},
		{
			name:    "upper case",
			ctx:     membershipValidationContext(),
			domain:  organizationDomainFor("organization-acme", "Acme.com", "Acme.com"),
			wantErr: "use lower case",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.EmailDomainJoining, !tc.gateOff)

			_, err := (&OrganizationDomainValidator{}).ValidateCreate(tc.ctx, tc.domain)

			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateCreate: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ValidateCreate error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestOrganizationDomainValidator_ValidateUpdateChecksTheOrganization(t *testing.T) {
	domain := organizationDomainFor("organization-other", "acme.com", "acme.com")
	_, err := (&OrganizationDomainValidator{}).ValidateUpdate(orgContextRequest("acme"), domain, domain)
	if err == nil || !strings.Contains(err.Error(), "organization-acme") {
		t.Fatalf("ValidateUpdate error = %v, want a namespace error", err)
	}
}
