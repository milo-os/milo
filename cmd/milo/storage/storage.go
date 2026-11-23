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
	var dsn, sinkConfig string
	var sink cockroach.Sink
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
			if sinkConfig != "" && sink == "" {
				return fmt.Errorf("--cockroachdb-changefeed-sink-config needs --cockroachdb-changefeed-sink")
			}
			if err := cockroach.Migrate(cmd.Context(), dsn, cockroach.ChangefeedSink(sink), cockroach.ChangefeedSinkConfig(sinkConfig)); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "storage schema is up to date")
			return nil
		},
	}
	cmd.Flags().StringVar(&dsn, "cockroachdb-dsn", "", "Connection string for the CockroachDB database, the same as the API server's --cockroachdb-dsn.")
	cmd.Flags().StringVar(&sinkConfig, "cockroachdb-changefeed-sink-config", "", "With --cockroachdb-changefeed-sink: Producer settings for the changefeed job, as the JSON object CockroachDB takes as kafka_sink_config, such as {\"Flush\": {\"Frequency\": \"2ms\", \"Messages\": 500}}. RequiredAcks defaults to ALL. Changing it replaces the job, which carries on from where the old one got to. Experimental.")
	cmd.Flags().Var(&sink, "cockroachdb-changefeed-sink", "Keep one changefeed job writing every change to this kafka:// sink, for API servers with --cockroachdb-watch-feed=kafka. \"none\" cancels the job, and leaving it unset leaves any job as it is. The database user also needs the EXTERNALIOIMPLICITACCESS privilege. Experimental.")
	return cmd
}
