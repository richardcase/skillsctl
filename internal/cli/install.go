package cli

import (
	"context"
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
	agents         []string
	skills         []string
	all            bool
	ref            string
	as             string
	pin            bool
	verifyKey      string
	verifyIdentity string
	verifyIssuer   string
	dryRun         bool
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
	cmd.Flags().StringVar(&o.verifyKey, "verify-key", "", "cosign public key to verify an oci:// image's signature against before installing")
	cmd.Flags().StringVar(&o.verifyIdentity, "verify-identity", "", "signer identity to verify a Sigstore keyless signature against (e.g. a CI workflow's OIDC subject)")
	cmd.Flags().StringVar(&o.verifyIssuer, "verify-issuer", "", "OIDC issuer that must have signed --verify-identity's certificate (e.g. https://token.actions.githubusercontent.com)")
	cmd.MarkFlagsRequiredTogether("verify-identity", "verify-issuer")
	cmd.MarkFlagsMutuallyExclusive("verify-key", "verify-identity")
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
	if (o.verifyKey != "" || o.verifyIdentity != "") && src.Channel != source.ChannelOCI {
		return fmt.Errorf("--verify-key/--verify-identity only apply to oci:// sources")
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
		Source:         src,
		Targets:        targets,
		Skills:         o.skills,
		All:            o.all,
		Ref:            o.ref,
		Pin:            o.pin,
		VerifyKey:      o.verifyKey,
		VerifyIdentity: o.verifyIdentity,
		VerifyIssuer:   o.verifyIssuer,
	}

	chosen, warnings, err := ch.Prepare(ctx, req)
	if err != nil {
		chosen, warnings, err = resolveAmbiguity(ctx, cmd, ch, &req, o, err)
	}
	if err != nil {
		reportAmbiguous(cmd, err)
		return err
	}
	for _, w := range warnings {
		cmd.Println(w)
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

	p, receipts, installSkips, err := ch.Install(req, wanted)
	if err != nil {
		return err
	}

	if o.dryRun {
		for _, line := range p.Describe() {
			cmd.Println(line)
		}
		reportSkipped(cmd, skipped)
		reportSkipped(cmd, installSkips)
		if err := skippedErr(skipped, chosen); err != nil {
			return err
		}
		if len(installSkips) > 0 {
			return partialf("%s could not be linked", count(len(installSkips), "skill"))
		}
		return nil
	}

	ex := &plan.Executor{DB: h.DB, Out: cmd.OutOrStdout(), Run: newRunner()}
	if err := ex.Apply(ctx, p); err != nil {
		return err
	}

	// A failed settle is reported after the receipts are committed, never
	// instead of committing them: an install we cannot fully describe still
	// beats one nothing recorded.
	receipts, serr := settle(ctx, ex, ch, receipts)

	// The links come last, because for a channel whose agent chooses the
	// directory this is the first moment there is a directory to point at.
	receipts, linkSkips, lerr := relink(ctx, ex, ch, receipts, func(state.Receipt) []target.Target {
		return targets
	})

	if err := h.Commit(); err != nil {
		return fmt.Errorf("%w\nthe skill was linked but the receipt was not saved; re-run this command to repair, "+
			"or remove the symlink by hand if it now points at an older revision", err)
	}

	for _, r := range receipts {
		// A local skill has no revision to name, and "@" with nothing after it
		// reads as something missing rather than something absent.
		where := strings.Join(ch.Agents(r), ", ")
		if r.Resolved == "" {
			cmd.Printf("installed %s into %s\n", r.Name, where)
			continue
		}
		cmd.Printf("installed %s @ %s into %s\n", r.Name, shortSha(r.Resolved), where)
	}
	reportSkipped(cmd, skipped)

	// An adopted plugin's fan-out is planned once, in Install, and then
	// recomputed once more here, because relink runs for every channel
	// regardless of which one already did its own linking. For that plugin the
	// same skip comes back from both: this is the one place they are merged
	// back into one line rather than reported twice.
	linkSkips = dedupeSkips(installSkips, linkSkips)
	reportSkipped(cmd, linkSkips)
	if serr != nil {
		return serr
	}
	if lerr != nil {
		return lerr
	}
	if err := skippedErr(skipped, chosen); err != nil {
		return err
	}
	if len(linkSkips) > 0 {
		return partialf("%s could not be linked", count(len(linkSkips), "skill"))
	}
	return nil
}

// dedupeSkips merges skip reasons from sources that can describe the same
// skip twice — an adopted plugin's fan-out runs once in Install and once more
// when relink recomputes it after settle — keeping the first occurrence of
// each exact line so one skip still reads as one line.
func dedupeSkips(lists ...[]string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, l := range lists {
		for _, s := range l {
			if seen[s] {
				continue
			}
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// resolveAmbiguity answers a repository holding several skills by asking which
// of them to install, and returns what Prepare would have returned had the
// answer been passed as --skill. It hands back the error it was given when
// there is nobody to ask, which is what leaves the non-interactive behaviour —
// the listing, and an exit code — exactly as it was.
func resolveAmbiguity(
	ctx context.Context, cmd *cobra.Command, ch channel.Channel,
	req *channel.Request, o installOpts, cause error,
) ([]channel.Candidate, []string, error) {
	var amb *channel.Ambiguous
	if !errors.As(cause, &amb) {
		return nil, nil, cause
	}
	// narrow also reports an ambiguity for a --skill that names nothing in the
	// repository. That is a typo rather than an unanswered question, and a
	// picker is no answer to it.
	if len(o.skills) > 0 || o.all {
		return nil, nil, cause
	}
	p := newPicker()
	if !p.Interactive() {
		return nil, nil, cause
	}

	names, err := selectSkills(p, amb, o.as != "")
	if err != nil {
		return nil, nil, err
	}

	// A second request, for the re-read only. Install must still see the ref
	// the user asked for: it records req.Ref as the ref to track, so pinning
	// the real request to the sha would freeze the skill against every future
	// update. Pinning this one is what makes the second pass offline — Resolve
	// passes a full sha straight through — and what stops a branch that moved
	// in between from installing a tree the listing never showed.
	lookup := *req
	lookup.Skills = names
	if amb.Resolved != "" {
		// A channel with no revision to name leaves this empty, and re-reading
		// a local directory costs another walk of it and nothing more.
		lookup.Ref = amb.Resolved
	}

	chosen, warnings, err := ch.Prepare(ctx, lookup)
	if err != nil {
		return nil, nil, err
	}

	req.Skills = names
	for _, line := range pickedListing(amb, names) {
		cmd.Println(line)
	}
	return chosen, warnings, nil
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
