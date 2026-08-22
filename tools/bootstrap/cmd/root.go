// Package cmd wires the bootstrap CLI's cobra command tree.
package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// rootOptions carries the global flags shared by every stage command.
// Per DESIGN-0001 OQ-8 these are CLI-only concerns: anything the
// Hoomlab service would also consume lives in the HCL config file
// instead.
type rootOptions struct {
	config   string
	output   string
	secrets  string
	dryRun   bool
	logLevel string
}

// secretsPath resolves the Talos secrets bundle location: --secrets
// when set, otherwise secrets.yaml next to the config file — keeping
// the bundle adjacent to the config it belongs to by default.
func (o *rootOptions) secretsPath() string {
	if o.secrets != "" {
		return o.secrets
	}
	return filepath.Join(filepath.Dir(o.config), "secrets.yaml")
}

// setupLogging replaces the default slog handler with one honoring
// --log-level.
func (o *rootOptions) setupLogging() error {
	var level slog.Level
	if err := level.UnmarshalText([]byte(o.logLevel)); err != nil {
		return fmt.Errorf("parse --log-level %q: %w", o.logLevel, err)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
	return nil
}

func newRootCmd(version, commit, date string) *cobra.Command {
	opts := &rootOptions{}

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
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			return opts.setupLogging()
		},
	}

	// The full global-flag surface is DESIGN-0001 contract, registered
	// up front: --dry-run is consumed from the Phase 2 stage commands,
	// --output/--secrets from the Phase 4 talos commands.
	pf := root.PersistentFlags()
	pf.StringVar(&opts.config, "config", "bootstrap.hcl", "bootstrap config file")
	pf.StringVar(&opts.output, "output", "./bootstrap-out", "output root for emitted files")
	pf.StringVar(&opts.secrets, "secrets", "",
		"Talos secrets bundle (default: secrets.yaml next to the config file)")
	pf.BoolVar(&opts.dryRun, "dry-run", false, "print pending steps without applying them")
	pf.StringVar(&opts.logLevel, "log-level", "info", "log level: debug, info, warn, error")

	root.AddCommand(
		newValidateCmd(opts),
		newPVECmd(opts),
		newTalosCmd(opts),
		newVersionCmd(version, commit, date),
	)
	return root
}

// Execute builds the command tree and runs it, returning any execution
// error for main to report.
func Execute(version, commit, date string) error {
	return newRootCmd(version, commit, date).Execute()
}
