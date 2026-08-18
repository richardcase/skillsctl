package cosignx

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func fake(out string, err error) *CLI {
	c := &CLI{Bin: "cosign"}
	c.output = func(context.Context, ...string) (string, error) { return out, err }
	return c
}

func TestVerifyBuildsTheExpectedArgv(t *testing.T) {
	var gotArgs []string
	c := &CLI{Bin: "cosign"}
	c.output = func(_ context.Context, args ...string) (string, error) {
		gotArgs = args
		return "Verification for ghcr.io/owner/skills@sha256:aaa --\n", nil
	}
	if err := c.Verify(context.Background(), "ghcr.io/owner/skills@sha256:aaa", "cosign.pub"); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	want := "verify --key cosign.pub ghcr.io/owner/skills@sha256:aaa"
	if got := strings.Join(gotArgs, " "); got != want {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

func TestVerifyWrapsAFailure(t *testing.T) {
	err := fake("", errors.New("no matching signatures")).Verify(context.Background(), "ghcr.io/owner/skills@sha256:aaa", "cosign.pub")
	if err == nil {
		t.Fatal("Verify accepted a failing cosign call")
	}
	if !strings.Contains(err.Error(), "verify ghcr.io/owner/skills@sha256:aaa") {
		t.Errorf("error = %v, want it to name the ref", err)
	}
}

func TestSignedReportsTrueWhenTreeListsASignature(t *testing.T) {
	out := "ghcr.io/owner/skills@sha256:aaa\n\n🔐 Signatures for an image tag: ghcr.io/owner/skills:sha256-aaa.sig\n"
	got, err := fake(out, nil).Signed(context.Background(), "ghcr.io/owner/skills@sha256:aaa")
	if err != nil {
		t.Fatalf("Signed: %v", err)
	}
	if !got {
		t.Error("Signed = false, want true for output listing a signature subtree")
	}
}

func TestSignedReportsFalseWhenTreeListsNone(t *testing.T) {
	out := "ghcr.io/owner/skills@sha256:aaa\n\n"
	got, err := fake(out, nil).Signed(context.Background(), "ghcr.io/owner/skills@sha256:aaa")
	if err != nil {
		t.Fatalf("Signed: %v", err)
	}
	if got {
		t.Error("Signed = true, want false when tree lists no signature subtree")
	}
}

func TestSignedSurfacesAMissingBinary(t *testing.T) {
	_, err := fake("", ErrNotFound).Signed(context.Background(), "ghcr.io/owner/skills@sha256:aaa")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want it to wrap ErrNotFound", err)
	}
}

func TestSignBuildsTheExpectedArgv(t *testing.T) {
	var gotArgs []string
	c := &CLI{Bin: "cosign"}
	c.output = func(_ context.Context, args ...string) (string, error) {
		gotArgs = args
		return "", nil
	}
	if err := c.Sign(context.Background(), "ghcr.io/owner/skills:v1", "cosign.key"); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	want := "sign --key cosign.key --yes ghcr.io/owner/skills:v1"
	if got := strings.Join(gotArgs, " "); got != want {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

func TestSignWrapsAFailure(t *testing.T) {
	err := fake("", errors.New("decrypt: incorrect password")).Sign(context.Background(), "ghcr.io/owner/skills:v1", "cosign.key")
	if err == nil {
		t.Fatal("Sign accepted a failing cosign call")
	}
	if !strings.Contains(err.Error(), "sign ghcr.io/owner/skills:v1") {
		t.Errorf("error = %v, want it to name the ref", err)
	}
}

func TestRunReportsAMissingBinaryRatherThanAnExecFailure(t *testing.T) {
	c := &CLI{Bin: "cosign-that-is-not-installed"}
	c.output = c.run

	if _, err := c.Signed(context.Background(), "ghcr.io/owner/skills@sha256:aaa"); !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}
