package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/manifest"
	"github.com/richardcase/skillsctl/internal/ocix"
	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/testregistry"
)

func TestPackageInstallOutdatedUpdateRemoveRoundTrip(t *testing.T) {
	h := newHarness(t)
	// Use the real registry client end to end; only the transport (an
	// in-process httptest server) is fake, so this exercises the exact code
	// path skillsctl runs against a real registry.
	h.oci = ocix.New()

	host := testregistry.New(t)
	ref := host + "/skills:v1"

	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "alpha", "SKILL.md"), []byte("---\nname: alpha\ndescription: a\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if out, err := h.run(t, "package", src, ref); err != nil {
		t.Fatalf("package: %v\n%s", err, out)
	}
	if out, err := h.run(t, "install", "oci://"+ref); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	out, _ := h.run(t, "outdated")
	if !strings.Contains(out, "current") {
		t.Errorf("outdated output %q, want it to report current", out)
	}

	// Repackage the same tag with different content, then confirm outdated
	// notices and update follows it.
	if err := os.WriteFile(filepath.Join(src, "alpha", "SKILL.md"), []byte("---\nname: alpha\ndescription: a\n---\nchanged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := h.run(t, "package", src, ref); err != nil {
		t.Fatalf("re-package: %v\n%s", err, out)
	}

	// outdated exits non-zero once a finding is present (ExitOutdated), so
	// its error is expected here rather than a failure — only the report
	// content is asserted, matching TestOutdatedReportsAMovedRef's pattern
	// in cli_test.go.
	out, _ = h.run(t, "outdated")
	if !strings.Contains(out, "outdated") {
		t.Errorf("outdated output %q, want it to report outdated after repackaging", out)
	}

	if out, err := h.run(t, "update"); err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	if out, err := h.run(t, "remove", "alpha"); err != nil {
		t.Fatalf("remove: %v\n%s", err, out)
	}
}

// packageSkill writes a one-skill tree and packages it at ref, so a test can
// make two tags that differ.
func packageSkill(t *testing.T, h *harness, ref, body string) {
	t.Helper()
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "alpha", "SKILL.md"), []byte("---\nname: alpha\ndescription: a\n---\n"+body+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := h.run(t, "package", src, ref); err != nil {
		t.Fatalf("package %s: %v\n%s", ref, err, out)
	}
}

func TestBundleWritesAnOCISourceSyncCanParseBack(t *testing.T) {
	h := newHarness(t)
	h.oci = ocix.New()

	host := testregistry.New(t)
	packageSkill(t, h, host+"/skills:v1", "body")
	if out, err := h.run(t, "install", "oci://"+host+"/skills:v1"); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	out, _, err := h.runSplit(t, "bundle")
	if err != nil {
		t.Fatalf("bundle: %v\n%s", err, out)
	}
	f, err := manifest.Decode([]byte(out))
	if err != nil {
		t.Fatalf("bundle produced a manifest that will not decode: %v\n%s", err, out)
	}
	if len(f.Skills) != 1 {
		t.Fatalf("manifest has %d entries, want 1:\n%s", len(f.Skills), out)
	}

	// The whole finding: a bare registry/repo:tag is caught by the owner/repo
	// git shorthand, so sync on the other machine would install from github.
	e := f.Skills[0]
	src, err := source.Parse(e.Source)
	if err != nil {
		t.Fatalf("manifest source %q will not parse: %v", e.Source, err)
	}
	if src.Channel != source.ChannelOCI {
		t.Fatalf("manifest source %q parses as the %s channel, want oci", e.Source, src.Channel)
	}
	if src.Registry != host || src.Repository != "skills" || src.Tag != "v1" {
		t.Errorf("parsed %s/%s:%s, want %s/skills:v1", src.Registry, src.Repository, src.Tag, host)
	}

	// And sync reads it back as the install it already has, rather than as a
	// receipt that disagrees with the manifest.
	path := filepath.Join(t.TempDir(), "skills.toml")
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
	sout, serr := h.run(t, "sync", path)
	if serr != nil {
		t.Fatalf("sync: %v\n%s", serr, sout)
	}
	if strings.Contains(sout, "differs") {
		t.Errorf("sync reported the bundled entry as differing:\n%s", sout)
	}
}

func TestUnpinWithARefMakesTheNextUpdateFollowThatTag(t *testing.T) {
	h := newHarness(t)
	h.oci = ocix.New()

	host := testregistry.New(t)
	packageSkill(t, h, host+"/skills:v1", "one")
	packageSkill(t, h, host+"/skills:v2", "two")

	if out, err := h.run(t, "install", "oci://"+host+"/skills:v1", "--pin"); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	pinned := h.receipts(t)["alpha"]["resolved"]

	// The verification this used to do shelled out to git ls-remote against a
	// registry reference, which failed with a git error.
	if out, err := h.run(t, "unpin", "alpha", "--ref", "v2"); err != nil {
		t.Fatalf("unpin: %v\n%s", err, out)
	}
	if got := h.receipts(t)["alpha"]["ref"]; got != "v2" {
		t.Fatalf("receipt tracks %v after unpin --ref v2", got)
	}

	if out, err := h.run(t, "update"); err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	got := h.receipts(t)["alpha"]["resolved"]
	if got == pinned {
		t.Errorf("update stayed at %v, so it resolved v1 rather than the tag the unpin chose", got)
	}
}

func TestUnpinRejectsATagTheRegistryDoesNotHave(t *testing.T) {
	h := newHarness(t)
	h.oci = ocix.New()

	host := testregistry.New(t)
	packageSkill(t, h, host+"/skills:v1", "one")
	if out, err := h.run(t, "install", "oci://"+host+"/skills:v1", "--pin"); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	out, err := h.run(t, "unpin", "alpha", "--ref", "nope")
	if err == nil {
		t.Fatalf("unpin accepted a tag the registry does not have:\n%s", out)
	}
	if h.receipts(t)["alpha"]["pinned"] != true {
		t.Error("the pin was released despite the ref failing to resolve")
	}
}
