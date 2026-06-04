package v1alpha1

import (
	"context"
	"strings"
	"testing"
	"time"

	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// getWebhookTestScheme returns a runtime.Scheme for webhook testing
func getWebhookTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = iamv1alpha1.AddToScheme(scheme)
	_ = resourcemanagerv1alpha1.AddToScheme(scheme)
	return scheme
}

func TestOrganizationMembershipValidator_ValidateCreate_Success(t *testing.T) {
	ctx := context.TODO()
	scheme := getWebhookTestScheme()

	// Create test role
	role := &iamv1alpha1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "viewer-role",
			Namespace: "organization-test",
		},
		Spec: iamv1alpha1.RoleSpec{
			LaunchStage: "Stable",
			IncludedPermissions: []string{
				"resourcemanager.miloapis.com/organizations.get",
			},
		},
	}

	// Create membership with valid role
	membership := &resourcemanagerv1alpha1.OrganizationMembership{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-membership",
			Namespace: "organization-test",
		},
		Spec: resourcemanagerv1alpha1.OrganizationMembershipSpec{
			OrganizationRef: resourcemanagerv1alpha1.OrganizationReference{
				Name: "test",
			},
			UserRef: resourcemanagerv1alpha1.MemberReference{
				Name: "test-user",
			},
			Roles: []resourcemanagerv1alpha1.RoleReference{
				{
					Name:      "viewer-role",
					Namespace: "organization-test",
				},
			},
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(role).
		Build()

	validator := &OrganizationMembershipValidator{
		client:             c,
		apiReader:          c,
		ownerRoleName:      "resourcemanager.miloapis.com-organizationowner",
		ownerRoleNamespace: "milo-system",
	}

	// Test validation
	warnings, err := validator.ValidateCreate(ctx, membership)
	if err != nil {
		t.Fatalf("ValidateCreate failed: %v", err)
	}
	if len(warnings) > 0 {
		t.Errorf("Expected no warnings, got %d", len(warnings))
	}
}

func TestOrganizationMembershipValidator_ValidateCreate_DuplicateRoles(t *testing.T) {
	ctx := context.TODO()
	scheme := getWebhookTestScheme()

	// Create membership with duplicate roles
	membership := &resourcemanagerv1alpha1.OrganizationMembership{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-membership",
			Namespace: "organization-test",
		},
		Spec: resourcemanagerv1alpha1.OrganizationMembershipSpec{
			OrganizationRef: resourcemanagerv1alpha1.OrganizationReference{
				Name: "test",
			},
			UserRef: resourcemanagerv1alpha1.MemberReference{
				Name: "test-user",
			},
			Roles: []resourcemanagerv1alpha1.RoleReference{
				{
					Name:      "viewer-role",
					Namespace: "organization-test",
				},
				{
					Name:      "viewer-role",
					Namespace: "organization-test",
				},
			},
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	validator := &OrganizationMembershipValidator{
		client:             c,
		apiReader:          c,
		ownerRoleName:      "resourcemanager.miloapis.com-organizationowner",
		ownerRoleNamespace: "milo-system",
	}

	// Test validation - should fail with duplicate roles
	_, err := validator.ValidateCreate(ctx, membership)
	if err == nil {
		t.Fatal("Expected validation to fail with duplicate roles, but it passed")
	}
	if err.Error() != "duplicate role reference detected: viewer-role in namespace organization-test" {
		t.Errorf("Expected duplicate role error, got: %v", err)
	}
}

func TestOrganizationMembershipValidator_ValidateCreate_NonexistentRole(t *testing.T) {
	ctx := context.TODO()
	scheme := getWebhookTestScheme()

	// Create membership with nonexistent role
	membership := &resourcemanagerv1alpha1.OrganizationMembership{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-membership",
			Namespace: "organization-test",
		},
		Spec: resourcemanagerv1alpha1.OrganizationMembershipSpec{
			OrganizationRef: resourcemanagerv1alpha1.OrganizationReference{
				Name: "test",
			},
			UserRef: resourcemanagerv1alpha1.MemberReference{
				Name: "test-user",
			},
			Roles: []resourcemanagerv1alpha1.RoleReference{
				{
					Name:      "nonexistent-role",
					Namespace: "organization-test",
				},
			},
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	validator := &OrganizationMembershipValidator{
		client:             c,
		apiReader:          c,
		ownerRoleName:      "resourcemanager.miloapis.com-organizationowner",
		ownerRoleNamespace: "milo-system",
	}

	// Test validation - should fail with nonexistent role
	_, err := validator.ValidateCreate(ctx, membership)
	if err == nil {
		t.Fatal("Expected validation to fail with nonexistent role, but it passed")
	}
	if err.Error() != "role 'nonexistent-role' not found in namespace 'organization-test'" {
		t.Errorf("Expected nonexistent role error, got: %v", err)
	}
}

func TestOrganizationMembershipValidator_ValidateCreate_EmptyRoleName(t *testing.T) {
	ctx := context.TODO()
	scheme := getWebhookTestScheme()

	// Create membership with empty role name
	membership := &resourcemanagerv1alpha1.OrganizationMembership{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-membership",
			Namespace: "organization-test",
		},
		Spec: resourcemanagerv1alpha1.OrganizationMembershipSpec{
			OrganizationRef: resourcemanagerv1alpha1.OrganizationReference{
				Name: "test",
			},
			UserRef: resourcemanagerv1alpha1.MemberReference{
				Name: "test-user",
			},
			Roles: []resourcemanagerv1alpha1.RoleReference{
				{
					Name:      "",
					Namespace: "organization-test",
				},
			},
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	validator := &OrganizationMembershipValidator{
		client:             c,
		apiReader:          c,
		ownerRoleName:      "resourcemanager.miloapis.com-organizationowner",
		ownerRoleNamespace: "milo-system",
	}

	// Test validation - should fail with empty role name
	_, err := validator.ValidateCreate(ctx, membership)
	if err == nil {
		t.Fatal("Expected validation to fail with empty role name, but it passed")
	}
	if err.Error() != "role name cannot be empty" {
		t.Errorf("Expected empty role name error, got: %v", err)
	}
}

func TestOrganizationMembershipValidator_ValidateDelete_AllowsNonOwner(t *testing.T) {
	ctx := context.TODO()
	scheme := getWebhookTestScheme()

	membership := &resourcemanagerv1alpha1.OrganizationMembership{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "member-alice",
			Namespace: "organization-test",
		},
		Spec: resourcemanagerv1alpha1.OrganizationMembershipSpec{
			OrganizationRef: resourcemanagerv1alpha1.OrganizationReference{
				Name: "test-org",
			},
			UserRef: resourcemanagerv1alpha1.MemberReference{
				Name: "alice",
			},
			Roles: []resourcemanagerv1alpha1.RoleReference{
				{
					Name:      "viewer-role",
					Namespace: "organization-test",
				},
			},
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(membership).
		Build()

	validator := &OrganizationMembershipValidator{
		client:             c,
		apiReader:          c,
		ownerRoleName:      "resourcemanager.miloapis.com-organizationowner",
		ownerRoleNamespace: "milo-system",
	}

	if _, err := validator.ValidateDelete(ctx, membership); err != nil {
		t.Fatalf("expected non-owner delete to pass, got error: %v", err)
	}
}

func TestOrganizationMembershipValidator_ValidateDelete_AllowsWhenAnotherOwnerExists(t *testing.T) {
	ctx := context.TODO()
	scheme := getWebhookTestScheme()

	target := &resourcemanagerv1alpha1.OrganizationMembership{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "member-alice",
			Namespace: "organization-test",
		},
		Spec: resourcemanagerv1alpha1.OrganizationMembershipSpec{
			OrganizationRef: resourcemanagerv1alpha1.OrganizationReference{
				Name: "test-org",
			},
			UserRef: resourcemanagerv1alpha1.MemberReference{
				Name: "alice",
			},
			Roles: []resourcemanagerv1alpha1.RoleReference{
				{
					Name:      "resourcemanager.miloapis.com-organizationowner",
					Namespace: "milo-system",
				},
			},
		},
	}

	otherOwner := &resourcemanagerv1alpha1.OrganizationMembership{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "member-bob",
			Namespace: "organization-test",
		},
		Spec: resourcemanagerv1alpha1.OrganizationMembershipSpec{
			OrganizationRef: resourcemanagerv1alpha1.OrganizationReference{
				Name: "test-org",
			},
			UserRef: resourcemanagerv1alpha1.MemberReference{
				Name: "bob",
			},
			Roles: []resourcemanagerv1alpha1.RoleReference{
				{
					Name:      "resourcemanager.miloapis.com-organizationowner",
					Namespace: "milo-system",
				},
			},
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(target, otherOwner).
		Build()

	validator := &OrganizationMembershipValidator{
		client:             c,
		apiReader:          c,
		ownerRoleName:      "resourcemanager.miloapis.com-organizationowner",
		ownerRoleNamespace: "milo-system",
	}

	if _, err := validator.ValidateDelete(ctx, target); err != nil {
		t.Fatalf("expected delete to succeed when another owner exists, got error: %v", err)
	}
}

func TestOrganizationMembershipValidator_ValidateDelete_BlocksLastOwner(t *testing.T) {
	ctx := context.TODO()
	scheme := getWebhookTestScheme()

	target := &resourcemanagerv1alpha1.OrganizationMembership{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "member-alice",
			Namespace: "organization-test",
		},
		Spec: resourcemanagerv1alpha1.OrganizationMembershipSpec{
			OrganizationRef: resourcemanagerv1alpha1.OrganizationReference{
				Name: "test-org",
			},
			UserRef: resourcemanagerv1alpha1.MemberReference{
				Name: "alice",
			},
			Roles: []resourcemanagerv1alpha1.RoleReference{
				{
					Name:      "resourcemanager.miloapis.com-organizationowner",
					Namespace: "milo-system",
				},
			},
		},
	}

	nonOwner := &resourcemanagerv1alpha1.OrganizationMembership{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "member-bob",
			Namespace: "organization-test",
		},
		Spec: resourcemanagerv1alpha1.OrganizationMembershipSpec{
			OrganizationRef: resourcemanagerv1alpha1.OrganizationReference{
				Name: "test-org",
			},
			UserRef: resourcemanagerv1alpha1.MemberReference{
				Name: "bob",
			},
			Roles: []resourcemanagerv1alpha1.RoleReference{
				{
					Name:      "organization-viewer",
					Namespace: "organization-test",
				},
			},
		},
	}

	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "organization-test",
			Finalizers: []string{"kubernetes"},
		},
	}

	organization := &resourcemanagerv1alpha1.Organization{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-org",
		},
	}

	// The user must exist without a DeletionTimestamp so the webhook does not
	// bypass the last-owner guard.
	alice := &iamv1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "alice",
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(target, nonOwner, namespace, organization, alice).
		Build()

	validator := &OrganizationMembershipValidator{
		client:             c,
		apiReader:          c,
		ownerRoleName:      "resourcemanager.miloapis.com-organizationowner",
		ownerRoleNamespace: "milo-system",
	}

	_, err := validator.ValidateDelete(ctx, target)
	if err == nil {
		t.Fatal("expected last owner deletion to be blocked, but it succeeded")
	}
	if !apierrors.IsForbidden(err) {
		t.Fatalf("expected forbidden error, got: %v", err)
	}
	expectedSnippet := "must have at least one owner"
	if !containsErrorMessage(err, expectedSnippet) {
		t.Fatalf("expected error message to contain %q, got: %v", expectedSnippet, err)
	}
}

func TestOrganizationMembershipValidator_ValidateDelete_AllowsWhenNamespaceTerminating(t *testing.T) {
	ctx := context.TODO()
	scheme := getWebhookTestScheme()

	target := &resourcemanagerv1alpha1.OrganizationMembership{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "member-alice",
			Namespace: "organization-test",
		},
		Spec: resourcemanagerv1alpha1.OrganizationMembershipSpec{
			OrganizationRef: resourcemanagerv1alpha1.OrganizationReference{
				Name: "test-org",
			},
			UserRef: resourcemanagerv1alpha1.MemberReference{
				Name: "alice",
			},
			Roles: []resourcemanagerv1alpha1.RoleReference{
				{
					Name:      "resourcemanager.miloapis.com-organizationowner",
					Namespace: "milo-system",
				},
			},
		},
	}

	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "organization-test",
			DeletionTimestamp: &metav1.Time{Time: time.Now().Add(-time.Minute)},
			Finalizers:        []string{"kubernetes"},
		},
	}

	organization := &resourcemanagerv1alpha1.Organization{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-org",
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(target, namespace, organization).
		Build()

	validator := &OrganizationMembershipValidator{
		client:             c,
		apiReader:          c,
		ownerRoleName:      "resourcemanager.miloapis.com-organizationowner",
		ownerRoleNamespace: "milo-system",
	}

	if _, err := validator.ValidateDelete(ctx, target); err != nil {
		t.Fatalf("expected deletion to succeed when namespace is terminating, got error: %v", err)
	}
}

func TestOrganizationMembershipValidator_ValidateDelete_AllowsWhenOrganizationDeleting(t *testing.T) {
	ctx := context.TODO()
	scheme := getWebhookTestScheme()

	target := &resourcemanagerv1alpha1.OrganizationMembership{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "member-alice",
			Namespace: "organization-test",
		},
		Spec: resourcemanagerv1alpha1.OrganizationMembershipSpec{
			OrganizationRef: resourcemanagerv1alpha1.OrganizationReference{
				Name: "test-org",
			},
			UserRef: resourcemanagerv1alpha1.MemberReference{
				Name: "alice",
			},
			Roles: []resourcemanagerv1alpha1.RoleReference{
				{
					Name:      "resourcemanager.miloapis.com-organizationowner",
					Namespace: "milo-system",
				},
			},
		},
	}

	organization := &resourcemanagerv1alpha1.Organization{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-org",
			DeletionTimestamp: &metav1.Time{Time: time.Now().Add(-time.Minute)},
			Finalizers:        []string{"resourcemanager.miloapis.com/finalizer"},
		},
	}

	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "organization-test",
			Finalizers: []string{"kubernetes"},
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(target, organization, namespace).
		Build()

	validator := &OrganizationMembershipValidator{
		client:             c,
		apiReader:          c,
		ownerRoleName:      "resourcemanager.miloapis.com-organizationowner",
		ownerRoleNamespace: "milo-system",
	}

	if _, err := validator.ValidateDelete(ctx, target); err != nil {
		t.Fatalf("expected deletion to succeed when organization is deleting, got error: %v", err)
	}
}

func TestOrganizationMembershipValidator_ValidateDelete_AllowsWhenOrganizationMissing(t *testing.T) {
	ctx := context.TODO()
	scheme := getWebhookTestScheme()

	target := &resourcemanagerv1alpha1.OrganizationMembership{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "member-alice",
			Namespace: "organization-test",
		},
		Spec: resourcemanagerv1alpha1.OrganizationMembershipSpec{
			OrganizationRef: resourcemanagerv1alpha1.OrganizationReference{
				Name: "missing-org",
			},
			UserRef: resourcemanagerv1alpha1.MemberReference{
				Name: "alice",
			},
			Roles: []resourcemanagerv1alpha1.RoleReference{
				{
					Name:      "resourcemanager.miloapis.com-organizationowner",
					Namespace: "milo-system",
				},
			},
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(target, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "organization-test",
				Finalizers: []string{"kubernetes"},
			},
		}).
		Build()

	validator := &OrganizationMembershipValidator{
		client:             c,
		apiReader:          c,
		ownerRoleName:      "resourcemanager.miloapis.com-organizationowner",
		ownerRoleNamespace: "milo-system",
	}

	if _, err := validator.ValidateDelete(ctx, target); err != nil {
		t.Fatalf("expected deletion to succeed when organization is missing, got error: %v", err)
	}
}

func TestOrganizationMembershipValidator_ValidateDelete_AllowsWhenUserIsBeingDeleted(t *testing.T) {
	ctx := context.TODO()
	scheme := getWebhookTestScheme()

	now := metav1.Now()
	user := &iamv1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "alice",
			DeletionTimestamp: &now,
			Finalizers:        []string{"some-finalizer"},
		},
	}

	target := &resourcemanagerv1alpha1.OrganizationMembership{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "member-alice",
			Namespace: "organization-test",
		},
		Spec: resourcemanagerv1alpha1.OrganizationMembershipSpec{
			OrganizationRef: resourcemanagerv1alpha1.OrganizationReference{
				Name: "test-org",
			},
			UserRef: resourcemanagerv1alpha1.MemberReference{
				Name: "alice",
			},
			Roles: []resourcemanagerv1alpha1.RoleReference{
				{
					Name:      "resourcemanager.miloapis.com-organizationowner",
					Namespace: "milo-system",
				},
			},
		},
	}

	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "organization-test"}}
	org := &resourcemanagerv1alpha1.Organization{ObjectMeta: metav1.ObjectMeta{Name: "test-org"}}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(target, user, namespace, org).
		Build()

	validator := &OrganizationMembershipValidator{
		client:             c,
		apiReader:          c,
		ownerRoleName:      "resourcemanager.miloapis.com-organizationowner",
		ownerRoleNamespace: "milo-system",
	}

	if _, err := validator.ValidateDelete(ctx, target); err != nil {
		t.Fatalf("expected deletion to succeed when user has a DeletionTimestamp, got error: %v", err)
	}
}

func TestOrganizationMembershipValidator_ValidateDelete_AllowsWhenUserAlreadyGone(t *testing.T) {
	ctx := context.TODO()
	scheme := getWebhookTestScheme()

	target := &resourcemanagerv1alpha1.OrganizationMembership{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "member-alice",
			Namespace: "organization-test",
		},
		Spec: resourcemanagerv1alpha1.OrganizationMembershipSpec{
			OrganizationRef: resourcemanagerv1alpha1.OrganizationReference{
				Name: "test-org",
			},
			UserRef: resourcemanagerv1alpha1.MemberReference{
				Name: "alice",
			},
			Roles: []resourcemanagerv1alpha1.RoleReference{
				{
					Name:      "resourcemanager.miloapis.com-organizationowner",
					Namespace: "milo-system",
				},
			},
		},
	}

	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "organization-test"}}
	org := &resourcemanagerv1alpha1.Organization{ObjectMeta: metav1.ObjectMeta{Name: "test-org"}}

	// User is not added to the client — simulating a user that was already deleted.
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(target, namespace, org).
		Build()

	validator := &OrganizationMembershipValidator{
		client:             c,
		apiReader:          c,
		ownerRoleName:      "resourcemanager.miloapis.com-organizationowner",
		ownerRoleNamespace: "milo-system",
	}

	if _, err := validator.ValidateDelete(ctx, target); err != nil {
		t.Fatalf("expected deletion to succeed when user no longer exists, got error: %v", err)
	}
}

func TestOrganizationMembershipValidator_ValidateUpdate_BlocksRemovingOwnerRoleForLastOwner(t *testing.T) {
	ctx := context.TODO()
	scheme := getWebhookTestScheme()

	oldMembership := &resourcemanagerv1alpha1.OrganizationMembership{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "member-alice",
			Namespace: "organization-test",
		},
		Spec: resourcemanagerv1alpha1.OrganizationMembershipSpec{
			OrganizationRef: resourcemanagerv1alpha1.OrganizationReference{
				Name: "test-org",
			},
			UserRef: resourcemanagerv1alpha1.MemberReference{
				Name: "alice",
			},
			Roles: []resourcemanagerv1alpha1.RoleReference{
				{
					Name:      "resourcemanager.miloapis.com-organizationowner",
					Namespace: "milo-system",
				},
			},
		},
	}

	newMembership := oldMembership.DeepCopy()
	newMembership.Spec.Roles = []resourcemanagerv1alpha1.RoleReference{}

	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "organization-test"}}
	organization := &resourcemanagerv1alpha1.Organization{ObjectMeta: metav1.ObjectMeta{Name: "test-org"}}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(oldMembership, namespace, organization).
		Build()

	validator := &OrganizationMembershipValidator{
		client:             c,
		apiReader:          c,
		ownerRoleName:      "resourcemanager.miloapis.com-organizationowner",
		ownerRoleNamespace: "milo-system",
	}

	if _, err := validator.ValidateUpdate(ctx, oldMembership, newMembership); err == nil {
		t.Fatal("expected update removing last owner role to be blocked, but it succeeded")
	} else if !apierrors.IsForbidden(err) {
		t.Fatalf("expected forbidden error, got: %v", err)
	} else if !containsErrorMessage(err, "must have at least one owner") {
		t.Fatalf("expected error message to mention owner requirement, got: %v", err)
	}
}

func TestOrganizationMembershipValidator_ValidateUpdate_AllowsRemovingOwnerRoleWhenAnotherOwnerExists(t *testing.T) {
	ctx := context.TODO()
	scheme := getWebhookTestScheme()

	oldMembership := &resourcemanagerv1alpha1.OrganizationMembership{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "member-alice",
			Namespace: "organization-test",
		},
		Spec: resourcemanagerv1alpha1.OrganizationMembershipSpec{
			OrganizationRef: resourcemanagerv1alpha1.OrganizationReference{
				Name: "test-org",
			},
			UserRef: resourcemanagerv1alpha1.MemberReference{
				Name: "alice",
			},
			Roles: []resourcemanagerv1alpha1.RoleReference{
				{
					Name:      "resourcemanager.miloapis.com-organizationowner",
					Namespace: "milo-system",
				},
			},
		},
	}

	newMembership := oldMembership.DeepCopy()
	newMembership.Spec.Roles = []resourcemanagerv1alpha1.RoleReference{}

	otherOwner := &resourcemanagerv1alpha1.OrganizationMembership{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "member-bob",
			Namespace: "organization-test",
		},
		Spec: resourcemanagerv1alpha1.OrganizationMembershipSpec{
			OrganizationRef: resourcemanagerv1alpha1.OrganizationReference{
				Name: "test-org",
			},
			UserRef: resourcemanagerv1alpha1.MemberReference{
				Name: "bob",
			},
			Roles: []resourcemanagerv1alpha1.RoleReference{
				{
					Name:      "resourcemanager.miloapis.com-organizationowner",
					Namespace: "milo-system",
				},
			},
		},
	}

	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "organization-test"}}
	organization := &resourcemanagerv1alpha1.Organization{ObjectMeta: metav1.ObjectMeta{Name: "test-org"}}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(oldMembership, otherOwner, namespace, organization).
		Build()

	validator := &OrganizationMembershipValidator{
		client:             c,
		apiReader:          c,
		ownerRoleName:      "resourcemanager.miloapis.com-organizationowner",
		ownerRoleNamespace: "milo-system",
	}

	if _, err := validator.ValidateUpdate(ctx, oldMembership, newMembership); err != nil {
		t.Fatalf("expected update to succeed when another owner exists, got error: %v", err)
	}
}

func TestOrganizationMembershipValidator_ValidateUpdate_AllowsRemovingOwnerRoleDuringNamespaceTeardown(t *testing.T) {
	ctx := context.TODO()
	scheme := getWebhookTestScheme()

	oldMembership := &resourcemanagerv1alpha1.OrganizationMembership{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "member-alice",
			Namespace: "organization-test",
		},
		Spec: resourcemanagerv1alpha1.OrganizationMembershipSpec{
			OrganizationRef: resourcemanagerv1alpha1.OrganizationReference{
				Name: "test-org",
			},
			UserRef: resourcemanagerv1alpha1.MemberReference{
				Name: "alice",
			},
			Roles: []resourcemanagerv1alpha1.RoleReference{
				{
					Name:      "resourcemanager.miloapis.com-organizationowner",
					Namespace: "milo-system",
				},
			},
		},
	}

	newMembership := oldMembership.DeepCopy()
	newMembership.Spec.Roles = []resourcemanagerv1alpha1.RoleReference{}

	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "organization-test",
			DeletionTimestamp: &metav1.Time{Time: time.Now().Add(-time.Minute)},
			Finalizers:        []string{"kubernetes"},
		},
	}
	organization := &resourcemanagerv1alpha1.Organization{ObjectMeta: metav1.ObjectMeta{Name: "test-org"}}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(oldMembership, namespace, organization).
		Build()

	validator := &OrganizationMembershipValidator{
		client:             c,
		apiReader:          c,
		ownerRoleName:      "resourcemanager.miloapis.com-organizationowner",
		ownerRoleNamespace: "milo-system",
	}

	if _, err := validator.ValidateUpdate(ctx, oldMembership, newMembership); err != nil {
		t.Fatalf("expected update to succeed when namespace is terminating, got error: %v", err)
	}
}

func containsErrorMessage(err error, needle string) bool {
	return err != nil && strings.Contains(err.Error(), needle)
}

func TestOrganizationMembershipValidator_ValidateCreate_MultipleRoles(t *testing.T) {
	ctx := context.TODO()
	scheme := getWebhookTestScheme()

	// Create test roles
	viewerRole := &iamv1alpha1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "viewer-role",
			Namespace: "organization-test",
		},
		Spec: iamv1alpha1.RoleSpec{
			LaunchStage: "Stable",
		},
	}

	editorRole := &iamv1alpha1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "editor-role",
			Namespace: "organization-test",
		},
		Spec: iamv1alpha1.RoleSpec{
			LaunchStage: "Stable",
		},
	}

	// Create membership with multiple valid roles
	membership := &resourcemanagerv1alpha1.OrganizationMembership{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-membership",
			Namespace: "organization-test",
		},
		Spec: resourcemanagerv1alpha1.OrganizationMembershipSpec{
			OrganizationRef: resourcemanagerv1alpha1.OrganizationReference{
				Name: "test",
			},
			UserRef: resourcemanagerv1alpha1.MemberReference{
				Name: "test-user",
			},
			Roles: []resourcemanagerv1alpha1.RoleReference{
				{
					Name:      "viewer-role",
					Namespace: "organization-test",
				},
				{
					Name:      "editor-role",
					Namespace: "organization-test",
				},
			},
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(viewerRole, editorRole).
		Build()

	validator := &OrganizationMembershipValidator{
		client: c,
	}

	// Test validation - should pass with multiple valid roles
	warnings, err := validator.ValidateCreate(ctx, membership)
	if err != nil {
		t.Fatalf("ValidateCreate failed with multiple roles: %v", err)
	}
	if len(warnings) > 0 {
		t.Errorf("Expected no warnings, got %d", len(warnings))
	}
}

func TestOrganizationMembershipValidator_ValidateCreate_CrossNamespaceRole(t *testing.T) {
	ctx := context.TODO()
	scheme := getWebhookTestScheme()

	// Create role in different namespace
	role := &iamv1alpha1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "shared-role",
			Namespace: "milo-system",
		},
		Spec: iamv1alpha1.RoleSpec{
			LaunchStage: "Stable",
		},
	}

	// Create membership referencing cross-namespace role
	membership := &resourcemanagerv1alpha1.OrganizationMembership{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-membership",
			Namespace: "organization-test",
		},
		Spec: resourcemanagerv1alpha1.OrganizationMembershipSpec{
			OrganizationRef: resourcemanagerv1alpha1.OrganizationReference{
				Name: "test",
			},
			UserRef: resourcemanagerv1alpha1.MemberReference{
				Name: "test-user",
			},
			Roles: []resourcemanagerv1alpha1.RoleReference{
				{
					Name:      "shared-role",
					Namespace: "milo-system",
				},
			},
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(role).
		Build()

	validator := &OrganizationMembershipValidator{
		client: c,
	}

	// Test validation - should pass with cross-namespace role
	warnings, err := validator.ValidateCreate(ctx, membership)
	if err != nil {
		t.Fatalf("ValidateCreate failed with cross-namespace role: %v", err)
	}
	if len(warnings) > 0 {
		t.Errorf("Expected no warnings, got %d", len(warnings))
	}
}

func TestOrganizationMembershipValidator_ValidateUpdate(t *testing.T) {
	ctx := context.TODO()
	scheme := getWebhookTestScheme()

	// Create test role
	role := &iamv1alpha1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "viewer-role",
			Namespace: "organization-test",
		},
		Spec: iamv1alpha1.RoleSpec{
			LaunchStage: "Stable",
		},
	}

	oldMembership := &resourcemanagerv1alpha1.OrganizationMembership{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-membership",
			Namespace: "organization-test",
		},
		Spec: resourcemanagerv1alpha1.OrganizationMembershipSpec{
			OrganizationRef: resourcemanagerv1alpha1.OrganizationReference{
				Name: "test",
			},
			UserRef: resourcemanagerv1alpha1.MemberReference{
				Name: "test-user",
			},
		},
	}

	newMembership := &resourcemanagerv1alpha1.OrganizationMembership{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-membership",
			Namespace: "organization-test",
		},
		Spec: resourcemanagerv1alpha1.OrganizationMembershipSpec{
			OrganizationRef: resourcemanagerv1alpha1.OrganizationReference{
				Name: "test",
			},
			UserRef: resourcemanagerv1alpha1.MemberReference{
				Name: "test-user",
			},
			Roles: []resourcemanagerv1alpha1.RoleReference{
				{
					Name:      "viewer-role",
					Namespace: "organization-test",
				},
			},
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(role).
		Build()

	validator := &OrganizationMembershipValidator{
		client: c,
	}

	// Test update validation
	warnings, err := validator.ValidateUpdate(ctx, oldMembership, newMembership)
	if err != nil {
		t.Fatalf("ValidateUpdate failed: %v", err)
	}
	if len(warnings) > 0 {
		t.Errorf("Expected no warnings, got %d", len(warnings))
	}
}

func TestOrganizationMembershipValidator_CheckDuplicateRoles(t *testing.T) {
	validator := &OrganizationMembershipValidator{}

	tests := []struct {
		name        string
		roles       []resourcemanagerv1alpha1.RoleReference
		namespace   string
		expectError bool
	}{
		{
			name: "no duplicates",
			roles: []resourcemanagerv1alpha1.RoleReference{
				{Name: "role1", Namespace: "ns1"},
				{Name: "role2", Namespace: "ns1"},
			},
			namespace:   "default",
			expectError: false,
		},
		{
			name: "duplicate with same namespace",
			roles: []resourcemanagerv1alpha1.RoleReference{
				{Name: "role1", Namespace: "ns1"},
				{Name: "role1", Namespace: "ns1"},
			},
			namespace:   "default",
			expectError: true,
		},
		{
			name: "same name different namespace",
			roles: []resourcemanagerv1alpha1.RoleReference{
				{Name: "role1", Namespace: "ns1"},
				{Name: "role1", Namespace: "ns2"},
			},
			namespace:   "default",
			expectError: false,
		},
		{
			name: "duplicate with empty namespace",
			roles: []resourcemanagerv1alpha1.RoleReference{
				{Name: "role1"},
				{Name: "role1"},
			},
			namespace:   "default",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			membership := &resourcemanagerv1alpha1.OrganizationMembership{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: tt.namespace,
				},
				Spec: resourcemanagerv1alpha1.OrganizationMembershipSpec{
					Roles: tt.roles,
				},
			}

			err := validator.checkDuplicateRoles(membership)
			if tt.expectError && err == nil {
				t.Error("Expected error for duplicate roles, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error, got: %v", err)
			}
		})
	}
}
