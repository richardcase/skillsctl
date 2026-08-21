package cli

import (
	"os"
	"path/filepath"
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

	// The receipt is only half the story: the point of a rollback is that the
	// agent now reads the old revision, which is the symlink's target.
	for name, dir := range map[string]string{"claude": h.claude, "codex": h.codex} {
		if link := readlink(t, filepath.Join(dir, "demo-skill")); !strings.HasSuffix(link, first) {
			t.Errorf("%s links to %q, want the first commit's revision %s", name, link, first)
		}
	}
}

func TestRollbackRefusesASkillEditedThroughItsSymlink(t *testing.T) {
	h := newHarness(t)
	dir, first := installed(t, h)
	testrepo.Commit(t, dir, map[string]string{"SKILL.md": skillMD + "\nMore.\n"})
	if out, err := h.run(t, "update"); err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}

	link := filepath.Join(h.claude, "demo-skill")
	edited := readlink(t, link)
	if err := os.WriteFile(filepath.Join(link, "SKILL.md"), []byte(skillMD+"\nEdited by hand.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out := exitCode(t, "rollback", "demo-skill")
	if code != ExitError {
		t.Errorf("exit = %d, want %d\n%s", code, ExitError, out)
	}
	if !strings.Contains(out, "--force") {
		t.Errorf("the skip should name the remedy:\n%s", out)
	}
	if got := readlink(t, link); got != edited {
		t.Errorf("the symlink moved to %q despite the skip", got)
	}

	out, err := h.run(t, "rollback", "demo-skill", "--force")
	if err != nil {
		t.Fatalf("rollback --force: %v\n%s", err, out)
	}
	if got := readlink(t, link); !strings.HasSuffix(got, first) {
		t.Errorf("--force left the symlink at %q, want the first commit's revision %s", got, first)
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
	if got.PreviousResolved != first {
		t.Errorf("PreviousResolved = %q, want the toggle to remember %q", got.PreviousResolved, first)
	}
	if link := readlink(t, filepath.Join(h.claude, "demo-skill")); !strings.HasSuffix(link, second) {
		t.Errorf("the symlink is at %q, want the toggle to land back on %s", link, second)
	}
}

// TestLifecycleInstallUpdateDiffRollback walks the whole lifecycle these
// commands were added for, in the order a user meets them: what changed, undo
// it, redo it. Each step is asserted on the symlink as well as the receipt,
// since the symlink is what an agent actually reads.
func TestLifecycleInstallUpdateDiffRollback(t *testing.T) {
	h := newHarness(t)
	dir, first := installed(t, h)
	link := filepath.Join(h.claude, "demo-skill")
	if got := readlink(t, link); !strings.HasSuffix(got, first) {
		t.Fatalf("install linked %q, want the first commit's revision %s", got, first)
	}

	second := testrepo.Commit(t, dir, map[string]string{"SKILL.md": skillMD + "\nMore.\n"})
	if out, err := h.run(t, "update"); err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	if got := readlink(t, link); !strings.HasSuffix(got, second) {
		t.Fatalf("update linked %q, want the second commit's revision %s", got, second)
	}

	out, err := h.run(t, "diff", "demo-skill", "--against", "previous")
	if err != nil {
		t.Fatalf("diff --against previous: %v\n%s", err, out)
	}
	if !strings.Contains(out, "More.") {
		t.Errorf("diff did not show what rollback would undo:\n%s", out)
	}

	if out, err := h.run(t, "rollback", "demo-skill"); err != nil {
		t.Fatalf("rollback: %v\n%s", err, out)
	}
	if got := readlink(t, link); !strings.HasSuffix(got, first) {
		t.Errorf("rollback linked %q, want the first commit's revision %s", got, first)
	}

	if out, err := h.run(t, "rollback", "demo-skill"); err != nil {
		t.Fatalf("second rollback: %v\n%s", err, out)
	}
	if got := readlink(t, link); !strings.HasSuffix(got, second) {
		t.Errorf("the second rollback linked %q, want the toggle back to %s", got, second)
	}

	// Back at the tip of the tracked ref, so there is nothing left to update
	// to and diff has nothing to show.
	out, err = h.run(t, "diff", "demo-skill")
	if err != nil {
		t.Fatalf("diff: %v\n%s", err, out)
	}
	if !strings.Contains(out, "no changes") {
		t.Errorf("diff should report nothing pending at the tip:\n%s", out)
	}
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
