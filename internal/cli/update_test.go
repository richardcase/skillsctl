package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/testrepo"
)

// installed sets up a repository holding one skill, installs it, and returns
// the repository's working tree and the sha it was installed at.
func installed(t *testing.T, h *harness, args ...string) (dir, sha string) {
	t.Helper()
	url, sha := testrepo.New(t, map[string]string{"SKILL.md": skillMD})
	out, err := h.run(t, append([]string{"install", url}, args...)...)
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	return testrepo.Dir(url), sha
}

// readReceipt returns the receipt for name, failing if it is not there.
func readReceipt(t *testing.T, h *harness, name string) state.Receipt {
	t.Helper()
	blob, err := os.ReadFile(filepath.Join(h.root, "state.json"))
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	var db state.DB
	if err := json.Unmarshal(blob, &db); err != nil {
		t.Fatalf("parse state: %v", err)
	}
	r, ok := db.Receipts[name]
	if !ok {
		t.Fatalf("no receipt for %q", name)
	}
	return *r
}

func readlink(t *testing.T, path string) string {
	t.Helper()
	got, err := os.Readlink(path)
	if err != nil {
		t.Fatalf("Readlink %s: %v", path, err)
	}
	return got
}

func TestUpdateRepointsEveryAgentAndOrphansTheOldRevision(t *testing.T) {
	h := newHarness(t)
	dir, first := installed(t, h)
	before := readReceipt(t, h, "demo-skill")

	second := testrepo.Commit(t, dir, map[string]string{"SKILL.md": skillMD + "\nMore.\n"})

	out, err := h.run(t, "update")
	if err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	if !strings.Contains(out, "updated demo-skill") {
		t.Errorf("update did not report the skill:\n%s", out)
	}

	for name, dir := range map[string]string{"claude": h.claude, "codex": h.codex} {
		got := readlink(t, filepath.Join(dir, "demo-skill"))
		if !strings.HasSuffix(got, second) {
			t.Errorf("%s still links to %q, want the new revision %s", name, got, second)
		}
		if _, err := os.Stat(filepath.Join(got, "SKILL.md")); err != nil {
			t.Errorf("%s links at a revision with no SKILL.md: %v", name, err)
		}
	}

	after := readReceipt(t, h, "demo-skill")
	switch {
	case after.Resolved != second:
		t.Errorf("receipt Resolved = %q, want %q", after.Resolved, second)
	case !after.UpdatedAt.After(before.UpdatedAt):
		t.Error("receipt UpdatedAt did not move")
	case !after.InstalledAt.Equal(before.InstalledAt):
		t.Error("receipt InstalledAt must not move: the skill was installed once")
	case after.ContentHash == before.ContentHash:
		t.Error("receipt ContentHash was not re-computed")
	case len(after.Links) != len(before.Links):
		t.Errorf("receipt has %d links, want the install's %d", len(after.Links), len(before.Links))
	}

	// The old revision is now unreferenced, and gc is what reclaims it.
	if !strings.Contains(out, "run `skillsctl gc` to reclaim") {
		t.Errorf("update did not mention the orphaned revision:\n%s", out)
	}
	oldRev := filepath.Join(h.root, "rev")
	if _, err := os.Stat(before.RevPath); err != nil {
		t.Errorf("update deleted the old revision itself; only gc may do that: %v", err)
	}

	gcOut, err := h.run(t, "gc")
	if err != nil {
		t.Fatalf("gc: %v\n%s", err, gcOut)
	}
	if _, err := os.Stat(before.RevPath); !os.IsNotExist(err) {
		t.Errorf("gc did not reclaim the old revision %s under %s", first, oldRev)
	}
	if _, err := os.Stat(after.RevPath); err != nil {
		t.Errorf("gc reclaimed the revision that is still linked: %v", err)
	}
}

func TestUpdateSkipsASkillEditedThroughItsSymlink(t *testing.T) {
	h := newHarness(t)
	dir, _ := installed(t, h)
	testrepo.Commit(t, dir, map[string]string{"SKILL.md": skillMD + "\nMore.\n"})

	link := filepath.Join(h.claude, "demo-skill")
	edited := readlink(t, link)
	if err := os.WriteFile(filepath.Join(link, "SKILL.md"), []byte(skillMD+"\nEdited by hand.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := h.run(t, "update")
	if err == nil {
		t.Fatalf("update of a dirty skill should not exit 0:\n%s", out)
	}
	// Nothing was updated at all, so this is a failure rather than a partial
	// result: a script must be able to tell the two apart.
	var partial *PartialError
	if errors.As(err, &partial) {
		t.Errorf("error is a PartialError (%v), want a plain error so the run exits %d", err, ExitError)
	}
	if !strings.Contains(out, "--force") {
		t.Errorf("the skip should name the remedy:\n%s", out)
	}
	if got := readlink(t, link); got != edited {
		t.Errorf("the symlink moved to %q despite the skip", got)
	}

	out, err = h.run(t, "update", "--force")
	if err != nil {
		t.Fatalf("update --force: %v\n%s", err, out)
	}
	if got := readlink(t, link); got == edited {
		t.Error("--force did not move the symlink")
	}
}

func TestUpdateDryRunChangesNothing(t *testing.T) {
	h := newHarness(t)
	dir, _ := installed(t, h)
	testrepo.Commit(t, dir, map[string]string{"SKILL.md": skillMD + "\nMore.\n"})

	link := filepath.Join(h.claude, "demo-skill")
	before := readlink(t, link)
	beforeReceipt := readReceipt(t, h, "demo-skill")

	out, err := h.run(t, "update", "--dry-run")
	if err != nil {
		t.Fatalf("update --dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "relink ") || !strings.Contains(out, "record ") {
		t.Errorf("dry run did not describe the plan:\n%s", out)
	}
	if !strings.Contains(out, "would update") {
		t.Errorf("dry run should say it would update, not that it did:\n%s", out)
	}

	if got := readlink(t, link); got != before {
		t.Errorf("dry run moved the symlink to %q", got)
	}
	if after := readReceipt(t, h, "demo-skill"); after.Resolved != beforeReceipt.Resolved {
		t.Errorf("dry run changed the receipt: Resolved = %q", after.Resolved)
	}
}

func TestUpdateSaysWhenEverythingIsCurrent(t *testing.T) {
	h := newHarness(t)
	installed(t, h)

	out, err := h.run(t, "update")
	if err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	if !strings.Contains(out, "up to date") {
		t.Errorf("update should say there was nothing to do:\n%s", out)
	}
}

func TestUpdateSkipsAPinUnlessItIsNamed(t *testing.T) {
	h := newHarness(t)
	dir, first := installed(t, h, "--pin")
	second := testrepo.Commit(t, dir, map[string]string{"SKILL.md": skillMD + "\nMore.\n"})

	out, err := h.run(t, "update")
	if err != nil {
		t.Fatalf("update over a pinned skill should exit 0: %v\n%s", err, out)
	}
	if !strings.Contains(out, "pinned") {
		t.Errorf("update should say why it skipped:\n%s", out)
	}
	if got := readReceipt(t, h, "demo-skill"); got.Resolved != first {
		t.Errorf("a pin was moved without being named: Resolved = %q", got.Resolved)
	}

	out, err = h.run(t, "update", "demo-skill")
	if err != nil {
		t.Fatalf("update demo-skill: %v\n%s", err, out)
	}
	got := readReceipt(t, h, "demo-skill")
	if got.Resolved != second {
		t.Errorf("naming the pin did not update it: Resolved = %q", got.Resolved)
	}
	if !got.Pinned {
		t.Error("updating a pinned skill should re-pin it at the new commit, not unpin it")
	}
}

func TestUpdateReportsOneFailureWithoutAbandoningTheRest(t *testing.T) {
	h := newHarness(t)

	// Two skills in one repository; the second commit deletes one of them.
	url, _ := testrepo.New(t, map[string]string{
		"alpha/SKILL.md": "---\nname: alpha\ndescription: A\n---\n",
		"beta/SKILL.md":  "---\nname: beta\ndescription: B\n---\n",
	})
	if out, err := h.run(t, "install", url, "--all"); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	dir := testrepo.Dir(url)
	if err := os.RemoveAll(filepath.Join(dir, "beta")); err != nil {
		t.Fatal(err)
	}
	testrepo.Commit(t, dir, map[string]string{"alpha/SKILL.md": "---\nname: alpha\ndescription: A2\n---\n"})

	out, err := h.run(t, "update")
	if err == nil {
		t.Fatalf("a partly-skipped update should not exit 0:\n%s", out)
	}
	var partial *PartialError
	if !errors.As(err, &partial) {
		t.Errorf("error is %T (%v), want a PartialError so the run exits %d", err, err, ExitPartial)
	}
	if !strings.Contains(out, "updated alpha") {
		t.Errorf("alpha should still have been updated:\n%s", out)
	}
	if !strings.Contains(out, "skipped beta") {
		t.Errorf("beta should have been reported as skipped:\n%s", out)
	}
	if got := readlink(t, filepath.Join(h.claude, "beta")); !strings.HasSuffix(got, "beta") {
		t.Errorf("beta's symlink was disturbed: %q", got)
	}
}

func TestUpdateRejectsANameThatIsNotInstalled(t *testing.T) {
	h := newHarness(t)
	installed(t, h)

	out, err := h.run(t, "update", "nope")
	if err == nil {
		t.Fatalf("update accepted a name that is not installed:\n%s", out)
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("error %q should name what was not found", err)
	}
}
