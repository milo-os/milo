package resourcemanager

import (
	"context"
	"testing"

	billingv1alpha1 "go.miloapis.com/billing/api/v1alpha1"
	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// getTestScheme returns a runtime.Scheme with all Milo APIs registered.
func getTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = iamv1alpha1.AddToScheme(scheme)
	_ = resourcemanagerv1alpha1.AddToScheme(scheme)
	_ = billingv1alpha1.AddToScheme(scheme)
	return scheme
}

// TestOrganizationMembershipController_ReconcileRoles tests the role reconciliation logic
func TestOrganizationMembershipController_ReconcileRoles(t *testing.T) {
	ctx := context.TODO()
	scheme := getTestScheme()

	// Setup test data
	organization := &resourcemanagerv1alpha1.Organization{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-org",
			UID:  types.UID("org-uid-123"),
		},
		Spec: resourcemanagerv1alpha1.OrganizationSpec{
			ContactInfo: &resourcemanagerv1alpha1.OrganizationContactInfo{
				Email: "owner@example.com",
				Name:  "Owner",
			},
		},
	}

	user := &iamv1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-user",
			UID:  types.UID("user-uid-456"),
		},
		Spec: iamv1alpha1.UserSpec{
			Email:      "test@example.com",
			GivenName:  "Test",
			FamilyName: "User",
		},
	}

	role := &iamv1alpha1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "org-viewer",
			Namespace: "organization-test-org",
		},
		Spec: iamv1alpha1.RoleSpec{
			LaunchStage: "Stable",
			IncludedPermissions: []string{
				"resourcemanager.miloapis.com/organizations.get",
			},
		},
	}

	membership := &resourcemanagerv1alpha1.OrganizationMembership{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-membership",
			Namespace: "organization-test-org",
			UID:       types.UID("membership-uid-789"),
		},
		Spec: resourcemanagerv1alpha1.OrganizationMembershipSpec{
			OrganizationRef: resourcemanagerv1alpha1.OrganizationReference{
				Name: "test-org",
			},
			UserRef: resourcemanagerv1alpha1.MemberReference{
				Name: "test-user",
			},
			Roles: []resourcemanagerv1alpha1.RoleReference{
				{
					Name:      "org-viewer",
					Namespace: "organization-test-org",
				},
			},
		},
	}

	// Create fake client with test objects
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(organization, user, role, membership).
		WithStatusSubresource(membership, &iamv1alpha1.PolicyBinding{}).
		Build()

	controller := &OrganizationMembershipController{
		Client: c,
	}

	// Test: Reconcile roles for the first time
	t.Run("Create PolicyBinding for new role", func(t *testing.T) {
		err := controller.reconcileRoles(ctx, membership, organization, user)
		if err != nil {
			t.Fatalf("reconcileRoles failed: %v", err)
		}

		// Verify RolesApplied condition is False (pending)
		rolesAppliedCondition := apimeta.FindStatusCondition(membership.Status.Conditions, RolesApplied)
		if rolesAppliedCondition == nil {
			t.Fatal("RolesApplied condition not found")
		}
		if rolesAppliedCondition.Status != metav1.ConditionFalse {
			t.Errorf("Expected RolesApplied condition status to be False (pending), got %s: %s",
				rolesAppliedCondition.Status, rolesAppliedCondition.Message)
		}

		// Verify AppliedRoles status shows Pending
		if len(membership.Status.AppliedRoles) != 1 {
			t.Fatalf("Expected 1 applied role, got %d", len(membership.Status.AppliedRoles))
		}

		appliedRole := membership.Status.AppliedRoles[0]
		if appliedRole.Name != "org-viewer" {
			t.Errorf("Expected role name 'org-viewer', got '%s'", appliedRole.Name)
		}
		if appliedRole.Status != "Pending" {
			t.Errorf("Expected role status 'Pending', got '%s'", appliedRole.Status)
		}
		if appliedRole.PolicyBindingRef == nil {
			t.Error("Expected PolicyBindingRef to be set")
		}

		// Verify PolicyBinding was created
		var bindings iamv1alpha1.PolicyBindingList
		err = c.List(ctx, &bindings)
		if err != nil {
			t.Fatalf("Failed to list PolicyBindings: %v", err)
		}
		if len(bindings.Items) != 1 {
			t.Fatalf("Expected 1 PolicyBinding, got %d", len(bindings.Items))
		}

		binding := bindings.Items[0]
		if binding.Spec.RoleRef.Name != "org-viewer" {
			t.Errorf("Expected PolicyBinding role 'org-viewer', got '%s'", binding.Spec.RoleRef.Name)
		}
		if len(binding.Spec.Subjects) != 1 || binding.Spec.Subjects[0].Name != "test-user" {
			t.Errorf("Expected PolicyBinding subject 'test-user', got %v", binding.Spec.Subjects)
		}

		// Now simulate PolicyBinding becoming Ready
		binding.Status.Conditions = []metav1.Condition{
			{
				Type:   "Ready",
				Status: metav1.ConditionTrue,
				Reason: "Ready",
			},
		}
		err = c.Status().Update(ctx, &binding)
		if err != nil {
			t.Fatalf("Failed to update PolicyBinding status: %v", err)
		}

		// Reconcile again - now should be Applied
		err = controller.reconcileRoles(ctx, membership, organization, user)
		if err != nil {
			t.Fatalf("Second reconcileRoles failed: %v", err)
		}

		// Verify RolesApplied condition is now True
		rolesAppliedCondition = apimeta.FindStatusCondition(membership.Status.Conditions, RolesApplied)
		if rolesAppliedCondition.Status != metav1.ConditionTrue {
			t.Errorf("Expected RolesApplied condition status to be True after Ready, got %s: %s",
				rolesAppliedCondition.Status, rolesAppliedCondition.Message)
		}

		// Verify role status is now Applied
		appliedRole = membership.Status.AppliedRoles[0]
		if appliedRole.Status != "Applied" {
			t.Errorf("Expected role status 'Applied' after Ready, got '%s'", appliedRole.Status)
		}
	})
}

// TestOrganizationMembershipController_ReconcileRoles_NonexistentRole tests handling of nonexistent roles
func TestOrganizationMembershipController_ReconcileRoles_NonexistentRole(t *testing.T) {
	ctx := context.TODO()
	scheme := getTestScheme()

	// Setup test data without the role
	organization := &resourcemanagerv1alpha1.Organization{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-org",
			UID:  types.UID("org-uid-123"),
		},
	}

	user := &iamv1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-user",
			UID:  types.UID("user-uid-456"),
		},
		Spec: iamv1alpha1.UserSpec{
			Email: "test@example.com",
		},
	}

	membership := &resourcemanagerv1alpha1.OrganizationMembership{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-membership",
			Namespace: "organization-test-org",
			UID:       types.UID("membership-uid-789"),
		},
		Spec: resourcemanagerv1alpha1.OrganizationMembershipSpec{
			OrganizationRef: resourcemanagerv1alpha1.OrganizationReference{
				Name: "test-org",
			},
			UserRef: resourcemanagerv1alpha1.MemberReference{
				Name: "test-user",
			},
			Roles: []resourcemanagerv1alpha1.RoleReference{
				{
					Name:      "nonexistent-role",
					Namespace: "organization-test-org",
				},
			},
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(organization, user, membership).
		WithStatusSubresource(membership).
		Build()

	controller := &OrganizationMembershipController{
		Client: c,
	}

	// Test: Reconcile with nonexistent role
	err := controller.reconcileRoles(ctx, membership, organization, user)
	if err != nil {
		t.Fatalf("reconcileRoles should not fail for nonexistent role: %v", err)
	}

	// Verify RolesApplied condition indicates partial failure
	rolesAppliedCondition := apimeta.FindStatusCondition(membership.Status.Conditions, RolesApplied)
	if rolesAppliedCondition == nil {
		t.Fatal("RolesApplied condition not found")
	}
	if rolesAppliedCondition.Status != metav1.ConditionFalse {
		t.Errorf("Expected RolesApplied condition status to be False for nonexistent role, got %s",
			rolesAppliedCondition.Status)
	}

	// Verify AppliedRoles status shows failure
	if len(membership.Status.AppliedRoles) != 1 {
		t.Fatalf("Expected 1 applied role entry, got %d", len(membership.Status.AppliedRoles))
	}

	appliedRole := membership.Status.AppliedRoles[0]
	if appliedRole.Status != "Failed" {
		t.Errorf("Expected role status 'Failed', got '%s'", appliedRole.Status)
	}
	if appliedRole.Message == "" {
		t.Error("Expected error message for failed role")
	}
}

// TestOrganizationMembershipController_GeneratePolicyBindingName tests name generation
func TestOrganizationMembershipController_GeneratePolicyBindingName(t *testing.T) {
	controller := &OrganizationMembershipController{}

	membership := &resourcemanagerv1alpha1.OrganizationMembership{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-membership",
		},
	}

	roleRef := resourcemanagerv1alpha1.RoleReference{
		Name:      "viewer",
		Namespace: "org-ns",
	}

	name1 := controller.generatePolicyBindingName(membership, roleRef)
	name2 := controller.generatePolicyBindingName(membership, roleRef)

	// Names should be deterministic
	if name1 != name2 {
		t.Errorf("Expected deterministic name generation, got %s and %s", name1, name2)
	}

	// Names should be different for different roles
	roleRef2 := resourcemanagerv1alpha1.RoleReference{
		Name:      "editor",
		Namespace: "org-ns",
	}
	name3 := controller.generatePolicyBindingName(membership, roleRef2)
	if name1 == name3 {
		t.Errorf("Expected different names for different roles, got %s for both", name1)
	}
}

// TestOrganizationMembershipController_GetRoleKey tests role key generation
func TestOrganizationMembershipController_GetRoleKey(t *testing.T) {
	controller := &OrganizationMembershipController{}

	tests := []struct {
		name     string
		roleRef  resourcemanagerv1alpha1.RoleReference
		expected string
	}{
		{
			name: "role with namespace",
			roleRef: resourcemanagerv1alpha1.RoleReference{
				Name:      "viewer",
				Namespace: "org-ns",
			},
			expected: "org-ns/viewer",
		},
		{
			name: "role without namespace",
			roleRef: resourcemanagerv1alpha1.RoleReference{
				Name: "viewer",
			},
			expected: "viewer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := controller.getRoleKey(tt.roleRef)
			if key != tt.expected {
				t.Errorf("Expected role key '%s', got '%s'", tt.expected, key)
			}
		})
	}
}
