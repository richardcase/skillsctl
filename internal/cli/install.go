package cli

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/richardcase/skillsctl/internal/discover"
	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/store"
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
	if src.Channel != source.ChannelGit {
		return fmt.Errorf("the %s channel is not supported yet", src.Channel)
	}

	e, err := newEnv()
	if err != nil {
		return err
	}
	targets, err := e.targets(o.agents)
	if err != nil {
		return err
	}

	g := gitx.New()
	sha, err := g.Resolve(ctx, src.RepoURL, o.ref)
	if err != nil {
		return err
	}

	// Populating the content-addressed cache is idempotent and not a
	// user-visible mutation, so it runs even for --dry-run. It is what lets
	// the plan below name the skills exactly rather than guess.
	revRoot, err := e.store.Ensure(ctx, g, src, sha)
	if err != nil {
		return err
	}
	revPath := filepath.Join(revRoot, filepath.FromSlash(src.Subpath))
	if rel, rerr := filepath.Rel(revRoot, revPath); rerr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("refusing to install: subpath %q resolves outside the revision directory", src.Subpath)
	}

	chosen, err := chooseSkills(cmd, src, sha, revPath, o)
	if err != nil {
		return err
	}
	if o.as != "" {
		if err := target.ValidateSkillName(o.as); err != nil {
			return fmt.Errorf("refusing to install: %w (from --as); pass --as <name> to choose one", err)
		}
		chosen[0].name = o.as
	}

	h, err := e.openState()
	if err != nil {
		return err
	}
	defer func() { _ = h.Close() }()

	wanted, skipped, err := dropInstalled(h.DB, chosen)
	if err != nil {
		return err
	}

	p, receipts, err := installPlan(wanted, targets, src, sha, revRoot, o)
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

	ex := &plan.Executor{DB: h.DB, Out: cmd.OutOrStdout()}
	if err := ex.Apply(ctx, p); err != nil {
		return err
	}
	if err := h.Commit(); err != nil {
		return fmt.Errorf("%w\nthe skill was linked but the receipt was not saved; re-run this command to repair, "+
			"or remove the symlink by hand if it now points at an older revision", err)
	}

	for _, r := range receipts {
		cmd.Printf("installed %s @ %s into %s\n", r.Name, shortSha(sha), targetNames(targets))
	}
	reportSkipped(cmd, skipped)
	return skippedErr(skipped, chosen)
}

// chooseSkills discovers the skills in revPath and narrows them to the ones the
// user asked for. When the choice is ambiguous it prints what is available and
// returns an error: install never guesses which skill was meant.
func chooseSkills(cmd *cobra.Command, src source.Source, sha, revPath string, o installOpts) ([]selection, error) {
	found, err := discover.Walk(revPath)
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("%s: %w", revPath, discover.ErrNoSkill)
	}

	available, err := resolveNames(found, src.DefaultName())
	if err != nil {
		return nil, err
	}

	switch {
	case o.all:
		return available, nil

	case len(o.skills) > 0:
		chosen, err := pickSkills(available, o.skills)
		if err != nil {
			printAvailable(cmd, src, sha, revPath, available)
			return nil, err
		}
		return chosen, nil

	case len(available) == 1:
		return available, nil

	default:
		printAvailable(cmd, src, sha, revPath, available)
		return nil, fmt.Errorf("this repository holds %d skills: pass --skill <name> (repeatable) or --all", len(available))
	}
}

// printAvailable writes the listing that accompanies an ambiguous or unmatched
// selection, so the next command the user types can be a correct one.
func printAvailable(cmd *cobra.Command, src source.Source, sha, revPath string, sels []selection) {
	header := fmt.Sprintf("skills in %s @ %s:", src.RepoURL, shortSha(sha))
	for _, line := range listing(discover.PluginMeta(revPath), header, sels) {
		cmd.Println(line)
	}
}

// dropInstalled splits a selection into the skills to install and the ones
// whose names are already taken. Naming a single skill is a request for that
// skill in particular, so a collision there is an error; asking for several is
// a request for whatever is missing, so collisions are reported and skipped.
func dropInstalled(db *state.DB, chosen []selection) (wanted []selection, skipped []string, err error) {
	for _, s := range chosen {
		existing, ok := db.Receipts[s.name]
		if !ok {
			wanted = append(wanted, s)
			continue
		}
		if len(chosen) == 1 {
			return nil, nil, fmt.Errorf("%q is already installed from %s: remove it first, or install this one with --as <name>",
				s.name, existing.Source)
		}
		skipped = append(skipped, fmt.Sprintf("skipped %s: already installed from %s", s.name, existing.Source))
	}
	if len(wanted) == 0 {
		return nil, nil, fmt.Errorf("every skill selected is already installed: remove one first, or install with --as <name>")
	}
	return wanted, skipped, nil
}

// installPlan builds the whole install as one plan: every link for every skill,
// then every receipt. One apply, so a failure part-way leaves nothing behind.
func installPlan(wanted []selection, targets []target.Target, src source.Source, sha, revRoot string, o installOpts) (plan.Plan, []state.Receipt, error) {
	var p plan.Plan
	receipts := make([]state.Receipt, 0, len(wanted))
	now := time.Now().UTC()

	for _, s := range wanted {
		hash, err := store.HashDir(s.skill.Dir)
		if err != nil {
			return p, nil, err
		}
		subpath, err := filepath.Rel(revRoot, s.skill.Dir)
		if err != nil {
			return p, nil, err
		}
		if subpath = filepath.ToSlash(subpath); subpath == "." {
			subpath = ""
		}

		receipt := state.Receipt{
			Name:        s.name,
			Channel:     string(src.Channel),
			Source:      src.RepoURL,
			Slug:        src.Slug(),
			Subpath:     subpath,
			Resolved:    sha,
			Pinned:      o.pin,
			RevPath:     s.skill.Dir,
			ContentHash: hash,
			InstalledAt: now,
			UpdatedAt:   now,
		}
		if !o.pin {
			receipt.Ref = o.ref
		}

		for _, t := range targets {
			linkPath := filepath.Join(t.Dir, s.name)
			if filepath.Dir(linkPath) != filepath.Clean(t.Dir) {
				return p, nil, fmt.Errorf("refusing to install %q: it would resolve outside %s", s.name, t.Dir)
			}
			p.Add(plan.Link{Target: t.Name, LinkPath: linkPath, RevPath: s.skill.Dir})
			receipt.Links = append(receipt.Links, state.Link{Target: t.Name, Path: linkPath})
		}
		p.Add(plan.Record{Receipt: receipt})
		receipts = append(receipts, receipt)
	}
	return p, receipts, nil
}

func reportSkipped(cmd *cobra.Command, skipped []string) {
	for _, line := range skipped {
		cmd.Println(line)
	}
}

// skippedErr makes a partial install exit non-zero, after the skills that could
// be installed have been reported: the work stands, the shell still notices.
func skippedErr(skipped []string, chosen []selection) error {
	if len(skipped) == 0 {
		return nil
	}
	return partialf("%d of %d selected skills were skipped: their names are already installed",
		len(skipped), len(chosen))
}
