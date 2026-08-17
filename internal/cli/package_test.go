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
