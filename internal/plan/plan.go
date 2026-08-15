// Package plan models a command's user-visible mutations as inspectable data,
// so that --dry-run is exact and tests can assert over op sequences.
package plan

import (
	"fmt"
	"strings"

	"github.com/richardcase/skillsctl/internal/state"
)

// Op is a single user-visible mutation.
type Op interface {
	Describe() string
}

// Link creates a symlink in an agent's skills directory.
type Link struct {
	Target   string
	LinkPath string
	RevPath  string
}

// Describe renders a user-visible description of the Link op.
func (o Link) Describe() string {
	return fmt.Sprintf("link    %s -> %s [%s]", o.LinkPath, o.RevPath, o.Target)
}

// Relink points an existing symlink at a different revision. Rolling one back
// is putting the previous revision back rather than removing the link:
// revision directories are immutable, so the one it moved away from is still
// there and still valid.
type Relink struct {
	Target   string
	LinkPath string
	RevPath  string
}

// Describe renders a user-visible description of the Relink op.
func (o Relink) Describe() string {
	return fmt.Sprintf("relink  %s -> %s [%s]", o.LinkPath, o.RevPath, o.Target)
}

// Unlink removes a symlink skillsctl created.
type Unlink struct {
	Target   string
	LinkPath string
}

// Describe renders a user-visible description of the Unlink op.
func (o Unlink) Describe() string {
	return fmt.Sprintf("unlink  %s [%s]", o.LinkPath, o.Target)
}

// Record writes a receipt.
type Record struct {
	Receipt state.Receipt
}

// Describe renders a user-visible description of the Record op. A receipt with
// no resolved revision is named without one rather than with an empty "@": a
// plugin's version is not knowable until the agent has installed it, and a
// dry-run should not imply otherwise.
func (o Record) Describe() string {
	if o.Receipt.Resolved == "" {
		return "record  " + o.Receipt.Name
	}
	return fmt.Sprintf("record  %s @ %s", o.Receipt.Name, short(o.Receipt.Resolved))
}

// Forget deletes a receipt.
type Forget struct {
	Name string
}

// Describe renders a user-visible description of the Forget op.
func (o Forget) Describe() string { return fmt.Sprintf("forget  %s", o.Name) }

// Exec shells out, used by the plugin channel.
type Exec struct {
	Argv []string
}

// Describe renders a user-visible description of the Exec op.
func (o Exec) Describe() string { return "exec    " + strings.Join(o.Argv, " ") }

// Note is a line in the plan that changes nothing.
//
// It exists for the one thing this tool cannot predict. A plugin's install path
// is decided by claude and read back afterwards, so the links that follow an
// install or an update cannot be named in the plan that precedes them. Printing
// nothing would leave a --dry-run silently short of what the command does, which
// is worse than printing a sentence that admits the gap.
type Note struct {
	Text string
}

// Describe renders the note, padded to the same column as every other op so a
// plan still reads as one list.
func (o Note) Describe() string { return "note    " + o.Text }

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// Plan is an ordered list of mutations.
type Plan struct {
	Ops []Op
}

// Add appends ops to the plan.
func (p *Plan) Add(ops ...Op) { p.Ops = append(p.Ops, ops...) }

// IsEmpty reports whether the plan would change nothing.
func (p Plan) IsEmpty() bool { return len(p.Ops) == 0 }

// Describe renders one line per op, for --dry-run.
func (p Plan) Describe() []string {
	out := make([]string, 0, len(p.Ops))
	for _, op := range p.Ops {
		out = append(out, op.Describe())
	}
	return out
}
