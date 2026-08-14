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

// undoStep reverses one op this apply carried out. The path is kept alongside
// the function so a rollback that itself fails can say which link it is
// warning about.
type undoStep struct {
	path string
	fn   func() error
}

// Apply runs every op in order. If one fails, the filesystem changes this apply
// made are undone before the error is returned: a symlink it created is
// removed, and one it re-pointed goes back to the revision it came from.
func (e *Executor) Apply(ctx context.Context, p Plan) error {
	var undo []undoStep

	rollback := func() {
		for i := len(undo) - 1; i >= 0; i-- {
			if err := undo[i].fn(); err != nil {
				_, _ = fmt.Fprintf(e.Out, "warning: could not roll back %s: %v\n", undo[i].path, err)
			}
		}
	}

	for _, op := range p.Ops {
		var err error
		switch o := op.(type) {
		case Link:
			var created bool
			created, err = target.Link(o.LinkPath, o.RevPath)
			if err == nil && created {
				undo = append(undo, undoStep{path: o.LinkPath, fn: func() error { return target.Unlink(o.LinkPath) }})
			}
		case Relink:
			var previous string
			previous, err = target.Relink(o.LinkPath, o.RevPath)
			if err == nil && previous != o.RevPath {
				// Restore what was actually there, not what the op expected:
				// a link that had drifted goes back where it was found.
				undo = append(undo, undoStep{path: o.LinkPath, fn: func() error {
					if previous == "" {
						return target.Unlink(o.LinkPath)
					}
					_, rerr := target.Relink(o.LinkPath, previous)
					return rerr
				}})
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
