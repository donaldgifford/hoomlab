package cmd

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/emit"
	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/steps"
	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/talos"
)

func newTalosCmd(opts *rootOptions) *cobra.Command {
	root := &cobra.Command{
		Use:   "talos",
		Short: "Talos cluster stages: secrets, artifacts, VMs, and bootstrap",
	}
	root.AddCommand(newTalosSecretsCmd(opts), newTalosEmitCmd(opts))
	return root
}

func newTalosEmitCmd(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "emit",
		Short: "Render the booty artifact tree and stage the boot assets",
		Long: `emit renders everything booty serves the PXE chain from, under
<output>/booty: the HCL catalog (one group per VM, pinned by the MAC
from the config), the complete machineconfig templates seeded from the
secrets bundle, the embedded iPXE chain script, a ready-to-run
booty-run.sh, and the Talos Image Factory kernel and initramfs.

Emission is pure rendering, so re-running is always safe: the check is
a byte-diff of a fresh render against what is already on disk, and boot
assets already staged are left alone. The CLI does not copy anything to
the booty host — moving the tree there is a documented operator step.

booty loads the catalog and templates once at startup, so any change
here means restarting the booty container.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cluster, err := loadCluster(cmd, opts)
			if err != nil {
				return err
			}
			bundle, err := talos.LoadSecretsBundle(opts.secretsPath())
			if errors.Is(err, talos.ErrSecretsMissing) {
				return fmt.Errorf("%w\nrun: bootstrap talos secrets", err)
			}
			if err != nil {
				return err
			}

			root := filepath.Join(opts.output, "booty")
			emitter := &emit.Emitter{Cluster: cluster, Bundle: bundle, Root: root}
			stage, err := emitter.Steps()
			if err != nil {
				return err
			}
			runner := steps.Runner{DryRun: opts.dryRun, Out: cmd.OutOrStdout()}
			res, err := runner.Run(cmd.Context(), stage)
			if err != nil {
				return fmt.Errorf("talos emit: %w", err)
			}
			if opts.dryRun {
				return nil
			}
			return emitSummary(cmd, root, res.Applied)
		},
	}
}

// emitSummary reports what emit did. A run that changed anything ends
// with the restart instruction: booty reads the catalog and templates
// once at startup, so a re-emit no one restarts serves stale artifacts
// and the failure shows up as a node booting the wrong config.
func emitSummary(cmd *cobra.Command, root string, applied int) error {
	if applied == 0 {
		_, err := fmt.Fprintf(cmd.OutOrStdout(),
			"✓ booty tree at %s is up to date (nothing to do)\nnext: bootstrap talos ipxe\n", root)
		if err != nil {
			return fmt.Errorf("write summary: %w", err)
		}
		return nil
	}
	if _, err := fmt.Fprintf(cmd.OutOrStdout(),
		"✓ booty tree written to %s (%d steps applied)\n"+
			"next: bootstrap talos ipxe\n\n"+
			"artifacts changed — restart the booty container (it loads the catalog\n"+
			"and templates once at startup): %s\n",
		root, applied, filepath.Join(root, "booty-run.sh")); err != nil {
		return fmt.Errorf("write summary: %w", err)
	}
	return nil
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
