package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/doctor"
)

// healthy leaves one skill installed into both agents, with nothing wrong.
func healthy(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	installed(t, h)
	return h
}

func TestDoctorOnAHealthyStoreExitsZero(t *testing.T) {
	healthy(t)

	code, out := exitCode(t, "doctor")
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d for a store nothing is wrong with\n%s", code, ExitOK, out)
	}
	if !strings.Contains(out, "Nothing wrong") {
		t.Errorf("a clean report should say so, got:\n%s", out)
	}
}

func TestDoctorOnAnEmptyStoreExitsZero(t *testing.T) {
	newHarness(t)

	code, out := exitCode(t, "doctor")
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d with nothing installed\n%s", code, ExitOK, out)
	}
}

func TestDoctorReportsALinkDeletedByHand(t *testing.T) {
	h := healthy(t)
	if err := os.Remove(filepath.Join(h.codex, "demo-skill")); err != nil {
		t.Fatal(err)
	}

	code, out := exitCode(t, "doctor")
	if code != ExitUnhealthy {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitUnhealthy, out)
	}
	if !strings.Contains(out, "missing links") {
		t.Errorf("want the finding grouped under a heading:\n%s", out)
	}
	if !strings.Contains(out, "skillsctl update demo-skill") {
		t.Errorf("every finding names the command that repairs it:\n%s", out)
	}
	// doctor ran to completion; what it found is the answer, not a failure.
	if !strings.Contains(out, "note:") || strings.Contains(out, "error:") {
		t.Errorf("a finding should be reported as a note:\n%s", out)
	}
}

func TestDoctorReportsARevisionNoReceiptReferences(t *testing.T) {
	h := healthy(t)
	if out, err := h.run(t, "remove", "demo-skill"); err != nil {
		t.Fatalf("remove: %v\n%s", err, out)
	}

	code, out := exitCode(t, "doctor")
	if code != ExitUnhealthy {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitUnhealthy, out)
	}
	if !strings.Contains(out, "orphan revisions") || !strings.Contains(out, "skillsctl gc") {
		t.Errorf("want the orphaned revision reported, with gc as the remedy:\n%s", out)
	}
	// It is about the store, not a skill, so the summary must not claim one.
	if strings.Contains(out, "in 1 skill") {
		t.Errorf("an orphan revision implicates no skill:\n%s", out)
	}
}

func TestDoctorReportsASkillEditedThroughItsSymlink(t *testing.T) {
	h := healthy(t)
	rev := h.revDir(t, "demo-skill")
	if err := os.WriteFile(filepath.Join(rev, "SKILL.md"), []byte(skillMD+"\nEdited.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out := exitCode(t, "doctor")
	if code != ExitUnhealthy {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitUnhealthy, out)
	}
	if !strings.Contains(out, "edited since install") || !strings.Contains(out, "--force") {
		t.Errorf("want the edit reported with the way to discard it:\n%s", out)
	}
}

// An agent that cannot be read leaves the report covering only part of what was
// asked, which outranks the findings: 2 rather than 4.
func TestDoctorIsPartialWhenAnAgentCannotBeScanned(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions, so nothing can be made unreadable")
	}
	h := healthy(t)
	if err := os.Remove(filepath.Join(h.claude, "demo-skill")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(h.codex, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(h.codex, 0o755) })

	code, out := exitCode(t, "doctor")
	if code != ExitPartial {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitPartial, out)
	}
	if !strings.Contains(out, "could not be scanned") {
		t.Errorf("the report should say what it could not read:\n%s", out)
	}
	if !strings.Contains(out, "missing links") {
		t.Errorf("the agent that could be scanned should still be reported on:\n%s", out)
	}
}

func TestDoctorWritesItsWholeReportToStdout(t *testing.T) {
	h := healthy(t)

	// A clean report still has to land on stdout: `skillsctl doctor > health`
	// should not produce an empty file.
	stdout, stderr, err := h.runSplit(t, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "Nothing wrong") {
		t.Errorf("the clean message is not on stdout:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if stderr != "" {
		t.Errorf("doctor wrote to stderr on success:\n%s", stderr)
	}

	if err := os.Remove(filepath.Join(h.codex, "demo-skill")); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, _ = h.runSplit(t, "doctor")
	for _, want := range []string{"missing links", "demo-skill", "fix:", "skillsctl update"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout is missing %q:\nstdout:\n%s\nstderr:\n%s", want, stdout, stderr)
		}
	}
}

func TestDoctorJSON(t *testing.T) {
	h := healthy(t)
	if err := os.Remove(filepath.Join(h.codex, "demo-skill")); err != nil {
		t.Fatal(err)
	}

	stdout, _, _ := h.runSplit(t, "doctor", "--json")

	var rep doctor.Report
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatalf("doctor --json emitted invalid JSON: %v\n%s", err, stdout)
	}
	if len(rep.Findings) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(rep.Findings), rep.Findings)
	}
	got := rep.Findings[0]
	if got.Kind != doctor.KindMissingLink || got.Name != "demo-skill" || got.Target != "codex" {
		t.Errorf("finding = %+v, want a missing link for demo-skill in codex", got)
	}
	if got.Remedy == "" {
		t.Errorf("finding = %+v, want the remedy carried in the JSON too", got)
	}
}

// A clean --json report has to be usable without a null check, so `jq
// '.findings[]'` says nothing rather than failing.
func TestDoctorJSONOnAHealthyStoreHasAnEmptyFindingsArray(t *testing.T) {
	h := healthy(t)

	stdout, stderr, err := h.runSplit(t, "doctor", "--json")
	if err != nil {
		t.Fatalf("doctor --json: %v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, `"findings": []`) {
		t.Errorf("want an empty findings array, got:\n%s", stdout)
	}
	if stderr != "" {
		t.Errorf("doctor wrote to stderr on success:\n%s", stderr)
	}
}

// doctor only reports. Nothing it can find is repaired, moved or deleted, which
// is what makes it safe to run anywhere.
func TestDoctorChangesNothing(t *testing.T) {
	h := healthy(t)

	// Damage of several kinds at once, so no branch of the scan is untested.
	if err := os.Remove(filepath.Join(h.codex, "demo-skill")); err != nil {
		t.Fatal(err)
	}
	rev := h.revDir(t, "demo-skill")
	if err := os.WriteFile(filepath.Join(rev, "SKILL.md"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(h.root, "rev", "example.com", "o", "stray", strings.Repeat("ab", 20))
	if err := os.MkdirAll(stray, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stray, "SKILL.md"), []byte("stray\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	before := tree(t, h.root, h.claude, h.codex)
	code, out := exitCode(t, "doctor")
	if code != ExitUnhealthy {
		t.Fatalf("exit = %d, want %d; the fixture is damaged\n%s", code, ExitUnhealthy, out)
	}
	if after := tree(t, h.root, h.claude, h.codex); after != before {
		t.Errorf("doctor changed something:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// tree renders every path under each root with its mode and size, so a test can
// assert that nothing moved, grew or vanished.
func tree(t *testing.T, roots ...string) string {
	t.Helper()
	var b strings.Builder
	for _, root := range roots {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				return rerr
			}
			fmt.Fprintf(&b, "%s %s %d\n", rel, info.Mode(), info.Size())
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return b.String()
}
