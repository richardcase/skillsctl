package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/richardcase/skillsctl/internal/channel"
	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/spf13/cobra"
)

// pinOpts is the flag set of one pin or unpin invocation. The two commands run
// the same code and differ in three things: the direction, whether there is a
// --ref to honour, and the words they report in.
type pinOpts struct {
	on     bool
	ref    string
	dryRun bool
}

func newPinCmd() *cobra.Command {
	o := pinOpts{on: true}

	cmd := &cobra.Command{
		Use:   "pin <name>...",
		Short: "Freeze a skill at the revision it is installed at",
		Long: "Freeze one or more skills at the revision they are already installed at, so\n" +
			"`update` leaves them alone.\n\n" +
			"Nothing is fetched and no symlink moves: pinning writes one field on the\n" +
			"receipt. A pinned skill tracks no ref, so `pin` says which one it dropped, and\n" +
			"`outdated` still reports it when that ref moves — a pin never hides that.\n\n" +
			"Naming a pinned skill in `update` still updates it, re-pinning it at the new\n" +
			"commit. `skillsctl unpin` releases it for good.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPin(cmd, args, o)
		},
	}

	cmd.Flags().BoolVar(&o.dryRun, "dry-run", false, "show what would change without changing it")
	return cmd
}

func newUnpinCmd() *cobra.Command {
	var o pinOpts

	cmd := &cobra.Command{
		Use:   "unpin <name>...",
		Short: "Let a pinned skill follow a ref again",
		Long: "Release the pin on one or more skills, so `update` moves them again.\n\n" +
			"A pin holds no ref, so an unpinned skill tracks the repository's default branch\n" +
			"unless --ref names another. The ref is checked before it is recorded: one that\n" +
			"does not resolve would fail the next `update` instead, naming a repository\n" +
			"rather than this command.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPin(cmd, args, o)
		},
	}

	cmd.Flags().StringVar(&o.ref, "ref", "", "branch or tag to track from now on (default: the repository's default branch)")
	cmd.Flags().BoolVar(&o.dryRun, "dry-run", false, "show what would change without changing it")
	return cmd
}

// pinEntry is what happened to one name: the receipt as it stood, what the
// channel made of it, or why nothing could be made of it.
type pinEntry struct {
	name   string
	before state.Receipt
	res    channel.PinResult
	err    error
}

func runPin(cmd *cobra.Command, names []string, o pinOpts) error {
	ctx := cmd.Context()

	e, err := newEnv()
	if err != nil {
		return err
	}

	// Nothing on disk changes, but the receipts do, so the lock is held for the
	// whole read-modify-write like every other command that writes one.
	h, err := e.openState()
	if err != nil {
		return err
	}
	defer func() { _ = h.Close() }()

	reg := e.channels()
	entries := make([]pinEntry, 0, len(names))
	taken := make(map[string]bool, len(names))

	var p plan.Plan
	for _, name := range names {
		// Naming a skill twice is one request for it, not two.
		if taken[name] {
			continue
		}
		taken[name] = true

		en, ops := pinOne(ctx, reg, h.DB, name, o)
		p.Add(ops.Ops...)
		entries = append(entries, en)
	}

	if o.dryRun {
		for _, line := range p.Describe() {
			cmd.Println(line)
		}
		reportPin(cmd, entries, o, true)
		return pinExit(entries, o)
	}

	if !p.IsEmpty() {
		ex := &plan.Executor{DB: h.DB, Out: cmd.OutOrStdout(), Run: newRunner()}
		if err := ex.Apply(ctx, p); err != nil {
			return err
		}
		if err := h.Commit(); err != nil {
			return fmt.Errorf("%w\nnothing on disk was touched, so re-running this command is safe", err)
		}
	}

	reportPin(cmd, entries, o, false)
	return pinExit(entries, o)
}

// pinOne resolves one name to a verdict and the ops that carry it out. Every
// way it can fail is a verdict rather than an error, so one unknown name never
// hides what could be done with the rest.
func pinOne(ctx context.Context, reg channel.Registry, db *state.DB, name string, o pinOpts) (pinEntry, plan.Plan) {
	en := pinEntry{name: name}

	r, ok := db.Receipts[name]
	if !ok {
		en.err = db.NotInstalled(name)
		return en, plan.Plan{}
	}
	en.before = *r

	ch, err := reg.ForReceipt(r)
	if err != nil {
		en.err = err
		return en, plan.Plan{}
	}

	p, res, err := ch.Pin(*r, channel.PinOptions{On: o.on, Ref: o.ref})
	if err != nil {
		en.err = err
		return en, plan.Plan{}
	}

	// The ref is verified only once the channel has accepted the receipt, which
	// is what keeps the one network call in this command away from the channels
	// that have no repository to make it against.
	if res.Changed && !o.on && o.ref != "" {
		if err := verifyRef(ctx, *r, o.ref); err != nil {
			en.err = err
			return en, plan.Plan{}
		}
	}

	en.res = res
	return en, p
}

// verifyRef checks that the ref an unpin is about to record resolves, against
// whatever the receipt's channel resolves against. Only the two channels that
// track a moving ref can be here: Local and Plugin refuse the unpin outright,
// so their receipts never reach this.
//
// The dispatch is on the receipt's channel rather than inside Pin because Pin
// is a receipt-only mutation with no context and no network — the one call this
// command makes stays here, where it can be seen.
func verifyRef(ctx context.Context, r state.Receipt, ref string) error {
	if r.Channel == string(source.ChannelOCI) {
		// A registry takes registry/repository:tag; the oci:// scheme the
		// receipt records means nothing to it.
		src, err := source.Parse(r.Source)
		if err != nil {
			return fmt.Errorf("this receipt records %q, which cannot be parsed as an oci source: %w", r.Source, err)
		}
		_, err = newOCI().Resolve(ctx, src.OCIRef(ref))
		return err
	}
	_, err := gitx.New().Resolve(ctx, r.Source, ref)
	return err
}

// reportPin writes one line per name. A dry run has already printed the plan,
// so it adds only the names that produced no op: the failures, and the skills
// that were already in the state that was asked for.
func reportPin(cmd *cobra.Command, entries []pinEntry, o pinOpts, dryRun bool) {
	for _, en := range entries {
		switch {
		case en.err != nil:
			cmd.Printf("skipped %s: %s\n", en.name, reason(en.err))
		case !en.res.Changed:
			cmd.Println(unchangedPinLine(en, o))
		case dryRun:
		default:
			cmd.Println(pinLine(en, o))
			if en.res.Note != "" {
				cmd.Printf("note: %s\n", en.res.Note)
			}
		}
	}
}

// reason renders an error for a line that already names the skill. A
// NotInstalledError names it too, so its name-less form is used instead of
// saying "brainstorm" twice in one sentence.
func reason(err error) string {
	var missing *state.NotInstalledError
	if errors.As(err, &missing) {
		return missing.Hint()
	}
	return err.Error()
}

// pinLine renders a skill that moved. A pin names the ref it dropped, because
// a pinned receipt tracks nothing and that is the part a later unpin cannot
// give back.
func pinLine(en pinEntry, o pinOpts) string {
	if !o.on {
		return fmt.Sprintf("unpinned %s; it now tracks %s", en.name, tracked(en.res.Receipt.Ref))
	}
	line := fmt.Sprintf("pinned %s at %s", en.name, shortSha(en.res.Receipt.Resolved))
	if en.before.Ref != "" {
		line += fmt.Sprintf(" (it no longer tracks %s)", en.before.Ref)
	}
	return line
}

func unchangedPinLine(en pinEntry, o pinOpts) string {
	if !o.on {
		return fmt.Sprintf("%s is not pinned", en.name)
	}
	return fmt.Sprintf("%s is already pinned at %s", en.name, shortSha(en.before.Resolved))
}

// tracked names the ref a receipt now follows. An empty ref is not missing
// information: it is how a receipt records the repository's default branch.
func tracked(ref string) string {
	if ref == "" {
		return "the repository's default branch"
	}
	return ref
}

// pinExit turns the report into an exit code, once the reasons are already on
// screen. A skill that was already in the state asked for counts as done: like
// a pin skipped by update, nothing about it failed.
func pinExit(entries []pinEntry, o pinOpts) error {
	verb := "pinned"
	if !o.on {
		verb = "unpinned"
	}

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
		return partialf("%s %s, %s skipped", count(done, "skill"), verb, count(skipped, "skill"))
	default:
		return fmt.Errorf("nothing was %s: %s skipped, for the reasons above", verb, count(skipped, "skill"))
	}
}
