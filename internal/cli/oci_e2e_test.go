package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/ocix"
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
