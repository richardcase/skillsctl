package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/testrepo"
)

// installIntoClaudeOnly is the situation the command exists for: a skill
// installed when only one agent was on the machine.
func installIntoClaudeOnly(t *testing.T, h *harness) string {
	t.Helper()

	url, sha := testrepo.New(t, map[string]string{"SKILL.md": skillMD})
	if out, err := h.run(t, "install", url, "-a", "claude"); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	return sha
}

// linkTargets reads the agents a receipt records, which is the removal
// contract and so the only thing worth asserting about a link.
func linkTargets(t *testing.T, h *harness, name string) []string {
	t.Helper()

	links, ok := h.receipts(t)[name]["links"].([]any)
	if !ok {
		t.Fatalf("receipt %s has no links", name)
	}
	out := make([]string, 0, len(links))
	for _, l := range links {
		out = append(out, l.(map[string]any)["target"].(string))
	}
	return out
}

func TestLinkAddsAnInstalledSkillToASecondAgent(t *testing.T) {
	h := newHarness(t)
	sha := installIntoClaudeOnly(t, h)

	if _, err := os.Lstat(filepath.Join(h.codex, "demo-skill")); err == nil {
		t.Fatal("codex already has the skill, so this test proves nothing")
	}

	out, err := h.run(t, "link", "demo-skill", "-a", "codex")
	if err != nil {
		t.Fatalf("link: %v\n%s", err, out)
	}
	if !strings.Contains(out, "linked demo-skill into codex") {
		t.Errorf("output = %q, want it to name the skill and the agent", out)
	}

	dest, err := os.Readlink(filepath.Join(h.codex, "demo-skill"))
	if err != nil {
		t.Fatalf("codex link missing: %v", err)
	}
	if !strings.Contains(dest, sha) {
		t.Errorf("codex link target = %q, want the revision the receipt already had", dest)
	}

	// Both agents point at one revision directory: linking adds a link, it does
	// not fetch a second copy.
	claudeDest, err := os.Readlink(filepath.Join(h.claude, "demo-skill"))
	if err != nil {
		t.Fatalf("claude link missing: %v", err)
	}
	if claudeDest != dest {
		t.Errorf("claude points at %q and codex at %q, want one revision", claudeDest, dest)
	}

	if got := linkTargets(t, h, "demo-skill"); len(got) != 2 {
		t.Errorf("links = %v, want claude and codex", got)
	}

	listed, err := h.run(t, "list")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, listed)
	}
	if !strings.Contains(listed, "claude") || !strings.Contains(listed, "codex") {
		t.Errorf("list = %q, want both agents", listed)
	}
}

// The issue asks for link and remove -a to be symmetric. Doing one then the
// other has to leave the receipt exactly where it started.
func TestLinkThenRemoveFromThatAgentIsSymmetric(t *testing.T) {
	h := newHarness(t)
	installIntoClaudeOnly(t, h)
	before := linkTargets(t, h, "demo-skill")

	if out, err := h.run(t, "link", "demo-skill", "-a", "codex"); err != nil {
		t.Fatalf("link: %v\n%s", err, out)
	}
	if out, err := h.run(t, "remove", "demo-skill", "-a", "codex"); err != nil {
		t.Fatalf("remove: %v\n%s", err, out)
	}

	if got := linkTargets(t, h, "demo-skill"); len(got) != len(before) || got[0] != before[0] {
		t.Errorf("links = %v, want back to %v", got, before)
	}
	if _, err := os.Lstat(filepath.Join(h.codex, "demo-skill")); !os.IsNotExist(err) {
		t.Errorf("codex link still there (%v), want it gone", err)
	}

	// And removing the last link still forgets the receipt, which is the half
	// of the contract linking must not have disturbed.
	if out, err := h.run(t, "remove", "demo-skill", "-a", "claude"); err != nil {
		t.Fatalf("remove: %v\n%s", err, out)
	}
	if _, ok := h.receipts(t)["demo-skill"]; ok {
		t.Error("receipt survived the removal of its last link, want it forgotten")
	}
}

// No -a means every present agent, the same default remove and install use.
//
// It exits 0 even though one of those agents already had the skill: every
// receipt has at least one link, so counting the default set's existing links
// as skipped work would make this form exit 2 every time it was used.
func TestLinkWithNoAgentFansOutToEveryPresentAgent(t *testing.T) {
	h := newHarness(t)
	installIntoClaudeOnly(t, h)

	code, out := exitCode(t, "link", "demo-skill")
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitOK, out)
	}
	if got := linkTargets(t, h, "demo-skill"); len(got) != 2 {
		t.Errorf("links = %v, want both present agents", got)
	}
	if _, err := os.Readlink(filepath.Join(h.codex, "demo-skill")); err != nil {
		t.Errorf("codex link missing: %v", err)
	}
	// The success line must name only the agent the plan actually touched.
	// Naming claude too, which already had it, would be a lie no one asked
	// for, since the default set folds an already-satisfied agent in
	// silently rather than reporting it.
	if !strings.Contains(out, "linked demo-skill into codex") {
		t.Errorf("output = %q, want the success line to name only codex", out)
	}
	if strings.Contains(out, "linked demo-skill into claude") ||
		strings.Contains(out, "linked demo-skill into codex, claude") ||
		strings.Contains(out, "linked demo-skill into claude, codex") {
		t.Errorf("output = %q, want claude left out of the success line: it was not touched", out)
	}
}

// Nothing was done and the message says why, which is exit 1 rather than a
// partial result.
func TestLinkIntoAnAgentThatAlreadyHasItDoesNothing(t *testing.T) {
	h := newHarness(t)
	installIntoClaudeOnly(t, h)

	code, out := exitCode(t, "link", "demo-skill", "-a", "claude")
	if code != ExitError {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitError, out)
	}
	if !strings.Contains(out, "already linked into claude") {
		t.Errorf("output = %q, want it to say which agent already has it", out)
	}
}

// Half the work is still work: the new link is made, the one that was already
// there is reported, and the code says the difference.
func TestLinkDoesTheRestWhenOneAgentAlreadyHasIt(t *testing.T) {
	h := newHarness(t)
	installIntoClaudeOnly(t, h)

	code, out := exitCode(t, "link", "demo-skill", "-a", "claude,codex")
	if code != ExitPartial {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitPartial, out)
	}
	if !strings.Contains(out, "already linked into claude") {
		t.Errorf("output = %q, want the agent that was skipped named", out)
	}
	if _, err := os.Readlink(filepath.Join(h.codex, "demo-skill")); err != nil {
		t.Errorf("codex link missing: %v, want the rest of the work done", err)
	}
	// The two lines must not contradict each other: claude is "already
	// linked" and must not also appear in "linked into", or the output
	// claims to have done work it did not do.
	if !strings.Contains(out, "linked demo-skill into codex") {
		t.Errorf("output = %q, want the success line to name only the agent actually touched", out)
	}
	if strings.Contains(out, "linked demo-skill into claude") ||
		strings.Contains(out, "linked demo-skill into codex, claude") ||
		strings.Contains(out, "linked demo-skill into claude, codex") {
		t.Errorf("output = %q, want claude left out of the success line: it was already linked, not touched", out)
	}
}

func TestLinkDryRunChangesNothing(t *testing.T) {
	h := newHarness(t)
	installIntoClaudeOnly(t, h)

	out, err := h.run(t, "link", "demo-skill", "-a", "codex", "--dry-run")
	if err != nil {
		t.Fatalf("link --dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "link") || !strings.Contains(out, "record") {
		t.Errorf("output = %q, want the Link and Record ops", out)
	}
	if _, err := os.Lstat(filepath.Join(h.codex, "demo-skill")); !os.IsNotExist(err) {
		t.Errorf("codex link exists (%v) after a dry run", err)
	}
	if got := linkTargets(t, h, "demo-skill"); len(got) != 1 {
		t.Errorf("links = %v, want the receipt untouched", got)
	}
}

// The argument is classified by asking the receipts, so a name that is neither
// installed nor a path has to name both readings.
func TestLinkReportsAnArgumentThatIsNeitherASkillNorAPath(t *testing.T) {
	h := newHarness(t)

	out, err := h.run(t, "link", "no-such-skill")
	if err == nil {
		t.Fatalf("link: nil error, want a refusal\n%s", out)
	}
	for _, want := range []string{"no-such-skill", "not installed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to mention %q", err, want)
		}
	}
}

// --skill, --all and --as belong to the path form. A receipt's name is the
// same in every agent, so there is nothing for --as to rename.
func TestLinkRejectsPathFormFlagsOnAnInstalledSkill(t *testing.T) {
	h := newHarness(t)
	installIntoClaudeOnly(t, h)

	for _, flag := range [][]string{{"--as", "other"}, {"--all"}, {"--skill", "demo-skill"}} {
		t.Run(flag[0], func(t *testing.T) {
			args := append([]string{"link", "demo-skill", "-a", "codex"}, flag...)
			out, err := h.run(t, args...)
			if err == nil {
				t.Fatalf("link %v: nil error, want a refusal\n%s", flag, out)
			}
			if !strings.Contains(err.Error(), flag[0]) {
				t.Errorf("error = %v, want it to name %s", err, flag[0])
			}
		})
	}
}

// The path form is reached by every argument that is not an installed skill,
// so the dispatch must not have moved it. local_test.go covers the behaviour;
// this covers the fork.
func TestLinkStillTakesAPathWhenAnInstalledSkillSharesTheDirectoryName(t *testing.T) {
	h := newHarness(t)
	installIntoClaudeOnly(t, h)

	dir := filepath.Join(t.TempDir(), "demo-skill")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	local := "---\nname: local-demo\ndescription: A local demo\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(local), 0o644); err != nil {
		t.Fatal(err)
	}

	// "./…/demo-skill" is a path even though "demo-skill" is an installed name.
	out, err := h.run(t, "link", dir, "-a", "codex")
	if err != nil {
		t.Fatalf("link path: %v\n%s", err, out)
	}
	if _, ok := h.receipts(t)["local-demo"]; !ok {
		t.Errorf("receipts = %v, want the path form to have installed local-demo", h.receipts(t))
	}
}

// A partial result is a note rather than a failure, so it must not be mistaken
// for one by anything that inspects the error.
func TestLinkPartialResultIsAPartialError(t *testing.T) {
	h := newHarness(t)
	installIntoClaudeOnly(t, h)

	_, err := h.run(t, "link", "demo-skill", "-a", "claude,codex")
	var partial *PartialError
	if !errors.As(err, &partial) {
		t.Errorf("error is %T (%v), want a PartialError so the run exits %d", err, err, ExitPartial)
	}
}
