package cockroach

import (
	"sync/atomic"

	"github.com/jackc/pgx/v5/pgxpool"
	k8smetrics "k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"
)

func init() {
	legacyregistry.CustomMustRegister(poolMetrics)
}

// poolMetrics reports the connection pool's state at each scrape. A process
// has one backend, which registers its pool when it opens and clears it on
// Close.
var poolMetrics = &poolCollector{}

type poolCollector struct {
	k8smetrics.BaseStableCollector
	pool atomic.Pointer[pgxpool.Pool]
}

func (c *poolCollector) watch(p *pgxpool.Pool) { c.pool.Store(p) }

// forget stops reporting p, unless another backend has replaced it.
func (c *poolCollector) forget(p *pgxpool.Pool) { c.pool.CompareAndSwap(p, nil) }

var (
	poolConns = k8smetrics.NewDesc("cockroachstorage_pool_connections",
		"Connections in the pool, by state (acquired, idle or constructing), and the pool's maximum as max",
		[]string{"state"}, nil, k8smetrics.ALPHA, "")
	poolAcquires = k8smetrics.NewDesc("cockroachstorage_pool_acquires_total",
		"Connections taken from the pool; empty counts those that had to wait for one to free up or open",
		[]string{"kind"}, nil, k8smetrics.ALPHA, "")
	poolAcquireSeconds = k8smetrics.NewDesc("cockroachstorage_pool_acquire_seconds_total",
		"Time spent waiting for a connection from the pool",
		nil, nil, k8smetrics.ALPHA, "")
)

func (c *poolCollector) DescribeWithStability(ch chan<- *k8smetrics.Desc) {
	ch <- poolConns
	ch <- poolAcquires
	ch <- poolAcquireSeconds
}

func (c *poolCollector) CollectWithStability(ch chan<- k8smetrics.Metric) {
	p := c.pool.Load()
	if p == nil {
		return
	}
	s := p.Stat()
	ch <- k8smetrics.NewLazyConstMetric(poolConns, k8smetrics.GaugeValue, float64(s.AcquiredConns()), "acquired")
	ch <- k8smetrics.NewLazyConstMetric(poolConns, k8smetrics.GaugeValue, float64(s.IdleConns()), "idle")
	ch <- k8smetrics.NewLazyConstMetric(poolConns, k8smetrics.GaugeValue, float64(s.ConstructingConns()), "constructing")
	ch <- k8smetrics.NewLazyConstMetric(poolConns, k8smetrics.GaugeValue, float64(s.MaxConns()), "max")
	ch <- k8smetrics.NewLazyConstMetric(poolAcquires, k8smetrics.CounterValue, float64(s.AcquireCount()), "all")
	ch <- k8smetrics.NewLazyConstMetric(poolAcquires, k8smetrics.CounterValue, float64(s.EmptyAcquireCount()), "empty")
	ch <- k8smetrics.NewLazyConstMetric(poolAcquireSeconds, k8smetrics.CounterValue, s.AcquireDuration().Seconds())
}
