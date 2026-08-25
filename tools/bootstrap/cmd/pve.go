package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/pve"
	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/steps"
	"github.com/donaldgifford/proxmox-go-sdk/proxmox/nodes"
)

func newPVECmd(opts *rootOptions) *cobra.Command {
	root := &cobra.Command{
		Use:   "pve",
		Short: "Proxmox cluster stages: formation and certificates",
	}
	root.AddCommand(newPVEFormCmd(opts), newPVECertsCmd(opts))
	return root
}

func newPVECertsCmd(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "certs",
		Short: "Set up ACME certificates on every cluster node",
		Long: `certs registers the ACME account, registers the DNS-01 challenge
plugin with the provider credentials from the config, wires every
node's certificate domain (<node>.<domain>), and orders each node's
certificate.

Renewal is the same command re-run: a certificate with less than 30
days of validity left goes pending again and is reordered. A rotated
provider token is detected and pushed the same way.`,
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
			certifier := &pve.Certifier{
				Cluster: cluster,
				Nodes:   client.Nodes(),
				Tasks:   client.Tasks(),
				DialRoot: func(ctx context.Context) (*nodes.Service, error) {
					root, err := pve.NewRootClient(ctx, cluster)
					if err != nil {
						return nil, err
					}
					return root.Nodes(), nil
				},
			}
			runner := steps.Runner{DryRun: opts.dryRun, Out: cmd.OutOrStdout()}
			res, err := runner.Run(cmd.Context(), certifier.Steps())
			if err != nil {
				return fmt.Errorf("pve certs: %w", err)
			}
			if opts.dryRun {
				return nil
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(),
				"✓ acme certificates converged on %d nodes (%d steps applied)\nnext: bootstrap talos secrets\n",
				len(cluster.PVE.Nodes), res.Applied); err != nil {
				return fmt.Errorf("write summary: %w", err)
			}
			return nil
		},
	}
}

func newPVEFormCmd(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "form",
		Short: "Form the Proxmox cluster from the configured nodes",
		Long: `form creates the cluster on the primary node, joins the remaining
nodes one at a time (each join waits for corosync membership and
quorum before the next), and verifies the formed cluster is quorate.

Already-formed state is skipped: re-running after any interruption
converges on the remaining steps. Joining wipes a node's local
configuration — nodes other than the primary must be fresh installs.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cluster, err := loadCluster(cmd, opts)
			if err != nil {
				return err
			}
			former := &pve.Former{Cluster: cluster, Dial: pve.NewDialer()}
			stage, err := former.Steps()
			if err != nil {
				return err
			}
			runner := steps.Runner{DryRun: opts.dryRun, Out: cmd.OutOrStdout()}
			res, err := runner.Run(cmd.Context(), stage)
			if err != nil {
				return fmt.Errorf("pve form: %w", err)
			}
			if opts.dryRun {
				return nil
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(),
				"✓ cluster %q formed and quorate (%d of %d steps applied)\nnext: bootstrap pve certs\n",
				cluster.Name, res.Applied, len(stage)); err != nil {
				return fmt.Errorf("write summary: %w", err)
			}
			return nil
		},
	}
}
