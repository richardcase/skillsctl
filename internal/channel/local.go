package channel

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/richardcase/skillsctl/internal/discover"
	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/store"
	"github.com/richardcase/skillsctl/internal/target"
)

// Local registers a skill that already exists on disk, where it already is.
// Nothing is fetched and nothing is copied: the receipt records where the skill
// lives and the symlinks point straight at it, so an edit to the source is
// visible to every agent immediately.
//
// That is the point rather than a shortcut. A revision directory is immutable
// and carries no .git precisely so that editing an installed skill cannot
// silently break its next update; this channel is where development happens
// instead.
type Local struct {
	linked

	store *store.Store
}

// NewLocal returns the local channel. It takes the store only to refuse paths
// inside it — nothing local is ever written there.
func NewLocal(st *store.Store) *Local { return &Local{store: st} }

// Ownership reports that the files are the user's, so gc has nothing to count
// while remove still has links to undo.
func (c *Local) Ownership() Ownership { return UserOwned }

// Prepare resolves the path, refuses the ones that cannot mean what they say,
// and narrows what it finds to the skills the request asked for.
func (c *Local) Prepare(_ context.Context, req Request) ([]Candidate, error) {
	if err := rejectRevisionFlags(req); err != nil {
		return nil, err
	}

	root, err := c.resolve(req)
	if err != nil {
		return nil, err
	}

	found, err := discover.Walk(root)
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("%s: %w", root, discover.ErrNoSkill)
	}

	// The fallback name comes from the resolved path rather than from the
	// source, so that `skillsctl install .` names the directory it was run in
	// instead of trying to call the skill ".".
	available, err := resolveNames(found, filepath.Base(root))
	if err != nil {
		return nil, err
	}

	chosen, err := narrow(available, req)
	if err != nil {
		var amb *Ambiguous
		if errors.As(err, &amb) {
			amb.Header = fmt.Sprintf("skills in %s:", root)
			amb.Meta = discover.PluginMeta(root)
			amb.Available = brief(available)
		}
		return nil, err
	}

	return localCandidates(chosen, root)
}

// resolve turns whatever the user typed into an absolute directory, and refuses
// the paths that would mean something other than "a skill I am working on".
func (c *Local) resolve(req Request) (string, error) {
	expanded, err := target.Expand(req.Source.Path)
	if err != nil {
		return "", err
	}
	root, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", req.Source.Path, err)
	}

	// Deliberately not EvalSymlinks: if the path the user gave is itself a
	// symlink, that is the thing they asked to link to.
	fi, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", root, err)
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("%s is not a directory: a local source is the directory holding %s", root, discover.FileName)
	}

	// A revision directory is skillsctl's own copy of somebody's repository,
	// not a skill of the user's. Recording one as local would put a receipt gc
	// ignores on top of a directory gc collects.
	if c.store.Contains(root) {
		return "", fmt.Errorf("%s is inside the skillsctl store: install it from its source instead, so updates keep working", root)
	}

	// Linking a skills directory into itself would make the symlink its own
	// target, and linking one agent's skill into another's is what `skillsctl
	// link <name> -a <agent>` is for.
	for _, t := range req.Targets {
		if within(t.Dir, root) {
			return "", fmt.Errorf("%s is already inside %s's skills directory: skillsctl manages what is there rather than linking it again", root, t.Name)
		}
	}
	return root, nil
}

// rejectRevisionFlags turns a flag that only means something for a fetched
// revision into an error naming it. A local skill has no revision to resolve
// and none to freeze, and silently ignoring --pin would let somebody believe
// they had frozen a directory they are still editing.
func rejectRevisionFlags(req Request) error {
	switch {
	case req.Ref != "":
		return fmt.Errorf("--ref names a git revision, and a local skill is whatever is in the directory right now: drop --ref")
	case req.Pin:
		return fmt.Errorf("--pin freezes a git revision, and a local skill is meant to change under you: drop --pin")
	}
	return nil
}

// localCandidates fills in what Install needs. Version and Hash stay empty:
// there is no revision, and a skill you are editing has no meaningful "as
// installed" state to compare against later.
func localCandidates(sels []selection, root string) ([]Candidate, error) {
	out := make([]Candidate, 0, len(sels))
	for _, s := range sels {
		subpath, err := filepath.Rel(root, s.skill.Dir)
		if err != nil {
			return nil, err
		}
		if subpath = filepath.ToSlash(subpath); subpath == "." {
			subpath = ""
		}
		out = append(out, Candidate{
			Name:    s.name,
			Desc:    s.skill.Description,
			Path:    s.skill.Dir,
			Subpath: subpath,
		})
	}
	return out, nil
}

// Install links each skill where it already is and records a receipt.
func (c *Local) Install(req Request, chosen []Candidate) (plan.Plan, []state.Receipt, error) {
	var p plan.Plan
	receipts := make([]state.Receipt, 0, len(chosen))
	now := time.Now().UTC()

	root, err := c.resolve(req)
	if err != nil {
		return p, nil, err
	}

	for _, s := range chosen {
		// No slug, no resolved revision, no content hash: a slug says where in
		// the store something lives, and nothing of this is in the store.
		receipt := state.Receipt{
			Name:        s.Name,
			Channel:     string(source.ChannelLocal),
			Source:      root,
			Subpath:     s.Subpath,
			RevPath:     s.Path,
			InstalledAt: now,
			UpdatedAt:   now,
		}

		for _, t := range req.Targets {
			linkPath := filepath.Join(t.Dir, s.Name)
			if filepath.Dir(linkPath) != filepath.Clean(t.Dir) {
				return p, nil, fmt.Errorf("refusing to install %q: it would resolve outside %s", s.Name, t.Dir)
			}
			p.Add(plan.Link{Target: t.Name, LinkPath: linkPath, RevPath: s.Path})
			receipt.Links = append(receipt.Links, state.Link{Target: t.Name, Path: linkPath})
		}
		p.Add(plan.Record{Receipt: receipt})
		receipts = append(receipts, receipt)
	}
	return p, receipts, nil
}

// Update has nothing to do. A local skill is already whatever its directory
// says it is, so there is no newer version of it to move to.
func (c *Local) Update(_ context.Context, rs []*state.Receipt, _ UpdateOptions) ([]Verdict, plan.Plan, error) {
	verdicts := make([]Verdict, 0, len(rs))
	for _, r := range rs {
		verdicts = append(verdicts, Verdict{
			Name:    r.Name,
			Channel: r.Channel,
			Current: r.Resolved,
			Status:  StatusSkipped,
		})
	}
	return verdicts, plan.Plan{}, nil
}

// Settle has nothing to complete: every path was known before the plan was
// built, because nothing had to be fetched to learn it.
func (c *Local) Settle(context.Context, []state.Receipt) ([]state.Receipt, error) {
	return nil, nil
}

// within reports whether p is at or below dir.
func within(dir, p string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
