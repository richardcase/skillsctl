package channel

import (
	"context"
	"errors"
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
