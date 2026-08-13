// Package store manages the content-addressed cache of repository mirrors and
// immutable revision directories.
package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/richardcase/skillsctl/internal/source"
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

// Ensure guarantees the revision is extracted, returning its path. It is a
// no-op when the revision is already present, so it is safe to call on every
// install including a --dry-run.
func (s *Store) Ensure(ctx context.Context, g gitx.Git, src source.Source, sha string) (string, error) {
	slug := src.Slug()
	rev := s.RevPath(slug, sha)

	if fi, err := os.Stat(rev); err == nil && fi.IsDir() {
		return rev, nil
	}

	mirror := s.MirrorPath(slug)
	if err := g.Mirror(ctx, src.RepoURL, mirror); err != nil {
		return "", fmt.Errorf("mirror %s: %w", src.RepoURL, err)
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
	defer func() { _ = os.RemoveAll(tmp) }()

	if err := g.Extract(ctx, mirror, sha, tmp); err != nil {
		return "", err
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
