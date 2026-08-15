package manifest

import (
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/channel"
	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/store"
	"github.com/richardcase/skillsctl/internal/target"
)

// registry builds a real registry. Nothing here reaches the network: the only
// method these tests call is Agents, which reads the receipt.
func registry(t *testing.T) channel.Registry {
	t.Helper()
	st := store.New(t.TempDir())
	return channel.Registry{
		Git:   channel.NewGit(st, gitx.New()),
		Local: channel.NewLocal(st),
	}
}

func present() []target.Target {
	return []target.Target{{Name: "claude"}, {Name: "codex"}}
}

func gitReceipt(name string, targets ...string) *state.Receipt {
	r := &state.Receipt{
		Name:     name,
		Channel:  "git",
		Source:   "https://github.com/owner/repo.git",
		Ref:      "main",
		Resolved: "9f8e7d6c5b4a39281706f5e4d3c2b1a098765432",
	}
	for _, tn := range targets {
		r.Links = append(r.Links, state.Link{Target: tn, Path: "/agents/" + tn + "/" + name})
	}
	return r
}

func TestFromReceiptsOmitsAgentsWhenEveryPresentAgentHasIt(t *testing.T) {
	f, excluded := FromReceipts([]*state.Receipt{gitReceipt("alpha", "claude", "codex")}, registry(t), present())

	if len(excluded) != 0 {
		t.Errorf("nothing should have been excluded, got %v", excluded)
	}
	if len(f.Skills) != 1 {
		t.Fatalf("got %d skills, want 1", len(f.Skills))
	}
	if f.Skills[0].Agents != nil {
		t.Errorf("agents = %v, want it omitted when it repeats the default", f.Skills[0].Agents)
	}
	if f.Skills[0].Ref != "main" {
		t.Errorf("ref = %q, want main", f.Skills[0].Ref)
	}
}

func TestFromReceiptsKeepsANarrowerAgentSet(t *testing.T) {
	f, _ := FromReceipts([]*state.Receipt{gitReceipt("alpha", "claude")}, registry(t), present())

	if len(f.Skills) != 1 || strings.Join(f.Skills[0].Agents, ",") != "claude" {
		t.Errorf("agents = %v, want [claude] preserved as a deliberate choice", f.Skills[0].Agents)
	}
}

// A pinned receipt records no ref, so the sha is the only thing that can carry
// the pin to another machine.
func TestFromReceiptsPutsThePinnedShaInRef(t *testing.T) {
	r := gitReceipt("alpha", "claude", "codex")
	r.Pinned = true
	r.Ref = ""

	f, _ := FromReceipts([]*state.Receipt{r}, registry(t), present())

	if len(f.Skills) != 1 {
		t.Fatalf("got %d skills, want 1", len(f.Skills))
	}
	if !f.Skills[0].Pinned {
		t.Error("the pin was lost")
	}
	if f.Skills[0].Ref != r.Resolved {
		t.Errorf("ref = %q, want the resolved sha %q", f.Skills[0].Ref, r.Resolved)
	}
}

func TestFromReceiptsExcludesLocalSkills(t *testing.T) {
	local := &state.Receipt{
		Name:    "mine",
		Channel: "local",
		Source:  "/Users/me/code/mine",
		Links:   []state.Link{{Target: "claude", Path: "/agents/claude/mine"}},
	}

	f, excluded := FromReceipts([]*state.Receipt{local, gitReceipt("alpha", "claude", "codex")}, registry(t), present())

	if len(f.Skills) != 1 || f.Skills[0].Name != "alpha" {
		t.Errorf("skills = %+v, want only the git skill", f.Skills)
	}
	if len(excluded) != 1 || !strings.Contains(excluded[0], "mine") || !strings.Contains(excluded[0], "/Users/me/code/mine") {
		t.Errorf("excluded = %v, want the local skill named with its path", excluded)
	}
}

// A committed skills.toml has to produce a stable diff.
func TestFromReceiptsSortsByName(t *testing.T) {
	f, _ := FromReceipts([]*state.Receipt{
		gitReceipt("gamma", "claude", "codex"),
		gitReceipt("alpha", "claude", "codex"),
		gitReceipt("beta", "claude", "codex"),
	}, registry(t), present())

	var got []string
	for _, s := range f.Skills {
		got = append(got, s.Name)
	}
	if strings.Join(got, ",") != "alpha,beta,gamma" {
		t.Errorf("order = %v, want sorted by name", got)
	}
}

// A plugin's skills reach its agent without a symlink of ours, so which agents
// have it was never a choice the user made.
func TestFromReceiptsGivesAPluginNoAgents(t *testing.T) {
	p := &state.Receipt{Name: "some-plugin", Channel: "plugin", Source: "some-plugin@marketplace", Resolved: "1.2.0"}

	f, excluded := FromReceipts([]*state.Receipt{p}, registry(t), present())

	if len(excluded) != 0 {
		t.Errorf("a plugin is portable and must not be excluded, got %v", excluded)
	}
	if len(f.Skills) != 1 {
		t.Fatalf("got %d skills, want 1", len(f.Skills))
	}
	if f.Skills[0].Agents != nil {
		t.Errorf("agents = %v, want none for a plugin", f.Skills[0].Agents)
	}
	if f.Skills[0].Source != "some-plugin@marketplace" {
		t.Errorf("source = %q, want the plugin id", f.Skills[0].Source)
	}
}

func TestFromReceiptsCarriesTheSubpath(t *testing.T) {
	r := gitReceipt("alpha", "claude", "codex")
	r.Subpath = "skills/alpha"

	f, _ := FromReceipts([]*state.Receipt{r}, registry(t), present())

	if len(f.Skills) != 1 || f.Skills[0].Subpath != "skills/alpha" {
		t.Errorf("subpath = %q, want it carried", f.Skills[0].Subpath)
	}
}
