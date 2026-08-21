package cmd

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/emit"
	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/pve"
	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/steps"
	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/talos"
)

func newTalosCmd(opts *rootOptions) *cobra.Command {
	root := &cobra.Command{
		Use:   "talos",
		Short: "Talos cluster stages: secrets, artifacts, VMs, and bootstrap",
	}
	root.AddCommand(
		newTalosSecretsCmd(opts),
		newTalosEmitCmd(opts),
		newTalosIPXECmd(opts),
		newTalosVMsCmd(opts),
	)
	return root
}

func newTalosIPXECmd(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "ipxe",
		Short: "Build the iPXE binary with the booty chain script embedded",
		Long: `ipxe builds <output>/booty/boot/ipxe.efi in a container: a pinned
iPXE source tree compiled with the emitted embed.ipxe baked in. That
embedded script is what makes network boot work at all — iPXE sends no
machine identity on its own, so the chain script is what turns a PXE
request into booty's /ipxe?mac=… lookup.

The build is skipped unless it is needed: pending only when the binary
is missing or when the chain script the config renders differs from the
one the existing binary was built with. In practice that means a
changed booty.url triggers a rebuild and nothing else does.

Requires docker, and takes a few minutes. Run talos emit first — that
stage owns embed.ipxe.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cluster, err := loadCluster(cmd, opts)
			if err != nil {
				return err
			}
			root := filepath.Join(opts.output, "booty")
			builder := &emit.IPXEBuilder{Root: root, BootyURL: cluster.Talos.Booty.URL}
			runner := steps.Runner{DryRun: opts.dryRun, Out: cmd.OutOrStdout()}
			res, err := runner.Run(cmd.Context(), builder.Steps())
			if err != nil {
				return fmt.Errorf("talos ipxe: %w", err)
			}
			if opts.dryRun {
				return nil
			}
			summary := "✓ ipxe.efi is already built for this booty url (nothing to do)\n"
			if res.Applied > 0 {
				summary = fmt.Sprintf("✓ ipxe.efi built at %s\n",
					filepath.Join(root, "boot", "ipxe.efi"))
			}
			if _, err := fmt.Fprint(cmd.OutOrStdout(), summary+"next: bootstrap talos vms\n"); err != nil {
				return fmt.Errorf("write summary: %w", err)
			}
			return nil
		},
	}
}

func newTalosVMsCmd(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "vms",
		Short: "Create and start the Talos VMs on their Proxmox nodes",
		Long: `vms creates every configured Talos VM on its target Proxmox node and
starts it. The VM settings are not left to Proxmox defaults: UEFI
without pre-enrolled Secure Boot keys, a VirtIO RNG, cpu=host, and a
disk-first boot order with a PXE fallback are each required for a Talos
node to network boot and then install itself.

Re-running converges: an existing VM is left alone and a stopped one is
started, so an interrupted run creates only what is missing.

The NIC MAC is the one from the config — the same MAC the emitted booty
catalog selects on, so the machine gets its own machineconfig.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cluster, err := loadCluster(cmd, opts)
			if err != nil {
				return err
			}
			client, err := pve.NewClient(cmd.Context(), cluster)
			if err != nil {
				return err
			}
			provisioner := &pve.Provisioner{
				Cluster: cluster,
				QEMU:    client.QEMU,
				Tasks:   client.Tasks(),
			}
			runner := steps.Runner{DryRun: opts.dryRun, Out: cmd.OutOrStdout()}
			res, err := runner.Run(cmd.Context(), provisioner.Steps())
			if err != nil {
				return fmt.Errorf("talos vms: %w", err)
			}
			if opts.dryRun {
				return nil
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(),
				"✓ %d talos vms created and running (%d steps applied)\n"+
					"the VMs are now PXE booting from booty; watch progress there\n"+
					"next: bootstrap talos bootstrap\n",
				len(cluster.Talos.Nodes), res.Applied); err != nil {
				return fmt.Errorf("write summary: %w", err)
			}
			return nil
		},
	}
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
