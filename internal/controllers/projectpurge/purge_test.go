package projectpurge

import (
	"context"
	"errors"
	"strings"
	"syscall"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

var configMapGVR = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}

func testDiscovery() func() ([]*metav1.APIResourceList, error) {
	return func() ([]*metav1.APIResourceList, error) {
		return []*metav1.APIResourceList{{
			GroupVersion: "v1",
			APIResources: []metav1.APIResource{
				{Name: "configmaps", Namespaced: true, Kind: "ConfigMap", Verbs: []string{"list", "deletecollection"}},
				{Name: "namespaces", Namespaced: false, Kind: "Namespace", Verbs: []string{"list", "deletecollection"}},
			},
		}}, nil
	}
}

func newDynamicClient(objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		scheme,
		map[schema.GroupVersionResource]string{configMapGVR: "ConfigMapList"},
		objects...,
	)
}

func configMap(namespace, name string, finalizers ...string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion("v1")
	obj.SetKind("ConfigMap")
	obj.SetNamespace(namespace)
	obj.SetName(name)
	if len(finalizers) > 0 {
		obj.SetFinalizers(finalizers)
	}
	return obj
}

func namespace(name string, conditions ...corev1.NamespaceCondition) *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status:     corev1.NamespaceStatus{Conditions: conditions},
	}
}

// terminating is a namespace that carries a deletion mark, as the namespace
// controller sees it while it drains the namespace.
func terminating(name string, conditions ...corev1.NamespaceCondition) *corev1.Namespace {
	ns := namespace(name, conditions...)
	now := metav1.Now()
	ns.DeletionTimestamp = &now
	ns.Finalizers = []string{"kubernetes"}
	ns.Status.Phase = corev1.NamespaceTerminating
	return ns
}

func TestPurgeStatus_CompleteWhenOnlyEmptyProtectedNamespacesRemain(t *testing.T) {
	core := fake.NewSimpleClientset(namespace("default"))
	status, err := purgeStatus(context.Background(), core, newDynamicClient(), testDiscovery())
	if err != nil {
		t.Fatalf("purgeStatus: %v", err)
	}
	if !status.Complete {
		t.Fatalf("expected complete, got blockers: %v", status.Blockers)
	}
}

func TestPurgeStatus_NamespaceStillTerminatingBlocks(t *testing.T) {
	core := fake.NewSimpleClientset(
		namespace("default"),
		terminating("demo", corev1.NamespaceCondition{
			Type:    corev1.NamespaceFinalizersRemaining,
			Status:  corev1.ConditionTrue,
			Message: "Some content in the namespace has finalizers remaining: example.com/hold in 1 resource instances",
		}),
	)

	status, err := purgeStatus(context.Background(), core, newDynamicClient(), testDiscovery())
	if err != nil {
		t.Fatalf("purgeStatus: %v", err)
	}
	if status.Complete {
		t.Fatal("expected the remaining namespace to block completion")
	}
	if len(status.Blockers) != 1 || status.Blockers[0].Namespace != "demo" {
		t.Fatalf("expected a blocker for namespace demo, got %v", status.Blockers)
	}
	if !strings.Contains(status.Message(), "example.com/hold") {
		t.Fatalf("expected the message to name the finalizer, got %q", status.Message())
	}
}

func TestPurgeStatus_ContentInProtectedNamespaceBlocks(t *testing.T) {
	core := fake.NewSimpleClientset(namespace("default"))
	dyn := newDynamicClient(configMap("default", "gateway-anchor", "networking.datumapis.com/gateway"))

	status, err := purgeStatus(context.Background(), core, dyn, testDiscovery())
	if err != nil {
		t.Fatalf("purgeStatus: %v", err)
	}
	if status.Complete {
		t.Fatal("expected content in the default namespace to block completion")
	}
	if len(status.Blockers) != 1 {
		t.Fatalf("expected one blocker, got %v", status.Blockers)
	}
	blocker := status.Blockers[0]
	if blocker.Namespace != "default" || blocker.Resource != "configmaps" || blocker.Name != "gateway-anchor" {
		t.Fatalf("unexpected blocker %+v", blocker)
	}
	msg := status.Message()
	if !strings.Contains(msg, "default/gateway-anchor") || !strings.Contains(msg, "networking.datumapis.com/gateway") {
		t.Fatalf("expected the message to name the object and its finalizer, got %q", msg)
	}
}

func TestPurgeStatus_ServerGoneIsComplete(t *testing.T) {
	core := fake.NewSimpleClientset()
	core.PrependReactor("list", "namespaces", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, syscall.ECONNREFUSED
	})

	status, err := purgeStatus(context.Background(), core, newDynamicClient(), testDiscovery())
	if err != nil {
		t.Fatalf("purgeStatus: %v", err)
	}
	if !status.Complete {
		t.Fatal("expected a project whose API server is gone to be complete")
	}
}

func TestPurgeStatus_TransientErrorIsReturned(t *testing.T) {
	core := fake.NewSimpleClientset()
	core.PrependReactor("list", "namespaces", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("etcdserver: request timed out")
	})

	if _, err := purgeStatus(context.Background(), core, newDynamicClient(), testDiscovery()); err == nil {
		t.Fatal("expected a transient error to be returned so the controller retries")
	}
}

func TestStatusMessage_TruncatesLongBlockerLists(t *testing.T) {
	var status Status
	for i := 0; i < maxReportedBlockers+3; i++ {
		status.Blockers = append(status.Blockers, Blocker{Namespace: "demo"})
	}
	if msg := status.Message(); !strings.Contains(msg, "and 3 more") {
		t.Fatalf("expected the message to be truncated, got %q", msg)
	}
}

func TestPurgeStatus_UnmarkedNamespaceIsNotYetDeleted(t *testing.T) {
	core := fake.NewSimpleClientset(namespace("default"), namespace("late"))

	status, err := purgeStatus(context.Background(), core, newDynamicClient(), testDiscovery())
	if err != nil {
		t.Fatalf("purgeStatus: %v", err)
	}
	if got := status.Unmarked(); len(got) != 1 || got[0] != "late" {
		t.Fatalf("expected the namespace without a deletion mark to be unmarked, got %v", got)
	}
	if msg := status.Message(); !strings.Contains(msg, `namespace "late" is not yet deleted`) {
		t.Fatalf("expected the message to say the namespace is not yet deleted, got %q", msg)
	}
}

func TestPurgeStatus_MarkedNamespaceIsStillTerminating(t *testing.T) {
	core := fake.NewSimpleClientset(namespace("default"), terminating("demo"))

	status, err := purgeStatus(context.Background(), core, newDynamicClient(), testDiscovery())
	if err != nil {
		t.Fatalf("purgeStatus: %v", err)
	}
	if got := status.Unmarked(); len(got) != 0 {
		t.Fatalf("expected a namespace with a deletion mark not to be unmarked, got %v", got)
	}
	if msg := status.Message(); !strings.Contains(msg, `namespace "demo" is still terminating`) {
		t.Fatalf("expected the message to say the namespace is still terminating, got %q", msg)
	}
}

func TestDeleteNamespaces_DeletesTheNamedNamespacesButNeverAProtectedOne(t *testing.T) {
	core := fake.NewSimpleClientset(namespace("default"), namespace("late"))

	refused := deleteNamespaces(context.Background(), core, []string{"late", "default"}, 2)
	if len(refused) != 0 {
		t.Fatalf("expected no refusal, got %v", refused)
	}
	var deleted []string
	for _, a := range core.Actions() {
		if a.GetVerb() == "delete" && a.GetResource().Resource == "namespaces" {
			deleted = append(deleted, a.(k8stesting.DeleteAction).GetName())
		}
	}
	if len(deleted) != 1 || deleted[0] != "late" {
		t.Fatalf("expected exactly one delete, of late, got %v", deleted)
	}
}

// The second delete of a namespace that is already terminating answers with the
// namespace and no error at the pinned Kubernetes (v1.35.0
// pkg/registry/core/namespace/storage/storage.go L250-L252), and a namespace
// already gone answers NotFound. Neither is a refusal.
func TestDeleteNamespaces_GoneOrAlreadyMarkedIsNoError(t *testing.T) {
	core := fake.NewSimpleClientset()
	core.PrependReactor("delete", "namespaces", func(a k8stesting.Action) (bool, runtime.Object, error) {
		switch a.(k8stesting.DeleteAction).GetName() {
		case "gone":
			return true, nil, apierrors.NewNotFound(schema.GroupResource{Resource: "namespaces"}, "gone")
		default:
			return true, terminating(a.(k8stesting.DeleteAction).GetName()), nil
		}
	})

	if refused := deleteNamespaces(context.Background(), core, []string{"gone", "draining"}, 2); len(refused) != 0 {
		t.Fatalf("expected no refusal, got %v", refused)
	}
}

func TestDeleteNamespaces_ReturnsEachRefusal(t *testing.T) {
	core := fake.NewSimpleClientset(namespace("late"))
	core.PrependReactor("delete", "namespaces", func(a k8stesting.Action) (bool, runtime.Object, error) {
		switch a.(k8stesting.DeleteAction).GetName() {
		case "reserved":
			return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "namespaces"}, "reserved", errors.New("held by a reservation"))
		case "flaky":
			return true, nil, apierrors.NewServiceUnavailable("etcd is not ready")
		}
		return false, nil, nil
	})

	refused := deleteNamespaces(context.Background(), core, []string{"late", "reserved", "flaky"}, 2)
	if len(refused) != 2 || refused["reserved"] == nil || refused["flaky"] == nil {
		t.Fatalf("expected the reserved and the flaky deletes to be refused, got %v", refused)
	}
	if _, err := core.CoreV1().Namespaces().Get(context.Background(), "late", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected late to be deleted beside the refusals, got %v", err)
	}
}

func TestStatus_NoteDeletesMarksADeletedNamespaceAndNamesARefusal(t *testing.T) {
	status := Status{Blockers: []Blocker{{Namespace: "late"}, {Namespace: "reserved"}}}
	status.NoteDeletes([]string{"late", "reserved"}, map[string]error{"reserved": errors.New("held by a reservation")})

	if got := status.Unmarked(); len(got) != 1 || got[0] != "reserved" {
		t.Fatalf("expected only the refused namespace to stay unmarked, got %v", got)
	}
	msg := status.Message()
	if !strings.Contains(msg, `namespace "late" is still terminating`) {
		t.Fatalf("expected a deleted namespace to read as still terminating, got %q", msg)
	}
	if !strings.Contains(msg, `namespace "reserved" is not yet deleted (delete refused: held by a reservation)`) {
		t.Fatalf("expected the refused namespace to name its refusal, got %q", msg)
	}
}

var entitlementGVR = schema.GroupVersionResource{Group: "services.miloapis.com", Version: "v1alpha1", Resource: "serviceentitlements"}

func clusterDiscovery() func() ([]*metav1.APIResourceList, error) {
	return func() ([]*metav1.APIResourceList, error) {
		lists, _ := testDiscovery()()
		return append(lists, &metav1.APIResourceList{
			GroupVersion: entitlementGVR.GroupVersion().String(),
			APIResources: []metav1.APIResource{
				{Name: "serviceentitlements", Namespaced: false, Kind: "ServiceEntitlement", Verbs: []string{"list", "delete", "deletecollection"}},
				{Name: "serviceentitlements/status", Namespaced: false, Kind: "ServiceEntitlement", Verbs: []string{"get", "update"}},
			},
		}, &metav1.APIResourceList{
			// Served from outside the project's storage: never deleted with it.
			GroupVersion: "identity.miloapis.com/v1alpha1",
			APIResources: []metav1.APIResource{
				{Name: "sessions", Namespaced: false, Kind: "Session", Verbs: []string{"list", "get", "delete"}},
			},
		}), nil
	}
}

func newClusterDynamicClient(objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			configMapGVR:   "ConfigMapList",
			entitlementGVR: "ServiceEntitlementList",
		},
		objects...,
	)
}

func entitlement(name string, finalizers ...string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion(entitlementGVR.GroupVersion().String())
	obj.SetKind("ServiceEntitlement")
	obj.SetName(name)
	if len(finalizers) > 0 {
		obj.SetFinalizers(finalizers)
	}
	return obj
}

func TestPurgeStatus_ClusterScopedObjectWithFinalizerBlocks(t *testing.T) {
	core := fake.NewSimpleClientset(namespace("default"))
	dyn := newClusterDynamicClient(
		entitlement("compute", "services.miloapis.com/entitlement"),
		entitlement("plain"),
	)

	status, err := purgeStatus(context.Background(), core, dyn, clusterDiscovery())
	if err != nil {
		t.Fatalf("purgeStatus: %v", err)
	}
	if status.Complete {
		t.Fatal("expected the held entitlement to block completion")
	}
	if len(status.Blockers) != 1 {
		t.Fatalf("expected one blocker, got %v", status.Blockers)
	}
	blocker := status.Blockers[0]
	if blocker.Namespace != "" || blocker.Resource != "serviceentitlements" || blocker.Name != "compute" {
		t.Fatalf("unexpected blocker %+v", blocker)
	}
	msg := status.Message()
	if !strings.Contains(msg, `serviceentitlements "compute"`) || !strings.Contains(msg, "services.miloapis.com/entitlement") {
		t.Fatalf("expected the message to name the object and its finalizer, got %q", msg)
	}
}

func TestPurgeStatus_ClusterScopedObjectWithoutFinalizerDoesNotBlock(t *testing.T) {
	core := fake.NewSimpleClientset(namespace("default"))
	dyn := newClusterDynamicClient(entitlement("plain"))

	status, err := purgeStatus(context.Background(), core, dyn, clusterDiscovery())
	if err != nil {
		t.Fatalf("purgeStatus: %v", err)
	}
	if !status.Complete {
		t.Fatalf("expected complete, got blockers: %v", status.Blockers)
	}
}

func TestDeleteClusterObjects_DeletesStoredClusterScopedKindsOnly(t *testing.T) {
	dyn := newClusterDynamicClient(
		entitlement("compute", "services.miloapis.com/entitlement"),
		entitlement("plain"),
	)
	resources, err := clusterScopedResources(clusterDiscovery())
	if err != nil {
		t.Fatalf("clusterScopedResources: %v", err)
	}
	if len(resources) != 1 || resources[0].gvr != entitlementGVR {
		t.Fatalf("expected only serviceentitlements, got %v", resources)
	}

	if err := deleteClusterObjects(context.Background(), dyn, resources, 2, metav1.ListOptions{}); err != nil {
		t.Fatalf("deleteClusterObjects: %v", err)
	}

	var deleted []string
	for _, a := range dyn.Actions() {
		if d, ok := a.(k8stesting.DeleteCollectionAction); ok {
			deleted = append(deleted, d.GetResource().Resource)
		}
	}
	if len(deleted) != 1 || deleted[0] != "serviceentitlements" {
		t.Fatalf("expected one DeleteCollection of serviceentitlements, got %v", deleted)
	}
}
