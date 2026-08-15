package channel

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/claudex"
	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/target"
)

// fakeClaude answers List from a table and counts the calls, so "one call for
// the whole batch" can be asserted without a second fixture.
type fakeClaude struct {
	installed []claudex.Installed
	err       error
	calls     int
}

func (f *fakeClaude) List(context.Context) ([]claudex.Installed, error) {
	f.calls++
	return f.installed, f.err
}
func (f *fakeClaude) InstallArgv(id string) []string   { return []string{"claude", "install", id} }
func (f *fakeClaude) UninstallArgv(id string) []string { return []string{"claude", "uninstall", id} }
func (f *fakeClaude) UpdateArgv(id string) []string    { return []string{"claude", "update", id} }

var pluginCfg = target.Config{Targets: []target.Target{
	{Name: "claude", Dir: "/agents/claude/skills", Plugins: true},
	{Name: "codex", Dir: "/agents/codex/skills"},
}}

func newPluginChannel(installed ...claudex.Installed) (*Plugin, *fakeClaude) {
	f := &fakeClaude{installed: installed}
	return NewPlugin(f, pluginCfg), f
}

func pluginRequest() Request {
	src, err := source.Parse("superpowers@claude-plugins-official")
	if err != nil {
		panic(err)
	}
	return Request{Source: src, Targets: pluginCfg.Targets}
}

func TestPluginOwnershipIsTheAgents(t *testing.T) {
	c, _ := newPluginChannel()
	if c.Ownership() != AgentOwned {
		t.Error("a plugin's files belong to the agent that installed it, so gc must not count them")
	}
}

func TestPluginInstallExecsThenRecords(t *testing.T) {
	c, _ := newPluginChannel()

	cands, err := c.Prepare(context.Background(), pluginRequest())
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if cands[0].Adopted {
		t.Error("nothing was installed, so nothing can be adopted")
	}

	p, receipts, err := c.Install(pluginRequest(), cands)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(p.Ops) != 2 {
		t.Fatalf("plan = %v, want an exec and a record", p.Describe())
	}
	if _, ok := p.Ops[0].(plan.Exec); !ok {
		t.Errorf("op 0 is %T, want plan.Exec", p.Ops[0])
	}

	r := receipts[0]
	if r.Resolved != "" || r.RevPath != "" {
		t.Errorf("receipt = %+v, want version and path left for Settle", r)
	}
	if len(r.Links) != 0 {
		t.Errorf("links = %v, want none", r.Links)
	}
	if r.Slug != "" {
		t.Error("a plugin receipt must record no slug: an empty one is what keeps gc from counting it")
	}
	if r.Source != "superpowers@claude-plugins-official" {
		t.Errorf("source = %q, want the id every claude plugin call takes", r.Source)
	}
}

func TestPluginInstallAdoptsWithoutExecuting(t *testing.T) {
	c, _ := newPluginChannel(claudex.Installed{
		ID: "superpowers@claude-plugins-official", Version: "6.3.0", InstallPath: "/p/6.3.0",
	})

	cands, err := c.Prepare(context.Background(), pluginRequest())
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !cands[0].Adopted || cands[0].Version != "6.3.0" {
		t.Fatalf("candidate = %+v, want it adopted at the installed version", cands[0])
	}

	p, receipts, err := c.Install(pluginRequest(), cands)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	for _, op := range p.Ops {
		if _, ok := op.(plan.Exec); ok {
			t.Error("adopting must not re-run the installer")
		}
	}
	if receipts[0].Resolved != "6.3.0" {
		t.Errorf("resolved = %q, want the version already installed, so the dry-run is exact", receipts[0].Resolved)
	}
}

func TestPluginSettleReadsBackVersionAndPath(t *testing.T) {
	c, _ := newPluginChannel(claudex.Installed{
		ID: "p@m", Version: "2.0.0", InstallPath: "/p/2.0.0",
	})

	changed, err := c.Settle(context.Background(), []state.Receipt{{Name: "p", Source: "p@m"}})
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if len(changed) != 1 {
		t.Fatalf("Settle returned %d receipts, want the one it completed", len(changed))
	}
	if changed[0].Resolved != "2.0.0" || changed[0].RevPath != "/p/2.0.0" {
		t.Errorf("settled = %+v, want the version and path claude reported", changed[0])
	}
}

func TestPluginSettleReturnsNothingWhenAlreadyCurrent(t *testing.T) {
	c, _ := newPluginChannel(claudex.Installed{ID: "p@m", Version: "2.0.0", InstallPath: "/p/2.0.0"})

	changed, err := c.Settle(context.Background(), []state.Receipt{
		{Name: "p", Source: "p@m", Resolved: "2.0.0", RevPath: "/p/2.0.0"},
	})
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if len(changed) != 0 {
		t.Errorf("Settle returned %+v, want nothing: a receipt that is already right is not a change", changed)
	}
}

// A plugin claude cannot account for is reported, but the receipts it could
// complete still come back, so a partial answer is still recorded.
func TestPluginSettleReportsWhatItCouldNotComplete(t *testing.T) {
	c, _ := newPluginChannel(claudex.Installed{ID: "a@m", Version: "1.0.0", InstallPath: "/a"})

	changed, err := c.Settle(context.Background(), []state.Receipt{
		{Name: "a", Source: "a@m"},
		{Name: "b", Source: "b@m"},
	})
	if err == nil {
		t.Fatal("Settle hid a plugin claude does not have")
	}
	if !strings.Contains(err.Error(), "b") {
		t.Errorf("error = %v, want it to name the receipt it could not complete", err)
	}
	if len(changed) != 1 || changed[0].Name != "a" {
		t.Errorf("changed = %+v, want the one it did complete", changed)
	}
}

func TestPluginUpdateAsksClaudeOnceForTheWholeBatch(t *testing.T) {
	c, f := newPluginChannel(
		claudex.Installed{ID: "a@m", Version: "1.0.0"},
		claudex.Installed{ID: "b@m", Version: "1.0.0"},
	)

	verdicts, p, err := c.Update(context.Background(), []*state.Receipt{
		{Name: "a", Channel: "plugin", Source: "a@m", Resolved: "1.0.0"},
		{Name: "b", Channel: "plugin", Source: "b@m", Resolved: "1.0.0"},
	}, UpdateOptions{})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if f.calls != 1 {
		t.Errorf("List called %d times, want 1: the batch is what lets a channel ask once", f.calls)
	}
	if len(verdicts) != 2 {
		t.Fatalf("got %d verdicts, want one per receipt", len(verdicts))
	}
	for _, v := range verdicts {
		if v.Status != StatusUpdated {
			t.Errorf("%s: status = %q, want updated", v.Name, v.Status)
		}
		if v.Latest != "" {
			t.Errorf("%s: Latest = %q, want empty until claude has run", v.Name, v.Latest)
		}
	}
	if len(p.Ops) != 4 {
		t.Errorf("plan = %v, want an exec and a record for each", p.Describe())
	}
}

func TestPluginUpdateReportsOneClaudeNoLongerHas(t *testing.T) {
	c, _ := newPluginChannel()

	verdicts, p, err := c.Update(context.Background(), []*state.Receipt{
		{Name: "gone", Channel: "plugin", Source: "gone@m"},
	}, UpdateOptions{})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if verdicts[0].Status != StatusError {
		t.Errorf("status = %q, want an error verdict rather than a silent skip", verdicts[0].Status)
	}
	if !strings.Contains(verdicts[0].Error, "no longer has") {
		t.Errorf("error = %q, want it to say claude lost the plugin", verdicts[0].Error)
	}
	if !p.IsEmpty() {
		t.Error("nothing may be planned for a plugin claude does not have")
	}
}

func TestPluginRemoveUninstallsAndForgets(t *testing.T) {
	c, _ := newPluginChannel()

	p, err := c.Remove(state.Receipt{Name: "p", Source: "p@m"}, nil)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(p.Ops) != 2 {
		t.Fatalf("plan = %v, want an uninstall and a forget", p.Describe())
	}
	if _, ok := p.Ops[0].(plan.Exec); !ok {
		t.Errorf("op 0 is %T, want plan.Exec", p.Ops[0])
	}
	if _, ok := p.Ops[1].(plan.Forget); !ok {
		t.Errorf("op 1 is %T, want plan.Forget: there is no partial removal to keep a receipt for", p.Ops[1])
	}
}

func TestPluginRemoveIgnoresAnAgentThatNeverHadIt(t *testing.T) {
	c, _ := newPluginChannel()

	p, err := c.Remove(state.Receipt{Name: "p", Source: "p@m"}, map[string]bool{"codex": true})
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !p.IsEmpty() {
		t.Errorf("plan = %v, want nothing: codex never had the plugin", p.Describe())
	}
}

func TestPluginAgentsComeFromTheConfig(t *testing.T) {
	c, _ := newPluginChannel()

	got := c.Agents(state.Receipt{})
	if len(got) != 1 || got[0] != "claude" {
		t.Errorf("Agents = %v, want the agents with plugins = true, since a plugin receipt has no links", got)
	}
}

func TestPluginRejectsRepositoryFlags(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  Request
		want string
	}{
		{name: "skill", req: Request{Skills: []string{"a"}}, want: "--skill"},
		{name: "all", req: Request{All: true}, want: "--all"},
		{name: "ref", req: Request{Ref: "main"}, want: "--ref"},
		{name: "pin", req: Request{Pin: true}, want: "--pin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := rejectRepositoryFlags(tc.req)
			if err == nil {
				t.Fatalf("%s was accepted for a plugin", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to name %s", err, tc.want)
			}
		})
	}
}

func TestPluginRefusesAgentsThatDoNotInstallPlugins(t *testing.T) {
	c, _ := newPluginChannel()

	req := pluginRequest()
	req.Targets = []target.Target{{Name: "codex"}}

	_, err := c.Prepare(context.Background(), req)
	if err == nil {
		t.Fatal("Prepare accepted an agent that does not install plugins")
	}
	if !strings.Contains(err.Error(), "plugins = true") {
		t.Errorf("error = %v, want it to name the config that would fix it", err)
	}
}

func TestPluginSurfacesAFailedRead(t *testing.T) {
	f := &fakeClaude{err: claudex.ErrNotFound}
	c := NewPlugin(f, pluginCfg)

	if _, err := c.Prepare(context.Background(), pluginRequest()); !errors.Is(err, claudex.ErrNotFound) {
		t.Errorf("error = %v, want it to wrap ErrNotFound", err)
	}
}

// pluginTree writes a plugin install path holding the named skills, so a
// reconcile has something real to walk and link at.
func pluginTree(t *testing.T, root string, names ...string) string {
	t.Helper()
	for _, n := range names {
		dir := filepath.Join(root, pluginSkillsDir, n)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "---\nname: " + n + "\ndescription: a skill\n---\n\nBody.\n"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// fanCfg is pluginCfg with real directories, for the tests that touch disk.
func fanCfg(t *testing.T) (target.Config, string) {
	t.Helper()
	agents := t.TempDir()
	cfg := target.Config{Targets: []target.Target{
		{Name: "claude", Dir: filepath.Join(agents, "claude"), Plugins: true},
		{Name: "codex", Dir: filepath.Join(agents, "codex")},
	}}
	return cfg, agents
}

func TestFanLinksEverySkillIntoEveryAgentThatCannotInstallPlugins(t *testing.T) {
	cfg, _ := fanCfg(t)
	rev := pluginTree(t, t.TempDir(), "alpha", "beta")
	c := NewPlugin(&fakeClaude{}, cfg)
	r := state.Receipt{Name: "superpowers", RevPath: rev}

	p, links, skipped, err := c.fan(r, cfg.Targets)
	if err != nil {
		t.Fatalf("fan: %v", err)
	}
	if len(skipped) != 0 {
		t.Errorf("nothing was in the way, but got skips: %v", skipped)
	}
	if len(p.Ops) != 2 {
		t.Fatalf("ops = %v, want one link per skill for codex alone", p.Describe())
	}
	for _, l := range links {
		if l.Target != "codex" {
			t.Errorf("linked into %s: claude installed the plugin and can already see it", l.Target)
		}
	}
	if len(links) != 2 {
		t.Fatalf("links = %v, want one per skill", links)
	}
	want := filepath.Join(cfg.Targets[1].Dir, "alpha")
	if links[0].Path != want {
		t.Errorf("links[0].Path = %q, want %q", links[0].Path, want)
	}
}

func TestFanRelinksASkillWhoseVersionDirectoryMoved(t *testing.T) {
	cfg, _ := fanCfg(t)
	old := pluginTree(t, t.TempDir(), "alpha")
	newRev := pluginTree(t, t.TempDir(), "alpha")

	linkPath := filepath.Join(cfg.Targets[1].Dir, "alpha")
	if err := os.MkdirAll(cfg.Targets[1].Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(old, pluginSkillsDir, "alpha"), linkPath); err != nil {
		t.Fatal(err)
	}

	c := NewPlugin(&fakeClaude{}, cfg)
	r := state.Receipt{
		Name:    "superpowers",
		RevPath: newRev,
		Links:   []state.Link{{Target: "codex", Path: linkPath}},
	}

	p, links, _, err := c.fan(r, cfg.Targets)
	if err != nil {
		t.Fatalf("fan: %v", err)
	}
	if len(p.Ops) != 1 {
		t.Fatalf("ops = %v, want one relink", p.Describe())
	}
	op, ok := p.Ops[0].(plan.Relink)
	if !ok {
		t.Fatalf("op = %T, want plan.Relink: a stale link keeps serving the old version rather than dangling", p.Ops[0])
	}
	if op.RevPath != filepath.Join(newRev, pluginSkillsDir, "alpha") {
		t.Errorf("relinked to %q, want the new version directory", op.RevPath)
	}
	if len(links) != 1 {
		t.Errorf("links = %v, want the one it re-pointed", links)
	}
}

func TestFanUnlinksASkillThePluginNoLongerShips(t *testing.T) {
	cfg, _ := fanCfg(t)
	rev := pluginTree(t, t.TempDir(), "alpha")
	gone := filepath.Join(cfg.Targets[1].Dir, "beta")

	c := NewPlugin(&fakeClaude{}, cfg)
	r := state.Receipt{
		Name:    "superpowers",
		RevPath: rev,
		Links: []state.Link{
			{Target: "codex", Path: filepath.Join(cfg.Targets[1].Dir, "alpha")},
			{Target: "codex", Path: gone},
		},
	}

	p, links, _, err := c.fan(r, cfg.Targets)
	if err != nil {
		t.Fatalf("fan: %v", err)
	}

	var unlinked []string
	for _, op := range p.Ops {
		if u, ok := op.(plan.Unlink); ok {
			unlinked = append(unlinked, u.LinkPath)
		}
	}
	if len(unlinked) != 1 || unlinked[0] != gone {
		t.Fatalf("unlinked = %v, want just %q", unlinked, gone)
	}
	for _, l := range links {
		if l.Path == gone {
			t.Error("a skill the plugin stopped shipping stayed in the removal contract")
		}
	}
}

func TestFanSkipsANameSomethingElseAlreadyHolds(t *testing.T) {
	cfg, _ := fanCfg(t)
	rev := pluginTree(t, t.TempDir(), "alpha", "beta")

	if err := os.MkdirAll(cfg.Targets[1].Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	if err := os.Symlink(other, filepath.Join(cfg.Targets[1].Dir, "alpha")); err != nil {
		t.Fatal(err)
	}

	c := NewPlugin(&fakeClaude{}, cfg)
	r := state.Receipt{Name: "superpowers", RevPath: rev}

	p, links, skipped, err := c.fan(r, cfg.Targets)
	if err != nil {
		t.Fatalf("fan: %v", err)
	}
	if len(skipped) != 1 || !strings.Contains(skipped[0], "alpha") {
		t.Fatalf("skipped = %v, want one line naming alpha", skipped)
	}
	if len(p.Ops) != 1 {
		t.Fatalf("ops = %v, want beta linked anyway: one taken name must not cost the others", p.Describe())
	}
	for _, l := range links {
		if strings.HasSuffix(l.Path, "alpha") {
			t.Error("recorded a link to a symlink somebody else made")
		}
	}
}

func TestFanLeavesAgentsItWasNotAskedAboutAlone(t *testing.T) {
	cfg, _ := fanCfg(t)
	rev := pluginTree(t, t.TempDir(), "alpha")
	held := state.Link{Target: "gemini", Path: "/agents/gemini/skills/alpha"}

	c := NewPlugin(&fakeClaude{}, cfg)
	r := state.Receipt{Name: "superpowers", RevPath: rev, Links: []state.Link{held}}

	_, links, _, err := c.fan(r, cfg.Targets)
	if err != nil {
		t.Fatalf("fan: %v", err)
	}

	var kept bool
	for _, l := range links {
		if l == held {
			kept = true
		}
	}
	if !kept {
		t.Error("reconciling codex dropped gemini's link: an agent not in add keeps what it had")
	}
}

func TestFanTreatsAPluginWithNoSkillsDirectoryAsPublishingNone(t *testing.T) {
	cfg, _ := fanCfg(t)
	c := NewPlugin(&fakeClaude{}, cfg)
	r := state.Receipt{Name: "empty", RevPath: t.TempDir()}

	p, _, _, err := c.fan(r, cfg.Targets)
	if err != nil {
		t.Fatalf("a plugin that ships no skills has nothing to fan out, not an error: %v", err)
	}
	if !p.IsEmpty() {
		t.Errorf("ops = %v, want none", p.Describe())
	}
}

func TestFanRefusesAnInstallPathThatIsNotThere(t *testing.T) {
	cfg, _ := fanCfg(t)
	c := NewPlugin(&fakeClaude{}, cfg)
	r := state.Receipt{Name: "superpowers", RevPath: filepath.Join(t.TempDir(), "gone")}

	if _, _, _, err := c.fan(r, cfg.Targets); err == nil {
		t.Error("linking into a directory that is not there would make every link dangle")
	}
}

// TestFanRefusesASkillNameThatWouldEscapeTheSkillsDirectory pins the
// path-safety rule AGENTS.md requires of anything that turns third-party data
// into a path: a plugin's SKILL.md is somebody else's file, and its frontmatter
// name is what fan joins onto an agent's skills directory. pluginTree always
// writes a frontmatter name matching the directory, so this writes one by hand
// with a mismatched, escaping name instead.
func TestFanRefusesASkillNameThatWouldEscapeTheSkillsDirectory(t *testing.T) {
	cfg, _ := fanCfg(t)
	rev := t.TempDir()
	dir := filepath.Join(rev, pluginSkillsDir, "alpha")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: ../evil\ndescription: a skill\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	c := NewPlugin(&fakeClaude{}, cfg)
	r := state.Receipt{Name: "superpowers", RevPath: rev}

	if _, _, _, err := c.fan(r, cfg.Targets); err == nil {
		t.Error("a frontmatter name that would escape the skills directory must not become a link path")
	}
}

// TestFanRefusesToReplaceARealDirectoryAtTheLinkPath pins the other arm of
// linkOpFor's os.Readlink switch: a path that exists but is not a symlink
// returns EINVAL, not an os.IsNotExist error, and that distinction is the only
// thing stopping fan from planning over a directory the user made themselves.
// The existing collision test puts a symlink in the way, which takes the
// default arm instead; this one puts a real directory there to take the EINVAL
// arm.
func TestFanRefusesToReplaceARealDirectoryAtTheLinkPath(t *testing.T) {
	cfg, _ := fanCfg(t)
	rev := pluginTree(t, t.TempDir(), "alpha", "beta")

	linkPath := filepath.Join(cfg.Targets[1].Dir, "alpha")
	if err := os.MkdirAll(linkPath, 0o755); err != nil {
		t.Fatal(err)
	}

	c := NewPlugin(&fakeClaude{}, cfg)
	r := state.Receipt{Name: "superpowers", RevPath: rev}

	p, links, skipped, err := c.fan(r, cfg.Targets)
	if err != nil {
		t.Fatalf("fan: %v", err)
	}
	if len(skipped) != 1 || !strings.Contains(skipped[0], "alpha") {
		t.Fatalf("skipped = %v, want one line naming alpha", skipped)
	}
	if len(p.Ops) != 1 {
		t.Fatalf("ops = %v, want beta linked anyway: a directory in the way must not block the other skill", p.Describe())
	}
	for _, l := range links {
		if strings.HasSuffix(l.Path, "alpha") {
			t.Error("recorded a link to a path that is a real directory, not a symlink skillsctl made")
		}
	}
}

// TestFanIsIdempotentOnceItsOpsAreApplied pins the property later tasks rely
// on: install and update both call fan after every run, so a second call with
// unchanged inputs and the first call's ops actually applied must plan
// nothing.
func TestFanIsIdempotentOnceItsOpsAreApplied(t *testing.T) {
	cfg, _ := fanCfg(t)
	rev := pluginTree(t, t.TempDir(), "alpha", "beta")
	c := NewPlugin(&fakeClaude{}, cfg)
	r := state.Receipt{Name: "superpowers", RevPath: rev}

	p, links, skipped, err := c.fan(r, cfg.Targets)
	if err != nil {
		t.Fatalf("fan: %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped = %v, want none", skipped)
	}

	e := &plan.Executor{DB: &state.DB{Receipts: map[string]*state.Receipt{}}, Out: io.Discard}
	if err := e.Apply(context.Background(), p); err != nil {
		t.Fatalf("apply: %v", err)
	}

	r.Links = links
	p2, _, skipped2, err := c.fan(r, cfg.Targets)
	if err != nil {
		t.Fatalf("fan (second call): %v", err)
	}
	if !p2.IsEmpty() {
		t.Errorf("second plan = %v, want none: fan must be idempotent once its ops are applied", p2.Describe())
	}
	if len(skipped2) != 0 {
		t.Errorf("skipped (second call) = %v, want none", skipped2)
	}
}
