// Package doctor answers one question: does what is on disk still match what
// the receipts say?
//
// It reads and decides; it never writes. Every finding names the command that
// repairs it rather than repairing anything itself, which is what lets the whole
// command run under the state lock without ever taking a decision away from the
// user. Nothing here imports channel or plan, for the same reason adopt does
// not: a scan that cannot mutate cannot disagree with itself between a dry run
// and a real one.
package doctor

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/store"
	"github.com/richardcase/skillsctl/internal/target"
)

// Kind is a class of inconsistency, and the thing findings are grouped by.
type Kind string

const (
	// KindMissingLink is a link a receipt records that is no longer on disk.
	KindMissingLink Kind = "missing-link"
	// KindNotASymlink is a receipt's link path occupied by something that is
	// not a symlink, so unlinking it would delete the user's own files.
	KindNotASymlink Kind = "not-a-symlink"
	// KindDanglingLink is a symlink in an agent's skills directory that
	// resolves to nothing.
	KindDanglingLink Kind = "dangling-link"
	// KindWrongTarget is a managed link that resolves somewhere other than the
	// revision its receipt records.
	KindWrongTarget Kind = "wrong-target"
	// KindNameCollision is one name resolving to different content in
	// different agents.
	KindNameCollision Kind = "name-collision"
	// KindMissingRevision is a receipt whose revision directory in the store is
	// not on disk.
	KindMissingRevision Kind = "missing-revision"
	// KindMissingSource is a receipt whose linked directory is the user's own,
	// outside the store, and is no longer there. It is separate from
	// KindMissingRevision because nothing can re-fetch it: a revision comes
	// back with update, and a directory somebody deleted does not.
	KindMissingSource Kind = "missing-source"
	// KindContentDrift is a linked subtree that no longer hashes to what was
	// recorded when it was installed — it has been edited through the symlink.
	KindContentDrift Kind = "content-drift"
	// KindOrphanLink is a symlink into the store that no receipt claims.
	KindOrphanLink Kind = "orphan-link"
	// KindOrphanRevision is a revision directory no receipt references.
	KindOrphanRevision Kind = "orphan-revision"
)

// order is the order findings are reported in, worst first: a link that is
// gone breaks a skill outright, while an orphan revision only costs disk.
var order = []Kind{
	KindMissingLink,
	KindNotASymlink,
	KindDanglingLink,
	KindWrongTarget,
	KindNameCollision,
	KindMissingSource,
	KindMissingRevision,
	KindContentDrift,
	KindOrphanLink,
	KindOrphanRevision,
}

// titles are the headings the plain-text report groups findings under.
var titles = map[Kind]string{
	KindMissingLink:     "missing links",
	KindNotASymlink:     "links replaced by something else",
	KindDanglingLink:    "dangling links",
	KindWrongTarget:     "links pointing away from their receipt",
	KindNameCollision:   "name collisions",
	KindMissingSource:   "linked directories that are gone",
	KindMissingRevision: "revisions missing from the store",
	KindContentDrift:    "edited since install",
	KindOrphanLink:      "links no receipt claims",
	KindOrphanRevision:  "orphan revisions",
}

// Finding is one thing that is wrong, and how to put it right.
type Finding struct {
	Kind Kind `json:"kind"`
	// Name is the skill; empty for a finding about the store rather than a
	// skill, which is to say an orphan revision.
	Name string `json:"name,omitempty"`
	// Target is the agent, where one is implicated.
	Target string `json:"target,omitempty"`
	// Path is the link or the revision directory the finding is about.
	Path string `json:"path,omitempty"`
	// Bytes is what deleting the path would reclaim, set for orphan revisions.
	Bytes  int64  `json:"bytes,omitempty"`
	Detail string `json:"detail"`
	// Remedy is the command that repairs this finding on its own. A group of
	// findings of one kind repairs in a single command; see Group.Remedy.
	Remedy string `json:"remedy"`
}

// Unscanned is an agent whose skills directory could not be read, which makes
// the report incomplete rather than clean.
type Unscanned struct {
	Target string `json:"target"`
	Path   string `json:"path"`
	Error  string `json:"error"`
}

// Report is everything one scan found.
type Report struct {
	Findings  []Finding   `json:"findings"`
	Unscanned []Unscanned `json:"unscanned,omitempty"`
}

// Group is every finding of one kind, under the single command that repairs
// them all.
type Group struct {
	Kind     Kind      `json:"kind"`
	Title    string    `json:"title"`
	Remedy   string    `json:"remedy"`
	Findings []Finding `json:"findings"`
}

// IsEmpty reports whether nothing is wrong.
func (r Report) IsEmpty() bool { return len(r.Findings) == 0 }

// Groups collects the findings by kind, worst first, each under one remedy.
func (r Report) Groups() []Group {
	byKind := map[Kind][]Finding{}
	for _, f := range r.Findings {
		byKind[f.Kind] = append(byKind[f.Kind], f)
	}

	out := make([]Group, 0, len(byKind))
	for _, k := range order {
		group := byKind[k]
		if len(group) == 0 {
			continue
		}
		out = append(out, Group{Kind: k, Title: titles[k], Remedy: remedy(k, names(group)), Findings: group})
	}
	return out
}

// Skills counts the distinct skills implicated, so a summary can say "3
// problems in 2 skills" rather than implying one skill each.
func (r Report) Skills() int {
	seen := map[string]bool{}
	for _, f := range r.Findings {
		if f.Name != "" {
			seen[f.Name] = true
		}
	}
	return len(seen)
}

// Scan compares every receipt against the filesystem, every agent's skills
// directory against the receipts, and the store against both.
//
// live is passed in rather than derived because deciding which receipts hold a
// revision alive is a question about channel ownership, and a scan that cannot
// mutate should not need the channel registry to answer it.
func Scan(ts []target.Target, db *state.DB, st *store.Store, live store.Live) (Report, error) {
	// Findings starts empty rather than nil so a clean report marshals to
	// "findings": [] — `skillsctl doctor --json | jq '.findings[]'` should say
	// nothing is wrong, not fail on null.
	rep := Report{Findings: []Finding{}}

	if err := checkReceipts(&rep, db, st); err != nil {
		return Report{}, err
	}
	checkDirectories(&rep, ts, db, st)
	if err := checkStore(&rep, st, live); err != nil {
		return Report{}, err
	}
	return rep, nil
}

// checkReceipts looks outward from the receipts: what they claim exists, and
// whether it still does.
//
// It reports only links that are absent. A link that is present is left to the
// directory pass, which has the entry in hand and can say what is wrong with
// it — reporting from both ends would report the same damage twice.
func checkReceipts(rep *Report, db *state.DB, st *store.Store) error {
	for _, r := range db.List() {
		for _, l := range r.Links {
			if _, err := os.Lstat(l.Path); err != nil {
				rep.add(Finding{
					Kind: KindMissingLink, Name: r.Name, Target: l.Target, Path: l.Path,
					Detail: fmt.Sprintf("%s is recorded but not on disk", l.Path),
				})
			}
		}

		// A plugin's files belong to the agent that installed them: it records
		// no revision and no hash, so both checks below fall away without a
		// channel special case. A local skill records a directory of the
		// user's own but no hash, so only the first applies to it.
		if r.RevPath == "" {
			continue
		}
		if fi, err := os.Stat(r.RevPath); err != nil || !fi.IsDir() {
			// A revision in the store can be fetched again; a directory of the
			// user's own cannot, so the two are different findings with
			// different remedies even though the check is one stat.
			kind := KindMissingSource
			if st.Contains(r.RevPath) {
				kind = KindMissingRevision
			}
			rep.add(Finding{
				Kind: kind, Name: r.Name, Path: r.RevPath,
				Detail: revisionDetail(r.RevPath, err),
			})
			continue
		}
		if r.ContentHash == "" {
			continue
		}

		hash, err := store.HashDir(r.RevPath)
		if err != nil {
			return fmt.Errorf("hash %s: %w", r.RevPath, err)
		}
		if hash != r.ContentHash {
			rep.add(Finding{
				Kind: KindContentDrift, Name: r.Name, Path: r.RevPath,
				Detail: fmt.Sprintf("no longer matches what was installed from %s", short(r.Resolved)),
			})
		}
	}
	return nil
}

func revisionDetail(path string, err error) string {
	switch {
	case err == nil:
		return fmt.Sprintf("%s is not a directory", path)
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Sprintf("%s is gone", path)
	default:
		return fmt.Sprintf("%s could not be read: %v", path, err)
	}
}

// occupant is one name found in one agent's skills directory, reduced to what
// the collision check needs: two agents holding the same name are only in
// conflict when they resolve to different content.
type occupant struct {
	target string
	dest   string
}

// checkDirectories looks inward from the agents: what is in their skills
// directories, and whether the receipts account for it.
func checkDirectories(rep *Report, ts []target.Target, db *state.DB, st *store.Store) {
	byName := map[string][]occupant{}
	var found []string

	for _, t := range ts {
		names, err := children(t.Dir)
		if err != nil {
			// One unreadable agent must not decide the verdict for the rest:
			// the other agents are still scanned, and the report says it is
			// incomplete.
			rep.Unscanned = append(rep.Unscanned, Unscanned{Target: t.Name, Path: t.Dir, Error: err.Error()})
			continue
		}
		for _, name := range names {
			dest, ok := classify(rep, t, name, db, st)
			if !ok {
				continue
			}
			if _, seen := byName[name]; !seen {
				found = append(found, name)
			}
			byName[name] = append(byName[name], occupant{target: t.Name, dest: dest})
		}
	}

	sort.Strings(found)
	for _, name := range found {
		group := byName[name]
		if !diverges(group) {
			continue
		}
		rep.add(Finding{
			Kind: KindNameCollision, Name: name,
			Detail: fmt.Sprintf("resolves differently in each agent: %s", destinations(group)),
		})
	}
}

// classify decides about one entry in one agent's skills directory, and returns
// what it resolves to so the collision check can compare it against the other
// agents. The second result is false when there is nothing to compare — an
// entry that could not be read at all.
//
// The order of the checks is the order in which an answer becomes certain: what
// the thing is on disk, then where it points, then whether a receipt claims it.
func classify(rep *Report, t target.Target, name string, db *state.DB, st *store.Store) (string, bool) {
	path := filepath.Join(t.Dir, name)

	fi, err := os.Lstat(path)
	if err != nil {
		// It was in the directory listing a moment ago. Something is removing
		// entries underneath the scan, which is not damage to report.
		return "", false
	}

	if fi.Mode()&os.ModeSymlink == 0 {
		// A real directory nobody claims is an unmanaged skill, which is what
		// adopt is for. One a receipt claims is a different matter: Unlink
		// refuses to delete anything that is not a symlink, so removing the
		// skill would leave the files behind and the receipt would go with
		// them.
		if claims(db.Receipts[name], path) {
			rep.add(Finding{
				Kind: KindNotASymlink, Name: name, Target: t.Name, Path: path,
				Detail: "a receipt records this as a link, but it is not one",
			})
		}
		return path, true
	}

	dest, err := resolve(path)
	if err != nil {
		rep.add(Finding{
			Kind: KindDanglingLink, Name: name, Target: t.Name, Path: path,
			Detail: err.Error(),
		})
		return "", false
	}

	if _, err := os.Stat(dest); err != nil {
		rep.add(Finding{
			Kind: KindDanglingLink, Name: name, Target: t.Name, Path: path,
			Detail: fmt.Sprintf("points at %s, which is gone", dest),
		})
		return dest, true
	}

	// Whether a receipt claims this name is asked before where it points, for
	// the reason adopt records: everything skillsctl installs points into the
	// store, so asking about the destination first would report every managed
	// skill as an orphan.
	r := db.Receipts[name]
	switch {
	case claims(r, path):
		if r.RevPath != "" && dest != filepath.Clean(r.RevPath) {
			rep.add(Finding{
				Kind: KindWrongTarget, Name: name, Target: t.Name, Path: path,
				Detail: fmt.Sprintf("points at %s, but the receipt records %s", dest, r.RevPath),
			})
		}
	case r == nil && st.Contains(dest):
		// The slug a revision sits under does not reverse into the source it
		// came from, so there is no receipt to write for this and nothing that
		// will ever update it.
		rep.add(Finding{
			Kind: KindOrphanLink, Name: name, Target: t.Name, Path: path,
			Detail: fmt.Sprintf("points into the store at %s, but no receipt claims it", dest),
		})
	}
	return dest, true
}

// claims reports whether a receipt records this exact link. A receipt for the
// name that records a different path is a hand-made second link, which adopt
// already reports and doctor only cares about if it resolves elsewhere.
func claims(r *state.Receipt, path string) bool {
	if r == nil {
		return false
	}
	for _, l := range r.Links {
		if l.Path == path {
			return true
		}
	}
	return false
}

// checkStore reports revisions no receipt references. Collect is a pure scan,
// so gc and doctor see the same answer; only the mirrors it also finds are left
// out, because a bare mirror with no revision left is cache rather than damage.
func checkStore(rep *Report, st *store.Store, live store.Live) error {
	found, err := st.Collect(live)
	if err != nil {
		return err
	}
	for _, rev := range found.Revisions {
		rep.add(Finding{
			Kind: KindOrphanRevision, Path: rev.Rel, Bytes: rev.Bytes,
			Detail: "no receipt references this revision",
		})
	}
	return nil
}

// children lists the entries of an agent's skills directory. A missing
// directory is an agent that has never installed anything rather than an error.
func children(dir string) ([]string, error) {
	des, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}

	names := make([]string, 0, len(des))
	for _, de := range des {
		// A dot entry is the agent's own bookkeeping — codex keeps a .system
		// directory here — and nothing loads it as a skill. Reporting it would
		// put a permanent finding, and so a permanent exit code, on a machine
		// with nothing wrong with it.
		if strings.HasPrefix(de.Name(), ".") {
			continue
		}
		names = append(names, de.Name())
	}
	sort.Strings(names)
	return names, nil
}

// resolve follows one symlink to an absolute path. A relative target is
// resolved against the directory holding the link, which is how the filesystem
// reads it.
//
// adopt has the same eight lines. They are copied rather than shared because
// the alternative is one scan package importing the other for a helper, and the
// two are deliberately independent: adopt asks what it can take over, doctor
// asks what has gone wrong, and neither should move when the other does.
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

func (r *Report) add(f Finding) {
	f.Remedy = remedy(f.Kind, []string{f.Name})
	r.Findings = append(r.Findings, f)
}

// remedy is the command that repairs a class of finding for the named skills.
// It is the whole of what report-only means: doctor knows how to fix everything
// it finds and says so, rather than deciding on the user's behalf.
func remedy(k Kind, names []string) string {
	list := strings.Join(names, " ")
	switch k {
	case KindMissingLink, KindWrongTarget, KindMissingRevision:
		return fmt.Sprintf("skillsctl update %s", list)
	case KindDanglingLink:
		return fmt.Sprintf("skillsctl update %s, or skillsctl remove %s", list, list)
	case KindMissingSource:
		return fmt.Sprintf("put the directory back, or skillsctl remove %s", list)
	case KindNotASymlink:
		return fmt.Sprintf("move the directory out of the way, then skillsctl update %s", list)
	case KindNameCollision:
		return fmt.Sprintf("skillsctl remove %s, then install each copy under its own name with --as", list)
	case KindContentDrift:
		return fmt.Sprintf("skillsctl update --force %s to discard the edit, or skillsctl link the directory you are editing", list)
	case KindOrphanLink:
		return "delete the link, then skillsctl gc"
	case KindOrphanRevision:
		return "skillsctl gc"
	}
	return ""
}

// names are the distinct skills a group of findings implicates, in the order
// they were found, so the remedy for a group is one command rather than one per
// finding.
func names(fs []Finding) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		if f.Name == "" || seen[f.Name] {
			continue
		}
		seen[f.Name] = true
		out = append(out, f.Name)
	}
	return out
}

// diverges reports whether the occupants of one name resolve to different
// things. Two agents linked at one revision is the ordinary managed case.
func diverges(group []occupant) bool {
	for _, o := range group[1:] {
		if o.dest != group[0].dest {
			return true
		}
	}
	return false
}

func destinations(group []occupant) string {
	out := make([]string, 0, len(group))
	for _, o := range group {
		out = append(out, fmt.Sprintf("%s -> %s", o.target, o.dest))
	}
	return strings.Join(out, ", ")
}

// short abbreviates a sha for a message, leaving anything that is not one — a
// plugin version — alone.
func short(resolved string) string {
	const n = 7
	if len(resolved) <= n {
		return resolved
	}
	for _, c := range resolved {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return resolved
		}
	}
	return resolved[:n]
}
