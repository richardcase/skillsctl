package cli

import (
	"fmt"

	"github.com/richardcase/skillsctl/internal/diff"
	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/spf13/cobra"
)

func newDiffCmd() *cobra.Command {
	var against string

	cmd := &cobra.Command{
		Use:   "diff <name>",
		Short: "Show what an update would change, or what a rollback would undo",
		Long: "Compare an installed skill's revision against another one and print a\n" +
			"unified diff.\n\n" +
			"--against latest (the default) compares against what `update` would move to,\n" +
			"reading refs only — nothing is fetched or installed. --against previous\n" +
			"compares against the revision `rollback` would swap back to.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDiff(cmd, args[0], against)
		},
	}

	cmd.Flags().StringVar(&against, "against", "latest", "revision to compare against: latest or previous")
	return cmd
}

func runDiff(cmd *cobra.Command, name, against string) error {
	var mode diff.Against
	switch against {
	case "latest":
		mode = diff.Latest
	case "previous":
		mode = diff.Previous
	default:
		return fmt.Errorf("--against must be latest or previous, not %q", against)
	}

	e, err := newEnv()
	if err != nil {
		return err
	}
	h, err := e.openState()
	if err != nil {
		return err
	}
	defer func() { _ = h.Close() }()

	r, ok := h.DB.Receipts[name]
	if !ok {
		return h.DB.NotInstalled(name)
	}

	unified, err := diff.Check(cmd.Context(), gitx.New(), newOCI(), e.store, r, mode)
	if err != nil {
		return err
	}
	if unified == "" {
		cmd.Println("no changes")
		return nil
	}
	cmd.Print(unified)
	return nil
}
