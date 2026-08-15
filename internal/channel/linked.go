package channel

import (
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
// channel does not, because the agent installed its own files and there are no
// links to read.
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
// An empty plan means every target already had it, which the caller reports for
// the same reason it reports an empty Remove — it is the one that knows what the
// user typed.
func (linked) Link(r state.Receipt, add []target.Target) (plan.Plan, error) {
	var p plan.Plan

	// target.Link would create the symlink whether or not anything is on the
	// other end of it, and a dangling entry in a skills directory is worse than
	// a refusal: the agent finds it, fails to load it, and says nothing useful.
	if fi, err := os.Stat(r.RevPath); err != nil || !fi.IsDir() {
		return p, fmt.Errorf("refusing to link %s: %s is not a directory, so every new link would dangle", r.Name, r.RevPath)
	}

	// Links is a set keyed by target: Remove builds its drop filter from the
	// target name and Unlink treats a missing link as success, so a second link
	// for one agent would plan two unlinks of one path and swallow the second.
	held := make(map[string]bool, len(r.Links))
	for _, l := range r.Links {
		held[l.Target] = true
	}

	// The receipt is the caller's, and Links shares its backing array with it.
	updated := r
	updated.Links = make([]state.Link, len(r.Links), len(r.Links)+len(add))
	copy(updated.Links, r.Links)

	for _, t := range add {
		if held[t.Name] {
			continue
		}
		linkPath, err := linkPathFor(t, r.Name)
		if err != nil {
			return plan.Plan{}, err
		}
		p.Add(plan.Link{Target: t.Name, LinkPath: linkPath, RevPath: r.RevPath})
		updated.Links = append(updated.Links, state.Link{Target: t.Name, Path: linkPath})
		held[t.Name] = true
	}
	if p.IsEmpty() {
		return p, nil
	}

	updated.UpdatedAt = time.Now().UTC()
	p.Add(plan.Record{Receipt: updated})
	return p, nil
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

// Agents reads the links, which are the record of where this skill was put.
func (linked) Agents(r state.Receipt) []string {
	names := make([]string, 0, len(r.Links))
	for _, l := range r.Links {
		names = append(names, l.Target)
	}
	return names
}
