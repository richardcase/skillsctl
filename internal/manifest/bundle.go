package manifest

import (
	"fmt"
	"sort"

	"github.com/richardcase/skillsctl/internal/channel"
	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/target"
)

// FromReceipts projects receipts into a manifest, returning the local skills it
// left out, each as "name (source)".
//
// A local skill's source is an absolute path on this machine and means nothing
// on another, so it is left out rather than written down knowing it is wrong for
// the file's only purpose. The caller names them: a silent drop is how somebody
// finds out on the new machine that something they rely on was never in the
// file.
//
// present is the agent set an install with no -a would have chosen, which is
// exactly what an omitted agents field means.
func FromReceipts(rs []*state.Receipt, reg channel.Registry, present []target.Target) (File, []string) {
	f := File{Version: SchemaVersion}
	var excluded []string

	for _, r := range rs {
		if r.Channel == string(source.ChannelLocal) {
			excluded = append(excluded, fmt.Sprintf("%s (%s)", r.Name, r.Source))
			continue
		}
		f.Skills = append(f.Skills, entryFor(r, reg, present))
	}

	sort.Slice(f.Skills, func(i, j int) bool { return f.Skills[i].Name < f.Skills[j].Name })
	return f, excluded
}

// entryFor projects one receipt.
func entryFor(r *state.Receipt, reg channel.Registry, present []target.Target) Entry {
	e := Entry{
		Name:    r.Name,
		Source:  r.Source,
		Subpath: r.Subpath,
		Ref:     r.Ref,
		Pinned:  r.Pinned,
	}
	// A pinned receipt records no ref, so its revision lives only in Resolved.
	// Dropping it would carry the pin across as a freeze at whatever HEAD the
	// other machine happens to see, which is not the same install.
	if r.Pinned {
		e.Ref = r.Resolved
	}
	// A receipt with no links is one whose agent installed the files itself, so
	// which agents have it was never a choice to preserve.
	if len(r.Links) > 0 {
		e.Agents = narrowerThan(reg.Agents(r), present)
	}
	return e
}

// narrowerThan returns agents when it is a deliberate subset of the present
// set, and nil when it covers all of it. An omitted field means install's own
// default, so emitting one that merely repeats the default would tie a manifest
// to the machine that produced it.
func narrowerThan(agents []string, present []target.Target) []string {
	have := make(map[string]bool, len(agents))
	for _, a := range agents {
		have[a] = true
	}
	for _, t := range present {
		if !have[t.Name] {
			return agents
		}
	}
	return nil
}
