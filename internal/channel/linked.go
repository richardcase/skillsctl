package channel

import (
	"time"

	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/state"
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

// Agents reads the links, which are the record of where this skill was put.
func (linked) Agents(r state.Receipt) []string {
	names := make([]string, 0, len(r.Links))
	for _, l := range r.Links {
		names = append(names, l.Target)
	}
	return names
}
