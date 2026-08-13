package cli

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/richardcase/skillsctl/internal/discover"
	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/store"
	"github.com/spf13/cobra"
)

func newInstallCmd() *cobra.Command {
	var (
		agents []string
		ref    string
		as     string
		pin    bool
		dryRun bool
	)

	cmd := &cobra.Command{
		Use:   "install <source>",
		Short: "Install a skill",
		Long: "Install a skill from a git repository.\n\n" +
			"Sources may be owner/repo, owner/repo/path/to/skill, any git URL, or a local path.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInstall(cmd, args[0], agents, ref, as, pin, dryRun)
		},
	}

	cmd.Flags().StringSliceVarP(&agents, "agent", "a", nil, "agents to link into (default: every agent found)")
	cmd.Flags().StringVar(&ref, "ref", "", "branch, tag or sha to install (default: the repository's HEAD)")
	cmd.Flags().StringVar(&as, "as", "", "install under this name instead of the one in SKILL.md")
	cmd.Flags().BoolVar(&pin, "pin", false, "freeze at the resolved sha, so update skips it")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change without changing it")
	return cmd
}

func runInstall(cmd *cobra.Command, raw string, agents []string, ref, as string, pin, dryRun bool) error {
	ctx := cmd.Context()

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
	targets, err := e.targets(agents)
	if err != nil {
		return err
	}

	g := gitx.New()
	sha, err := g.Resolve(ctx, src.RepoURL, ref)
	if err != nil {
		return err
	}

	// Populating the content-addressed cache is idempotent and not a
	// user-visible mutation, so it runs even for --dry-run. It is what lets
	// the plan below name the skill exactly rather than guess.
	revRoot, err := e.store.Ensure(ctx, g, src, sha)
	if err != nil {
		return err
	}
	revPath := filepath.Join(revRoot, filepath.FromSlash(src.Subpath))

	skill, err := discover.Root(revPath)
	if err != nil {
		return err
	}

	name := as
	if name == "" {
		name = skill.Name
	}
	if name == "" {
		name = src.DefaultName()
	}
	if name == "" {
		return fmt.Errorf("could not determine a name for this skill: pass --as")
	}

	hash, err := store.HashDir(revPath)
	if err != nil {
		return err
	}

	h, err := e.openState()
	if err != nil {
		return err
	}
	defer func() { _ = h.Close() }()

	if existing, ok := h.DB.Receipts[name]; ok {
		return fmt.Errorf("%q is already installed from %s: remove it first, or install this one with --as <name>", name, existing.Source)
	}

	now := time.Now().UTC()
	receipt := state.Receipt{
		Name:        name,
		Channel:     string(src.Channel),
		Source:      src.RepoURL,
		Slug:        src.Slug(),
		Subpath:     src.Subpath,
		Resolved:    sha,
		Pinned:      pin,
		RevPath:     revPath,
		ContentHash: hash,
		InstalledAt: now,
		UpdatedAt:   now,
	}
	if !pin {
		receipt.Ref = ref
	}

	var p plan.Plan
	for _, t := range targets {
		linkPath := filepath.Join(t.Dir, name)
		p.Add(plan.Link{Target: t.Name, LinkPath: linkPath, RevPath: revPath})
		receipt.Links = append(receipt.Links, state.Link{Target: t.Name, Path: linkPath})
	}
	p.Add(plan.Record{Receipt: receipt})

	if dryRun {
		for _, line := range p.Describe() {
			cmd.Println(line)
		}
		return nil
	}

	ex := &plan.Executor{DB: h.DB, Out: cmd.OutOrStdout()}
	if err := ex.Apply(ctx, p); err != nil {
		return err
	}
	if err := h.Commit(); err != nil {
		return fmt.Errorf("%w\nthe skill was linked but the receipt was not saved; re-run this command to repair", err)
	}

	cmd.Printf("installed %s @ %s into %s\n", name, shortSha(sha), targetNames(targets))
	return nil
}
