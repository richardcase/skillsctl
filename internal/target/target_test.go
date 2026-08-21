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
	defaults, err := Default()
	if err != nil {
		t.Fatalf("Default: %v", err)
	}
	if len(got.Targets) != len(defaults.Targets) {
		t.Fatalf("got %d targets, want the %d defaults", len(got.Targets), len(defaults.Targets))
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

// Resolve is the one rule install -a, sync and bundle all use to turn an
// agent selection into targets, so its three cases are pinned here rather than
// re-derived at each call site.
func TestResolve(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Targets: []Target{
		{Name: "claude", Dir: filepath.Join(root, ".claude", "skills")},
		{Name: "codex", Dir: filepath.Join(root, ".codex", "skills")},
	}}

	t.Run("named goes through Select", func(t *testing.T) {
		got, err := cfg.Resolve([]string{"codex"})
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if len(got) != 1 || got[0].Name != "codex" {
			t.Fatalf("Resolve(%v) = %+v, want [codex]", []string{"codex"}, got)
		}
		if _, err := cfg.Resolve([]string{"emacs"}); err == nil {
			t.Error("Resolve accepted an unknown agent name; want an error")
		}
	})

	t.Run("empty falls back to Present", func(t *testing.T) {
		got, err := cfg.Resolve(nil)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if len(got) != 1 || got[0].Name != "claude" {
			t.Fatalf("Resolve(nil) = %+v, want only the present agent", got)
		}
	})

	t.Run("empty Present is an error", func(t *testing.T) {
		empty := Config{Targets: []Target{
			{Name: "claude", Dir: filepath.Join(t.TempDir(), ".claude", "skills")},
		}}
		if _, err := empty.Resolve(nil); err == nil {
			t.Error("Resolve accepted an empty agent selection with no agent present; want an error")
		}
	})
}

func TestLoadExpandsBareTilde(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	body := "[[target]]\nname = \"claude\"\ndir = \"~\"\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory in this environment")
	}
	if got.Targets[0].Dir != home {
		t.Errorf("Dir = %q, want %q", got.Targets[0].Dir, home)
	}
}

func TestConfigPathPrecedence(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory in this environment")
	}

	t.Run("SKILLSCTL_CONFIG wins over everything", func(t *testing.T) {
		t.Setenv("SKILLSCTL_CONFIG", "/explicit/config.toml")
		t.Setenv("XDG_CONFIG_HOME", "/xdg")
		got, err := ConfigPath()
		if err != nil {
			t.Fatalf("ConfigPath: %v", err)
		}
		if got != "/explicit/config.toml" {
			t.Errorf("ConfigPath() = %q, want the SKILLSCTL_CONFIG value", got)
		}
	})

	t.Run("XDG_CONFIG_HOME wins when SKILLSCTL_CONFIG is unset", func(t *testing.T) {
		t.Setenv("SKILLSCTL_CONFIG", "")
		t.Setenv("XDG_CONFIG_HOME", "/xdg")
		got, err := ConfigPath()
		if err != nil {
			t.Fatalf("ConfigPath: %v", err)
		}
		want := filepath.Join("/xdg", "skillsctl", "config.toml")
		if got != want {
			t.Errorf("ConfigPath() = %q, want %q", got, want)
		}
	})

	t.Run("falls back to ~/.config when neither is set", func(t *testing.T) {
		t.Setenv("SKILLSCTL_CONFIG", "")
		t.Setenv("XDG_CONFIG_HOME", "")
		got, err := ConfigPath()
		if err != nil {
			t.Fatalf("ConfigPath: %v", err)
		}
		want := filepath.Join(home, ".config", "skillsctl", "config.toml")
		if got != want {
			t.Errorf("ConfigPath() = %q, want %q", got, want)
		}
	})
}

func TestLoadRejectsTildeUser(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	body := "[[target]]\nname = \"claude\"\ndir = \"~someone/skills\"\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(p); err == nil {
		t.Fatal("Load accepted ~user syntax; want an error rather than a silently wrong path")
	}
}

func TestWithoutPluginsIsTheAgentsToFanOutTo(t *testing.T) {
	ts := []Target{
		{Name: "claude", Plugins: true},
		{Name: "codex"},
		{Name: "gemini"},
	}

	got := WithoutPlugins(ts)
	if len(got) != 2 || got[0].Name != "codex" || got[1].Name != "gemini" {
		t.Fatalf("WithoutPlugins = %v, want codex then gemini in that order", got)
	}
	if len(WithoutPlugins(nil)) != 0 {
		t.Error("no agents means nothing to fan out to, not a panic")
	}
}

func TestLoadParsesRegistryTable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := "[[target]]\nname = \"claude\"\ndir = \"" + filepath.Join(dir, "skills") + "\"\n\n" +
		"[registry]\nurl = \"https://example.com/skills.json\"\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Registry.URL != "https://example.com/skills.json" {
		t.Errorf("Registry.URL = %q, want %q", cfg.Registry.URL, "https://example.com/skills.json")
	}
}

func TestLoadLeavesRegistryEmptyWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := "[[target]]\nname = \"claude\"\ndir = \"" + filepath.Join(dir, "skills") + "\"\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Registry.URL != "" {
		t.Errorf("Registry.URL = %q, want empty", cfg.Registry.URL)
	}
}
