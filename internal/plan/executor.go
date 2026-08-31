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
// the caller commits the state handle only after Apply returns nil. If an op
// partway through the plan fails, every change this call made — filesystem
// links and DB.Receipts alike — is undone before the error is returned, so a
// failed apply leaves both the filesystem and DB exactly as they were found.
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

// Apply runs every op in order. If one fails, the changes this apply made are
// undone before the error is returned: a symlink it created is removed, one
// it re-pointed goes back to the revision it came from, and a receipt it
// recorded or forgot goes back to what it was. Exec has no compensating
// action — an op that already ran an external command stays run.
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
			previous, existed := e.DB.Receipts[r.Name]
			e.DB.Receipts[r.Name] = &r
			undo = append(undo, undoStep{path: r.Name, fn: func() error {
				if existed {
					e.DB.Receipts[r.Name] = previous
				} else {
					delete(e.DB.Receipts, r.Name)
				}
				return nil
			}})
		case Forget:
			previous, existed := e.DB.Receipts[o.Name]
			delete(e.DB.Receipts, o.Name)
			if existed {
				undo = append(undo, undoStep{path: o.Name, fn: func() error {
					e.DB.Receipts[o.Name] = previous
					return nil
				}})
			}
		case Exec:
			err = e.run(ctx, o.Argv)
		case Note:
			// A note is the plan saying something it cannot yet do; there is
			// nothing to apply and nothing to roll back.
			_ = o
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
