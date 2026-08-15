package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/testrepo"
)

// The whole point of the pair: a pin added after the fact is honoured by
// update, and removing it lets the skill move again — none of which needed a
// remove and reinstall.
func TestPinStopsUpdateAndUnpinLetsItMoveAgain(t *testing.T) {
	h := newHarness(t)
	dir, first := installed(t, h)

	out, err := h.run(t, "pin", "demo-skill")
	if err != nil {
		t.Fatalf("pin: %v\n%s", err, out)
	}
	if !strings.Contains(out, "pinned demo-skill at "+first[:7]) {
		t.Errorf("pin did not report the revision it froze:\n%s", out)
	}

	pinned := readReceipt(t, h, "demo-skill")
	if !pinned.Pinned {
		t.Error("the receipt was not pinned")
	}
	if pinned.Ref != "" {
		t.Errorf("Ref = %q, want it cleared: a pinned receipt tracks nothing", pinned.Ref)
	}
	if pinned.Resolved != first {
		t.Errorf("Resolved = %q, want %q: a pin freezes what is installed", pinned.Resolved, first)
	}

	second := testrepo.Commit(t, dir, map[string]string{"SKILL.md": skillMD + "\nMore.\n"})

	out, err = h.run(t, "update")
	if err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	if !strings.Contains(out, "skipped demo-skill: pinned") {
		t.Errorf("update did not skip the pinned skill:\n%s", out)
	}

	out, err = h.run(t, "unpin", "demo-skill")
	if err != nil {
		t.Fatalf("unpin: %v\n%s", err, out)
	}
	if !strings.Contains(out, "unpinned demo-skill") {
		t.Errorf("unpin did not report what it did:\n%s", out)
	}

	out, err = h.run(t, "update")
	if err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	if !strings.Contains(out, "updated demo-skill") {
		t.Errorf("update did not move the unpinned skill:\n%s", out)
	}
	if got := readReceipt(t, h, "demo-skill"); got.Resolved != second {
		t.Errorf("Resolved = %q, want the new commit %q", got.Resolved, second)
	}
}

// A pin drops the ref it was tracking, so a pin followed by an unpin has to say
// what the skill tracks now rather than leave the user to guess.
func TestPinSaysWhichRefItDropped(t *testing.T) {
	h := newHarness(t)
	installed(t, h, "--ref", "main")

	out, err := h.run(t, "pin", "demo-skill")
	if err != nil {
		t.Fatalf("pin: %v\n%s", err, out)
	}
	if !strings.Contains(out, "no longer tracks main") {
		t.Errorf("pin did not say which ref it dropped:\n%s", out)
	}

	out, err = h.run(t, "unpin", "demo-skill")
	if err != nil {
		t.Fatalf("unpin: %v\n%s", err, out)
	}
	if !strings.Contains(out, "default branch") {
		t.Errorf("unpin did not say what the skill tracks now:\n%s", out)
	}
}

func TestUnpinTracksTheRefItIsGiven(t *testing.T) {
	h := newHarness(t)
	installed(t, h)

	out, err := h.run(t, "pin", "demo-skill")
	if err != nil {
		t.Fatalf("pin: %v\n%s", err, out)
	}

	out, err = h.run(t, "unpin", "demo-skill", "--ref", "main")
	if err != nil {
		t.Fatalf("unpin: %v\n%s", err, out)
	}
	if !strings.Contains(out, "tracks main") {
		t.Errorf("unpin did not name the ref it recorded:\n%s", out)
	}
	if got := readReceipt(t, h, "demo-skill"); got.Ref != "main" {
		t.Errorf("Ref = %q, want main", got.Ref)
	}
}

// A ref that does not resolve would turn the next update into a failure the
// user cannot connect to this command, so it is refused here instead.
func TestUnpinRejectsARefThatDoesNotResolve(t *testing.T) {
	h := newHarness(t)
	installed(t, h)

	if out, err := h.run(t, "pin", "demo-skill"); err != nil {
		t.Fatalf("pin: %v\n%s", err, out)
	}

	out, err := h.run(t, "unpin", "demo-skill", "--ref", "no-such-branch")
	if err == nil {
		t.Fatalf("unpin accepted a ref that does not resolve:\n%s", out)
	}
	if !strings.Contains(out, "no-such-branch") {
		t.Errorf("the error should name the ref:\n%s", out)
	}
	if got := readReceipt(t, h, "demo-skill"); !got.Pinned {
		t.Error("the receipt was unpinned despite the refused ref")
	}
}

func TestPinDryRunChangesNothing(t *testing.T) {
	h := newHarness(t)
	installed(t, h)

	out, err := h.run(t, "pin", "demo-skill", "--dry-run")
	if err != nil {
		t.Fatalf("pin --dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "record  demo-skill") {
		t.Errorf("a dry run should print the plan:\n%s", out)
	}
	for _, verb := range []string{"link", "unlink", "relink", "exec"} {
		if strings.Contains(out, verb+"  ") {
			t.Errorf("a pin plans nothing but a record, and this holds a %s:\n%s", verb, out)
		}
	}
	if got := readReceipt(t, h, "demo-skill"); got.Pinned {
		t.Error("a dry run pinned the receipt")
	}
}

func TestPinRefusesASkillWithNoRevisionToFreeze(t *testing.T) {
	h := newHarness(t)
	dir := localDir(t, nil)

	if out, err := h.run(t, "link", dir); err != nil {
		t.Fatalf("link: %v\n%s", err, out)
	}

	out, err := h.run(t, "pin", "my-skill")
	if err == nil {
		t.Fatalf("pin accepted a local skill:\n%s", out)
	}
	if !strings.Contains(out, "my-skill") {
		t.Errorf("the refusal should name the skill:\n%s", out)
	}
}

// adopt pins a working copy so that a plain update cannot re-point the user's
// symlink into the store. Unpinning it is the user asking for exactly that, so
// it goes through — and says so, because it is the one case where writing a
// receipt field changes what a later command does to files the user owns.
func TestUnpinWarnsWhenTheFilesAreAWorkingCopy(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"skills/demo/SKILL.md": skillMD})
	clone := testrepo.Clone(t, url)
	checkout := filepath.Join(clone, "skills", "demo")
	handLink(t, h.claude, "demo-skill", checkout)

	if out, err := h.run(t, "adopt"); err != nil {
		t.Fatalf("adopt: %v\n%s", err, out)
	}

	out, err := h.run(t, "unpin", "demo-skill")
	if err != nil {
		t.Fatalf("unpin: %v\n%s", err, out)
	}
	if !strings.Contains(out, checkout) {
		t.Errorf("unpin did not name the working copy at %s:\n%s", checkout, out)
	}
	if !strings.Contains(out, "store") {
		t.Errorf("unpin did not say where the next update will move the links:\n%s", out)
	}
}

func TestPinReportsANameThatIsNotInstalled(t *testing.T) {
	newHarness(t)

	code, out := exitCode(t, "pin", "never-installed")
	if code != ExitError {
		t.Errorf("exit = %d, want %d: nothing was pinned\n%s", code, ExitError, out)
	}
	if !strings.Contains(out, "never-installed") {
		t.Errorf("the error should name the skill:\n%s", out)
	}
}

// Pinning several skills where one name is unknown pins the rest: the work
// stands, and the shell still notices.
func TestPinIsPartialWhenOneNameIsUnknown(t *testing.T) {
	h := newHarness(t)
	installed(t, h)

	code, out := exitCode(t, "pin", "demo-skill", "never-installed")
	if code != ExitPartial {
		t.Errorf("exit = %d, want %d\n%s", code, ExitPartial, out)
	}
	if !strings.Contains(out, "pinned demo-skill") {
		t.Errorf("the skill that could be pinned should still be reported:\n%s", out)
	}
	if got := readReceipt(t, h, "demo-skill"); !got.Pinned {
		t.Error("the skill that could be pinned was not")
	}
}

// Asking for the state a skill is already in is not a failure: nothing was
// asked for that could not be done.
func TestPinTwiceIsReportedAndNotAnError(t *testing.T) {
	h := newHarness(t)
	installed(t, h)

	if out, err := h.run(t, "pin", "demo-skill"); err != nil {
		t.Fatalf("pin: %v\n%s", err, out)
	}

	code, out := exitCode(t, "pin", "demo-skill")
	if code != ExitOK {
		t.Errorf("exit = %d, want %d\n%s", code, ExitOK, out)
	}
	if !strings.Contains(out, "already pinned") {
		t.Errorf("pinning twice should say so:\n%s", out)
	}
}
