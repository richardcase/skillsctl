package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// exitCode drives the command tree the way main does, returning the exit code
// alongside the output.
func exitCode(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var buf bytes.Buffer
	root := NewRootCmd()
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	return run(context.Background(), root), buf.String()
}

// TestExitPartialWhenGCFreesSomeOfWhatItFound makes one dead revision
// undeletable by taking write permission off the directory holding it, so gc
// frees the other and cannot free that one.
func TestExitPartialWhenGCFreesSomeOfWhatItFound(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions, so nothing can be made undeletable")
	}
	h := newHarness(t)

	// Two unreferenced revisions of two repositories, and no receipts.
	var stuck string
	for i, slug := range []string{"github.com/o/free", "github.com/o/stuck"} {
		dir := filepath.Join(h.root, "rev", filepath.FromSlash(slug), strings.Repeat("abcdef01", 5))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			stuck = filepath.Dir(dir)
		}
	}
	if err := os.Chmod(stuck, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(stuck, 0o755) })

	code, out := exitCode(t, "gc")
	if code != ExitPartial {
		t.Fatalf("exit = %d, want %d for a gc that freed some of what it found\n%s", code, ExitPartial, out)
	}
	if !strings.Contains(out, "freed") {
		t.Errorf("a partial gc should say what it did free:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(h.root, "rev", "github.com", "o", "free")); !os.IsNotExist(err) {
		t.Errorf("the deletable revision was not freed: %v", err)
	}
}

func TestExitOKOnSuccess(t *testing.T) {
	newHarness(t)

	code, out := exitCode(t, "list")
	if code != ExitOK {
		t.Errorf("exit = %d, want %d\n%s", code, ExitOK, out)
	}
}

func TestExitErrorOnFailure(t *testing.T) {
	newHarness(t)

	code, out := exitCode(t, "remove", "never-installed")
	if code != ExitError {
		t.Errorf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(out, "error:") {
		t.Errorf("a failure should be reported as an error:\n%s", out)
	}
	if strings.Contains(out, "Usage:") {
		t.Errorf("a runtime failure is not a usage mistake and should not show help:\n%s", out)
	}
}

// A usage mistake — here, package's required args are missing — should show
// the command's help underneath the error, unlike a runtime failure.
func TestExitErrorOnUsageMistakeShowsHelp(t *testing.T) {
	newHarness(t)

	code, out := exitCode(t, "package")
	if code != ExitError {
		t.Errorf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(out, "error: accepts 2 arg(s)") {
		t.Errorf("want the arg-count error, got:\n%s", out)
	}
	if !strings.Contains(out, "Usage:") || !strings.Contains(out, "Package walks") {
		t.Errorf("a usage mistake should show the command's help:\n%s", out)
	}
}

// An unknown flag is also a usage mistake caught before RunE runs.
func TestExitErrorOnUnknownFlagShowsHelp(t *testing.T) {
	newHarness(t)

	code, out := exitCode(t, "list", "--nope")
	if code != ExitError {
		t.Errorf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(out, "Usage:") {
		t.Errorf("an unknown flag should show the command's help:\n%s", out)
	}
}

func TestExitPartialOnAPartialInstall(t *testing.T) {
	h := newHarness(t)
	url, _ := multiRepo(t)

	if _, err := h.run(t, "install", url, "--skill", "alpha"); err != nil {
		t.Fatal(err)
	}

	code, out := exitCode(t, "install", url, "--all")
	if code != ExitPartial {
		t.Errorf("exit = %d, want %d for an install that did part of the work\n%s", code, ExitPartial, out)
	}
	if !strings.Contains(out, "note:") {
		t.Errorf("a partial install should be reported as a note:\n%s", out)
	}
	if strings.Contains(out, "error:") {
		t.Errorf("a partial install is not a failure:\n%s", out)
	}
	if !strings.Contains(out, "installed beta") {
		t.Errorf("the work that was done should still be reported:\n%s", out)
	}
}

func TestExitErrorWhenNothingCouldBeInstalled(t *testing.T) {
	h := newHarness(t)
	url, _ := multiRepo(t)

	if _, err := h.run(t, "install", url, "--all"); err != nil {
		t.Fatal(err)
	}

	code, out := exitCode(t, "install", url, "--all")
	if code != ExitError {
		t.Errorf("exit = %d, want %d: installing nothing at all is a failure\n%s", code, ExitError, out)
	}
}

func TestPartialErrorIsAnError(t *testing.T) {
	var err error = &PartialError{Reason: "half done"}
	if err.Error() != "half done" {
		t.Errorf("Error() = %q, want %q", err.Error(), "half done")
	}
}
