// Package cli builds the skillsctl command tree.
package cli

import (
	"errors"

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
	root.AddCommand(
		newAdoptCmd(),
		newGCCmd(),
		newInstallCmd(),
		newLinkCmd(),
		newListCmd(),
		newOutdatedCmd(),
		newRemoveCmd(),
		newUpdateCmd(),
		newVersionCmd(),
	)
	return root
}

// Execute runs the command tree and returns the process exit code.
func Execute() int {
	return run(NewRootCmd())
}

// run executes a command tree and maps its error to an exit code. It is split
// from Execute so tests can drive it with their own args and buffers.
func run(root *cobra.Command) int {
	err := root.Execute()
	if err == nil {
		return ExitOK
	}

	var partial *PartialError
	if errors.As(err, &partial) {
		root.PrintErrf("note: %v\n", err)
		return ExitPartial
	}

	var finding *OutdatedError
	if errors.As(err, &finding) {
		root.PrintErrf("note: %v\n", err)
		return ExitOutdated
	}

	root.PrintErrf("error: %v\n", err)
	return ExitError
}
