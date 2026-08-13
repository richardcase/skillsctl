// Package discover reads SKILL.md files and their YAML frontmatter.
package discover

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ErrNoSkill reports a directory with no SKILL.md.
var ErrNoSkill = errors.New("no SKILL.md")

// FileName is the file that marks a directory as a skill.
const FileName = "SKILL.md"

// Meta is the frontmatter skillsctl cares about.
type Meta struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// Skill is a discovered skill directory.
type Skill struct {
	Meta
	Dir string
}

// Frontmatter parses a leading `---` YAML block. A file with no block is not
// an error: the caller falls back to a name derived from the source.
func Frontmatter(data []byte) (Meta, error) {
	body := bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))

	const fence = "---\n"
	if !bytes.HasPrefix(body, []byte(fence)) {
		return Meta{}, nil
	}
	rest := body[len(fence):]

	end := bytes.Index(rest, []byte("\n---"))
	if end < 0 {
		return Meta{}, fmt.Errorf("frontmatter block is not terminated by ---")
	}

	var m Meta
	if err := yaml.Unmarshal(rest[:end+1], &m); err != nil {
		return Meta{}, fmt.Errorf("parse frontmatter: %w", err)
	}
	return m, nil
}

// Root reads the SKILL.md directly inside dir.
func Root(dir string) (Skill, error) {
	p := filepath.Join(dir, FileName)

	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return Skill{}, fmt.Errorf("%s: %w", dir, ErrNoSkill)
	}
	if err != nil {
		return Skill{}, fmt.Errorf("read %s: %w", p, err)
	}

	m, err := Frontmatter(data)
	if err != nil {
		return Skill{}, fmt.Errorf("%s: %w", p, err)
	}
	return Skill{Meta: m, Dir: dir}, nil
}
