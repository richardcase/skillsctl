package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/prompt"
	"github.com/richardcase/skillsctl/internal/testrepo"
)

// singleSkillRepo is a fixture with exactly one skill, so the skill-ambiguity
// picker never fires and every Select call in these tests is the agent
// picker's.
func singleSkillRepo(t *testing.T) (url, sha string) {
	t.Helper()
	return testrepo.New(t, map[string]string{"SKILL.md": skillMD})
}

func TestInstallOffersEveryConfiguredAgentWithClaudeAndCodexPreTicked(t *testing.T) {
	h := newHarness(t)
	url, _ := singleSkillRepo(t)
	// The test harness's config only has claude and codex (see newHarness),
	// and both agent directories exist, so both should start ticked.
	h.picker.on, h.picker.choose = true, picks(0, 1)

	out, err := h.run(t, "install", url)
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	asked := h.picker.asked
	if len(asked.Items) != 2 {
		t.Fatalf("picker was offered %d rows, want 2 (claude, codex): %+v", len(asked.Items), asked.Items)
	}
	if !strings.Contains(asked.Items[0].Label, "claude") || !asked.Items[0].Selected {
		t.Errorf("row 0 = %+v, want claude pre-ticked", asked.Items[0])
	}
	if !strings.Contains(asked.Items[1].Label, "codex") || !asked.Items[1].Selected {
		t.Errorf("row 1 = %+v, want codex pre-ticked", asked.Items[1])
	}
	if asked.Single {
		t.Error("the agent picker takes several agents")
	}
}

func TestInstallPicksASubsetOfAgents(t *testing.T) {
	h := newHarness(t)
	url, _ := singleSkillRepo(t)
	h.picker.on, h.picker.choose = true, picks(1) // codex only

	out, err := h.run(t, "install", url)
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if linked(t, h, "demo-skill") {
		t.Error("claude was not picked, so it should not be linked")
	}
	if _, err := os.Lstat(filepath.Join(h.codex, "demo-skill")); err != nil {
		t.Error("codex was picked, so it should be linked")
	}
}

func TestInstallCancelledAgentPickChangesNothing(t *testing.T) {
	h := newHarness(t)
	url, _ := singleSkillRepo(t)
	h.picker.on = true // choose is nil, so the picker cancels

	out, err := h.run(t, "install", url)
	if err == nil {
		t.Fatalf("a cancelled install succeeded\n%s", out)
	}
	if !errors.Is(err, prompt.ErrCancelled) {
		t.Errorf("error = %v, want it to be ErrCancelled", err)
	}
	if linked(t, h, "demo-skill") {
		t.Error("nothing should have been linked")
	}
	if _, err := os.Stat(filepath.Join(h.root, "state.json")); !os.IsNotExist(err) {
		t.Error("nothing should have been recorded")
	}
}

func TestInstallAgentFlagNeverPromptsForAgents(t *testing.T) {
	h := newHarness(t)
	url, _ := singleSkillRepo(t)
	h.picker.on, h.picker.choose = true, picks(1)

	out, err := h.run(t, "install", url, "--agent", "claude")
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if len(h.picker.asked.Items) != 0 {
		t.Error("naming -a should never open the agent picker")
	}
	if !linked(t, h, "demo-skill") {
		t.Error("the named agent should be linked into")
	}
}

// h.picker.on defaults to false in newHarness, so a bare install without a
// terminal falls back to the old silent "every present agent" behaviour —
// the exact default this feature changes when a terminal is available.
func TestInstallNonInteractiveFallsBackToEveryPresentAgent(t *testing.T) {
	h := newHarness(t)
	url, _ := singleSkillRepo(t)

	out, err := h.run(t, "install", url)
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if len(h.picker.asked.Items) != 0 {
		t.Error("there was nobody to ask, so the picker should never have been shown")
	}
	if !linked(t, h, "demo-skill") {
		t.Error("claude is present, so it should be linked")
	}
	if _, err := os.Lstat(filepath.Join(h.codex, "demo-skill")); err != nil {
		t.Error("codex is present, so it should also be linked")
	}
}

func TestLinkOffersAgentPickerWhenAgentIsOmitted(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		t.Fatal(err)
	}
	h.picker.on, h.picker.choose = true, picks(1) // codex only

	out, err := h.run(t, "link", dir)
	if err != nil {
		t.Fatalf("link: %v\n%s", err, out)
	}
	if linked(t, h, "demo-skill") {
		t.Error("claude was not picked, so it should not be linked")
	}
	if _, err := os.Lstat(filepath.Join(h.codex, "demo-skill")); err != nil {
		t.Error("codex was picked, so it should be linked")
	}
}

func TestAdoptOffersAgentPickerWhenAgentIsOmitted(t *testing.T) {
	h := newHarness(t)
	dir := localDir(t, nil)
	handLink(t, h.codex, "my-skill", dir)
	h.picker.on, h.picker.choose = true, picks(1) // codex only, where the symlink actually is

	out, err := h.run(t, "adopt")
	if err != nil {
		t.Fatalf("adopt: %v\n%s", err, out)
	}
	if !strings.Contains(out, "adopted 1 skill") {
		t.Errorf("output = %q, want it to say what it adopted", out)
	}
}
