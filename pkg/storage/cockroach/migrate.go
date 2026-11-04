package cockroach

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"go.miloapis.com/milo/pkg/storage/cockroach/migrations"
)

// Migrate applies every pending schema migration to the database at dsn. It
// runs once per rollout, before any API server calls [Open].
func Migrate(ctx context.Context, dsn string) error {
	// Concurrent runs, such as a retried Job and one started by hand, take
	// turns on a lease row. CockroachDB has no session-level advisory locks,
	// which goose's default locker needs.
	locker, err := lock.NewPostgresTableLocker()
	if err != nil {
		return fmt.Errorf("create the storage migration lock: %w", err)
	}
	return withProvider(dsn, func(p *goose.Provider) error {
		if _, err := p.Up(ctx); err != nil {
			return fmt.Errorf("apply storage migrations: %w", err)
		}
		return nil
	}, goose.WithLocker(locker))
}

// checkSchema returns an error when the database's schema is behind this
// binary's. A schema ahead of it is accepted, so older binaries keep running
// through a rollout and after a rollback.
func checkSchema(ctx context.Context, dsn string) error {
	return withProvider(dsn, func(p *goose.Provider) error {
		current, target, err := p.GetVersions(ctx)
		if err != nil {
			return fmt.Errorf("read storage schema version: %w", err)
		}
		if current < target {
			return fmt.Errorf("storage schema is at version %d, this binary needs %d: run the storage migrations first", current, target)
		}
		return nil
	})
}

func withProvider(dsn string, fn func(*goose.Provider) error, opts ...goose.ProviderOption) error {
	// The pool's parser keeps its own options, such as pool_max_conn_lifetime,
	// out of the parameters sent to the server.
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("parse cockroachdb dsn: %w", err)
	}
	db := stdlib.OpenDB(*cfg.ConnConfig)
	defer db.Close()
	p, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS, opts...)
	if err != nil {
		return fmt.Errorf("load storage migrations: %w", err)
	}
	return fn(p)
}
