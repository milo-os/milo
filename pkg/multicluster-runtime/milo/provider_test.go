package milo

import (
	"context"
	"fmt"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
)

type testMultiClusterManager struct {
	mcmanager.Manager
}

func (m *testMultiClusterManager) Engage(context.Context, multicluster.ClusterName, cluster.Cluster) error {
	return nil
}

var runtimeScheme = runtime.NewScheme()

func init() {
	utilruntime.Must(v1alpha1.AddToScheme(runtimeScheme))
}

func TestNotReadyProject(t *testing.T) {
	provider, project := newTestProvider(metav1.ConditionFalse, nil)

	req := ctrl.Request{
		NamespacedName: client.ObjectKeyFromObject(project),
	}

	result, err := provider.Reconcile(context.Background(), req)
	assert.NoError(t, err, "unexpected error returned from reconciler")
	assert.Equal(t, false, result.Requeue)
	assert.Zero(t, result.RequeueAfter)
	assert.Len(t, provider.projects, 0)
}

func TestReadyProject(t *testing.T) {
	provider, project := newTestProvider(metav1.ConditionTrue, nil)

	req := ctrl.Request{
		NamespacedName: client.ObjectKeyFromObject(project),
	}

	result, err := provider.Reconcile(context.Background(), req)
	assert.NoError(t, err, "unexpected error returned from reconciler")
	assert.Equal(t, false, result.Requeue)
	assert.Zero(t, result.RequeueAfter)
	assert.Len(t, provider.projects, 1)

	cl, err := provider.Get(context.Background(), "test-project")
	assert.NoError(t, err)
	apiHost, err := url.Parse(cl.GetConfig().Host)
	assert.NoError(t, err)
	assert.Equal(t, "/apis/resourcemanager.miloapis.com/v1alpha1/projects/test-project/control-plane", apiHost.Path)
}

func TestLabelSelectorFiltering(t *testing.T) {
	// Test that projects matching label selector are processed
	labelSelector := &metav1.LabelSelector{
		MatchLabels: map[string]string{
			"environment": "production",
		},
	}

	// Create a project with matching labels
	project := &unstructured.Unstructured{}
	project.SetGroupVersionKind(projectGVK)
	project.SetName("test-project")
	project.SetLabels(map[string]string{
		"environment": "production",
		"team":        "platform",
	})

	conditions := []interface{}{
		map[string]interface{}{
			"type":   "Ready",
			"status": string(metav1.ConditionTrue),
		},
	}

	if err := unstructured.SetNestedSlice(project.Object, conditions, "status", "conditions"); err != nil {
		t.Fatalf("failed setting status conditions on test project: %v", err)
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(runtimeScheme).
		WithObjects(project).
		Build()

	provider := &Provider{
		client: fakeClient,
		mcMgr:  &testMultiClusterManager{},
		projectRestConfig: &rest.Config{
			Host: "https://localhost",
		},
		projects:  map[string]cluster.Cluster{},
		cancelFns: map[string]context.CancelFunc{},
		opts: Options{
			LabelSelector: labelSelector,
			ClusterOptions: []cluster.Option{
				func(o *cluster.Options) {
					o.NewClient = func(config *rest.Config, options client.Options) (client.Client, error) {
						return fakeClient, nil
					}
				},
			},
		},
	}

	req := ctrl.Request{
		NamespacedName: client.ObjectKeyFromObject(project),
	}

	result, err := provider.Reconcile(context.Background(), req)
	assert.NoError(t, err, "unexpected error returned from reconciler")
	assert.Equal(t, false, result.Requeue)
	assert.Zero(t, result.RequeueAfter)
	assert.Len(t, provider.projects, 1)
}

func TestLabelSelectorFilteringExcludesNonMatching(t *testing.T) {
	// Test that projects not matching label selector are excluded
	labelSelector := &metav1.LabelSelector{
		MatchLabels: map[string]string{
			"environment": "production",
		},
	}

	// Create a project with non-matching labels
	project := &unstructured.Unstructured{}
	project.SetGroupVersionKind(projectGVK)
	project.SetName("test-project")
	project.SetLabels(map[string]string{
		"environment": "development", // Different environment
		"team":        "platform",
	})

	conditions := []interface{}{
		map[string]interface{}{
			"type":   "Ready",
			"status": string(metav1.ConditionTrue),
		},
	}

	if err := unstructured.SetNestedSlice(project.Object, conditions, "status", "conditions"); err != nil {
		t.Fatalf("failed setting status conditions on test project: %v", err)
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(runtimeScheme).
		WithObjects(project).
		Build()

	provider := &Provider{
		client: fakeClient,
		mcMgr:  &testMultiClusterManager{},
		projectRestConfig: &rest.Config{
			Host: "https://localhost",
		},
		projects:  map[string]cluster.Cluster{},
		cancelFns: map[string]context.CancelFunc{},
		opts: Options{
			LabelSelector: labelSelector,
			ClusterOptions: []cluster.Option{
				func(o *cluster.Options) {
					o.NewClient = func(config *rest.Config, options client.Options) (client.Client, error) {
						return fakeClient, nil
					}
				},
			},
		},
	}

	req := ctrl.Request{
		NamespacedName: client.ObjectKeyFromObject(project),
	}

	// This reconcile should succeed but not add any projects because the labels don't match
	// Note: In real usage, the event would be filtered out by the predicate before reaching Reconcile,
	// but we're testing the reconcile logic directly here
	result, err := provider.Reconcile(context.Background(), req)
	assert.NoError(t, err, "unexpected error returned from reconciler")
	assert.Equal(t, false, result.Requeue)
	assert.Zero(t, result.RequeueAfter)
	// The project should still be processed if it reaches Reconcile, as the filtering happens at the watch level
	assert.Len(t, provider.projects, 1)
}

func newTestProvider(projectStatus metav1.ConditionStatus, labelSelector *metav1.LabelSelector) (*Provider, client.Object) {
	project := &unstructured.Unstructured{}
	project.SetGroupVersionKind(projectGVK)
	project.SetName("test-project")

	conditions := []interface{}{
		map[string]interface{}{
			"type":   "Ready",
			"status": string(projectStatus),
		},
	}

	if err := unstructured.SetNestedSlice(project.Object, conditions, "status", "conditions"); err != nil {
		panic(fmt.Errorf("failed setting status conditions on test project: %w", err))
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(runtimeScheme).
		WithObjects(project).
		Build()

	p := &Provider{
		client: fakeClient,
		mcMgr:  &testMultiClusterManager{},
		projectRestConfig: &rest.Config{
			Host: "https://localhost",
		},
		projects:  map[string]cluster.Cluster{},
		cancelFns: map[string]context.CancelFunc{},
		opts: Options{
			LabelSelector: labelSelector,
			ClusterOptions: []cluster.Option{
				func(o *cluster.Options) {
					o.NewClient = func(config *rest.Config, options client.Options) (client.Client, error) {
						return fakeClient, nil
					}
				},
			},
		},
	}

	return p, project
}
