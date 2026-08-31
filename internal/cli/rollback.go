package cli

import (
	"context"
	"fmt"

	"github.com/richardcase/skillsctl/internal/channel"
	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/spf13/cobra"
)

func newRollbackCmd() *cobra.Command {
	var (
		force  bool
		dryRun bool
	)

	cmd := &cobra.Command{
		Use:   "rollback <name>...",
		Short: "Undo the last update, swapping a skill back onto its previous revision",
		Long: "Point one or more skills back at the revision they were on before their\n" +
			"last update, keeping their name, their agents and their pin.\n\n" +
			"Rollback is a toggle: running it again undoes itself, swapping back to the\n" +
			"revision the first rollback moved away from. A skill that has never been\n" +
			"updated has nothing to roll back to. `skillsctl diff <name> --against\n" +
			"previous` shows what a rollback would change before you run it.\n\n" +
			"A skill that has been edited through its symlink is skipped unless --force,\n" +
			"since rolling it back would discard those edits.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRollback(cmd, args, force, dryRun)
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "roll back even a skill that has been edited since it was installed")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change without changing it")
	return cmd
}

// rollbackEntry is what happened to one name: the verdict the channel
// returned, or why nothing could be made of it.
type rollbackEntry struct {
	name string
	v    channel.Verdict
	err  error
}

func runRollback(cmd *cobra.Command, names []string, force, dryRun bool) error {
	ctx := cmd.Context()

	e, err := newEnv()
	if err != nil {
		return err
	}
	h, err := e.openState(cmd.Context())
	if err != nil {
		return err
	}
	defer func() { _ = h.Close() }()

	reg := e.channels()
	entries := make([]rollbackEntry, 0, len(names))
	taken := make(map[string]bool, len(names))

	var p plan.Plan
	for _, name := range names {
		// Naming a skill twice is one request for it, not two.
		if taken[name] {
			continue
		}
		taken[name] = true

		en, ops := rollbackOne(ctx, reg, h.DB, name, force)
		p.Add(ops.Ops...)
		entries = append(entries, en)
	}

	if dryRun {
		for _, line := range p.Describe() {
			cmd.Println(line)
		}
		reportRollback(cmd, entries, true)
		return rollbackExit(entries)
	}

	if !p.IsEmpty() {
		ex := &plan.Executor{DB: h.DB, Out: cmd.OutOrStdout(), Run: newRunner()}
		if err := ex.Apply(ctx, p); err != nil {
			return err
		}
		if err := h.Commit(); err != nil {
			return fmt.Errorf("%w\nthe skill was re-linked but the receipt was not saved; re-run this command to repair", err)
		}
	}

	reportRollback(cmd, entries, false)
	return rollbackExit(entries)
}

// rollbackOne resolves one name to a verdict and the ops that carry it out.
// Every way it can fail is a verdict rather than an error, so one unknown
// name never hides what could be done with the rest.
func rollbackOne(ctx context.Context, reg channel.Registry, db *state.DB, name string, force bool) (rollbackEntry, plan.Plan) {
	en := rollbackEntry{name: name}

	r, ok := db.Receipts[name]
	if !ok {
		en.err = db.NotInstalled(name)
		return en, plan.Plan{}
	}

	ch, err := reg.ForReceipt(r)
	if err != nil {
		en.err = err
		return en, plan.Plan{}
	}

	p, v, err := ch.Rollback(ctx, *r, force)
	if err != nil {
		en.err = err
		return en, plan.Plan{}
	}
	en.v = v
	return en, p
}

// reportRollback writes one line per name. A dry run has already printed the
// plan, so it adds only the names that produced no op: the failures.
func reportRollback(cmd *cobra.Command, entries []rollbackEntry, dryRun bool) {
	for _, en := range entries {
		switch {
		case en.err != nil:
			cmd.Printf("skipped %s: %s\n", en.name, reason(en.err))
		case dryRun:
		default:
			cmd.Printf("rolled back %s to %s\n", en.name, shortSha(en.v.Latest))
		}
	}
}

// rollbackExit turns the report into an exit code, once the reasons are
// already on screen.
func rollbackExit(entries []rollbackEntry) error {
	var done, skipped int
	for _, en := range entries {
		if en.err != nil {
			skipped++
			continue
		}
		done++
	}

	switch {
	case skipped == 0:
		return nil
	case done > 0:
		return partialf("%s rolled back, %s skipped", count(done, "skill"), count(skipped, "skill"))
	default:
		return fmt.Errorf("nothing was rolled back: %s skipped, for the reasons above", count(skipped, "skill"))
	}
}
