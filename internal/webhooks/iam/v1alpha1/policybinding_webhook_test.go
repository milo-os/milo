package v1alpha1

import (
	"context"
	"errors"
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
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
)

func policyBinding(subjects ...iamv1alpha1.Subject) *iamv1alpha1.PolicyBinding {
	return &iamv1alpha1.PolicyBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "binding", Namespace: "organization-acme"},
		Spec: iamv1alpha1.PolicyBindingSpec{
			RoleRef:  iamv1alpha1.RoleReference{Name: "viewer", Namespace: testAssignableRolesNamespace},
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
		"leaves a namespaced group with an already-set uid untouched": {
			subjects: []iamv1alpha1.Subject{{Kind: "Group", Name: "loaders", Namespace: "organization-acme", UID: "preset-uid"}},
			assertUID: func(t *testing.T, s []iamv1alpha1.Subject) {
				assert.Equal(t, "preset-uid", s[0].UID)
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

// projectContextRequest returns a context whose admission request carries a
// project parent context, as a future project control plane would inject it.
// The PolicyBinding webhook is not yet wired for project scope and fails closed
// on it, so this is used to assert that safeguard.
func projectContextRequest(projectName string) context.Context {
	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			UserInfo: authenticationv1.UserInfo{
				Extra: map[string]authenticationv1.ExtraValue{
					iamv1alpha1.ParentNameExtraKey:     {projectName},
					iamv1alpha1.ParentKindExtraKey:     {"Project"},
					iamv1alpha1.ParentAPIGroupExtraKey: {resourcemanagerv1alpha1.GroupVersion.Group},
				},
			},
		},
	}
	return admission.NewContextWithRequest(context.Background(), req)
}

// testAssignableRolesNamespace is the namespace that the PolicyBinding
// validator requires org-context bindings to reference for their roleRef.
const testAssignableRolesNamespace = "datum-cloud"

// assignableRole returns the assignable Role that org-context bindings in these
// tests reference, existing in the assignable-roles namespace.
func assignableRole() *iamv1alpha1.Role {
	return &iamv1alpha1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: "viewer", Namespace: testAssignableRolesNamespace},
		Spec:       iamv1alpha1.RoleSpec{LaunchStage: "Stable"},
	}
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
				RoleRef:  iamv1alpha1.RoleReference{Name: "viewer", Namespace: testAssignableRolesNamespace},
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
					RoleRef:  iamv1alpha1.RoleReference{Name: "viewer", Namespace: testAssignableRolesNamespace},
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
					RoleRef:  iamv1alpha1.RoleReference{Name: "viewer", Namespace: testAssignableRolesNamespace},
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
					RoleRef:  iamv1alpha1.RoleReference{Name: "viewer", Namespace: testAssignableRolesNamespace},
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
		"org context allows an organization-wide grant to the request organization": {
			preObjects: []client.Object{projectA},
			ctx:        orgContextRequest(orgA),
			binding: &iamv1alpha1.PolicyBinding{
				ObjectMeta: metav1.ObjectMeta{Name: "binding", Namespace: resourcemanagerv1alpha1.OrganizationNamespace(orgA)},
				Spec: iamv1alpha1.PolicyBindingSpec{
					RoleRef:  iamv1alpha1.RoleReference{Name: "viewer", Namespace: testAssignableRolesNamespace},
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
			expectError: false,
		},
		"org context denies an organization-wide grant to another organization": {
			ctx: orgContextRequest(orgA),
			binding: &iamv1alpha1.PolicyBinding{
				ObjectMeta: metav1.ObjectMeta{Name: "binding", Namespace: resourcemanagerv1alpha1.OrganizationNamespace(orgA)},
				Spec: iamv1alpha1.PolicyBindingSpec{
					RoleRef:  iamv1alpha1.RoleReference{Name: "viewer", Namespace: testAssignableRolesNamespace},
					Subjects: []iamv1alpha1.Subject{*sa},
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
			expectError: true,
			contains:    "does not match the request organization",
		},
		"org context denies a resourceKind target": {
			ctx: orgContextRequest(orgA),
			binding: &iamv1alpha1.PolicyBinding{
				ObjectMeta: metav1.ObjectMeta{Name: "binding", Namespace: resourcemanagerv1alpha1.OrganizationNamespace(orgA)},
				Spec: iamv1alpha1.PolicyBindingSpec{
					RoleRef:  iamv1alpha1.RoleReference{Name: "viewer", Namespace: testAssignableRolesNamespace},
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
					RoleRef:  iamv1alpha1.RoleReference{Name: "viewer", Namespace: testAssignableRolesNamespace},
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
		"project context fails closed on create": {
			ctx:         projectContextRequest("project-a"),
			binding:     projectBinding("project-a"),
			expectError: true,
			contains:    "project-context PolicyBindings are not yet supported",
		},
		"org context denies a role in a non-assignable namespace": {
			preObjects: []client.Object{projectA},
			ctx:        orgContextRequest(orgA),
			binding: &iamv1alpha1.PolicyBinding{
				ObjectMeta: metav1.ObjectMeta{Name: "binding", Namespace: resourcemanagerv1alpha1.OrganizationNamespace(orgA)},
				Spec: iamv1alpha1.PolicyBindingSpec{
					RoleRef:  iamv1alpha1.RoleReference{Name: "viewer", Namespace: resourcemanagerv1alpha1.OrganizationNamespace(orgA)},
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
			expectError: true,
			contains:    "spec.roleRef.namespace",
		},
		"org context denies a non-existent role in the assignable namespace": {
			preObjects: []client.Object{projectA},
			ctx:        orgContextRequest(orgA),
			binding: &iamv1alpha1.PolicyBinding{
				ObjectMeta: metav1.ObjectMeta{Name: "binding", Namespace: resourcemanagerv1alpha1.OrganizationNamespace(orgA)},
				Spec: iamv1alpha1.PolicyBindingSpec{
					RoleRef:  iamv1alpha1.RoleReference{Name: "does-not-exist", Namespace: testAssignableRolesNamespace},
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
			expectError: true,
			contains:    "datum-cloud/does-not-exist",
		},
		"system:masters in org context bypasses the restrictions even with a user subject and foreign target": {
			preObjects: []client.Object{projectB},
			ctx:        orgContextSuperuserRequest(orgA),
			binding: &iamv1alpha1.PolicyBinding{
				ObjectMeta: metav1.ObjectMeta{Name: "binding", Namespace: resourcemanagerv1alpha1.OrganizationNamespace(orgA)},
				Spec: iamv1alpha1.PolicyBindingSpec{
					RoleRef:  iamv1alpha1.RoleReference{Name: "viewer", Namespace: testAssignableRolesNamespace},
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
					RoleRef:  iamv1alpha1.RoleReference{Name: "viewer", Namespace: testAssignableRolesNamespace},
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

			// Seed the assignable Role alongside each case's preObjects so the
			// org-context role check passes everywhere it isn't the target of the
			// test.
			objects := append([]client.Object{assignableRole()}, tc.preObjects...)
			cl := fake.NewClientBuilder().WithScheme(runtimeScheme).WithObjects(objects...).Build()
			validator := &PolicyBindingValidator{client: cl, assignableRolesNamespace: testAssignableRolesNamespace}

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
			RoleRef:  iamv1alpha1.RoleReference{Name: "viewer", Namespace: testAssignableRolesNamespace},
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
		ctx         context.Context
		oldPB       *iamv1alpha1.PolicyBinding
		newPB       *iamv1alpha1.PolicyBinding
		expectError bool
		contains    string
	}{
		"update keeps serviceaccount subjects and is allowed": {
			ctx:         orgContextRequest(orgA),
			oldPB:       saBinding,
			newPB:       saBinding,
			expectError: false,
		},
		"update that swaps in a user subject is allowed": {
			ctx:   orgContextRequest(orgA),
			oldPB: saBinding,
			newPB: func() *iamv1alpha1.PolicyBinding {
				b := saBinding.DeepCopy()
				b.Spec.Subjects = []iamv1alpha1.Subject{{Kind: "User", Name: "alice", UID: "user-uid-1"}}
				return b
			}(),
			expectError: false,
		},
		"update that retargets to an organization-wide grant in the request org is allowed": {
			ctx:   orgContextRequest(orgA),
			oldPB: saBinding,
			newPB: func() *iamv1alpha1.PolicyBinding {
				b := saBinding.DeepCopy()
				b.Spec.ResourceSelector.ResourceRef = &iamv1alpha1.ResourceReference{
					APIGroup: resourcemanagerv1alpha1.GroupVersion.Group,
					Kind:     "Organization",
					Name:     orgA,
					UID:      "org-uid-1",
				}
				return b
			}(),
			expectError: false,
		},
		"update that retargets to an organization-wide grant in another org is denied": {
			ctx:   orgContextRequest(orgA),
			oldPB: saBinding,
			newPB: func() *iamv1alpha1.PolicyBinding {
				b := saBinding.DeepCopy()
				b.Spec.ResourceSelector.ResourceRef = &iamv1alpha1.ResourceReference{
					APIGroup: resourcemanagerv1alpha1.GroupVersion.Group,
					Kind:     "Organization",
					Name:     "globex",
					UID:      "org-uid-2",
				}
				return b
			}(),
			expectError: true,
			contains:    "does not match the request organization",
		},
		"project context fails closed on update": {
			ctx:         projectContextRequest("project-a"),
			oldPB:       saBinding,
			newPB:       saBinding,
			expectError: true,
			contains:    "project-context PolicyBindings are not yet supported",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cl := fake.NewClientBuilder().WithScheme(runtimeScheme).WithObjects(projectA, assignableRole()).Build()
			validator := &PolicyBindingValidator{client: cl, assignableRolesNamespace: testAssignableRolesNamespace}

			_, err := validator.ValidateUpdate(tc.ctx, tc.oldPB, tc.newPB)

			if tc.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.contains)
				return
			}
			require.NoError(t, err)
		})
	}
}
func TestPolicyBindingValidator_ValidateDelete(t *testing.T) {
	orgA := "acme"

	bindingIn := func(namespace string) *iamv1alpha1.PolicyBinding {
		return &iamv1alpha1.PolicyBinding{
			ObjectMeta: metav1.ObjectMeta{Name: "binding", Namespace: namespace},
			Spec: iamv1alpha1.PolicyBindingSpec{
				RoleRef: iamv1alpha1.RoleReference{Name: "viewer"},
				Subjects: []iamv1alpha1.Subject{
					{Kind: "ServiceAccount", Name: "robot", UID: "sa-uid-1"},
				},
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
	}

	tests := map[string]struct {
		ctx         context.Context
		binding     *iamv1alpha1.PolicyBinding
		expectError bool
		contains    string
	}{
		"org context can delete a binding in the organization's namespace": {
			ctx:         orgContextRequest(orgA),
			binding:     bindingIn(resourcemanagerv1alpha1.OrganizationNamespace(orgA)),
			expectError: false,
		},
		"org context cannot delete a binding in another namespace": {
			ctx:         orgContextRequest(orgA),
			binding:     bindingIn("default"),
			expectError: true,
			contains:    "may only be deleted from the organization's namespace",
		},
		"org context cannot delete a binding in another organization's namespace": {
			ctx:         orgContextRequest(orgA),
			binding:     bindingIn(resourcemanagerv1alpha1.OrganizationNamespace("globex")),
			expectError: true,
			contains:    "may only be deleted from the organization's namespace",
		},
		"not in org context can delete a binding in any namespace": {
			ctx: admission.NewContextWithRequest(context.Background(), admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{},
			}),
			binding:     bindingIn("default"),
			expectError: false,
		},
		"system:masters in org context bypasses the deletion restriction": {
			ctx:         orgContextSuperuserRequest(orgA),
			binding:     bindingIn("default"),
			expectError: false,
		},
		"project context fails closed on delete": {
			ctx:         projectContextRequest("project-a"),
			binding:     bindingIn("default"),
			expectError: true,
			contains:    "project-context PolicyBindings are not yet supported",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cl := fake.NewClientBuilder().WithScheme(runtimeScheme).Build()
			validator := &PolicyBindingValidator{client: cl}

			_, err := validator.ValidateDelete(tc.ctx, tc.binding)

			if tc.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.contains)
				return
			}
			require.NoError(t, err)
		})
	}
}

// projectTargetedBinding returns a binding whose resourceRef names the Project
// projectName with the uid projectUID, as the portal's "share project" flow
// creates it.
func projectTargetedBinding(projectName, projectUID string) *iamv1alpha1.PolicyBinding {
	pb := policyBinding(iamv1alpha1.Subject{Kind: "User", Name: "alice", UID: "user-uid-1"})
	pb.Spec.ResourceSelector = iamv1alpha1.ResourceSelector{
		ResourceRef: &iamv1alpha1.ResourceReference{
			APIGroup: resourcemanagerv1alpha1.GroupVersion.Group,
			Kind:     "Project",
			Name:     projectName,
			UID:      projectUID,
		},
	}
	return pb
}

// projectWithUID returns the Project name in organization acme with the given uid.
func projectWithUID(name, uid string) *resourcemanagerv1alpha1.Project {
	project := testProject(name, "acme")
	project.UID = types.UID(uid)
	return project
}

// projectOwnerReference is the owner reference a binding on the Project name
// with the given uid must carry: the same shape createOwnerPolicyBinding gives
// the project owner's binding, with neither controller nor blockOwnerDeletion.
func projectOwnerReference(name, uid string) metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion: resourcemanagerv1alpha1.GroupVersion.String(),
		Kind:       "Project",
		Name:       name,
		UID:        types.UID(uid),
	}
}

// requestWith returns a context whose admission request has the given
// operation, groups and extras.
func requestWith(op admissionv1.Operation, groups []string, extra map[string]authenticationv1.ExtraValue) context.Context {
	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: op,
			UserInfo:  authenticationv1.UserInfo{Groups: groups, Extra: extra},
		},
	}
	return admission.NewContextWithRequest(context.Background(), req)
}

// projectParentExtra is the parent context the Milo API server injects into a
// request made through a project control plane.
func projectParentExtra(projectName string) map[string]authenticationv1.ExtraValue {
	return map[string]authenticationv1.ExtraValue{
		iamv1alpha1.ParentNameExtraKey:     {projectName},
		iamv1alpha1.ParentKindExtraKey:     {"Project"},
		iamv1alpha1.ParentAPIGroupExtraKey: {resourcemanagerv1alpha1.GroupVersion.Group},
	}
}

// throughProjectControlPlane marks ctx as the webhook server's ClusterAwareServer
// does for a request made through the project control plane projectName.
func throughProjectControlPlane(ctx context.Context, projectName string) context.Context {
	return mccontext.WithCluster(ctx, multicluster.ClusterName(projectName))
}

func TestPolicyBindingMutator_ProjectOwnerReference(t *testing.T) {
	const name, liveUID, staleUID = "web", "project-uid-live", "project-uid-stale"

	otherOwner := metav1.OwnerReference{APIVersion: "iam.miloapis.com/v1alpha1", Kind: "Group", Name: "loaders", UID: "group-uid-1"}
	withOwners := func(pb *iamv1alpha1.PolicyBinding, refs ...metav1.OwnerReference) *iamv1alpha1.PolicyBinding {
		pb.OwnerReferences = refs
		return pb
	}
	withRef := func(ref iamv1alpha1.ResourceReference) *iamv1alpha1.PolicyBinding {
		pb := projectTargetedBinding(name, liveUID)
		pb.Spec.ResourceSelector.ResourceRef = &ref
		return pb
	}

	tests := map[string]struct {
		ctx        context.Context
		cached     []client.Object // what the webhook's cache holds
		live       []client.Object // what the API server holds
		liveErr    error           // the API server's answer to every read, when set
		binding    *iamv1alpha1.PolicyBinding
		wantOwners []metav1.OwnerReference
		wantErr    string
		wantNoRead bool // the case must not read the Project at all
	}{
		// A UID mismatch reads as an absent owner to the garbage collector,
		// which would delete the binding at once.
		"adds no owner when the spec names another uid than the live Project": {
			ctx:     orgContextRequest("acme"),
			live:    []client.Object{projectWithUID(name, liveUID)},
			binding: projectTargetedBinding(name, staleUID),
		},
		"decides from the live Project, not the cache, when the cache holds the spec's uid": {
			ctx:     orgContextRequest("acme"),
			cached:  []client.Object{projectWithUID(name, staleUID)},
			live:    []client.Object{projectWithUID(name, liveUID)},
			binding: projectTargetedBinding(name, staleUID),
		},
		"decides from the live Project, not the cache, when the cache holds another uid": {
			ctx:        orgContextRequest("acme"),
			cached:     []client.Object{projectWithUID(name, staleUID)},
			live:       []client.Object{projectWithUID(name, liveUID)},
			binding:    projectTargetedBinding(name, liveUID),
			wantOwners: []metav1.OwnerReference{projectOwnerReference(name, liveUID)},
		},
		"adds an owner reference to the live Project in organization context": {
			ctx:        orgContextRequest("acme"),
			live:       []client.Object{projectWithUID(name, liveUID)},
			binding:    projectTargetedBinding(name, liveUID),
			wantOwners: []metav1.OwnerReference{projectOwnerReference(name, liveUID)},
		},
		"adds an owner reference in platform scope": {
			ctx:        requestWith(admissionv1.Create, nil, nil),
			live:       []client.Object{projectWithUID(name, liveUID)},
			binding:    projectTargetedBinding(name, liveUID),
			wantOwners: []metav1.OwnerReference{projectOwnerReference(name, liveUID)},
		},
		"adds an owner reference on an update": {
			ctx:        requestWith(admissionv1.Update, []string{"system:masters"}, nil),
			live:       []client.Object{projectWithUID(name, liveUID)},
			binding:    projectTargetedBinding(name, liveUID),
			wantOwners: []metav1.OwnerReference{projectOwnerReference(name, liveUID)},
		},
		"adds no owner when the Project does not exist": {
			ctx:     orgContextRequest("acme"),
			binding: projectTargetedBinding(name, liveUID),
		},
		"fails closed when the Project cannot be read": {
			ctx:     orgContextRequest("acme"),
			liveErr: errors.New("etcd unavailable"),
			binding: projectTargetedBinding(name, liveUID),
			wantErr: "etcd unavailable",
		},
		// A binding stored in a project control plane would look for its owner
		// there, find no Project, and be deleted.
		"adds no owner on a project control plane": {
			ctx:     throughProjectControlPlane(projectContextRequest("other"), "other"),
			live:    []client.Object{projectWithUID(name, liveUID)},
			binding: projectTargetedBinding(name, liveUID),
		},
		"adds no owner on a project control plane for system:masters": {
			ctx:     throughProjectControlPlane(requestWith(admissionv1.Create, []string{"system:masters"}, projectParentExtra("other")), "other"),
			live:    []client.Object{projectWithUID(name, liveUID)},
			binding: projectTargetedBinding(name, liveUID),
		},
		"adds no owner for a Project kind in another API group": {
			ctx:     orgContextRequest("acme"),
			live:    []client.Object{projectWithUID(name, liveUID)},
			binding: withRef(iamv1alpha1.ResourceReference{APIGroup: "example.com", Kind: "Project", Name: name, UID: liveUID}),
		},
		// A Project of the same name and uid exists in the next two cases, so
		// only the kind keeps the reference off.
		"adds no owner for an Organization target": {
			ctx:     orgContextRequest("acme"),
			live:    []client.Object{projectWithUID(name, liveUID)},
			binding: withRef(iamv1alpha1.ResourceReference{APIGroup: resourcemanagerv1alpha1.GroupVersion.Group, Kind: "Organization", Name: name, UID: liveUID}),
		},
		"adds no owner for a namespaced target": {
			ctx:     orgContextRequest("acme"),
			live:    []client.Object{projectWithUID(name, liveUID)},
			binding: withRef(iamv1alpha1.ResourceReference{APIGroup: resourcemanagerv1alpha1.GroupVersion.Group, Kind: "OrganizationMembership", Name: name, Namespace: "organization-other", UID: liveUID}),
		},
		"adds no owner for a resourceKind selector": {
			ctx:     orgContextRequest("acme"),
			binding: policyBinding(iamv1alpha1.Subject{Kind: "User", Name: "alice", UID: "user-uid-1"}),
		},
		"keeps an existing reference to the Project and adds no duplicate": {
			ctx:        orgContextRequest("acme"),
			live:       []client.Object{projectWithUID(name, liveUID)},
			binding:    withOwners(projectTargetedBinding(name, liveUID), projectOwnerReference(name, liveUID)),
			wantOwners: []metav1.OwnerReference{projectOwnerReference(name, liveUID)},
		},
		// After the backfill every update of an owned binding comes here; a read
		// that fails or stalls must not refuse it.
		"reads nothing for an update of a binding that already has the reference": {
			ctx:        requestWith(admissionv1.Update, nil, nil),
			liveErr:    errors.New("etcd unavailable"),
			binding:    withOwners(projectTargetedBinding(name, liveUID), projectOwnerReference(name, liveUID)),
			wantOwners: []metav1.OwnerReference{projectOwnerReference(name, liveUID)},
			wantNoRead: true,
		},
		// A reference to the Project's name with an older uid reads as absent to
		// the collector; the live one keeps the binding alive.
		"adds the live reference beside one to an older uid of the same name": {
			ctx:        orgContextRequest("acme"),
			live:       []client.Object{projectWithUID(name, liveUID)},
			binding:    withOwners(projectTargetedBinding(name, liveUID), projectOwnerReference(name, staleUID)),
			wantOwners: []metav1.OwnerReference{projectOwnerReference(name, staleUID), projectOwnerReference(name, liveUID)},
		},
		"appends to other owner references and keeps them": {
			ctx:        orgContextRequest("acme"),
			live:       []client.Object{projectWithUID(name, liveUID)},
			binding:    withOwners(projectTargetedBinding(name, liveUID), otherOwner),
			wantOwners: []metav1.OwnerReference{otherOwner, projectOwnerReference(name, liveUID)},
		},
	}

	for testName, tc := range tests {
		t.Run(testName, func(t *testing.T) {
			cached := fake.NewClientBuilder().WithScheme(runtimeScheme).WithObjects(tc.cached...).Build()
			reads := 0
			live := fake.NewClientBuilder().WithScheme(runtimeScheme).WithObjects(tc.live...).
				WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						reads++
						if tc.liveErr != nil {
							return tc.liveErr
						}
						return c.Get(ctx, key, obj, opts...)
					},
				}).Build()
			mutator := &PolicyBindingMutator{client: cached, reader: live}

			err := mutator.Default(tc.ctx, tc.binding)

			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantOwners, tc.binding.OwnerReferences)
			if tc.wantNoRead {
				assert.Zero(t, reads, "the Project was read")
			}
		})
	}
}

// splitReaders is a manager whose cache and API reader hold different objects.
type splitReaders struct {
	cached client.Client
	live   client.Reader
}

func (r splitReaders) GetClient() client.Client    { return r.cached }
func (r splitReaders) GetAPIReader() client.Reader { return r.live }

func TestNewPolicyBindingMutator_ReadsTheProjectThroughTheAPIReader(t *testing.T) {
	const name, liveUID, staleUID = "web", "project-uid-live", "project-uid-stale"
	mgr := splitReaders{
		cached: fake.NewClientBuilder().WithScheme(runtimeScheme).WithObjects(projectWithUID(name, staleUID)).Build(),
		live:   fake.NewClientBuilder().WithScheme(runtimeScheme).WithObjects(projectWithUID(name, liveUID)).Build(),
	}
	pb := projectTargetedBinding(name, liveUID)

	require.NoError(t, newPolicyBindingMutator(mgr).Default(orgContextRequest("acme"), pb))

	assert.Equal(t, []metav1.OwnerReference{projectOwnerReference(name, liveUID)}, pb.OwnerReferences)
}
