package cli

import (
	"path/filepath"
	"strings"

	"github.com/richardcase/skillsctl/internal/channel"
	"github.com/richardcase/skillsctl/internal/discover"
	"github.com/richardcase/skillsctl/internal/prompt"
)

// maxDescription is how much of a skill's description the listing shows.
const maxDescription = 72

// listing renders the available skills, one per line, for the messages that
// have to tell the user what they could have asked for. Grouped the same way
// the interactive picker groups its rows (see bucketByCategory), so a
// multi-plugin or multi-category repository reads the same whether or not a
// terminal is attached.
func listing(meta discover.Metadata, header string, cands []channel.Candidate) []string {
	lines := headerLines(meta, header)
	labels := rowLabels(cands)

	root, groups := bucketByCategory(cands)
	if groups == nil {
		for _, l := range labels {
			lines = append(lines, "  "+l)
		}
		return lines
	}

	for _, i := range root {
		lines = append(lines, "  "+labels[i])
	}
	for _, g := range groups {
		lines = append(lines, "  "+g.name+":")
		for _, i := range g.idx {
			lines = append(lines, "    "+labels[i])
		}
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
	items, member := pickerItems(amb.Available)

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
		names = append(names, amb.Available[member[i]].Name)
	}
	return names, nil
}

// categoryPrefixLen reports how many leading Subpath segments are shared by
// every candidate's parent directory - a wrapper folder (a plugin's "skills/"
// convention, for example) that every skill sits under and that therefore
// carries no distinguishing signal. Such a prefix is stripped before the
// first remaining segment is taken as the category, so a repo laid out as
// skills/<category>/<name> groups by <category> rather than by "skills".
//
// A candidate at the source's root has no parent segments at all, which
// forces the intersection to zero: a repo mixing root-level and nested
// skills is left unstripped, same as before this existed.
func categoryPrefixLen(cands []channel.Candidate) int {
	var prefix []string
	set := false
	for _, c := range cands {
		dir := filepath.ToSlash(c.Subpath)
		var parts []string
		if dir != "" {
			parts = strings.Split(dir, "/")
			parts = parts[:len(parts)-1] // drop the skill's own directory
		}
		if !set {
			prefix = parts
			set = true
			continue
		}
		prefix = commonPrefix(prefix, parts)
	}
	return len(prefix)
}

// commonPrefix returns the longest leading run of elements shared by a and b.
func commonPrefix(a, b []string) []string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return a[:i]
		}
	}
	return a[:n]
}

// category is the group a candidate's skill is shown under: its marketplace
// Plugin when one was resolved, otherwise the folder a candidate's skill
// lives under within its source, once the prefixLen leading segments common
// to every candidate (see categoryPrefixLen) are stripped, or "" for one that
// sits at the source's root or directly under such a shared wrapper. The
// folder form is derived from Subpath rather than stored anywhere.
func category(c channel.Candidate, prefixLen int) string {
	if c.Plugin != "" {
		return c.Plugin
	}
	dir := filepath.ToSlash(c.Subpath)
	if dir == "" {
		return ""
	}
	parts := strings.Split(dir, "/")
	if len(parts) <= prefixLen+1 {
		return ""
	}
	return parts[prefixLen]
}

// categoryGroup is every candidate index sharing one category, in the order
// bucketByCategory found them.
type categoryGroup struct {
	name string
	idx  []int
}

// bucketByCategory groups cands' indices by category(), in first-seen order.
// root holds the indices with no category (sit at the source's root or
// directly under a shared wrapper); groups is nil once fewer than 2
// categories are present, telling the caller to fall back to a flat list -
// a repository with all its skills at the root, the common case, is
// unaffected by grouping.
//
// cands is sorted by name, not by where a skill sits in the repository, so
// this buckets by category itself rather than assuming same-category
// candidates are adjacent.
func bucketByCategory(cands []channel.Candidate) (root []int, groups []categoryGroup) {
	prefixLen := categoryPrefixLen(cands)
	cats := make([]string, len(cands))
	var order []string
	seen := map[string]bool{}
	for i, c := range cands {
		cats[i] = category(c, prefixLen)
		if cats[i] != "" && !seen[cats[i]] {
			seen[cats[i]] = true
			order = append(order, cats[i])
		}
	}
	if len(order) < 2 {
		return nil, nil
	}

	for i := range cands {
		if cats[i] == "" {
			root = append(root, i)
		}
	}
	for _, cat := range order {
		var idx []int
		for i := range cands {
			if cats[i] == cat {
				idx = append(idx, i)
			}
		}
		groups = append(groups, categoryGroup{name: cat, idx: idx})
	}
	return root, groups
}

// pickerItems builds the rows selectSkills shows, and member, the parallel
// slice that maps each row back to its index in cands: -1 for a header row,
// the candidate's index otherwise.
func pickerItems(cands []channel.Candidate) ([]prompt.Item, []int) {
	labels := rowLabels(cands)

	root, groups := bucketByCategory(cands)
	if groups == nil {
		items := make([]prompt.Item, len(labels))
		member := make([]int, len(labels))
		for i, l := range labels {
			items[i] = prompt.Item{Label: l}
			member[i] = i
		}
		return items, member
	}

	var items []prompt.Item
	var member []int
	// Root-level candidates, if any, are listed first and stay unheaded.
	for _, i := range root {
		items = append(items, prompt.Item{Label: labels[i]})
		member = append(member, i)
	}
	for _, g := range groups {
		items = append(items, prompt.Item{Label: g.name, Header: true})
		member = append(member, -1)
		for _, i := range g.idx {
			items = append(items, prompt.Item{Label: labels[i]})
			member = append(member, i)
		}
	}
	return items, member
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
