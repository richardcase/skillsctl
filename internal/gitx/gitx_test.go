package gitx

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/richardcase/skillsctl/internal/testrepo"
)

func TestResolveHEAD(t *testing.T) {
	url, sha := testrepo.New(t, map[string]string{"SKILL.md": "---\nname: demo\n---\n"})

	got, err := New().Resolve(context.Background(), url, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != sha {
		t.Errorf("Resolve() = %q, want %q", got, sha)
	}
}

func TestResolveBranch(t *testing.T) {
	url, sha := testrepo.New(t, map[string]string{"SKILL.md": "x"})

	got, err := New().Resolve(context.Background(), url, "main")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != sha {
		t.Errorf("Resolve(main) = %q, want %q", got, sha)
	}
}

func TestResolvePassesThroughFullSha(t *testing.T) {
	sha := "0123456789abcdef0123456789abcdef01234567"

	got, err := New().Resolve(context.Background(), "file:///nonexistent", sha)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != sha {
		t.Errorf("Resolve(sha) = %q, want the sha unchanged", got)
	}
}

func TestResolveUnknownRefErrors(t *testing.T) {
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": "x"})

	if _, err := New().Resolve(context.Background(), url, "no-such-branch"); err == nil {
		t.Fatal("Resolve accepted an unknown ref; want an error")
	}
}

func TestMirrorThenExtract(t *testing.T) {
	url, sha := testrepo.New(t, map[string]string{
		"SKILL.md":               "---\nname: demo\n---\nbody\n",
		"skills/nested/SKILL.md": "---\nname: nested\n---\n",
	})

	ctx := context.Background()
	root := t.TempDir()
	mirror := filepath.Join(root, "demo.git")
	g := New()

	if err := g.Mirror(ctx, url, mirror); err != nil {
		t.Fatalf("Mirror: %v", err)
	}
	// A second Mirror must fetch into the existing mirror, not fail.
	if err := g.Mirror(ctx, url, mirror); err != nil {
		t.Fatalf("second Mirror: %v", err)
	}

	dest := filepath.Join(root, "rev")
	if err := g.Extract(ctx, mirror, sha, dest); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(dest, "SKILL.md"))
	if err != nil {
		t.Fatalf("root SKILL.md missing: %v", err)
	}
	if string(body) != "---\nname: demo\n---\nbody\n" {
		t.Errorf("extracted content = %q", body)
	}
	if _, err := os.Stat(filepath.Join(dest, "skills", "nested", "SKILL.md")); err != nil {
		t.Errorf("nested file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, ".git")); !os.IsNotExist(err) {
		t.Error("extracted revision must not contain a .git directory")
	}
}

func TestMirrorPicksUpNewCommits(t *testing.T) {
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": "v1"})
	dir := testrepo.Dir(url)

	ctx := context.Background()
	mirror := filepath.Join(t.TempDir(), "m.git")
	g := New()
	if err := g.Mirror(ctx, url, mirror); err != nil {
		t.Fatal(err)
	}

	second := testrepo.Commit(t, dir, map[string]string{"SKILL.md": "v2"})
	if err := g.Mirror(ctx, url, mirror); err != nil {
		t.Fatalf("re-Mirror: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "rev")
	if err := g.Extract(ctx, mirror, second, dest); err != nil {
		t.Fatalf("Extract of the new sha: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(dest, "SKILL.md"))
	if string(body) != "v2" {
		t.Errorf("extracted %q, want v2", body)
	}
}
