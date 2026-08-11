package projectpurge

import (
	"context"
	"errors"
	"strings"
	"syscall"
	"testing"

	corev1 "k8s.io/api/core/v1"
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
		namespace("demo", corev1.NamespaceCondition{
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
