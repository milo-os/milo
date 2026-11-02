package cockroach

import (
	"cmp"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"k8s.io/apimachinery/pkg/util/cache"
)

// initialRevision is a project's revision before its first write. Kubernetes
// reserves 0 to mean any revision.
const initialRevision = 1

// A Backend is the database connection and the background work that every
// store in the process shares. It is safe for concurrent use.
type Backend struct {
	pool         *pgxpool.Pool
	revTS        revisionTimestamps
	hub          *hub
	feed         *feed
	committer    committer
	sweeper      *sweeper
	lagReadiness time.Duration
	stops        []func()
}

// An Option configures [Open].
type Option func(*options)

type options struct {
	maxConns                    int32
	groupCommit                 bool
	projectWrites, transactions int
	admissionWait               time.Duration
	lagReadiness                time.Duration
	hub                         hubConfig
	feedRetry                   time.Duration
	sweepInterval               time.Duration
	migrate                     bool
}

func defaultOptions() options {
	return options{
		groupCommit:   true,
		admissionWait: time.Second,
		hub:           defaultHubConfig(),
		feedRetry:     time.Second,
		sweepInterval: time.Second,
	}
}

// MigrateOnOpen has [Open] apply pending schema migrations first, as [Migrate]
// does. Servers starting together take turns on the migration lock, and only
// the first finds anything to apply. The server's database user then needs the
// rights to change the schema and cluster settings, which a separate
// migration step keeps away from the server.
func MigrateOnOpen() Option {
	return func(o *options) { o.migrate = true }
}

// MaxConns caps the connection pool. Zero keeps the DSN's setting.
func MaxConns(n int32) Option {
	return func(o *options) { o.maxConns = n }
}

// GroupCommit sets whether a project's concurrent writes commit together, in
// one transaction. Quiet projects wait for nothing. It's on by default, and
// turning it off is a diagnostic fallback.
func GroupCommit(enabled bool) Option {
	return func(o *options) { o.groupCommit = enabled }
}

// MaxProjectWritesInFlight caps the writes in flight per project. A write past
// the cap waits briefly for a slot and then gets 429 Too Many Requests. Zero
// keeps the default of 8, or 256 with GroupCommit.
func MaxProjectWritesInFlight(n int) Option {
	return func(o *options) { o.projectWrites = n }
}

// MaxTransactionsInFlight caps the write transactions in flight in this
// process. Each transaction is one write, or one batch with GroupCommit. A
// write past the cap waits briefly and then gets 429 Too Many Requests. Zero
// keeps the default of three quarters of the pool's connections.
func MaxTransactionsInFlight(n int) Option {
	return func(o *options) { o.transactions = n }
}

// AdmissionWait sets how long a write past either cap waits for a slot before
// it gets 429 Too Many Requests. Zero keeps the default of 1s.
func AdmissionWait(d time.Duration) Option {
	return func(o *options) { o.admissionWait = cmp.Or(d, o.admissionWait) }
}

// WatchReplay sets how far back a watch can resume, as a time window and a
// number of events behind its project's latest revision. A watch from further
// back gets 410 Gone, and the client lists again. Exact reads at a revision
// share the window, so it must stay under half the objects table's GC TTL,
// and Open refuses a longer one. Zero keeps the defaults of 5 minutes and
// 1024 events.
func WatchReplay(window time.Duration, events int) Option {
	return func(o *options) {
		o.hub.replay.window = cmp.Or(window, o.hub.replay.window)
		o.hub.replay.events = cmp.Or(events, o.hub.replay.events)
	}
}

// WatchLagReadiness makes the API server report itself not ready while
// watches lag more than d behind writes. Lag usually comes from the cluster,
// so it tends to make every replica unready at once. Zero, the default,
// leaves lag to the metrics.
func WatchLagReadiness(d time.Duration) Option {
	return func(o *options) { o.lagReadiness = d }
}

// A pooled connection lives for connLifetime plus up to connLifetimeJitter,
// unless the DSN sets pool_max_conn_lifetime. Rotating connections spreads
// them over every node again after a rolling restart.
const (
	connLifetime       = 10 * time.Minute
	connLifetimeJitter = 2 * time.Minute
)

const (
	defaultProjectWrites            = 8
	defaultProjectWritesGroupCommit = maxBatchWrites
)

func (o options) validate() error {
	if o.maxConns < 0 || o.projectWrites < 0 || o.transactions < 0 || o.lagReadiness < 0 {
		return errors.New("cockroach storage: connection and write limits and the lag limit cannot be negative")
	}
	if o.admissionWait < 0 || o.hub.replay.window < 0 || o.hub.replay.events < 0 {
		return errors.New("cockroach storage: the admission wait and the watch replay window and events cannot be negative")
	}
	return nil
}

// Open connects to the database at dsn and starts the background work. The
// schema must be one that [Migrate] set up for this binary's version. The
// caller closes the Backend with [Backend.Close].
func Open(ctx context.Context, dsn string, opts ...Option) (*Backend, error) {
	o := defaultOptions()
	for _, opt := range opts {
		opt(&o)
	}
	if err := o.validate(); err != nil {
		return nil, err
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse cockroachdb dsn: %w", err)
	}
	reloadClientCert(&cfg.ConnConfig.Config, dsn)
	if o.maxConns > 0 {
		cfg.MaxConns = o.maxConns
	}
	if !strings.Contains(dsn, "pool_max_conn_lifetime") {
		cfg.MaxConnLifetime, cfg.MaxConnLifetimeJitter = connLifetime, connLifetimeJitter
	}
	if o.migrate {
		if err := Migrate(ctx, dsn); err != nil {
			return nil, err
		}
	}
	if err := checkSchema(ctx, dsn); err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect to cockroachdb: %w", err)
	}
	if err := checkGCWindow(ctx, pool, o.hub.replay.window); err != nil {
		pool.Close()
		return nil, err
	}
	b := &Backend{
		pool:         pool,
		revTS:        revisionTimestamps{c: cache.NewExpiring(), window: o.hub.replay.window},
		lagReadiness: o.lagReadiness,
	}
	b.onClose(pool.Close)
	poolMetrics.watch(pool)
	b.onClose(func() { poolMetrics.forget(pool) })
	b.hub = newHub(b.revTS, o.hub)
	b.feed = &feed{pool: pool, connConfig: cfg.ConnConfig, retryDelay: o.feedRetry, handler: b.hub, prefill: o.hub.replay.window}

	projectWrites := defaultProjectWrites
	if o.groupCommit {
		projectWrites = defaultProjectWritesGroupCommit
	}
	var stopWrites func()
	b.committer, stopWrites = newCommitter(b.tryCommitBatch,
		newAdmission("project", cmp.Or(o.projectWrites, projectWrites), o.admissionWait),
		newAdmission("server", cmp.Or(o.transactions, int(cfg.MaxConns)*3/4), o.admissionWait),
		o.groupCommit)
	b.onClose(stopWrites)

	// A tombstone outlasts the events that replay its delete, so a feed that
	// resumes from its cursor still finds it.
	b.sweeper = &sweeper{pool: pool, committer: b.committer, tombstoneRetention: 2 * o.hub.replay.window}

	// The background work stops first, because the sweeps write through the
	// committers.
	bg, cancel := context.WithCancel(context.WithoutCancel(ctx))
	var running sync.WaitGroup
	b.onClose(func() {
		cancel()
		running.Wait()
	})
	running.Go(func() { b.feed.run(bg) })
	running.Go(func() { every(bg, fixed(o.hub.tickInterval), b.feed.reportLag) })
	running.Go(func() { every(bg, fixed(o.hub.tickInterval), b.hub.tick) })
	running.Go(func() { every(bg, jittered(o.sweepInterval), func(time.Time) { b.sweeper.sweep(bg) }) })
	return b, nil
}

// reloadClientCert has cfg's connections read the DSN's client certificate
// afresh at every TLS handshake, as etcd's client does, so a rotated
// certificate takes effect as the pool replaces its connections. The CA bundle
// stays as loaded, so rotating the CA takes a restart.
func reloadClientCert(cfg *pgconn.Config, dsn string) {
	tlsConfigs := []*tls.Config{cfg.TLSConfig}
	for _, f := range cfg.Fallbacks {
		tlsConfigs = append(tlsConfigs, f.TLSConfig)
	}
	for _, tc := range tlsConfigs {
		if tc == nil || len(tc.Certificates) == 0 {
			continue
		}
		tc.Certificates = nil
		tc.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			fresh, err := pgconn.ParseConfig(dsn)
			if err != nil {
				return nil, fmt.Errorf("reload the cockroachdb client certificate: %w", err)
			}
			return &fresh.TLSConfig.Certificates[0], nil
		}
	}
}

// Ping returns an error when the database doesn't answer.
func (b *Backend) Ping(ctx context.Context) error {
	return b.pool.Ping(ctx)
}

// WatchReady returns an error until the watch feed has delivered its first
// update and watches see current writes. A feed that breaks later leaves the
// server ready, since every replica usually shares the cause. With
// [WatchLagReadiness], it also returns one while watches lag further behind
// than that, which a broken feed soon does.
func (b *Backend) WatchReady() error {
	select {
	case <-b.hub.ready:
	default:
		return errors.New("the CockroachDB watch feed has not started")
	}
	if !b.feed.caughtUp.Load() {
		return fmt.Errorf("the CockroachDB watch feed hasn't caught up since it started, and is %s behind", b.feed.lag().Round(time.Second))
	}
	if lag := b.feed.lag(); b.lagReadiness > 0 && lag > b.lagReadiness {
		return fmt.Errorf("the CockroachDB watch feed is %s behind, more than %s", lag.Round(time.Second), b.lagReadiness)
	}
	return nil
}

// checkGCWindow returns an error unless the objects table's GC TTL is over
// twice window. Exact reads at a revision in the window read past versions,
// and GC removes those once the TTL runs out.
func checkGCWindow(ctx context.Context, q querier, window time.Duration) error {
	ttl, err := gcTTL(ctx, q)
	if err != nil {
		return err
	}
	if ttl <= 2*window {
		return fmt.Errorf("cockroach storage: a %s watch replay window needs a GC TTL over %s on the objects table, which has %s", window, 2*window, ttl)
	}
	return nil
}

// gcTTL reads the objects table's GC TTL.
func gcTTL(ctx context.Context, q querier) (time.Duration, error) {
	var zone string
	if err := q.QueryRow(ctx, `SELECT raw_config_sql FROM [SHOW ZONE CONFIGURATION FROM TABLE objects]`).Scan(&zone); err != nil {
		return 0, fmt.Errorf("read the objects table's GC TTL: %w", err)
	}
	match := gcTTLPattern.FindStringSubmatch(zone)
	if match == nil {
		return 0, fmt.Errorf("the objects table's zone configuration sets no GC TTL: %s", zone)
	}
	seconds, err := strconv.Atoi(match[1])
	return time.Duration(seconds) * time.Second, err
}

var gcTTLPattern = regexp.MustCompile(`gc\.ttlseconds = (\d+)`)

// Close stops the background work and closes the pool. Stores must not be
// used after Close.
func (b *Backend) Close() {
	for _, stop := range slices.Backward(b.stops) {
		stop()
	}
}

// onClose has Close call stop, before the stop functions registered earlier.
func (b *Backend) onClose(stop func()) { b.stops = append(b.stops, stop) }

// revisionTimestamps maps a project's revisions to their commit timestamps,
// which exact reads at those revisions need.
type revisionTimestamps struct {
	c      *cache.Expiring
	window time.Duration
}

type revKey struct {
	project string
	rev     int64
}

// put keeps rev's commit timestamp until it's a window old, however often
// it's recorded. An exact read at an older timestamp would find its versions
// gone.
func (r revisionTimestamps) put(project string, rev int64, ts hlc) {
	if ttl := r.window - time.Since(ts.time()); ttl > 0 {
		r.c.Set(revKey{project, rev}, ts, ttl)
	}
}

func (r revisionTimestamps) get(project string, rev int64) (hlc, bool) {
	ts, ok := r.c.Get(revKey{project, rev})
	if !ok {
		return hlc{}, false
	}
	return ts.(hlc), true
}

// readAt returns a timestamp at which project's state is exactly rev: rev's
// own commit time, or else the instant before rev+1 committed, since nothing
// else in the project commits between the two. The fallback, which successor
// reports, holds only when rev and rev+1 committed separately. The hub
// records commit times in release order, so knowing rev+1's means knowing
// rev's, unless rev's has just expired.
func (r revisionTimestamps) readAt(project string, rev int64) (ts hlc, successor, ok bool) {
	if ts, ok := r.get(project, rev); ok {
		return ts, false, true
	}
	ts, ok = r.get(project, rev+1)
	return ts.prev(), ok, ok
}

func (r revisionTimestamps) forget(project string, rev int64) {
	r.c.Delete(revKey{project, rev})
}
