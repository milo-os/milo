package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apiserver/pkg/server/mux"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/component-base/metrics/legacyregistry"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/priorityqueue"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func scrape(t *testing.T, handler http.Handler) string {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	body, err := io.ReadAll(rec.Body)
	require.NoError(t, err)
	return string(body)
}

func countLines(body, prefix string) int {
	n := 0
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, prefix) {
			n++
		}
	}
	return n
}

func TestInstalledMetricsHandlerServesControllerRuntimeMetrics(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ctrl, err := controller.NewTypedUnmanaged("metrics-test", controller.TypedOptions[reconcile.Request]{
		Reconciler: reconcile.Func(func(context.Context, reconcile.Request) (reconcile.Result, error) {
			return reconcile.Result{}, nil
		}),
		SkipNameValidation: ptr.To(true),
	})
	require.NoError(t, err)
	go func() { _ = ctrl.Start(ctx) }()

	prioQueue := priorityqueue.New[string]("metrics-test-priority")
	defer prioQueue.ShutDown()
	prioQueue.Add("item")

	plainQueue := workqueue.NewTypedRateLimitingQueueWithConfig(
		workqueue.DefaultTypedControllerRateLimiter[string](),
		workqueue.TypedRateLimitingQueueConfig[string]{Name: "metrics-test-plain"},
	)
	defer plainQueue.ShutDown()
	plainQueue.Add("item")

	m := mux.NewPathRecorderMux("test")
	m.Handle("/metrics", legacyregistry.Handler())
	installMetricsHandler(m)

	var body string
	require.Eventually(t, func() bool {
		body = scrape(t, m)
		return strings.Contains(body, `controller_runtime_reconcile_total{controller="metrics-test",result="success"}`)
	}, 5*time.Second, 50*time.Millisecond)

	assert.Contains(t, body, `workqueue_adds_total{controller="metrics-test-priority",name="metrics-test-priority"} 1`)
	assert.Contains(t, body, `workqueue_adds_total{name="metrics-test-plain"} 1`)
	assert.Equal(t, 1, countLines(body, "# TYPE workqueue_adds_total "))
	assert.Equal(t, 1, countLines(body, "go_goroutines "))
	assert.Equal(t, 1, countLines(body, "process_start_time_seconds "))
}

func TestMergedGathererKeepsFirstFamilyAndDropsDuplicateSeries(t *testing.T) {
	first := prometheus.NewRegistry()
	second := prometheus.NewRegistry()

	firstVec := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "shared_total", Help: "first"}, []string{"name"})
	secondVec := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "shared_total", Help: "second"}, []string{"name"})
	first.MustRegister(firstVec)
	second.MustRegister(secondVec)
	firstVec.WithLabelValues("a").Inc()
	secondVec.WithLabelValues("a").Add(5)
	secondVec.WithLabelValues("b").Inc()

	mismatched := prometheus.NewGauge(prometheus.GaugeOpts{Name: "typed", Help: "gauge"})
	first.MustRegister(prometheus.NewCounter(prometheus.CounterOpts{Name: "typed", Help: "counter"}))
	second.MustRegister(mismatched)

	families, err := mergedGatherer{first, second}.Gather()
	require.NoError(t, err)
	require.Len(t, families, 2)

	shared := families[0]
	assert.Equal(t, "shared_total", shared.GetName())
	assert.Equal(t, "first", shared.GetHelp())
	require.Len(t, shared.GetMetric(), 2)
	assert.Equal(t, float64(1), shared.GetMetric()[0].GetCounter().GetValue())
	assert.Equal(t, "b", shared.GetMetric()[1].GetLabel()[0].GetValue())

	typed := families[1]
	assert.Equal(t, "typed", typed.GetName())
	assert.Equal(t, "counter", typed.GetHelp())
	assert.Len(t, typed.GetMetric(), 1)
}
