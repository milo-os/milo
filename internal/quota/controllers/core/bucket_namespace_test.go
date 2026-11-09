package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	quotav1alpha1 "go.miloapis.com/milo/pkg/apis/quota/v1alpha1"
)

func TestAllowanceBucketIsNotCreatedInADepartingNamespace(t *testing.T) {
	buckets := schema.GroupResource{Group: quotav1alpha1.GroupVersion.Group, Resource: "allowancebuckets"}
	terminating := apierrors.NewForbidden(buckets, "b", nil)
	terminating.ErrStatus.Details.Causes = []metav1.StatusCause{{Type: corev1.NamespaceTerminatingCause}}

	for name, createErr := range map[string]error{
		"namespace terminating": terminating,
		"namespace gone":        apierrors.NewNotFound(schema.GroupResource{Resource: "namespaces"}, "organization-o"),
	} {
		t.Run(name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			require.NoError(t, quotav1alpha1.AddToScheme(scheme))
			consumer := quotav1alpha1.ConsumerRef{APIGroup: "resourcemanager.miloapis.com", Kind: "Organization", Name: "o"}
			claim := &quotav1alpha1.ResourceClaim{
				ObjectMeta: metav1.ObjectMeta{Name: "c", Namespace: "organization-o"},
				Spec: quotav1alpha1.ResourceClaimSpec{
					ConsumerRef: consumer,
					Requests:    []quotav1alpha1.ResourceRequest{{ResourceType: "example.com/things", Amount: 1}},
				},
			}
			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(claim).
				WithInterceptorFuncs(interceptor.Funcs{
					Create: func(context.Context, client.WithWatch, client.Object, ...client.CreateOption) error {
						return createErr
					},
				}).Build()

			r := &AllowanceBucketController{}
			key := types.NamespacedName{Namespace: "organization-o", Name: generateAllowanceBucketName("example.com/things", consumer)}
			require.NoError(t, r.ensureBucketFromClaims(context.Background(), cl, key))
		})
	}

	t.Run("any other refusal is still an error", func(t *testing.T) {
		require.False(t, namespaceGoing(apierrors.NewForbidden(buckets, "b", nil)))
	})
}
