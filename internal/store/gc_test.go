package store

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const (
	slugA = "github.com/o/r"
	slugB = "github.com/o/other"
	sha1s = "1111111111111111111111111111111111111111"
	sha2s = "2222222222222222222222222222222222222222"
)

// layout builds a store containing files, keyed by "/" separated paths
// relative to the store root.
func layout(t *testing.T, files map[string]string) *Store {
	t.Helper()
	s := New(t.TempDir())
	for name, body := range files {
		p := filepath.Join(s.Root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// rels is the sorted display paths of a report, for comparison in tests.
func rels(rep Report) []string {
	out := make([]string, 0, len(rep.Revisions)+len(rep.Mirrors))
	for _, item := range rep.All() {
		out = append(out, item.Rel)
	}
	sort.Strings(out)
	return out
}

func want(t *testing.T, rep Report, paths ...string) {
	t.Helper()
	sort.Strings(paths)
	got := rels(rep)
	if strings.Join(got, ",") != strings.Join(paths, ",") {
		t.Errorf("collected %v, want %v", got, paths)
	}
}

func TestCollectOnAnEmptyStoreFindsNothing(t *testing.T) {
	s := New(t.TempDir())
	rep, err := s.Collect(Live{})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if !rep.IsEmpty() {
		t.Errorf("empty store yielded %v", rels(rep))
	}
	if rep.Bytes() != 0 {
		t.Errorf("Bytes() = %d, want 0", rep.Bytes())
	}
}

func TestCollectKeepsARevisionTwoReceiptsShare(t *testing.T) {
	s := layout(t, map[string]string{
		"rev/github.com/o/r/" + sha1s + "/a/SKILL.md": "a",
		"rev/github.com/o/r/" + sha1s + "/b/SKILL.md": "b",
		"cache/github.com/o/r.git/HEAD":               "ref: refs/heads/main\n",
	})

	both := Live{
		RevPaths: []string{
			filepath.Join(s.RevPath(slugA, sha1s), "a"),
			filepath.Join(s.RevPath(slugA, sha1s), "b"),
		},
		Slugs: []string{slugA, slugA},
	}
	rep, err := s.Collect(both)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	want(t, rep)

	// One receipt gone: the revision is still shared, so nothing is dead.
	one := Live{RevPaths: both.RevPaths[:1], Slugs: []string{slugA}}
	rep, err = s.Collect(one)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	want(t, rep)

	// Both gone: the revision and its mirror are collectable, each named
	// precisely rather than rolled up into the tree above them.
	rep, err = s.Collect(Live{})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	want(t, rep, "rev/github.com/o/r/"+sha1s, "cache/github.com/o/r.git")
}

func TestCollectTakesADeadSiblingRevisionButKeepsTheMirror(t *testing.T) {
	s := layout(t, map[string]string{
		"rev/github.com/o/r/" + sha1s + "/SKILL.md": "live",
		"rev/github.com/o/r/" + sha2s + "/SKILL.md": "stale",
		"cache/github.com/o/r.git/HEAD":             "x",
	})

	rep, err := s.Collect(Live{
		RevPaths: []string{s.RevPath(slugA, sha1s)},
		Slugs:    []string{slugA},
	})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	want(t, rep, "rev/github.com/o/r/"+sha2s)
	if len(rep.Mirrors) != 0 {
		t.Errorf("mirror collected while a revision of that repo is live: %v", rep.Mirrors)
	}
}

func TestCollectTakesAnEntireRepositoryNoReceiptReferences(t *testing.T) {
	s := layout(t, map[string]string{
		"rev/github.com/o/r/" + sha1s + "/SKILL.md":     "live",
		"rev/github.com/o/other/" + sha2s + "/SKILL.md": "dead",
		"cache/github.com/o/r.git/HEAD":                 "x",
		"cache/github.com/o/other.git/HEAD":             "x",
	})

	rep, err := s.Collect(Live{
		RevPaths: []string{s.RevPath(slugA, sha1s)},
		Slugs:    []string{slugA},
	})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	want(t, rep, "rev/github.com/o/other/"+sha2s, "cache/github.com/o/other.git")
}

func TestDeletePrunesTheDirectoriesItEmpties(t *testing.T) {
	s := layout(t, map[string]string{
		"rev/github.com/o/other/" + sha2s + "/SKILL.md": "dead",
		"cache/github.com/o/other.git/HEAD":             "x",
	})

	rep, err := s.Collect(Live{})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if _, err := s.Delete(rep); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// rev/ and cache/ survive, so the store keeps its shape; everything the
	// deletion emptied beneath them is gone.
	for _, dir := range []string{"rev", "cache"} {
		entries, rerr := os.ReadDir(filepath.Join(s.Root, dir))
		if rerr != nil {
			t.Fatalf("%s/ should still exist: %v", dir, rerr)
		}
		if len(entries) != 0 {
			t.Errorf("%s/ still holds %v", dir, entries)
		}
	}
}

func TestCollectTakesAbandonedExtractionDirectories(t *testing.T) {
	s := layout(t, map[string]string{
		"rev/github.com/o/r/" + sha1s + "/SKILL.md": "live",
		"rev/github.com/o/r/.tmp-abc123/SKILL.md":   "half-written",
	})

	rep, err := s.Collect(Live{
		RevPaths: []string{s.RevPath(slugA, sha1s)},
		Slugs:    []string{slugA},
	})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	want(t, rep, "rev/github.com/o/r/.tmp-abc123")
}

func TestCollectNeverLooksInsideALiveRevision(t *testing.T) {
	// A receipt names a subpath, so only part of the revision is referenced.
	// The unreferenced siblings are still part of an extracted revision and
	// must not be collected piecemeal.
	s := layout(t, map[string]string{
		"rev/github.com/o/r/" + sha1s + "/a/SKILL.md":     "linked",
		"rev/github.com/o/r/" + sha1s + "/b/SKILL.md":     "not linked",
		"rev/github.com/o/r/" + sha1s + "/README.md":      "top level",
		"rev/github.com/o/r/" + sha1s + "/.git-ish/thing": "content",
	})

	rep, err := s.Collect(Live{
		RevPaths: []string{filepath.Join(s.RevPath(slugA, sha1s), "a")},
		Slugs:    []string{slugA},
	})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	want(t, rep)
}

func TestCollectReportsBytes(t *testing.T) {
	body := strings.Repeat("x", 4096)
	s := layout(t, map[string]string{
		"rev/github.com/o/r/" + sha1s + "/SKILL.md": body,
	})

	rep, err := s.Collect(Live{})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if rep.Bytes() != int64(len(body)) {
		t.Errorf("Bytes() = %d, want %d", rep.Bytes(), len(body))
	}
}

func TestCollectSkipsMirrorsWhenAReceiptHasNoSlug(t *testing.T) {
	s := layout(t, map[string]string{
		"rev/github.com/o/r/" + sha1s + "/SKILL.md":     "live",
		"rev/github.com/o/other/" + sha2s + "/SKILL.md": "dead",
		"cache/github.com/o/r.git/HEAD":                 "x",
		"cache/github.com/o/other.git/HEAD":             "x",
	})

	rep, err := s.Collect(Live{
		RevPaths: []string{s.RevPath(slugA, sha1s)},
		Slugs:    []string{""},
	})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if !rep.MirrorsSkipped {
		t.Error("MirrorsSkipped should be set when a receipt carries no slug")
	}
	if len(rep.Mirrors) != 0 {
		t.Errorf("mirrors considered despite an unidentifiable receipt: %v", rep.Mirrors)
	}
	// The revision is still protected, by containment rather than by slug.
	want(t, rep, "rev/github.com/o/other/"+sha2s)
}

func TestCollectDoesNotFollowSymlinksOutOfTheStore(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "precious")
	if err := os.WriteFile(outside, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := layout(t, map[string]string{
		"rev/github.com/o/r/" + sha1s + "/SKILL.md": "dead",
	})
	link := filepath.Join(s.RevPath(slugA, sha1s), "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	rep, err := s.Collect(Live{})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if _, err := s.Delete(rep); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("collection followed a symlink out of the store: %v", err)
	}
}

func TestDeleteFreesExactlyWhatItReported(t *testing.T) {
	s := layout(t, map[string]string{
		"rev/github.com/o/r/" + sha1s + "/SKILL.md":     "live",
		"rev/github.com/o/other/" + sha2s + "/SKILL.md": "dead",
		"cache/github.com/o/r.git/HEAD":                 "x",
		"cache/github.com/o/other.git/HEAD":             "x",
	})

	found, err := s.Collect(Live{
		RevPaths: []string{s.RevPath(slugA, sha1s)},
		Slugs:    []string{slugA},
	})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	freed, err := s.Delete(found)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if freed.Bytes() != found.Bytes() {
		t.Errorf("freed %d bytes, reported %d", freed.Bytes(), found.Bytes())
	}

	for _, gone := range []string{s.RevPath(slugB, sha2s), s.MirrorPath(slugB)} {
		if _, serr := os.Stat(gone); !os.IsNotExist(serr) {
			t.Errorf("%s survived collection", gone)
		}
	}
	for _, kept := range []string{s.RevPath(slugA, sha1s), s.MirrorPath(slugA)} {
		if _, serr := os.Stat(kept); serr != nil {
			t.Errorf("%s was collected but is live: %v", kept, serr)
		}
	}

	// A second pass has nothing left to find.
	again, err := s.Collect(Live{
		RevPaths: []string{s.RevPath(slugA, sha1s)},
		Slugs:    []string{slugA},
	})
	if err != nil {
		t.Fatalf("second Collect: %v", err)
	}
	if !again.IsEmpty() {
		t.Errorf("collection is not idempotent, second pass found %v", rels(again))
	}
}
