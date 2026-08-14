// Package testrepo builds throwaway git repositories for tests, so no test
// ever needs the network.
package testrepo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// Write creates files (paths relative to dir, "/" separated) under dir.
func Write(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// New creates a repository containing files and returns its file:// URL and
// the sha of the initial commit.
func New(t *testing.T, files map[string]string) (url, sha string) {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "init", "-q", "-b", "main")
	sha = Commit(t, dir, files)
	return "file://" + dir, sha
}

// Commit writes files into an existing repository and commits them, returning
// the new sha. dir is the working tree path, not the file:// URL.
func Commit(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	Write(t, dir, files)
	run(t, dir, "add", "-A")
	run(t, dir, "commit", "-q", "-m", "update")
	return run(t, dir, "rev-parse", "HEAD")
}

// Dir converts a file:// URL returned by New back into a filesystem path.
func Dir(url string) string { return strings.TrimPrefix(url, "file://") }

// Clone checks url out into a fresh directory and returns it. Unlike the
// repository New builds, a clone has an origin remote, which is what makes it
// a working copy whose provenance can be recovered.
func Clone(t *testing.T, url string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "clone")
	run(t, "", "clone", "-q", url, dir)
	return dir
}
