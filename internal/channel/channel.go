// Package channel turns a parsed source, or an existing receipt, into the
// mutations that install, update or remove it.
//
// The channel is the only thing that differs between a skill fetched from a git
// repository and a plugin the agent installs on our behalf. Everything
// downstream is shared: the plan, the executor, the receipts, the exit codes.
// Keeping that difference behind one interface is what stops the same switch on
// the channel appearing in install, update, remove, list and gc.
package channel

import (
	"context"
	"errors"
	"fmt"

	"github.com/richardcase/skillsctl/internal/discover"
	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/target"
)

// Ownership says who owns the files an install produced. It is the only thing
// list, remove and gc need to know about a channel, and all they need to know:
// anything finer belongs behind one of the methods below.
type Ownership int

const (
	// StoreOwned means skillsctl extracted the files into its own store. Its
	// revision and mirror are live roots for gc.
	StoreOwned Ownership = iota
	// AgentOwned means the agent installed the files and owns them: skillsctl
	// records the install and undoes it through the agent, and nothing of ours
	// is in the store, so gc has nothing to count. It may still have made
	// symlinks — a plugin's skills are fanned out to the agents that cannot
	// install plugins — but they point outside the store, so this answer is
	// about what gc counts rather than about whether links exist.
	AgentOwned
	// UserOwned means the files are the user's own, in a directory they chose.
	// skillsctl links to them and records where they are; it never copies them,
	// never updates them, and on remove takes away only its own symlinks. Like
	// AgentOwned the store holds nothing, and like StoreOwned the links are the
	// removal contract — which is why there are three of these and not two.
	UserOwned
)

// String names an ownership for a report. The words are short because they are
// a field value, not a sentence: how each reads to a user is the caller's
// business.
func (o Ownership) String() string {
	switch o {
	case StoreOwned:
		return "store"
	case AgentOwned:
		return "agent"
	case UserOwned:
		return "user"
	}
	return "unknown"
}

// Request is one install invocation, already parsed.
type Request struct {
	Source  source.Source
	Targets []target.Target
	Skills  []string
	All     bool
	Ref     string
	Pin     bool
}

// Candidate is one installable unit a channel found: a skill inside a
// repository, or the plugin itself.
//
// Prepare fills in everything Install will need, so that Install is a pure
// function of the request and the candidates that survived the caller's
// name-collision check, and needs no state carried between the two.
type Candidate struct {
	// Name is what the skill will be recorded and linked under.
	Name string
	// Desc is one line about it, for the listing an ambiguous request prints.
	Desc string
	// Path is where the files are: a directory in the store for a channel that
	// owns them, the agent's own install path for one that does not.
	Path string
	// Subpath locates the skill within its source, "" when the source is the
	// skill.
	Subpath string
	// Version is the resolved revision: a sha, or a plugin version. Empty when
	// only the agent can say what it will be, which is what Settle is for.
	Version string
	// Hash fingerprints the tree at install time, so a later update can tell
	// whether it has been edited since. Empty when the agent owns the tree and
	// its dirtiness is not ours to judge.
	Hash string
	// Adopted marks a candidate the agent has already installed, so Install
	// records it rather than installing it a second time.
	Adopted bool
}

// Ambiguous reports a request that names more than one candidate without
// saying which is wanted. It carries the candidates rather than printing them:
// how a listing looks is cli's business, and no channel holds a command.
type Ambiguous struct {
	Header    string
	Meta      discover.Metadata
	Available []Candidate
	Reason    string
	// Resolved is the revision the listing describes. A caller that answers
	// the ambiguity by asking the user can put it back in Request.Ref to
	// narrow against the same tree it showed them, rather than resolving the
	// ref a second time and possibly getting one that has moved since. Empty
	// for a channel with no revision to name.
	Resolved string
}

// Error renders the reason the request could not be narrowed.
func (e *Ambiguous) Error() string { return e.Reason }

// Status is a channel's verdict on one receipt during an update.
type Status string

const (
	// StatusUpdated means the source has moved and the plan re-links the skill.
	StatusUpdated Status = "updated"
	// StatusCurrent means the installed revision is still the current one.
	StatusCurrent Status = "current"
	// StatusPinned means the skill is pinned and was not named explicitly.
	StatusPinned Status = "pinned"
	// StatusDirty means the installed tree was edited since it was installed.
	StatusDirty Status = "dirty"
	// StatusSkipped means the receipt has no upstream to update from.
	StatusSkipped Status = "n/a"
	// StatusError means this skill could not be updated.
	StatusError Status = "error"
)

// Verdict is a channel's decision about one receipt.
type Verdict struct {
	Name    string
	Channel string
	Ref     string
	Current string
	Latest  string
	Pinned  bool
	Status  Status
	Error   string
	// Note carries something worth saying about an entry that was still
	// updated, such as a revision directory that had gone missing.
	Note string
}

// UpdateOptions loosens what Update will do.
type UpdateOptions struct {
	// Named is true when the user named skills explicitly, which is what
	// overrides a pin.
	Named bool
	// Force updates a skill whose tree no longer matches the hash recorded at
	// install time.
	Force bool
}

// Channel installs, updates and removes skills through one mechanism.
type Channel interface {
	Ownership() Ownership

	// Prepare does the read-only work an install needs before it can name what
	// it would change — resolving a ref and populating the cache, or asking the
	// agent what it already has — and narrows the result to what the request
	// asked for. A request it cannot narrow comes back as *Ambiguous.
	//
	// Prepare runs even for a --dry-run, so nothing it does may be visible to
	// the user. Populating a content-addressed cache qualifies; installing
	// something does not.
	Prepare(ctx context.Context, req Request) ([]Candidate, error)

	// Install turns the candidates that survived the caller's name-collision
	// check into the plan and the receipts that plan will write.
	//
	// The []string is skip reasons, in the same shape Link returns them: a
	// plugin already adopted knows its install path up front and so can plan
	// its fan-out inline, and a name another skill already holds must skip
	// one link rather than fail the whole install. A channel with nothing to
	// skip returns nil.
	Install(req Request, chosen []Candidate) (plan.Plan, []state.Receipt, []string, error)

	// Update decides what each of these receipts should become and returns the
	// mutations. Every receipt given belongs to this channel.
	//
	// It takes the whole set rather than one receipt at a time so that a
	// channel can answer them together: git resolves each repository once
	// however many skills came from it.
	Update(ctx context.Context, rs []*state.Receipt, o UpdateOptions) ([]Verdict, plan.Plan, error)

	// Pin freezes a receipt at the revision it is already on, or releases it to
	// track a ref again. A channel with no revision to freeze refuses.
	//
	// It changes no files, so the plan it returns is the record and nothing
	// else. A receipt already in the state asked for comes back unchanged
	// rather than as an error.
	Pin(r state.Receipt, o PinOptions) (plan.Plan, PinResult, error)

	// Settle completes receipt fields that are knowable only after the plan has
	// been applied, and returns only the receipts it changed. A channel that
	// knew everything up front returns nil.
	Settle(ctx context.Context, rs []state.Receipt) ([]state.Receipt, error)

	// Remove turns a receipt, narrowed to the agents named in drop, into the
	// ops that undo it. An empty drop means every agent.
	Remove(r state.Receipt, drop map[string]bool) (plan.Plan, error)

	// Link makes the agents in add hold what this receipt says they should: it
	// adds the links that are missing, re-points the ones whose destination has
	// moved, and takes away the ones whose skill the source no longer ships. An
	// empty plan means they already agreed.
	//
	// It is reconciliation rather than addition because one channel's
	// destination moves under it: claude installs each version of a plugin
	// beside the last. Install, update and link then all reduce to this one
	// question asked of a different set of agents.
	//
	// The reasons come back separately from the error because a name another
	// skill already holds costs one link, not the command.
	//
	// It sits on the interface rather than behind a type assertion so that a
	// channel which cannot serve it has to say so, and a fourth channel is told
	// by the compiler that this is a question it must answer.
	Link(r state.Receipt, add []target.Target) (plan.Plan, []string, error)

	// Agents names the agents a receipt is live in, for list. A channel that
	// symlinks reads the receipt's links; one whose agent installs for itself
	// answers from the config.
	Agents(r state.Receipt) []string
}

// ErrUnsupported reports a channel that skillsctl can parse but cannot yet
// install. Sources are parsed long before their channel is built, so this is
// the difference between "that is not a source" and "not yet".
var ErrUnsupported = errors.New("not supported yet")

// Registry resolves a channel by name.
type Registry struct {
	Git    Channel
	Plugin Channel
	Local  Channel
	OCI    Channel
}

// For returns the channel that handles c.
func (r Registry) For(c source.Channel) (Channel, error) {
	switch c {
	case source.ChannelGit:
		if r.Git != nil {
			return r.Git, nil
		}
	case source.ChannelPlugin:
		if r.Plugin != nil {
			return r.Plugin, nil
		}
	case source.ChannelLocal:
		if r.Local != nil {
			return r.Local, nil
		}
	case source.ChannelOCI:
		if r.OCI != nil {
			return r.OCI, nil
		}
	}
	return nil, fmt.Errorf("the %s channel is %w", c, ErrUnsupported)
}

// ForReceipt returns the channel a receipt was installed through, naming the
// skill in the error: a receipt is on disk, so "which one" is the first thing
// the user will ask.
func (r Registry) ForReceipt(rc *state.Receipt) (Channel, error) {
	ch, err := r.For(source.Channel(rc.Channel))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rc.Name, err)
	}
	return ch, nil
}

// Agents names the agents a receipt is live in. A receipt whose channel is not
// registered still answers, from its own links: what is on disk is a fact, and
// list reports facts rather than refusing to describe them.
func (r Registry) Agents(rc *state.Receipt) []string {
	if ch, err := r.ForReceipt(rc); err == nil {
		return ch.Agents(*rc)
	}
	names := make([]string, 0, len(rc.Links))
	for _, l := range rc.Links {
		names = append(names, l.Target)
	}
	return names
}
