package cli

import (
	"context"

	"github.com/richardcase/skillsctl/internal/channel"
	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/target"
)

// relink makes each receipt's links agree with the receipt, after the plan that
// wrote it has run, and returns rs with the reconciled receipts replaced.
//
// It runs for every channel, which is what keeps install and update free of a
// branch on which one they are serving. A channel that made its links in its own
// plan finds them already right and returns an empty plan, so this costs it
// nothing; a channel whose agent chose the directory only knows where to point
// now, and this is where its links are made.
//
// An error here is not fatal to the command that called it, for the same reason
// a settle's is not: the plugin is installed either way, and a receipt that
// records it is worth more than one nothing wrote.
func relink(
	ctx context.Context,
	ex *plan.Executor,
	ch channel.Channel,
	rs []state.Receipt,
	targetsFor func(state.Receipt) []target.Target,
) ([]state.Receipt, []string, error) {
	out := make([]state.Receipt, 0, len(rs))
	var skipped []string
	var firstErr error

	for _, r := range rs {
		p, skips, err := ch.Link(r, targetsFor(r))
		skipped = append(skipped, skips...)
		if err == nil && !p.IsEmpty() {
			err = ex.Apply(ctx, p)
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			out = append(out, r)
			continue
		}

		// The plan's Record is what holds the new links, so the reconciled
		// receipt is read back from where the executor put it rather than
		// rebuilt here.
		if got, ok := ex.DB.Receipts[r.Name]; ok {
			out = append(out, *got)
			continue
		}
		out = append(out, r)
	}
	return out, skipped, firstErr
}
