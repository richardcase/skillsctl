package target

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLinkCreatesParentDirectories(t *testing.T) {
	root := t.TempDir()
	rev := filepath.Join(root, "rev")
	if err := os.MkdirAll(rev, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "agent", "skills", "my-skill")

	created, err := Link(link, rev)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if !created {
		t.Error("Link should report created == true on first call")
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

	created, err := Link(link, rev)
	if err != nil {
		t.Fatalf("first Link: %v", err)
	}
	if !created {
		t.Error("first Link should report created == true")
	}

	created, err = Link(link, rev)
	if err != nil {
		t.Fatalf("second Link should be a no-op, got: %v", err)
	}
	if created {
		t.Error("second Link should report created == false on idempotent call")
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

	_, err := Link(link, rev)
	if err == nil {
		t.Fatal("Link overwrote an existing real directory; want an error")
	}
	if fi, err := os.Lstat(link); err != nil || !fi.IsDir() {
		t.Error("the existing directory must be left untouched")
	}
}

func TestLinkPointsAHandMadeSymlinkAtAdopt(t *testing.T) {
	root := t.TempDir()
	rev := filepath.Join(root, "rev")
	other := filepath.Join(root, "other")
	for _, d := range []string{rev, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(root, "skills", "my-skill")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, link); err != nil {
		t.Fatal(err)
	}

	_, err := Link(link, rev)
	if err == nil {
		t.Fatal("Link re-pointed a symlink somebody else made; want an error")
	}
	// Taking it over is the whole point of adopt, so the error has to say so.
	if !strings.Contains(err.Error(), "skillsctl adopt") {
		t.Errorf("error = %q, want it to name adopt as the takeover path", err)
	}
}

func TestUnlinkRemovesOnlySymlinks(t *testing.T) {
	root := t.TempDir()
	rev := filepath.Join(root, "rev")
	if err := os.MkdirAll(rev, 0o755); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(root, "skills", "linked")
	if _, err := Link(link, rev); err != nil {
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

func TestRelinkRepointsAndReportsThePrevious(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, "old")
	fresh := filepath.Join(root, "new")
	for _, d := range []string{old, fresh} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(root, "skills", "my-skill")
	if _, err := Link(link, old); err != nil {
		t.Fatal(err)
	}

	previous, err := Relink(link, fresh)
	if err != nil {
		t.Fatalf("Relink: %v", err)
	}
	if previous != old {
		t.Errorf("Relink reported previous %q, want %q", previous, old)
	}
	got, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("Readlink: %v", err)
	}
	if got != fresh {
		t.Errorf("symlink points at %q, want %q", got, fresh)
	}

	// Nothing may be left in the skills directory beyond the link itself: a
	// leftover temp directory would show up as a skill to the agent.
	entries, err := os.ReadDir(filepath.Dir(link))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("skills directory holds %d entries, want just the link", len(entries))
	}
}

func TestRelinkCreatesAMissingLink(t *testing.T) {
	root := t.TempDir()
	rev := filepath.Join(root, "rev")
	if err := os.MkdirAll(rev, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "skills", "my-skill")

	previous, err := Relink(link, rev)
	if err != nil {
		t.Fatalf("Relink: %v", err)
	}
	if previous != "" {
		t.Errorf("Relink reported previous %q, want empty for a link it created", previous)
	}
	if got, err := os.Readlink(link); err != nil || got != rev {
		t.Errorf("Readlink = %q, %v; want %q", got, err, rev)
	}
}

func TestRelinkRefusesToClobber(t *testing.T) {
	root := t.TempDir()
	rev := filepath.Join(root, "rev")
	if err := os.MkdirAll(rev, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "skills", "my-skill")
	if err := os.MkdirAll(link, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := Relink(link, rev); err == nil {
		t.Fatal("Relink replaced a real directory; want an error")
	}
	if fi, err := os.Lstat(link); err != nil || !fi.IsDir() {
		t.Error("the existing directory must be left untouched")
	}
}

func TestOccupiedRejectsEscapingNames(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"../escape", "a/b", `a\b`, ".."} {
		if !Occupied(dir, name) {
			t.Errorf("Occupied(%q, %q) = false, want true for an invalid name", dir, name)
		}
	}
	if _, err := os.Lstat(filepath.Join(filepath.Dir(dir), "escape")); !os.IsNotExist(err) {
		t.Error("Occupied must not touch anything outside dir")
	}
}

func TestValidateSkillName(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"good-name", false},
		{"name.with.dots", false},
		{"", true},
		{".", true},
		{"..", true},
		{"../x", true},
		{"a/b", true},
		{`a\b`, true},
		{"foo..bar", true},
		{"..hidden", true},
	}
	for _, tc := range tests {
		err := ValidateSkillName(tc.name)
		if tc.wantErr && err == nil {
			t.Errorf("ValidateSkillName(%q) = nil, want an error", tc.name)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("ValidateSkillName(%q) = %v, want nil", tc.name, err)
		}
	}
}
