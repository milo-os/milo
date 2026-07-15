package v1alpha1

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	"go.miloapis.com/milo/pkg/features"
	admissionv1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/component-base/featuregate"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func TestProjectSuspensionMutator_Default(t *testing.T) {
	scheme := runtime.NewScheme()
	utilruntime.Must(resourcemanagerv1alpha1.AddToScheme(scheme))

	project := &resourcemanagerv1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-project",
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(project).Build()

	mutator := &ProjectSuspensionMutator{
		client: fakeClient,
		scheme: scheme,
	}

	ps := &resourcemanagerv1alpha1.ProjectSuspension{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-suspension",
		},
		Spec: resourcemanagerv1alpha1.ProjectSuspensionSpec{
			ProjectRef: resourcemanagerv1alpha1.ProjectReference{
				Name: "test-project",
			},
			Reason:             resourcemanagerv1alpha1.ReasonAbuse,
			ReinstateAuthority: resourcemanagerv1alpha1.AuthorityOperator,
		},
	}

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			UserInfo: authenticationv1.UserInfo{
				Username: "test-operator",
			},
		},
	}
	ctx := admission.NewContextWithRequest(context.Background(), req)

	err := mutator.Default(ctx, ps)
	require.NoError(t, err)

	assert.Equal(t, "test-operator", ps.Spec.RequestedBy)
	assert.Len(t, ps.OwnerReferences, 1)
	assert.Equal(t, "test-project", ps.OwnerReferences[0].Name)
}

func TestProjectSuspensionValidator_ValidateCreate(t *testing.T) {
	// Enable the feature gate for the duration of the test
	gates := map[featuregate.Feature]featuregate.FeatureSpec{
		features.ProjectSuspension: {Default: true, PreRelease: featuregate.Alpha},
	}
	err := utilfeature.DefaultMutableFeatureGate.Add(gates)
	if err != nil {
		// already added is fine
	}
	utilfeature.DefaultMutableFeatureGate.Set(fmt.Sprintf("%s=true", features.ProjectSuspension))
	defer utilfeature.DefaultMutableFeatureGate.Set(fmt.Sprintf("%s=false", features.ProjectSuspension))

	scheme := runtime.NewScheme()
	utilruntime.Must(resourcemanagerv1alpha1.AddToScheme(scheme))

	project := &resourcemanagerv1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-project",
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(project).Build()

	validator := &ProjectSuspensionValidator{
		client:          fakeClient,
		systemNamespace: "milo-system",
	}

	tests := map[string]struct {
		ps          *resourcemanagerv1alpha1.ProjectSuspension
		expectErr   bool
		errContains string
	}{
		"valid suspension": {
			ps: &resourcemanagerv1alpha1.ProjectSuspension{
				Spec: resourcemanagerv1alpha1.ProjectSuspensionSpec{
					ProjectRef: resourcemanagerv1alpha1.ProjectReference{
						Name: "test-project",
					},
					Reason:             resourcemanagerv1alpha1.ReasonAbuse,
					ReinstateAuthority: resourcemanagerv1alpha1.AuthorityOperator,
					RequestedBy:        "operator",
				},
			},
			expectErr: false,
		},

		"non-existent project": {
			ps: &resourcemanagerv1alpha1.ProjectSuspension{
				Spec: resourcemanagerv1alpha1.ProjectSuspensionSpec{
					ProjectRef: resourcemanagerv1alpha1.ProjectReference{
						Name: "non-existent",
					},
					Reason:             resourcemanagerv1alpha1.ReasonAbuse,
					ReinstateAuthority: resourcemanagerv1alpha1.AuthorityOperator,
					RequestedBy:        "operator",
				},
			},
			expectErr:   true,
			errContains: "non-existent",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := validator.ValidateCreate(context.Background(), tc.ps)
			if tc.expectErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tc.errContains)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestProjectSuspensionValidator_ValidateDelete(t *testing.T) {
	scheme := runtime.NewScheme()
	utilruntime.Must(resourcemanagerv1alpha1.AddToScheme(scheme))

	// 1. Setup client with a project present
	project := &resourcemanagerv1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-project",
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(project).Build()

	validator := &ProjectSuspensionValidator{
		client:          fakeClient,
		systemNamespace: "milo-system",
	}

	ps := &resourcemanagerv1alpha1.ProjectSuspension{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-suspension",
		},
		Spec: resourcemanagerv1alpha1.ProjectSuspensionSpec{
			ProjectRef: resourcemanagerv1alpha1.ProjectReference{
				Name: "test-project",
			},
			ReinstateAuthority: resourcemanagerv1alpha1.AuthorityOperator,
		},
	}

	// Case 1: Non-operator tries to delete and project is present
	reqConsumer := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			UserInfo: authenticationv1.UserInfo{
				Username: "test-consumer",
				Groups:   []string{"system:authenticated"},
			},
		},
	}
	ctxConsumer := admission.NewContextWithRequest(context.Background(), reqConsumer)
	_, err := validator.ValidateDelete(ctxConsumer, ps)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "only operators can lift/delete this suspension")

	// Case 2: Operator deletes and project is present
	reqOperator := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			UserInfo: authenticationv1.UserInfo{
				Username: "test-operator",
				Groups:   []string{"system:masters"},
			},
		},
	}
	ctxOperator := admission.NewContextWithRequest(context.Background(), reqOperator)
	_, err = validator.ValidateDelete(ctxOperator, ps)
	assert.NoError(t, err)

	// Case 3: Non-operator deletes but project is not found (garbage collection scenario)
	emptyClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	validatorGC := &ProjectSuspensionValidator{
		client:          emptyClient,
		systemNamespace: "milo-system",
	}
	_, err = validatorGC.ValidateDelete(ctxConsumer, ps)
	assert.NoError(t, err)
}
