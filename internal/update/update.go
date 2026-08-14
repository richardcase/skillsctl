// Package update moves installed skills onto a newer revision of the ref they
// track. It decides what each receipt should become and returns the mutations
// as a plan, so applying it — or printing it for --dry-run — is the caller's
// job and both take the same path through this package.
package update

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/richardcase/skillsctl/internal/discover"
	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/store"
)

// Status is the verdict for one receipt.
type Status string

const (
	// StatusUpdated means the ref has moved and the plan re-links the skill.
	StatusUpdated Status = "updated"
	// StatusCurrent means the tracked ref still points at the installed sha.
	StatusCurrent Status = "current"
	// StatusPinned means the skill is pinned and was not named explicitly.
	StatusPinned Status = "pinned"
	// StatusDirty means the linked subtree was edited since it was installed.
	StatusDirty Status = "dirty"
	// StatusSkipped means the receipt has no upstream to update from.
	StatusSkipped Status = "n/a"
	// StatusError means this skill could not be updated.
	StatusError Status = "error"
)

// Entry is the verdict for one receipt.
type Entry struct {
	Name    string
	Channel string
	Ref     string
	Current string
	Latest  string
	Pinned  bool
	Status  Status
	Error   string
	// Note carries something worth saying about an entry that was still
	// updated, such as a revision directory that had gone missing.
	Note string
}

// Options narrows and loosens what Plan will do.
type Options struct {
	// Names selects skills by name. Empty means every receipt, minus the
	// pinned ones: a pin is only overridden by naming the skill.
	Names []string
	// Force updates a skill whose linked subtree no longer matches the hash
	// recorded at install time.
	Force bool
}

// resolution is one ls-remote answer, cached so that N skills installed from
// one repository cost one round trip rather than N.
type resolution struct {
	sha string
	err error
}

// Plan returns one Entry per selected receipt, in the order they were given,
// and the plan that carries out every entry whose Status is StatusUpdated.
//
// Only a request that cannot be interpreted at all — a name that is not
// installed — comes back as an error. Everything else is a per-entry verdict,
// so one unreachable remote or one dirty skill never hides the rest.
//
// Plan extracts the revisions it plans to link. That is a side effect, but not
// a user-visible one: the store is content-addressed and populating it is
// idempotent, and it is what lets a --dry-run name the exact revision path
// rather than a sha it has not yet fetched.
func Plan(ctx context.Context, g gitx.Git, s *store.Store, receipts []*state.Receipt, o Options) ([]Entry, plan.Plan, error) {
	selected, err := selectReceipts(receipts, o.Names)
	if err != nil {
		return nil, plan.Plan{}, err
	}

	named := len(o.Names) > 0
	seen := map[string]resolution{}
	entries := make([]Entry, 0, len(selected))

	var p plan.Plan
	now := time.Now().UTC()

	for _, r := range selected {
		// An empty ref means the repository's default branch. Install records
		// no ref for a pinned skill, so that is also what a pin resolves
		// against when one is named explicitly.
		ref := r.Ref
		if ref == "" {
			ref = "HEAD"
		}

		e := Entry{Name: r.Name, Channel: r.Channel, Ref: ref, Current: r.Resolved, Pinned: r.Pinned}

		switch {
		case r.Channel != string(source.ChannelGit):
			// Only the git channel has a ref that can move.
			e.Status = StatusSkipped
			entries = append(entries, e)
			continue
		case r.Pinned && !named:
			e.Status = StatusPinned
			entries = append(entries, e)
			continue
		}

		key := r.Source + "\x00" + ref
		got, ok := seen[key]
		if !ok {
			got.sha, got.err = g.Resolve(ctx, r.Source, ref)
			seen[key] = got
		}
		if got.err != nil {
			entries = append(entries, fail(e, got.err))
			continue
		}

		e.Latest = got.sha
		if got.sha == r.Resolved {
			e.Status = StatusCurrent
			entries = append(entries, e)
			continue
		}

		dirty, note, err := inspect(r)
		if err != nil {
			entries = append(entries, fail(e, err))
			continue
		}
		if dirty && !o.Force {
			e.Status = StatusDirty
			entries = append(entries, e)
			continue
		}
		e.Note = note

		ops, receipt, err := relink(ctx, g, s, r, got.sha, now)
		if err != nil {
			entries = append(entries, fail(e, err))
			continue
		}

		p.Add(ops...)
		p.Add(plan.Record{Receipt: receipt})
		e.Status = StatusUpdated
		entries = append(entries, e)
	}

	return entries, p, nil
}

// selectReceipts narrows receipts to the names asked for. A name that is not
// installed is the one thing that fails the whole command: the user asked for
// something specific that does not exist, and carrying on would silently
// update a different set than the one they typed.
func selectReceipts(receipts []*state.Receipt, names []string) ([]*state.Receipt, error) {
	if len(names) == 0 {
		return receipts, nil
	}

	byName := make(map[string]*state.Receipt, len(receipts))
	for _, r := range receipts {
		byName[r.Name] = r
	}

	out := make([]*state.Receipt, 0, len(names))
	taken := make(map[string]bool, len(names))
	for _, name := range names {
		r, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("%q is not installed: run `skillsctl list` to see what is", name)
		}
		// Naming a skill twice is one request for it, not two: planning it
		// twice would report it twice and re-link it over itself.
		if taken[name] {
			continue
		}
		taken[name] = true
		out = append(out, r)
	}
	return out, nil
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
func relink(ctx context.Context, g gitx.Git, s *store.Store, r *state.Receipt, sha string, now time.Time) ([]plan.Op, state.Receipt, error) {
	slug, err := slugFor(r)
	if err != nil {
		return nil, state.Receipt{}, err
	}

	revRoot, err := s.Ensure(ctx, g, slug, r.Source, sha)
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

func fail(e Entry, err error) Entry {
	e.Status = StatusError
	e.Error = err.Error()
	return e
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
