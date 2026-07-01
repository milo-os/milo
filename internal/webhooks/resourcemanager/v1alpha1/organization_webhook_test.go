package v1alpha1

import (
	"context"
	"testing"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	"go.miloapis.com/milo/pkg/features"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	admissionv1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func withFeatureGate(t *testing.T, enabled bool) {
	t.Helper()
	if err := utilfeature.DefaultMutableFeatureGate.SetFromMap(map[string]bool{
		string(features.UnifiedOrganizations): enabled,
	}); err != nil {
		t.Fatalf("failed to set feature gate: %v", err)
	}
	t.Cleanup(func() {
		_ = utilfeature.DefaultMutableFeatureGate.SetFromMap(map[string]bool{
			string(features.UnifiedOrganizations): false,
		})
	})
}

func organizationAdmissionContext(t *testing.T, userInfo authenticationv1.UserInfo) context.Context {
	t.Helper()
	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			UserInfo: userInfo,
		},
	}
	ctx := admission.NewContextWithRequest(context.Background(), req)
	return ctx
}

func TestOrganizationMutator_Default(t *testing.T) {
	scheme := runtime.NewScheme()
	utilruntime.Must(resourcemanagerv1alpha1.AddToScheme(scheme))

	mutator := &OrganizationMutator{}

	t.Run("legacy mode leaves user-chosen name and type", func(t *testing.T) {
		withFeatureGate(t, false)
		org := &resourcemanagerv1alpha1.Organization{
			ObjectMeta: metav1.ObjectMeta{Name: "acme-corp"},
			Spec: resourcemanagerv1alpha1.OrganizationSpec{
				Type: resourcemanagerv1alpha1.OrganizationTypeStandard,
			},
		}
		ctx := organizationAdmissionContext(t, authenticationv1.UserInfo{
			Username: "user@example.com",
			Groups:   []string{"system:authenticated"},
		})

		if err := mutator.Default(ctx, org); err != nil {
			t.Fatalf("Default() error = %v", err)
		}
		if org.Name != "acme-corp" {
			t.Fatalf("name = %q, want acme-corp", org.Name)
		}
		if org.Spec.Type != resourcemanagerv1alpha1.OrganizationTypeStandard {
			t.Fatalf("type = %q, want Standard", org.Spec.Type)
		}
	})

	t.Run("unified mode strips type and defaults generateName", func(t *testing.T) {
		withFeatureGate(t, true)
		org := &resourcemanagerv1alpha1.Organization{
			Spec: resourcemanagerv1alpha1.OrganizationSpec{
				Type: resourcemanagerv1alpha1.OrganizationTypePersonal,
			},
		}
		ctx := organizationAdmissionContext(t, authenticationv1.UserInfo{
			Username: "user@example.com",
			UID:      "uid-123",
			Groups:   []string{"system:authenticated"},
		})

		if err := mutator.Default(ctx, org); err != nil {
			t.Fatalf("Default() error = %v", err)
		}
		if org.Spec.Type != "" {
			t.Fatalf("type = %q, want empty", org.Spec.Type)
		}
		if org.GenerateName != "org-" {
			t.Fatalf("generateName = %q, want org-", org.GenerateName)
		}
		if org.Annotations[resourcemanagerv1alpha1.OrganizationCreatorUserUIDAnnotation] != "uid-123" {
			t.Fatalf("creator annotation = %q, want uid-123", org.Annotations[resourcemanagerv1alpha1.OrganizationCreatorUserUIDAnnotation])
		}
	})
}

func TestOrganizationValidator_ValidateCreate(t *testing.T) {
	scheme := runtime.NewScheme()
	utilruntime.Must(resourcemanagerv1alpha1.AddToScheme(scheme))
	utilruntime.Must(iamv1alpha1.AddToScheme(scheme))

	user := &iamv1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "user-123",
			UID:  "uid-123",
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(user).Build()
	validator := &OrganizationValidator{client: fakeClient}

	t.Run("legacy mode accepts user-chosen name and type", func(t *testing.T) {
		withFeatureGate(t, false)
		org := &resourcemanagerv1alpha1.Organization{
			ObjectMeta: metav1.ObjectMeta{Name: "acme-corp"},
			Spec: resourcemanagerv1alpha1.OrganizationSpec{
				Type: resourcemanagerv1alpha1.OrganizationTypeStandard,
			},
		}
		ctx := organizationAdmissionContext(t, authenticationv1.UserInfo{
			UID:    "uid-123",
			Groups: []string{"system:authenticated"},
		})
		dryRun := true
		ctx = admission.NewContextWithRequest(ctx, admission.Request{
			AdmissionRequest: admissionv1.AdmissionRequest{
				UserInfo: authenticationv1.UserInfo{
					UID:    "uid-123",
					Groups: []string{"system:authenticated"},
				},
				DryRun: &dryRun,
			},
		})

		if _, err := validator.ValidateCreate(ctx, org); err != nil {
			t.Fatalf("ValidateCreate() error = %v", err)
		}
	})

	t.Run("legacy mode requires type", func(t *testing.T) {
		withFeatureGate(t, false)
		org := &resourcemanagerv1alpha1.Organization{
			ObjectMeta: metav1.ObjectMeta{Name: "acme-corp"},
		}
		ctx := organizationAdmissionContext(t, authenticationv1.UserInfo{
			Groups: []string{"system:authenticated"},
		})

		if _, err := validator.ValidateCreate(ctx, org); err == nil {
			t.Fatal("ValidateCreate() expected type validation error")
		}
	})

	t.Run("unified mode rejects user-chosen name", func(t *testing.T) {
		withFeatureGate(t, true)
		org := &resourcemanagerv1alpha1.Organization{
			ObjectMeta: metav1.ObjectMeta{Name: "acme-corp"},
		}
		ctx := organizationAdmissionContext(t, authenticationv1.UserInfo{
			Groups: []string{"system:authenticated"},
		})

		if _, err := validator.ValidateCreate(ctx, org); err == nil {
			t.Fatal("ValidateCreate() expected name validation error")
		}
	})

	t.Run("unified mode accepts generateName prefix", func(t *testing.T) {
		withFeatureGate(t, true)
		org := &resourcemanagerv1alpha1.Organization{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "org-"},
		}
		ctx := organizationAdmissionContext(t, authenticationv1.UserInfo{
			Groups: []string{"system:authenticated"},
		})
		dryRun := true
		ctx = admission.NewContextWithRequest(ctx, admission.Request{
			AdmissionRequest: admissionv1.AdmissionRequest{
				UserInfo: authenticationv1.UserInfo{
					Groups: []string{"system:authenticated"},
				},
				DryRun: &dryRun,
			},
		})

		if _, err := validator.ValidateCreate(ctx, org); err != nil {
			t.Fatalf("ValidateCreate() error = %v", err)
		}
	})
}
