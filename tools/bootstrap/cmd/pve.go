package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/pve"
	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/steps"
)

func newPVECmd(opts *rootOptions) *cobra.Command {
	root := &cobra.Command{
		Use:   "pve",
		Short: "Proxmox cluster stages: formation and certificates",
	}
	root.AddCommand(newPVEFormCmd(opts))
	return root
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
			applied, err := runner.Run(cmd.Context(), stage)
			if err != nil {
				return fmt.Errorf("pve form: %w", err)
			}
			if opts.dryRun {
				return nil
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(),
				"✓ cluster %q formed and quorate (%d of %d steps applied)\nnext: bootstrap pve certs\n",
				cluster.Name, applied, len(stage)); err != nil {
				return fmt.Errorf("write summary: %w", err)
			}
			return nil
		},
	}
}
