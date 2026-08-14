// Package discover reads SKILL.md files and their YAML frontmatter.
package discover

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrNoSkill reports a directory with no SKILL.md.
var ErrNoSkill = errors.New("no SKILL.md")

// FileName is the file that marks a directory as a skill.
const FileName = "SKILL.md"

// MaxDepth bounds how far below the walk root Walk descends. Skills live near
// the top of a repository; the bound keeps a pathological tree from turning a
// walk into a full-repository scan.
const MaxDepth = 5

// skipDirs are never descended into: neither holds skills, and both can be
// large enough to dominate the walk.
var skipDirs = map[string]bool{".git": true, "node_modules": true}

// Meta is the frontmatter skillsctl cares about.
type Meta struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// Skill is a discovered skill directory.
type Skill struct {
	Meta
	Dir string
	// Rel is Dir as a slash path relative to the root the walk started from,
	// "." when that root is itself the skill. It names the skill's position in
	// the repository, which is what a receipt records and what --skill matches
	// when a name is missing or ambiguous.
	Rel string
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
	return Skill{Meta: m, Dir: dir, Rel: "."}, nil
}

// Walk returns every skill at or under dir, ordered by Rel.
//
// A directory holding a SKILL.md is a skill and is not descended into, so a
// repository whose root is a skill yields exactly one, and example directories
// inside a skill never become skills of their own. A tree with no SKILL.md
// anywhere is not an error: the caller decides whether that is fatal.
func Walk(dir string) ([]Skill, error) {
	if _, err := os.Stat(dir); err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}

	var skills []Skill
	walk := func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		if rel != "." {
			if skipDirs[d.Name()] || strings.Count(rel, "/")+1 > MaxDepth {
				return fs.SkipDir
			}
		}

		s, err := Root(p)
		if errors.Is(err, ErrNoSkill) {
			return nil
		}
		if err != nil {
			return err
		}
		s.Rel = rel
		skills = append(skills, s)
		return fs.SkipDir
	}
	if err := filepath.WalkDir(dir, walk); err != nil {
		return nil, err
	}

	slices.SortFunc(skills, func(a, b Skill) int { return strings.Compare(a.Rel, b.Rel) })
	return skills, nil
}
