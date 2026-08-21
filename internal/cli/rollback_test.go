package cli

import (
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/testrepo"
)

func TestRollbackSwapsBackToThePreviousRevision(t *testing.T) {
	h := newHarness(t)
	dir, first := installed(t, h)
	second := testrepo.Commit(t, dir, map[string]string{"SKILL.md": skillMD + "\nMore.\n"})

	out, err := h.run(t, "update")
	if err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	if got := readReceipt(t, h, "demo-skill"); got.Resolved != second {
		t.Fatalf("fixture: Resolved = %q, want %q", got.Resolved, second)
	}

	out, err = h.run(t, "rollback", "demo-skill")
	if err != nil {
		t.Fatalf("rollback: %v\n%s", err, out)
	}
	if !strings.Contains(out, "rolled back demo-skill to "+first[:7]) {
		t.Errorf("rollback did not report the revision it moved to:\n%s", out)
	}
	got := readReceipt(t, h, "demo-skill")
	if got.Resolved != first {
		t.Errorf("Resolved = %q, want the first commit %q", got.Resolved, first)
	}
	if got.PreviousResolved != second {
		t.Errorf("PreviousResolved = %q, want the toggle to remember %q", got.PreviousResolved, second)
	}
}

func TestRollbackTwiceTogglesBack(t *testing.T) {
	h := newHarness(t)
	dir, first := installed(t, h)
	second := testrepo.Commit(t, dir, map[string]string{"SKILL.md": skillMD + "\nMore.\n"})

	if out, err := h.run(t, "update"); err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	if out, err := h.run(t, "rollback", "demo-skill"); err != nil {
		t.Fatalf("rollback: %v\n%s", err, out)
	}
	if out, err := h.run(t, "rollback", "demo-skill"); err != nil {
		t.Fatalf("second rollback: %v\n%s", err, out)
	}

	got := readReceipt(t, h, "demo-skill")
	if got.Resolved != second {
		t.Errorf("Resolved = %q, want the toggle to land back on %q", got.Resolved, second)
	}
	_ = first
}

func TestRollbackRefusesASkillThatHasNeverBeenUpdated(t *testing.T) {
	h := newHarness(t)
	installed(t, h)

	code, out := exitCode(t, "rollback", "demo-skill")
	if code != ExitError {
		t.Errorf("exit = %d, want %d\n%s", code, ExitError, out)
	}
	if !strings.Contains(out, "nothing to roll back to") {
		t.Errorf("the error should say there is nothing to roll back to:\n%s", out)
	}
}

func TestRollbackReportsANameThatIsNotInstalled(t *testing.T) {
	newHarness(t)

	code, out := exitCode(t, "rollback", "never-installed")
	if code != ExitError {
		t.Errorf("exit = %d, want %d\n%s", code, ExitError, out)
	}
	if !strings.Contains(out, "never-installed") {
		t.Errorf("the error should name the skill:\n%s", out)
	}
}

func TestRollbackDryRunChangesNothing(t *testing.T) {
	h := newHarness(t)
	dir, _ := installed(t, h)
	testrepo.Commit(t, dir, map[string]string{"SKILL.md": skillMD + "\nMore.\n"})
	if out, err := h.run(t, "update"); err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	before := readReceipt(t, h, "demo-skill")

	out, err := h.run(t, "rollback", "demo-skill", "--dry-run")
	if err != nil {
		t.Fatalf("rollback --dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "relink") {
		t.Errorf("a dry run should print the plan:\n%s", out)
	}
	if got := readReceipt(t, h, "demo-skill"); got.Resolved != before.Resolved {
		t.Error("a dry run changed the receipt")
	}
}
