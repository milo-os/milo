package resourcemanager

import (
	"context"
	"testing"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	resourcemanagerv1alpha "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestOrganizationController_reconcileOrganizationOwnerBootstrap(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	utilruntime.Must(resourcemanagerv1alpha.AddToScheme(scheme))
	utilruntime.Must(iamv1alpha1.AddToScheme(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))

	user := &iamv1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "uid-123",
			UID:  "uid-123",
		},
		Spec: iamv1alpha1.UserSpec{
			Email: "user@example.com",
		},
	}

	org := &resourcemanagerv1alpha.Organization{
		ObjectMeta: metav1.ObjectMeta{
			Name: "org-abc123",
			Annotations: map[string]string{
				resourcemanagerv1alpha.OrganizationCreatorUserUIDAnnotation: user.Name,
			},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(user, org).Build()
	controller := &OrganizationController{
		Client:             fakeClient,
		OwnerRoleName:      "resourcemanager.miloapis.com-organizationowner",
		OwnerRoleNamespace: "milo-system",
	}

	if err := controller.reconcileOrganizationOwnerBootstrap(context.Background(), org); err != nil {
		t.Fatalf("reconcileOrganizationOwnerBootstrap() error = %v", err)
	}

	var namespace corev1.Namespace
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: "organization-org-abc123"}, &namespace); err != nil {
		t.Fatalf("expected organization namespace to be created: %v", err)
	}

	var membership resourcemanagerv1alpha.OrganizationMembership
	if err := fakeClient.Get(context.Background(), types.NamespacedName{
		Name:      "member-uid-123",
		Namespace: "organization-org-abc123",
	}, &membership); err != nil {
		t.Fatalf("expected owner membership to be created: %v", err)
	}

	if len(membership.Spec.Roles) != 1 || membership.Spec.Roles[0].Name != controller.OwnerRoleName {
		t.Fatalf("membership roles = %#v, want owner role", membership.Spec.Roles)
	}

	updatedOrg := &resourcemanagerv1alpha.Organization{}
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: org.Name}, updatedOrg); err != nil {
		t.Fatalf("getting updated organization: %v", err)
	}
	if _, ok := updatedOrg.Annotations[resourcemanagerv1alpha.OrganizationCreatorUserUIDAnnotation]; ok {
		t.Fatal("creator annotation should be removed after bootstrap")
	}
}
