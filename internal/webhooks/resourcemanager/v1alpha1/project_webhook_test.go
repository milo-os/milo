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

	// Create test organizations for the fake client
	testOrg := &v1alpha1.Organization{
		ObjectMeta: metav1.ObjectMeta{
			Name: orgName,
			UID:  types.UID("org-123"),
		},
		Spec: v1alpha1.OrganizationSpec{},
	}

	// Create test user for the fake client
	testUser := &iamv1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-user",
			UID:  types.UID("user-123"),
		},
		Spec: iamv1alpha1.UserSpec{
			Email:      "test@example.com",
			GivenName:  "Test",
			FamilyName: "User",
		},
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

	tests := map[string]struct {
		oldProject     *v1alpha1.Project
		newProject     *v1alpha1.Project
		expectError    bool
		errorContains  string
		expectWarnings bool
	}{
		"update allowed when organization label unchanged": {
			oldProject: &v1alpha1.Project{
				ObjectMeta: metav1.ObjectMeta{
					Name: projectName,
					UID:  types.UID(projectUID),
					Labels: map[string]string{
						v1alpha1.OrganizationNameLabel: orgName,
						"other-label":                  "original-value",
					},
				},
				Spec: v1alpha1.ProjectSpec{
					OwnerRef: v1alpha1.OwnerReference{
						Kind: "Organization",
						Name: orgName,
					},
				},
			},
			newProject: &v1alpha1.Project{
				ObjectMeta: metav1.ObjectMeta{
					Name: projectName,
					UID:  types.UID(projectUID),
					Labels: map[string]string{
						v1alpha1.OrganizationNameLabel: orgName,
						"other-label":                  "updated-value",
						"new-label":                    "new-value",
					},
				},
				Spec: v1alpha1.ProjectSpec{
					OwnerRef: v1alpha1.OwnerReference{
						Kind: "Organization",
						Name: orgName,
					},
				},
			},
			expectError: false,
		},
		"update blocked when organization label removed by setting labels to nil": {
			oldProject: &v1alpha1.Project{
				ObjectMeta: metav1.ObjectMeta{
					Name: projectName,
					UID:  types.UID(projectUID),
					Labels: map[string]string{
						v1alpha1.OrganizationNameLabel: orgName,
						"other-label":                  "value",
					},
				},
				Spec: v1alpha1.ProjectSpec{
					OwnerRef: v1alpha1.OwnerReference{
						Kind: "Organization",
						Name: orgName,
					},
				},
			},
			newProject: &v1alpha1.Project{
				ObjectMeta: metav1.ObjectMeta{
					Name:   projectName,
					UID:    types.UID(projectUID),
					Labels: nil, // Labels set to nil
				},
				Spec: v1alpha1.ProjectSpec{
					OwnerRef: v1alpha1.OwnerReference{
						Kind: "Organization",
						Name: orgName,
					},
				},
			},
			expectError:   true,
			errorContains: "organization label cannot be removed",
		},
		"update blocked when organization label specifically removed": {
			oldProject: &v1alpha1.Project{
				ObjectMeta: metav1.ObjectMeta{
					Name: projectName,
					UID:  types.UID(projectUID),
					Labels: map[string]string{
						v1alpha1.OrganizationNameLabel: orgName,
						"other-label":                  "value",
					},
				},
				Spec: v1alpha1.ProjectSpec{
					OwnerRef: v1alpha1.OwnerReference{
						Kind: "Organization",
						Name: orgName,
					},
				},
			},
			newProject: &v1alpha1.Project{
				ObjectMeta: metav1.ObjectMeta{
					Name: projectName,
					UID:  types.UID(projectUID),
					Labels: map[string]string{
						"other-label": "value", // Organization label removed
					},
				},
				Spec: v1alpha1.ProjectSpec{
					OwnerRef: v1alpha1.OwnerReference{
						Kind: "Organization",
						Name: orgName,
					},
				},
			},
			expectError:   true,
			errorContains: "organization label cannot be removed",
		},
		"update blocked when organization label value changed": {
			oldProject: &v1alpha1.Project{
				ObjectMeta: metav1.ObjectMeta{
					Name: projectName,
					UID:  types.UID(projectUID),
					Labels: map[string]string{
						v1alpha1.OrganizationNameLabel: orgName,
						"other-label":                  "value",
					},
				},
				Spec: v1alpha1.ProjectSpec{
					OwnerRef: v1alpha1.OwnerReference{
						Kind: "Organization",
						Name: orgName,
					},
				},
			},
			newProject: &v1alpha1.Project{
				ObjectMeta: metav1.ObjectMeta{
					Name: projectName,
					UID:  types.UID(projectUID),
					Labels: map[string]string{
						v1alpha1.OrganizationNameLabel: "different-org", // Organization label changed
						"other-label":                  "value",
					},
				},
				Spec: v1alpha1.ProjectSpec{
					OwnerRef: v1alpha1.OwnerReference{
						Kind: "Organization",
						Name: orgName,
					},
				},
			},
			expectError:   true,
			errorContains: "organization label cannot be changed",
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
				// Verify it's a field validation error
				var statusErr *errors.StatusError
				assert.ErrorAs(t, err, &statusErr, "error should be a StatusError")
				assert.Equal(t, metav1.StatusReasonInvalid, statusErr.ErrStatus.Reason, "error reason should be Invalid")
			} else {
				assert.NoError(t, err, "expected no validation error")
			}

			if tt.expectWarnings {
				assert.NotEmpty(t, warnings, "expected validation warnings")
			} else {
				assert.Empty(t, warnings, "expected no validation warnings")
			}
		})
	}
}
