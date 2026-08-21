package cli

import (
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/testrepo"
)

func TestDiffAgainstLatestShowsWhatUpdateWouldChange(t *testing.T) {
	h := newHarness(t)
	dir, _ := installed(t, h)
	testrepo.Commit(t, dir, map[string]string{"SKILL.md": skillMD + "\nMore.\n"})

	out, err := h.run(t, "diff", "demo-skill")
	if err != nil {
		t.Fatalf("diff: %v\n%s", err, out)
	}
	if !strings.Contains(out, "More.") {
		t.Errorf("diff missing the pending change:\n%s", out)
	}
}

func TestDiffWithNoChangesSaysSo(t *testing.T) {
	h := newHarness(t)
	installed(t, h)

	out, err := h.run(t, "diff", "demo-skill")
	if err != nil {
		t.Fatalf("diff: %v\n%s", err, out)
	}
	if !strings.Contains(out, "no changes") {
		t.Errorf("diff should say there is nothing to show:\n%s", out)
	}
}

func TestDiffAgainstPreviousShowsWhatRollbackWouldUndo(t *testing.T) {
	h := newHarness(t)
	dir, _ := installed(t, h)
	testrepo.Commit(t, dir, map[string]string{"SKILL.md": skillMD + "\nMore.\n"})
	if out, err := h.run(t, "update"); err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}

	out, err := h.run(t, "diff", "demo-skill", "--against", "previous")
	if err != nil {
		t.Fatalf("diff --against previous: %v\n%s", err, out)
	}
	if !strings.Contains(out, "More.") {
		t.Errorf("diff missing the change rollback would undo:\n%s", out)
	}
}

func TestDiffAgainstPreviousRefusesASkillThatHasNeverBeenUpdated(t *testing.T) {
	h := newHarness(t)
	installed(t, h)

	code, out := exitCode(t, "diff", "demo-skill", "--against", "previous")
	if code != ExitError {
		t.Errorf("exit = %d, want %d\n%s", code, ExitError, out)
	}
	if !strings.Contains(out, "never been updated") {
		t.Errorf("the error should say the skill has never been updated:\n%s", out)
	}
}

func TestDiffRejectsAnUnknownAgainstValue(t *testing.T) {
	h := newHarness(t)
	installed(t, h)

	code, out := exitCode(t, "diff", "demo-skill", "--against", "yesterday")
	if code != ExitError {
		t.Errorf("exit = %d, want %d\n%s", code, ExitError, out)
	}
	if !strings.Contains(out, "yesterday") {
		t.Errorf("the error should name the value it rejected:\n%s", out)
	}
}

func TestDiffReportsANameThatIsNotInstalled(t *testing.T) {
	newHarness(t)

	code, out := exitCode(t, "diff", "never-installed")
	if code != ExitError {
		t.Errorf("exit = %d, want %d\n%s", code, ExitError, out)
	}
	if !strings.Contains(out, "never-installed") {
		t.Errorf("the error should name the skill:\n%s", out)
	}
}
