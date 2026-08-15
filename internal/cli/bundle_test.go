package cli

import (
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/manifest"
	"github.com/richardcase/skillsctl/internal/testrepo"
)

// `bundle > skills.toml` has to capture the manifest and nothing else.
func TestBundleWritesTheManifestToStdout(t *testing.T) {
	h := newHarness(t)
	url, sha := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	if out, err := h.run(t, "install", url); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	stdout, stderr, err := h.runSplit(t, "bundle")
	if err != nil {
		t.Fatalf("bundle: %v\n%s", err, stderr)
	}
	if stderr != "" {
		t.Errorf("bundle wrote to stderr with nothing to warn about:\n%s", stderr)
	}

	f, derr := manifest.Decode([]byte(stdout))
	if derr != nil {
		t.Fatalf("bundle did not emit a decodable manifest: %v\n%s", derr, stdout)
	}
	if len(f.Skills) != 1 || f.Skills[0].Name != "demo-skill" {
		t.Fatalf("skills = %+v, want demo-skill", f.Skills)
	}
	// Installed into every present agent, so the field carries no choice.
	if f.Skills[0].Agents != nil {
		t.Errorf("agents = %v, want it omitted", f.Skills[0].Agents)
	}
	if f.Skills[0].Pinned {
		t.Error("the skill was not pinned")
	}
	if strings.Contains(stdout, sha) {
		t.Errorf("an unpinned entry should track its ref, not freeze the sha:\n%s", stdout)
	}
}

func TestBundleCarriesAPin(t *testing.T) {
	h := newHarness(t)
	url, sha := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	if out, err := h.run(t, "install", url, "--pin"); err != nil {
		t.Fatalf("install --pin: %v\n%s", err, out)
	}

	stdout, _, err := h.runSplit(t, "bundle")
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	f, derr := manifest.Decode([]byte(stdout))
	if derr != nil {
		t.Fatalf("decode: %v\n%s", derr, stdout)
	}
	if len(f.Skills) != 1 || !f.Skills[0].Pinned || f.Skills[0].Ref != sha {
		t.Errorf("entry = %+v, want pinned at %s", f.Skills, sha)
	}
}

func TestBundleNamesTheLocalSkillsItLeftOut(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	testrepo.Write(t, dir, map[string]string{"SKILL.md": skillMD})

	if out, err := h.run(t, "link", dir); err != nil {
		t.Fatalf("link: %v\n%s", err, out)
	}

	stdout, stderr, err := h.runSplit(t, "bundle")
	if err != nil {
		t.Fatalf("bundle: %v\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "demo-skill") || !strings.Contains(stderr, dir) {
		t.Errorf("the excluded skill should be named on stderr with its path:\n%s", stderr)
	}
	if strings.Contains(stdout, "demo-skill") {
		t.Errorf("a local skill must not reach the manifest:\n%s", stdout)
	}
	// Excluding everything is still success: an empty manifest plus the warning
	// is a truthful account of a machine holding only local skills.
	f, derr := manifest.Decode([]byte(stdout))
	if derr != nil {
		t.Fatalf("decode: %v\n%s", derr, stdout)
	}
	if len(f.Skills) != 0 {
		t.Errorf("skills = %+v, want none", f.Skills)
	}
}

func TestBundleOnAnEmptyStore(t *testing.T) {
	h := newHarness(t)

	stdout, _, err := h.runSplit(t, "bundle")
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	f, derr := manifest.Decode([]byte(stdout))
	if derr != nil {
		t.Fatalf("an empty store must still emit a valid manifest: %v\n%s", derr, stdout)
	}
	if f.Version != manifest.SchemaVersion || len(f.Skills) != 0 {
		t.Errorf("manifest = %+v, want a versioned empty file", f)
	}
}
