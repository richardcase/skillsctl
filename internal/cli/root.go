// Package cli builds the skillsctl command tree.
package cli

import (
	"github.com/spf13/cobra"
)

// NewRootCmd builds the command tree. Tests construct a fresh tree per case.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "skillsctl",
		Short:         "Install, update and remove agent skills",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newVersionCmd())
	return root
}

// Execute runs the command tree and returns the process exit code.
func Execute() int {
	root := NewRootCmd()
	if err := root.Execute(); err != nil {
		root.PrintErrf("error: %v\n", err)
		return 1
	}
	return 0
}
