package cli

import (
	"bytes"
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
	return run(root), buf.String()
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
