package cli

import (
	"strings"

	"github.com/richardcase/skillsctl/internal/channel"
	"github.com/richardcase/skillsctl/internal/discover"
)

// maxDescription is how much of a skill's description the listing shows.
const maxDescription = 72

// listing renders the available skills, one per line, for the messages that
// have to tell the user what they could have asked for. Any .claude-plugin
// metadata goes above the header, describing the repository the skills are in.
func listing(meta discover.Metadata, header string, cands []channel.Candidate) []string {
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

	width := 0
	for _, c := range cands {
		if len(c.Name) > width {
			width = len(c.Name)
		}
	}
	for _, c := range cands {
		line := "  " + c.Name
		if d := firstLine(c.Desc); d != "" {
			line += strings.Repeat(" ", width-len(c.Name)) + "  " + d
		}
		lines = append(lines, line)
	}
	return lines
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
