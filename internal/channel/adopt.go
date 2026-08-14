package channel

import (
	"time"

	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/state"
)

// Adopting a skill that is already in an agent's skills directory writes a
// receipt for something skillsctl did not put there. The receipt shapes live
// here, beside the install receipts they have to match, because a receipt that
// records provenance differently depending on how it was created would make
// every command downstream of it ask which kind it was holding.
//
// Neither constructor emits a plan. The symlink is already on disk and already
// points where the receipt says, so the whole plan is the record — which is
// what makes adopt incapable of a destructive op rather than merely careful to
// avoid one.

// AdoptReceipt records a skill that is linked into an agent's skills directory
// from somewhere skillsctl knows nothing else about.
//
// dest is both the source and the revision path: the symlink's target is the
// skill, so there is no root to be relative to and no subpath. That is exactly
// the receipt `skillsctl link <dest>` would have written, which is what makes
// removal identical too — the links go, the directory stays.
func (c *Local) AdoptReceipt(name, dest string, links []state.Link, now time.Time) state.Receipt {
	r := localReceipt(name, dest, "", dest, now)
	r.Links = links
	return r
}

// AdoptReceipt records a skill whose provenance was recoverable from the git
// working copy it is linked into.
//
// It is pinned to the sha the checkout is already at, and deliberately so. The
// directory on the other end of the symlink may be one the user develops in,
// and an unpinned receipt would let the next plain `update` re-point their
// symlink into the store. Pinned, `outdated` still reports the ref moving —
// a pin never hides that — while `update` acts only when the skill is named,
// which is the moment the user asks for the takeover to complete.
//
// RevPath is the working copy rather than a store path because RevPath records
// where the linked files actually are, and that is where they are until that
// first named update. ContentHash is left empty for the same reason: it exists
// to notice edits to an immutable extraction, and a working copy is neither
// immutable nor ours. `inspect` already reads an empty hash as the by-hand case
// and stands its dirty check down.
func (c *Git) AdoptReceipt(name string, repo source.Source, sha, subpath, dest string, links []state.Link, now time.Time) state.Receipt {
	return state.Receipt{
		Name:        name,
		Channel:     string(source.ChannelGit),
		Source:      repo.RepoURL,
		Slug:        repo.Slug(),
		Subpath:     subpath,
		Resolved:    sha,
		Pinned:      true,
		RevPath:     dest,
		Links:       links,
		InstalledAt: now,
		UpdatedAt:   now,
	}
}
