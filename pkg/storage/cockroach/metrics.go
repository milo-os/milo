package cockroach

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	k8smetrics "k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"
)

// The storage's metrics. None has a project label, because a series per
// project would cost more than it tells.
var (
	// The buckets resolve the low milliseconds, where most operations land.
	dbOperations = k8smetrics.NewHistogramVec(
		&k8smetrics.HistogramOpts{
			Name:           "cockroachstorage_db_operation_seconds",
			Help:           "Time spent in the database per storage operation, by operation (get, write or revision); a write counts each attempt",
			Buckets:        k8smetrics.ExponentialBuckets(0.0005, 2, 14),
			StabilityLevel: k8smetrics.ALPHA,
		},
		[]string{"operation"},
	)
	dbErrors = k8smetrics.NewCounterVec(
		&k8smetrics.CounterOpts{
			Name:           "cockroachstorage_db_errors_total",
			Help:           "Failed storage operations, by operation and SQLSTATE, or canceled or other for errors without one",
			StabilityLevel: k8smetrics.ALPHA,
		},
		[]string{"operation", "code"},
	)
	retries = k8smetrics.NewCounterVec(
		&k8smetrics.CounterOpts{
			Name:           "cockroachstorage_db_retries_total",
			Help:           "Transactions run again after the database aborted them, by operation and SQLSTATE",
			StabilityLevel: k8smetrics.ALPHA,
		},
		[]string{"operation", "code"},
	)
)

func init() {
	legacyregistry.MustRegister(dbOperations, dbErrors, retries)
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

// observeOperation records a storage operation's time in the database, and its
// failure if it failed.
func observeOperation(operation string, start time.Time, err error) {
	dbOperations.WithLabelValues(operation).Observe(time.Since(start).Seconds())
	if err != nil {
		dbErrors.WithLabelValues(operation, errorCode(err)).Inc()
	}
}

// errorCode returns err's SQLSTATE, or "canceled" or "other" when it has none.
func errorCode(err error) string {
	if pe, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pe.Code
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "canceled"
	}
	return "other"
}
