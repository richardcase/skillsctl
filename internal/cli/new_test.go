package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewScaffoldsAndLinksASkill(t *testing.T) {
	h := newHarness(t)
	t.Chdir(t.TempDir())

	out, err := h.run(t, "new", "my-new-skill", "--description", "Do a thing")
	if err != nil {
		t.Fatalf("new: %v\n%s", err, out)
	}
	if !strings.Contains(out, "installed my-new-skill into claude, codex") {
		t.Errorf("output = %q, want it to say the skill was linked", out)
	}

	body, err := os.ReadFile(filepath.Join("my-new-skill", "SKILL.md"))
	if err != nil {
		t.Fatalf("read scaffolded SKILL.md: %v", err)
	}
	if !strings.Contains(string(body), `name: "my-new-skill"`) || !strings.Contains(string(body), `description: "Do a thing"`) {
		t.Errorf("SKILL.md = %q, want the name and description in its frontmatter", body)
	}

	r := h.receipts(t)["my-new-skill"]
	if r["channel"] != "local" {
		t.Errorf("channel = %v, want local", r["channel"])
	}

	if _, err := os.Lstat(filepath.Join(h.claude, "my-new-skill")); err != nil {
		t.Errorf("claude link missing: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(h.codex, "my-new-skill")); err != nil {
		t.Errorf("codex link missing: %v", err)
	}
}

func TestNewDefaultsDescriptionToATODO(t *testing.T) {
	h := newHarness(t)
	t.Chdir(t.TempDir())

	if out, err := h.run(t, "new", "undescribed"); err != nil {
		t.Fatalf("new: %v\n%s", err, out)
	}

	body, err := os.ReadFile(filepath.Join("undescribed", "SKILL.md"))
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}
	if !strings.Contains(string(body), `description: "TODO`) {
		t.Errorf("SKILL.md = %q, want a TODO placeholder description", body)
	}
}

func TestNewRefusesAnExistingDirectory(t *testing.T) {
	h := newHarness(t)
	t.Chdir(t.TempDir())

	if err := os.Mkdir("taken", 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := h.run(t, "new", "taken")
	if err == nil {
		t.Fatalf("new accepted an existing directory\n%s", out)
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %v, want it to say the directory already exists", err)
	}
	if _, statErr := os.Stat(filepath.Join("taken", "SKILL.md")); !os.IsNotExist(statErr) {
		t.Error("new wrote into a directory it did not create")
	}
}

func TestNewRejectsAnInvalidName(t *testing.T) {
	h := newHarness(t)
	t.Chdir(t.TempDir())

	out, err := h.run(t, "new", "../escaped")
	if err == nil {
		t.Fatalf("new accepted a name containing a path separator\n%s", out)
	}
	if _, statErr := os.Stat("../escaped"); !os.IsNotExist(statErr) {
		t.Error("new created something outside the working directory")
	}
}

func TestNewDryRunChangesNothing(t *testing.T) {
	h := newHarness(t)
	t.Chdir(t.TempDir())

	out, err := h.run(t, "new", "preview-only", "--dry-run")
	if err != nil {
		t.Fatalf("new --dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "write preview-only/SKILL.md") {
		t.Errorf("output = %q, want it to describe the scaffold it would write", out)
	}
	if !strings.Contains(out, "skillsctl link ./preview-only") {
		t.Errorf("output = %q, want it to name the follow-up link command", out)
	}
	if _, err := os.Stat("preview-only"); !os.IsNotExist(err) {
		t.Error("dry run created the skill directory")
	}
	if _, err := os.Stat(filepath.Join(h.root, "state.json")); !os.IsNotExist(err) {
		t.Error("dry run wrote the receipts database")
	}
}

func TestNewDryRunValidatesTheAgentFlag(t *testing.T) {
	h := newHarness(t)
	t.Chdir(t.TempDir())

	out, err := h.run(t, "new", "preview-only", "--dry-run", "-a", "bogus")
	if err == nil {
		t.Fatalf("new --dry-run accepted an unknown agent\n%s", out)
	}
	if _, err := os.Stat("preview-only"); !os.IsNotExist(err) {
		t.Error("dry run created the skill directory despite the bad agent")
	}
}
