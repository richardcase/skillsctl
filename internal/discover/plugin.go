package discover

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// PluginDir is the directory Claude Code plugins describe themselves in.
const PluginDir = ".claude-plugin"

// Metadata is display-only information about the repository a set of skills
// came from. It never affects which skills are discovered or what they are
// named: skillsctl's own listing is friendlier when a repository says what it
// is, and that is all this is for.
type Metadata struct {
	Name        string
	Description string
}

// PluginMeta reads .claude-plugin/plugin.json, falling back to
// marketplace.json. A missing, unreadable or malformed file yields a zero
// Metadata rather than an error: this is decoration, and decoration must never
// fail an install.
func PluginMeta(dir string) Metadata {
	for _, name := range []string{"plugin.json", "marketplace.json"} {
		data, err := os.ReadFile(filepath.Join(dir, PluginDir, name))
		if err != nil {
			continue
		}
		var m Metadata
		var raw struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if json.Unmarshal(data, &raw) != nil {
			continue
		}
		m.Name, m.Description = raw.Name, raw.Description
		if m != (Metadata{}) {
			return m
		}
	}
	return Metadata{}
}
