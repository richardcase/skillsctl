package discover

import (
	"os"
	"path/filepath"
	"testing"
)

func writePluginFile(t *testing.T, root, name, body string) {
	t.Helper()
	dir := filepath.Join(root, PluginDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPluginMetaFromPluginJSON(t *testing.T) {
	dir := t.TempDir()
	writePluginFile(t, dir, "plugin.json", `{"name":"agent-skills","description":"A pile of skills","version":"1.2.3"}`)

	got := PluginMeta(dir)
	if got.Name != "agent-skills" {
		t.Errorf("Name = %q, want agent-skills", got.Name)
	}
	if got.Description != "A pile of skills" {
		t.Errorf("Description = %q", got.Description)
	}
}

func TestPluginMetaFallsBackToMarketplaceJSON(t *testing.T) {
	dir := t.TempDir()
	writePluginFile(t, dir, "marketplace.json", `{"name":"acme","owner":{"name":"Acme"},"plugins":[]}`)

	got := PluginMeta(dir)
	if got.Name != "acme" {
		t.Errorf("Name = %q, want acme", got.Name)
	}
}

func TestPluginMetaPrefersPluginJSON(t *testing.T) {
	dir := t.TempDir()
	writePluginFile(t, dir, "plugin.json", `{"name":"from-plugin"}`)
	writePluginFile(t, dir, "marketplace.json", `{"name":"from-marketplace"}`)

	if got := PluginMeta(dir); got.Name != "from-plugin" {
		t.Errorf("Name = %q, want from-plugin", got.Name)
	}
}

func TestPluginMetaAbsentIsZero(t *testing.T) {
	if got := PluginMeta(t.TempDir()); got != (Metadata{}) {
		t.Errorf("PluginMeta = %+v, want a zero Metadata", got)
	}
}

func TestPluginMetaMalformedIsZeroNotAFailure(t *testing.T) {
	dir := t.TempDir()
	writePluginFile(t, dir, "plugin.json", "{not json")

	if got := PluginMeta(dir); got != (Metadata{}) {
		t.Errorf("PluginMeta = %+v, want a zero Metadata: decoration must never fail an install", got)
	}
}

func TestMarketplacePluginsParsesNameAndSource(t *testing.T) {
	dir := t.TempDir()
	writePluginFile(t, dir, "marketplace.json", `{"name":"humanlayer","plugins":[
		{"name":"show-me","source":"./plugins/show-me"},
		{"name":"improve-claude-md","source":"./plugins/improve-claude-md"}
	]}`)

	got := MarketplacePlugins(dir)
	want := []PluginEntry{
		{Name: "show-me", Source: "plugins/show-me"},
		{Name: "improve-claude-md", Source: "plugins/improve-claude-md"},
	}
	if len(got) != len(want) {
		t.Fatalf("MarketplacePlugins = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestMarketplacePluginsStripsTrailingSlash(t *testing.T) {
	dir := t.TempDir()
	writePluginFile(t, dir, "marketplace.json", `{"plugins":[{"name":"show-me","source":"./plugins/show-me/"}]}`)

	got := MarketplacePlugins(dir)
	if len(got) != 1 || got[0].Source != "plugins/show-me" {
		t.Errorf("MarketplacePlugins = %+v, want Source %q", got, "plugins/show-me")
	}
}

func TestMarketplacePluginsAbsentIsNil(t *testing.T) {
	if got := MarketplacePlugins(t.TempDir()); got != nil {
		t.Errorf("MarketplacePlugins = %+v, want nil", got)
	}
}

func TestMarketplacePluginsMalformedIsNilNotAFailure(t *testing.T) {
	dir := t.TempDir()
	writePluginFile(t, dir, "marketplace.json", "{not json")

	if got := MarketplacePlugins(dir); got != nil {
		t.Errorf("MarketplacePlugins = %+v, want nil: decoration must never fail an install", got)
	}
}

func TestMatchPluginExactMatch(t *testing.T) {
	entries := []PluginEntry{{Name: "show-me", Source: "show-me"}}
	got, ok := MatchPlugin(entries, "show-me")
	if !ok || got.Name != "show-me" {
		t.Fatalf("MatchPlugin = %+v, %v, want show-me, true", got, ok)
	}
}

func TestMatchPluginPrefixMatch(t *testing.T) {
	entries := []PluginEntry{{Name: "show-me", Source: "plugins/show-me"}}
	got, ok := MatchPlugin(entries, "plugins/show-me/skills/show-me")
	if !ok || got.Name != "show-me" {
		t.Fatalf("MatchPlugin = %+v, %v, want show-me, true", got, ok)
	}
}

func TestMatchPluginNoMatch(t *testing.T) {
	entries := []PluginEntry{{Name: "show-me", Source: "plugins/show-me"}}
	if _, ok := MatchPlugin(entries, "plugins/other/skills/other"); ok {
		t.Fatal("MatchPlugin matched an unrelated path")
	}
}

func TestMatchPluginDoesNotMatchSiblingWithSharedPrefix(t *testing.T) {
	entries := []PluginEntry{{Name: "show-me", Source: "plugins/show-me"}}
	if _, ok := MatchPlugin(entries, "plugins/show-me-extra/skills/x"); ok {
		t.Fatal("MatchPlugin matched a sibling directory whose name merely shares a prefix")
	}
}

func TestMatchPluginPicksLongestSourceOnOverlap(t *testing.T) {
	entries := []PluginEntry{
		{Name: "outer", Source: "plugins"},
		{Name: "inner", Source: "plugins/show-me"},
	}
	got, ok := MatchPlugin(entries, "plugins/show-me/skills/show-me")
	if !ok || got.Name != "inner" {
		t.Fatalf("MatchPlugin = %+v, %v, want inner, true", got, ok)
	}
}

func TestDecorateSetsPluginAndBackfillsDescription(t *testing.T) {
	root := t.TempDir()
	writePluginFile(t, root, "marketplace.json", `{"plugins":[{"name":"show-me","source":"./plugins/show-me"}]}`)
	writePluginFile(t, filepath.Join(root, "plugins", "show-me"), "plugin.json", `{"description":"Explain visually"}`)

	skills := []Skill{
		{Meta: Meta{Name: "show-me"}, Rel: "plugins/show-me/skills/show-me"},
	}
	got := Decorate(root, skills)
	if got[0].Plugin != "show-me" {
		t.Errorf("Plugin = %q, want show-me", got[0].Plugin)
	}
	if got[0].Description != "Explain visually" {
		t.Errorf("Description = %q, want the plugin.json fallback", got[0].Description)
	}
}

func TestDecorateDoesNotOverrideExistingDescription(t *testing.T) {
	root := t.TempDir()
	writePluginFile(t, root, "marketplace.json", `{"plugins":[{"name":"show-me","source":"./plugins/show-me"}]}`)
	writePluginFile(t, filepath.Join(root, "plugins", "show-me"), "plugin.json", `{"description":"Fallback"}`)

	skills := []Skill{
		{Meta: Meta{Name: "show-me", Description: "Own description"}, Rel: "plugins/show-me/skills/show-me"},
	}
	got := Decorate(root, skills)
	if got[0].Description != "Own description" {
		t.Errorf("Description = %q, want the skill's own description preserved", got[0].Description)
	}
}

func TestDecorateNoopWhenNoMarketplaceJSON(t *testing.T) {
	root := t.TempDir()
	skills := []Skill{{Meta: Meta{Name: "solo"}, Rel: "solo"}}

	got := Decorate(root, skills)
	if got[0].Plugin != "" {
		t.Errorf("Plugin = %q, want empty for a repo with no marketplace.json", got[0].Plugin)
	}
}
