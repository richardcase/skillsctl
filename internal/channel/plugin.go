package channel

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/richardcase/skillsctl/internal/claudex"
	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/target"
)

// Plugin installs Claude Code plugins by asking Claude Code to do it. The
// agent owns the files, so there is nothing to put in the store and nothing to
// symlink: a plugin's skills are already visible to the agent that installed
// it. What skillsctl adds is the receipt, so that list can see it and remove
// can undo it.
type Plugin struct {
	claude claudex.Plugins
	cfg    target.Config
}

// NewPlugin returns the plugin channel. It takes the whole agent config
// because a plugin receipt records no links: which agents hold a plugin is
// answered by the config rather than by the receipt.
func NewPlugin(p claudex.Plugins, cfg target.Config) *Plugin {
	return &Plugin{claude: p, cfg: cfg}
}

// Ownership reports that the agent holds the files and undoes them.
func (c *Plugin) Ownership() Ownership { return AgentOwned }

// pluginID is the plugin@marketplace form every claude plugin call takes, and
// what a receipt records as its source.
func pluginID(src source.Source) string { return src.Plugin + "@" + src.Marketplace }

// Prepare asks the agent what it already has. A plugin it has installed
// already is adopted rather than installed again, which makes install
// idempotent and gives a way to bring an existing plugin under skillsctl
// without uninstalling it first.
func (c *Plugin) Prepare(ctx context.Context, req Request) ([]Candidate, error) {
	if err := rejectRepositoryFlags(req); err != nil {
		return nil, err
	}
	if _, err := c.installFor(req.Targets); err != nil {
		return nil, err
	}

	installed, err := c.claude.List(ctx)
	if err != nil {
		return nil, err
	}

	cand := Candidate{Name: req.Source.DefaultName()}
	if got, ok := find(installed, pluginID(req.Source)); ok {
		cand.Adopted = true
		cand.Version = got.Version
		cand.Path = got.InstallPath
	}
	return []Candidate{cand}, nil
}

// rejectRepositoryFlags turns a flag that only means something for a git
// source into an error naming it. Ignoring --pin on a plugin would let someone
// believe they had frozen a version that claude is free to move.
func rejectRepositoryFlags(req Request) error {
	switch {
	case len(req.Skills) > 0:
		return fmt.Errorf("--skill picks one skill out of a repository, and a plugin is installed whole: drop --skill")
	case req.All:
		return fmt.Errorf("--all installs every skill in a repository, and a plugin is installed whole: drop --all")
	case req.Ref != "":
		return fmt.Errorf("--ref names a git revision, and a plugin's version is whatever its marketplace publishes: drop --ref")
	case req.Pin:
		return fmt.Errorf("--pin freezes a git revision, and claude decides which version of a plugin is installed: drop --pin")
	}
	return nil
}

// installFor narrows the requested agents to those that install plugins. A
// request naming only agents that do not is refused rather than quietly
// installing for someone else.
func (c *Plugin) installFor(ts []target.Target) ([]target.Target, error) {
	out := target.WithPlugins(ts)
	if len(out) == 0 {
		names := make([]string, 0, len(ts))
		for _, t := range ts {
			names = append(names, t.Name)
		}
		return nil, fmt.Errorf("none of the agents in play (%s) installs plugins: the plugin channel needs one with `plugins = true` in the config, which claude has by default",
			strings.Join(names, ", "))
	}
	return out, nil
}

// Install records the plugin, having asked claude to install it first unless
// claude already had it.
func (c *Plugin) Install(req Request, chosen []Candidate) (plan.Plan, []state.Receipt, error) {
	var p plan.Plan
	receipts := make([]state.Receipt, 0, len(chosen))
	now := time.Now().UTC()
	id := pluginID(req.Source)

	for _, s := range chosen {
		if !s.Adopted {
			p.Add(plan.Exec{Argv: c.claude.InstallArgv(id)})
		}

		// No links, no slug and no content hash: nothing of ours is on disk.
		// Resolved and RevPath stay empty until Settle unless the plugin was
		// already installed, in which case they are known now and the plan
		// describes itself exactly.
		receipt := state.Receipt{
			Name:        s.Name,
			Channel:     string(source.ChannelPlugin),
			Source:      id,
			Resolved:    s.Version,
			RevPath:     s.Path,
			InstalledAt: now,
			UpdatedAt:   now,
		}
		p.Add(plan.Record{Receipt: receipt})
		receipts = append(receipts, receipt)
	}
	return p, receipts, nil
}

// Update asks claude to move each plugin to its latest version.
//
// Nothing here can say in advance whether that will change anything: the CLI
// reports a version only for a plugin it has already installed. So every
// receipt is planned as an update with no Latest, Settle reads back what claude
// chose, and an update that moved nothing is reduced to "current" before it is
// reported.
func (c *Plugin) Update(ctx context.Context, rs []*state.Receipt, _ UpdateOptions) ([]Verdict, plan.Plan, error) {
	installed, err := c.claude.List(ctx)
	if err != nil {
		return nil, plan.Plan{}, err
	}

	verdicts := make([]Verdict, 0, len(rs))
	var p plan.Plan
	now := time.Now().UTC()

	for _, r := range rs {
		v := Verdict{Name: r.Name, Channel: r.Channel, Current: r.Resolved}

		if _, ok := find(installed, r.Source); !ok {
			verdicts = append(verdicts, fail(v, fmt.Errorf(
				"claude no longer has %s installed: run `skillsctl remove %s` and install it again", r.Source, r.Name)))
			continue
		}

		p.Add(plan.Exec{Argv: c.claude.UpdateArgv(r.Source)})
		receipt := *r
		receipt.UpdatedAt = now
		p.Add(plan.Record{Receipt: receipt})

		v.Status = StatusUpdated
		verdicts = append(verdicts, v)
	}
	return verdicts, p, nil
}

// Settle reads back the version and install path claude settled on, which is
// the only moment either can be known.
//
// A plugin claude does not report is an error rather than a silent gap: the
// receipt is committed regardless, because one missing its version still names
// a skill that remove can undo, but the user is told it happened.
func (c *Plugin) Settle(ctx context.Context, rs []state.Receipt) ([]state.Receipt, error) {
	if len(rs) == 0 {
		return nil, nil
	}

	installed, err := c.claude.List(ctx)
	if err != nil {
		return nil, err
	}

	changed := make([]state.Receipt, 0, len(rs))
	var missing []string
	for _, r := range rs {
		got, ok := find(installed, r.Source)
		if !ok {
			missing = append(missing, r.Name)
			continue
		}
		if got.Version == r.Resolved && got.InstallPath == r.RevPath {
			continue
		}
		r.Resolved = got.Version
		r.RevPath = got.InstallPath
		changed = append(changed, r)
	}

	if len(missing) > 0 {
		return changed, fmt.Errorf("claude reports no install for %s, so the version could not be recorded; `skillsctl list` will show it blank until the next update",
			strings.Join(missing, ", "))
	}
	return changed, nil
}

// Remove asks claude to uninstall the plugin and forgets the receipt.
//
// There is no partial removal to keep a receipt for: a plugin is installed once
// for the agent that owns plugins, so removing it from that agent removes it
// outright.
func (c *Plugin) Remove(r state.Receipt, drop map[string]bool) (plan.Plan, error) {
	var p plan.Plan
	if len(drop) > 0 && !c.named(drop) {
		return p, nil
	}
	p.Add(plan.Exec{Argv: c.claude.UninstallArgv(r.Source)})
	p.Add(plan.Forget{Name: r.Name})
	return p, nil
}

// Link refuses, because a plugin has no link of ours to duplicate: claude
// installed the plugin's own files into its own cache and can already see the
// skills in them.
//
// Fanning a plugin's skills out to other agents is a real feature and a
// different one — it would have to link out of somebody else's cache — so the
// error says that rather than calling it unsupported.
func (c *Plugin) Link(r state.Receipt, _ []target.Target) (plan.Plan, []string, error) {
	return plan.Plan{}, nil, fmt.Errorf("%s is a plugin, and a plugin's skills are already visible to %s without a symlink: "+
		"linking one into another agent is not supported yet",
		r.Name, strings.Join(c.Agents(r), ", "))
}

// named reports whether an agent that installs plugins was among those the
// user asked to remove from.
func (c *Plugin) named(drop map[string]bool) bool {
	for _, name := range c.Agents(state.Receipt{}) {
		if drop[name] {
			return true
		}
	}
	return false
}

// Agents answers from the config rather than from the receipt: a plugin
// receipt records no links because the agent that installed it can already see
// its skills.
func (c *Plugin) Agents(state.Receipt) []string {
	ts := target.WithPlugins(c.cfg.Targets)
	names := make([]string, 0, len(ts))
	for _, t := range ts {
		names = append(names, t.Name)
	}
	return names
}

func find(installed []claudex.Installed, id string) (claudex.Installed, bool) {
	for _, p := range installed {
		if p.ID == id {
			return p, true
		}
	}
	return claudex.Installed{}, false
}
