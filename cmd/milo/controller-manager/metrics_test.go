package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apiserver/pkg/server/mux"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/component-base/metrics/legacyregistry"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/priorityqueue"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
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
	require.NoError(t, ctrl.Watch(source.Func(func(_ context.Context, q workqueue.TypedRateLimitingInterface[reconcile.Request]) error {
		q.Add(reconcile.Request{NamespacedName: types.NamespacedName{Name: "item"}})
		return nil
	})))
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

	controllerQueueSeries := regexp.MustCompile(`(?m)^workqueue_adds_total\{controller="metrics-test",name="metrics-test"\} 1$`)
	var body string
	require.Eventually(t, func() bool {
		body = scrape(t, m)
		return strings.Contains(body, `controller_runtime_reconcile_total{controller="metrics-test",result="success"}`) &&
			controllerQueueSeries.MatchString(body)
	}, 5*time.Second, 50*time.Millisecond)

	assert.Contains(t, body, `workqueue_adds_total{controller="metrics-test-priority",name="metrics-test-priority"} 1`)
	assert.Contains(t, body, `workqueue_adds_total{name="metrics-test-plain"} 1`)
	assert.Equal(t, 1, countLines(body, "# TYPE workqueue_adds_total "))
	assert.Equal(t, 1, countLines(body, "go_goroutines "))
	assert.Equal(t, 1, countLines(body, "process_start_time_seconds "))

	body = scrape(t, m)
	assert.Regexp(t, `(?m)^promhttp_metric_handler_requests_total\{code="200"\} [1-9]`, body)
}

func mustParse(t *testing.T, body string) map[string]*dto.MetricFamily {
	t.Helper()
	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(strings.NewReader(body))
	require.NoError(t, err)
	return families
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

	families, err := mergedGatherer{first, second}.Gather()
	require.NoError(t, err)
	require.Len(t, families, 1)

	shared := families[0]
	assert.Equal(t, "shared_total", shared.GetName())
	assert.Equal(t, "first", shared.GetHelp())
	require.Len(t, shared.GetMetric(), 2)
	assert.Equal(t, float64(1), shared.GetMetric()[0].GetCounter().GetValue())
	assert.Equal(t, "b", shared.GetMetric()[1].GetLabel()[0].GetValue())
}

func TestMergedGathererKeepsSeriesWithDifferentHelpAndLabels(t *testing.T) {
	legacy := prometheus.NewRegistry()
	runtime := prometheus.NewRegistry()

	legacyDepth := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "workqueue_depth",
		Help: "[ALPHA] Current depth of workqueue",
	}, []string{"name"})
	runtimeDepth := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "workqueue_depth",
		Help: "Current depth of workqueue",
	}, []string{"name", "controller", "priority"})
	legacy.MustRegister(legacyDepth)
	runtime.MustRegister(runtimeDepth)
	legacyDepth.WithLabelValues("garbagecollector").Set(2)
	runtimeDepth.WithLabelValues("allowance-bucket", "allowance-bucket", "").Set(7)

	body := scrape(t, metricsHandler(prometheus.NewRegistry(), legacy, runtime))
	families := mustParse(t, body)

	depth, ok := families["workqueue_depth"]
	require.True(t, ok)
	assert.Equal(t, "[ALPHA] Current depth of workqueue", depth.GetHelp())
	require.Len(t, depth.GetMetric(), 2)

	byName := map[string]float64{}
	for _, metric := range depth.GetMetric() {
		labels := map[string]string{}
		for _, label := range metric.GetLabel() {
			labels[label.GetName()] = label.GetValue()
		}
		byName[labels["name"]] = metric.GetGauge().GetValue()
		if labels["name"] == "allowance-bucket" {
			assert.Equal(t, "allowance-bucket", labels["controller"])
		}
	}
	assert.Equal(t, map[string]float64{"garbagecollector": 2, "allowance-bucket": 7}, byName)
}

func TestMergedGathererReportsTypeClash(t *testing.T) {
	first := prometheus.NewRegistry()
	second := prometheus.NewRegistry()
	first.MustRegister(prometheus.NewCounter(prometheus.CounterOpts{Name: "typed", Help: "counter"}))
	second.MustRegister(prometheus.NewGauge(prometheus.GaugeOpts{Name: "typed", Help: "gauge"}))
	second.MustRegister(prometheus.NewGauge(prometheus.GaugeOpts{Name: "unrelated", Help: "gauge"}))

	families, err := mergedGatherer{first, second}.Gather()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"typed"`)
	require.Len(t, families, 2)
	assert.Equal(t, "counter", families[0].GetHelp())
	assert.Len(t, families[0].GetMetric(), 1)

	errorsRegistry := prometheus.NewRegistry()
	body := scrape(t, metricsHandler(errorsRegistry, first, second))
	families2 := mustParse(t, body)
	assert.Contains(t, families2, "typed")
	assert.Contains(t, families2, "unrelated")

	gathered, err := errorsRegistry.Gather()
	require.NoError(t, err)
	var gatherErrors float64
	for _, family := range gathered {
		if family.GetName() == "promhttp_metric_handler_errors_total" {
			for _, metric := range family.GetMetric() {
				gatherErrors += metric.GetCounter().GetValue()
			}
		}
	}
	assert.Equal(t, float64(1), gatherErrors)
}
