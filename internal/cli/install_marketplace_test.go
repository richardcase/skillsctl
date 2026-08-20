package cli

import (
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/testrepo"
)

const (
	showMeMD          = "---\nname: show-me\ndescription: Explain visually\n---\n\nBody.\n"
	improveClaudeMDMD = "---\nname: improve-claude-md\ndescription: Improve CLAUDE.md files\n---\n\nBody.\n"
	marketplaceJSON   = `{"name":"humanlayer","plugins":[
		{"name":"show-me","source":"./plugins/show-me"},
		{"name":"improve-claude-md","source":"./plugins/improve-claude-md"}
	]}`
)

// marketplaceRepo is a fixture repository shaped like a Claude Code plugin
// marketplace: a root marketplace.json naming two plugins, each nesting a
// skill four levels deep at plugins/<plugin>/skills/<skill>/SKILL.md.
func marketplaceRepo(t *testing.T) (url, sha string) {
	t.Helper()
	return testrepo.New(t, map[string]string{
		".claude-plugin/marketplace.json":                             marketplaceJSON,
		"plugins/show-me/skills/show-me/SKILL.md":                     showMeMD,
		"plugins/improve-claude-md/skills/improve-claude-md/SKILL.md": improveClaudeMDMD,
	})
}

func TestInstallMarketplaceRepoListsGroupedByPlugin(t *testing.T) {
	h := newHarness(t)
	url, _ := marketplaceRepo(t)

	out, err := h.run(t, "install", url)
	if err == nil {
		t.Fatalf("bare install of a marketplace repo succeeded; it must not guess\n%s", out)
	}
	for _, want := range []string{"show-me", "improve-claude-md", "Explain visually", "Improve CLAUDE.md files"} {
		if !strings.Contains(out, want) {
			t.Errorf("output should list what is available (%q missing):\n%s", want, out)
		}
	}
	if linked(t, h, "show-me") || linked(t, h, "improve-claude-md") {
		t.Error("nothing should have been linked")
	}
}

func TestInstallMarketplaceSkillFlagMatchesRegardlessOfNesting(t *testing.T) {
	h := newHarness(t)
	url, _ := marketplaceRepo(t)

	if out, err := h.run(t, "install", url, "--skill", "show-me"); err != nil {
		t.Fatalf("install --skill show-me: %v\n%s", err, out)
	}
	if !linked(t, h, "show-me") {
		t.Error("show-me should be linked")
	}
	if linked(t, h, "improve-claude-md") {
		t.Error("improve-claude-md was not asked for")
	}
}

func TestInstallMarketplaceAllInstallsBothAcrossPlugins(t *testing.T) {
	h := newHarness(t)
	url, _ := marketplaceRepo(t)

	out, err := h.run(t, "install", url, "--all")
	if err != nil {
		t.Fatalf("install --all: %v\n%s", err, out)
	}
	for _, name := range []string{"show-me", "improve-claude-md"} {
		if !linked(t, h, name) {
			t.Errorf("%s should be linked", name)
		}
	}
}

func TestInstallMarketplaceDuplicateSkillNameAcrossPluginsErrors(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{
		".claude-plugin/marketplace.json": `{"plugins":[
			{"name":"plugin-a","source":"./plugins/plugin-a"},
			{"name":"plugin-b","source":"./plugins/plugin-b"}
		]}`,
		"plugins/plugin-a/skills/shared/SKILL.md": "---\nname: shared\n---\n\nBody.\n",
		"plugins/plugin-b/skills/shared/SKILL.md": "---\nname: shared\n---\n\nBody.\n",
	})

	out, err := h.run(t, "install", url, "--all")
	if err == nil {
		t.Fatalf("install of two same-named skills from different plugins succeeded\n%s", out)
	}
	for _, want := range []string{"plugins/plugin-a/skills/shared", "plugins/plugin-b/skills/shared"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to name both subpaths (%q missing)", err, want)
		}
	}
}
