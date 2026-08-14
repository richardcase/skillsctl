// Package store manages the content-addressed cache of repository mirrors and
// immutable revision directories.
package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/richardcase/skillsctl/internal/gitx"
)

// Store is a skillsctl data root.
type Store struct{ Root string }

// New returns a Store rooted at root.
func New(root string) *Store { return &Store{Root: root} }

// Home locates the data root, honouring SKILLSCTL_HOME and XDG_DATA_HOME
// before falling back to ~/.local/share. Go's os.UserConfigDir is deliberately
// not used: on macOS it resolves to ~/Library/Application Support.
func Home() (string, error) {
	if p := os.Getenv("SKILLSCTL_HOME"); p != "" {
		return p, nil
	}
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "skillsctl"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, ".local", "share", "skillsctl"), nil
}

// MirrorPath is where the bare mirror for slug lives.
func (s *Store) MirrorPath(slug string) string {
	return filepath.Join(s.Root, "cache", filepath.FromSlash(slug)+".git")
}

// RevPath is where the extracted tree for slug at sha lives. A revision holds
// the whole repository; subpath selection happens at link time.
func (s *Store) RevPath(slug, sha string) string {
	return filepath.Join(s.Root, "rev", filepath.FromSlash(slug), sha)
}

// StatePath is the receipts database.
func (s *Store) StatePath() string { return filepath.Join(s.Root, "state.json") }

// within reports whether p stays inside the store root. Store paths are built
// from user-supplied source strings, so this is checked rather than assumed.
func (s *Store) within(p string) error {
	rel, err := filepath.Rel(s.Root, p)
	if err != nil {
		return fmt.Errorf("resolve %s against store root: %w", p, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("refusing to use path outside the store: %s", p)
	}
	return nil
}

// Join resolves a repository-relative subpath against a revision directory.
// A subpath can come from a user-supplied source string or from a receipt, so
// one that escapes the revision is refused rather than cleaned: silently
// stripping a .. segment would select a different skill than the one named.
func Join(root, subpath string) (string, error) {
	if subpath == "" {
		return root, nil
	}
	clean := filepath.Clean(filepath.FromSlash(subpath))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("subpath %q resolves outside the revision directory", subpath)
	}
	return filepath.Join(root, clean), nil
}

// Ensure guarantees the revision is extracted, returning its path. It is a
// no-op when the revision is already present, so it is safe to call on every
// install including a --dry-run.
//
// It takes the slug rather than a source.Source because a receipt records the
// slug it was installed under: re-deriving one by parsing the recorded URL
// would make the store layout depend on a round trip nothing asserts.
func (s *Store) Ensure(ctx context.Context, g gitx.Git, slug, repoURL, sha string) (string, error) {
	rev := s.RevPath(slug, sha)
	mirror := s.MirrorPath(slug)

	if err := s.within(rev); err != nil {
		return "", err
	}
	if err := s.within(mirror); err != nil {
		return "", err
	}

	if fi, err := os.Stat(rev); err == nil && fi.IsDir() {
		return rev, nil
	}

	if err := g.Mirror(ctx, repoURL, mirror); err != nil {
		return "", fmt.Errorf("mirror %s: %w", repoURL, err)
	}

	if err := os.MkdirAll(filepath.Dir(rev), 0o755); err != nil {
		return "", fmt.Errorf("create revision directory: %w", err)
	}

	// Extract into a sibling temp directory, then rename, so a revision
	// directory is never observed half-written.
	tmp, err := os.MkdirTemp(filepath.Dir(rev), ".tmp-")
	if err != nil {
		return "", fmt.Errorf("create temp revision directory: %w", err)
	}
	defer func() {
		if rerr := os.RemoveAll(tmp); rerr != nil {
			fmt.Fprintf(os.Stderr, "skillsctl: could not remove temporary extraction directory %s: %v\n", tmp, rerr)
		}
	}()

	if err := g.Extract(ctx, mirror, sha, tmp); err != nil {
		return "", fmt.Errorf("extract %s at %s: %w", repoURL, sha, err)
	}
	if err := os.Rename(tmp, rev); err != nil {
		// Another process may have won the race; accept its result.
		if fi, serr := os.Stat(rev); serr == nil && fi.IsDir() {
			return rev, nil
		}
		return "", fmt.Errorf("publish revision: %w", err)
	}
	return rev, nil
}
