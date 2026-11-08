package resourcemanager

import (
	"context"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	resourcemanagerv1alpha "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
)

func TestWriteCleanupConditionAfterAConflict(t *testing.T) {
	awaiting := metav1.Condition{
		Type:    resourcemanagerv1alpha.ProjectResourceCleanup,
		Status:  metav1.ConditionTrue,
		Reason:  resourcemanagerv1alpha.ProjectCleanupAwaitingCompletionReason,
		Message: "Waiting for project resources to be removed",
	}
	conflict := apierrors.NewConflict(schema.GroupResource{Group: "resourcemanager.miloapis.com", Resource: "projects"}, "p", nil)

	for name, tc := range map[string]struct {
		conflicts   int
		wantStop    bool
		wantRequeue bool
		wantWritten bool
	}{
		"one lost race is retried and written": {conflicts: 1, wantWritten: true},
		"a write that keeps losing requeues":   {conflicts: 100, wantStop: true, wantRequeue: true},
	} {
		t.Run(name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := resourcemanagerv1alpha.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			project := &resourcemanagerv1alpha.Project{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "organization-o"}}
			remaining := tc.conflicts
			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(project).WithStatusSubresource(project).
				WithInterceptorFuncs(interceptor.Funcs{
					SubResourceUpdate: func(ctx context.Context, c client.Client, sub string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
						if remaining > 0 {
							remaining--
							return conflict
						}
						return c.SubResource(sub).Update(ctx, obj, opts...)
					},
				}).Build()

			r := &ProjectController{ControlPlaneClient: cl}
			stale := project.DeepCopy()
			stop, result, err := r.writeCleanupCondition(context.Background(), stale, awaiting, "update awaiting status")
			if err != nil {
				t.Fatalf("a lost race is not a reconcile error: %v", err)
			}
			if stop != tc.wantStop || (result.RequeueAfter > 0) != tc.wantRequeue {
				t.Fatalf("stop=%v result=%+v, want stop=%v requeue=%v", stop, result, tc.wantStop, tc.wantRequeue)
			}

			live := &resourcemanagerv1alpha.Project{}
			if err := cl.Get(context.Background(), client.ObjectKeyFromObject(project), live); err != nil {
				t.Fatal(err)
			}
			written := apimeta.IsStatusConditionTrue(live.Status.Conditions, resourcemanagerv1alpha.ProjectResourceCleanup)
			if written != tc.wantWritten {
				t.Fatalf("condition written = %v, want %v", written, tc.wantWritten)
			}
		})
	}
}
