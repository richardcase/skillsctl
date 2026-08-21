// Package lint validates a skill's SKILL.md before it is published, catching
// what discover's installer-facing parsing tolerates: a missing frontmatter
// block, an empty name or description, and a name discover would accept but
// target would refuse to link.
package lint

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/richardcase/skillsctl/internal/discover"
	"github.com/richardcase/skillsctl/internal/target"
)

// Severity classifies a Finding. Only Error affects a caller's exit code;
// Warning is printed but does not fail the lint.
type Severity int

const (
	// Error fails the caller's exit code.
	Error Severity = iota
	// Warning is printed but does not fail the lint.
	Warning
)

// String renders the severity the way a report names it.
func (s Severity) String() string {
	if s == Warning {
		return "warning"
	}
	return "error"
}

// MarshalJSON renders the severity as its name rather than its underlying
// int, so --json output reads the same way the text report does.
func (s Severity) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

// UnmarshalJSON is MarshalJSON's inverse, so a caller that stores or
// re-reads a --json report gets the same severity back.
func (s *Severity) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err != nil {
		return err
	}
	if str == "warning" {
		*s = Warning
	} else {
		*s = Error
	}
	return nil
}

// Finding is one issue found in a skill.
type Finding struct {
	Skill    string   `json:"skill"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
}

// Path lints the skill at path, or every skill found under path when path
// itself holds no SKILL.md — the same single-skill-vs-repository distinction
// discover.Walk already makes for install's listing.
func Path(path string) ([]Finding, error) {
	if s, err := discover.Root(path); err == nil {
		return check(s), nil
	} else if !errors.Is(err, discover.ErrNoSkill) {
		return []Finding{{Skill: path, Severity: Error, Message: err.Error()}}, nil
	}

	skills, err := discover.Walk(path)
	if err != nil {
		return nil, err
	}
	if len(skills) == 0 {
		return nil, fmt.Errorf("no SKILL.md found under %s", path)
	}

	var findings []Finding
	for _, s := range skills {
		findings = append(findings, check(s)...)
	}
	return findings, nil
}

// check runs every rule against one already-parsed skill.
func check(s discover.Skill) []Finding {
	var findings []Finding
	add := func(sev Severity, format string, args ...any) {
		findings = append(findings, Finding{Skill: s.Dir, Severity: sev, Message: fmt.Sprintf(format, args...)})
	}

	switch err := target.ValidateSkillName(s.Name); {
	case s.Name == "":
		add(Error, "name is empty")
	case err != nil:
		add(Error, "invalid name: %s", err)
	case filepath.Base(s.Dir) != s.Name:
		add(Warning, "name %q does not match directory %q", s.Name, filepath.Base(s.Dir))
	}

	if s.Description == "" {
		add(Error, "description is empty")
	}
	return findings
}
