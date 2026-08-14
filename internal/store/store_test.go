package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/testrepo"
)

func TestHomePrefersSkillsctlHome(t *testing.T) {
	t.Setenv("SKILLSCTL_HOME", "/custom/root")
	got, err := Home()
	if err != nil {
		t.Fatalf("Home: %v", err)
	}
	if got != "/custom/root" {
		t.Errorf("Home() = %q, want /custom/root", got)
	}
}

func TestHomeUsesXDGDataHome(t *testing.T) {
	t.Setenv("SKILLSCTL_HOME", "")
	t.Setenv("XDG_DATA_HOME", "/xdg/data")
	got, err := Home()
	if err != nil {
		t.Fatalf("Home: %v", err)
	}
	want := filepath.Join("/xdg/data", "skillsctl")
	if got != want {
		t.Errorf("Home() = %q, want %q", got, want)
	}
}

func TestPathsAreDerivedFromSlug(t *testing.T) {
	s := New("/root")
	if got, want := s.MirrorPath("github.com/o/r"), filepath.Join("/root", "cache", "github.com", "o", "r.git"); got != want {
		t.Errorf("MirrorPath = %q, want %q", got, want)
	}
	if got, want := s.RevPath("github.com/o/r", "abc"), filepath.Join("/root", "rev", "github.com", "o", "r", "abc"); got != want {
		t.Errorf("RevPath = %q, want %q", got, want)
	}
	if got, want := s.StatePath(), filepath.Join("/root", "state.json"); got != want {
		t.Errorf("StatePath = %q, want %q", got, want)
	}
}

func TestEnsureExtractsAndIsIdempotent(t *testing.T) {
	url, sha := testrepo.New(t, map[string]string{"SKILL.md": "---\nname: demo\n---\n"})
	src, err := source.Parse(url)
	if err != nil {
		t.Fatalf("Parse(%q): %v", url, err)
	}

	s := New(t.TempDir())
	ctx := context.Background()

	rev, err := s.Ensure(ctx, gitx.New(), src.Slug(), src.RepoURL, sha)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if _, err := os.Stat(filepath.Join(rev, "SKILL.md")); err != nil {
		t.Fatalf("SKILL.md not extracted: %v", err)
	}
	if !strings.HasSuffix(rev, sha) {
		t.Errorf("revision path %q should end in the sha", rev)
	}

	// Second call must be a no-op that returns the same path.
	marker := filepath.Join(rev, "MARKER")
	if err := os.WriteFile(marker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	again, err := s.Ensure(ctx, gitx.New(), src.Slug(), src.RepoURL, sha)
	if err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	if again != rev {
		t.Errorf("second Ensure returned %q, want %q", again, rev)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Error("second Ensure re-extracted instead of reusing the cached revision")
	}
}

func TestJoinResolvesASubpathInsideTheRevision(t *testing.T) {
	root := filepath.Join("/root", "rev", "github.com", "o", "r", "abc")

	got, err := Join(root, "skills/alpha")
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if want := filepath.Join(root, "skills", "alpha"); got != want {
		t.Errorf("Join = %q, want %q", got, want)
	}

	// An empty subpath means the repository itself is the skill.
	got, err = Join(root, "")
	if err != nil {
		t.Fatalf("Join with an empty subpath: %v", err)
	}
	if got != root {
		t.Errorf("Join = %q, want %q", got, root)
	}
}

func TestJoinRejectsASubpathThatEscapesTheRevision(t *testing.T) {
	root := filepath.Join("/root", "rev", "github.com", "o", "r", "abc")

	for _, subpath := range []string{"..", "../elsewhere", "skills/../../elsewhere", "/etc", "skills/../.."} {
		if got, err := Join(root, subpath); err == nil {
			t.Errorf("Join(%q) = %q, want an error", subpath, got)
		}
	}
}

func TestWithinRejectsPathsOutsideTheRoot(t *testing.T) {
	s := New(t.TempDir())

	if err := s.within(filepath.Join(s.Root, "rev", "github.com", "o", "r", "abc")); err != nil {
		t.Errorf("within rejected a legitimate store path: %v", err)
	}
	if err := s.within(filepath.Join(s.Root, "rev", "..", "..", "escaped")); err == nil {
		t.Error("within accepted a path outside the store root")
	}
}

func TestEnsureLeavesNoTempDirOnFailure(t *testing.T) {
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": "x"})
	src, _ := source.Parse(url)

	s := New(t.TempDir())
	_, err := s.Ensure(context.Background(), gitx.New(), src.Slug(), src.RepoURL, "0123456789abcdef0123456789abcdef01234567")
	if err == nil {
		t.Fatal("Ensure succeeded for a sha that does not exist")
	}

	revParent := filepath.Dir(s.RevPath(src.Slug(), "irrelevant"))
	entries, rerr := os.ReadDir(revParent)
	if rerr != nil {
		return // nothing was created at all, which is fine
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("temporary extraction directory %q was left behind", e.Name())
		}
	}
}
