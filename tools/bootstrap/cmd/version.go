package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newVersionCmd(version, commit, date string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := fmt.Fprintf(cmd.OutOrStdout(),
				"bootstrap %s (commit %s, built %s)\n", version, commit, date); err != nil {
				return fmt.Errorf("write version: %w", err)
			}
			return nil
		},
	}
}
