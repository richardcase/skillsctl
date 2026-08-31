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
// symlink target included — that would resolve outside dest.
//
// Every mutation goes through an *os.Root opened on dest rather than through
// paths built by hand: os.Root re-resolves each path component against
// dest's own file descriptor, so it also catches an entry that only escapes
// by walking through a symlink an earlier entry in the same stream planted
// (e.g. a dir "a", a symlink "a/b" -> "..", then a file "a/b/evil" — the
// escape is invisible to a purely syntactic Clean-and-prefix check like
// safeJoin, because "b" isn't known to be a symlink until it's resolved).
func Untar(r io.Reader, dest string) error {
	root, err := os.OpenRoot(dest)
	if err != nil {
		return fmt.Errorf("open %s: %w", dest, err)
	}
	defer func() { _ = root.Close() }()

	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}

		name := filepath.FromSlash(hdr.Name)

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(name, 0o755); err != nil {
				return fmt.Errorf("create %s: %w", hdr.Name, err)
			}
		case tar.TypeReg:
			if err := root.MkdirAll(filepath.Dir(name), 0o755); err != nil {
				return fmt.Errorf("create %s: %w", filepath.Dir(hdr.Name), err)
			}
			f, err := root.OpenFile(name, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return fmt.Errorf("create %s: %w", hdr.Name, err)
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return fmt.Errorf("write %s: %w", hdr.Name, err)
			}
			if err := f.Close(); err != nil {
				return fmt.Errorf("close %s: %w", hdr.Name, err)
			}
		case tar.TypeSymlink:
			// Root.Symlink stores Linkname verbatim rather than validating
			// it — it only refuses to let a later entry follow the link
			// outside dest. Reject an escaping target here too, so a
			// malicious entry fails the extraction instead of leaving a
			// dangling symlink in the store.
			if filepath.IsAbs(hdr.Linkname) {
				return fmt.Errorf("symlink %s points outside the revision directory", hdr.Name)
			}
			if _, err := safeJoin(dest, filepath.Join(filepath.Dir(hdr.Name), hdr.Linkname)); err != nil {
				return fmt.Errorf("symlink %s escapes the revision directory", hdr.Name)
			}
			if err := root.MkdirAll(filepath.Dir(name), 0o755); err != nil {
				return err
			}
			if err := root.Symlink(hdr.Linkname, name); err != nil {
				return fmt.Errorf("symlink %s: %w", hdr.Name, err)
			}
		case tar.TypeLink:
			// Unlike a symlink target, a hardlink's Linkname is relative to
			// the archive root rather than to hdr.Name's directory.
			if err := root.MkdirAll(filepath.Dir(name), 0o755); err != nil {
				return err
			}
			if err := root.Link(filepath.FromSlash(hdr.Linkname), name); err != nil {
				return fmt.Errorf("hardlink %s: %w", hdr.Name, err)
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
