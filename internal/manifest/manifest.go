// Package manifest is the skills.toml format: the portable projection of a
// receipt set that bundle writes and sync reads.
//
// A receipt is the private record of an install, full of absolute paths and
// content hashes. A manifest is what survives being carried to another machine:
// which skill, from where, at which revision, for which agents. Everything else
// a receipt holds is either machine-local or derivable from those.
package manifest

import (
	"fmt"
	"io"

	"github.com/pelletier/go-toml/v2"
	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/target"
)

// SchemaVersion is the manifest format version. Bump it only for a breaking
// change, and add a migration when you do.
const SchemaVersion = 1

// Entry is one skill a manifest names.
//
// Ref carries a branch or tag for a skill that tracks one, and the frozen sha
// for a pinned skill: install --pin records no ref, so the sha is the only
// place a pin's revision lives, and one field answering "which revision" is
// what makes syncing a pinned entry exactly `install --ref <sha> --pin`.
//
// Agents is empty when the skill is in every agent present on the machine,
// which is what an omitted -a means to install. Naming them is for a choice
// that was narrower than the default.
type Entry struct {
	Name    string   `toml:"name"`
	Source  string   `toml:"source"`
	Subpath string   `toml:"subpath,omitempty"`
	Ref     string   `toml:"ref,omitempty"`
	Pinned  bool     `toml:"pinned,omitempty"`
	Agents  []string `toml:"agents,omitempty"`
}

// File is a whole skills.toml.
type File struct {
	Version int     `toml:"version"`
	Skills  []Entry `toml:"skill"`
}

// Encode writes f as TOML, supplying the version so that no manifest this
// build produces is missing one.
func Encode(w io.Writer, f File) error {
	if f.Version == 0 {
		f.Version = SchemaVersion
	}
	blob, err := toml.Marshal(f)
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	if _, err := w.Write(blob); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}

// Decode parses a manifest and checks that every entry names one installable
// skill. A manifest is read with nobody standing by to answer a question, so an
// entry that could be read two ways is refused here rather than guessed at
// later.
func Decode(b []byte) (File, error) {
	var f File
	if err := toml.Unmarshal(b, &f); err != nil {
		return File{}, fmt.Errorf("parse manifest: %w", err)
	}

	switch {
	case f.Version == 0:
		// No version field: a hand-written file, or one from a build that
		// predates the field.
		f.Version = SchemaVersion
	case f.Version < 0:
		return File{}, fmt.Errorf("manifest version %d is not a version", f.Version)
	case f.Version > SchemaVersion:
		return File{}, fmt.Errorf("this manifest is version %d and this build understands %d: upgrade skillsctl",
			f.Version, SchemaVersion)
	}

	seen := make(map[string]bool, len(f.Skills))
	for i, e := range f.Skills {
		if err := e.validate(); err != nil {
			return File{}, fmt.Errorf("skill %d: %w", i+1, err)
		}
		if seen[e.Name] {
			return File{}, fmt.Errorf("skill %d: %q is named twice, and one name is one install", i+1, e.Name)
		}
		seen[e.Name] = true
	}
	return f, nil
}

// Parse turns an entry into the source an install would be given.
//
// The subpath has two spellings — its own field, which is what bundle writes,
// and inside the source string, which is what somebody would type at the
// command line. Folding the field in only when the source names none is what
// keeps them from being concatenated into a path that is neither.
func (e Entry) Parse() (source.Source, error) {
	bare, err := source.Parse(e.Source)
	if err != nil {
		return source.Source{}, err
	}
	if e.Subpath == "" || bare.Subpath != "" {
		return bare, nil
	}
	return source.Parse(e.Source + source.SubpathSep + e.Subpath)
}

// validate refuses an entry sync could not act on unambiguously.
func (e Entry) validate() error {
	if e.Name == "" {
		return fmt.Errorf("has no name: an entry names its skill, because that name is what sync compares against what is installed")
	}
	// The name becomes a receipt key and a path segment in an agent's skills
	// directory, and a manifest is third-party data like any other.
	if err := target.ValidateSkillName(e.Name); err != nil {
		return err
	}
	if e.Source == "" {
		return fmt.Errorf("%q has no source", e.Name)
	}

	bare, err := source.Parse(e.Source)
	if err != nil {
		return fmt.Errorf("%q: %w", e.Name, err)
	}
	if e.Subpath != "" && bare.Subpath != "" && bare.Subpath != e.Subpath {
		return fmt.Errorf("%q names subpath %q in its source and %q in its subpath field: they must agree",
			e.Name, bare.Subpath, e.Subpath)
	}
	if _, err := e.Parse(); err != nil {
		return fmt.Errorf("%q: %w", e.Name, err)
	}
	return nil
}
