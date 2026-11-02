// Package migrations holds the CockroachDB storage schema as goose migrations.
//
// A binary runs against a schema newer than its own during a rollout and
// after a rollback, so a migration only adds what older binaries can ignore,
// such as a column with a default, an index or a setting. Removing or
// renaming something waits until no running binary uses it.
package migrations

import "embed"

// FS holds the migrations, applied in file-name order.
//
//go:embed *.sql
var FS embed.FS
