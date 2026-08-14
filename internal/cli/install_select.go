package cli

import (
	"fmt"
	"path"
	"strings"

	"github.com/richardcase/skillsctl/internal/discover"
	"github.com/richardcase/skillsctl/internal/target"
)

// maxDescription is how much of a skill's description the listing shows.
const maxDescription = 72

// selection pairs a discovered skill with the name it will be linked under.
type selection struct {
	skill discover.Skill
	name  string
}

// resolveNames gives every discovered skill its link name: the frontmatter
// name, else fallback for a skill that is the walk root, else the skill's own
// directory name. fallback is the name derived from the source, which only
// makes sense for a skill with no directory of its own to be named after.
func resolveNames(found []discover.Skill, fallback string) ([]selection, error) {
	sels := make([]selection, 0, len(found))
	for _, s := range found {
		name, origin := s.Name, "the SKILL.md in "+s.Rel
		if name == "" {
			if s.Rel == "." {
				name, origin = fallback, "the source"
			} else {
				name, origin = path.Base(s.Rel), "the directory "+s.Rel
			}
		}
		if err := target.ValidateSkillName(name); err != nil {
			return nil, fmt.Errorf("refusing to install: %w (from %s); pass --as <name> to choose one", err, origin)
		}
		sels = append(sels, selection{skill: s, name: name})
	}

	seen := make(map[string]string, len(sels))
	for _, s := range sels {
		if prev, ok := seen[s.name]; ok {
			return nil, fmt.Errorf("%s and %s both resolve to the name %q: install them one at a time with --skill and --as",
				prev, s.skill.Rel, s.name)
		}
		seen[s.name] = s.skill.Rel
	}
	return sels, nil
}

// pickSkills selects the skills the user named, in the order they named them.
// A name matches a skill's resolved name first, then its path within the
// repository, so a skill whose frontmatter is missing or ambiguous can still be
// asked for. Asking for the same skill twice installs it once.
func pickSkills(all []selection, wanted []string) ([]selection, error) {
	out := make([]selection, 0, len(wanted))
	chosen := make(map[string]bool, len(wanted))

	for _, want := range wanted {
		s, ok := matchSkill(all, want)
		if !ok {
			return nil, fmt.Errorf("no skill named %q in this repository", want)
		}
		if chosen[s.skill.Rel] {
			continue
		}
		chosen[s.skill.Rel] = true
		out = append(out, s)
	}
	return out, nil
}

// matchSkill resolves one --skill value: by name, then by path.
func matchSkill(all []selection, want string) (selection, bool) {
	for _, s := range all {
		if s.name == want {
			return s, true
		}
	}
	for _, s := range all {
		if s.skill.Rel == want {
			return s, true
		}
	}
	return selection{}, false
}

// listing renders the available skills, one per line, for the messages that
// have to tell the user what they could have asked for. Any .claude-plugin
// metadata goes above the header, describing the repository the skills are in.
func listing(meta discover.Metadata, header string, sels []selection) []string {
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
	for _, s := range sels {
		if len(s.name) > width {
			width = len(s.name)
		}
	}
	for _, s := range sels {
		line := "  " + s.name
		if d := firstLine(s.skill.Description); d != "" {
			line += strings.Repeat(" ", width-len(s.name)) + "  " + d
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
