// Package adopt answers one question about an agent's skills directory: which
// of the things already in it can skillsctl honestly manage?
//
// It reads and decides; it never writes. Turning a decision into a receipt or a
// plan belongs to the caller, which is why nothing here imports channel or
// plan. That split is what lets the scan be exact: --dry-run and the real run
// see the same answer because they run the same read-only pass.
package adopt

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/richardcase/skillsctl/internal/discover"
	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/store"
	"github.com/richardcase/skillsctl/internal/target"
)

// Class is what the scan decided about one entry.
type Class string

const (
	// ClassLocal is adoptable as a local skill: a symlink to a directory that
	// skillsctl can record but has no provenance for.
	ClassLocal Class = "local"
	// ClassGit is adoptable and its provenance is recoverable: the symlink
	// points into a clean git working copy with a remote.
	ClassGit Class = "git"
	// ClassManaged means a receipt already covers it, so there is nothing to do.
	ClassManaged Class = "managed"
	// ClassLink is a link to add to a receipt that already exists: a hand-made
	// symlink into a second agent, pointing where that receipt says its files
	// are. It adopts as a link rather than a receipt, because the receipt is
	// already there and adopting the name again would overwrite it.
	ClassLink Class = "link"
	// ClassSkipped means it cannot be adopted; Reason says why.
	ClassSkipped Class = "skipped"
)

// Entry is one name in one agent's skills directory.
type Entry struct {
	Name   string `json:"name"`
	Target string `json:"target"`
	Path   string `json:"path"`
	Dest   string `json:"dest,omitempty"`
	Class  Class  `json:"class"`
	// Reason says why an entry was skipped, or why one that looked like a git
	// checkout is being adopted as local anyway.
	Reason string `json:"reason,omitempty"`
	// Repo is set when Class is ClassGit, and is the whole of what a git
	// receipt needs to be written.
	Repo *Provenance `json:"-"`
}

// Provenance is where an adoptable entry came from, reduced to the facts a git
// receipt records. The remote is kept parsed rather than raw because that is
// the form a receipt stores it in: the canonical URL and the slug both come
// from it, and a remote that does not parse as a git source is not provenance
// at all.
type Provenance struct {
	Repo    source.Source
	SHA     string
	Subpath string
}

// Adoption is one receipt adopt would write, and every link it covers.
//
// Receipts are keyed by name alone, so the same skill linked into two agents is
// one adoption with two links rather than two adoptions that would overwrite
// each other.
type Adoption struct {
	Name  string
	Class Class
	Dest  string
	Repo  *Provenance
	Links []state.Link
}

// Report is everything one scan found.
type Report struct {
	Entries []Entry `json:"entries"`
}

// Scan classifies every direct child of each target's skills directory.
//
// Only direct children are considered: a link name in an agent's skills
// directory is exactly one path segment, so anything nested is part of a skill
// rather than a skill of its own.
func Scan(ctx context.Context, ts []target.Target, db *state.DB, g gitx.Git, st *store.Store) (Report, error) {
	var rep Report

	for _, t := range ts {
		names, err := children(t.Dir)
		if err != nil {
			return Report{}, err
		}
		for _, name := range names {
			rep.Entries = append(rep.Entries, classify(ctx, t, name, db, g, st))
		}
	}
	return rep, nil
}

// children lists the entries of an agent's skills directory. A target counts as
// present when its parent exists, so a missing skills directory is an agent
// that has never installed anything rather than an error.
func children(dir string) ([]string, error) {
	des, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}

	names := make([]string, 0, len(des))
	for _, de := range des {
		// A dot entry is the agent's own bookkeeping — codex keeps a .system
		// directory here — and nothing loads it as a skill. Reporting it as
		// unadoptable would put a permanent skipped entry, and so a permanent
		// exit code 2, on a machine with nothing wrong with it.
		if strings.HasPrefix(de.Name(), ".") {
			continue
		}
		names = append(names, de.Name())
	}
	sort.Strings(names)
	return names, nil
}

// classify decides about one entry. The order of the checks is the order in
// which an answer becomes certain: what the thing is on disk, then whether it
// is already ours, then whether it is a skill at all, and only then where it
// might have come from.
func classify(ctx context.Context, t target.Target, name string, db *state.DB, g gitx.Git, st *store.Store) Entry {
	e := Entry{Name: name, Target: t.Name, Path: filepath.Join(t.Dir, name)}

	fi, err := os.Lstat(e.Path)
	if err != nil {
		return skip(e, fmt.Sprintf("could not be read: %v", err))
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		// There is no symlink to record, and Links is the removal contract: a
		// receipt without one could never be removed. Moving the directory is
		// the one thing adopt must not do, so name the remedy instead.
		return skip(e, fmt.Sprintf("not a symlink: move it out of %s and run skillsctl link on it", t.Dir))
	}

	dest, err := resolve(e.Path)
	if err != nil {
		return skip(e, err.Error())
	}
	e.Dest = dest

	if fi, err := os.Stat(dest); err != nil {
		if os.IsNotExist(err) {
			return skip(e, fmt.Sprintf("dangling symlink to %s", dest))
		}
		return skip(e, fmt.Sprintf("could not be read: %v", err))
	} else if !fi.IsDir() {
		return skip(e, fmt.Sprintf("%s is not a directory", dest))
	}

	// Whether a receipt claims this name is asked before anything else about
	// the link, because it is the stronger fact: every skill skillsctl
	// installed itself points into the store, and a managed one must not be
	// mistaken for the orphan below.
	if r, ok := db.Receipts[name]; ok {
		return managed(e, r)
	}

	// A link into the store that no receipt claims is an orphan: the slug it
	// sits under does not reverse into the source it came from, so adopting it
	// would record a receipt that cannot be updated. Reporting it is doctor's
	// job; all adopt can say is that it is not ours to take.
	if st.Contains(dest) {
		return skip(e, "points into the skillsctl store but no receipt claims it")
	}

	// The name comes off the filesystem and becomes a receipt key, so it is
	// third-party data like any other.
	if err := target.ValidateSkillName(name); err != nil {
		return skip(e, err.Error())
	}

	if _, err := discover.Root(dest); err != nil {
		if errors.Is(err, discover.ErrNoSkill) {
			return skip(e, fmt.Sprintf("no %s in %s", discover.FileName, dest))
		}
		return skip(e, err.Error())
	}

	return promote(ctx, e, g)
}

// promote decides between the git and local channels.
//
// Everything it refuses falls back to local rather than being skipped: the
// skill is adoptable either way, and the only thing at stake is whether the
// receipt can also carry provenance.
func promote(ctx context.Context, e Entry, g gitx.Git) Entry {
	e.Class = ClassLocal

	o, err := g.Describe(ctx, e.Dest)
	switch {
	case err != nil:
		// Not a working copy at all, which is the ordinary case.
		return e
	case o.RepoURL == "":
		e.Reason = "a git working copy with no remote"
		return e
	case o.Dirty:
		// The sha would name a tree other than the files on disk, and a
		// receipt that says where something came from must be true.
		e.Reason = "a git working copy with uncommitted changes"
		return e
	}

	// A remote git itself is happy with is not necessarily one skillsctl can
	// install from later — a bare filesystem path is the common case — and a
	// receipt whose source cannot be re-installed is worse than one that never
	// claimed to know.
	repo, err := source.Parse(o.RepoURL)
	if err != nil || repo.Channel != source.ChannelGit {
		e.Reason = fmt.Sprintf("a git working copy whose remote is not a git source: %s", o.RepoURL)
		return e
	}

	e.Class = ClassGit
	e.Repo = &Provenance{Repo: repo, SHA: o.SHA, Subpath: o.Prefix}
	return e
}

// resolve follows one symlink to an absolute path. A relative target is
// resolved against the directory holding the link, which is how the filesystem
// reads it.
func resolve(linkPath string) (string, error) {
	dest, err := os.Readlink(linkPath)
	if err != nil {
		return "", fmt.Errorf("could not be read: %w", err)
	}
	if !filepath.IsAbs(dest) {
		dest = filepath.Join(filepath.Dir(linkPath), dest)
	}
	return filepath.Clean(dest), nil
}

func skip(e Entry, reason string) Entry {
	e.Class = ClassSkipped
	e.Reason = reason
	return e
}

// managed reports an entry a receipt already covers.
//
// A receipt that does not record *this* link is the second-link case: the skill
// is managed, but for another agent, and the symlink in front of us is one
// `skillsctl link <name> -a <agent>` would have made. It is adopted as a link
// rather than a receipt, because adopting the name again would overwrite the
// receipt that is already there.
//
// The condition is that the symlink already points at the receipt's own
// RevPath. A receipt says where its links point — update re-points every one of
// them and remove deletes every one of them — so recording a link to anywhere
// else would make skillsctl act on a directory the user never gave it.
func managed(e Entry, r *state.Receipt) Entry {
	for _, l := range r.Links {
		if l.Path == e.Path {
			e.Class = ClassManaged
			return e
		}
	}

	where := agents(r)
	if where == "" {
		return skip(e, fmt.Sprintf("a receipt for %q already exists", e.Name))
	}

	// Links is a set keyed by target: Remove builds its drop filter from the
	// target name and Unlink treats a missing link as success, so a second link
	// for one agent would plan two unlinks of one path and swallow the second.
	for _, l := range r.Links {
		if l.Target == e.Target {
			return skip(e, fmt.Sprintf("%s is already managed for %s, at %s", e.Name, e.Target, l.Path))
		}
	}

	if r.RevPath == "" || e.Dest != r.RevPath {
		return skip(e, fmt.Sprintf("%s points at %s but the receipt managing it for %s points at %s",
			e.Name, e.Dest, where, r.RevPath))
	}

	e.Class = ClassLink
	return e
}

func agents(r *state.Receipt) string {
	names := make([]string, 0, len(r.Links))
	for _, l := range r.Links {
		names = append(names, l.Target)
	}
	return strings.Join(names, ", ")
}

// Adoptions groups the adoptable entries into the receipts they would become,
// sorted by name.
//
// One name pointing at two different directories is the case this exists to
// catch: receipts are keyed by name, so adopting both would silently keep
// whichever came last. Neither is adopted, and both say so.
func (r Report) Adoptions() []Adoption {
	byName := map[string][]Entry{}
	var order []string
	for _, e := range r.Entries {
		if e.Class != ClassLocal && e.Class != ClassGit {
			continue
		}
		if _, ok := byName[e.Name]; !ok {
			order = append(order, e.Name)
		}
		byName[e.Name] = append(byName[e.Name], e)
	}
	sort.Strings(order)

	out := make([]Adoption, 0, len(order))
	for _, name := range order {
		group := byName[name]
		if conflicted(group) {
			continue
		}
		a := Adoption{Name: name, Class: group[0].Class, Dest: group[0].Dest, Repo: group[0].Repo}
		for _, e := range group {
			a.Links = append(a.Links, state.Link{Target: e.Target, Path: e.Path})
		}
		out = append(out, a)
	}
	return out
}

// Addition is a set of links to add to a receipt that already exists.
//
// It is separate from Adoption because the two write different things: an
// adoption is a whole new receipt, an addition amends one that is there. They
// can never collide over a name — once a receipt claims a name, every entry
// under it goes through managed, so it is classified as one or the other.
type Addition struct {
	Name  string
	Links []state.Link
}

// Additions groups the second links into the receipts they amend, sorted by
// name, for the same reason Adoptions groups: receipts are keyed by name, so
// one skill hand-linked into two agents is one amendment carrying two links
// rather than two that would overwrite each other.
func (r Report) Additions() []Addition {
	byName := map[string][]state.Link{}
	var order []string
	for _, e := range r.Entries {
		if e.Class != ClassLink {
			continue
		}
		if _, ok := byName[e.Name]; !ok {
			order = append(order, e.Name)
		}
		byName[e.Name] = append(byName[e.Name], state.Link{Target: e.Target, Path: e.Path})
	}
	sort.Strings(order)

	out := make([]Addition, 0, len(order))
	for _, name := range order {
		out = append(out, Addition{Name: name, Links: byName[name]})
	}
	return out
}

// Skipped is every entry that could not be adopted, including the halves of a
// name collision, in the order they were found.
func (r Report) Skipped() []Entry {
	byName := map[string][]Entry{}
	for _, e := range r.Entries {
		if e.Class == ClassLocal || e.Class == ClassGit {
			byName[e.Name] = append(byName[e.Name], e)
		}
	}

	var out []Entry
	for _, e := range r.Entries {
		switch {
		case e.Class == ClassSkipped:
			out = append(out, e)
		case conflicted(byName[e.Name]):
			out = append(out, skip(e, fmt.Sprintf("%s points at a different directory in each agent: %s", e.Name, destinations(byName[e.Name]))))
		}
	}
	return out
}

// Managed counts the entries a receipt already covers.
func (r Report) Managed() int {
	n := 0
	for _, e := range r.Entries {
		if e.Class == ClassManaged {
			n++
		}
	}
	return n
}

func conflicted(group []Entry) bool {
	if len(group) == 0 {
		return false
	}
	for _, e := range group[1:] {
		if e.Dest != group[0].Dest || e.Class != group[0].Class {
			return true
		}
	}
	return false
}

func destinations(group []Entry) string {
	seen := make([]string, 0, len(group))
	for _, e := range group {
		seen = append(seen, fmt.Sprintf("%s -> %s", e.Target, e.Dest))
	}
	return strings.Join(seen, ", ")
}
