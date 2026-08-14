package cli

import (
	"fmt"

	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/update"
	"github.com/spf13/cobra"
)

func newUpdateCmd() *cobra.Command {
	var (
		force  bool
		dryRun bool
	)

	cmd := &cobra.Command{
		Use:   "update [name...]",
		Short: "Move skills to a newer revision of the ref they track",
		Long: "Re-point each skill at the head of the ref it tracks, keeping its name, its\n" +
			"agents and its pin.\n\n" +
			"With no arguments every skill is updated except the pinned ones; naming a skill\n" +
			"updates it even when it is pinned, re-pinning it at the new commit. A skill that\n" +
			"has been edited through its symlink is skipped unless --force, since updating it\n" +
			"would discard the edit. The old revision stays on disk until `skillsctl gc`.",
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := newEnv()
			if err != nil {
				return err
			}

			// The state lock is taken before anything is written to the store
			// and held until this command exits, so a concurrent gc can never
			// collect the revision this update is about to link.
			h, err := e.openState()
			if err != nil {
				return err
			}
			defer func() { _ = h.Close() }()

			receipts := h.DB.List()
			if len(receipts) == 0 {
				cmd.Println("No skills installed.")
				return nil
			}

			entries, p, err := update.Plan(cmd.Context(), gitx.New(), e.store, receipts,
				update.Options{Names: args, Force: force})
			if err != nil {
				return err
			}

			if dryRun {
				for _, line := range p.Describe() {
					cmd.Println(line)
				}
				reportUpdate(cmd, entries, dryRun)
				return updateExit(entries)
			}

			if !p.IsEmpty() {
				ex := &plan.Executor{DB: h.DB, Out: cmd.OutOrStdout()}
				if err := ex.Apply(cmd.Context(), p); err != nil {
					return err
				}
				if err := h.Commit(); err != nil {
					return fmt.Errorf("%w\nthe skills were re-linked but the receipts were not saved; re-run this command to repair", err)
				}
			}

			reportUpdate(cmd, entries, dryRun)
			if !p.IsEmpty() {
				hintReclaimable(cmd, e, h.DB)
			}
			return updateExit(entries)
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "update even a skill that has been edited since it was installed")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change without changing it")
	return cmd
}

// reportUpdate writes one line per skill that is not simply current. A run
// where everything was already up to date still says so, rather than printing
// nothing and leaving the user wondering whether it ran.
func reportUpdate(cmd *cobra.Command, entries []update.Entry, dryRun bool) {
	verb := "updated"
	if dryRun {
		verb = "would update"
	}

	var interesting int
	for _, e := range entries {
		line := updateLine(e, verb)
		if line == "" {
			continue
		}
		interesting++
		cmd.Println(line)
	}
	if interesting == 0 {
		cmd.Println("Everything is up to date.")
	}
}

// updateLine renders one entry, or "" for a skill that needs no comment.
func updateLine(e update.Entry, verb string) string {
	switch e.Status {
	case update.StatusUpdated:
		line := fmt.Sprintf("%s %s %s -> %s", verb, e.Name, shortSha(e.Current), shortSha(e.Latest))
		if e.Note != "" {
			line += fmt.Sprintf(" (%s)", e.Note)
		}
		return line
	case update.StatusDirty:
		return fmt.Sprintf("skipped %s: edited since it was installed; pass --force to update it anyway", e.Name)
	case update.StatusPinned:
		return fmt.Sprintf("skipped %s: pinned at %s; name it explicitly to update it", e.Name, shortSha(e.Current))
	case update.StatusSkipped:
		return fmt.Sprintf("skipped %s: the %s channel has no ref to update from", e.Name, e.Channel)
	case update.StatusError:
		return fmt.Sprintf("skipped %s: %s", e.Name, e.Error)
	default:
		return ""
	}
}

// updateExit turns the report into an exit code. Work that was skipped is the
// difference between "all of it" and "some of it", so it sets ExitPartial when
// something else did move and ExitError when nothing did — a run that updated
// nothing it was asked to update is not a success, whatever the reason.
//
// A pin is not a skip in that sense: skipping it is what the user asked for by
// pinning, so like outdated it never sets a code on its own.
func updateExit(entries []update.Entry) error {
	var updated, skipped int
	for _, e := range entries {
		switch e.Status {
		case update.StatusUpdated:
			updated++
		case update.StatusDirty, update.StatusError, update.StatusSkipped:
			skipped++
		}
	}

	switch {
	case skipped == 0:
		return nil
	case updated > 0:
		return partialf("%s updated, %s skipped", count(updated, "skill"), count(skipped, "skill"))
	default:
		return fmt.Errorf("nothing was updated: %s skipped, for the reasons above", count(skipped, "skill"))
	}
}
