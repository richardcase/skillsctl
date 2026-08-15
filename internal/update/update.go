// Package update moves installed skills onto a newer revision of what they
// track. It selects the receipts a run applies to, hands each channel the ones
// it owns, and merges the verdicts back into the order the receipts arrived in.
//
// Deciding what a receipt should become is the channel's job, not this
// package's: a git skill tracks a ref that can move, a plugin is whatever the
// agent last installed, and neither knows about the other.
package update

import (
	"context"
	"fmt"

	"github.com/richardcase/skillsctl/internal/channel"
	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/state"
)

// Entry is the verdict for one receipt. It is channel.Verdict under another
// name: a channel decides a verdict, this package only orders them.
type Entry = channel.Verdict

// Status is the verdict itself.
type Status = channel.Status

// The statuses, re-exported so callers reporting on an update need only this
// package.
const (
	StatusUpdated = channel.StatusUpdated
	StatusCurrent = channel.StatusCurrent
	StatusPinned  = channel.StatusPinned
	StatusDirty   = channel.StatusDirty
	StatusSkipped = channel.StatusSkipped
	StatusError   = channel.StatusError
)

// Options narrows and loosens what Plan will do.
type Options struct {
	// Names selects skills by name. Empty means every receipt, minus the
	// pinned ones: a pin is only overridden by naming the skill.
	Names []string
	// Force updates a skill whose installed tree no longer matches the hash
	// recorded at install time.
	Force bool
}

// Plan returns one Entry per selected receipt, in the order they were given,
// and the plan that carries out every entry whose Status is StatusUpdated.
//
// Only a request that cannot be interpreted at all — a name that is not
// installed — comes back as an error. Everything else is a per-entry verdict,
// so one unreachable remote or one dirty skill never hides the rest.
func Plan(ctx context.Context, reg channel.Registry, receipts []*state.Receipt, o Options) ([]Entry, plan.Plan, error) {
	selected, err := selectReceipts(receipts, o.Names)
	if err != nil {
		return nil, plan.Plan{}, err
	}

	entries := make([]Entry, len(selected))
	opts := channel.UpdateOptions{Named: len(o.Names) > 0, Force: o.Force}

	// Receipts are grouped by channel so that each channel is asked once and
	// can answer them together — git resolves a repository once however many
	// skills came from it. They are keyed by the receipt's channel name rather
	// than by the channel value, so nothing here depends on a Channel
	// implementation being comparable.
	var order []string
	chans := map[string]channel.Channel{}
	grouped := map[string][]int{}

	for i, r := range selected {
		ch, cerr := reg.ForReceipt(r)
		if cerr != nil {
			// A channel that cannot update anything is not a failure. The
			// skill stays installed and stays as it is; the report says so.
			entries[i] = skipped(r)
			continue
		}
		if _, ok := chans[r.Channel]; !ok {
			chans[r.Channel] = ch
			order = append(order, r.Channel)
		}
		grouped[r.Channel] = append(grouped[r.Channel], i)
	}

	var p plan.Plan
	for _, name := range order {
		at := grouped[name]
		rs := make([]*state.Receipt, 0, len(at))
		for _, i := range at {
			rs = append(rs, selected[i])
		}

		verdicts, cp, err := chans[name].Update(ctx, rs, opts)
		if err != nil {
			return nil, plan.Plan{}, err
		}
		if len(verdicts) != len(rs) {
			return nil, plan.Plan{}, fmt.Errorf("the %s channel returned %d verdicts for %d skills", name, len(verdicts), len(rs))
		}
		for n, i := range at {
			entries[i] = verdicts[n]
		}
		p.Add(cp.Ops...)
	}

	return entries, p, nil
}

// Reconcile folds what a settle learned back into the entries a run will
// report, and is called between applying the plan and printing it.
//
// It exists for the channel that cannot say in advance whether an update will
// move anything. Such a channel plans every receipt as an update with no
// Latest; once the agent has run and the settled receipt carries the version it
// chose, an update that changed nothing becomes "current" — so a no-op neither
// prints a line nor pushes the command to a partial exit code.
func Reconcile(entries []Entry, settled []state.Receipt) []Entry {
	if len(settled) == 0 {
		return entries
	}

	byName := make(map[string]state.Receipt, len(settled))
	for _, r := range settled {
		byName[r.Name] = r
	}

	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		r, ok := byName[e.Name]
		if !ok || e.Status != StatusUpdated || e.Latest != "" {
			out = append(out, e)
			continue
		}
		e.Latest = r.Resolved
		if e.Latest == e.Current {
			e.Status = StatusCurrent
		}
		out = append(out, e)
	}
	return out
}

// skipped is the verdict for a receipt whose channel has nothing to update
// from, which is what the local channel and any future read-only one will be.
func skipped(r *state.Receipt) Entry {
	// An empty ref means the source's default, which is what a git receipt
	// would have resolved against; reporting it keeps the column populated.
	ref := r.Ref
	if ref == "" {
		ref = "HEAD"
	}
	return Entry{
		Name:    r.Name,
		Channel: r.Channel,
		Ref:     ref,
		Current: r.Resolved,
		Pinned:  r.Pinned,
		Status:  StatusSkipped,
	}
}

// selectReceipts narrows receipts to the names asked for. A name that is not
// installed is the one thing that fails the whole command: the user asked for
// something specific that does not exist, and carrying on would silently
// update a different set than the one they typed.
func selectReceipts(receipts []*state.Receipt, names []string) ([]*state.Receipt, error) {
	if len(names) == 0 {
		return receipts, nil
	}

	byName := make(map[string]*state.Receipt, len(receipts))
	for _, r := range receipts {
		byName[r.Name] = r
	}

	out := make([]*state.Receipt, 0, len(names))
	taken := make(map[string]bool, len(names))
	for _, name := range names {
		r, ok := byName[name]
		if !ok {
			return nil, state.NotInstalled(name, receipts)
		}
		// Naming a skill twice is one request for it, not two: planning it
		// twice would report it twice and re-link it over itself.
		if taken[name] {
			continue
		}
		taken[name] = true
		out = append(out, r)
	}
	return out, nil
}
