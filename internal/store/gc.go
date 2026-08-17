package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Live is the root set collection keeps. Everything in the store that no live
// root references is garbage.
//
// It is deliberately not a receipt slice: the store owns the on-disk layout,
// state owns the receipt schema, and neither needs to know the other.
type Live struct {
	// RevPaths are receipt RevPath values — absolute paths at or below a
	// revision root, since a receipt names the subpath it linked.
	RevPaths []string
	// Slugs are receipt Slug values. An empty entry means one receipt's
	// repository identity is unknown, which disables mirror collection.
	Slugs []string
}

// Reclaimable is one directory collection would delete.
type Reclaimable struct {
	// Path is absolute, for deletion.
	Path string `json:"path"`
	// Rel is slash-separated and relative to the store root, for display.
	Rel string `json:"rel"`
	// Bytes is the apparent size of the tree.
	Bytes int64 `json:"bytes"`
}

// Report is what one collection pass found, or freed.
type Report struct {
	Revisions []Reclaimable `json:"revisions"`
	Mirrors   []Reclaimable `json:"mirrors"`
	// MirrorsSkipped records that a receipt carried no slug, so no mirror
	// could be proven unused and none were considered.
	MirrorsSkipped bool `json:"mirrorsSkipped,omitempty"`
}

// Bytes is the total apparent size of everything in the report.
func (r Report) Bytes() int64 {
	var total int64
	for _, item := range r.All() {
		total += item.Bytes
	}
	return total
}

// IsEmpty reports whether there is nothing to collect.
func (r Report) IsEmpty() bool { return len(r.Revisions) == 0 && len(r.Mirrors) == 0 }

// All returns every entry, revisions before mirrors.
func (r Report) All() []Reclaimable {
	out := make([]Reclaimable, 0, len(r.Revisions)+len(r.Mirrors))
	out = append(out, r.Revisions...)
	return append(out, r.Mirrors...)
}

// shaRe and digestRe match a revision directory's name: a git sha, or the
// algorithm-prefixed content digest an OCI artifact is named by. They decide
// only where the scan stops descending, never what is dead, so a slug
// component that happens to look like one costs precision at worst: the tree
// beneath it is reported and deleted as one item instead of one per revision.
//
// Failing to match, though, is not harmless: prune would descend into a live
// revision and collect the files under it one by one, which is why every
// revision-naming scheme Ensure can produce must be recognised here.
var (
	shaRe    = regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`)
	digestRe = regexp.MustCompile(`^[a-z0-9]+(?:[.+_-][a-z0-9]+)*:[0-9a-fA-F]{7,}$`)
)

// Collect scans the store and reports everything no live root references. It
// changes nothing, so --dry-run and the real run see the same answer.
//
// Callers must hold the state lock: an install extracts its revision before
// committing the receipt that makes it live, and the lock is what stops a
// collection from landing in between.
func (s *Store) Collect(live Live) (Report, error) {
	var rep Report

	// Revision liveness is by containment. A receipt's RevPath sits at or
	// below its revision root, so keeping every ancestor of it keeps the
	// revision without having to know where the slug ends and the sha
	// begins — slug depth varies by channel and by URL.
	keep := make([]string, 0, len(live.RevPaths))
	for _, p := range live.RevPaths {
		if p != "" {
			keep = append(keep, filepath.Clean(p))
		}
	}

	revisions, err := s.prune(filepath.Join(s.Root, "rev"), keep, isExtraction)
	if err != nil {
		return Report{}, err
	}
	rep.Revisions = revisions

	// A mirror is named by slug alone, so one unidentifiable receipt makes
	// every mirror unprovable. Revisions are unaffected: they are kept by
	// containment, not by slug.
	mirrors := make([]string, 0, len(live.Slugs))
	for _, slug := range live.Slugs {
		if slug == "" {
			rep.MirrorsSkipped = true
			return rep, nil
		}
		mirrors = append(mirrors, filepath.Clean(s.MirrorPath(slug)))
	}

	rep.Mirrors, err = s.prune(filepath.Join(s.Root, "cache"), mirrors, isMirror)
	if err != nil {
		return Report{}, err
	}
	return rep, nil
}

// Delete removes everything in rep and returns what it actually freed, so a
// reported byte count stays truthful when one removal fails.
func (s *Store) Delete(rep Report) (Report, error) {
	freed := Report{MirrorsSkipped: rep.MirrorsSkipped}
	var errs []error

	for _, group := range []struct {
		in  []Reclaimable
		out *[]Reclaimable
	}{
		{rep.Revisions, &freed.Revisions},
		{rep.Mirrors, &freed.Mirrors},
	} {
		for _, item := range group.in {
			if err := s.within(item.Path); err != nil {
				errs = append(errs, err)
				continue
			}
			if err := os.RemoveAll(item.Path); err != nil {
				errs = append(errs, fmt.Errorf("remove %s: %w", item.Rel, err))
				continue
			}
			*group.out = append(*group.out, item)
			s.pruneEmptyParents(item.Path)
		}
	}
	return freed, errors.Join(errs...)
}

// pruneEmptyParents removes the slug directories a deletion just emptied,
// stopping below rev/ and cache/ so the store keeps its shape. Nothing here is
// reported: an empty directory frees nothing, and a failure to remove one is
// not a reason to fail the collection.
func (s *Store) pruneEmptyParents(deleted string) {
	for dir := filepath.Dir(deleted); filepath.Dir(dir) != s.Root; dir = filepath.Dir(dir) {
		if err := s.within(dir); err != nil {
			return
		}
		// Remove refuses on a non-empty directory, which is exactly the
		// condition to stop on.
		if err := os.Remove(dir); err != nil {
			return
		}
	}
}

// prune walks dir and reports every dead directory at the finest granularity
// it can name one, so the report says which revision is going rather than
// which corner of the tree.
//
// bound names the directories the walk must never enter: an extracted
// revision and a bare mirror are collected whole or not at all. Half-deleting
// a revision would leave an extraction Ensure considers complete — it only
// stats the root — with no path back to a good one.
func (s *Store) prune(dir string, live []string, bound func(name string) bool) ([]Reclaimable, error) {
	entries, err := os.ReadDir(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}

	var out []Reclaimable
	for _, e := range entries {
		// Anything that is not a directory — a stray file, a symlink — was
		// not put here by skillsctl. Leave it alone.
		if !e.IsDir() {
			continue
		}
		child := filepath.Join(dir, e.Name())

		if holdsLive(child, live) {
			if bound(e.Name()) {
				continue // live content is in here; never look inside
			}
			sub, perr := s.prune(child, live, bound)
			if perr != nil {
				return nil, perr
			}
			out = append(out, sub...)
			continue
		}

		// Dead. Keep descending while there is a more precise name for what
		// is being deleted; the emptied ancestors are pruned on deletion.
		if !bound(e.Name()) {
			nested, perr := hasSubdirectories(child)
			if perr != nil {
				return nil, perr
			}
			if nested {
				sub, serr := s.prune(child, live, bound)
				if serr != nil {
					return nil, serr
				}
				out = append(out, sub...)
				continue
			}
		}

		item, rerr := s.reclaimable(child)
		if rerr != nil {
			return nil, rerr
		}
		out = append(out, item)
	}
	return out, nil
}

func hasSubdirectories(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			return true, nil
		}
	}
	return false, nil
}

// holdsLive reports whether any live path is dir itself or sits beneath it.
func holdsLive(dir string, live []string) bool {
	prefix := dir + string(filepath.Separator)
	for _, l := range live {
		if l == dir || strings.HasPrefix(l, prefix) {
			return true
		}
	}
	return false
}

// isExtraction matches what Ensure and EnsureOCI create under rev/: a
// published revision named by its git sha or by its OCI digest, and the
// temporary directory either extracts into first.
func isExtraction(name string) bool {
	return shaRe.MatchString(name) || digestRe.MatchString(name) || strings.HasPrefix(name, ".tmp-")
}

func isMirror(name string) bool { return strings.HasSuffix(name, ".git") }

// reclaimable measures a doomed tree and checks it is one the store owns.
func (s *Store) reclaimable(path string) (Reclaimable, error) {
	if err := s.within(path); err != nil {
		return Reclaimable{}, err
	}
	size, err := treeSize(path)
	if err != nil {
		return Reclaimable{}, err
	}
	rel, err := filepath.Rel(s.Root, path)
	if err != nil {
		rel = path
	}
	return Reclaimable{Path: path, Rel: filepath.ToSlash(rel), Bytes: size}, nil
}

// treeSize sums the apparent size of every file under root. Symlinks are
// repository content: they count as their own size and are never followed.
func treeSize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		total += info.Size()
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("measure %s: %w", root, err)
	}
	return total, nil
}
