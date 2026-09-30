package app

import (
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
	"k8s.io/apiserver/pkg/server/mux"
	"k8s.io/component-base/metrics/legacyregistry"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

func installMetricsHandler(m *mux.PathRecorderMux) {
	m.Unregister("/metrics")
	m.Handle("/metrics", metricsHandler(legacyregistry.DefaultGatherer, ctrlmetrics.Registry))
}

func metricsHandler(gatherers ...prometheus.Gatherer) http.Handler {
	return promhttp.InstrumentMetricHandler(
		prometheus.DefaultRegisterer,
		promhttp.HandlerFor(mergedGatherer(gatherers), promhttp.HandlerOpts{}),
	)
}

type mergedGatherer []prometheus.Gatherer

func (g mergedGatherer) Gather() ([]*dto.MetricFamily, error) {
	var errs []error
	byName := map[string]*dto.MetricFamily{}
	seen := map[string]map[string]struct{}{}

	for _, gatherer := range g {
		families, err := gatherer.Gather()
		if err != nil {
			errs = append(errs, err)
		}
		for _, family := range families {
			name := family.GetName()
			existing, ok := byName[name]
			if !ok {
				byName[name] = family
				seen[name] = map[string]struct{}{}
				for _, metric := range family.GetMetric() {
					seen[name][labelSignature(metric)] = struct{}{}
				}
				continue
			}
			if existing.GetType() != family.GetType() {
				continue
			}
			for _, metric := range family.GetMetric() {
				signature := labelSignature(metric)
				if _, dup := seen[name][signature]; dup {
					continue
				}
				seen[name][signature] = struct{}{}
				existing.Metric = append(existing.Metric, metric)
			}
		}
	}

	merged := make([]*dto.MetricFamily, 0, len(byName))
	for _, family := range byName {
		merged = append(merged, family)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].GetName() < merged[j].GetName() })
	return merged, errors.Join(errs...)
}

func labelSignature(metric *dto.Metric) string {
	pairs := make([]string, 0, len(metric.GetLabel()))
	for _, label := range metric.GetLabel() {
		pairs = append(pairs, label.GetName()+"\xff"+label.GetValue())
	}
	sort.Strings(pairs)
	return strings.Join(pairs, "\xfe")
}
