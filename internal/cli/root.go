// Package cli builds the skillsctl command tree.
package cli

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

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
		newBrowseCmd(),
		newBundleCmd(),
		newDiffCmd(),
		newDoctorCmd(),
		newGCCmd(),
		newInfoCmd(),
		newInstallCmd(),
		newLinkCmd(),
		newLintCmd(),
		newListCmd(),
		newNewCmd(),
		newOutdatedCmd(),
		newPackageCmd(),
		newPinCmd(),
		newRemoveCmd(),
		newRollbackCmd(),
		newSearchCmd(),
		newSyncCmd(),
		newUnpinCmd(),
		newUpdateCmd(),
		newVersionCmd(),
	)
	silenceUsageOnceRunning(root)
	return root
}

// silenceUsageOnceRunning marks a command's own SilenceUsage true only once
// its RunE begins. Cobra validates args and flags (including required-flag
// and mutually-exclusive-flag checks) before RunE ever runs, so an error
// from those checks always finds SilenceUsage still false: that is what
// lets run distinguish a usage mistake, which should show help, from a
// command's own runtime failure, which should not.
func silenceUsageOnceRunning(cmd *cobra.Command) {
	if inner := cmd.RunE; inner != nil {
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			return inner(cmd, args)
		}
	}
	for _, c := range cmd.Commands() {
		silenceUsageOnceRunning(c)
	}
}

// Execute runs the command tree and returns the process exit code. The
// context is canceled on SIGINT/SIGTERM so a command waiting on something
// cancellable — such as a contended state lock — stops promptly on Ctrl-C
// instead of leaving the OS's default disposition as the only way out.
func Execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return run(ctx, NewRootCmd())
}

// run executes a command tree and maps its error to an exit code. It is split
// from Execute so tests can drive it with their own args and buffers.
func run(ctx context.Context, root *cobra.Command) int {
	cmd, err := root.ExecuteContextC(ctx)
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

	var unhealthy *UnhealthyError
	if errors.As(err, &unhealthy) {
		root.PrintErrf("note: %v\n", err)
		return ExitUnhealthy
	}

	root.PrintErrf("error: %v\n", err)
	if !cmd.SilenceUsage {
		cmd.Println()
		_ = cmd.Help()
	}
	return ExitError
}
