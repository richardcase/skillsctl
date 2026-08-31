package target

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ValidateSkillName rejects a name that is not a single path element. A skill's
// name can come from a repository's SKILL.md, which is third-party data, and it
// is joined onto an agent's skills directory to build a symlink path — so a name
// containing a separator or a dot segment would let a published repository decide
// where skillsctl creates directories and symlinks.
func ValidateSkillName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("skill name is empty")
	case name == "." || name == "..":
		return fmt.Errorf("skill name %q is a directory reference, not a name", name)
	case strings.ContainsAny(name, `/\`):
		return fmt.Errorf("skill name %q contains a path separator", name)
	case strings.ContainsRune(name, 0):
		return fmt.Errorf("skill name %q contains a NUL byte", name)
	}
	return nil
}

// Occupied reports whether dir/name already has something at it — a
// receipt's own symlink, a foreign symlink, or anything else. It is what
// Link would refuse to overwrite, checked before offering the name as a
// choice rather than after. name can originate in a repository's SKILL.md,
// so an invalid name is reported occupied rather than joined into a path:
// callers already validate before reaching here, but the check belongs next
// to the path it guards rather than solely in a caller that could forget it.
func Occupied(dir, name string) bool {
	if ValidateSkillName(name) != nil {
		return true
	}
	_, err := os.Lstat(filepath.Join(dir, name))
	return err == nil
}

// Link points linkPath at revPath, creating parent directories as needed.
// It reports whether it created the symlink: an existing symlink that already
// points at revPath is a no-op success and reports created == false, so callers
// can tell "I made this" from "this was already here".
// It refuses to replace anything that is not such a symlink.
func Link(linkPath, revPath string) (created bool, err error) {
	if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
		return false, fmt.Errorf("create skills directory: %w", err)
	}

	fi, err := os.Lstat(linkPath)
	switch {
	case err == nil && fi.Mode()&os.ModeSymlink != 0:
		existing, rerr := os.Readlink(linkPath)
		if rerr == nil && existing == revPath {
			return false, nil
		}
		// A symlink somebody made by hand is exactly what adopt takes over, so
		// name it rather than only the blunt remedy. A real directory is not:
		// there would be no link to record, so the message below stays as it is.
		return false, fmt.Errorf("%s is already a symlink to %s: run `skillsctl adopt` to take it over, or remove it first", linkPath, existing)
	case err == nil:
		return false, fmt.Errorf("%s already exists and is not a skillsctl symlink: remove it first", linkPath)
	case !os.IsNotExist(err):
		return false, fmt.Errorf("inspect %s: %w", linkPath, err)
	}

	if err := os.Symlink(revPath, linkPath); err != nil {
		return false, fmt.Errorf("link %s: %w", linkPath, err)
	}
	return true, nil
}

// Relink points an existing symlink at revPath, returning what it pointed at
// before so the caller can put it back. A missing linkPath is created, which
// repairs a receipt whose link was deleted by hand; anything that is not a
// symlink is refused, never replaced.
//
// The new link is written into a sibling temporary directory and renamed over
// the old one, so an agent reading the skills directory during an update sees
// either the old skill or the new one, never a gap.
func Relink(linkPath, revPath string) (previous string, err error) {
	dir := filepath.Dir(linkPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create skills directory: %w", err)
	}

	fi, err := os.Lstat(linkPath)
	switch {
	case os.IsNotExist(err):
		if err := os.Symlink(revPath, linkPath); err != nil {
			return "", fmt.Errorf("link %s: %w", linkPath, err)
		}
		return "", nil
	case err != nil:
		return "", fmt.Errorf("inspect %s: %w", linkPath, err)
	case fi.Mode()&os.ModeSymlink == 0:
		return "", fmt.Errorf("refusing to re-point %s: it is not a skillsctl symlink", linkPath)
	}

	previous, err = os.Readlink(linkPath)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", linkPath, err)
	}
	if previous == revPath {
		return previous, nil
	}

	tmp, err := os.MkdirTemp(dir, ".tmp-link-")
	if err != nil {
		return "", fmt.Errorf("create temp link directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	staged := filepath.Join(tmp, filepath.Base(linkPath))
	if err := os.Symlink(revPath, staged); err != nil {
		return "", fmt.Errorf("link %s: %w", linkPath, err)
	}
	if err := os.Rename(staged, linkPath); err != nil {
		return "", fmt.Errorf("re-point %s: %w", linkPath, err)
	}
	return previous, nil
}

// Unlink removes linkPath when it is a symlink. A missing path succeeds; a
// real file or directory is an error, never a deletion.
func Unlink(linkPath string) error {
	fi, err := os.Lstat(linkPath)
	switch {
	case os.IsNotExist(err):
		return nil
	case err != nil:
		return fmt.Errorf("inspect %s: %w", linkPath, err)
	case fi.Mode()&os.ModeSymlink == 0:
		return fmt.Errorf("refusing to remove %s: it is not a symlink", linkPath)
	}

	if err := os.Remove(linkPath); err != nil {
		return fmt.Errorf("remove %s: %w", linkPath, err)
	}
	return nil
}
