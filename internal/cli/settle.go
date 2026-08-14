package cli

import (
	"context"

	"github.com/richardcase/skillsctl/internal/channel"
	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/state"
)

// settle completes receipts whose fields are knowable only after the plan has
// run — a version an agent chose, a path it decided on — and returns rs with
// those receipts replaced, so the caller reports what was actually recorded.
//
// The completed receipts are written by applying a second plan rather than by
// assigning into the DB, so the executor stays the only thing that mutates
// receipts and a settle is as inspectable as the install that preceded it.
//
// An error here is not fatal to the command that called it. The caller commits
// what it has and reports the failure, because a receipt missing its version
// still names the skill, and one that was never written leaves an install
// nothing can undo.
func settle(ctx context.Context, ex *plan.Executor, ch channel.Channel, rs []state.Receipt) ([]state.Receipt, error) {
	// A channel that could complete some receipts and not others returns both
	// the ones it settled and the reason for the rest, so a partial answer is
	// still recorded.
	changed, serr := ch.Settle(ctx, rs)
	if len(changed) == 0 {
		return rs, serr
	}

	var p plan.Plan
	for _, r := range changed {
		p.Add(plan.Record{Receipt: r})
	}
	if err := ex.Apply(ctx, p); err != nil {
		if serr == nil {
			serr = err
		}
		return rs, serr
	}

	byName := make(map[string]state.Receipt, len(changed))
	for _, r := range changed {
		byName[r.Name] = r
	}
	out := make([]state.Receipt, 0, len(rs))
	for _, r := range rs {
		if settled, ok := byName[r.Name]; ok {
			out = append(out, settled)
			continue
		}
		out = append(out, r)
	}
	return out, serr
}
