package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/talos"
)

func newTalosCmd(opts *rootOptions) *cobra.Command {
	root := &cobra.Command{
		Use:   "talos",
		Short: "Talos cluster stages: secrets, artifacts, VMs, and bootstrap",
	}
	root.AddCommand(newTalosSecretsCmd(opts))
	return root
}

func newTalosSecretsCmd(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "secrets",
		Short: "Generate the Talos secrets bundle (once, never overwritten)",
		Long: `secrets generates a fresh Talos machinery secrets bundle — the cluster
CA, tokens, and encryption keys every machineconfig is seeded from —
and writes it to --secrets (default: secrets.yaml next to the config
file).

The bundle IS the cluster identity: regenerating it would orphan every
node holding the old one. An existing file is therefore never touched —
re-running is a no-op. Back the file up; treat it like a private key.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cluster, err := loadCluster(cmd, opts)
			if err != nil {
				return err
			}
			path := opts.secretsPath()
			if opts.dryRun {
				return dryRunSecrets(cmd, path)
			}
			err = talos.GenerateSecretsBundle(path, cluster.Talos.Version)
			if errors.Is(err, talos.ErrSecretsExist) {
				if _, werr := fmt.Fprintf(cmd.OutOrStdout(),
					"✓ secrets bundle already exists at %s — leaving it alone\nnext: bootstrap talos emit\n",
					path); werr != nil {
					return fmt.Errorf("write summary: %w", werr)
				}
				return nil
			}
			if err != nil {
				return fmt.Errorf("talos secrets: %w", err)
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(),
				"✓ secrets bundle written to %s — back this file up, it is the cluster identity\nnext: bootstrap talos emit\n",
				path); err != nil {
				return fmt.Errorf("write summary: %w", err)
			}
			return nil
		},
	}
}

// dryRunSecrets reports what talos secrets would do without touching
// the filesystem beyond a stat.
func dryRunSecrets(cmd *cobra.Command, path string) error {
	state := "pending"
	if talos.SecretsBundleExists(path) {
		state = "done"
	}
	if _, err := fmt.Fprintf(cmd.OutOrStdout(),
		"%-10s generate secrets bundle at %s\n", state, path); err != nil {
		return fmt.Errorf("write dry-run: %w", err)
	}
	return nil
}
