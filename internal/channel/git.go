package channel

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"time"

	"github.com/richardcase/skillsctl/internal/discover"
	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/store"
	"github.com/richardcase/skillsctl/internal/target"
)

// Git installs skills from a git repository: a bare mirror in the store, an
// immutable revision directory per sha, and a symlink per agent.
type Git struct {
	linked

	store *store.Store
	git   gitx.Git
}

// NewGit returns the git channel backed by st and g.
func NewGit(st *store.Store, g gitx.Git) *Git { return &Git{store: st, git: g} }

// Ownership reports that the store holds the files and the links undo them.
func (c *Git) Ownership() Ownership { return StoreOwned }

// selection pairs a discovered skill with the name it will be linked under.
// The narrowing below works on selections rather than Candidates because
// --skill matches a skill's position in the repository as well as its name,
// and that position is relative to the walk root rather than to the revision.
type selection struct {
	skill discover.Skill
	name  string
}

// Prepare resolves the ref, extracts the revision, and narrows the skills it
// found to the ones the request asked for.
func (c *Git) Prepare(ctx context.Context, req Request) ([]Candidate, error) {
	src := req.Source

	sha, err := c.git.Resolve(ctx, src.RepoURL, req.Ref)
	if err != nil {
		return nil, err
	}

	// Populating the content-addressed cache is idempotent and not a
	// user-visible mutation, so it runs even for --dry-run. It is what lets
	// the plan name the skills exactly rather than guess.
	revRoot, err := c.store.Ensure(ctx, c.git, src.Slug(), src.RepoURL, sha)
	if err != nil {
		return nil, err
	}
	revPath, err := store.Join(revRoot, src.Subpath)
	if err != nil {
		return nil, fmt.Errorf("refusing to install: %w", err)
	}

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

	chosen, err := narrow(available, req)
	if err != nil {
		var amb *Ambiguous
		if errors.As(err, &amb) {
			amb.Header = fmt.Sprintf("skills in %s @ %s:", src.RepoURL, shortSha(sha))
			amb.Meta = discover.PluginMeta(revPath)
			amb.Available = brief(available)
		}
		return nil, err
	}

	return c.candidates(chosen, revRoot, sha)
}

// narrow reduces the discovered skills to the ones the request asked for.
// Install never guesses which skill was meant, so a request it cannot narrow
// comes back as *Ambiguous with everything the caller needs to list.
func narrow(available []selection, req Request) ([]selection, error) {
	switch {
	case req.All:
		return available, nil

	case len(req.Skills) > 0:
		chosen, err := pickSkills(available, req.Skills)
		if err != nil {
			return nil, &Ambiguous{Reason: err.Error()}
		}
		return chosen, nil

	case len(available) == 1:
		return available, nil

	default:
		return nil, &Ambiguous{
			Reason: fmt.Sprintf("this repository holds %d skills: pass --skill <name> (repeatable) or --all", len(available)),
		}
	}
}

// candidates fills in everything Install will need, so that Install itself
// touches neither the store nor the filesystem.
func (c *Git) candidates(sels []selection, revRoot, sha string) ([]Candidate, error) {
	out := make([]Candidate, 0, len(sels))
	for _, s := range sels {
		hash, err := store.HashDir(s.skill.Dir)
		if err != nil {
			return nil, err
		}
		subpath, err := filepath.Rel(revRoot, s.skill.Dir)
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
			Version: sha,
			Hash:    hash,
		})
	}
	return out, nil
}

// brief renders selections for a listing, which needs no revision path and no
// hash: the point of the listing is to name what could have been asked for.
func brief(sels []selection) []Candidate {
	out := make([]Candidate, 0, len(sels))
	for _, s := range sels {
		out = append(out, Candidate{Name: s.name, Desc: s.skill.Description})
	}
	return out
}

// Install links each candidate into each target and records a receipt. The
// whole install is one plan, so a failure part-way leaves nothing behind.
func (c *Git) Install(req Request, chosen []Candidate) (plan.Plan, []state.Receipt, []string, error) {
	var p plan.Plan
	receipts := make([]state.Receipt, 0, len(chosen))
	now := time.Now().UTC()

	for _, s := range chosen {
		receipt := state.Receipt{
			Name:        s.Name,
			Channel:     string(source.ChannelGit),
			Source:      req.Source.RepoURL,
			Slug:        req.Source.Slug(),
			Subpath:     s.Subpath,
			Resolved:    s.Version,
			Pinned:      req.Pin,
			RevPath:     s.Path,
			ContentHash: s.Hash,
			InstalledAt: now,
			UpdatedAt:   now,
		}
		if !req.Pin {
			receipt.Ref = req.Ref
		}

		for _, t := range req.Targets {
			linkPath, err := linkPathFor(t, s.Name)
			if err != nil {
				return p, nil, nil, err
			}
			p.Add(plan.Link{Target: t.Name, LinkPath: linkPath, RevPath: s.Path})
			receipt.Links = append(receipt.Links, state.Link{Target: t.Name, Path: linkPath})
		}
		p.Add(plan.Record{Receipt: receipt})
		receipts = append(receipts, receipt)
	}
	return p, receipts, nil, nil
}

// resolution is one ls-remote answer, cached so that N skills installed from
// one repository cost one round trip rather than N.
type resolution struct {
	sha string
	err error
}

// Update re-points each receipt at the head of the ref it tracks.
//
// Only a request that cannot be interpreted at all comes back as an error.
// Everything else is a per-receipt verdict, so one unreachable remote or one
// dirty skill never hides the rest.
//
// Update extracts the revisions it plans to link. That is a side effect, but
// not a user-visible one: the store is content-addressed and populating it is
// idempotent, and it is what lets a --dry-run name the exact revision path
// rather than a sha it has not yet fetched.
func (c *Git) Update(ctx context.Context, rs []*state.Receipt, o UpdateOptions) ([]Verdict, plan.Plan, error) {
	seen := map[string]resolution{}
	verdicts := make([]Verdict, 0, len(rs))

	var p plan.Plan
	now := time.Now().UTC()

	for _, r := range rs {
		// An empty ref means the repository's default branch. Install records
		// no ref for a pinned skill, so that is also what a pin resolves
		// against when one is named explicitly.
		ref := r.Ref
		if ref == "" {
			ref = "HEAD"
		}

		v := Verdict{Name: r.Name, Channel: r.Channel, Ref: ref, Current: r.Resolved, Pinned: r.Pinned}

		if r.Pinned && !o.Named {
			v.Status = StatusPinned
			verdicts = append(verdicts, v)
			continue
		}

		key := r.Source + "\x00" + ref
		got, ok := seen[key]
		if !ok {
			got.sha, got.err = c.git.Resolve(ctx, r.Source, ref)
			seen[key] = got
		}
		if got.err != nil {
			verdicts = append(verdicts, fail(v, got.err))
			continue
		}

		v.Latest = got.sha
		if got.sha == r.Resolved {
			v.Status = StatusCurrent
			verdicts = append(verdicts, v)
			continue
		}

		dirty, note, err := inspect(r)
		if err != nil {
			verdicts = append(verdicts, fail(v, err))
			continue
		}
		if dirty && !o.Force {
			v.Status = StatusDirty
			verdicts = append(verdicts, v)
			continue
		}
		v.Note = note

		ops, receipt, err := c.relink(ctx, r, got.sha, now)
		if err != nil {
			verdicts = append(verdicts, fail(v, err))
			continue
		}

		p.Add(ops...)
		p.Add(plan.Record{Receipt: receipt})
		v.Status = StatusUpdated
		verdicts = append(verdicts, v)
	}

	return verdicts, p, nil
}

// Settle has nothing to complete: a sha is known before the plan is built, and
// so is every path derived from it.
func (c *Git) Settle(context.Context, []state.Receipt) ([]state.Receipt, error) {
	return nil, nil
}

// inspect reports whether the linked subtree has been edited since it was
// installed. A revision directory carries no .git, so the hash recorded at
// install time is the only way to notice.
//
// Two cases are deliberately not dirty. A receipt with no recorded hash
// predates the field or was written by hand, and refusing to update it would
// leave it stuck for good. A revision directory that has gone missing takes
// the symlink with it, so there are no edits to lose and the update repairs a
// link that is already dangling.
func inspect(r *state.Receipt) (dirty bool, note string, err error) {
	if r.ContentHash == "" {
		return false, "", nil
	}
	hash, err := store.HashDir(r.RevPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, fmt.Sprintf("%s was missing from the store", r.RevPath), nil
		}
		return false, "", err
	}
	return hash != r.ContentHash, "", nil
}

// relink extracts sha and builds the ops that move one receipt onto it.
func (c *Git) relink(ctx context.Context, r *state.Receipt, sha string, now time.Time) ([]plan.Op, state.Receipt, error) {
	slug, err := slugFor(r)
	if err != nil {
		return nil, state.Receipt{}, err
	}

	revRoot, err := c.store.Ensure(ctx, c.git, slug, r.Source, sha)
	if err != nil {
		return nil, state.Receipt{}, err
	}
	revPath, err := store.Join(revRoot, r.Subpath)
	if err != nil {
		return nil, state.Receipt{}, err
	}

	// The skill may have been moved or deleted upstream. Saying so is better
	// than re-pointing the symlink at a directory that is not a skill.
	if _, err := discover.Root(revPath); err != nil {
		return nil, state.Receipt{}, fmt.Errorf("%s no longer holds a skill at %s: %w", shortSha(sha), subpathOrRoot(r.Subpath), err)
	}

	hash, err := store.HashDir(revPath)
	if err != nil {
		return nil, state.Receipt{}, err
	}

	ops := make([]plan.Op, 0, len(r.Links))
	for _, l := range r.Links {
		ops = append(ops, plan.Relink{Target: l.Target, LinkPath: l.Path, RevPath: revPath})
	}

	// Everything the user chose at install time survives an update: the name
	// they installed it under, the agents they linked it into, the ref it
	// tracks, and the pin. Only what the new revision decides changes.
	receipt := *r
	receipt.Resolved = sha
	receipt.RevPath = revPath
	receipt.ContentHash = hash
	receipt.UpdatedAt = now
	return ops, receipt, nil
}

// slugFor is where in the store this receipt's revisions live. Receipts written
// before the slug was recorded fall back to deriving it from the source, which
// is what install did to produce it in the first place.
func slugFor(r *state.Receipt) (string, error) {
	if r.Slug != "" {
		return r.Slug, nil
	}
	src, err := source.Parse(r.Source)
	if err != nil {
		return "", fmt.Errorf("this receipt records no store location and %q cannot be parsed: %w", r.Source, err)
	}
	return src.Slug(), nil
}

// resolveNames gives every discovered skill its link name: the frontmatter
// name, else fallback for a skill that is the walk root, else the skill's own
// directory name. fallback is the name derived from the source, which only
// makes sense for a skill with no directory of its own to be named after.
func resolveNames(found []discover.Skill, fallback string) ([]selection, error) {
	sels := make([]selection, 0, len(found))
	for _, s := range found {
		name, origin := s.Name, "the SKILL.md in "+s.Rel
		if name == "" {
			if s.Rel == "." {
				name, origin = fallback, "the source"
			} else {
				name, origin = path.Base(s.Rel), "the directory "+s.Rel
			}
		}
		if err := target.ValidateSkillName(name); err != nil {
			return nil, fmt.Errorf("refusing to install: %w (from %s); pass --as <name> to choose one", err, origin)
		}
		sels = append(sels, selection{skill: s, name: name})
	}

	seen := make(map[string]string, len(sels))
	for _, s := range sels {
		if prev, ok := seen[s.name]; ok {
			return nil, fmt.Errorf("%s and %s both resolve to the name %q: install them one at a time with --skill and --as",
				prev, s.skill.Rel, s.name)
		}
		seen[s.name] = s.skill.Rel
	}
	return sels, nil
}

// pickSkills selects the skills the user named, in the order they named them.
// A name matches a skill's resolved name first, then its path within the
// repository, so a skill whose frontmatter is missing or ambiguous can still be
// asked for. Asking for the same skill twice installs it once.
func pickSkills(all []selection, wanted []string) ([]selection, error) {
	out := make([]selection, 0, len(wanted))
	chosen := make(map[string]bool, len(wanted))

	for _, want := range wanted {
		s, ok := matchSkill(all, want)
		if !ok {
			return nil, fmt.Errorf("no skill named %q in this repository", want)
		}
		if chosen[s.skill.Rel] {
			continue
		}
		chosen[s.skill.Rel] = true
		out = append(out, s)
	}
	return out, nil
}

// matchSkill resolves one --skill value: by name, then by path.
func matchSkill(all []selection, want string) (selection, bool) {
	for _, s := range all {
		if s.name == want {
			return s, true
		}
	}
	for _, s := range all {
		if s.skill.Rel == want {
			return s, true
		}
	}
	return selection{}, false
}

func fail(v Verdict, err error) Verdict {
	v.Status = StatusError
	v.Error = err.Error()
	return v
}

func subpathOrRoot(subpath string) string {
	if subpath == "" {
		return "its root"
	}
	return subpath
}

func shortSha(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
