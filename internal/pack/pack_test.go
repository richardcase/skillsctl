package pack

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func tarNames(t *testing.T, data []byte) []string {
	t.Helper()
	var names []string
	tr := tar.NewReader(bytes.NewReader(data))
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		names = append(names, hdr.Name)
	}
	return names
}

func TestTarIncludesEveryFileAndSubdirectory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "alpha", "SKILL.md"), "---\nname: alpha\n---\n")
	writeFile(t, filepath.Join(dir, "alpha", "scripts", "run.sh"), "#!/bin/sh\n")

	var buf bytes.Buffer
	if err := Tar(&buf, dir); err != nil {
		t.Fatal(err)
	}

	names := tarNames(t, buf.Bytes())
	want := []string{"alpha/", "alpha/SKILL.md", "alpha/scripts/", "alpha/scripts/run.sh"}
	for _, w := range want {
		found := false
		for _, n := range names {
			if n == w {
				found = true
			}
		}
		if !found {
			t.Errorf("tar is missing %q, got %v", w, names)
		}
	}
}

func TestTarExcludesGitDirectoryUnconditionally(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "alpha", "SKILL.md"), "---\nname: alpha\n---\n")
	writeFile(t, filepath.Join(dir, ".git", "HEAD"), "ref: refs/heads/main\n")

	var buf bytes.Buffer
	if err := Tar(&buf, dir); err != nil {
		t.Fatal(err)
	}

	for _, n := range tarNames(t, buf.Bytes()) {
		if n == ".git/" || n == ".git/HEAD" {
			t.Errorf("tar included %q, .git must never be packaged", n)
		}
	}
}

func TestTarHonoursRootGitignore(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "alpha", "SKILL.md"), "---\nname: alpha\n---\n")
	writeFile(t, filepath.Join(dir, "alpha", "notes.local"), "secret\n")
	writeFile(t, filepath.Join(dir, ".gitignore"), "*.local\n")

	var buf bytes.Buffer
	if err := Tar(&buf, dir); err != nil {
		t.Fatal(err)
	}

	for _, n := range tarNames(t, buf.Bytes()) {
		if n == "alpha/notes.local" {
			t.Errorf("tar included %q, root .gitignore should have excluded it", n)
		}
	}
}

func TestTarScopesNestedGitignoreToItsOwnSubtree(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "alpha", "SKILL.md"), "---\nname: alpha\n---\n")
	writeFile(t, filepath.Join(dir, "alpha", "build.tmp"), "junk\n")
	writeFile(t, filepath.Join(dir, "alpha", ".gitignore"), "*.tmp\n")
	writeFile(t, filepath.Join(dir, "beta", "SKILL.md"), "---\nname: beta\n---\n")
	writeFile(t, filepath.Join(dir, "beta", "build.tmp"), "kept\n")

	var buf bytes.Buffer
	if err := Tar(&buf, dir); err != nil {
		t.Fatal(err)
	}

	names := tarNames(t, buf.Bytes())
	for _, n := range names {
		if n == "alpha/build.tmp" {
			t.Errorf("alpha/.gitignore should have excluded %q", n)
		}
	}
	found := false
	for _, n := range names {
		if n == "beta/build.tmp" {
			found = true
		}
	}
	if !found {
		t.Errorf("beta/build.tmp should survive: alpha's .gitignore must not leak into beta, got %v", names)
	}
}
