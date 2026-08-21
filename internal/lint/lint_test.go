package lint

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, dir, skillMD string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPathValidSkillHasNoFindings(t *testing.T) {
	dir := write(t, filepath.Join(t.TempDir(), "foo"), "---\nname: foo\ndescription: A demo skill.\n---\n\nBody.\n")

	got, err := Path(dir)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("findings = %+v, want none", got)
	}
}

func TestPathMissingNameIsError(t *testing.T) {
	dir := write(t, filepath.Join(t.TempDir(), "foo"), "---\ndescription: A demo skill.\n---\n\nBody.\n")

	got, err := Path(dir)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if !hasFinding(got, Error, "name is empty") {
		t.Errorf("findings = %+v, want an error that the name is empty", got)
	}
}

func TestPathMissingDescriptionIsError(t *testing.T) {
	dir := write(t, filepath.Join(t.TempDir(), "foo"), "---\nname: foo\n---\n\nBody.\n")

	got, err := Path(dir)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if !hasFinding(got, Error, "description is empty") {
		t.Errorf("findings = %+v, want an error that the description is empty", got)
	}
}

func TestPathNoFrontmatterAtAllIsError(t *testing.T) {
	dir := write(t, filepath.Join(t.TempDir(), "foo"), "# Just a heading\n\nBody.\n")

	got, err := Path(dir)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if !hasFinding(got, Error, "name is empty") || !hasFinding(got, Error, "description is empty") {
		t.Errorf("findings = %+v, want both name and description reported empty", got)
	}
}

func TestPathNameDirectoryMismatchIsWarning(t *testing.T) {
	dir := write(t, filepath.Join(t.TempDir(), "foo"), "---\nname: bar\ndescription: A demo skill.\n---\n\nBody.\n")

	got, err := Path(dir)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if !hasFinding(got, Warning, `name "bar" does not match directory "foo"`) {
		t.Errorf("findings = %+v, want a warning about the name/directory mismatch", got)
	}
	for _, f := range got {
		if f.Severity == Error {
			t.Errorf("findings = %+v, a name/directory mismatch alone should not be an error", got)
		}
	}
}

func TestPathInvalidNameIsError(t *testing.T) {
	dir := write(t, filepath.Join(t.TempDir(), "foo"), "---\nname: bad/name\ndescription: A demo skill.\n---\n\nBody.\n")

	got, err := Path(dir)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	found := false
	for _, f := range got {
		if f.Severity == Error && f.Message != "" {
			found = true
		}
	}
	if !found {
		t.Errorf("findings = %+v, want an error for a name containing a path separator", got)
	}
}

func TestPathMalformedFrontmatterIsFinding(t *testing.T) {
	dir := write(t, filepath.Join(t.TempDir(), "foo"), "---\nname: [unclosed\n")

	got, err := Path(dir)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if len(got) != 1 || got[0].Severity != Error {
		t.Errorf("findings = %+v, want exactly one error for malformed frontmatter", got)
	}
}

func TestPathNoSkillMDIsAnError(t *testing.T) {
	dir := t.TempDir()

	if _, err := Path(dir); err == nil {
		t.Error("Path on an empty directory should return an error, got nil")
	}
}

func TestPathOnANonexistentDirectoryIsAnError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")

	if _, err := Path(dir); err == nil {
		t.Error("Path on a directory that does not exist should return an error, got nil")
	}
}

func TestPathWalksMultipleSkills(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "alpha"), "---\nname: alpha\ndescription: Alpha.\n---\n\nBody.\n")
	write(t, filepath.Join(root, "beta"), "---\nname: beta\n---\n\nBody.\n")

	got, err := Path(root)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if !hasFinding(got, Error, "description is empty") {
		t.Errorf("findings = %+v, want beta's missing description reported", got)
	}
	for _, f := range got {
		if f.Skill == filepath.Join(root, "alpha") {
			t.Errorf("findings = %+v, alpha is valid and should have no findings", got)
		}
	}
}

func TestSeverityString(t *testing.T) {
	if got := Error.String(); got != "error" {
		t.Errorf("Error.String() = %q, want %q", got, "error")
	}
	if got := Warning.String(); got != "warning" {
		t.Errorf("Warning.String() = %q, want %q", got, "warning")
	}
}

func TestSeverityJSONRoundTrips(t *testing.T) {
	for _, sev := range []Severity{Error, Warning} {
		blob, err := json.Marshal(sev)
		if err != nil {
			t.Fatalf("Marshal(%v): %v", sev, err)
		}
		var got Severity
		if err := json.Unmarshal(blob, &got); err != nil {
			t.Fatalf("Unmarshal(%s): %v", blob, err)
		}
		if got != sev {
			t.Errorf("round trip of %v via %s got %v", sev, blob, got)
		}
	}
}

func hasFinding(findings []Finding, sev Severity, msg string) bool {
	for _, f := range findings {
		if f.Severity == sev && f.Message == msg {
			return true
		}
	}
	return false
}
