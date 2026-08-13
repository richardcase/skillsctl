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

	head, rest, found := bytes.Cut(body, []byte("\n"))
	if !found || !isFence(head) {
		return Meta{}, nil
	}

	end := -1
	for offset := 0; offset <= len(rest); {
		line := rest[offset:]
		nl := bytes.IndexByte(line, '\n')
		if nl >= 0 {
			line = line[:nl]
		}
		if isFence(line) {
			end = offset
			break
		}
		if nl < 0 {
			break
		}
		offset += nl + 1
	}
	if end < 0 {
		return Meta{}, fmt.Errorf("frontmatter block is not terminated by ---")
	}

	var m Meta
	if err := yaml.Unmarshal(rest[:end], &m); err != nil {
		return Meta{}, fmt.Errorf("parse frontmatter: %w", err)
	}
	return m, nil
}

// isFence reports whether line is a frontmatter delimiter: exactly ---, allowing
// trailing spaces or tabs. Opening and closing fences use the same rule, so a
// fence is never recognised at one end and missed at the other.
func isFence(line []byte) bool {
	return bytes.Equal(bytes.TrimRight(line, " \t"), []byte("---"))
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
