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
