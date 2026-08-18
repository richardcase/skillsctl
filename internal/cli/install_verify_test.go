package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/testrepo"
)

type fakeOCIWithLayer struct {
	digest string
}

func (f *fakeOCIWithLayer) Resolve(context.Context, string) (string, error) { return f.digest, nil }
func (f *fakeOCIWithLayer) Pull(_ context.Context, _, dest string) error {
	return writeSkillMD(dest)
}
func (f *fakeOCIWithLayer) Push(context.Context, string, io.Reader) error { return nil }

type verifyingCosign struct {
	verifyErr          error
	signed             bool
	verified           []string
	verifyKeylessErr   error
	verifiedKeyless    []string
	lastVerifyIdentity string
	lastVerifyIssuer   string
}

func (c *verifyingCosign) Verify(_ context.Context, ref, _ string) error {
	c.verified = append(c.verified, ref)
	return c.verifyErr
}
func (c *verifyingCosign) Signed(context.Context, string) (bool, error) { return c.signed, nil }
func (c *verifyingCosign) Sign(context.Context, string, string) error   { return nil }

func (c *verifyingCosign) SignKeyless(context.Context, string) error { return nil }

func (c *verifyingCosign) VerifyKeyless(_ context.Context, ref, identity, issuer string) error {
	c.verifiedKeyless = append(c.verifiedKeyless, ref)
	c.lastVerifyIdentity = identity
	c.lastVerifyIssuer = issuer
	return c.verifyKeylessErr
}

func TestInstallVerifiesTheSignatureBeforeInstalling(t *testing.T) {
	h := newHarness(t)
	h.oci = &fakeOCIWithLayer{digest: "sha256:aaa"}
	h.cosign = &verifyingCosign{}

	out, err := h.run(t, "install", "oci://ghcr.io/owner/skills:v1", "--all", "--verify-key", "cosign.pub")
	if err != nil {
		t.Fatalf("install --verify-key: %v\n%s", err, out)
	}
	if strings.Contains(out, "warning:") {
		t.Errorf("a verified install should print no warning:\n%s", out)
	}
}

func TestInstallFailsClosedOnABadSignature(t *testing.T) {
	h := newHarness(t)
	h.oci = &fakeOCIWithLayer{digest: "sha256:aaa"}
	h.cosign = &verifyingCosign{verifyErr: errors.New("no matching signatures")}

	if out, err := h.run(t, "install", "oci://ghcr.io/owner/skills:v1", "--all", "--verify-key", "cosign.pub"); err == nil {
		t.Fatalf("install accepted a failing verification:\n%s", out)
	}
}

func TestInstallWarnsWhenSignedButNotVerified(t *testing.T) {
	h := newHarness(t)
	h.oci = &fakeOCIWithLayer{digest: "sha256:aaa"}
	h.cosign = &verifyingCosign{signed: true}

	out, err := h.run(t, "install", "oci://ghcr.io/owner/skills:v1", "--all")
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if !strings.Contains(out, "warning:") || !strings.Contains(out, "--verify-key") {
		t.Errorf("output should warn about the unverified signature:\n%s", out)
	}
}

func TestInstallRejectsVerifyKeyOnANonOCISource(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	if out, err := h.run(t, "install", url, "--verify-key", "cosign.pub"); err == nil {
		t.Fatalf("install accepted --verify-key on a git source:\n%s", out)
	} else if !strings.Contains(err.Error(), "oci://") {
		t.Errorf("error = %v, want it to name oci:// sources", err)
	}
}

func TestInstallVerifiesKeylessBeforeInstalling(t *testing.T) {
	h := newHarness(t)
	h.oci = &fakeOCIWithLayer{digest: "sha256:aaa"}
	h.cosign = &verifyingCosign{}

	out, err := h.run(t, "install", "oci://ghcr.io/owner/skills:v1", "--all",
		"--verify-identity", "signer@example.com", "--verify-issuer", "https://accounts.google.com")
	if err != nil {
		t.Fatalf("install --verify-identity/--verify-issuer: %v\n%s", err, out)
	}
	if strings.Contains(out, "warning:") {
		t.Errorf("a verified install should print no warning:\n%s", out)
	}
	cs := h.cosign.(*verifyingCosign)
	if cs.lastVerifyIdentity != "signer@example.com" {
		t.Errorf("VerifyKeyless identity = %q, want the --verify-identity value", cs.lastVerifyIdentity)
	}
	if cs.lastVerifyIssuer != "https://accounts.google.com" {
		t.Errorf("VerifyKeyless issuer = %q, want the --verify-issuer value", cs.lastVerifyIssuer)
	}
}

func TestInstallFailsClosedOnABadKeylessSignature(t *testing.T) {
	h := newHarness(t)
	h.oci = &fakeOCIWithLayer{digest: "sha256:aaa"}
	h.cosign = &verifyingCosign{verifyKeylessErr: errors.New("no matching signatures")}

	out, err := h.run(t, "install", "oci://ghcr.io/owner/skills:v1", "--all",
		"--verify-identity", "signer@example.com", "--verify-issuer", "https://accounts.google.com")
	if err == nil {
		t.Fatalf("install accepted a failing keyless verification:\n%s", out)
	}
}

func TestInstallRequiresIdentityAndIssuerTogether(t *testing.T) {
	h := newHarness(t)
	h.oci = &fakeOCIWithLayer{digest: "sha256:aaa"}
	h.cosign = &verifyingCosign{}

	out, err := h.run(t, "install", "oci://ghcr.io/owner/skills:v1", "--all",
		"--verify-identity", "signer@example.com")
	if err == nil {
		t.Fatalf("install accepted --verify-identity without --verify-issuer:\n%s", out)
	}
	if !strings.Contains(err.Error(), "verify-issuer") {
		t.Errorf("error = %v, want it to name --verify-issuer", err)
	}
}

func TestInstallRejectsVerifyKeyAndVerifyIdentityTogether(t *testing.T) {
	h := newHarness(t)
	h.oci = &fakeOCIWithLayer{digest: "sha256:aaa"}
	h.cosign = &verifyingCosign{}

	out, err := h.run(t, "install", "oci://ghcr.io/owner/skills:v1", "--all",
		"--verify-key", "cosign.pub",
		"--verify-identity", "signer@example.com", "--verify-issuer", "https://accounts.google.com")
	if err == nil {
		t.Fatalf("install accepted --verify-key and --verify-identity together:\n%s", out)
	}
}

func TestInstallRejectsVerifyIdentityOnANonOCISource(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	out, err := h.run(t, "install", url, "--verify-identity", "signer@example.com", "--verify-issuer", "https://accounts.google.com")
	if err == nil {
		t.Fatalf("install accepted --verify-identity on a git source:\n%s", out)
	}
	if !strings.Contains(err.Error(), "oci://") {
		t.Errorf("error = %v, want it to name oci:// sources", err)
	}
}

// writeSkillMD lays out one skill at dest, the shape internal/channel's own
// fakeOCI.Pull already uses.
func writeSkillMD(dest string) error {
	dir := filepath.Join(dest, "alpha")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: alpha\ndescription: a skill\n---\n"), 0o644)
}
