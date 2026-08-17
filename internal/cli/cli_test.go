package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/claudex"
	"github.com/richardcase/skillsctl/internal/outdated"
	"github.com/richardcase/skillsctl/internal/prompt"
	"github.com/richardcase/skillsctl/internal/testrepo"
)

// harness points skillsctl at a temp store and two temp agent directories.
type harness struct {
	root   string
	agents string
	claude string
	codex  string

	// plugins is what the claude CLI would have reported, and ran is what a
	// plan's Exec ops asked it to do. Both are stubbed for every test, so no
	// test can reach the real binary or the developer's own ~/.claude.
	plugins *fakePlugins
	ran     [][]string

	// picker answers an ambiguous install. It reports itself non-interactive
	// until a test says otherwise, so the listing-and-exit behaviour is what
	// every test that is not about selection still sees.
	picker *fakePicker
}

// fakePicker stands in for a terminal. choose is what the user would have
// done; asked records what they would have been shown.
type fakePicker struct {
	on     bool
	choose func(prompt.Options) ([]int, error)
	asked  prompt.Options
}

func (p *fakePicker) Interactive() bool { return p.on }

func (p *fakePicker) Select(opts prompt.Options) ([]int, error) {
	p.asked = opts
	if p.choose == nil {
		return nil, prompt.ErrCancelled
	}
	return p.choose(opts)
}

// picks makes a fakePicker that ticks the rows at these indices.
func picks(idx ...int) func(prompt.Options) ([]int, error) {
	return func(prompt.Options) ([]int, error) { return idx, nil }
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	root := t.TempDir()
	agents := t.TempDir()
	h := &harness{
		root:    filepath.Join(root, "store"),
		agents:  agents,
		claude:  filepath.Join(agents, ".claude", "skills"),
		codex:   filepath.Join(agents, ".codex", "skills"),
		plugins: &fakePlugins{root: filepath.Join(root, "plugins")},
		picker:  &fakePicker{},
	}

	// Swapping the three seams for the whole test, restored by t.Cleanup. A
	// test that means to exercise the plugin channel populates h.plugins; one
	// that does not still cannot shell out. The picker is the same bargain: a
	// test that means to choose sets h.picker.on, and one that does not cannot
	// block on a terminal.
	realPlugins, realRunner, realPicker := newPlugins, newRunner, newPicker
	newPlugins = func() claudex.Plugins { return h.plugins }
	newPicker = func() picker { return h.picker }
	newRunner = func() func(context.Context, []string) error {
		return func(_ context.Context, argv []string) error {
			h.ran = append(h.ran, argv)
			return h.plugins.exec(argv)
		}
	}
	t.Cleanup(func() { newPlugins, newRunner, newPicker = realPlugins, realRunner, realPicker })

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

// runSplit is run with the two streams kept apart, for asserting which one
// output lands on. run merges them, which hides the difference.
func (h *harness) runSplit(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errBuf bytes.Buffer
	root := NewRootCmd()
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetArgs(args)
	err = root.Execute()
	return out.String(), errBuf.String(), err
}

// receipts reads the committed state, so a test can assert what was recorded
// rather than only what was printed.
func (h *harness) receipts(t *testing.T) map[string]map[string]any {
	t.Helper()
	blob, err := os.ReadFile(filepath.Join(h.root, "state.json"))
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	var db struct {
		Receipts map[string]map[string]any `json:"receipts"`
	}
	if err := json.Unmarshal(blob, &db); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	return db.Receipts
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

func TestListWritesToStdout(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	// An empty store still has to say so on stdout: `list > skills.txt`
	// should not produce an empty file.
	stdout, stderr, err := h.runSplit(t, "list")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "No skills installed") {
		t.Errorf("the empty-store message is not on stdout:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}

	if out, ierr := h.run(t, "install", url); ierr != nil {
		t.Fatalf("install: %v\n%s", ierr, out)
	}

	for _, args := range [][]string{{"list"}, {"list", "--json"}} {
		stdout, stderr, err = h.runSplit(t, args...)
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, stderr)
		}
		if !strings.Contains(stdout, "demo-skill") {
			t.Errorf("%v wrote its output somewhere other than stdout:\nstdout:\n%s\nstderr:\n%s", args, stdout, stderr)
		}
		if stderr != "" {
			t.Errorf("%v wrote to stderr on success:\n%s", args, stderr)
		}
	}

	// The JSON form must be parseable on its own, with nothing else mixed in.
	var receipts []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(stdout), &receipts); err != nil {
		t.Fatalf("list --json stdout is not valid JSON on its own: %v\n%s", err, stdout)
	}
}

func TestInstallRejectsEscapingNameFromFrontmatter(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{
		"SKILL.md": "---\nname: ../../../escaped\n---\n",
	})

	out, err := h.run(t, "install", url)
	if err == nil {
		t.Fatalf("install accepted a traversing name from SKILL.md\n%s", out)
	}

	escaped := filepath.Join(filepath.Dir(h.agents), "escaped")
	if _, serr := os.Lstat(escaped); !os.IsNotExist(serr) {
		t.Errorf("a link was created outside the agent directories at %s", escaped)
	}
}

func TestInstallRejectsEscapingAsFlag(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	if _, err := h.run(t, "install", url, "--as", "../escaped"); err == nil {
		t.Fatal("install accepted a traversing --as value")
	}
}

// revDir is the revision directory an install of url created, found by
// following one of the symlinks it left behind.
func (h *harness) revDir(t *testing.T, name string) string {
	t.Helper()
	dest, err := os.Readlink(filepath.Join(h.claude, name))
	if err != nil {
		t.Fatalf("link for %q missing: %v", name, err)
	}
	return dest
}

func TestGCKeepsSharedRevisionUntilTheLastReceiptGoes(t *testing.T) {
	h := newHarness(t)

	// Two skills in one repository, installed with --all, is the case gc has
	// to get right: two receipts naming different subpaths of one revision.
	url, _ := testrepo.New(t, map[string]string{
		"a/SKILL.md": "---\nname: a\ndescription: A\n---\n",
		"b/SKILL.md": "---\nname: b\ndescription: B\n---\n",
	})
	if out, err := h.run(t, "install", url, "--all"); err != nil {
		t.Fatalf("install --all: %v\n%s", err, out)
	}

	// Both links resolve into the same revision, one subpath deeper.
	rev := filepath.Dir(h.revDir(t, "b"))
	if other := filepath.Dir(h.revDir(t, "a")); other != rev {
		t.Fatalf("the two skills did not share a revision: %q vs %q", other, rev)
	}

	if out, err := h.run(t, "remove", "a"); err != nil {
		t.Fatalf("remove a: %v\n%s", err, out)
	}
	out, err := h.run(t, "gc")
	if err != nil {
		t.Fatalf("gc: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Nothing to reclaim") {
		t.Errorf("gc collected something while a receipt still shares the revision:\n%s", out)
	}
	// The whole revision survives, not just the subpath b still links to:
	// a revision is collected entire or not at all.
	for _, sub := range []string{"a", "b"} {
		if _, serr := os.Stat(filepath.Join(rev, sub, "SKILL.md")); serr != nil {
			t.Fatalf("gc removed %s/ from a revision that is still in use: %v", sub, serr)
		}
	}

	if out, err := h.run(t, "remove", "b"); err != nil {
		t.Fatalf("remove b: %v\n%s", err, out)
	}
	out, err = h.run(t, "gc")
	if err != nil {
		t.Fatalf("gc: %v\n%s", err, out)
	}
	if !strings.Contains(out, "reclaimed") || !strings.Contains(out, "B") {
		t.Errorf("gc should report what it reclaimed and how much:\n%s", out)
	}
	if _, serr := os.Stat(rev); !os.IsNotExist(serr) {
		t.Errorf("the unreferenced revision survived gc: %v", serr)
	}
	if _, serr := os.Stat(filepath.Join(h.root, "cache")); serr == nil {
		entries, _ := os.ReadDir(filepath.Join(h.root, "cache"))
		if len(entries) != 0 {
			t.Errorf("the unreferenced mirror survived gc: %v", entries)
		}
	}
}

func TestGCCollectsARevisionADryRunLeftBehind(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	// install --dry-run still extracts, because that is what lets it name the
	// skill exactly. Nothing records the result, so gc owns the cleanup.
	if out, err := h.run(t, "install", url, "--dry-run"); err != nil {
		t.Fatalf("install --dry-run: %v\n%s", err, out)
	}
	revRoot := filepath.Join(h.root, "rev")
	if _, err := os.Stat(revRoot); err != nil {
		t.Fatalf("dry run extracted nothing, so there is nothing to collect: %v", err)
	}

	out, err := h.run(t, "gc")
	if err != nil {
		t.Fatalf("gc: %v\n%s", err, out)
	}
	if !strings.Contains(out, "reclaimed") {
		t.Errorf("gc did not collect the orphaned extraction:\n%s", out)
	}
	entries, _ := os.ReadDir(revRoot)
	if len(entries) != 0 {
		t.Errorf("rev/ still holds %v after gc", entries)
	}
}

func TestGCDryRunChangesNothing(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	if out, err := h.run(t, "install", url); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	rev := h.revDir(t, "demo-skill")
	if out, err := h.run(t, "remove", "demo-skill"); err != nil {
		t.Fatalf("remove: %v\n%s", err, out)
	}

	out, err := h.run(t, "gc", "--dry-run")
	if err != nil {
		t.Fatalf("gc --dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "would reclaim") {
		t.Errorf("dry run should say what it would reclaim:\n%s", out)
	}
	if _, serr := os.Stat(filepath.Join(rev, "SKILL.md")); serr != nil {
		t.Errorf("gc --dry-run deleted the revision: %v", serr)
	}

	// The real run then collects exactly what the dry run described.
	if out, err := h.run(t, "gc"); err != nil {
		t.Fatalf("gc: %v\n%s", err, out)
	}
	if _, serr := os.Stat(rev); !os.IsNotExist(serr) {
		t.Errorf("revision survived the real gc: %v", serr)
	}
}

func TestGCWithNothingToReclaim(t *testing.T) {
	h := newHarness(t)
	out, err := h.run(t, "gc")
	if err != nil {
		t.Fatalf("gc: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Nothing to reclaim") {
		t.Errorf("gc on an empty store should say so, got:\n%s", out)
	}
}

func TestRemoveHintsAtReclaimableDisk(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	if out, err := h.run(t, "install", url); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	out, err := h.run(t, "remove", "demo-skill")
	if err != nil {
		t.Fatalf("remove: %v\n%s", err, out)
	}
	if !strings.Contains(out, "skillsctl gc") {
		t.Errorf("remove should point at gc for the disk it orphaned:\n%s", out)
	}
}

func TestGCWritesItsWholeReportToStdout(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	if out, err := h.run(t, "install", url); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if out, err := h.run(t, "remove", "demo-skill"); err != nil {
		t.Fatalf("remove: %v\n%s", err, out)
	}

	// `skillsctl gc > log` must capture the listing and its summary together.
	stdout, stderr, err := h.runSplit(t, "gc", "--dry-run")
	if err != nil {
		t.Fatalf("gc: %v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "would reclaim") {
		t.Errorf("the summary is not on stdout:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if !strings.Contains(stdout, "rev/") {
		t.Errorf("the listing is not on stdout:\nstdout:\n%s", stdout)
	}
	if stderr != "" {
		t.Errorf("gc wrote to stderr on success:\n%s", stderr)
	}
}

func TestGCJSON(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	if out, err := h.run(t, "install", url); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if out, err := h.run(t, "remove", "demo-skill"); err != nil {
		t.Fatalf("remove: %v\n%s", err, out)
	}

	out, err := h.run(t, "gc", "--dry-run", "--json")
	if err != nil {
		t.Fatalf("gc --json: %v\n%s", err, out)
	}
	var got struct {
		Revisions []struct {
			Rel   string `json:"rel"`
			Bytes int64  `json:"bytes"`
		} `json:"revisions"`
		Mirrors []struct {
			Rel string `json:"rel"`
		} `json:"mirrors"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("gc --json emitted invalid JSON: %v\n%s", err, out)
	}
	if len(got.Revisions) != 1 || got.Revisions[0].Bytes == 0 {
		t.Errorf("want one sized revision, got %+v", got.Revisions)
	}
	if len(got.Mirrors) != 1 {
		t.Errorf("want one mirror, got %+v", got.Mirrors)
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

func TestListFilterByChannel(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})
	if out, err := h.run(t, "install", url); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	dir := localDir(t, nil)
	if out, err := h.run(t, "link", dir); err != nil {
		t.Fatalf("link: %v\n%s", err, out)
	}

	t.Run("include", func(t *testing.T) {
		out, err := h.run(t, "list", "--include-channel", "git")
		if err != nil {
			t.Fatalf("list --include-channel git: %v\n%s", err, out)
		}
		if !strings.Contains(out, "demo-skill") || strings.Contains(out, "my-skill") {
			t.Errorf("--include-channel git should show only demo-skill, got:\n%s", out)
		}
	})

	t.Run("exclude", func(t *testing.T) {
		out, err := h.run(t, "list", "--exclude-channel", "local")
		if err != nil {
			t.Fatalf("list --exclude-channel local: %v\n%s", err, err)
		}
		if !strings.Contains(out, "demo-skill") || strings.Contains(out, "my-skill") {
			t.Errorf("--exclude-channel local should hide my-skill, got:\n%s", out)
		}
	})

	t.Run("include json", func(t *testing.T) {
		out, err := h.run(t, "list", "--include-channel", "local", "--json")
		if err != nil {
			t.Fatalf("list --include-channel local --json: %v\n%s", err, out)
		}
		var got []struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("invalid JSON: %v\n%s", err, out)
		}
		if len(got) != 1 || got[0].Name != "my-skill" {
			t.Errorf("got %+v, want only my-skill", got)
		}
	})

	t.Run("mutually exclusive", func(t *testing.T) {
		if _, err := h.run(t, "list", "--include-channel", "git", "--exclude-channel", "local"); err == nil {
			t.Fatal("want an error when both flags are set")
		}
	})

	t.Run("unrecognised channel", func(t *testing.T) {
		if _, err := h.run(t, "list", "--include-channel", "bogus"); err == nil {
			t.Fatal("want an error for an unrecognised channel")
		}
	})
}

func TestOutdatedReportsAMovedRef(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	if out, err := h.run(t, "install", url); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	code, out := exitCode(t, "outdated")
	if code != ExitOK {
		t.Fatalf("a freshly installed skill should be current: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "current") {
		t.Errorf("want a current row, got:\n%s", out)
	}

	testrepo.Commit(t, testrepo.Dir(url), map[string]string{"NOTES.md": "moved on\n"})

	code, out = exitCode(t, "outdated")
	if code != ExitOutdated {
		t.Fatalf("exit = %d, want %d once the ref moved\n%s", code, ExitOutdated, out)
	}
	if !strings.Contains(out, "outdated") {
		t.Errorf("want an outdated row, got:\n%s", out)
	}
	if strings.Contains(out, "error:") {
		t.Errorf("an available update is a finding, not a failure:\n%s", out)
	}
}

// A skill whose remote cannot be read leaves the report covering only part of
// what was asked, which is exactly what ExitPartial means.
func TestOutdatedIsPartialWhenARemoteCannotBeRead(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	if out, err := h.run(t, "install", url); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if err := os.RemoveAll(testrepo.Dir(url)); err != nil {
		t.Fatal(err)
	}

	code, out := exitCode(t, "outdated")
	if code != ExitPartial {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitPartial, out)
	}
	if !strings.Contains(out, "error:") {
		t.Errorf("the row should say why it could not be checked:\n%s", out)
	}
}

// A pin is a decision, and update skips pinned skills — so the move is
// reported but nothing actionable follows, and the exit code stays 0.
func TestOutdatedMarksAPinnedSkillWithoutFailing(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	if out, err := h.run(t, "install", url, "--pin"); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	testrepo.Commit(t, testrepo.Dir(url), map[string]string{"NOTES.md": "moved on\n"})

	code, out := exitCode(t, "outdated")
	if code != ExitOK {
		t.Fatalf("a pinned skill must not set an exit code: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "outdated") || !strings.Contains(out, "pinned") {
		t.Errorf("want the move reported and marked pinned, got:\n%s", out)
	}
}

func TestOutdatedJSON(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	if out, err := h.run(t, "install", url); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	sha := testrepo.Commit(t, testrepo.Dir(url), map[string]string{"NOTES.md": "moved on\n"})

	out, _ := h.run(t, "outdated", "--json")

	var entries []outdated.Entry
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	if entries[0].Status != outdated.StatusOutdated {
		t.Errorf("status = %q, want %q", entries[0].Status, outdated.StatusOutdated)
	}
	if entries[0].Latest != sha {
		t.Errorf("latest = %q, want %q", entries[0].Latest, sha)
	}
}

func TestOutdatedEmpty(t *testing.T) {
	h := newHarness(t)
	out, err := h.run(t, "outdated")
	if err != nil {
		t.Fatalf("outdated: %v", err)
	}
	if !strings.Contains(out, "No skills installed") {
		t.Errorf("empty report should say so, got:\n%s", out)
	}
}
