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

	t.Cleanup(p.Close)

	for i := 0; i < 100; i++ {
		if p.suspensionCache.HasSynced() {
			break
		}
		time.Sleep(5 * time.Millisecond)
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

	u := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "resourcemanager.miloapis.com/v1alpha1",
			"kind":       "Project",
			"metadata": map[string]interface{}{
				"name": name,
			},
			"status": status,
		},
	}
	u.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "resourcemanager.miloapis.com",
		Version: "v1alpha1",
		Kind:    "Project",
	})
	return u
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

// TestGetSuspensionState_UnknownProject_Allows covers a project absent
// from the cache — whether because it doesn't exist, or because it hasn't
// been observed yet. There is no live API fallback (see GetSuspensionState
// in cache.go): the apiserver's /readyz check (ReadinessCheck in
// register.go) is what guarantees the cache is synced before real traffic
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

func TestValidate_AccessReviewExempt(t *testing.T) {
	p, _ := newTestPlugin(t, newUnstructuredProject("proj-1", true, resourcemanagerv1alpha1.ReasonFraud))

	ctx := milorequest.WithProject(context.Background(), "proj-1")
	sarGVR := schema.GroupVersionResource{Group: "authorization.k8s.io", Version: "v1", Resource: "localsubjectaccessreviews"}
	_, attrs := newAttrs(ctx, admission.Create, nil, sarGVR)

	if err := p.Validate(ctx, attrs, nil); err != nil {
		t.Errorf("Validate() error = %v, want nil for access review requests even when suspended", err)
	}
}

// waitForSuspensionState polls getSuspensionState until it matches wantSuspended
// or the timeout elapses, since watch-event delivery through the fake dynamic
// client is asynchronous with respect to the calling goroutine.
func waitForSuspensionState(t *testing.T, p *Plugin, ctx context.Context, projectID string, wantSuspended bool) *suspensionState {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		state := p.getSuspensionState(ctx, projectID)
		suspended := state != nil && state.Suspended
		if suspended == wantSuspended {
			return state
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for suspended=%v, last state=%+v", wantSuspended, state)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestInformerCache_RealtimeUpdatesNoRequestGetCalls(t *testing.T) {
	p, client := newTestPlugin(t, newUnstructuredProject("proj-1", true, resourcemanagerv1alpha1.ReasonFraud))

	ctx := milorequest.WithProject(context.Background(), "proj-1")

	// GetSuspensionState has no live API fallback at all (see its doc
	// comment) — this reactor is a structural regression guard: it fails
	// the test immediately if that ever changes, rather than merely
	// counting calls.
	client.PrependReactor("get", "projects", func(action clienttesting.Action) (bool, runtime.Object, error) {
		t.Fatalf("GetSuspensionState must never call the API server directly")
		return false, nil, nil
	})

	// 1. Steady-state check: project is suspended in cache.
	state := waitForSuspensionState(t, p, ctx, "proj-1", true)
	if !strings.Contains(uniqueSortedReasons(state.Suspensions)[0], "Fraud") {
		t.Errorf("expected suspension reason to contain %q, got %+v", "Fraud", state.Suspensions)
	}

	// 2. Unsuspend the project via the fake dynamic client, so a real watch
	// event flows through the reflector's Update path into the cache map
	// (rather than poking the map directly, which would not exercise that
	// wiring at all).
	activeProj := newUnstructuredProject("proj-1", false)
	activeProj.SetResourceVersion("999")
	if _, err := client.Resource(projectGVR).Update(context.Background(), activeProj, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("failed to update project via fake client: %v", err)
	}

	// 3. Verify real-time cache update: project is no longer suspended.
	waitForSuspensionState(t, p, ctx, "proj-1", false)
}
