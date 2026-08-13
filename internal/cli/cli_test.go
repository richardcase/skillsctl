package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/testrepo"
)

// harness points skillsctl at a temp store and two temp agent directories.
type harness struct {
	root   string
	agents string
	claude string
	codex  string
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	root := t.TempDir()
	agents := t.TempDir()
	h := &harness{
		root:   filepath.Join(root, "store"),
		agents: agents,
		claude: filepath.Join(agents, ".claude", "skills"),
		codex:  filepath.Join(agents, ".codex", "skills"),
	}

	// Both agent parent directories exist, so both are "present".
	for _, d := range []string{filepath.Join(agents, ".claude"), filepath.Join(agents, ".codex")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	cfg := filepath.Join(root, "config.toml")
	body := "[[target]]\nname = \"claude\"\ndir = \"" + h.claude + "\"\nplugins = true\n\n" +
		"[[target]]\nname = \"codex\"\ndir = \"" + h.codex + "\"\n"
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SKILLSCTL_HOME", h.root)
	t.Setenv("SKILLSCTL_CONFIG", cfg)
	return h
}

// run executes the command tree and returns combined output.
func (h *harness) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	root := NewRootCmd()
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

const skillMD = "---\nname: demo-skill\ndescription: A demo\n---\n\nBody.\n"

func TestInstallListRemoveRoundTrip(t *testing.T) {
	h := newHarness(t)
	url, sha := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	out, err := h.run(t, "install", url)
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	for name, dir := range map[string]string{"claude": h.claude, "codex": h.codex} {
		link := filepath.Join(dir, "demo-skill")
		dest, rerr := os.Readlink(link)
		if rerr != nil {
			t.Fatalf("%s link missing: %v", name, rerr)
		}
		if _, serr := os.Stat(filepath.Join(dest, "SKILL.md")); serr != nil {
			t.Errorf("%s link does not resolve to a skill: %v", name, serr)
		}
		if !strings.Contains(dest, sha) {
			t.Errorf("%s link target %q should contain the sha", name, dest)
		}
	}

	out, err = h.run(t, "list")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, out)
	}
	if !strings.Contains(out, "demo-skill") {
		t.Errorf("list output missing the skill:\n%s", out)
	}

	out, err = h.run(t, "remove", "demo-skill")
	if err != nil {
		t.Fatalf("remove: %v\n%s", err, out)
	}
	for name, dir := range map[string]string{"claude": h.claude, "codex": h.codex} {
		if _, serr := os.Lstat(filepath.Join(dir, "demo-skill")); !os.IsNotExist(serr) {
			t.Errorf("%s link survived removal", name)
		}
	}

	out, _ = h.run(t, "list")
	if strings.Contains(out, "demo-skill") {
		t.Errorf("removed skill still listed:\n%s", out)
	}
}

func TestInstallFallsBackToRepoNameWithoutFrontmatter(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": "# No frontmatter\n"})

	if out, err := h.run(t, "install", url); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	want := filepath.Base(testrepo.Dir(url))
	if _, err := os.Lstat(filepath.Join(h.claude, want)); err != nil {
		t.Errorf("expected a link named %q from the repo name: %v", want, err)
	}
}

func TestInstallSingleAgent(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	if out, err := h.run(t, "install", url, "-a", "claude"); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if _, err := os.Lstat(filepath.Join(h.claude, "demo-skill")); err != nil {
		t.Errorf("claude link missing: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(h.codex, "demo-skill")); !os.IsNotExist(err) {
		t.Error("codex should not have been linked")
	}
}

func TestInstallDryRunChangesNothing(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	out, err := h.run(t, "install", url, "--dry-run")
	if err != nil {
		t.Fatalf("install --dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "link") {
		t.Errorf("dry run should describe the link ops:\n%s", out)
	}
	if _, err := os.Lstat(filepath.Join(h.claude, "demo-skill")); !os.IsNotExist(err) {
		t.Error("dry run created a symlink")
	}
	if _, err := os.Stat(filepath.Join(h.root, "state.json")); !os.IsNotExist(err) {
		t.Error("dry run wrote the receipts database")
	}
}

func TestInstallRejectsDuplicateName(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	if _, err := h.run(t, "install", url); err != nil {
		t.Fatalf("first install: %v", err)
	}
	out, err := h.run(t, "install", url)
	if err == nil {
		t.Fatalf("second install succeeded; want a name-collision error\n%s", out)
	}
	if !strings.Contains(err.Error(), "--as") {
		t.Errorf("error should suggest --as, got: %v", err)
	}
}

func TestInstallAsOverridesName(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	if out, err := h.run(t, "install", url, "--as", "renamed"); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if _, err := os.Lstat(filepath.Join(h.claude, "renamed")); err != nil {
		t.Errorf("link named 'renamed' missing: %v", err)
	}
}

func TestInstallRejectsUnsupportedChannel(t *testing.T) {
	h := newHarness(t)

	if _, err := h.run(t, "install", "superpowers@claude-plugins-official"); err == nil {
		t.Fatal("plugin install succeeded; the plugin channel arrives in phase 3")
	}
}

func TestRemoveSingleAgentKeepsReceipt(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	if _, err := h.run(t, "install", url); err != nil {
		t.Fatal(err)
	}
	if out, err := h.run(t, "remove", "demo-skill", "-a", "codex"); err != nil {
		t.Fatalf("remove: %v\n%s", err, out)
	}

	if _, err := os.Lstat(filepath.Join(h.codex, "demo-skill")); !os.IsNotExist(err) {
		t.Error("codex link should be gone")
	}
	if _, err := os.Lstat(filepath.Join(h.claude, "demo-skill")); err != nil {
		t.Error("claude link should survive a codex-only removal")
	}

	out, _ := h.run(t, "list")
	if !strings.Contains(out, "demo-skill") {
		t.Errorf("skill should still be listed while one link remains:\n%s", out)
	}
}

func TestRemoveUnknownSkillErrors(t *testing.T) {
	h := newHarness(t)
	if _, err := h.run(t, "remove", "never-installed"); err == nil {
		t.Fatal("remove of an unknown skill succeeded; want an error")
	}
}

func TestListJSON(t *testing.T) {
	h := newHarness(t)
	url, sha := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	if _, err := h.run(t, "install", url); err != nil {
		t.Fatal(err)
	}
	out, err := h.run(t, "list", "--json")
	if err != nil {
		t.Fatalf("list --json: %v\n%s", err, out)
	}

	var got []struct {
		Name     string `json:"name"`
		Channel  string `json:"channel"`
		Resolved string `json:"resolved"`
		Links    []struct {
			Target string `json:"target"`
		} `json:"links"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("list --json emitted invalid JSON: %v\n%s", err, out)
	}
	if len(got) != 1 {
		t.Fatalf("got %d receipts, want 1", len(got))
	}
	if got[0].Name != "demo-skill" || got[0].Resolved != sha {
		t.Errorf("receipt = %+v, want demo-skill @ %s", got[0], sha)
	}
	if len(got[0].Links) != 2 {
		t.Errorf("got %d links, want 2", len(got[0].Links))
	}
}

func TestListEmpty(t *testing.T) {
	h := newHarness(t)
	out, err := h.run(t, "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "No skills installed") {
		t.Errorf("empty list should say so, got:\n%s", out)
	}
}
