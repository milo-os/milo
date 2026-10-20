package milo

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestMetricsCountEngagedProjects(t *testing.T) {
	provider, project := newTestProvider(metav1.ConditionTrue, nil)
	provider.name = t.Name()
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(project)}

	_, err := provider.Reconcile(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, 1.0, testutil.ToFloat64(engagedProjects.WithLabelValues(t.Name())))

	require.NoError(t, provider.client.Delete(context.Background(), project))
	_, err = provider.Reconcile(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, 0.0, testutil.ToFloat64(engagedProjects.WithLabelValues(t.Name())))
}

func TestMetricsCountFailedEngage(t *testing.T) {
	provider, project := newTestProvider(metav1.ConditionTrue, nil)
	provider.name = t.Name()
	provider.mcMgr = &failingEngageManager{}

	_, err := provider.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKeyFromObject(project),
	})
	require.Error(t, err)

	assert.Equal(t, 1.0, testutil.ToFloat64(registrationFailures.WithLabelValues(t.Name(), stageEngage)))
	assert.Equal(t, 0.0, testutil.ToFloat64(engagedProjects.WithLabelValues(t.Name())))
}

func TestMetricsReportInitialSync(t *testing.T) {
	provider, projects := newSyncTestProvider(t, nil, []testProject{
		{name: "ready", status: metav1.ConditionTrue},
		{name: "not-ready", status: metav1.ConditionFalse},
	})
	provider.name = t.Name()

	provider.markStarted(time.Now())
	assert.Equal(t, 0.0, testutil.ToFloat64(initialSyncComplete.WithLabelValues(t.Name())),
		"initial sync must report 0, not be absent, once the provider starts")

	require.NoError(t, provider.takeInitialSnapshot(context.Background()))
	for _, project := range projects {
		assert.Equal(t, 0.0, testutil.ToFloat64(initialSyncComplete.WithLabelValues(t.Name())))
		_, err := provider.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(project)})
		require.NoError(t, err)
	}

	assert.Equal(t, 1.0, testutil.ToFloat64(initialSyncComplete.WithLabelValues(t.Name())))
	assert.Greater(t, testutil.ToFloat64(initialSyncDuration.WithLabelValues(t.Name())), 0.0)
	assert.Equal(t, 1.0, testutil.ToFloat64(engagedProjects.WithLabelValues(t.Name())))
}
