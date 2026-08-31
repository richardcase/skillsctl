package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/target"
	"github.com/spf13/cobra"
)

// newLinkCmd serves two argument forms the design pairs on one line.
//
// `link <path>` is install restricted to a local path. The two spellings exist
// because "install" describes what the user wants and "link" describes what
// actually happens: nothing is fetched, nothing is copied, and the skill stays
// where they are editing it.
//
// `link <name> -a <agent>` is the inverse of `remove <name> -a <agent>`: it
// adds a link to a skill that is already installed, for the agent that was not
// on the machine when it was.
func newLinkCmd() *cobra.Command {
	var o installOpts

	cmd := &cobra.Command{
		Use:   "link <name>|<path>",
		Short: "Link an installed skill into another agent, or a skill you are working on",
		Long: "Given the name of an installed skill, add a link to it for the agents named\n" +
			"with -a. This is the inverse of `skillsctl remove <name> -a <agent>`, and the\n" +
			"way to reach an agent that was not on this machine at install time.\n\n" +
			"Given a path, register a skill from a directory on this machine, linked in\n" +
			"place. Nothing is copied into the store, so edits to the directory are live in\n" +
			"every agent immediately — which is what makes this the way to develop a skill.\n" +
			"`skillsctl install ./path` does exactly the same thing.\n\n" +
			"Which form you meant is decided by looking the argument up in the receipts.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			miss, err := lookup(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if miss == nil {
				return runLinkName(cmd, args[0], o)
			}

			src, perr := source.Parse(args[0])
			if perr != nil {
				// A bare word is the common typo here — it is neither a
				// receipt's name nor anything source.Parse recognises — so the
				// near-misses matter more in this message than in any other.
				return fmt.Errorf("%q is %s\nit is not a path to a directory on this machine either: `skillsctl install %s` fetches something new",
					args[0], miss.Hint(), args[0])
			}
			if src.Channel != source.ChannelLocal {
				return fmt.Errorf("link takes a path to a directory on this machine, and %q is a %s source: install it with `skillsctl install %s`",
					args[0], src.Channel, args[0])
			}
			return runInstall(cmd, args[0], o)
		},
	}

	cmd.Flags().StringSliceVarP(&o.agents, "agent", "a", nil, "agents to link into (default: every agent found)")
	cmd.Flags().StringArrayVar(&o.skills, "skill", nil, "skill to link, by name or path; repeat for several")
	cmd.Flags().BoolVar(&o.all, "all", false, "link every skill found in the directory")
	cmd.Flags().StringVar(&o.as, "as", "", "link under this name instead of the one in SKILL.md")
	cmd.Flags().BoolVar(&o.dryRun, "dry-run", false, "show what would change without changing it")
	return cmd
}

// lookup reports whether arg names a skill in the receipts, returning nil when
// it does and the miss — carrying any near-misses — when it does not.
//
// It opens and closes state on its own rather than handing back a live handle:
// state.Open blocks on an exclusive flock, which is held per open file
// description, so a second Open in this process would wait on the first
// forever. The path form takes its own handle a moment later, and the name form
// looks the receipt up again under the handle it keeps — that second lookup is
// the one that decides, so nothing rests on what this saw.
func lookup(ctx context.Context, arg string) (*state.NotInstalledError, error) {
	e, err := newEnv()
	if err != nil {
		return nil, err
	}
	h, err := e.openState(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = h.Close() }()

	if _, ok := h.DB.Receipts[arg]; ok {
		return nil, nil
	}
	return &state.NotInstalledError{Name: arg, Suggestions: h.DB.NearMisses(arg)}, nil
}

// runLinkName adds a link to a receipt that already exists. The only state
// change is Receipt.Links, which is the removal contract, so this is exactly
// what a later `remove -a` will undo.
func runLinkName(cmd *cobra.Command, name string, o installOpts) error {
	if err := rejectPathFormFlags(o); err != nil {
		return err
	}

	e, err := newEnv()
	if err != nil {
		return err
	}
	h, err := e.openState(cmd.Context())
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
	targets, err := e.targets(o.agents)
	if err != nil {
		return err
	}

	// What counts as "already has it" is the channel's answer, not the
	// receipt's links: claude holds a plugin without a link of ours, so a
	// receipt that records none is not an agent that has nothing. It is only a
	// first approximation, though: Agents reports an agent as having it once it
	// holds at least one of a plugin's links, which is not the same as holding
	// all of them. So the decision of whether there is anything left to do is
	// not made here — every named target is asked for below, and it is Link's
	// answer, not this partition, that decides it.
	_, already := partitionLinked(ch.Agents(*receipt), targets)
	asked := len(targets)

	// Reconciliation is idempotent and skips what already agrees, so asking
	// for every named target rather than only the ones partitionLinked thinks
	// are missing is harmless when they agree — and it is what lets this
	// repair an agent that holds only some of a plugin's links, which
	// partitionLinked would otherwise have written off as already linked.
	p, linkSkips, err := ch.Link(*receipt, targets)
	if err != nil {
		return err
	}
	// An empty plan with nothing skipped is the only case every named target
	// genuinely needed nothing. An empty plan that still has skips is not that
	// case — a name collision can block every link a target was asked for
	// without the plan having anything to show for it — so it falls through to
	// be reported and priced in below like any other skip.
	if p.IsEmpty() && len(linkSkips) == 0 {
		return fmt.Errorf("%s is already linked into %s", name, strings.Join(already, ", "))
	}

	// partitionLinked's already is the first approximation above, and a named
	// agent the plan just repaired must not also be reported as one that
	// needed nothing: that would print "linked into X" and "X already had it"
	// about the same agent in the same breath. The plan is the precise answer,
	// so an agent it actually touched is dropped from already even though
	// partitionLinked thought it already had everything.
	touched := touchedTargets(p)
	if len(touched) > 0 {
		var kept []string
		for _, a := range already {
			if !touched[a] {
				kept = append(kept, a)
			}
		}
		already = kept
	}

	// An agent the user named and that already had it is a request that could
	// not be carried out, and so a partial result. One that merely fell out of
	// the default set is not: every receipt has at least one link, so counting
	// those would make the bare `link <name>` exit 2 every single time.
	if len(o.agents) == 0 {
		already = nil
	}

	if o.dryRun {
		for _, line := range p.Describe() {
			cmd.Println(line)
		}
		reportSkipped(cmd, linkSkips)
		reportAlreadyLinked(cmd, name, already)
		return linkExitErr(already, asked, linkSkips)
	}

	ex := &plan.Executor{DB: h.DB, Out: cmd.OutOrStdout(), Run: newRunner()}
	if err := ex.Apply(cmd.Context(), p); err != nil {
		return err
	}
	if err := h.Commit(); err != nil {
		return fmt.Errorf("%w\nthe links were created but the receipt was not updated; re-run this command to repair", err)
	}

	// The success line names what the plan actually touched, not every target
	// that was asked for: printing the full requested list here is what caused
	// "linked into claude, codex" to appear next to "already linked into
	// claude" for the very same command, about the very same agent. touched is
	// the same map that trims already above, so the two lines cannot disagree
	// about which agent did which — an agent can lead to at most one of them,
	// except when a repair genuinely touches an agent partitionLinked also
	// thought already had everything (13 of 14 skills, say): that agent
	// correctly lands in the touched line only, having just been dropped from
	// already by the block above.
	var justLinked []string
	for _, t := range targets {
		if touched[t.Name] {
			justLinked = append(justLinked, t.Name)
		}
	}
	if len(justLinked) > 0 {
		cmd.Printf("linked %s into %s\n", name, strings.Join(justLinked, ", "))
	}
	reportSkipped(cmd, linkSkips)
	reportAlreadyLinked(cmd, name, already)
	return linkExitErr(already, asked, linkSkips)
}

// linkExitErr is the exit code both branches of runLinkName carry: the dry
// run is only trustworthy if it is the same pass as the real run rather than
// a different branch, so what it returns has to be computed the one way.
func linkExitErr(already []string, asked int, linkSkips []string) error {
	if err := alreadyLinkedErr(already, asked); err != nil {
		return err
	}
	if len(linkSkips) > 0 {
		return partialf("%s could not be linked", count(len(linkSkips), "skill"))
	}
	return nil
}

// rejectPathFormFlags refuses the flags that only mean something when link is
// choosing skills out of a directory. An installed skill was chosen when it was
// installed, and its name is the receipt key, so it is the same in every agent.
func rejectPathFormFlags(o installOpts) error {
	switch {
	case o.as != "":
		return fmt.Errorf("--as renames a skill as it is installed, and this one already is: " +
			"a receipt's name is the same in every agent")
	case o.all:
		return fmt.Errorf("--all picks skills out of a directory, and this argument names one that is already installed")
	case len(o.skills) > 0:
		return fmt.Errorf("--skill picks skills out of a directory, and this argument names one that is already installed")
	}
	return nil
}

// partitionLinked splits the requested targets into the ones to link and the
// ones the channel says already have it, so that the caller can report the
// second group by name. Link skips them too, but only the caller knows what was
// asked for and so which ones are worth mentioning.
func partitionLinked(held []string, targets []target.Target) (add []target.Target, already []string) {
	has := make(map[string]bool, len(held))
	for _, name := range held {
		has[name] = true
	}

	for _, t := range targets {
		if has[t.Name] {
			already = append(already, t.Name)
			continue
		}
		add = append(add, t)
	}
	return add, already
}

func reportAlreadyLinked(cmd *cobra.Command, name string, already []string) {
	for _, a := range already {
		cmd.Printf("%s is already linked into %s\n", name, a)
	}
}

// alreadyLinkedErr makes a partial link exit non-zero, after the agents that
// could be linked have been reported: the work stands, the shell still notices.
// It counts rather than repeating the lines above it, which is the shape a
// partial install already has.
func alreadyLinkedErr(already []string, asked int) error {
	if len(already) == 0 {
		return nil
	}
	return partialf("%d of %d agents named already had it", len(already), asked)
}

// touchedTargets names every target a plan actually changes something for.
func touchedTargets(p plan.Plan) map[string]bool {
	out := make(map[string]bool, len(p.Ops))
	for _, op := range p.Ops {
		switch o := op.(type) {
		case plan.Link:
			out[o.Target] = true
		case plan.Relink:
			out[o.Target] = true
		case plan.Unlink:
			out[o.Target] = true
		}
	}
	return out
}
