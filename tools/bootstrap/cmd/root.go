// Package cmd wires the bootstrap CLI's cobra command tree.
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newRootCmd(version, commit, date string) *cobra.Command {
	root := &cobra.Command{
		Use:   "bootstrap",
		Short: "Bootstrap a Proxmox + Talos cluster from HCL configuration",
		Long: `bootstrap takes bare Proxmox nodes to a formed Proxmox cluster with a
Talos Kubernetes cluster running on it — the foundation Hoomlab runs on.

It is deliberately operator-driven: stages are separate commands the
operator runs, inspects, and re-runs. Re-runs converge — with no state
file, the configuration files and the world are the only two truths.`,
		Version:       fmt.Sprintf("%s (%s, %s)", version, commit, date),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	return root
}

// Execute builds the command tree and runs it, returning any execution
// error for main to report.
func Execute(version, commit, date string) error {
	return newRootCmd(version, commit, date).Execute()
}
