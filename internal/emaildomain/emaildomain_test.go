package emaildomain

import (
	"context"
	"errors"
	"slices"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
)

func TestOf(t *testing.T) {
	for email, want := range map[string]string{
		"jane@Example.COM":    "example.com",
		"a@b@eu.example.com":  "eu.example.com",
		"no-domain@":          "",
		"not-an-email":        "",
		"jane@sub.example.io": "sub.example.io",
	} {
		if got := Of(email); got != want {
			t.Errorf("Of(%q) = %q, want %q", email, got, want)
		}
	}
}

func TestCovers(t *testing.T) {
	for _, tc := range []struct {
		verified    []string
		emailDomain string
		want        bool
	}{
		{[]string{"example.com"}, "example.com", true},
		{[]string{"example.com"}, "eu.example.com", true},
		{[]string{"Example.com."}, "EU.example.com", true},
		{[]string{"www.example.com"}, "example.com", false},
		{[]string{"example.com"}, "badexample.com", false},
		{[]string{"example.com"}, "example.co", false},
		{nil, "example.com", false},
	} {
		if got := Covers(tc.verified, tc.emailDomain); got != tc.want {
			t.Errorf("Covers(%v, %q) = %v, want %v", tc.verified, tc.emailDomain, got, tc.want)
		}
	}
}

func project(name, org string, ready bool) *resourcemanagerv1alpha1.Project {
	status := metav1.ConditionFalse
	if ready {
		status = metav1.ConditionTrue
	}
	return &resourcemanagerv1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{resourcemanagerv1alpha1.OrganizationNameLabel: org},
		},
		Spec: resourcemanagerv1alpha1.ProjectSpec{
			OwnerRef: resourcemanagerv1alpha1.OwnerReference{Kind: "Organization", Name: org},
		},
		Status: resourcemanagerv1alpha1.ProjectStatus{
			Conditions: []metav1.Condition{{Type: resourcemanagerv1alpha1.ProjectReady, Status: status}},
		},
	}
}

func domain(name string, verified metav1.ConditionStatus) unstructured.Unstructured {
	return unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{"domainName": name},
		"status": map[string]any{"conditions": []any{
			map[string]any{"type": "VerifiedDNS", "status": "True"},
			map[string]any{"type": "Verified", "status": string(verified)},
		}},
	}}
}

func TestVerifiedDomains(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = resourcemanagerv1alpha1.AddToScheme(scheme)

	for _, tc := range []struct {
		name     string
		projects []client.Object
		domains  map[string][]unstructured.Unstructured
		failures map[string]error
		want     []string
		wantErr  bool
	}{
		{
			name:     "collects verified Domains from ready projects of the organization",
			projects: []client.Object{project("p1", "org", true), project("p2", "org", true)},
			domains: map[string][]unstructured.Unstructured{
				"p1": {domain("example.com", metav1.ConditionTrue), domain("pending.io", metav1.ConditionUnknown)},
				"p2": {domain("Other.ORG", metav1.ConditionTrue)},
			},
			want: []string{"example.com", "other.org"},
		},
		{
			name:     "ignores projects of other organizations and projects that aren't ready",
			projects: []client.Object{project("mine", "org", false), project("theirs", "other", true)},
			domains: map[string][]unstructured.Unstructured{
				"mine":   {domain("example.com", metav1.ConditionTrue)},
				"theirs": {domain("example.com", metav1.ConditionTrue)},
			},
		},
		{
			name:     "a project without the Domain API has none",
			projects: []client.Object{project("p1", "org", true)},
			failures: map[string]error{"p1": apierrors.NewNotFound(DomainGVR.GroupResource(), "")},
		},
		{
			name:     "an unreadable project doesn't hide another project's verified Domain",
			projects: []client.Object{project("broken", "org", true), project("ok", "org", true)},
			domains:  map[string][]unstructured.Unstructured{"ok": {domain("example.com", metav1.ConditionTrue)}},
			failures: map[string]error{"broken": errors.New("connection refused")},
			want:     []string{"example.com"},
		},
		{
			name:     "an unreadable project is an error when nothing is verified",
			projects: []client.Object{project("broken", "org", true)},
			failures: map[string]error{"broken": errors.New("connection refused")},
			wantErr:  true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := &Verifier{
				Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(tc.projects...).Build(),
				ListDomains: func(_ context.Context, project string) ([]unstructured.Unstructured, error) {
					if err := tc.failures[project]; err != nil {
						return nil, err
					}
					return tc.domains[project], nil
				},
			}
			got, err := v.VerifiedDomains(context.Background(), "org")
			if (err != nil) != tc.wantErr {
				t.Fatalf("VerifiedDomains error = %v, wantErr %v", err, tc.wantErr)
			}
			slices.Sort(got)
			if !slices.Equal(got, tc.want) {
				t.Errorf("VerifiedDomains = %v, want %v", got, tc.want)
			}
		})
	}
}
