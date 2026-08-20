package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newValidateCmd(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Parse and validate the bootstrap config file",
		Long: `validate loads the config file, resolves env() references, and runs
every decode-time and semantic check. It reports all problems at once
as position-aware diagnostics and exits non-zero on any error — a
missing export or a config mistake fails here, before any stage
touches an API.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cluster, err := loadCluster(cmd, opts)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(),
				"✓ %s: cluster %q is valid (%d pve nodes, %d talos nodes)\n",
				opts.config, cluster.Name,
				len(cluster.PVE.Nodes), len(cluster.Talos.Nodes)); err != nil {
				return fmt.Errorf("write summary: %w", err)
			}
			return nil
		},
	}
}
