package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/lint"
)

func writeSkill(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLintOnAValidSkillExitsZero(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "demo-skill")
	writeSkill(t, dir, skillMD)

	code, out := exitCode(t, "lint", dir)
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitOK, out)
	}
	if !strings.Contains(out, "Nothing wrong") {
		t.Errorf("a clean lint should say so, got:\n%s", out)
	}
}

func TestLintReportsAnEmptyDescriptionAndExitsUnhealthy(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "demo-skill")
	writeSkill(t, dir, "---\nname: demo-skill\n---\n\nBody.\n")

	code, out := exitCode(t, "lint", dir)
	if code != ExitUnhealthy {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitUnhealthy, out)
	}
	if !strings.Contains(out, "description is empty") {
		t.Errorf("want the empty description reported:\n%s", out)
	}
	if !strings.Contains(out, "note:") {
		t.Errorf("lint findings should be reported as a note:\n%s", out)
	}
}

func TestLintReportsAMismatchAsAWarningWithoutFailingExit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "demo-skill")
	writeSkill(t, dir, "---\nname: other-name\ndescription: A demo.\n---\n\nBody.\n")

	code, out := exitCode(t, "lint", dir)
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d: a naming mismatch alone should not fail\n%s", code, ExitOK, out)
	}
	if !strings.Contains(out, `does not match directory`) {
		t.Errorf("want the mismatch reported:\n%s", out)
	}
}

func TestLintWalksADirectoryOfSkills(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, filepath.Join(root, "alpha"), "---\nname: alpha\ndescription: Alpha.\n---\n\nBody.\n")
	writeSkill(t, filepath.Join(root, "beta"), "---\nname: beta\n---\n\nBody.\n")

	code, out := exitCode(t, "lint", root)
	if code != ExitUnhealthy {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitUnhealthy, out)
	}
	if !strings.Contains(out, "beta") || !strings.Contains(out, "description is empty") {
		t.Errorf("want beta's missing description reported:\n%s", out)
	}
	if strings.Contains(out, "alpha") {
		t.Errorf("alpha is valid and should not be mentioned:\n%s", out)
	}
}

func TestLintOnAPathWithNoSkillMDIsAnError(t *testing.T) {
	dir := t.TempDir()

	code, out := exitCode(t, "lint", dir)
	if code != ExitError {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitError, out)
	}
	if !strings.Contains(out, "error:") {
		t.Errorf("want an error prefix:\n%s", out)
	}
}

func TestLintJSON(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "demo-skill")
	writeSkill(t, dir, "---\nname: demo-skill\n---\n\nBody.\n")

	var stdout, stderr strings.Builder
	root := NewRootCmd()
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"lint", dir, "--json"})
	_ = run(root)

	var findings []lint.Finding
	if err := json.Unmarshal([]byte(stdout.String()), &findings); err != nil {
		t.Fatalf("lint --json emitted invalid JSON: %v\n%s", err, stdout.String())
	}
	if len(findings) != 1 || findings[0].Severity != lint.Error {
		t.Errorf("findings = %+v, want exactly one error finding", findings)
	}
}

func TestLintChangesNothing(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, filepath.Join(root, "alpha"), "---\nname: alpha\ndescription: Alpha.\n---\n\nBody.\n")

	before := tree(t, root)
	exitCode(t, "lint", root)
	if after := tree(t, root); after != before {
		t.Errorf("lint changed something:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
