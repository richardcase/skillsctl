package target

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinkCreatesParentDirectories(t *testing.T) {
	root := t.TempDir()
	rev := filepath.Join(root, "rev")
	if err := os.MkdirAll(rev, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "agent", "skills", "my-skill")

	if err := Link(link, rev); err != nil {
		t.Fatalf("Link: %v", err)
	}
	got, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("Readlink: %v", err)
	}
	if got != rev {
		t.Errorf("symlink points at %q, want %q", got, rev)
	}
}

func TestLinkIsIdempotent(t *testing.T) {
	root := t.TempDir()
	rev := filepath.Join(root, "rev")
	if err := os.MkdirAll(rev, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "skills", "my-skill")

	if err := Link(link, rev); err != nil {
		t.Fatalf("first Link: %v", err)
	}
	if err := Link(link, rev); err != nil {
		t.Fatalf("second Link should be a no-op, got: %v", err)
	}
}

func TestLinkRefusesToClobber(t *testing.T) {
	root := t.TempDir()
	rev := filepath.Join(root, "rev")
	if err := os.MkdirAll(rev, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "skills", "my-skill")
	if err := os.MkdirAll(link, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := Link(link, rev); err == nil {
		t.Fatal("Link overwrote an existing real directory; want an error")
	}
	if fi, err := os.Lstat(link); err != nil || !fi.IsDir() {
		t.Error("the existing directory must be left untouched")
	}
}

func TestUnlinkRemovesOnlySymlinks(t *testing.T) {
	root := t.TempDir()
	rev := filepath.Join(root, "rev")
	if err := os.MkdirAll(rev, 0o755); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(root, "skills", "linked")
	if err := Link(link, rev); err != nil {
		t.Fatal(err)
	}
	if err := Unlink(link); err != nil {
		t.Fatalf("Unlink: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Error("symlink was not removed")
	}

	realDir := filepath.Join(root, "skills", "real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Unlink(realDir); err == nil {
		t.Fatal("Unlink removed a real directory; want an error")
	}
	if _, err := os.Stat(realDir); err != nil {
		t.Error("the real directory must survive")
	}
}

func TestUnlinkMissingPathSucceeds(t *testing.T) {
	if err := Unlink(filepath.Join(t.TempDir(), "nothing-here")); err != nil {
		t.Errorf("Unlink of a missing path should be a no-op, got: %v", err)
	}
}
