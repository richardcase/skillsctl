package channel

import (
	"errors"
	"fmt"
	"time"

	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/state"
)

// Pinning is a receipt-only mutation: it freezes the revision a skill is
// already on, so nothing is fetched, nothing is extracted and no symlink moves.
// The whole plan is the record, which is what makes it impossible for pin to
// contain a destructive op rather than merely unlikely to — the same shape
// adopt arrived at, for the same reason.
//
// All three implementations live here, beside each other, because two of them
// are refusals and a refusal only makes sense next to the thing it is refusing
// to do.

// PinOptions says which way the pin is being moved.
type PinOptions struct {
	// On pins; false releases the pin.
	On bool
	// Ref is what the skill tracks once it is released. Empty means the
	// repository's default branch, which is what Update already reads an empty
	// ref as. It means nothing when On is true: a pinned receipt tracks
	// nothing.
	Ref string
}

// PinResult is what a pin or unpin did to one receipt.
type PinResult struct {
	// Receipt is the receipt as it now stands, unchanged when Changed is false.
	Receipt state.Receipt
	// Changed is false when the receipt was already in the state that was
	// asked for, which is reported rather than treated as an error.
	Changed bool
	// Note carries something worth saying about a receipt that did change.
	Note string
}

// Pin freezes this receipt at the revision it is already on, or releases it to
// track a ref again.
//
// Everything else the user chose survives: the name, the agents, the revision
// and the links to it. Only the pin, the ref it implies, and the timestamp
// move.
func (c *Git) Pin(r state.Receipt, o PinOptions) (plan.Plan, PinResult, error) {
	var p plan.Plan

	if r.Pinned == o.On {
		return p, PinResult{Receipt: r}, nil
	}

	res := PinResult{Receipt: r, Changed: true}
	res.Receipt.Pinned = o.On
	res.Receipt.UpdatedAt = time.Now().UTC()

	if o.On {
		// Install clears the ref when it pins, and Update reads an empty ref
		// as the default branch, which is what lets a named update of a pin
		// resolve against something. A pin made after the fact records the
		// same shape.
		res.Receipt.Ref = ""
	} else {
		res.Receipt.Ref = o.Ref
		// A revision outside the store is a working copy adopt took over. The
		// pin is what stopped a plain update re-pointing that symlink into the
		// store, so releasing it is worth saying out loud.
		if !c.store.Contains(r.RevPath) {
			res.Note = fmt.Sprintf("its files are at %s, and the next update will re-point the symlinks into skillsctl's store", r.RevPath)
		}
	}

	p.Add(plan.Record{Receipt: res.Receipt})
	return p, res, nil
}

// Pin refuses: a local skill is whatever is in its directory right now, so
// there is no revision to freeze and none to release it back to. Recording a
// pin on one would let somebody believe they had frozen a directory they are
// still editing, which is what rejectRevisionFlags refuses at install time.
//
// Neither refusal names the skill. Both reach the user through a report that
// has already named it, and the reason is the part that is not obvious.
func (c *Local) Pin(state.Receipt, PinOptions) (plan.Plan, PinResult, error) {
	return plan.Plan{}, PinResult{}, errors.New("a local skill is whatever is in its directory right now, so there is no revision to pin")
}

// Pin refuses: the agent installs a plugin and decides which version it holds,
// so skillsctl has nothing it could freeze.
func (c *Plugin) Pin(state.Receipt, PinOptions) (plan.Plan, PinResult, error) {
	return plan.Plan{}, PinResult{}, errors.New("claude decides which version of a plugin is installed, so skillsctl cannot pin one")
}

// Pin freezes this receipt at the digest it is already on, or releases it to
// track a tag again. Identical in shape to Git.Pin — a tag stands in for a
// branch, a digest for a sha.
func (c *OCI) Pin(r state.Receipt, o PinOptions) (plan.Plan, PinResult, error) {
	var p plan.Plan

	if r.Pinned == o.On {
		return p, PinResult{Receipt: r}, nil
	}

	res := PinResult{Receipt: r, Changed: true}
	res.Receipt.Pinned = o.On
	res.Receipt.UpdatedAt = time.Now().UTC()

	if o.On {
		res.Receipt.Ref = ""
	} else {
		res.Receipt.Ref = o.Ref
		if !c.store.Contains(r.RevPath) {
			res.Note = fmt.Sprintf("its files are at %s, and the next update will re-point the symlinks into skillsctl's store", r.RevPath)
		}
	}

	p.Add(plan.Record{Receipt: res.Receipt})
	return p, res, nil
}
