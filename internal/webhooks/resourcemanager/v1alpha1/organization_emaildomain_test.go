package v1alpha1

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	featuregatetesting "k8s.io/component-base/featuregate/testing"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"go.miloapis.com/milo/internal/emaildomain"
	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	"go.miloapis.com/milo/pkg/features"
)

func TestOrganizationValidator_validateEmailDomainJoining(t *testing.T) {
	scheme := runtime.NewScheme()
	utilruntime.Must(resourcemanagerv1alpha1.AddToScheme(scheme))
	utilruntime.Must(iamv1alpha1.AddToScheme(scheme))

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
	viewer := &iamv1alpha1.Role{ObjectMeta: metav1.ObjectMeta{Name: "viewer", Namespace: "datum-cloud"}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(project, viewer).Build()

	validator := &OrganizationValidator{
		client:                   c,
		systemNamespace:          "milo-system",
		assignableRolesNamespace: "datum-cloud",
		emailDomains: &emaildomain.Verifier{
			Client: c,
			ListDomains: func(context.Context, string) ([]unstructured.Unstructured, error) {
				return []unstructured.Unstructured{{Object: map[string]any{
					"spec":   map[string]any{"domainName": "example.com"},
					"status": map[string]any{"conditions": []any{map[string]any{"type": "Verified", "status": "True"}}},
				}}}, nil
			},
		},
	}

	viewerRole := []resourcemanagerv1alpha1.RoleReference{{Name: "viewer", Namespace: "datum-cloud"}}
	org := func(roles []resourcemanagerv1alpha1.RoleReference, domains ...string) *resourcemanagerv1alpha1.Organization {
		o := &resourcemanagerv1alpha1.Organization{ObjectMeta: metav1.ObjectMeta{Name: "acme"}}
		if len(domains) > 0 {
			o.Spec.EmailDomainJoining = &resourcemanagerv1alpha1.OrganizationEmailDomainJoining{Domains: domains, Roles: roles}
		}
		return o
	}

	for _, tc := range []struct {
		name       string
		gate       bool
		oldOrg     *resourcemanagerv1alpha1.Organization
		newOrg     *resourcemanagerv1alpha1.Organization
		wantErrors []string
	}{
		{
			name:   "not set",
			gate:   true,
			newOrg: org(nil),
		},
		{
			name:       "feature gate off",
			newOrg:     org(viewerRole, "example.com"),
			wantErrors: []string{"not available yet"},
		},
		{
			name:   "domain covered by a verified project Domain",
			gate:   true,
			oldOrg: org(nil),
			newOrg: org(viewerRole, "example.com", "eu.example.com"),
		},
		{
			name:       "domain without a verified Domain gets instructions",
			gate:       true,
			newOrg:     org(viewerRole, "example.com", "other.io"),
			wantErrors: []string{"other.io", "DNS TXT record"},
		},
		{
			name:   "unchanged setting is not re-checked",
			gate:   false,
			oldOrg: org(viewerRole, "other.io"),
			newOrg: org(viewerRole, "other.io"),
		},
		{
			name:   "only added domains are checked",
			gate:   true,
			oldOrg: org(viewerRole, "other.io"),
			newOrg: org(viewerRole, "other.io", "example.com"),
		},
		{
			name:       "role outside the allowed namespaces",
			gate:       true,
			newOrg:     org([]resourcemanagerv1alpha1.RoleReference{{Name: "admin", Namespace: "kube-system"}}, "example.com"),
			wantErrors: []string{"roles[0].namespace"},
		},
		{
			name:       "role that doesn't exist",
			gate:       true,
			newOrg:     org([]resourcemanagerv1alpha1.RoleReference{{Name: "missing", Namespace: "datum-cloud"}}, "example.com"),
			wantErrors: []string{"datum-cloud/missing"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.EmailDomainJoining, tc.gate)

			errs, err := validator.validateEmailDomainJoining(context.Background(), tc.oldOrg, tc.newOrg)
			if err != nil {
				t.Fatalf("validateEmailDomainJoining error: %v", err)
			}
			if len(tc.wantErrors) == 0 {
				if len(errs) > 0 {
					t.Fatalf("unexpected errors: %v", errs)
				}
				return
			}
			got := errs.ToAggregate()
			if got == nil {
				t.Fatalf("expected errors containing %v, got none", tc.wantErrors)
			}
			for _, want := range tc.wantErrors {
				if !strings.Contains(got.Error(), want) {
					t.Errorf("errors %q don't contain %q", got.Error(), want)
				}
			}
		})
	}
}
