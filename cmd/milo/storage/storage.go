// Package storage holds commands that manage the API server's storage.
package storage

import (
	"fmt"

	"github.com/spf13/cobra"

	"go.miloapis.com/milo/pkg/storage/cockroach"
)

// NewCommand returns the storage command and its subcommands.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "storage",
		Short: "Manage the API server's storage",
	}
	cmd.AddCommand(newMigrateCommand())
	return cmd
}

func newMigrateCommand() *cobra.Command {
	var dsn string
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Apply pending CockroachDB storage migrations",
		Long: "Applies every pending schema migration to the CockroachDB database that stores the API server's resources. " +
			"Run it once per rollout, before API servers on the new version start, because a server won't start on a schema older than its own. " +
			"Running it again, or from several places at once, is safe.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if dsn == "" {
				return fmt.Errorf("--cockroachdb-dsn is required")
			}
			if err := cockroach.Migrate(cmd.Context(), dsn); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "storage schema is up to date")
			return nil
		},
	}
	cmd.Flags().StringVar(&dsn, "cockroachdb-dsn", "", "Connection string for the CockroachDB database, the same as the API server's --cockroachdb-dsn.")
	return cmd
}
