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

	resourcemanagerv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	milorequest "go.miloapis.com/milo/pkg/request"
)

var namespaceGVK = schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Namespace"}
var namespaceGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"}

func newTestPlugin(t *testing.T, objects ...runtime.Object) (*Plugin, *fake.FakeDynamicClient) {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := resourcemanagerv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	client := fake.NewSimpleDynamicClient(scheme, objects...)

	p, err := NewPlugin()
	if err != nil {
		t.Fatalf("NewPlugin() error = %v", err)
	}
	p.SetDynamicClient(client)
	if err := p.ValidateInitialization(); err != nil {
		t.Fatalf("ValidateInitialization() error = %v", err)
	}

	return p, client
}

func newUnstructuredProject(name string, suspended bool, reasons ...resourcemanagerv1alpha1.ProjectSuspensionReason) *unstructured.Unstructured {
	status := map[string]interface{}{}

	if suspended {
		status["conditions"] = []interface{}{
			map[string]interface{}{
				"type":   resourcemanagerv1alpha1.ProjectSuspended,
				"status": string(metav1.ConditionTrue),
				"reason": resourcemanagerv1alpha1.ProjectSuspendedReason,
			},
		}

		suspensions := make([]interface{}, 0, len(reasons))
		for _, r := range reasons {
			suspensions = append(suspensions, map[string]interface{}{
				"reason":             string(r),
				"suspendedAt":        metav1.Now().Format(time.RFC3339),
				"reinstateAuthority": string(resourcemanagerv1alpha1.AuthorityOperator),
			})
		}
		status["suspensions"] = suspensions
	} else {
		status["conditions"] = []interface{}{
			map[string]interface{}{
				"type":   resourcemanagerv1alpha1.ProjectSuspended,
				"status": string(metav1.ConditionFalse),
				"reason": resourcemanagerv1alpha1.ProjectActiveReason,
			},
		}
	}

	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "resourcemanager.miloapis.com/v1alpha1",
			"kind":       "Project",
			"metadata": map[string]interface{}{
				"name": name,
			},
			"status": status,
		},
	}
}

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

func TestGetSuspensionState_ProjectNotFound_FailsOpen(t *testing.T) {
	p, _ := newTestPlugin(t) // no projects registered

	ctx := milorequest.WithProject(context.Background(), "missing-project")
	_, attrs := newAttrs(ctx, admission.Create, nil, namespaceGVR)

	if err := p.Validate(ctx, attrs, nil); err != nil {
		t.Errorf("Validate() error = %v, want nil (fail open on 404)", err)
	}
}

func TestGetSuspensionState_NonNotFoundError_FailsOpen(t *testing.T) {
	p, client := newTestPlugin(t, newUnstructuredProject("proj-1", true, resourcemanagerv1alpha1.ReasonFraud))

	client.PrependReactor("get", "projects", func(action clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewInternalError(errNonNotFound)
	})

	ctx := milorequest.WithProject(context.Background(), "proj-1")
	_, attrs := newAttrs(ctx, admission.Create, nil, namespaceGVR)

	if err := p.Validate(ctx, attrs, nil); err != nil {
		t.Errorf("Validate() error = %v, want nil (fail open on non-404 error)", err)
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

func TestValidate_AccessReviewExempt(t *testing.T) {
	p, _ := newTestPlugin(t, newUnstructuredProject("proj-1", true, resourcemanagerv1alpha1.ReasonFraud))

	ctx := milorequest.WithProject(context.Background(), "proj-1")
	sarGVR := schema.GroupVersionResource{Group: "authorization.k8s.io", Version: "v1", Resource: "localsubjectaccessreviews"}
	_, attrs := newAttrs(ctx, admission.Create, nil, sarGVR)

	if err := p.Validate(ctx, attrs, nil); err != nil {
		t.Errorf("Validate() error = %v, want nil for access review requests even when suspended", err)
	}
}

func TestGetSuspensionState_CacheHitAvoidsSecondGetWithinTTL(t *testing.T) {
	p, client := newTestPlugin(t, newUnstructuredProject("proj-1", true, resourcemanagerv1alpha1.ReasonFraud))
	p.cacheTTL = 20 * time.Millisecond

	var getCalls int
	client.PrependReactor("get", "projects", func(action clienttesting.Action) (bool, runtime.Object, error) {
		getCalls++
		return false, nil, nil // let default reactor chain handle it
	})

	ctx := milorequest.WithProject(context.Background(), "proj-1")

	if _, err := p.getSuspensionState(ctx, "proj-1"); err != nil {
		t.Fatalf("getSuspensionState() error = %v", err)
	}
	if _, err := p.getSuspensionState(ctx, "proj-1"); err != nil {
		t.Fatalf("getSuspensionState() error = %v", err)
	}
	if getCalls != 1 {
		t.Errorf("getCalls = %d, want 1 (second call should hit cache)", getCalls)
	}

	time.Sleep(p.cacheTTL + 20*time.Millisecond)

	if _, err := p.getSuspensionState(ctx, "proj-1"); err != nil {
		t.Fatalf("getSuspensionState() error = %v", err)
	}
	if getCalls != 2 {
		t.Errorf("getCalls = %d, want 2 (call after TTL expiry should hit client again)", getCalls)
	}
}

var errNonNotFound = &testError{"transient lookup failure"}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }
