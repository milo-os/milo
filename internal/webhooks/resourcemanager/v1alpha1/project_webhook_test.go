package v1alpha1

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	"go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var runtimeScheme = runtime.NewScheme()

func init() {
	utilruntime.Must(v1alpha1.AddToScheme(runtimeScheme))
	utilruntime.Must(iamv1alpha1.AddToScheme(runtimeScheme))
}

func TestProjectValidator_ValidateUpdate(t *testing.T) {
	const (
		systemNamespace      = "milo-system"
		projectOwnerRoleName = "project-owner"
		orgName              = "test-org"
		projectName          = "test-project"
		projectUID           = "project-123"
	)

	testOrg := &v1alpha1.Organization{
		ObjectMeta: metav1.ObjectMeta{Name: orgName, UID: types.UID("org-123")},
	}
	testUser := &iamv1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{Name: "test-user", UID: types.UID("user-123")},
		Spec:       iamv1alpha1.UserSpec{Email: "test@example.com", GivenName: "Test", FamilyName: "User"},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(runtimeScheme).
		WithObjects(testOrg, testUser).
		Build()

	validator := &ProjectValidator{
		Client:               fakeClient,
		SystemNamespace:      systemNamespace,
		ProjectOwnerRoleName: projectOwnerRoleName,
	}

	// project builds a Project with the given labels; it owns the fields ValidateUpdate reads.
	project := func(labels map[string]string) *v1alpha1.Project {
		return &v1alpha1.Project{
			ObjectMeta: metav1.ObjectMeta{Name: projectName, UID: types.UID(projectUID), Labels: labels},
			Spec:       v1alpha1.ProjectSpec{OwnerRef: v1alpha1.OwnerReference{Kind: "Organization", Name: orgName}},
		}
	}
	// deleting marks a Project for deletion with the project-controller finalizer, the state whose
	// finalizer removal a label-less legacy Project used to have refused.
	deleting := func(p *v1alpha1.Project) *v1alpha1.Project {
		p.DeletionTimestamp = &metav1.Time{Time: metav1.Now().Time}
		p.Finalizers = []string{"resourcemanager.miloapis.com/project-controller"}
		return p
	}

	withOrg := func() map[string]string {
		return map[string]string{v1alpha1.OrganizationNameLabel: orgName, "other-label": "value"}
	}

	tests := map[string]struct {
		oldProject    *v1alpha1.Project
		newProject    *v1alpha1.Project
		expectError   bool
		errorContains string
	}{
		"label unchanged, other labels edited": {
			oldProject:  project(map[string]string{v1alpha1.OrganizationNameLabel: orgName, "other-label": "original-value"}),
			newProject:  project(map[string]string{v1alpha1.OrganizationNameLabel: orgName, "other-label": "updated-value", "new-label": "new-value"}),
			expectError: false,
		},
		"label removed by setting labels to nil": {
			oldProject:    project(withOrg()),
			newProject:    project(nil),
			expectError:   true,
			errorContains: "field is immutable",
		},
		"label removed, other labels kept": {
			oldProject:    project(withOrg()),
			newProject:    project(map[string]string{"other-label": "value"}),
			expectError:   true,
			errorContains: "field is immutable",
		},
		"label value changed": {
			oldProject:    project(withOrg()),
			newProject:    project(map[string]string{v1alpha1.OrganizationNameLabel: "different-org", "other-label": "value"}),
			expectError:   true,
			errorContains: "field is immutable",
		},
		// A Project created before the organization label existed carries none. An update that
		// leaves it absent (such as the finalizer removal of a deleting Project) must pass: the
		// label is unchanged.
		"label absent on both old and new": {
			oldProject:  project(map[string]string{"other-label": "value"}),
			newProject:  deleting(project(map[string]string{"other-label": "value"})),
			expectError: false,
		},
		// Setting a value where there was none attaches the Project to an organization, an
		// ownership change that is not an in-place update. The immutable rule refuses it.
		"label added to a label-less project": {
			oldProject:    project(map[string]string{"other-label": "value"}),
			newProject:    project(withOrg()),
			expectError:   true,
			errorContains: "field is immutable",
		},
		// A missing label counts as an empty value, so adding the label with an empty value is
		// not a change. This pins that boundary of ValidateImmutableField; a real org value is
		// refused by the case above.
		"label added with an empty value is unchanged": {
			oldProject:  project(map[string]string{"other-label": "value"}),
			newProject:  project(map[string]string{v1alpha1.OrganizationNameLabel: "", "other-label": "value"}),
			expectError: false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			warnings, err := validator.ValidateUpdate(ctx, tt.oldProject, tt.newProject)

			if tt.expectError {
				assert.Error(t, err, "expected validation error")
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains, "error message should contain expected text")
				}
				var statusErr *errors.StatusError
				assert.ErrorAs(t, err, &statusErr, "error should be a StatusError")
				assert.Equal(t, metav1.StatusReasonInvalid, statusErr.ErrStatus.Reason, "error reason should be Invalid")
			} else {
				assert.NoError(t, err, "expected no validation error")
			}

			assert.Empty(t, warnings, "expected no validation warnings")
		})
	}
}
