package target

import (
	"fmt"
	"os"
	"path/filepath"
)

// LinkState is what is at a link path, judged against where a receipt says it
// should point. A receipt records that a symlink was created; only the disk can
// say whether it is still there and still points at the same directory, and
// that is the difference `info` exists to show.
type LinkState int

const (
	// LinkOK is a symlink pointing at the directory the receipt names.
	LinkOK LinkState = iota
	// LinkElsewhere is a symlink pointing at some other directory. Whether
	// that directory exists is not asked: "this is not the link we recorded"
	// is the stronger fact, and the destination is reported alongside it.
	LinkElsewhere
	// LinkDangling is a symlink pointing at the recorded directory, which is
	// no longer there.
	LinkDangling
	// LinkMissing is nothing at all at the link path.
	LinkMissing
	// LinkForeign is something at the link path that is not a symlink, so it
	// is not ours and never was.
	LinkForeign
	// LinkUnreadable is a link path that could not be read.
	LinkUnreadable
)

// String renders a state as the word a report prints.
func (s LinkState) String() string {
	switch s {
	case LinkOK:
		return "ok"
	case LinkElsewhere:
		return "elsewhere"
	case LinkDangling:
		return "dangling"
	case LinkMissing:
		return "missing"
	case LinkForeign:
		return "not a symlink"
	case LinkUnreadable:
		return "unreadable"
	}
	return "unknown"
}

// Inspect reports what is at linkPath and whether it points at want.
//
// It is the read-only counterpart to Link, Relink and Unlink, which own the
// same Lstat/Readlink vocabulary but all exist to change something. Inspect
// changes nothing and never fails the caller: the error it returns explains a
// LinkUnreadable and is descriptive rather than fatal, since a report that
// cannot read one link should still print the rest.
//
// dest is where the symlink points, resolved to an absolute path, and is empty
// when there is no symlink to follow.
func Inspect(linkPath, want string) (state LinkState, dest string, err error) {
	fi, err := os.Lstat(linkPath)
	switch {
	case os.IsNotExist(err):
		return LinkMissing, "", nil
	case err != nil:
		return LinkUnreadable, "", fmt.Errorf("inspect %s: %w", linkPath, err)
	case fi.Mode()&os.ModeSymlink == 0:
		return LinkForeign, "", nil
	}

	dest, err = os.Readlink(linkPath)
	if err != nil {
		return LinkUnreadable, "", fmt.Errorf("read %s: %w", linkPath, err)
	}
	// A relative target is resolved against the directory holding the link,
	// which is how the filesystem reads it.
	if !filepath.IsAbs(dest) {
		dest = filepath.Join(filepath.Dir(linkPath), dest)
	}
	dest = filepath.Clean(dest)

	if dest != filepath.Clean(want) {
		return LinkElsewhere, dest, nil
	}
	if _, err := os.Stat(dest); err != nil {
		if os.IsNotExist(err) {
			return LinkDangling, dest, nil
		}
		return LinkUnreadable, dest, fmt.Errorf("read %s: %w", dest, err)
	}
	return LinkOK, dest, nil
}
