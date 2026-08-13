package target

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "absent.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Targets) != len(Default().Targets) {
		t.Fatalf("got %d targets, want the %d defaults", len(got.Targets), len(Default().Targets))
	}
	if got.Targets[0].Name != "claude" {
		t.Errorf("first default target = %q, want claude", got.Targets[0].Name)
	}
	if !got.Targets[0].Plugins {
		t.Error("claude target must have Plugins enabled")
	}
}

func TestLoadExpandsTilde(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	body := `
[[target]]
name = "claude"
dir = "~/.claude/skills"
plugins = true
`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".claude", "skills")
	if got.Targets[0].Dir != want {
		t.Errorf("Dir = %q, want %q", got.Targets[0].Dir, want)
	}
}

func TestPresentUsesParentDirectory(t *testing.T) {
	root := t.TempDir()
	// ~/.claude exists but ~/.claude/skills does not: still present, because
	// the skills directory is created on demand at first install.
	if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := Config{Targets: []Target{
		{Name: "claude", Dir: filepath.Join(root, ".claude", "skills")},
		{Name: "codex", Dir: filepath.Join(root, ".codex", "skills")},
	}}

	got := cfg.Present()
	if len(got) != 1 || got[0].Name != "claude" {
		t.Fatalf("Present() = %+v, want only claude", got)
	}
}

func TestSelect(t *testing.T) {
	cfg := Config{Targets: []Target{{Name: "claude"}, {Name: "codex"}, {Name: "gemini"}}}

	got, err := cfg.Select([]string{"codex", "claude"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Select returned %d targets, want 2", len(got))
	}

	if _, err := cfg.Select([]string{"emacs"}); err == nil {
		t.Error("Select accepted an unknown agent name; want an error")
	}
}
