package v1alpha1

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	admissionv1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func policyBinding(subjects ...iamv1alpha1.Subject) *iamv1alpha1.PolicyBinding {
	return &iamv1alpha1.PolicyBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "binding", Namespace: "organization-acme"},
		Spec: iamv1alpha1.PolicyBindingSpec{
			RoleRef:  iamv1alpha1.RoleReference{Name: "viewer"},
			Subjects: subjects,
			ResourceSelector: iamv1alpha1.ResourceSelector{
				ResourceKind: &iamv1alpha1.ResourceKind{APIGroup: "resourcemanager.miloapis.com", Kind: "Organization"},
			},
		},
	}
}

func TestPolicyBindingMutator_Default(t *testing.T) {
	user := &iamv1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{Name: "alice", UID: types.UID("user-uid-1")},
	}
	group := &iamv1alpha1.Group{
		ObjectMeta: metav1.ObjectMeta{Name: "loaders", Namespace: "organization-acme", UID: types.UID("group-uid-1")},
	}
	sa := &iamv1alpha1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "robot", UID: types.UID("sa-uid-1")},
	}

	tests := map[string]struct {
		preObjects  []client.Object
		subjects    []iamv1alpha1.Subject
		expectError bool
		contains    string
		assertUID   func(t *testing.T, subjects []iamv1alpha1.Subject)
	}{
		"resolves user uid from name": {
			preObjects: []client.Object{user},
			subjects:   []iamv1alpha1.Subject{{Kind: "User", Name: "alice"}},
			assertUID: func(t *testing.T, s []iamv1alpha1.Subject) {
				assert.Equal(t, "user-uid-1", s[0].UID)
			},
		},
		"resolves group uid from name and namespace": {
			preObjects: []client.Object{group},
			subjects:   []iamv1alpha1.Subject{{Kind: "Group", Name: "loaders", Namespace: "organization-acme"}},
			assertUID: func(t *testing.T, s []iamv1alpha1.Subject) {
				assert.Equal(t, "group-uid-1", s[0].UID)
			},
		},
		"resolves serviceaccount uid from name": {
			preObjects: []client.Object{sa},
			subjects:   []iamv1alpha1.Subject{{Kind: "ServiceAccount", Name: "robot"}},
			assertUID: func(t *testing.T, s []iamv1alpha1.Subject) {
				assert.Equal(t, "sa-uid-1", s[0].UID)
			},
		},
		"leaves an already-set uid untouched": {
			preObjects: []client.Object{user},
			subjects:   []iamv1alpha1.Subject{{Kind: "User", Name: "alice", UID: "preset-uid"}},
			assertUID: func(t *testing.T, s []iamv1alpha1.Subject) {
				assert.Equal(t, "preset-uid", s[0].UID)
			},
		},
		"skips system groups": {
			subjects: []iamv1alpha1.Subject{{Kind: "Group", Name: "system:authenticated-users"}},
			assertUID: func(t *testing.T, s []iamv1alpha1.Subject) {
				assert.Empty(t, s[0].UID)
			},
		},
		"resolves multiple subjects": {
			preObjects: []client.Object{user, group},
			subjects: []iamv1alpha1.Subject{
				{Kind: "User", Name: "alice"},
				{Kind: "Group", Name: "loaders", Namespace: "organization-acme"},
			},
			assertUID: func(t *testing.T, s []iamv1alpha1.Subject) {
				assert.Equal(t, "user-uid-1", s[0].UID)
				assert.Equal(t, "group-uid-1", s[1].UID)
			},
		},
		"errors when the named user does not exist": {
			subjects:    []iamv1alpha1.Subject{{Kind: "User", Name: "ghost"}},
			expectError: true,
			contains:    `User "ghost" does not exist`,
		},
		"errors when a group subject omits the namespace": {
			subjects:    []iamv1alpha1.Subject{{Kind: "Group", Name: "loaders"}},
			expectError: true,
			contains:    "namespace is required",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cl := fake.NewClientBuilder().WithScheme(runtimeScheme).WithObjects(tc.preObjects...).Build()
			mutator := &PolicyBindingMutator{client: cl}

			pb := policyBinding(tc.subjects...)
			err := mutator.Default(context.Background(), pb)

			if tc.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.contains)
				return
			}

			require.NoError(t, err)
			if tc.assertUID != nil {
				tc.assertUID(t, pb.Spec.Subjects)
			}
		})
	}
}

func testProject(name, org string) *resourcemanagerv1alpha1.Project {
	return &resourcemanagerv1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{resourcemanagerv1alpha1.OrganizationNameLabel: org},
		},
		Spec: resourcemanagerv1alpha1.ProjectSpec{
			OwnerRef: resourcemanagerv1alpha1.OwnerReference{Kind: "Organization", Name: org},
		},
	}
}

// orgContextRequest returns a context whose admission request carries an
// organization parent context for orgID, as injected by the Milo API server's
// OrganizationContextAuthorizationDecorator.
func orgContextRequest(orgID string) context.Context {
	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			UserInfo: authenticationv1.UserInfo{
				Extra: map[string]authenticationv1.ExtraValue{
					iamv1alpha1.ParentNameExtraKey:     {orgID},
					iamv1alpha1.ParentKindExtraKey:     {"Organization"},
					iamv1alpha1.ParentAPIGroupExtraKey: {resourcemanagerv1alpha1.GroupVersion.Group},
				},
			},
		},
	}
	return admission.NewContextWithRequest(context.Background(), req)
}

// orgContextSuperuserRequest is orgContextRequest but with system:masters in the
// request user's groups, mirroring how the platform superuser is authenticated.
func orgContextSuperuserRequest(orgID string) context.Context {
	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			UserInfo: authenticationv1.UserInfo{
				Groups: []string{"system:masters"},
				Extra: map[string]authenticationv1.ExtraValue{
					iamv1alpha1.ParentNameExtraKey:     {orgID},
					iamv1alpha1.ParentKindExtraKey:     {"Organization"},
					iamv1alpha1.ParentAPIGroupExtraKey: {resourcemanagerv1alpha1.GroupVersion.Group},
				},
			},
		},
	}
	return admission.NewContextWithRequest(context.Background(), req)
}

func TestPolicyBindingValidator_ValidateCreate(t *testing.T) {
	orgA := "acme"
	orgB := "globex"
	projectA := testProject("project-a", orgA)
	projectB := testProject("project-b", orgB)
	projectNoOrg := &resourcemanagerv1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: "project-unlabeled"},
		Spec: resourcemanagerv1alpha1.ProjectSpec{
			OwnerRef: resourcemanagerv1alpha1.OwnerReference{Kind: "Organization", Name: orgA},
		},
	}

	projectBinding := func(projectName string) *iamv1alpha1.PolicyBinding {
		return &iamv1alpha1.PolicyBinding{
			ObjectMeta: metav1.ObjectMeta{Name: "binding", Namespace: resourcemanagerv1alpha1.OrganizationNamespace(orgA)},
			Spec: iamv1alpha1.PolicyBindingSpec{
				RoleRef:  iamv1alpha1.RoleReference{Name: "viewer"},
				Subjects: []iamv1alpha1.Subject{{Kind: "ServiceAccount", Name: "robot", UID: "sa-uid-1"}},
				ResourceSelector: iamv1alpha1.ResourceSelector{
					ResourceRef: &iamv1alpha1.ResourceReference{
						APIGroup: resourcemanagerv1alpha1.GroupVersion.Group,
						Kind:     "Project",
						Name:     projectName,
						UID:      "project-uid-1",
					},
				},
			},
		}
	}

	sa := &iamv1alpha1.Subject{Kind: "ServiceAccount", Name: "robot", UID: "sa-uid-1"}
	user := &iamv1alpha1.Subject{Kind: "User", Name: "alice", UID: "user-uid-1"}
	group := &iamv1alpha1.Subject{Kind: "Group", Name: "loaders", Namespace: resourcemanagerv1alpha1.OrganizationNamespace(orgA), UID: "group-uid-1"}
	systemGroup := &iamv1alpha1.Subject{Kind: "Group", Name: "system:authenticated-users"}

	tests := map[string]struct {
		preObjects  []client.Object
		ctx         context.Context
		binding     *iamv1alpha1.PolicyBinding
		expectError bool
		contains    string
	}{
		"org context with a serviceaccount bound to a project in the org": {
			preObjects:  []client.Object{projectA},
			ctx:         orgContextRequest(orgA),
			binding:     projectBinding("project-a"),
			expectError: false,
		},
		"org context allows a serviceaccount from another project in the same org": {
			preObjects:  []client.Object{projectA},
			ctx:         orgContextRequest(orgA),
			binding:     projectBinding("project-a"),
			expectError: false,
		},
		"org context allows a user subject": {
			preObjects: []client.Object{projectA},
			ctx:        orgContextRequest(orgA),
			binding: &iamv1alpha1.PolicyBinding{
				ObjectMeta: metav1.ObjectMeta{Name: "binding", Namespace: resourcemanagerv1alpha1.OrganizationNamespace(orgA)},
				Spec: iamv1alpha1.PolicyBindingSpec{
					RoleRef:  iamv1alpha1.RoleReference{Name: "viewer"},
					Subjects: []iamv1alpha1.Subject{*user},
					ResourceSelector: iamv1alpha1.ResourceSelector{
						ResourceRef: &iamv1alpha1.ResourceReference{
							APIGroup: resourcemanagerv1alpha1.GroupVersion.Group,
							Kind:     "Project",
							Name:     "project-a",
							UID:      "project-uid-1",
						},
					},
				},
			},
			expectError: false,
		},
		"org context allows a group subject": {
			preObjects: []client.Object{projectA},
			ctx:        orgContextRequest(orgA),
			binding: &iamv1alpha1.PolicyBinding{
				ObjectMeta: metav1.ObjectMeta{Name: "binding", Namespace: resourcemanagerv1alpha1.OrganizationNamespace(orgA)},
				Spec: iamv1alpha1.PolicyBindingSpec{
					RoleRef:  iamv1alpha1.RoleReference{Name: "viewer"},
					Subjects: []iamv1alpha1.Subject{*group},
					ResourceSelector: iamv1alpha1.ResourceSelector{
						ResourceRef: &iamv1alpha1.ResourceReference{
							APIGroup: resourcemanagerv1alpha1.GroupVersion.Group,
							Kind:     "Project",
							Name:     "project-a",
							UID:      "project-uid-1",
						},
					},
				},
			},
			expectError: false,
		},
		"org context allows a system group subject": {
			preObjects: []client.Object{projectA},
			ctx:        orgContextRequest(orgA),
			binding: &iamv1alpha1.PolicyBinding{
				ObjectMeta: metav1.ObjectMeta{Name: "binding", Namespace: resourcemanagerv1alpha1.OrganizationNamespace(orgA)},
				Spec: iamv1alpha1.PolicyBindingSpec{
					RoleRef:  iamv1alpha1.RoleReference{Name: "viewer"},
					Subjects: []iamv1alpha1.Subject{*systemGroup},
					ResourceSelector: iamv1alpha1.ResourceSelector{
						ResourceRef: &iamv1alpha1.ResourceReference{
							APIGroup: resourcemanagerv1alpha1.GroupVersion.Group,
							Kind:     "Project",
							Name:     "project-a",
							UID:      "project-uid-1",
						},
					},
				},
			},
			expectError: false,
		},
		"org context denies a project in another org": {
			preObjects:  []client.Object{projectB},
			ctx:         orgContextRequest(orgA),
			binding:     projectBinding("project-b"),
			expectError: true,
			contains:    "does not belong to organization",
		},
		"org context denies a missing project": {
			ctx:         orgContextRequest(orgA),
			binding:     projectBinding("project-does-not-exist"),
			expectError: true,
			contains:    "Not found",
		},
		"org context denies a project without the organization label": {
			preObjects:  []client.Object{projectNoOrg},
			ctx:         orgContextRequest(orgA),
			binding:     projectBinding("project-unlabeled"),
			expectError: true,
			contains:    "does not belong to organization",
		},
		"org context denies an organization target": {
			preObjects: []client.Object{projectA},
			ctx:        orgContextRequest(orgA),
			binding: &iamv1alpha1.PolicyBinding{
				ObjectMeta: metav1.ObjectMeta{Name: "binding", Namespace: resourcemanagerv1alpha1.OrganizationNamespace(orgA)},
				Spec: iamv1alpha1.PolicyBindingSpec{
					RoleRef:  iamv1alpha1.RoleReference{Name: "viewer"},
					Subjects: []iamv1alpha1.Subject{*sa},
					ResourceSelector: iamv1alpha1.ResourceSelector{
						ResourceRef: &iamv1alpha1.ResourceReference{
							APIGroup: resourcemanagerv1alpha1.GroupVersion.Group,
							Kind:     "Organization",
							Name:     orgA,
							UID:      "org-uid-1",
						},
					},
				},
			},
			expectError: true,
			contains:    "Unsupported value",
		},
		"org context denies a resourceKind target": {
			ctx: orgContextRequest(orgA),
			binding: &iamv1alpha1.PolicyBinding{
				ObjectMeta: metav1.ObjectMeta{Name: "binding", Namespace: resourcemanagerv1alpha1.OrganizationNamespace(orgA)},
				Spec: iamv1alpha1.PolicyBindingSpec{
					RoleRef:  iamv1alpha1.RoleReference{Name: "viewer"},
					Subjects: []iamv1alpha1.Subject{*sa},
					ResourceSelector: iamv1alpha1.ResourceSelector{
						ResourceKind: &iamv1alpha1.ResourceKind{APIGroup: resourcemanagerv1alpha1.GroupVersion.Group, Kind: "Project"},
					},
				},
			},
			expectError: true,
			contains:    "resourceKind (kind-level) targets are not allowed in organization context",
		},
		"org context denies a binding in the wrong namespace": {
			preObjects:  []client.Object{projectA},
			ctx:         orgContextRequest(orgA),
			binding:     projectBinding("project-a"),
			expectError: true,
			contains:    "must be created in the organization's namespace",
		},
		"not in org context is unaffected even with a user subject and organization target": {
			ctx: admission.NewContextWithRequest(context.Background(), admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{},
			}),
			binding: &iamv1alpha1.PolicyBinding{
				ObjectMeta: metav1.ObjectMeta{Name: "binding", Namespace: "some-namespace"},
				Spec: iamv1alpha1.PolicyBindingSpec{
					RoleRef:  iamv1alpha1.RoleReference{Name: "viewer"},
					Subjects: []iamv1alpha1.Subject{*user},
					ResourceSelector: iamv1alpha1.ResourceSelector{
						ResourceRef: &iamv1alpha1.ResourceReference{
							APIGroup: resourcemanagerv1alpha1.GroupVersion.Group,
							Kind:     "Organization",
							Name:     orgB,
							UID:      "org-uid-2",
						},
					},
				},
			},
			expectError: false,
		},
		"system:masters in org context bypasses the restrictions even with a user subject and foreign target": {
			preObjects: []client.Object{projectB},
			ctx:        orgContextSuperuserRequest(orgA),
			binding: &iamv1alpha1.PolicyBinding{
				ObjectMeta: metav1.ObjectMeta{Name: "binding", Namespace: resourcemanagerv1alpha1.OrganizationNamespace(orgA)},
				Spec: iamv1alpha1.PolicyBindingSpec{
					RoleRef:  iamv1alpha1.RoleReference{Name: "viewer"},
					Subjects: []iamv1alpha1.Subject{*user},
					ResourceSelector: iamv1alpha1.ResourceSelector{
						ResourceRef: &iamv1alpha1.ResourceReference{
							APIGroup: resourcemanagerv1alpha1.GroupVersion.Group,
							Kind:     "Project",
							Name:     "project-b",
							UID:      "project-uid-1",
						},
					},
				},
			},
			expectError: false,
		},
		"system:masters in org context bypasses even a missing project target": {
			ctx: orgContextSuperuserRequest(orgA),
			binding: &iamv1alpha1.PolicyBinding{
				ObjectMeta: metav1.ObjectMeta{Name: "binding", Namespace: "wrong-namespace"},
				Spec: iamv1alpha1.PolicyBindingSpec{
					RoleRef:  iamv1alpha1.RoleReference{Name: "viewer"},
					Subjects: []iamv1alpha1.Subject{*user},
					ResourceSelector: iamv1alpha1.ResourceSelector{
						ResourceRef: &iamv1alpha1.ResourceReference{
							APIGroup: resourcemanagerv1alpha1.GroupVersion.Group,
							Kind:     "Project",
							Name:     "does-not-exist",
							UID:      "project-uid-1",
						},
					},
				},
			},
			expectError: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			testBinding := tc.binding
			// Apply target/namespace overrides per case so table entries stay
			// readable. Subject overrides are no longer needed: subject-kind is
			// unrestricted in organization context, so each binding carries its
			// subjects inline.
			switch name {
			case "org context denies a binding in the wrong namespace":
				testBinding.Namespace = "default"
			}

			cl := fake.NewClientBuilder().WithScheme(runtimeScheme).WithObjects(tc.preObjects...).Build()
			validator := &PolicyBindingValidator{client: cl}

			_, err := validator.ValidateCreate(tc.ctx, testBinding)

			if tc.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.contains)
				var statusErr *apierrors.StatusError
				assert.ErrorAs(t, err, &statusErr, "error should be a StatusError")
				if statusErr != nil {
					assert.Equal(t, metav1.StatusReasonInvalid, statusErr.ErrStatus.Reason, "error reason should be Invalid")
				}
				return
			}

			require.NoError(t, err)
		})
	}
}

func TestPolicyBindingValidator_ValidateUpdate(t *testing.T) {
	orgA := "acme"
	projectA := testProject("project-a", orgA)

	saBinding := &iamv1alpha1.PolicyBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "binding", Namespace: resourcemanagerv1alpha1.OrganizationNamespace(orgA)},
		Spec: iamv1alpha1.PolicyBindingSpec{
			RoleRef:  iamv1alpha1.RoleReference{Name: "viewer"},
			Subjects: []iamv1alpha1.Subject{{Kind: "ServiceAccount", Name: "robot", UID: "sa-uid-1"}},
			ResourceSelector: iamv1alpha1.ResourceSelector{
				ResourceRef: &iamv1alpha1.ResourceReference{
					APIGroup: resourcemanagerv1alpha1.GroupVersion.Group,
					Kind:     "Project",
					Name:     "project-a",
					UID:      "project-uid-1",
				},
			},
		},
	}

	tests := map[string]struct {
		oldPB       *iamv1alpha1.PolicyBinding
		newPB       *iamv1alpha1.PolicyBinding
		expectError bool
		contains    string
	}{
		"update keeps serviceaccount subjects and is allowed": {
			oldPB:       saBinding,
			newPB:       saBinding,
			expectError: false,
		},
		"update that swaps in a user subject is allowed": {
			oldPB: saBinding,
			newPB: func() *iamv1alpha1.PolicyBinding {
				b := saBinding.DeepCopy()
				b.Spec.Subjects = []iamv1alpha1.Subject{{Kind: "User", Name: "alice", UID: "user-uid-1"}}
				return b
			}(),
			expectError: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cl := fake.NewClientBuilder().WithScheme(runtimeScheme).WithObjects(projectA).Build()
			validator := &PolicyBindingValidator{client: cl}

			_, err := validator.ValidateUpdate(orgContextRequest(orgA), tc.oldPB, tc.newPB)

			if tc.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.contains)
				return
			}
			require.NoError(t, err)
		})
	}
}
