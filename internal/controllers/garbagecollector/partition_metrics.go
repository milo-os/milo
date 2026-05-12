package garbagecollector

import (
	"sync"

	"k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"
)

const partitionSubsystem = "gc_partition"

var (
	// partitionCount tracks the total number of registered per-project GC partitions.
	partitionCount = metrics.NewGauge(
		&metrics.GaugeOpts{
			Subsystem:      partitionSubsystem,
			Name:           "total",
			Help:           "Total number of registered per-project GC partitions",
			StabilityLevel: metrics.ALPHA,
		},
	)

	// partitionMonitorCount tracks the number of resource monitors per partition.
	partitionMonitorCount = metrics.NewGaugeVec(
		&metrics.GaugeOpts{
			Subsystem:      partitionSubsystem,
			Name:           "monitor_count",
			Help:           "Number of resource monitors in a GC partition",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"project"},
	)

	// partitionSynced tracks whether each partition's monitors are all synced.
	partitionSynced = metrics.NewGaugeVec(
		&metrics.GaugeOpts{
			Subsystem:      partitionSubsystem,
			Name:           "synced",
			Help:           "Whether a GC partition's monitors are all synced (1) or not (0)",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"project"},
	)
)

var registerPartitionMetrics sync.Once

func registerPartitionMetricsOnce() {
	registerPartitionMetrics.Do(func() {
		legacyregistry.MustRegister(
			partitionCount,
			partitionMonitorCount,
			partitionSynced,
		)
	})
}

func monitorCountForBuilder(gb *GraphBuilder) int {
	gb.monitorLock.RLock()
	defer gb.monitorLock.RUnlock()
	return len(gb.monitors)
}

func boolToFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
