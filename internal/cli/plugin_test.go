package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/claudex"
	"github.com/richardcase/skillsctl/internal/testrepo"
)

const pluginID = "superpowers@claude-plugins-official"

// fakePlugins stands in for the claude binary. It answers List from a table and
// mutates that table when a plan's Exec op asks it to, so an install followed by
// a settle reads back what the install would really have produced.
type fakePlugins struct {
	installed []claudex.Installed
	// next is the version an install or update lands on. Empty means the CLI
	// does nothing observable, which is how a no-op update is simulated.
	next string
	// listErr fails every read, standing in for a missing binary.
	listErr error
	// calls counts List, so "one call for the whole batch" can be asserted.
	calls int

	// root is a real directory each install path is built under, and skills
	// names the skills a plugin ships there. Without a tree on disk there is
	// nothing to fan out, so the fake builds one.
	root   string
	skills []string
}

func (f *fakePlugins) List(context.Context) ([]claudex.Installed, error) {
	f.calls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]claudex.Installed(nil), f.installed...), nil
}

func (f *fakePlugins) InstallArgv(id string) []string {
	return []string{"claude", "plugin", "install", id, "--scope", "user"}
}

func (f *fakePlugins) UninstallArgv(id string) []string {
	return []string{"claude", "plugin", "uninstall", id, "--scope", "user"}
}

func (f *fakePlugins) UpdateArgv(id string) []string {
	return []string{"claude", "plugin", "update", id, "--scope", "user"}
}

// exec applies what the real CLI would have done to the installed set.
func (f *fakePlugins) exec(argv []string) error {
	if len(argv) < 4 || argv[1] != "plugin" {
		return fmt.Errorf("unexpected command %v", argv)
	}
	verb, id := argv[2], argv[3]

	switch verb {
	case "install", "update":
		version := f.next
		if version == "" {
			version = "1.0.0"
		}
		path, err := f.tree(id, version)
		if err != nil {
			return err
		}
		f.put(claudex.Installed{
			ID: id, Version: version, Scope: "user", Enabled: true,
			InstallPath: path,
		})
	case "uninstall":
		var out []claudex.Installed
		for _, p := range f.installed {
			if p.ID != id {
				out = append(out, p)
			}
		}
		f.installed = out
	default:
		return fmt.Errorf("unexpected verb %q", verb)
	}
	return nil
}

// tree writes the skills directory claude would have unpacked, so a reconcile
// has something real to link at.
func (f *fakePlugins) tree(id, version string) (string, error) {
	dir := filepath.Join(f.root, strings.ReplaceAll(id, "@", "-"), version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	for _, n := range f.skills {
		sd := filepath.Join(dir, "skills", n)
		if err := os.MkdirAll(sd, 0o755); err != nil {
			return "", err
		}
		body := "---\nname: " + n + "\ndescription: a skill\n---\n\nBody.\n"
		if err := os.WriteFile(filepath.Join(sd, "SKILL.md"), []byte(body), 0o644); err != nil {
			return "", err
		}
	}
	return dir, nil
}

func (f *fakePlugins) put(p claudex.Installed) {
	for i := range f.installed {
		if f.installed[i].ID == p.ID {
			f.installed[i] = p
			return
		}
	}
	f.installed = append(f.installed, p)
}

func (f *fakePlugins) has(id string) bool {
	for _, p := range f.installed {
		if p.ID == id {
			return true
		}
	}
	return false
}

func TestInstallPluginFansItsSkillsOutToCodex(t *testing.T) {
	h := newHarness(t)
	h.plugins.skills = []string{"alpha", "beta"}

	out, err := h.run(t, "install", pluginID)
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	for _, name := range []string{"alpha", "beta"} {
		dest, rerr := os.Readlink(filepath.Join(h.codex, name))
		if rerr != nil {
			t.Fatalf("codex has no link for %s: %v", name, rerr)
		}
		if _, serr := os.Stat(filepath.Join(dest, "SKILL.md")); serr != nil {
			t.Errorf("%s -> %s does not hold a SKILL.md", name, dest)
		}
	}

	// claude installed the plugin and can already see it, so there is nothing
	// of ours in its skills directory.
	if _, serr := os.Stat(filepath.Join(h.claude, "alpha")); !os.IsNotExist(serr) {
		t.Error("linked into claude, which can already see the plugin's skills")
	}

	links := h.receipts(t)["superpowers"]["links"].([]any)
	if len(links) != 2 {
		t.Errorf("recorded links = %v, want one per skill: they are the removal contract", links)
	}
}

func TestInstallPluginRecordsTheVersionClaudeChose(t *testing.T) {
	h := newHarness(t)
	h.plugins.next = "6.3.0"

	out, err := h.run(t, "install", pluginID)
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	if len(h.ran) != 1 || h.ran[0][2] != "install" {
		t.Fatalf("commands run = %v, want one claude plugin install", h.ran)
	}
	if !strings.Contains(out, "installed superpowers @ 6.3.0 into claude") {
		t.Errorf("output = %q, want the version claude settled on and the agent that has it", out)
	}

	// The version and path are only knowable after the install, so the receipt
	// is proof that the settle ran.
	r := h.receipts(t)["superpowers"]
	if r["resolved"] != "6.3.0" {
		t.Errorf("resolved = %v, want the version read back from claude", r["resolved"])
	}
	wantPath := filepath.Join(h.plugins.root, strings.ReplaceAll(pluginID, "@", "-"), "6.3.0")
	if r["revPath"] != wantPath {
		t.Errorf("revPath = %v, want the path claude installed into (%s)", r["revPath"], wantPath)
	}
	if r["channel"] != "plugin" || r["source"] != pluginID {
		t.Errorf("receipt = %v, want the plugin channel and its id", r)
	}
	if links, ok := r["links"].([]any); ok && len(links) != 0 {
		t.Errorf("links = %v, want none: the agent already sees a plugin it installed", links)
	}
}

func TestInstallPluginAdoptsOneClaudeAlreadyHas(t *testing.T) {
	h := newHarness(t)
	// Create a real directory structure for the adopted plugin, with an empty
	// skills directory so fan can walk it without error.
	pluginPath := h.root + "/adopted/6.3.0"
	testrepo.Write(t, pluginPath, map[string]string{
		"skills/.gitkeep": "",
	})
	h.plugins.installed = []claudex.Installed{
		{ID: pluginID, Version: "6.3.0", InstallPath: pluginPath},
	}

	out, err := h.run(t, "install", pluginID)
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	if len(h.ran) != 0 {
		t.Errorf("commands run = %v, want none: claude already had it", h.ran)
	}
	if !strings.Contains(out, "6.3.0") {
		t.Errorf("output = %q, want the version it adopted", out)
	}
}

// Adopting makes the dry-run exact, because the version is known before the
// plan is built rather than after it has run.
func TestInstallPluginDryRunNamesWhatItWouldRun(t *testing.T) {
	h := newHarness(t)

	out, err := h.run(t, "install", pluginID, "--dry-run")
	if err != nil {
		t.Fatalf("install --dry-run: %v\n%s", err, out)
	}

	if !strings.Contains(out, "exec    claude plugin install "+pluginID) {
		t.Errorf("output = %q, want the exact command it would run", out)
	}
	// No version to record yet, so the line must not imply one.
	if !strings.Contains(out, "record  superpowers\n") {
		t.Errorf("output = %q, want the record line without a version", out)
	}
	if len(h.ran) != 0 {
		t.Errorf("commands run = %v, want none under --dry-run", h.ran)
	}
	if h.plugins.has(pluginID) {
		t.Error("--dry-run installed the plugin")
	}
}

func TestInstallPluginRejectsFlagsThatOnlyMeanSomethingForARepository(t *testing.T) {
	for _, tc := range []struct{ name, flag, value string }{
		{name: "ref", flag: "--ref", value: "main"},
		{name: "skill", flag: "--skill", value: "alpha"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			out, err := h.run(t, "install", pluginID, tc.flag, tc.value)
			if err == nil {
				t.Fatalf("install accepted %s for a plugin\n%s", tc.flag, out)
			}
			if !strings.Contains(err.Error(), tc.flag) {
				t.Errorf("error = %v, want it to name %s", err, tc.flag)
			}
		})
	}

	t.Run("pin", func(t *testing.T) {
		h := newHarness(t)
		if _, err := h.run(t, "install", pluginID, "--pin"); err == nil || !strings.Contains(err.Error(), "--pin") {
			t.Errorf("error = %v, want it to name --pin", err)
		}
	})
}

func TestInstallPluginRefusesAnAgentThatDoesNotInstallPlugins(t *testing.T) {
	h := newHarness(t)

	_, err := h.run(t, "install", pluginID, "-a", "codex")
	if err == nil {
		t.Fatal("install accepted an agent with no plugin support")
	}
	if !strings.Contains(err.Error(), "plugins = true") {
		t.Errorf("error = %v, want it to name the config that would fix it", err)
	}
}

func TestListShowsAPluginWithItsVersionAndAgent(t *testing.T) {
	h := newHarness(t)
	h.plugins.next = "6.3.0"
	if out, err := h.run(t, "install", pluginID); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	out, err := h.run(t, "list")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, out)
	}
	if !strings.Contains(out, "superpowers") || !strings.Contains(out, "plugin") {
		t.Errorf("list = %q, want the plugin row", out)
	}
	// A version is not a sha and must survive intact.
	if !strings.Contains(out, "6.3.0") {
		t.Errorf("list = %q, want the version unabbreviated", out)
	}
	if !strings.Contains(out, "claude") {
		t.Errorf("list = %q, want the agent derived from the channel, since a plugin has no links", out)
	}
}

func TestRemovePluginUninstallsThroughClaude(t *testing.T) {
	h := newHarness(t)
	h.plugins.next = "6.3.0"
	if out, err := h.run(t, "install", pluginID); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	h.ran = nil

	out, err := h.run(t, "remove", "superpowers")
	if err != nil {
		t.Fatalf("remove: %v\n%s", err, out)
	}

	if len(h.ran) != 1 || h.ran[0][2] != "uninstall" {
		t.Fatalf("commands run = %v, want one claude plugin uninstall", h.ran)
	}
	if h.plugins.has(pluginID) {
		t.Error("remove left the plugin installed")
	}

	listed, err := h.run(t, "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(listed, "No skills installed") {
		t.Errorf("list = %q, want the receipt forgotten", listed)
	}
}

func TestRemovePluginFromAnAgentThatCannotHaveItChangesNothing(t *testing.T) {
	h := newHarness(t)
	h.plugins.next = "6.3.0"
	if out, err := h.run(t, "install", pluginID); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	h.ran = nil

	if _, err := h.run(t, "remove", "superpowers", "-a", "codex"); err == nil {
		t.Fatal("remove accepted an agent that never had the plugin")
	}
	if len(h.ran) != 0 {
		t.Errorf("commands run = %v, want none", h.ran)
	}
	if !h.plugins.has(pluginID) {
		t.Error("remove uninstalled a plugin it was not asked to")
	}
}

func TestUpdatePluginReportsOnlyWhatMoved(t *testing.T) {
	h := newHarness(t)
	h.plugins.next = "6.3.0"
	if out, err := h.run(t, "install", pluginID); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	t.Run("no-op update says nothing moved", func(t *testing.T) {
		h.ran = nil
		out, err := h.run(t, "update")
		if err != nil {
			t.Fatalf("update: %v\n%s", err, out)
		}
		if !strings.Contains(out, "Everything is up to date") {
			t.Errorf("update = %q, want a re-install at the same version reported as current", out)
		}
	})

	t.Run("a moved version is reported and recorded", func(t *testing.T) {
		h.plugins.next = "6.4.0"
		h.ran = nil

		out, err := h.run(t, "update")
		if err != nil {
			t.Fatalf("update: %v\n%s", err, out)
		}
		if len(h.ran) != 1 || h.ran[0][2] != "update" {
			t.Fatalf("commands run = %v, want one claude plugin update", h.ran)
		}
		if !strings.Contains(out, "6.3.0") || !strings.Contains(out, "6.4.0") {
			t.Errorf("update = %q, want the version it moved from and to", out)
		}

		if got := h.receipts(t)["superpowers"]["resolved"]; got != "6.4.0" {
			t.Errorf("resolved = %v, want the new version read back", got)
		}
	})
}

func TestUpdatePluginDryRunSaysTheVersionIsNotKnowableYet(t *testing.T) {
	h := newHarness(t)
	h.plugins.next = "6.3.0"
	if out, err := h.run(t, "install", pluginID); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	h.ran = nil

	out, err := h.run(t, "update", "--dry-run")
	if err != nil {
		t.Fatalf("update --dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "exec    claude plugin update "+pluginID) {
		t.Errorf("output = %q, want the exact command", out)
	}
	if strings.Contains(out, "-> \n") {
		t.Errorf("output = %q, want no arrow pointing at an unknown version", out)
	}
	if len(h.ran) != 0 {
		t.Errorf("commands run = %v, want none under --dry-run", h.ran)
	}
}

func TestUpdateReportsAPluginClaudeNoLongerHas(t *testing.T) {
	h := newHarness(t)
	h.plugins.next = "6.3.0"
	if out, err := h.run(t, "install", pluginID); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	h.plugins.installed = nil

	out, err := h.run(t, "update")
	if err == nil {
		t.Fatalf("update reported success for a plugin claude has lost\n%s", out)
	}
	if !strings.Contains(out, "no longer has") {
		t.Errorf("output = %q, want it to say claude lost the plugin", out)
	}
}

// store.Collect reads an empty slug as "repository identity unknown" and gives
// up on mirrors entirely. A plugin receipt has no slug because nothing of ours
// is in the store, so leaving it in the live set would stop gc reclaiming any
// mirror at all — for every skill, not just the plugin.
func TestGCStillReclaimsMirrorsWhileAPluginIsInstalled(t *testing.T) {
	h := newHarness(t)
	h.plugins.next = "6.3.0"

	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})
	if out, err := h.run(t, "install", url); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if out, err := h.run(t, "install", pluginID); err != nil {
		t.Fatalf("install plugin: %v\n%s", err, out)
	}
	if out, err := h.run(t, "remove", "demo-skill"); err != nil {
		t.Fatalf("remove: %v\n%s", err, out)
	}

	out, err := h.run(t, "gc")
	if err != nil {
		t.Fatalf("gc: %v\n%s", err, out)
	}
	if strings.Contains(out, "no bare mirror could be proven unused") {
		t.Fatalf("gc = %q, want the plugin receipt excluded from the live set", out)
	}
	if !strings.Contains(out, "mirror") {
		t.Errorf("gc = %q, want the orphaned mirror reclaimed", out)
	}

	// And the plugin itself is untouched: nothing of it lives in the store.
	if !h.plugins.has(pluginID) {
		t.Error("gc removed a plugin the agent owns")
	}
	if listed, _ := h.run(t, "list"); !strings.Contains(listed, "superpowers") {
		t.Errorf("list = %q, want the plugin receipt intact after gc", listed)
	}
}

func TestUpdatePluginRepointsCodexAtTheNewVersion(t *testing.T) {
	h := newHarness(t)
	h.plugins.skills = []string{"alpha"}

	if out, err := h.run(t, "install", pluginID); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	before, err := os.Readlink(filepath.Join(h.codex, "alpha"))
	if err != nil {
		t.Fatal(err)
	}

	h.plugins.next = "2.0.0"
	if out, err := h.run(t, "update"); err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}

	after, err := os.Readlink(filepath.Join(h.codex, "alpha"))
	if err != nil {
		t.Fatalf("codex lost its link across the update: %v", err)
	}
	if after == before {
		t.Fatalf("link still points at %s: claude keeps the old version directory, so a stale link "+
			"goes on serving it rather than dangling", before)
	}
	if !strings.Contains(after, "2.0.0") {
		t.Errorf("link points at %q, want the 2.0.0 directory", after)
	}
}

func TestUpdatePluginUnlinksASkillItStoppedShipping(t *testing.T) {
	h := newHarness(t)
	h.plugins.skills = []string{"alpha", "beta"}

	if out, err := h.run(t, "install", pluginID); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	h.plugins.next = "2.0.0"
	h.plugins.skills = []string{"alpha"}
	if out, err := h.run(t, "update"); err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}

	if _, err := os.Lstat(filepath.Join(h.codex, "beta")); !os.IsNotExist(err) {
		t.Error("beta is still linked into codex, pointing into a version directory nothing will ever collect")
	}
	links := h.receipts(t)["superpowers"]["links"].([]any)
	if len(links) != 1 {
		t.Errorf("recorded links = %v, want only alpha", links)
	}
}

func TestLinkPluginIntoAnAgentThatWasNotThereAtInstallTime(t *testing.T) {
	h := newHarness(t)
	h.plugins.skills = []string{"alpha"}

	if out, err := h.run(t, "install", pluginID, "-a", "claude"); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if _, err := os.Lstat(filepath.Join(h.codex, "alpha")); !os.IsNotExist(err) {
		t.Fatal("codex was not named, so it should hold nothing yet")
	}

	out, err := h.run(t, "link", "superpowers", "-a", "codex")
	if err != nil {
		t.Fatalf("link: %v\n%s", err, out)
	}
	dest, err := os.Readlink(filepath.Join(h.codex, "alpha"))
	if err != nil {
		t.Fatalf("codex has no link for alpha: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "SKILL.md")); err != nil {
		t.Errorf("alpha -> %s does not hold a SKILL.md", dest)
	}
}

func TestLinkPluginIntoTheAgentThatOwnsItSaysItAlreadyHasIt(t *testing.T) {
	h := newHarness(t)
	h.plugins.skills = []string{"alpha"}

	if out, err := h.run(t, "install", pluginID); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	out, err := h.run(t, "link", "superpowers", "-a", "claude")
	if err == nil {
		t.Fatalf("claude can already see the plugin, so there is nothing to add\n%s", out)
	}
	if !strings.Contains(out+err.Error(), "already linked into claude") {
		t.Errorf("output = %q / err = %v, want it to say claude already has it", out, err)
	}
}

func TestOutdatedReportsAPluginClaudeMovedBehindOurBack(t *testing.T) {
	h := newHarness(t)
	h.plugins.skills = []string{"alpha"}

	if out, err := h.run(t, "install", pluginID); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	// What `claude plugin update` on its own would leave behind.
	h.plugins.next = "2.0.0"
	if err := h.plugins.exec([]string{"claude", "plugin", "update", pluginID}); err != nil {
		t.Fatal(err)
	}

	out, _ := h.run(t, "outdated")
	if !strings.Contains(out, "stale") {
		t.Errorf("output = %q, want the plugin reported as stale rather than n/a", out)
	}
}

func TestInstallPluginSurfacesAMissingClaude(t *testing.T) {
	h := newHarness(t)
	h.plugins.listErr = claudex.ErrNotFound

	_, err := h.run(t, "install", pluginID)
	if err == nil {
		t.Fatal("install succeeded with no claude binary")
	}
	if !strings.Contains(err.Error(), "git source") {
		t.Errorf("error = %v, want it to name the way out", err)
	}
}
