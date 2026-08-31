package cli

import (
	"context"
	"fmt"

	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/target"
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
			return runUpdate(cmd, args, force, dryRun)
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "update even a skill that has been edited since it was installed")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change without changing it")
	return cmd
}

// runUpdate is `update`'s body, factored out so `browse` can move a chosen
// batch of names through the exact same plan/settle/report/exit path instead
// of a copy of it.
func runUpdate(cmd *cobra.Command, names []string, force, dryRun bool) error {
	e, err := newEnv()
	if err != nil {
		return err
	}

	// The state lock is taken before anything is written to the store
	// and held until this command exits, so a concurrent gc can never
	// collect the revision this update is about to link.
	h, err := e.openState(cmd.Context())
	if err != nil {
		return err
	}
	defer func() { _ = h.Close() }()

	receipts := h.DB.List()
	if len(receipts) == 0 {
		cmd.Println("No skills installed.")
		return nil
	}

	entries, p, err := update.Plan(cmd.Context(), e.channels(), receipts,
		update.Options{Names: names, Force: force})
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

	var serr error
	var linkSkips []string
	if !p.IsEmpty() {
		ex := &plan.Executor{DB: h.DB, Out: cmd.OutOrStdout(), Run: newRunner()}
		if err := ex.Apply(cmd.Context(), p); err != nil {
			return err
		}

		// A channel whose agent chooses the version can only be asked
		// once it has run, so the entries are corrected before they are
		// reported rather than after.
		entries, linkSkips, serr = settleUpdated(cmd.Context(), ex, e, h.DB, entries)

		if err := h.Commit(); err != nil {
			return fmt.Errorf("%w\nthe skills were re-linked but the receipts were not saved; re-run this command to repair", err)
		}
	}

	reportUpdate(cmd, entries, dryRun)
	reportSkipped(cmd, linkSkips)
	if !p.IsEmpty() {
		hintReclaimable(cmd, e, h.DB)
	}
	if serr != nil {
		cmd.Printf("warning: %v\n", serr)
	}
	if err := updateExit(entries); err != nil {
		return err
	}
	if serr != nil {
		return partialf("the update ran, but a version could not be read back")
	}
	return nil
}

// linkedTargets is the agents a receipt already reaches, as targets, for the
// reconcile that follows an update.
//
// An update re-points what is there; it does not fan out further. An agent that
// has since been deleted from the config is passed over rather than unlinked:
// config drift is not this command's business.
func linkedTargets(cfg target.Config, r state.Receipt) []target.Target {
	held := make(map[string]bool, len(r.Links))
	for _, l := range r.Links {
		held[l.Target] = true
	}

	var out []target.Target
	for _, t := range cfg.Targets {
		if held[t.Name] {
			out = append(out, t)
		}
	}
	return out
}

// settleUpdated completes the receipts an update just wrote, for the channels
// that cannot know a version until their agent has run, and folds the result
// back into the entries so the report says what actually happened rather than
// what was planned.
//
// It groups by channel for the same reason update.Plan does: a channel is asked
// once, and answers for everything it owns.
func settleUpdated(ctx context.Context, ex *plan.Executor, e *env, db *state.DB, entries []update.Entry) ([]update.Entry, []string, error) {
	reg := e.channels()
	grouped := map[string][]state.Receipt{}
	var order []string

	for _, en := range entries {
		if en.Status != update.StatusUpdated {
			continue
		}
		r, ok := db.Receipts[en.Name]
		if !ok {
			continue
		}
		if _, seen := grouped[en.Channel]; !seen {
			order = append(order, en.Channel)
		}
		grouped[en.Channel] = append(grouped[en.Channel], *r)
	}

	var settled []state.Receipt
	var skipped []string
	var firstErr error
	for _, name := range order {
		ch, err := reg.For(source.Channel(name))
		if err != nil {
			continue
		}
		got, err := settle(ctx, ex, ch, grouped[name])
		if err != nil && firstErr == nil {
			firstErr = err
		}

		// The links follow the settle for the same reason they follow an
		// install: a channel whose agent chose the directory has only now been
		// told where the new one is.
		got, skips, lerr := relink(ctx, ex, ch, got, func(r state.Receipt) []target.Target {
			return linkedTargets(e.cfg, r)
		})
		skipped = append(skipped, skips...)
		if lerr != nil && firstErr == nil {
			firstErr = lerr
		}

		settled = append(settled, got...)
	}
	return update.Reconcile(entries, settled), skipped, firstErr
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
		// An empty Latest is not missing information, it is the answer: the
		// plugin channel cannot name the version its agent will install until
		// the agent has installed it, so a --dry-run says so rather than
		// printing an arrow pointing at nothing.
		line := fmt.Sprintf("%s %s %s -> %s", verb, e.Name, shortSha(e.Current), shortSha(e.Latest))
		if e.Latest == "" {
			line = fmt.Sprintf("%s %s from %s, to whatever version its source publishes", verb, e.Name, shortSha(e.Current))
		}
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
// Two statuses are deliberately not skips in that sense, because neither is
// work that failed. A pin was skipped because the user asked for it to be, by
// pinning. A channel with nothing to update from — a local skill is whatever
// its directory says right now — had nothing to do rather than something it
// could not do. Neither sets a code on its own; `skillsctl update` on a machine
// holding only local skills has succeeded completely.
func updateExit(entries []update.Entry) error {
	var updated, skipped int
	for _, e := range entries {
		switch e.Status {
		case update.StatusUpdated:
			updated++
		case update.StatusDirty, update.StatusError:
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
