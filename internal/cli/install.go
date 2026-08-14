package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/richardcase/skillsctl/internal/channel"
	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/target"
	"github.com/spf13/cobra"
)

// installOpts is the flag set of one install invocation.
type installOpts struct {
	agents []string
	skills []string
	all    bool
	ref    string
	as     string
	pin    bool
	dryRun bool
}

func newInstallCmd() *cobra.Command {
	var o installOpts

	cmd := &cobra.Command{
		Use:   "install <source>",
		Short: "Install a skill",
		Long: "Install one or more skills from a git repository.\n\n" +
			"Sources may be owner/repo, owner/repo/path/to/skill, any git URL, or a local path.\n" +
			"A // separates a repository from a subpath inside it, as in\n" +
			"git@github.com:owner/repo.git//skills/alpha.\n\n" +
			"A repository holding several skills needs --skill <name> or --all; without one of\n" +
			"those, install lists what it found rather than guessing.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInstall(cmd, args[0], o)
		},
	}

	cmd.Flags().StringSliceVarP(&o.agents, "agent", "a", nil, "agents to link into (default: every agent found)")
	// StringArray, not StringSlice: a skill name is taken whole, never split on commas.
	cmd.Flags().StringArrayVar(&o.skills, "skill", nil, "skill to install, by name or path; repeat for several")
	cmd.Flags().BoolVar(&o.all, "all", false, "install every skill found in the repository")
	cmd.Flags().StringVar(&o.ref, "ref", "", "branch, tag or sha to install (default: the repository's HEAD)")
	cmd.Flags().StringVar(&o.as, "as", "", "install under this name instead of the one in SKILL.md")
	cmd.Flags().BoolVar(&o.pin, "pin", false, "freeze at the resolved sha, so update skips it")
	cmd.Flags().BoolVar(&o.dryRun, "dry-run", false, "show what would change without changing it")
	return cmd
}

func runInstall(cmd *cobra.Command, raw string, o installOpts) error {
	ctx := cmd.Context()

	if o.all && len(o.skills) > 0 {
		return fmt.Errorf("--all and --skill contradict each other: pass one or the other")
	}
	if o.as != "" && (o.all || len(o.skills) > 1) {
		return fmt.Errorf("--as renames a single skill, so it cannot be combined with --all or several --skill flags")
	}

	src, err := source.Parse(raw)
	if err != nil {
		return err
	}

	e, err := newEnv()
	if err != nil {
		return err
	}
	ch, err := e.channels().For(src.Channel)
	if err != nil {
		return err
	}
	targets, err := e.targets(o.agents)
	if err != nil {
		return err
	}

	// The state lock is taken before anything is written to the store, and
	// held until this command exits. That is the invariant gc relies on: no
	// revision directory exists without its creator holding the lock until
	// the receipt that makes it live has been committed, so a concurrent gc
	// can never collect an extraction that is about to be recorded.
	h, err := e.openState()
	if err != nil {
		return err
	}
	defer func() { _ = h.Close() }()

	req := channel.Request{
		Source:  src,
		Targets: targets,
		Skills:  o.skills,
		All:     o.all,
		Ref:     o.ref,
		Pin:     o.pin,
	}

	chosen, err := ch.Prepare(ctx, req)
	if err != nil {
		reportAmbiguous(cmd, err)
		return err
	}

	if o.as != "" {
		if err := target.ValidateSkillName(o.as); err != nil {
			return fmt.Errorf("refusing to install: %w (from --as); pass --as <name> to choose one", err)
		}
		chosen[0].Name = o.as
	}

	wanted, skipped, err := dropInstalled(h.DB, chosen)
	if err != nil {
		return err
	}

	p, receipts, err := ch.Install(req, wanted)
	if err != nil {
		return err
	}

	if o.dryRun {
		for _, line := range p.Describe() {
			cmd.Println(line)
		}
		reportSkipped(cmd, skipped)
		return skippedErr(skipped, chosen)
	}

	ex := &plan.Executor{DB: h.DB, Out: cmd.OutOrStdout(), Run: newRunner()}
	if err := ex.Apply(ctx, p); err != nil {
		return err
	}

	// A failed settle is reported after the receipts are committed, never
	// instead of committing them: an install we cannot fully describe still
	// beats one nothing recorded.
	receipts, serr := settle(ctx, ex, ch, receipts)

	if err := h.Commit(); err != nil {
		return fmt.Errorf("%w\nthe skill was linked but the receipt was not saved; re-run this command to repair, "+
			"or remove the symlink by hand if it now points at an older revision", err)
	}

	for _, r := range receipts {
		cmd.Printf("installed %s @ %s into %s\n", r.Name, shortSha(r.Resolved), strings.Join(ch.Agents(r, e.cfg), ", "))
	}
	reportSkipped(cmd, skipped)
	if serr != nil {
		return serr
	}
	return skippedErr(skipped, chosen)
}

// reportAmbiguous prints what the user could have asked for, when the channel
// could not narrow the request to a single answer. The channel returns the
// candidates; how a listing looks is this package's business.
func reportAmbiguous(cmd *cobra.Command, err error) {
	var amb *channel.Ambiguous
	if !errors.As(err, &amb) {
		return
	}
	for _, line := range listing(amb.Meta, amb.Header, amb.Available) {
		cmd.Println(line)
	}
}

// dropInstalled splits a selection into the skills to install and the ones
// whose names are already taken. Naming a single skill is a request for that
// skill in particular, so a collision there is an error; asking for several is
// a request for whatever is missing, so collisions are reported and skipped.
func dropInstalled(db *state.DB, chosen []channel.Candidate) (wanted []channel.Candidate, skipped []string, err error) {
	for _, s := range chosen {
		existing, ok := db.Receipts[s.Name]
		if !ok {
			wanted = append(wanted, s)
			continue
		}
		if len(chosen) == 1 {
			return nil, nil, fmt.Errorf("%q is already installed from %s: remove it first, or install this one with --as <name>",
				s.Name, existing.Source)
		}
		skipped = append(skipped, fmt.Sprintf("skipped %s: already installed from %s", s.Name, existing.Source))
	}
	if len(wanted) == 0 {
		return nil, nil, fmt.Errorf("every skill selected is already installed: remove one first, or install with --as <name>")
	}
	return wanted, skipped, nil
}

func reportSkipped(cmd *cobra.Command, skipped []string) {
	for _, line := range skipped {
		cmd.Println(line)
	}
}

// skippedErr makes a partial install exit non-zero, after the skills that could
// be installed have been reported: the work stands, the shell still notices.
func skippedErr(skipped []string, chosen []channel.Candidate) error {
	if len(skipped) == 0 {
		return nil
	}
	return partialf("%d of %d selected skills were skipped: their names are already installed",
		len(skipped), len(chosen))
}
