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
	// Plugins marks an agent that installs plugins from a marketplace for
	// itself. It gates installing a plugin, never seeing one: an agent without
	// it is where a plugin's skills are linked, not one they are kept from.
	Plugins bool `toml:"plugins"`
}

// RegistryConfig configures where `skillsctl search` fetches the skill
// registry from.
type RegistryConfig struct {
	URL string `toml:"url"`
}

// Config is the set of agents skillsctl knows about, plus where it fetches
// the skill registry from.
type Config struct {
	Targets  []Target       `toml:"target"`
	Registry RegistryConfig `toml:"registry"`
}

// Default is the built-in agent table, used when no config file exists.
//
// Directory conventions for the non-claude/codex/gemini agents are taken from
// vercel-labs/skills' src/agents.ts (the "npx skills" implementation this
// project's README acknowledges as an inspiration), the closest thing to an
// authoritative source for where each agent actually looks for skills.
func Default() (Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, fmt.Errorf("locate home directory: %w", err)
	}
	cfgHome, err := configHome()
	if err != nil {
		return Config{}, err
	}
	return Config{Targets: []Target{
		{Name: "claude", Dir: filepath.Join(home, ".claude", "skills"), ProjectDir: ".claude/skills", Plugins: true},
		{Name: "codex", Dir: filepath.Join(home, ".codex", "skills"), ProjectDir: ".codex/skills"},
		{Name: "gemini", Dir: filepath.Join(home, ".gemini", "skills"), ProjectDir: ".gemini/skills"},
		{Name: "cursor", Dir: filepath.Join(home, ".cursor", "skills"), ProjectDir: ".agents/skills"},
		{Name: "windsurf", Dir: filepath.Join(home, ".codeium", "windsurf", "skills"), ProjectDir: ".windsurf/skills"},
		{Name: "cline", Dir: filepath.Join(home, ".agents", "skills"), ProjectDir: ".agents/skills"},
		{Name: "continue", Dir: filepath.Join(home, ".continue", "skills"), ProjectDir: ".continue/skills"},
		{Name: "zed", Dir: filepath.Join(home, ".agents", "skills"), ProjectDir: ".agents/skills"},
		{Name: "amp", Dir: filepath.Join(cfgHome, "agents", "skills"), ProjectDir: ".agents/skills"},
		{Name: "opencode", Dir: filepath.Join(cfgHome, "opencode", "skills"), ProjectDir: ".agents/skills"},
		{Name: "copilot", Dir: filepath.Join(home, ".copilot", "skills"), ProjectDir: ".agents/skills"},
		{Name: "antigravity", Dir: filepath.Join(home, ".gemini", "antigravity", "skills"), ProjectDir: ".agents/skills"},
		{Name: "kiro", Dir: filepath.Join(home, ".kiro", "skills"), ProjectDir: ".kiro/skills"},
	}}, nil
}

// configHome is XDG_CONFIG_HOME, falling back to ~/.config — the same rule
// ConfigPath applies to skillsctl's own config file, reused here for the
// agents (amp, opencode) that keep their own config under it too.
func configHome() (string, error) {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return x, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, ".config"), nil
}

// ConfigPath is where the agent table lives, honouring SKILLSCTL_CONFIG and
// XDG_CONFIG_HOME before falling back to ~/.config.
func ConfigPath() (string, error) {
	if p := os.Getenv("SKILLSCTL_CONFIG"); p != "" {
		return p, nil
	}
	cfgHome, err := configHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfgHome, "skillsctl", "config.toml"), nil
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
		dir, err := Expand(cfg.Targets[i].Dir)
		if err != nil {
			return Config{}, fmt.Errorf("%s: %w", path, err)
		}
		cfg.Targets[i].Dir = dir
	}
	return cfg, nil
}

// Expand resolves a leading ~ against the user's home directory. ~user syntax
// is refused rather than guessed at, because the only sensible reading of it is
// "a directory belonging to somebody else".
func Expand(p string) (string, error) {
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

// WithPlugins narrows targets to the agents that install plugins from a
// marketplace. A plugin's skills are already visible to the agent that
// installed it, so this is both who the plugin channel can install for and who
// a plugin receipt is live in.
func WithPlugins(ts []Target) []Target {
	var out []Target
	for _, t := range ts {
		if t.Plugins {
			out = append(out, t)
		}
	}
	return out
}

// WithoutPlugins narrows targets to the agents that cannot install plugins from
// a marketplace, which is exactly the set a plugin's skills have to be linked
// into. It is the complement of WithPlugins rather than a second flag: an agent
// either fetches a plugin for itself or is shown one, never both.
func WithoutPlugins(ts []Target) []Target {
	var out []Target
	for _, t := range ts {
		if !t.Plugins {
			out = append(out, t)
		}
	}
	return out
}

// Resolve returns the named targets, or every present agent when names is
// empty — the rule install -a resolves by, and every other command that takes
// an agent selection resolves the same way.
func (c Config) Resolve(names []string) ([]Target, error) {
	if len(names) > 0 {
		return c.Select(names)
	}
	present := c.Present()
	if len(present) == 0 {
		return nil, fmt.Errorf("no agent directories found: create one (for example ~/.claude) or configure targets")
	}
	return present, nil
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
