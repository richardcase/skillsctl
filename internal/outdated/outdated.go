// Package outdated answers one question about installed skills: has the ref
// each one tracks moved since it was installed — or, for a plugin, has the
// agent that owns it moved its install out from under the receipt? It
// performs no network fetch: a git receipt is checked with ls-remote alone,
// an OCI receipt's manifest is read without pulling any layer, nothing is
// mirrored or extracted, and claude is asked only when a plugin receipt is
// present.
package outdated

import (
	"context"
	"fmt"

	"github.com/richardcase/skillsctl/internal/claudex"
	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/richardcase/skillsctl/internal/ocix"
	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/state"
)

// Status is the verdict for one receipt.
type Status string

const (
	// StatusCurrent means the tracked ref still points at the installed sha.
	StatusCurrent Status = "current"
	// StatusOutdated means the tracked ref has moved.
	StatusOutdated Status = "outdated"
	// StatusStale means the agent that owns the files has moved on: the version
	// or the install path it reports is not the one the receipt records, so the
	// links skillsctl made point into a directory it has replaced.
	StatusStale Status = "stale"
	// StatusSkipped means the receipt has no upstream to compare against.
	StatusSkipped Status = "n/a"
	// StatusError means the remote could not be read.
	StatusError Status = "error"
)

// Entry is one receipt checked against its remote.
type Entry struct {
	Name    string `json:"name"`
	Channel string `json:"channel"`
	Source  string `json:"source"`
	Ref     string `json:"ref"`
	Current string `json:"current"`
	Latest  string `json:"latest,omitempty"`
	Pinned  bool   `json:"pinned,omitempty"`
	Status  Status `json:"status"`
	Error   string `json:"error,omitempty"`
}

// resolution is one ls-remote answer, cached so that N skills installed from
// one repository cost one round trip rather than N.
type resolution struct {
	sha string
	err error
}

// Check resolves each receipt's tracked ref against its remote, and each
// plugin's recorded install against what its agent has now.
//
// p is consulted lazily, so a store holding no plugins never shells out to
// claude and the package keeps its promise that it fetches nothing.
func Check(ctx context.Context, g gitx.Git, p claudex.Plugins, o ocix.OCI, receipts []*state.Receipt) []Entry {
	seen := map[string]resolution{}
	entries := make([]Entry, 0, len(receipts))

	var installed []claudex.Installed
	var listErr error
	var listed bool
	plugins := func() ([]claudex.Installed, error) {
		if !listed {
			listed = true
			installed, listErr = p.List(ctx)
		}
		return installed, listErr
	}

	for _, r := range receipts {
		e := Entry{
			Name:    r.Name,
			Channel: r.Channel,
			Source:  r.Source,
			Current: r.Resolved,
			Pinned:  r.Pinned,
		}

		// A plugin tracks no ref, so there is no Ref to report; what it has
		// instead is an install its agent is free to move underneath it.
		if r.Channel == string(source.ChannelPlugin) {
			entries = append(entries, checkPlugin(e, r, plugins))
			continue
		}

		// An OCI receipt's Source already names the tag, so unlike a git remote
		// it needs no separate ref suffix in the seen key: two receipts sharing
		// a tag share a key outright. The scheme comes off first — a registry
		// has never heard of oci://.
		if r.Channel == string(source.ChannelOCI) {
			e.Ref = r.Ref
			// The tag checked is the one the receipt tracks, which unpin --ref
			// can have moved; the source's own tag stands in only when the
			// source will not parse, so a stale receipt still gets an answer.
			ref := source.BareRef(r.Source)
			if src, perr := source.Parse(r.Source); perr == nil {
				ref = src.OCIRef(r.Ref)
			}
			got, ok := seen[ref]
			if !ok {
				got.sha, got.err = o.Resolve(ctx, ref)
				seen[ref] = got
			}
			if got.err != nil {
				e.Status = StatusError
				e.Error = got.err.Error()
				entries = append(entries, e)
				continue
			}
			e.Latest = got.sha
			e.Status = StatusCurrent
			if got.sha != r.Resolved {
				e.Status = StatusOutdated
			}
			entries = append(entries, e)
			continue
		}

		// An empty ref means the repository's default branch. Install records
		// no ref for a pinned skill, so this is also what makes a pin visible
		// rather than silently current.
		ref := r.Ref
		if ref == "" {
			ref = "HEAD"
		}
		e.Ref = ref

		// Only the git channel has a ref that can move.
		if r.Channel != string(source.ChannelGit) {
			e.Status = StatusSkipped
			entries = append(entries, e)
			continue
		}

		key := r.Source + "\x00" + ref
		got, ok := seen[key]
		if !ok {
			got.sha, got.err = g.Resolve(ctx, r.Source, ref)
			seen[key] = got
		}

		latest, err := got.sha, got.err
		if err != nil {
			// One unreachable remote must not hide the rest of the report.
			e.Status = StatusError
			e.Error = err.Error()
			entries = append(entries, e)
			continue
		}

		e.Latest = latest
		e.Status = StatusCurrent
		if latest != r.Resolved {
			e.Status = StatusOutdated
		}
		entries = append(entries, e)
	}
	return entries
}

// checkPlugin compares what the receipt records against what the agent has now.
//
// This is not the marketplace comparison a plugin's users will eventually want —
// nothing here asks whether a newer version has been published. It answers the
// question the receipt's links raise: is the directory they point into still the
// one claude calls this plugin's install path?
func checkPlugin(e Entry, r *state.Receipt, list func() ([]claudex.Installed, error)) Entry {
	got, err := list()
	if err != nil {
		e.Status = StatusError
		e.Error = err.Error()
		return e
	}

	for _, p := range got {
		if p.ID != r.Source {
			continue
		}
		e.Latest = p.Version
		e.Status = StatusCurrent
		if p.Version != r.Resolved || p.InstallPath != r.RevPath {
			e.Status = StatusStale
		}
		return e
	}

	e.Status = StatusError
	e.Error = fmt.Sprintf("claude no longer has %s installed", r.Source)
	return e
}
