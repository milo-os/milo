package projectsuspension

import (
	"context"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"

	"go.miloapis.com/milo/internal/apiserver/projectstate/projectstatetest"
	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	milorequest "go.miloapis.com/milo/pkg/request"
)

var namespaceGVK = schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Namespace"}
var namespaceGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"}

func newTestPlugin(t *testing.T, objects ...runtime.Object) (*Plugin, *fake.FakeDynamicClient) {
	t.Helper()

	states, client := projectstatetest.NewCache(t, objects...)

	p, err := NewPlugin()
	if err != nil {
		t.Fatalf("NewPlugin() error = %v", err)
	}
	p.SetProjectStates(states)
	if err := p.ValidateInitialization(); err != nil {
		t.Fatalf("ValidateInitialization() error = %v", err)
	}

	return p, client
}

var newUnstructuredProject = projectstatetest.NewProject

func newAttrs(ctx context.Context, op admission.Operation, userInfo user.Info, resource schema.GroupVersionResource) (context.Context, admission.Attributes) {
	if userInfo == nil {
		userInfo = &user.DefaultInfo{Name: "test-user"}
	}
	attrs := admission.NewAttributesRecord(
		nil, nil,
		namespaceGVK,
		"", "test-object",
		resource,
		"",
		op,
		nil,
		false,
		userInfo,
	)
	return ctx, attrs
}

func TestValidate_NotSuspended_Allows(t *testing.T) {
	p, _ := newTestPlugin(t, newUnstructuredProject("proj-1", false))

	ctx := milorequest.WithProject(context.Background(), "proj-1")

	for _, op := range []admission.Operation{admission.Create, admission.Update} {
		_, attrs := newAttrs(ctx, op, nil, namespaceGVR)
		if err := p.Validate(ctx, attrs, nil); err != nil {
			t.Errorf("Validate() for op %s error = %v, want nil", op, err)
		}
	}
}

func TestValidate_SuspendedSingleReason_Denies(t *testing.T) {
	p, _ := newTestPlugin(t, newUnstructuredProject("proj-1", true, resourcemanagerv1alpha1.ReasonFraud))

	ctx := milorequest.WithProject(context.Background(), "proj-1")
	_, attrs := newAttrs(ctx, admission.Create, nil, namespaceGVR)

	err := p.Validate(ctx, attrs, nil)
	if err == nil {
		t.Fatalf("Validate() error = nil, want forbidden error")
	}
	if !apierrors.IsForbidden(err) {
		t.Fatalf("apierrors.IsForbidden(err) = false, want true; err = %v", err)
	}

	statusErr, ok := err.(*apierrors.StatusError)
	if !ok {
		t.Fatalf("err is not *apierrors.StatusError: %T", err)
	}

	found := false
	for _, cause := range statusErr.ErrStatus.Details.Causes {
		if cause.Type == resourcemanagerv1alpha1.ProjectSuspendedCause {
			found = true
			if !strings.Contains(cause.Message, "Fraud") {
				t.Errorf("cause message = %q, want it to contain %q", cause.Message, "Fraud")
			}
		}
	}
	if !found {
		t.Errorf("Details.Causes = %+v, want a cause with Type %q", statusErr.ErrStatus.Details.Causes, resourcemanagerv1alpha1.ProjectSuspendedCause)
	}
	if !strings.Contains(statusErr.ErrStatus.Message, "Fraud") {
		t.Errorf("error message = %q, want it to contain %q", statusErr.ErrStatus.Message, "Fraud")
	}
}

func TestValidate_SuspendedUpdate_Denies(t *testing.T) {
	p, _ := newTestPlugin(t, newUnstructuredProject("proj-1", true, resourcemanagerv1alpha1.ReasonFraud))

	ctx := milorequest.WithProject(context.Background(), "proj-1")
	_, attrs := newAttrs(ctx, admission.Update, nil, namespaceGVR)

	err := p.Validate(ctx, attrs, nil)
	if !apierrors.IsForbidden(err) {
		t.Fatalf("Validate() for Update error = %v, want forbidden error", err)
	}
}

func TestValidate_SuspendedStatusSubresourceUpdate_Denies(t *testing.T) {
	p, _ := newTestPlugin(t, newUnstructuredProject("proj-1", true, resourcemanagerv1alpha1.ReasonFraud))

	ctx := milorequest.WithProject(context.Background(), "proj-1")
	attrs := admission.NewAttributesRecord(
		nil, nil,
		namespaceGVK,
		"", "test-object",
		namespaceGVR,
		"status",
		admission.Update,
		nil,
		false,
		&user.DefaultInfo{Name: "test-user"},
	)

	err := p.Validate(ctx, attrs, nil)
	if !apierrors.IsForbidden(err) {
		t.Fatalf("Validate() for status subresource Update error = %v, want forbidden error", err)
	}
}

func TestValidate_SuspendedFinalizerRemovalUpdate_Allows(t *testing.T) {
	p, _ := newTestPlugin(t, newUnstructuredProject("proj-1", true, resourcemanagerv1alpha1.ReasonFraud))

	ctx := milorequest.WithProject(context.Background(), "proj-1")

	oldObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Namespace",
			"metadata": map[string]interface{}{
				"name":              "terminating-ns",
				"deletionTimestamp": metav1.Now().Format(time.RFC3339),
				"finalizers":        []interface{}{"example.com/finalizer"},
			},
		},
	}
	newObj := oldObj.DeepCopy()
	newObj.SetFinalizers(nil)

	attrs := admission.NewAttributesRecord(
		newObj, oldObj,
		namespaceGVK,
		"", "terminating-ns",
		namespaceGVR,
		"",
		admission.Update,
		nil,
		false,
		&user.DefaultInfo{Name: "controller"},
	)

	if err := p.Validate(ctx, attrs, nil); err != nil {
		t.Errorf("Validate() for finalizer-removal update on an already-terminating object error = %v, want nil", err)
	}
}

func TestValidate_SuspendedUpdateOnNonTerminatingObject_StillDenies(t *testing.T) {
	p, _ := newTestPlugin(t, newUnstructuredProject("proj-1", true, resourcemanagerv1alpha1.ReasonFraud))

	ctx := milorequest.WithProject(context.Background(), "proj-1")

	oldObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Namespace",
			"metadata": map[string]interface{}{
				"name": "live-ns",
				// No deletionTimestamp: this object is not terminating.
			},
		},
	}

	attrs := admission.NewAttributesRecord(
		oldObj.DeepCopy(), oldObj,
		namespaceGVK,
		"", "live-ns",
		namespaceGVR,
		"",
		admission.Update,
		nil,
		false,
		&user.DefaultInfo{Name: "test-user"},
	)

	err := p.Validate(ctx, attrs, nil)
	if !apierrors.IsForbidden(err) {
		t.Fatalf("Validate() for update on a non-terminating object error = %v, want forbidden error", err)
	}
}

func TestValidate_SuspendedMultipleReasons_SortedDeterministicMessage(t *testing.T) {
	p, _ := newTestPlugin(t, newUnstructuredProject(
		"proj-1", true,
		resourcemanagerv1alpha1.ReasonFraud,
		resourcemanagerv1alpha1.ReasonBilling,
	))

	ctx := milorequest.WithProject(context.Background(), "proj-1")
	_, attrs := newAttrs(ctx, admission.Create, nil, namespaceGVR)

	err := p.Validate(ctx, attrs, nil)
	if err == nil {
		t.Fatalf("Validate() error = nil, want forbidden error")
	}

	statusErr, ok := err.(*apierrors.StatusError)
	if !ok {
		t.Fatalf("err is not *apierrors.StatusError: %T", err)
	}

	// Billing sorts before Fraud lexicographically.
	wantSubstring := "Billing, Fraud"
	if !strings.Contains(statusErr.ErrStatus.Message, wantSubstring) {
		t.Errorf("error message = %q, want it to contain %q (sorted, deterministic)", statusErr.ErrStatus.Message, wantSubstring)
	}
}

func TestHandler_DeleteNeverRoutedToValidate(t *testing.T) {
	p, err := NewPlugin()
	if err != nil {
		t.Fatalf("NewPlugin() error = %v", err)
	}

	if p.Handles(admission.Delete) {
		t.Errorf("Handles(Delete) = true, want false: Delete requests must never reach Validate")
	}
	if !p.Handles(admission.Create) {
		t.Errorf("Handles(Create) = false, want true")
	}
	if !p.Handles(admission.Update) {
		t.Errorf("Handles(Update) = false, want true")
	}
}

func TestValidate_NoProjectInContext_NoOpNoClientCall(t *testing.T) {
	p, client := newTestPlugin(t, newUnstructuredProject("proj-1", true, resourcemanagerv1alpha1.ReasonFraud))

	client.PrependReactor("get", "projects", func(action clienttesting.Action) (bool, runtime.Object, error) {
		t.Fatalf("dynamic client Get should not be called when no project is in context")
		return false, nil, nil
	})

	ctx := context.Background() // no project ID set
	_, attrs := newAttrs(ctx, admission.Create, nil, namespaceGVR)

	if err := p.Validate(ctx, attrs, nil); err != nil {
		t.Errorf("Validate() error = %v, want nil", err)
	}
}

// TestGetSuspensionState_UnknownProject_Allows covers a project absent
// from the cache — whether because it doesn't exist, or because it hasn't
// been observed yet. There is no live API fallback (see getSuspensionState):
// the apiserver's /readyz check (projectstate.Cache.ReadinessCheck) is what
// guarantees the cache is synced before real traffic
// arrives, not a per-request escape hatch here. So absence from the cache
// always means "allow" — there is no separate lookup-error path to test
// anymore.
func TestGetSuspensionState_UnknownProject_Allows(t *testing.T) {
	p, _ := newTestPlugin(t) // no projects registered

	ctx := milorequest.WithProject(context.Background(), "missing-project")
	_, attrs := newAttrs(ctx, admission.Create, nil, namespaceGVR)

	if err := p.Validate(ctx, attrs, nil); err != nil {
		t.Errorf("Validate() error = %v, want nil for a project absent from the cache", err)
	}
}

func TestValidate_SystemMastersExempt(t *testing.T) {
	p, _ := newTestPlugin(t, newUnstructuredProject("proj-1", true, resourcemanagerv1alpha1.ReasonFraud))

	ctx := milorequest.WithProject(context.Background(), "proj-1")
	userInfo := &user.DefaultInfo{Name: "admin", Groups: []string{user.SystemPrivilegedGroup}}
	_, attrs := newAttrs(ctx, admission.Create, userInfo, namespaceGVR)

	if err := p.Validate(ctx, attrs, nil); err != nil {
		t.Errorf("Validate() error = %v, want nil for system:masters even when suspended", err)
	}
}

// TestValidate_AccessReviewExempt covers every authorization.k8s.io review
// resource, including selfsubjectaccessreviews — clients (e.g. cloud-portal)
// create one of these to decide whether to even attempt a read (a "can I
// view this?" check before rendering a detail page). Denying the review
// itself while suspended made read access look revoked, even though reads
// are always supposed to be allowed regardless of suspension; this is a
// regression test for that bug.
func TestValidate_AccessReviewExempt(t *testing.T) {
	for _, resource := range []string{
		"subjectaccessreviews",
		"localsubjectaccessreviews",
		"selfsubjectaccessreviews",
		"selfsubjectrulesreviews",
	} {
		t.Run(resource, func(t *testing.T) {
			p, _ := newTestPlugin(t, newUnstructuredProject("proj-1", true, resourcemanagerv1alpha1.ReasonFraud))

			ctx := milorequest.WithProject(context.Background(), "proj-1")
			sarGVR := schema.GroupVersionResource{Group: "authorization.k8s.io", Version: "v1", Resource: resource}
			_, attrs := newAttrs(ctx, admission.Create, nil, sarGVR)

			if err := p.Validate(ctx, attrs, nil); err != nil {
				t.Errorf("Validate() error = %v, want nil for %s even when suspended", err, resource)
			}
		})
	}
}
