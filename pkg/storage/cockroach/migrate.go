package cockroach

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
	"k8s.io/klog/v2"

	"go.miloapis.com/milo/pkg/storage/cockroach/migrations"
)

// A MigrateOption configures [Migrate].
type MigrateOption func(*migrateOptions)

type migrateOptions struct {
	sink, sinkConfig string
}

// Migrate applies every pending schema migration to the database at dsn, and
// sets up the changefeed job a [ChangefeedSink] asks for. It runs once per
// rollout, before any API server calls [Open].
func Migrate(ctx context.Context, dsn string, opts ...MigrateOption) error {
	var o migrateOptions
	for _, opt := range opts {
		opt(&o)
	}
	db, err := openDB(dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	// Concurrent runs, such as a retried Job and one started by hand, take
	// turns on a lease row. CockroachDB has no session-level advisory locks,
	// which goose's default locker needs.
	locker, err := lock.NewPostgresTableLocker()
	if err != nil {
		return fmt.Errorf("create the storage migration lock: %w", err)
	}
	if err := locker.Lock(ctx, db); err != nil {
		return fmt.Errorf("take the storage migration lock: %w", err)
	}
	defer func() {
		if err := locker.Unlock(context.WithoutCancel(ctx), db); err != nil {
			klog.ErrorS(err, "Cannot release the storage migration lock; it expires on its own")
		}
	}()
	p, err := newProvider(db)
	if err != nil {
		return err
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("apply storage migrations: %w", err)
	}
	if o.sink == "" {
		return nil
	}
	return reconcileChangefeed(ctx, db, o.sink, o.sinkConfig)
}

// checkSchema returns an error when the database's schema is behind this
// binary's. A schema ahead of it is accepted, so older binaries keep running
// through a rollout and after a rollback.
func checkSchema(ctx context.Context, dsn string) error {
	db, err := openDB(dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	p, err := newProvider(db)
	if err != nil {
		return err
	}
	current, target, err := p.GetVersions(ctx)
	if err != nil {
		return fmt.Errorf("read storage schema version: %w", err)
	}
	if current < target {
		return fmt.Errorf("storage schema is at version %d, this binary needs %d: run the storage migrations first", current, target)
	}
	return nil
}

func openDB(dsn string) (*sql.DB, error) {
	// The pool's parser keeps its own options, such as pool_max_conn_lifetime,
	// out of the parameters sent to the server.
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse cockroachdb dsn: %w", err)
	}
	return stdlib.OpenDB(*cfg.ConnConfig), nil
}

func newProvider(db *sql.DB) (*goose.Provider, error) {
	p, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		return nil, fmt.Errorf("load storage migrations: %w", err)
	}
	return p, nil
}
