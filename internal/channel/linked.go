package channel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/target"
)

// linked supplies the removal contract for every channel whose skills reach an
// agent through a symlink skillsctl created. The links a receipt records are
// the complete account of what to undo, which is what lets remove be exact
// rather than inferring anything from the filesystem.
//
// Both the git and local channels embed it: they differ in where the files come
// from, not in how they reach an agent or how they stop reaching it. The plugin
// channel does not, because its removal contract is two things at once — the
// agent's uninstall command for the agent that installed it, and links for
// everyone else — and because one plugin receipt holds many links per agent
// rather than one.
type linked struct{}

// Remove unlinks the receipt from the agents in drop, and forgets it when that
// was the last of them. An empty drop means every agent.
//
// An empty plan means nothing in drop was linked; the caller reports that,
// because it is the one that knows what the user typed.
func (linked) Remove(r state.Receipt, drop map[string]bool) (plan.Plan, error) {
	var p plan.Plan
	var keep []state.Link

	for _, l := range r.Links {
		if len(drop) > 0 && !drop[l.Target] {
			keep = append(keep, l)
			continue
		}
		p.Add(plan.Unlink{Target: l.Target, LinkPath: l.Path})
	}
	if p.IsEmpty() {
		return p, nil
	}

	if len(keep) == 0 {
		p.Add(plan.Forget{Name: r.Name})
	} else {
		updated := r
		updated.Links = keep
		updated.UpdatedAt = time.Now().UTC()
		p.Add(plan.Record{Receipt: updated})
	}
	return p, nil
}

// Link adds the receipt to the agents in add, and is Remove read backwards: the
// same links that are the removal contract are what this appends to.
//
// A channel that embeds linked has one skill at one known path, so the wider
// reconciliation contract collapses to addition here: there is never a link to
// re-point or take away.
//
// An empty plan means every target already had it, which the caller reports for
// the same reason it reports an empty Remove — it is the one that knows what the
// user typed. Nothing is ever skipped for a reason worth printing, so the
// reasons are always nil.
func (linked) Link(r state.Receipt, add []target.Target) (plan.Plan, []string, error) {
	var p plan.Plan

	// target.Link would create the symlink whether or not anything is on the
	// other end of it, and a dangling entry in a skills directory is worse than
	// a refusal: the agent finds it, fails to load it, and says nothing useful.
	if fi, err := os.Stat(r.RevPath); err != nil || !fi.IsDir() {
		return p, nil, fmt.Errorf("refusing to link %s: %s is not a directory, so every new link would dangle", r.Name, r.RevPath)
	}

	// Links is a set keyed by the path a link sits at: Unlink treats a missing
	// link as success, so two entries naming one path would plan two unlinks of
	// it and swallow the second. A receipt for a single skill has one path per
	// agent, which is why this reads as one link per target.
	held := make(map[string]bool, len(r.Links))
	for _, l := range r.Links {
		held[l.Path] = true
	}

	// The receipt is the caller's, and Links shares its backing array with it.
	updated := r
	updated.Links = make([]state.Link, len(r.Links), len(r.Links)+len(add))
	copy(updated.Links, r.Links)

	for _, t := range add {
		linkPath, err := linkPathFor(t, r.Name)
		if err != nil {
			return plan.Plan{}, nil, err
		}
		if held[linkPath] {
			continue
		}
		p.Add(plan.Link{Target: t.Name, LinkPath: linkPath, RevPath: r.RevPath})
		updated.Links = append(updated.Links, state.Link{Target: t.Name, Path: linkPath})
		held[linkPath] = true
	}
	if p.IsEmpty() {
		return p, nil, nil
	}

	updated.UpdatedAt = time.Now().UTC()
	p.Add(plan.Record{Receipt: updated})
	return p, nil, nil
}

// linkPathFor is where a skill's symlink goes in one agent's skills directory.
//
// A skill's name arrives from a third party's SKILL.md and becomes a path, so
// it is checked every time it is joined rather than trusted because it was
// checked once at install.
func linkPathFor(t target.Target, name string) (string, error) {
	linkPath := filepath.Join(t.Dir, name)
	if filepath.Dir(linkPath) != filepath.Clean(t.Dir) {
		return "", fmt.Errorf("refusing to link %q: it would resolve outside %s", name, t.Dir)
	}
	return linkPath, nil
}

// Rollback refuses: this channel's files are the user's own, with no
// revision history skillsctl can swap back to. Git and OCI, which embed
// linked too, override this with the real thing.
func (linked) Rollback(context.Context, state.Receipt) (plan.Plan, Verdict, error) {
	return plan.Plan{}, Verdict{}, ErrRollbackUnsupported
}

// Agents reads the links, which are the record of where this skill was put.
func (linked) Agents(r state.Receipt) []string {
	names := make([]string, 0, len(r.Links))
	for _, l := range r.Links {
		names = append(names, l.Target)
	}
	return names
}
