package gitx

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Untar writes the tar stream in r into dest, rejecting any entry — a
// symlink target included — that would resolve outside dest. Containment is
// checked by walking each path component against the real filesystem rather
// than syntactically: an earlier entry in the archive may have planted a
// symlink that turns a later entry's syntactically-clean ".." into a real
// escape once it is actually resolved, so every placement and every
// symlink/hardlink target is re-walked with withinDest, which follows
// symlinks extracted by prior entries exactly as the OS would.
func Untar(r io.Reader, dest string) error {
	realDest, err := filepath.EvalSymlinks(dest)
	if err != nil {
		return fmt.Errorf("resolve destination %s: %w", dest, err)
	}

	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}

		target, err := safeJoin(dest, hdr.Name)
		if err != nil {
			return err
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := withinDest(realDest, splitClean(hdr.Name)); err != nil {
				return fmt.Errorf("entry %s escapes the destination: %w", hdr.Name, err)
			}
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("create %s: %w", target, err)
			}
		case tar.TypeReg:
			if err := withinDest(realDest, splitClean(filepath.Dir(hdr.Name))); err != nil {
				return fmt.Errorf("entry %s escapes the destination: %w", hdr.Name, err)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("create %s: %w", filepath.Dir(target), err)
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return fmt.Errorf("create %s: %w", target, err)
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return fmt.Errorf("write %s: %w", target, err)
			}
			if err := f.Close(); err != nil {
				return fmt.Errorf("close %s: %w", target, err)
			}
		case tar.TypeSymlink:
			if filepath.IsAbs(hdr.Linkname) {
				return fmt.Errorf("symlink %s points outside the revision directory", hdr.Name)
			}
			if err := withinDest(realDest, splitClean(filepath.Dir(hdr.Name))); err != nil {
				return fmt.Errorf("symlink %s escapes the revision directory: %w", hdr.Name, err)
			}
			// The link's target is resolved relative to hdr.Name's
			// directory, exactly as the OS resolves a relative symlink, so
			// its components are walked starting where hdr.Name's own
			// directory components left off rather than re-cleaned
			// together with them — collapsing them first would let a
			// prior entry's symlink cancel out a ".." syntactically
			// without the escape it performs on the real filesystem.
			comps := append(splitClean(filepath.Dir(hdr.Name)), splitClean(hdr.Linkname)...)
			if err := withinDest(realDest, comps); err != nil {
				return fmt.Errorf("symlink %s escapes the revision directory: %w", hdr.Name, err)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return fmt.Errorf("symlink %s: %w", target, err)
			}
		case tar.TypeLink:
			// Unlike a symlink target, a hardlink's Linkname is relative to
			// the archive root rather than to hdr.Name's directory.
			linkTarget, err := safeJoin(dest, hdr.Linkname)
			if err != nil {
				return fmt.Errorf("hardlink %s escapes the revision directory", hdr.Name)
			}
			if err := withinDest(realDest, splitClean(filepath.Dir(hdr.Name))); err != nil {
				return fmt.Errorf("hardlink %s escapes the revision directory: %w", hdr.Name, err)
			}
			if err := withinDest(realDest, splitClean(hdr.Linkname)); err != nil {
				return fmt.Errorf("hardlink %s escapes the revision directory: %w", hdr.Name, err)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Link(linkTarget, target); err != nil {
				return fmt.Errorf("link %s: %w", target, err)
			}
		case tar.TypeXHeader, tar.TypeXGlobalHeader:
			// archive/tar already consumes pax extended/global headers and
			// merges them into the following entry's Header; nothing to do.
		default:
			return fmt.Errorf("entry %s: unsupported tar type %q", hdr.Name, hdr.Typeflag)
		}
	}
}

func safeJoin(dest, name string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("archive entry %q escapes the destination", name)
	}
	return filepath.Join(dest, clean), nil
}

// splitClean splits a slash-separated archive path into its cleaned
// components ("." and empty components removed), without resolving it
// against dest. Each caller composes these slices before handing them to
// withinDest, rather than joining the raw strings with filepath.Join/Clean
// first — Clean cancels ".." against whatever text precedes it with no
// regard for whether that text names a real directory or a symlink planted
// by an earlier entry, which is exactly the syntactic blind spot this
// module exists to close.
func splitClean(p string) []string {
	clean := filepath.Clean(filepath.FromSlash(p))
	if clean == "." {
		return nil
	}
	return strings.Split(clean, string(os.PathSeparator))
}

// maxSymlinkDepth bounds the recursion in withinDest so a cycle of
// attacker-supplied symlinks (a -> b, b -> a) fails with an error instead of
// exhausting the stack. It matches the ELOOP limit Linux and
// filepath.EvalSymlinks use.
const maxSymlinkDepth = 40

// withinDest walks comps one at a time from realDest, following any symlink
// already extracted by a prior entry exactly as the OS would when resolving
// a real path: a ".." backs the walk's current position up by one real
// directory (rejected once that position is realDest itself, since nothing
// legitimate needs to leave dest), and an existing symlink component is
// resolved via its Linkname before the walk continues. Components that
// don't exist yet are appended literally, since nothing has been planted
// there for the walk to be redirected by. It returns an error if comps
// would resolve outside realDest.
func withinDest(realDest string, comps []string) error {
	_, err := resolveComponents(realDest, realDest, comps, 0)
	return err
}

func resolveComponents(realDest, current string, comps []string, depth int) (string, error) {
	if depth > maxSymlinkDepth {
		return "", fmt.Errorf("too many levels of symbolic links")
	}
	for _, c := range comps {
		switch c {
		case "":
			continue
		case "..":
			if current == realDest {
				return "", fmt.Errorf("escapes the destination")
			}
			current = filepath.Dir(current)
		default:
			next := filepath.Join(current, c)
			fi, err := os.Lstat(next)
			switch {
			case err != nil && os.IsNotExist(err):
				current = next
			case err != nil:
				return "", err
			case fi.Mode()&os.ModeSymlink != 0:
				link, err := os.Readlink(next)
				if err != nil {
					return "", err
				}
				if filepath.IsAbs(link) {
					return "", fmt.Errorf("escapes the destination")
				}
				current, err = resolveComponents(realDest, current, splitClean(link), depth+1)
				if err != nil {
					return "", err
				}
			default:
				current = next
			}
		}
	}
	return current, nil
}
