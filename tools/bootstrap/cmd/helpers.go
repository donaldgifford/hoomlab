package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/config"
)

// loadCluster loads and validates the config for a stage command,
// rendering any diagnostics to the command's stderr.
func loadCluster(cmd *cobra.Command, opts *rootOptions) (*config.Cluster, error) {
	cluster, diags := config.Load(opts.config)
	if _, err := diags.WriteTo(cmd.ErrOrStderr()); err != nil {
		return nil, fmt.Errorf("render diagnostics: %w", err)
	}
	if diags.HasErrors() {
		return nil, fmt.Errorf("configuration %s is invalid", opts.config)
	}
	return cluster, nil
}
