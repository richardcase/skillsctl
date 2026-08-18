package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type recordingOCI struct {
	pushedRef string
	pushedTar []byte
}

func (r *recordingOCI) Resolve(context.Context, string) (string, error) { return "sha256:dryrun", nil }
func (r *recordingOCI) Pull(context.Context, string, string) error      { return nil }
func (r *recordingOCI) Push(_ context.Context, ref string, body io.Reader) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	r.pushedRef = ref
	r.pushedTar = data
	return nil
}

func TestPackageDryRunListsSkillsWithoutPushing(t *testing.T) {
	h := newHarness(t)
	rec := &recordingOCI{}
	h.oci = rec

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "alpha", "SKILL.md"), []byte("---\nname: alpha\ndescription: a\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := h.run(t, "package", dir, "ghcr.io/owner/skills:v1", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if rec.pushedRef != "" {
		t.Errorf("--dry-run pushed to %q, want no push", rec.pushedRef)
	}
	if !strings.Contains(out, "alpha") {
		t.Errorf("dry-run output %q does not mention the alpha skill", out)
	}
}

func TestPackagePushesTheTarredTree(t *testing.T) {
	h := newHarness(t)
	rec := &recordingOCI{}
	h.oci = rec

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "alpha", "SKILL.md"), []byte("---\nname: alpha\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := h.run(t, "package", dir, "ghcr.io/owner/skills:v1"); err != nil {
		t.Fatal(err)
	}
	if rec.pushedRef != "ghcr.io/owner/skills:v1" {
		t.Errorf("pushed to %q, want ghcr.io/owner/skills:v1", rec.pushedRef)
	}
	if len(rec.pushedTar) == 0 {
		t.Error("pushed an empty tar")
	}
}

type recordingCosign struct {
	signRef, signKey string
	signErr          error
	signKeylessRef   string
	signKeylessErr   error
}

func (c *recordingCosign) Verify(context.Context, string, string) error { return nil }
func (c *recordingCosign) Signed(context.Context, string) (bool, error) { return false, nil }
func (c *recordingCosign) Sign(_ context.Context, ref, keyPath string) error {
	c.signRef, c.signKey = ref, keyPath
	return c.signErr
}

func (c *recordingCosign) SignKeyless(_ context.Context, ref string) error {
	c.signKeylessRef = ref
	return c.signKeylessErr
}

func (c *recordingCosign) VerifyKeyless(context.Context, string, string, string) error { return nil }

func TestPackageSignsAfterPushingWhenSignKeyIsGiven(t *testing.T) {
	h := newHarness(t)
	rec := &recordingOCI{}
	cs := &recordingCosign{}
	h.oci = rec
	h.cosign = cs

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "alpha", "SKILL.md"), []byte("---\nname: alpha\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := h.run(t, "package", dir, "ghcr.io/owner/skills:v1", "--sign-key", "cosign.key")
	if err != nil {
		t.Fatalf("package: %v\n%s", err, out)
	}
	if cs.signRef != "ghcr.io/owner/skills:v1" {
		t.Errorf("signed ref = %q, want ghcr.io/owner/skills:v1", cs.signRef)
	}
	if cs.signKey != "cosign.key" {
		t.Errorf("sign key = %q, want cosign.key", cs.signKey)
	}
	if !strings.Contains(out, "signed ghcr.io/owner/skills:v1") {
		t.Errorf("output %q should confirm the signature", out)
	}
}

func TestPackageDoesNotSignWithoutSignKey(t *testing.T) {
	h := newHarness(t)
	rec := &recordingOCI{}
	cs := &recordingCosign{}
	h.oci = rec
	h.cosign = cs

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "alpha", "SKILL.md"), []byte("---\nname: alpha\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := h.run(t, "package", dir, "ghcr.io/owner/skills:v1"); err != nil {
		t.Fatal(err)
	}
	if cs.signRef != "" {
		t.Errorf("signed %q without --sign-key", cs.signRef)
	}
}

func TestPackageDryRunDoesNotSign(t *testing.T) {
	h := newHarness(t)
	rec := &recordingOCI{}
	cs := &recordingCosign{}
	h.oci = rec
	h.cosign = cs

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "alpha", "SKILL.md"), []byte("---\nname: alpha\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := h.run(t, "package", dir, "ghcr.io/owner/skills:v1", "--sign-key", "cosign.key", "--dry-run")
	if err != nil {
		t.Fatalf("package --dry-run: %v\n%s", err, out)
	}
	if cs.signRef != "" {
		t.Error("--dry-run must not sign")
	}
	if !strings.Contains(out, "and sign it") {
		t.Errorf("dry-run output %q should mention signing", out)
	}
}
