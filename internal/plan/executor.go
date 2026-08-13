package plan

import (
	"context"
	"fmt"
	"io"
	"os/exec"

	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/target"
)

// Executor applies a plan. Receipt changes land in DB but are not persisted:
// the caller commits the state handle only after Apply returns nil, so a
// failed apply leaves the on-disk receipts untouched.
type Executor struct {
	DB  *state.DB
	Out io.Writer

	// Run executes an Exec op. Defaults to os/exec; tests inject a fake.
	Run func(ctx context.Context, argv []string) error
}

// Apply runs every op in order. If one fails, symlinks created by this apply
// are removed before the error is returned.
func (e *Executor) Apply(ctx context.Context, p Plan) error {
	var linked []string

	rollback := func() {
		for i := len(linked) - 1; i >= 0; i-- {
			if err := target.Unlink(linked[i]); err != nil {
				_, _ = fmt.Fprintf(e.Out, "warning: could not roll back %s: %v\n", linked[i], err)
			}
		}
	}

	for _, op := range p.Ops {
		var err error
		switch o := op.(type) {
		case Link:
			if err = target.Link(o.LinkPath, o.RevPath); err == nil {
				linked = append(linked, o.LinkPath)
			}
		case Unlink:
			err = target.Unlink(o.LinkPath)
		case Record:
			r := o.Receipt
			e.DB.Receipts[r.Name] = &r
		case Forget:
			delete(e.DB.Receipts, o.Name)
		case Exec:
			err = e.run(ctx, o.Argv)
		default:
			err = fmt.Errorf("unknown op %T", op)
		}

		if err != nil {
			rollback()
			return fmt.Errorf("%s: %w", op.Describe(), err)
		}
	}
	return nil
}

func (e *Executor) run(ctx context.Context, argv []string) error {
	if e.Run != nil {
		return e.Run(ctx, argv)
	}
	if len(argv) == 0 {
		return fmt.Errorf("empty command")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout = e.Out
	cmd.Stderr = e.Out
	return cmd.Run()
}
