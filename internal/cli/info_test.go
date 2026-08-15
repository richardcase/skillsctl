package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/claudex"
	"github.com/richardcase/skillsctl/internal/testrepo"
)

// infoJSON runs `info <name> --json` and decodes it.
func infoJSON(t *testing.T, h *harness, name string) map[string]any {
	t.Helper()

	out, _, err := h.runSplit(t, "info", name, "--json")
	if err != nil {
		t.Fatalf("info --json: %v\n%s", err, out)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode info --json: %v\n%s", err, out)
	}
	return got
}

func TestInfoShowsEveryReceiptField(t *testing.T) {
	h := newHarness(t)
	url, sha := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	if out, err := h.run(t, "install", url); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	out, err := h.run(t, "info", "demo-skill")
	if err != nil {
		t.Fatalf("info: %v\n%s", err, out)
	}

	// The description is parsed from SKILL.md and rendered nowhere else, which
	// is half the reason this command exists.
	for _, want := range []string{
		"demo-skill",
		"A demo",
		"git",
		url,
		sha, // in full: list already has the short form
		h.claude,
		h.codex,
		"claude",
		"codex",
		"installed",
		"updated",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("info output does not mention %q:\n%s", want, out)
		}
	}
}

// The report is the command's product, so a shell has to be able to capture it.
func TestInfoWritesTheReportToStdout(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})
	if out, err := h.run(t, "install", url); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	stdout, stderr, err := h.runSplit(t, "info", "demo-skill")
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	if !strings.Contains(stdout, "demo-skill") {
		t.Errorf("stdout = %q, want the report", stdout)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Errorf("stderr = %q, want nothing", stderr)
	}
}

// info --json is a superset of one element of list --json, so a script that
// reads a field off list can read the same field off info.
func TestInfoJSONCarriesEveryFieldListJSONDoes(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})
	if out, err := h.run(t, "install", url); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	listOut, _, err := h.runSplit(t, "list", "--json")
	if err != nil {
		t.Fatalf("list --json: %v", err)
	}
	var listed []map[string]any
	if err := json.Unmarshal([]byte(listOut), &listed); err != nil {
		t.Fatalf("decode list --json: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("list --json returned %d receipts, want 1", len(listed))
	}

	got := infoJSON(t, h, "demo-skill")
	for key, want := range listed[0] {
		if key == "links" {
			// Deliberately richer: checked in its own test.
			continue
		}
		if got[key] == nil {
			t.Errorf("info --json is missing %q, which list --json has", key)
			continue
		}
		if got[key] != want {
			t.Errorf("info --json %q = %v, want %v as list --json spells it", key, got[key], want)
		}
	}

	for _, key := range []string{"description", "agents", "ownership"} {
		if got[key] == nil {
			t.Errorf("info --json is missing the derived key %q", key)
		}
	}
}

func TestInfoJSONLinksCarryTheirState(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})
	if out, err := h.run(t, "install", url); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	links, ok := infoJSON(t, h, "demo-skill")["links"].([]any)
	if !ok || len(links) != 2 {
		t.Fatalf("links = %v, want one per agent", links)
	}
	for _, l := range links {
		entry, ok := l.(map[string]any)
		if !ok {
			t.Fatalf("link entry = %v, want an object", l)
		}
		for _, key := range []string{"target", "path", "state", "dest"} {
			if entry[key] == nil {
				t.Errorf("link entry %v is missing %q", entry, key)
			}
		}
		if entry["state"] != "ok" {
			t.Errorf("state = %v, want ok for a link that was just made", entry["state"])
		}
	}
}

func TestInfoReportsADanglingLink(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})
	if out, err := h.run(t, "install", url); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	dest, err := os.Readlink(filepath.Join(h.claude, "demo-skill"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dest); err != nil {
		t.Fatal(err)
	}

	out, err := h.run(t, "info", "demo-skill")
	// A broken link is a finding, not a failure: info answered the question.
	if err != nil {
		t.Fatalf("info: %v\n%s", err, out)
	}
	if !strings.Contains(out, "dangling") {
		t.Errorf("info output does not report the broken link:\n%s", out)
	}
}

func TestInfoReportsALinkPointingElsewhere(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})
	if out, err := h.run(t, "install", url); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	elsewhere := t.TempDir()
	link := filepath.Join(h.codex, "demo-skill")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, link); err != nil {
		t.Fatal(err)
	}

	out, err := h.run(t, "info", "demo-skill")
	if err != nil {
		t.Fatalf("info: %v\n%s", err, out)
	}
	if !strings.Contains(out, "elsewhere") {
		t.Errorf("info output does not report the re-pointed link:\n%s", out)
	}
	if !strings.Contains(out, elsewhere) {
		t.Errorf("info output does not name where the link now points:\n%s", out)
	}
}

func TestInfoReportsAMissingLink(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})
	if out, err := h.run(t, "install", url); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if err := os.Remove(filepath.Join(h.codex, "demo-skill")); err != nil {
		t.Fatal(err)
	}

	out, err := h.run(t, "info", "demo-skill")
	if err != nil {
		t.Fatalf("info: %v\n%s", err, out)
	}
	if !strings.Contains(out, "missing") {
		t.Errorf("info output does not report the deleted link:\n%s", out)
	}
}

// pin clears Ref, and everywhere else an empty Ref means the default branch.
// For a pinned receipt that reading is wrong twice: it tracks nothing, and
// update will not move it.
func TestInfoOnAPinnedSkillDoesNotClaimADefaultBranch(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})
	if out, err := h.run(t, "install", url, "--pin"); err != nil {
		t.Fatalf("install --pin: %v\n%s", err, out)
	}

	out, err := h.run(t, "info", "demo-skill")
	if err != nil {
		t.Fatalf("info: %v\n%s", err, out)
	}
	if strings.Contains(out, "default branch") {
		t.Errorf("info claims a pinned skill tracks a default branch:\n%s", out)
	}
	if !strings.Contains(out, "pinned") {
		t.Errorf("info does not say the skill is pinned:\n%s", out)
	}
}

func TestInfoNamesTheRefAnUnpinnedSkillTracks(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})
	if out, err := h.run(t, "install", url); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	out, err := h.run(t, "info", "demo-skill")
	if err != nil {
		t.Fatalf("info: %v\n%s", err, out)
	}
	if !strings.Contains(out, "default branch") {
		t.Errorf("info does not say what an unpinned skill tracks:\n%s", out)
	}
}

// A local skill has no revision, and an empty cell would read as a missing one
// rather than an absent one.
func TestInfoOnALocalSkillOmitsTheRevision(t *testing.T) {
	h := newHarness(t)
	dir := localDir(t, nil)
	if out, err := h.run(t, "link", dir); err != nil {
		t.Fatalf("link: %v\n%s", err, out)
	}

	out, err := h.run(t, "info", "my-skill")
	if err != nil {
		t.Fatalf("info: %v\n%s", err, out)
	}
	if strings.Contains(out, "revision") {
		t.Errorf("info shows a revision for a local skill:\n%s", out)
	}
	if strings.Contains(out, "default branch") {
		t.Errorf("info claims a local skill tracks a ref:\n%s", out)
	}
	if !strings.Contains(out, dir) {
		t.Errorf("info does not name the directory the skill lives in:\n%s", out)
	}
	if !strings.Contains(out, "Under development") {
		t.Errorf("info does not show the description:\n%s", out)
	}
}

// A plugin records no links, because the agent that installed it can already
// see its skills, so the agents come from the config instead.
func TestInfoOnAPluginNamesTheAgentThatOwnsIt(t *testing.T) {
	h := newHarness(t)
	// A real directory, because adopting a plugin now walks its skills/ to fan
	// them out to the agents that cannot install plugins. An empty skills/ is
	// the case this test wants: nothing to fan out, so info reports the agent
	// that owns the plugin and nothing else.
	pluginPath := h.root + "/superpowers/6.3.0"
	testrepo.Write(t, pluginPath, map[string]string{"skills/.gitkeep": ""})
	h.plugins.installed = []claudex.Installed{
		{ID: pluginID, Version: "6.3.0", InstallPath: pluginPath},
	}
	if out, err := h.run(t, "install", pluginID); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	out, err := h.run(t, "info", "superpowers")
	if err != nil {
		t.Fatalf("info: %v\n%s", err, out)
	}
	if !strings.Contains(out, "claude") {
		t.Errorf("info does not name the agent that owns the plugin:\n%s", out)
	}
	if !strings.Contains(out, "6.3.0") {
		t.Errorf("info does not show the version claude installed:\n%s", out)
	}
	if strings.Contains(out, "default branch") {
		t.Errorf("info claims a plugin tracks a ref:\n%s", out)
	}
}

// A plugin's install path holds no SKILL.md at its root, so a missing one is
// ordinary rather than an error.
func TestInfoWithoutASkillFileStillReportsTheReceipt(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})
	if out, err := h.run(t, "install", url); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	dest, err := os.Readlink(filepath.Join(h.claude, "demo-skill"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dest, "SKILL.md")); err != nil {
		t.Fatal(err)
	}

	out, err := h.run(t, "info", "demo-skill")
	if err != nil {
		t.Fatalf("info: %v\n%s", err, out)
	}
	if !strings.Contains(out, url) {
		t.Errorf("info gave up on a receipt whose SKILL.md is gone:\n%s", out)
	}
	if strings.Contains(out, "A demo") {
		t.Errorf("info showed a description it could not have read:\n%s", out)
	}
}

// adopt records a git skill whose files are in the user's own working copy.
// Calling that "skillsctl's store" would be a lie the path on the line above
// contradicts.
func TestInfoDoesNotClaimAnAdoptedCheckoutIsInTheStore(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"skills/demo/SKILL.md": skillMD})
	clone := testrepo.Clone(t, url)
	handLink(t, h.claude, "demo-skill", filepath.Join(clone, "skills", "demo"))
	if out, err := h.run(t, "adopt"); err != nil {
		t.Fatalf("adopt: %v\n%s", err, out)
	}

	out, err := h.run(t, "info", "demo-skill")
	if err != nil {
		t.Fatalf("info: %v\n%s", err, out)
	}
	if strings.Contains(out, "(skillsctl's store)") {
		t.Errorf("info places a working copy in the store:\n%s", out)
	}
	if !strings.Contains(out, "working copy of your own") {
		t.Errorf("info does not say whose files these are:\n%s", out)
	}
}

func TestInfoOnAnUnknownNameSuggestsNearMisses(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})
	if out, err := h.run(t, "install", url); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	out, err := h.run(t, "info", "demo")
	if err == nil {
		t.Fatalf("info accepted a name that is not installed\n%s", out)
	}
	msg := err.Error()
	if !strings.Contains(msg, "did you mean") || !strings.Contains(msg, "demo-skill") {
		t.Errorf("error = %q, want it to suggest the skill that is installed", msg)
	}
}

func TestInfoOnAnUnknownNameWithNothingLikeItNamesList(t *testing.T) {
	h := newHarness(t)
	out, err := h.run(t, "info", "zzz")
	if err == nil {
		t.Fatalf("info accepted a name that is not installed\n%s", out)
	}
	if !strings.Contains(err.Error(), "skillsctl list") {
		t.Errorf("error = %q, want it to name the remedy", err)
	}
}
