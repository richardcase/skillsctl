package discover

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

// PluginEntry is one plugins[] entry in a root marketplace.json: the name a
// plugin is registered under, and where its skills live relative to the
// marketplace root.
type PluginEntry struct {
	Name   string
	Source string
}

// MarketplacePlugins reads .claude-plugin/marketplace.json's plugins[] array.
// A missing, unreadable or malformed file yields nil rather than an error:
// this is decoration, and decoration must never fail an install.
func MarketplacePlugins(dir string) []PluginEntry {
	data, err := os.ReadFile(filepath.Join(dir, PluginDir, "marketplace.json"))
	if err != nil {
		return nil
	}
	var raw struct {
		Plugins []struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		} `json:"plugins"`
	}
	if json.Unmarshal(data, &raw) != nil {
		return nil
	}
	entries := make([]PluginEntry, 0, len(raw.Plugins))
	for _, p := range raw.Plugins {
		entries = append(entries, PluginEntry{Name: p.Name, Source: normalizeSource(p.Source)})
	}
	return entries
}

// normalizeSource strips a leading "./" and a trailing "/" from a plugins[]
// source path, so it compares directly against a Skill.Rel.
func normalizeSource(s string) string {
	s = strings.TrimPrefix(s, "./")
	return strings.TrimSuffix(s, "/")
}

// MatchPlugin reports which entry a skill at rel belongs to: rel equals the
// entry's Source, or sits under it. When more than one entry matches - a
// plugin nested inside another's source - the longest, most specific Source
// wins.
func MatchPlugin(entries []PluginEntry, rel string) (PluginEntry, bool) {
	best := -1
	for i, e := range entries {
		if e.Source == "" {
			continue
		}
		if rel != e.Source && !strings.HasPrefix(rel, e.Source+"/") {
			continue
		}
		if best < 0 || len(e.Source) > len(entries[best].Source) {
			best = i
		}
	}
	if best < 0 {
		return PluginEntry{}, false
	}
	return entries[best], true
}

// Decorate fills each skill's Plugin field from walkRoot's marketplace.json,
// and backfills a missing frontmatter description from that plugin's own
// plugin.json. walkRoot is the directory Walk was called on. A repository
// with no marketplace.json leaves skills unchanged.
func Decorate(walkRoot string, skills []Skill) []Skill {
	entries := MarketplacePlugins(walkRoot)
	if len(entries) == 0 {
		return skills
	}
	for i, s := range skills {
		entry, ok := MatchPlugin(entries, s.Rel)
		if !ok {
			continue
		}
		skills[i].Plugin = entry.Name
		if s.Description == "" {
			skills[i].Description = PluginMeta(filepath.Join(walkRoot, filepath.FromSlash(entry.Source))).Description
		}
	}
	return skills
}
