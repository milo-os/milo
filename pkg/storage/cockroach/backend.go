package cockroach

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// A Backend is the process's connection to the database. It is safe for
// concurrent use.
type Backend struct {
	pool  *pgxpool.Pool
	stops []func()
}

// An Option configures [Open].
type Option func(*options)

type options struct {
	maxConns int32
	migrate  bool
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

// A pooled connection lives for connLifetime plus up to connLifetimeJitter,
// unless the DSN sets pool_max_conn_lifetime. Rotating connections spreads
// them over every node again after a rolling restart.
const (
	connLifetime       = 10 * time.Minute
	connLifetimeJitter = 2 * time.Minute
)

func (o options) validate() error {
	if o.maxConns < 0 {
		return errors.New("cockroach storage: the connection limit cannot be negative")
	}
	return nil
}

// Open connects to the database at dsn. The schema must be one that [Migrate]
// set up for this binary's version. The caller closes the Backend with
// [Backend.Close].
func Open(ctx context.Context, dsn string, opts ...Option) (*Backend, error) {
	var o options
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
	b := &Backend{pool: pool}
	b.onClose(pool.Close)
	poolMetrics.watch(pool)
	b.onClose(func() { poolMetrics.forget(pool) })
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

// Close closes the pool.
func (b *Backend) Close() {
	for _, stop := range slices.Backward(b.stops) {
		stop()
	}
}

// onClose has Close call stop, before the stop functions registered earlier.
func (b *Backend) onClose(stop func()) { b.stops = append(b.stops, stop) }
