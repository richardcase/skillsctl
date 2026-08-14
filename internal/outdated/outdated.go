// Package outdated answers one question about installed skills: has the ref
// each one tracks moved since it was installed? It reads remotes with
// ls-remote only, so nothing is fetched, mirrored or extracted.
package outdated

import (
	"context"

	"github.com/richardcase/skillsctl/internal/gitx"
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

// Check resolves each receipt's tracked ref against its remote.
func Check(ctx context.Context, g gitx.Git, receipts []*state.Receipt) []Entry {
	seen := map[string]resolution{}
	entries := make([]Entry, 0, len(receipts))
	for _, r := range receipts {
		// An empty ref means the repository's default branch. Install records
		// no ref for a pinned skill, so this is also what makes a pin visible
		// rather than silently current.
		ref := r.Ref
		if ref == "" {
			ref = "HEAD"
		}

		e := Entry{
			Name:    r.Name,
			Channel: r.Channel,
			Source:  r.Source,
			Ref:     ref,
			Current: r.Resolved,
			Pinned:  r.Pinned,
		}

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
