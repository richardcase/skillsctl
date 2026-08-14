package gitx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestMirrorRefetchesFromTheRequestedURL(t *testing.T) {
	first, _ := testrepo.New(t, map[string]string{"SKILL.md": "from-first"})
	second, secondSha := testrepo.New(t, map[string]string{"SKILL.md": "from-second"})

	ctx := context.Background()
	root := t.TempDir()
	mirror := filepath.Join(root, "shared.git")
	g := New()

	if err := g.Mirror(ctx, first, mirror); err != nil {
		t.Fatal(err)
	}
	// Same mirror path, different repository: Mirror must fetch from the URL it
	// was given, not from whatever origin the first call left configured.
	if err := g.Mirror(ctx, second, mirror); err != nil {
		t.Fatalf("second Mirror: %v", err)
	}

	dest := filepath.Join(root, "rev")
	if err := g.Extract(ctx, mirror, secondSha, dest); err != nil {
		t.Fatalf("Extract of the second repo's sha: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dest, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "from-second" {
		t.Errorf("extracted %q, want from-second", body)
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

// TestExtractFailsFastWhenDestinationIsUnwritable proves Extract returns
// promptly with the untar error instead of hanging when untar fails partway
// through the archive. The regression this guards: if Extract fails to drain
// the rest of the tar stream after untar errors, git blocks writing into a
// full pipe once its output exceeds the OS pipe buffer, and cmd.Wait() then
// blocks forever waiting for a process that is itself blocked on write().
//
// "0-trigger.md" sorts first in the archive (git emits tree entries in byte
// order) and is small; "zzz-big.bin" sorts after it and is large. dest is
// pre-created as a directory Extract's MkdirAll will find already present
// (so MkdirAll succeeds without needing write permission) but with no write
// bit, so untar's first os.OpenFile — for "0-trigger.md" — fails only after
// the pipe has been opened and reading has begun. If Extract does not then
// drain the remaining, unread "zzz-big.bin" bytes before Wait, git blocks
// writing them into the pipe and the test times out.
func TestExtractFailsFastWhenDestinationIsUnwritable(t *testing.T) {
	url, sha := testrepo.New(t, map[string]string{
		"0-trigger.md": "x",
		"zzz-big.bin":  strings.Repeat("a", 4<<20), // 4 MiB, far larger than any OS pipe buffer.
	})

	ctx := context.Background()
	root := t.TempDir()
	mirror := filepath.Join(root, "m.git")
	g := New()
	if err := g.Mirror(ctx, url, mirror); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(root, "dest")
	if err := os.Mkdir(dest, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dest, 0o755) }) // let t.TempDir() clean up

	done := make(chan error, 1)
	go func() { done <- g.Extract(ctx, mirror, sha, dest) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Extract succeeded despite an unwritable destination")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Extract hung: the tar stream was not drained before Wait")
	}
}

func TestDescribeReadsACheckout(t *testing.T) {
	url, sha := testrepo.New(t, map[string]string{"skills/demo/SKILL.md": "---\nname: demo\n---\n"})
	clone := testrepo.Clone(t, url)

	got, err := New().Describe(context.Background(), filepath.Join(clone, "skills", "demo"))
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if got.Prefix != "skills/demo" {
		t.Errorf("Prefix = %q, want skills/demo", got.Prefix)
	}
	if got.RepoURL != url {
		t.Errorf("RepoURL = %q, want %q", got.RepoURL, url)
	}
	if got.SHA != sha {
		t.Errorf("SHA = %q, want %q", got.SHA, sha)
	}
	if got.Ref != "main" {
		t.Errorf("Ref = %q, want main", got.Ref)
	}
	if got.Dirty {
		t.Error("a fresh clone reported as dirty")
	}
}

func TestDescribeScopesDirtinessToTheDirectory(t *testing.T) {
	url, _ := testrepo.New(t, map[string]string{
		"skills/demo/SKILL.md": "---\nname: demo\n---\n",
		"elsewhere/notes.md":   "notes\n",
	})
	clone := testrepo.Clone(t, url)
	demo := filepath.Join(clone, "skills", "demo")

	// Churn outside the skill says nothing about the skill.
	if err := os.WriteFile(filepath.Join(clone, "elsewhere", "notes.md"), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := New().Describe(context.Background(), demo)
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if got.Dirty {
		t.Error("an edit outside the directory made it dirty")
	}

	if err := os.WriteFile(filepath.Join(demo, "SKILL.md"), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = New().Describe(context.Background(), demo)
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if !got.Dirty {
		t.Error("an edit inside the directory did not make it dirty")
	}
}

func TestDescribeCountsAnUntrackedFileAsDirty(t *testing.T) {
	url, _ := testrepo.New(t, map[string]string{"skills/demo/SKILL.md": "---\nname: demo\n---\n"})
	clone := testrepo.Clone(t, url)
	demo := filepath.Join(clone, "skills", "demo")

	if err := os.WriteFile(filepath.Join(demo, "extra.md"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := New().Describe(context.Background(), demo)
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if !got.Dirty {
		t.Error("a file git has never seen did not count as dirty")
	}
}

func TestDescribeReportsNoRemoteRatherThanFailing(t *testing.T) {
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": "---\nname: demo\n---\n"})

	got, err := New().Describe(context.Background(), testrepo.Dir(url))
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if got.RepoURL != "" {
		t.Errorf("RepoURL = %q, want empty for a repository with no origin", got.RepoURL)
	}
	if got.Prefix != "" {
		t.Errorf("Prefix = %q, want empty at the repository root", got.Prefix)
	}
	if got.SHA == "" {
		t.Error("SHA is empty for a repository that has a commit")
	}
}

func TestDescribeRejectsSomethingThatIsNotARepository(t *testing.T) {
	_, err := New().Describe(context.Background(), t.TempDir())
	if !errors.Is(err, ErrNotRepo) {
		t.Errorf("Describe outside a repository = %v, want ErrNotRepo", err)
	}
}
