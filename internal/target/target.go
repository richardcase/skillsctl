// Package target describes the agents skillsctl installs into, and manages the
// symlinks in their skills directories.
package target

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Target is one agent's skills directory.
type Target struct {
	Name       string `toml:"name"`
	Dir        string `toml:"dir"`
	ProjectDir string `toml:"project_dir"`
	Plugins    bool   `toml:"plugins"`
}

// Config is the set of agents skillsctl knows about.
type Config struct {
	Targets []Target `toml:"target"`
}

// Default is the built-in agent table, used when no config file exists.
func Default() (Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, fmt.Errorf("locate home directory: %w", err)
	}
	return Config{Targets: []Target{
		{Name: "claude", Dir: filepath.Join(home, ".claude", "skills"), ProjectDir: ".claude/skills", Plugins: true},
		{Name: "codex", Dir: filepath.Join(home, ".codex", "skills"), ProjectDir: ".codex/skills"},
		{Name: "gemini", Dir: filepath.Join(home, ".gemini", "skills"), ProjectDir: ".gemini/skills"},
	}}, nil
}

// ConfigPath is where the agent table lives, honouring SKILLSCTL_CONFIG and
// XDG_CONFIG_HOME before falling back to ~/.config.
func ConfigPath() (string, error) {
	if p := os.Getenv("SKILLSCTL_CONFIG"); p != "" {
		return p, nil
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "skillsctl", "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, ".config", "skillsctl", "config.toml"), nil
}

// Load reads the agent table, returning Default when the file does not exist.
func Load(path string) (Config, error) {
	blob, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Default()
	}
	if err != nil {
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}

	var cfg Config
	if err := toml.Unmarshal(blob, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(cfg.Targets) == 0 {
		return Config{}, fmt.Errorf("%s defines no [[target]] entries", path)
	}
	for i := range cfg.Targets {
		dir, err := expand(cfg.Targets[i].Dir)
		if err != nil {
			return Config{}, fmt.Errorf("%s: %w", path, err)
		}
		cfg.Targets[i].Dir = dir
	}
	return cfg, nil
}

func expand(p string) (string, error) {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		if strings.HasPrefix(p, "~") {
			return "", fmt.Errorf("unsupported path %q: ~user syntax is not supported, use an absolute path", p)
		}
		return p, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("expand %q: locate home directory: %w", p, err)
	}
	if p == "~" {
		return home, nil
	}
	return filepath.Join(home, p[2:]), nil
}

// Present returns the targets whose agent directory exists. The skills
// subdirectory itself may be absent; it is created at first install.
func (c Config) Present() []Target {
	var out []Target
	for _, t := range c.Targets {
		if fi, err := os.Stat(filepath.Dir(t.Dir)); err == nil && fi.IsDir() {
			out = append(out, t)
		}
	}
	return out
}

// Select returns the named targets, in the order given.
func (c Config) Select(names []string) ([]Target, error) {
	byName := make(map[string]Target, len(c.Targets))
	known := make([]string, 0, len(c.Targets))
	for _, t := range c.Targets {
		byName[t.Name] = t
		known = append(known, t.Name)
	}

	out := make([]Target, 0, len(names))
	for _, n := range names {
		t, ok := byName[n]
		if !ok {
			return nil, fmt.Errorf("unknown agent %q (known: %s)", n, strings.Join(known, ", "))
		}
		out = append(out, t)
	}
	return out, nil
}
