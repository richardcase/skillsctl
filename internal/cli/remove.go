package cli

import (
	"fmt"

	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/spf13/cobra"
)

func newRemoveCmd() *cobra.Command {
	var (
		agents []string
		dryRun bool
	)

	cmd := &cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"uninstall", "rm"},
		Short:   "Remove an installed skill",
		Long: "Remove a skill from every agent, or from just the agents named with -a.\n\n" +
			"Removing from some agents keeps the receipt; removing the last link forgets it.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRemove(cmd, args[0], agents, dryRun)
		},
	}

	cmd.Flags().StringSliceVarP(&agents, "agent", "a", nil, "remove from these agents only")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change without changing it")
	return cmd
}

// runRemove is `remove`'s body, factored out so `browse` can remove a chosen
// batch of names one at a time through the exact same path instead of a copy
// of it.
func runRemove(cmd *cobra.Command, name string, agents []string, dryRun bool) error {
	e, err := newEnv()
	if err != nil {
		return err
	}
	h, err := e.openState()
	if err != nil {
		return err
	}
	defer func() { _ = h.Close() }()

	receipt, ok := h.DB.Receipts[name]
	if !ok {
		return h.DB.NotInstalled(name)
	}
	ch, err := e.channels().ForReceipt(receipt)
	if err != nil {
		return err
	}

	drop := map[string]bool{}
	if len(agents) > 0 {
		selected, err := e.cfg.Select(agents)
		if err != nil {
			return err
		}
		for _, t := range selected {
			drop[t.Name] = true
		}
	}

	p, err := ch.Remove(*receipt, drop)
	if err != nil {
		return err
	}
	if p.IsEmpty() {
		return fmt.Errorf("%q is not linked into %v", name, agents)
	}

	if dryRun {
		for _, line := range p.Describe() {
			cmd.Println(line)
		}
		return nil
	}

	ex := &plan.Executor{DB: h.DB, Out: cmd.OutOrStdout(), Run: newRunner()}
	if err := ex.Apply(cmd.Context(), p); err != nil {
		return err
	}
	if err := h.Commit(); err != nil {
		return fmt.Errorf("%w\nthe links were removed but the receipt was not updated; re-run this command to repair", err)
	}

	cmd.Printf("removed %s\n", name)
	hintReclaimable(cmd, e, h.DB)
	return nil
}
