package channel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/richardcase/skillsctl/internal/claudex"
	"github.com/richardcase/skillsctl/internal/discover"
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
//
// The skip reasons come back rather than as plan.Note ops: a Note is for
// something the plan cannot say yet, and a skip is known now, the same way
// Link's is. Folding it into a Note here would make the real run learn of it
// only when relink recomputes the same fan after Settle, which is exactly the
// dry-run/exit-code split commit 0947a06 fixed for Link.
func (c *Plugin) Install(req Request, chosen []Candidate) (plan.Plan, []state.Receipt, []string, error) {
	var p plan.Plan
	receipts := make([]state.Receipt, 0, len(chosen))
	var skipped []string
	now := time.Now().UTC()
	id := pluginID(req.Source)

	for _, s := range chosen {
		if !s.Adopted {
			p.Add(plan.Exec{Argv: c.claude.InstallArgv(id)})
		}

		// No slug and no content hash: nothing of ours is in the store. Resolved
		// and RevPath stay empty until Settle unless the plugin was already
		// installed, in which case they are known now and the plan describes
		// itself exactly.
		receipt := state.Receipt{
			Name:        s.Name,
			Channel:     string(source.ChannelPlugin),
			Source:      id,
			Resolved:    s.Version,
			RevPath:     s.Path,
			InstalledAt: now,
			UpdatedAt:   now,
		}

		// A plugin claude already has is the one case where the install path is
		// known before the plan runs, so its links go in the plan and the dry run
		// is exact. Otherwise claude decides the path and Settle reads it back,
		// and the reconcile that follows the apply is where the links are made —
		// which the plan says rather than leaving a third of its work unmentioned.
		if s.Adopted {
			ops, links, skips, err := c.fan(receipt, req.Targets)
			if err != nil {
				return plan.Plan{}, nil, nil, err
			}
			p.Add(ops.Ops...)
			skipped = append(skipped, skips...)
			receipt.Links = links
		} else if fanTo := target.WithoutPlugins(req.Targets); len(fanTo) > 0 {
			p.Add(plan.Note{Text: fmt.Sprintf("then link the skills %s ships into %s, once claude reports where it put them",
				s.Name, strings.Join(targetNames(fanTo), ", "))})
		}

		p.Add(plan.Record{Receipt: receipt})
		receipts = append(receipts, receipt)
	}
	return p, receipts, skipped, nil
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

		got, ok := find(installed, r.Source)
		if !ok {
			verdicts = append(verdicts, fail(v, fmt.Errorf(
				"claude no longer has %s installed: run `skillsctl remove %s` and install it again", r.Source, r.Name)))
			continue
		}

		// claude reports what it has now, before this update's exec runs. When
		// that already disagrees with the receipt — a `claude plugin update` run
		// outside skillsctl, say — this update's own "moved from X to Y" line
		// would otherwise read as contradicting what claude just said about
		// itself: both are true, but only one of them is news.
		if got.Version != "" && got.Version != r.Resolved {
			v.Note = fmt.Sprintf("claude was already at %s; skillsctl's record had fallen behind", got.Version)
		}

		p.Add(plan.Exec{Argv: c.claude.UpdateArgv(r.Source)})
		receipt := *r
		receipt.UpdatedAt = now
		p.Add(plan.Record{Receipt: receipt})

		// The version claude will land on, and so the directory the links must
		// follow, is not known until after the exec has run — the same reason
		// Install cannot put a fresh plugin's links in its own plan. A dry run
		// can still name who is affected: every agent this receipt already
		// reaches by symlink is reconciled once the real version is known.
		if agents := linkedAgents(*r); len(agents) > 0 {
			p.Add(plan.Note{Text: fmt.Sprintf("then re-point %s's links in %s once claude reports the version it moved to",
				r.Name, strings.Join(agents, ", "))})
		}

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

// Remove undoes the receipt for the agents in drop. An empty drop means every
// agent: uninstall the plugin through claude, take every link away, forget it.
//
// A subset is two contracts rather than one, so which agents were named decides
// which applies. An agent that only holds links loses them and the receipt
// stays, because the plugin is still installed. The agent that installed it
// cannot be singled out while anything is linked: uninstalling deletes the
// directory every other agent's links point into, so -a claude would have to
// either strand them or silently do more than the user asked for. Naming the
// command that does mean "everywhere" is better than either.
//
// The uninstall goes first because its failure is the more likely one. If claude
// refuses, the plan stops with the receipt intact and nothing is left dangling.
// If the uninstall succeeds but an unlink fails later, the plugin is gone, some
// links are removed, the rest dangle into a deleted directory, and the receipt is
// never committed — state.json goes on claiming a plugin claude no longer has.
// The order prevents the likely failure; the unlikely one is reported as-is.
func (c *Plugin) Remove(r state.Receipt, drop map[string]bool) (plan.Plan, error) {
	var p plan.Plan

	if len(drop) == 0 {
		p.Add(plan.Exec{Argv: c.claude.UninstallArgv(r.Source)})
		for _, l := range r.Links {
			p.Add(plan.Unlink{Target: l.Target, LinkPath: l.Path})
		}
		p.Add(plan.Forget{Name: r.Name})
		return p, nil
	}

	if owner := c.owner(drop); owner != "" {
		if len(r.Links) > 0 {
			return plan.Plan{}, fmt.Errorf("%s owns the %s plugin, and uninstalling it would strand its skills in %s\n"+
				"run `skillsctl remove %s` to remove it everywhere",
				owner, r.Name, strings.Join(linkedAgents(r), ", "), r.Name)
		}
		p.Add(plan.Exec{Argv: c.claude.UninstallArgv(r.Source)})
		p.Add(plan.Forget{Name: r.Name})
		return p, nil
	}

	var keep []state.Link
	for _, l := range r.Links {
		if !drop[l.Target] {
			keep = append(keep, l)
			continue
		}
		p.Add(plan.Unlink{Target: l.Target, LinkPath: l.Path})
	}
	if p.IsEmpty() {
		return p, nil
	}

	// The receipt survives however many links go, because the plugin itself is
	// still installed for the agent that owns it.
	updated := r
	updated.Links = keep
	updated.UpdatedAt = time.Now().UTC()
	p.Add(plan.Record{Receipt: updated})
	return p, nil
}

// Link makes the agents in add hold the skills this plugin ships.
//
// It is reconciliation rather than addition because the directory it links into
// moves: claude installs each version of a plugin beside the last and keeps the
// old one, so a link left alone would go on serving a version the receipt says
// was replaced. Install, update and link all reduce to this one call.
func (c *Plugin) Link(r state.Receipt, add []target.Target) (plan.Plan, []string, error) {
	p, links, skipped, err := c.fan(r, add)
	if err != nil {
		return plan.Plan{}, nil, err
	}
	if p.IsEmpty() {
		return p, skipped, nil
	}

	updated := r
	updated.Links = links
	updated.UpdatedAt = time.Now().UTC()
	p.Add(plan.Record{Receipt: updated})
	return p, skipped, nil
}

// owner names the plugin-installing agent among those the user asked to remove
// from, or "" if none was named.
func (c *Plugin) owner(drop map[string]bool) string {
	for _, t := range target.WithPlugins(c.cfg.Targets) {
		if drop[t.Name] {
			return t.Name
		}
	}
	return ""
}

// linkedAgents names the agents a receipt reaches by symlink, each once and in
// the order the links were made.
func linkedAgents(r state.Receipt) []string {
	seen := map[string]bool{}
	names := make([]string, 0, len(r.Links))
	for _, l := range r.Links {
		if seen[l.Target] {
			continue
		}
		seen[l.Target] = true
		names = append(names, l.Target)
	}
	return names
}

// targetNames is the agents' names, for a message.
func targetNames(ts []target.Target) []string {
	names := make([]string, 0, len(ts))
	for _, t := range ts {
		names = append(names, t.Name)
	}
	return names
}

// Agents names the agents this plugin is live in: the one that installed it,
// which only the config knows, together with the ones its skills were linked
// into, which only the receipt knows. Neither answers alone any more — claude
// holds the plugin without a link, and codex holds links without being able to
// install one.
//
// The order is the config's, so that list's agents column does not depend on the
// order the links happened to be made in.
func (c *Plugin) Agents(r state.Receipt) []string {
	linked := make(map[string]bool, len(r.Links))
	for _, l := range r.Links {
		linked[l.Target] = true
	}

	names := make([]string, 0, len(c.cfg.Targets))
	for _, t := range c.cfg.Targets {
		if t.Plugins || linked[t.Name] {
			names = append(names, t.Name)
		}
	}
	return names
}

// pluginSkillsDir is where a plugin keeps the skills it publishes. Only this
// subdirectory is walked: a plugin's root also holds commands, hooks, agents and
// its own tests, and a SKILL.md in any of those is not a skill the plugin
// publishes.
const pluginSkillsDir = "skills"

// pluginSkill is one skill a plugin publishes, under the name it will take in an
// agent's skills directory.
type pluginSkill struct {
	name string
	dir  string
}

// skills reads what a plugin publishes, from the install path claude reported.
//
// A plugin with no skills directory publishes none, which is not an error: it
// has nothing to fan out rather than something that failed to.
func (c *Plugin) skills(r state.Receipt) ([]pluginSkill, error) {
	// target.Link would create a symlink whether or not anything is on the other
	// end of it, and a dangling entry in a skills directory is worse than a
	// refusal: the agent finds it, fails to load it, and says nothing useful.
	if fi, err := os.Stat(r.RevPath); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("refusing to link %s: %s is not a directory, so every link would dangle", r.Name, r.RevPath)
	}

	dir := filepath.Join(r.RevPath, pluginSkillsDir)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil, nil
	}

	found, err := discover.Walk(dir)
	if err != nil {
		return nil, fmt.Errorf("read the skills %s ships: %w", r.Name, err)
	}

	out := make([]pluginSkill, 0, len(found))
	for _, s := range found {
		// A skill's name arrives from a third party's SKILL.md and becomes a
		// path. A plugin shipping one that cannot be a path is malformed rather
		// than merely inconvenient, so it stops the fan-out instead of being
		// skipped like a name that is merely taken.
		name := s.Name
		if name == "" {
			name = filepath.Base(s.Dir)
		}
		if err := target.ValidateSkillName(name); err != nil {
			return nil, fmt.Errorf("%s ships a skill at %s that cannot be linked: %w", r.Name, s.Rel, err)
		}
		out = append(out, pluginSkill{name: name, dir: s.Dir})
	}
	return out, nil
}

// linkOpFor decides what to do about one intended link by looking at what is
// already at its path.
//
// The receipt cannot answer this on its own: a state.Link records where a
// symlink is, not what it points at, and by the time this runs RevPath has
// already moved on to whatever claude last installed. So the filesystem is the
// authority, and the receipt only says which links are ours to re-point.
func linkOpFor(t target.Target, linkPath, dest string, ours bool) (plan.Op, string) {
	got, err := os.Readlink(linkPath)
	switch {
	case os.IsNotExist(err):
		return plan.Link{Target: t.Name, LinkPath: linkPath, RevPath: dest}, ""
	case err != nil:
		return nil, fmt.Sprintf("skipped %s for %s: %s is not a symlink skillsctl can replace",
			filepath.Base(linkPath), t.Name, linkPath)
	case got == dest:
		return nil, ""
	case ours:
		return plan.Relink{Target: t.Name, LinkPath: linkPath, RevPath: dest}, ""
	default:
		return nil, fmt.Sprintf("skipped %s for %s: %s already points at %s",
			filepath.Base(linkPath), t.Name, linkPath, got)
	}
}

// fan reconciles one receipt's links for the agents in add, and is the whole of
// what "this plugin's skills reach that agent" means. Install and Link share it
// so there is one definition of it; they differ only in whether the receipt is
// one they are about to write or one that already exists.
//
// The links it returns are the receipt's complete new set, not only the ones for
// add: an agent this call is not reconciling keeps what it had.
func (c *Plugin) fan(r state.Receipt, add []target.Target) (plan.Plan, []state.Link, []string, error) {
	var p plan.Plan

	// An agent that installs plugins is never linked: it can already see the
	// skills, so a symlink into its own cache would be a second name for
	// something it has.
	fanTo := target.WithoutPlugins(add)
	if len(fanTo) == 0 {
		return p, r.Links, nil, nil
	}

	skills, err := c.skills(r)
	if err != nil {
		return plan.Plan{}, nil, nil, err
	}

	touched := make(map[string]bool, len(fanTo))
	for _, t := range fanTo {
		touched[t.Name] = true
	}
	recorded := make(map[string]bool, len(r.Links))
	for _, l := range r.Links {
		recorded[l.Path] = true
	}

	links := make([]state.Link, 0, len(r.Links))
	for _, l := range r.Links {
		if !touched[l.Target] {
			links = append(links, l)
		}
	}

	var skipped []string
	live := map[string]bool{}
	for _, t := range fanTo {
		for _, s := range skills {
			linkPath, err := linkPathFor(t, s.name)
			if err != nil {
				return plan.Plan{}, nil, nil, err
			}
			// Two skills in one plugin claiming one name would otherwise plan
			// two links at one path and record it twice, which Unlink would
			// then undo once.
			if live[linkPath] {
				skipped = append(skipped, fmt.Sprintf("skipped %s for %s: %s ships two skills under that name",
					s.name, t.Name, r.Name))
				continue
			}
			live[linkPath] = true

			op, why := linkOpFor(t, linkPath, s.dir, recorded[linkPath])
			if why != "" {
				skipped = append(skipped, why)
				// A path that is not ours to take is recorded only if it
				// already was. The receipt must not start claiming a symlink
				// somebody else made, and must not stop claiming one it made
				// that somebody has since replaced.
				if recorded[linkPath] {
					links = append(links, state.Link{Target: t.Name, Path: linkPath})
				}
				continue
			}
			if op != nil {
				p.Add(op)
			}
			links = append(links, state.Link{Target: t.Name, Path: linkPath})
		}
	}

	// A skill the plugin has stopped shipping leaves a link into a version
	// directory claude keeps forever and the agent loads happily. It is the
	// reason this is reconciliation rather than addition.
	for _, l := range r.Links {
		if touched[l.Target] && !live[l.Path] {
			p.Add(plan.Unlink{Target: l.Target, LinkPath: l.Path})
		}
	}
	return p, links, skipped, nil
}

func find(installed []claudex.Installed, id string) (claudex.Installed, bool) {
	for _, p := range installed {
		if p.ID == id {
			return p, true
		}
	}
	return claudex.Installed{}, false
}
