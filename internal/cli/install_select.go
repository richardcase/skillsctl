package cli

import (
	"strings"

	"github.com/richardcase/skillsctl/internal/channel"
	"github.com/richardcase/skillsctl/internal/discover"
	"github.com/richardcase/skillsctl/internal/prompt"
)

// maxDescription is how much of a skill's description the listing shows.
const maxDescription = 72

// listing renders the available skills, one per line, for the messages that
// have to tell the user what they could have asked for.
func listing(meta discover.Metadata, header string, cands []channel.Candidate) []string {
	lines := headerLines(meta, header)
	for _, label := range rowLabels(cands) {
		lines = append(lines, "  "+label)
	}
	return lines
}

// headerLines is what sits above the list. Any .claude-plugin metadata goes
// first, describing the repository the skills are in.
func headerLines(meta discover.Metadata, header string) []string {
	var lines []string
	if meta.Name != "" {
		head := meta.Name
		if meta.Description != "" {
			head += " - " + firstLine(meta.Description)
		}
		lines = append(lines, head)
	}
	if header != "" {
		lines = append(lines, header)
	}
	return lines
}

// rowLabels renders one line per candidate, names padded so the descriptions
// line up. The picker and the plain listing both build their rows from this,
// so what a skill looks like cannot drift between being offered and being
// reported.
func rowLabels(cands []channel.Candidate) []string {
	width := 0
	for _, c := range cands {
		width = max(width, len(c.Name))
	}

	labels := make([]string, 0, len(cands))
	for _, c := range cands {
		label := c.Name
		if d := firstLine(c.Desc); d != "" {
			label += strings.Repeat(" ", width-len(c.Name)) + "  " + d
		}
		labels = append(labels, label)
	}
	return labels
}

// firstLine reduces a description to a single line short enough to sit in a
// column beside a skill name.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if len(s) > maxDescription {
		s = strings.TrimSpace(s[:maxDescription]) + "..."
	}
	return s
}

// selectSkills asks which of an ambiguous repository's skills to install, and
// answers in the form --skill would have taken. single is for --as, which
// renames one skill and so cannot be handed several.
func selectSkills(p picker, amb *channel.Ambiguous, single bool) ([]string, error) {
	labels := rowLabels(amb.Available)
	items := make([]prompt.Item, len(labels))
	for i, l := range labels {
		items[i] = prompt.Item{Label: l}
	}

	help := "↑/↓ move · space toggle · a all · enter install · q cancel"
	if single {
		help = "↑/↓ move · enter install · q cancel"
	}

	chosen, err := p.Select(prompt.Options{
		Header: headerLines(amb.Meta, amb.Header),
		Items:  items,
		Single: single,
		Help:   help,
	})
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(chosen))
	for _, i := range chosen {
		names = append(names, amb.Available[i].Name)
	}
	return names, nil
}

// pickedListing is what replaces the picker once it has been erased, so the
// scrollback reads the same as the non-interactive form: the same header, and
// the rows that were actually chosen.
func pickedListing(amb *channel.Ambiguous, names []string) []string {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}

	var chosen []channel.Candidate
	for _, c := range amb.Available {
		if want[c.Name] {
			chosen = append(chosen, c)
		}
	}
	return listing(amb.Meta, amb.Header, chosen)
}
