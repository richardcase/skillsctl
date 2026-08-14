package cli

import (
	"fmt"
	"time"

	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/state"
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
			name := args[0]

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
				return fmt.Errorf("%q is not installed", name)
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

			var p plan.Plan
			var keep []state.Link
			for _, l := range receipt.Links {
				if len(drop) > 0 && !drop[l.Target] {
					keep = append(keep, l)
					continue
				}
				p.Add(plan.Unlink{Target: l.Target, LinkPath: l.Path})
			}

			if p.IsEmpty() {
				return fmt.Errorf("%q is not linked into %v", name, agents)
			}

			if len(keep) == 0 {
				p.Add(plan.Forget{Name: name})
			} else {
				updated := *receipt
				updated.Links = keep
				updated.UpdatedAt = time.Now().UTC()
				p.Add(plan.Record{Receipt: updated})
			}

			if dryRun {
				for _, line := range p.Describe() {
					cmd.Println(line)
				}
				return nil
			}

			ex := &plan.Executor{DB: h.DB, Out: cmd.OutOrStdout()}
			if err := ex.Apply(cmd.Context(), p); err != nil {
				return err
			}
			if err := h.Commit(); err != nil {
				return fmt.Errorf("%w\nthe links were removed but the receipt was not updated; re-run this command to repair", err)
			}

			cmd.Printf("removed %s\n", name)
			hintReclaimable(cmd, e, h.DB)
			return nil
		},
	}

	cmd.Flags().StringSliceVarP(&agents, "agent", "a", nil, "remove from these agents only")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change without changing it")
	return cmd
}
