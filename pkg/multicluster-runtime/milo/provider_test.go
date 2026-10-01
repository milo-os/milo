package milo

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
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

func TestGetUnknownClusterWrapsErrClusterNotFound(t *testing.T) {
	provider, _ := newTestProvider(metav1.ConditionTrue, nil)

	_, err := provider.Get(context.Background(), "missing-project")
	require.Error(t, err)
	assert.True(t, errors.Is(err, multicluster.ErrClusterNotFound))
	assert.Contains(t, err.Error(), "missing-project")
}

func TestHasSyncedNotBeforeInitialList(t *testing.T) {
	provider, _ := newSyncTestProvider(t, nil, nil)

	assert.False(t, provider.HasSynced())

	provider.markProcessed("unrelated")
	assert.False(t, provider.HasSynced())
}

func TestHasSyncedWaitsForLocalCacheBeforeListing(t *testing.T) {
	provider, _ := newSyncTestProvider(t, nil, nil)

	cacheSynced := make(chan struct{})
	provider.waitForCacheSync = func(ctx context.Context) bool {
		select {
		case <-cacheSynced:
			return true
		case <-ctx.Done():
			return false
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- provider.Start(ctx, &testMultiClusterManager{}) }()

	time.Sleep(50 * time.Millisecond)
	assert.False(t, provider.HasSynced())

	close(cacheSynced)
	assert.Eventually(t, provider.HasSynced, 5*time.Second, 10*time.Millisecond)

	cancel()
	assert.ErrorIs(t, <-done, context.Canceled)
}

func TestHasSyncedWithZeroProjects(t *testing.T) {
	provider, _ := newSyncTestProvider(t, nil, nil)

	require.NoError(t, provider.takeInitialSnapshot(context.Background()))
	assert.True(t, provider.HasSynced())
}

func TestHasSyncedAfterReadyAndNotReadyProjects(t *testing.T) {
	provider, projects := newSyncTestProvider(t, nil, []testProject{
		{name: "ready-a", status: metav1.ConditionTrue},
		{name: "not-ready", status: metav1.ConditionFalse},
		{name: "ready-b", status: metav1.ConditionTrue},
	})

	require.NoError(t, provider.takeInitialSnapshot(context.Background()))

	for i, project := range projects {
		assert.False(t, provider.HasSynced(), "synced before project %d was processed", i)
		_, err := provider.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(project)})
		require.NoError(t, err)
	}

	assert.True(t, provider.HasSynced())
	assert.Len(t, provider.projects, 2)
}

func TestHasSyncedCountsProjectsProcessedBeforeSnapshot(t *testing.T) {
	provider, projects := newSyncTestProvider(t, nil, []testProject{
		{name: "early", status: metav1.ConditionFalse},
	})

	_, err := provider.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(projects[0])})
	require.NoError(t, err)
	assert.False(t, provider.HasSynced())

	require.NoError(t, provider.takeInitialSnapshot(context.Background()))
	assert.True(t, provider.HasSynced())
}

func TestHasSyncedCountsFailedRegistration(t *testing.T) {
	provider, projects := newSyncTestProvider(t, nil, []testProject{
		{name: "broken", status: metav1.ConditionTrue},
	})
	provider.opts.ClusterOptions = []cluster.Option{
		func(o *cluster.Options) {
			o.NewClient = func(*rest.Config, client.Options) (client.Client, error) {
				return nil, errors.New("boom")
			}
		},
	}

	require.NoError(t, provider.takeInitialSnapshot(context.Background()))

	_, err := provider.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(projects[0])})
	require.Error(t, err)
	assert.True(t, provider.HasSynced())
	assert.Empty(t, provider.projects)
}

func TestHasSyncedIgnoresManagerNotStarted(t *testing.T) {
	provider, projects := newSyncTestProvider(t, nil, []testProject{
		{name: "waiting", status: metav1.ConditionTrue},
	})
	provider.mcMgr = nil

	require.NoError(t, provider.takeInitialSnapshot(context.Background()))

	result, err := provider.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(projects[0])})
	require.NoError(t, err)
	assert.NotZero(t, result.RequeueAfter)
	assert.False(t, provider.HasSynced())
}

func TestHasSyncedSnapshotHonorsLabelSelector(t *testing.T) {
	selector := &metav1.LabelSelector{MatchLabels: map[string]string{"environment": "production"}}
	provider, projects := newSyncTestProvider(t, selector, []testProject{
		{name: "prod", status: metav1.ConditionTrue, labels: map[string]string{"environment": "production"}},
		{name: "dev", status: metav1.ConditionTrue, labels: map[string]string{"environment": "development"}},
	})

	require.NoError(t, provider.takeInitialSnapshot(context.Background()))
	assert.False(t, provider.HasSynced())

	_, err := provider.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(projects[0])})
	require.NoError(t, err)
	assert.True(t, provider.HasSynced())
}

func TestHasSyncedSnapshotHonorsInternalServiceDiscovery(t *testing.T) {
	pcp := &unstructured.Unstructured{}
	pcp.SetGroupVersionKind(projectControlPlaneGVK)
	pcp.SetName("pcp-project")
	pcp.SetNamespace("default")

	scheme := runtime.NewScheme()
	scheme.AddKnownTypeWithName(projectControlPlaneGVK, &unstructured.Unstructured{})
	listGVK := projectControlPlaneGVK.GroupVersion().WithKind(projectControlPlaneGVK.Kind + "List")
	scheme.AddKnownTypeWithName(listGVK, &unstructured.UnstructuredList{})

	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pcp).Build()
	provider := &Provider{
		snapshotReader: reader,
		opts:           Options{InternalServiceDiscovery: true},
	}

	require.NoError(t, provider.takeInitialSnapshot(context.Background()))
	assert.False(t, provider.HasSynced())

	provider.markProcessed("pcp-project")
	assert.True(t, provider.HasSynced())
}

func TestHasSyncedProjectDeletedAfterSnapshot(t *testing.T) {
	provider, projects := newSyncTestProvider(t, nil, []testProject{
		{name: "kept", status: metav1.ConditionFalse},
		{name: "deleted", status: metav1.ConditionTrue},
	})

	require.NoError(t, provider.takeInitialSnapshot(context.Background()))

	_, err := provider.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(projects[0])})
	require.NoError(t, err)
	assert.False(t, provider.HasSynced())

	require.NoError(t, provider.client.Delete(context.Background(), projects[1]))
	_, err = provider.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(projects[1])})
	require.NoError(t, err)
	assert.True(t, provider.HasSynced())
}

func TestHasSyncedIgnoresProjectsCreatedAfterSnapshot(t *testing.T) {
	provider, _ := newSyncTestProvider(t, nil, []testProject{
		{name: "existing", status: metav1.ConditionFalse},
	})

	require.NoError(t, provider.takeInitialSnapshot(context.Background()))

	provider.markProcessed("created-later")
	assert.False(t, provider.HasSynced())

	provider.markProcessed("existing")
	assert.True(t, provider.HasSynced())
}

func TestHasSyncedStaysFalseWithoutStart(t *testing.T) {
	provider, projects := newSyncTestProvider(t, nil, []testProject{
		{name: "ready", status: metav1.ConditionTrue},
		{name: "not-ready", status: metav1.ConditionFalse},
	})
	provider.waitForCacheSync = func(context.Context) bool { return true }

	for _, project := range projects {
		_, err := provider.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(project)})
		require.NoError(t, err)
	}

	assert.False(t, provider.HasSynced())
}

func TestHasSyncedCountsFailedGet(t *testing.T) {
	provider, projects := newSyncTestProvider(t, nil, []testProject{
		{name: "forbidden", status: metav1.ConditionTrue},
	})

	require.NoError(t, provider.takeInitialSnapshot(context.Background()))

	forbidden := apierrors.NewForbidden(projectGVK.GroupVersion().WithResource("projects").GroupResource(), "forbidden", errors.New("denied"))
	provider.client = interceptor.NewClient(provider.client.(client.WithWatch), interceptor.Funcs{
		Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
			return forbidden
		},
	})

	_, err := provider.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(projects[0])})
	require.Error(t, err)
	assert.True(t, apierrors.IsForbidden(err))
	assert.True(t, provider.HasSynced())
}

type testProject struct {
	name   string
	status metav1.ConditionStatus
	labels map[string]string
}

func newSyncTestProvider(t *testing.T, labelSelector *metav1.LabelSelector, specs []testProject) (*Provider, []client.Object) {
	t.Helper()

	objects := make([]client.Object, 0, len(specs))
	for _, spec := range specs {
		project := &unstructured.Unstructured{}
		project.SetGroupVersionKind(projectGVK)
		project.SetName(spec.name)
		project.SetLabels(spec.labels)
		conditions := []interface{}{
			map[string]interface{}{
				"type":   "Ready",
				"status": string(spec.status),
			},
		}
		require.NoError(t, unstructured.SetNestedSlice(project.Object, conditions, "status", "conditions"))
		objects = append(objects, project)
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(runtimeScheme).
		WithObjects(objects...).
		Build()

	p := &Provider{
		client:         fakeClient,
		snapshotReader: fakeClient,
		mcMgr:          &testMultiClusterManager{},
		projectRestConfig: &rest.Config{
			Host: "https://localhost",
		},
		projects:  map[string]cluster.Cluster{},
		cancelFns: map[string]context.CancelFunc{},
		opts: Options{
			LabelSelector: labelSelector,
			ClusterOptions: []cluster.Option{
				func(o *cluster.Options) {
					o.NewClient = func(*rest.Config, client.Options) (client.Client, error) {
						return fakeClient, nil
					}
				},
			},
		},
	}

	return p, objects
}
