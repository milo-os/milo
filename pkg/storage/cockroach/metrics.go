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

// The watch hub's metrics. Sinkless changefeeds export none of their own, so
// these are the only view of the feed. None has a project label, because a
// series per project would cost more than it tells.
var (
	feedLag = k8smetrics.NewGauge(
		&k8smetrics.GaugeOpts{
			Name:           "cockroachstorage_watch_feed_lag_seconds",
			Help:           "Time since the watch feed's last resolved timestamp: every write before it has reached the hub",
			StabilityLevel: k8smetrics.ALPHA,
		},
	)
	delivery = k8smetrics.NewHistogram(
		&k8smetrics.HistogramOpts{
			Name: "cockroachstorage_watch_delivery_seconds",
			Help: "Time from a write's commit to the hub handing it to watchers: the watch delay storage adds. " +
				"The commit time comes from the database's clock, which CockroachDB keeps within its maximum clock offset of this server's",
			Buckets:        k8smetrics.ExponentialBuckets(0.001, 2, 14),
			StabilityLevel: k8smetrics.ALPHA,
		},
	)
	expiredObjects = k8smetrics.NewCounter(
		&k8smetrics.CounterOpts{
			Name:           "cockroachstorage_expired_objects_total",
			Help:           "Objects whose TTL passed that this process deleted, each with its own revision and DELETED event",
			StabilityLevel: k8smetrics.ALPHA,
		},
	)
	purgedTombstones = k8smetrics.NewCounter(
		&k8smetrics.CounterOpts{
			Name:           "cockroachstorage_purged_tombstones_total",
			Help:           "Tombstones of deleted objects this process removed once watchers no longer needed them",
			StabilityLevel: k8smetrics.ALPHA,
		},
	)
	writeAdmissions = k8smetrics.NewCounterVec(
		&k8smetrics.CounterOpts{
			Name: "cockroachstorage_write_admissions_total",
			Help: "Admissions by limit (project: writes in flight per project; server: transactions in flight per server) " +
				"and result: admitted at once, waited for a slot, or rejected with 429",
			StabilityLevel: k8smetrics.ALPHA,
		},
		[]string{"limit", "result"},
	)
	feedRestarts = k8smetrics.NewCounterVec(
		&k8smetrics.CounterOpts{
			Name:           "cockroachstorage_watch_feed_restarts_total",
			Help:           "Watch feed restarts, by kind: resume from the cursor, or reset to the current state, which makes open watches relist",
			StabilityLevel: k8smetrics.ALPHA,
		},
		[]string{"kind"},
	)
	watchEvents = k8smetrics.NewCounter(
		&k8smetrics.CounterOpts{
			Name:           "cockroachstorage_watch_events_total",
			Help:           "Writes the hub released to watchers, in each project's revision order",
			StabilityLevel: k8smetrics.ALPHA,
		},
	)
	pendingEvents = k8smetrics.NewGauge(
		&k8smetrics.GaugeOpts{
			Name:           "cockroachstorage_watch_pending_events",
			Help:           "Object writes the hub holds because an earlier revision of the same project has not arrived",
			StabilityLevel: k8smetrics.ALPHA,
		},
	)
	recentEvents = k8smetrics.NewGauge(
		&k8smetrics.GaugeOpts{
			Name:           "cockroachstorage_watch_recent_events",
			Help:           "Released events the hub keeps for watches that start at an older revision",
			StabilityLevel: k8smetrics.ALPHA,
		},
	)
	openWatchers = k8smetrics.NewGauge(
		&k8smetrics.GaugeOpts{
			Name:           "cockroachstorage_watchers",
			Help:           "Open watches the hub sends events to",
			StabilityLevel: k8smetrics.ALPHA,
		},
	)
	closedWatchers = k8smetrics.NewCounterVec(
		&k8smetrics.CounterOpts{
			Name:           "cockroachstorage_watchers_closed_total",
			Help:           "Watches the hub closed, by reason: behind (the watcher fell too far behind) or reset (the feed restarted from the current state)",
			StabilityLevel: k8smetrics.ALPHA,
		},
		[]string{"reason"},
	)
	bookmarks = k8smetrics.NewCounter(
		&k8smetrics.CounterOpts{
			Name:           "cockroachstorage_watch_bookmarks_total",
			Help:           "Periodic bookmarks sent to watches that allow them",
			StabilityLevel: k8smetrics.ALPHA,
		},
	)
	recentEventBytes = k8smetrics.NewGauge(
		&k8smetrics.GaugeOpts{
			Name:           "cockroachstorage_watch_recent_event_bytes",
			Help:           "Bytes of object values the hub keeps in its recent events, across projects",
			StabilityLevel: k8smetrics.ALPHA,
		},
	)
	// The buckets resolve the low milliseconds, where most operations land.
	dbOperations = k8smetrics.NewHistogramVec(
		&k8smetrics.HistogramOpts{
			Name:           "cockroachstorage_db_operation_seconds",
			Help:           "Time spent in the database per storage operation, by operation (get, list, write or revision); a write counts each attempt",
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
			Help:           "Transactions run again after the database aborted them, by operation (write or list) and SQLSTATE",
			StabilityLevel: k8smetrics.ALPHA,
		},
		[]string{"operation", "code"},
	)
	feedWaits = k8smetrics.NewCounterVec(
		&k8smetrics.CounterOpts{
			Name: "cockroachstorage_watch_feed_waits_total",
			Help: "Requests that waited for the hub to reach a revision, by request (watch or exact_read) " +
				"and result (caught_up or timeout)",
			StabilityLevel: k8smetrics.ALPHA,
		},
		[]string{"request", "result"},
	)
)

func init() {
	legacyregistry.MustRegister(feedLag, delivery, expiredObjects, purgedTombstones, writeAdmissions, feedRestarts, watchEvents, pendingEvents, recentEvents,
		recentEventBytes, dbOperations, dbErrors, retries, openWatchers, closedWatchers, bookmarks, feedWaits)
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
