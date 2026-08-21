package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/prompt"
	"github.com/richardcase/skillsctl/internal/testrepo"
)

// browseChoose builds a fakePicker.choose that answers the two Selects
// `browse` makes in turn: the multi-select of skills (Single == false) gets
// skillIdx, and the single-select of an action (Single == true) gets
// []int{actionIdx} (0 == update, 1 == remove).
func browseChoose(skillIdx []int, actionIdx int) func(prompt.Options) ([]int, error) {
	return func(opts prompt.Options) ([]int, error) {
		if !opts.Single {
			return skillIdx, nil
		}
		return []int{actionIdx}, nil
	}
}

func TestBrowseUpdatesSelectedSkills(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})
	if out, err := h.run(t, "install", url); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	sha := testrepo.Commit(t, testrepo.Dir(url), map[string]string{"NOTES.md": "moved on\n"})

	h.picker.on = true
	h.picker.choose = browseChoose([]int{0}, 0) // the one skill, then "Update selected"

	out, err := h.run(t, "browse")
	if err != nil {
		t.Fatalf("browse: %v\n%s", err, out)
	}
	if !strings.Contains(out, "updated demo-skill") {
		t.Errorf("output = %q, want it to report the update", out)
	}

	r := h.receipts(t)["demo-skill"]
	if r["resolved"] != sha {
		t.Errorf("resolved = %v, want %v", r["resolved"], sha)
	}
}

func TestBrowseRemovesSelectedSkills(t *testing.T) {
	h := newHarness(t)
	urlA, _ := testrepo.New(t, map[string]string{"SKILL.md": "---\nname: alpha\n---\n"})
	urlB, _ := testrepo.New(t, map[string]string{"SKILL.md": "---\nname: beta\n---\n"})
	if out, err := h.run(t, "install", urlA); err != nil {
		t.Fatalf("install alpha: %v\n%s", err, out)
	}
	if out, err := h.run(t, "install", urlB); err != nil {
		t.Fatalf("install beta: %v\n%s", err, out)
	}

	// Receipts sort by name (List), so alpha is index 0 and beta is index 1.
	h.picker.on = true
	h.picker.choose = browseChoose([]int{0}, 1) // alpha, then "Remove selected"

	out, err := h.run(t, "browse")
	if err != nil {
		t.Fatalf("browse: %v\n%s", err, out)
	}
	if !strings.Contains(out, "removed alpha") {
		t.Errorf("output = %q, want it to report the removal", out)
	}

	receipts := h.receipts(t)
	if _, ok := receipts["alpha"]; ok {
		t.Error("alpha should have been removed")
	}
	if _, ok := receipts["beta"]; !ok {
		t.Error("beta should not have been touched")
	}
}

func TestBrowseCancellingTheSkillPickerChangesNothing(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})
	if out, err := h.run(t, "install", url); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	h.picker.on = true
	h.picker.choose = nil // fakePicker.Select returns ErrCancelled

	if _, err := h.run(t, "browse"); err == nil {
		t.Fatal("browse succeeded despite the picker being cancelled")
	}
	if _, ok := h.receipts(t)["demo-skill"]; !ok {
		t.Error("a cancelled browse must not touch the receipt")
	}
}

func TestBrowseWithNobodyToAskRefuses(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})
	if out, err := h.run(t, "install", url); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	// h.picker.on defaults to false: nobody to ask.
	out, err := h.run(t, "browse")
	if err == nil {
		t.Fatalf("browse ran with no interactive picker\n%s", out)
	}
	if !strings.Contains(err.Error(), "skillsctl update") || !strings.Contains(err.Error(), "skillsctl remove") {
		t.Errorf("error = %v, want it to name update and remove directly", err)
	}
}

func TestBrowseWithNothingInstalled(t *testing.T) {
	h := newHarness(t)
	h.picker.on = true

	out, err := h.run(t, "browse")
	if err != nil {
		t.Fatalf("browse: %v\n%s", err, out)
	}
	if !strings.Contains(out, "No skills installed") {
		t.Errorf("output = %q, want the empty-store message", out)
	}
}

func TestBrowseDryRunChangesNothing(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})
	if out, err := h.run(t, "install", url); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	h.picker.on = true
	h.picker.choose = browseChoose([]int{0}, 1) // the skill, then "Remove selected"

	out, err := h.run(t, "browse", "--dry-run")
	if err != nil {
		t.Fatalf("browse --dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "unlink") {
		t.Errorf("output = %q, want the dry-run plan for the remove", out)
	}
	if _, err := os.Lstat(filepath.Join(h.claude, "demo-skill")); err != nil {
		t.Error("dry run removed the link")
	}
	if _, ok := h.receipts(t)["demo-skill"]; !ok {
		t.Error("dry run removed the receipt")
	}
}
