package target

import (
	"fmt"
	"os"
	"path/filepath"
)

// Link points linkPath at revPath, creating parent directories as needed.
// It is a no-op when the link already points where it should, and it refuses
// to replace anything that is not such a symlink.
func Link(linkPath, revPath string) error {
	if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
		return fmt.Errorf("create skills directory: %w", err)
	}

	fi, err := os.Lstat(linkPath)
	switch {
	case err == nil && fi.Mode()&os.ModeSymlink != 0:
		existing, rerr := os.Readlink(linkPath)
		if rerr == nil && existing == revPath {
			return nil
		}
		return fmt.Errorf("%s is already a symlink to %s: remove it first", linkPath, existing)
	case err == nil:
		return fmt.Errorf("%s already exists and is not a skillsctl symlink: remove it first", linkPath)
	case !os.IsNotExist(err):
		return fmt.Errorf("inspect %s: %w", linkPath, err)
	}

	if err := os.Symlink(revPath, linkPath); err != nil {
		return fmt.Errorf("link %s: %w", linkPath, err)
	}
	return nil
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
