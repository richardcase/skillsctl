package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// HashDir fingerprints a directory tree by path, mode and content. It is how
// skillsctl notices that someone edited a skill through its symlink, since
// revision directories carry no .git of their own.
func HashDir(root string) (string, error) {
	type entry struct {
		rel  string
		mode fs.FileMode
		sum  string
	}
	var entries []entry

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}

		sum := ""
		if info.Mode()&os.ModeSymlink != 0 {
			dest, err := os.Readlink(p)
			if err != nil {
				return err
			}
			h := sha256.Sum256([]byte(dest))
			sum = hex.EncodeToString(h[:])
		} else {
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			h := sha256.New()
			_, cerr := io.Copy(h, f)
			_ = f.Close()
			if cerr != nil {
				return cerr
			}
			sum = hex.EncodeToString(h.Sum(nil))
		}

		entries = append(entries, entry{rel: filepath.ToSlash(rel), mode: info.Mode(), sum: sum})
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("hash %s: %w", root, err)
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].rel < entries[j].rel })

	h := sha256.New()
	for _, e := range entries {
		_, _ = fmt.Fprintf(h, "%s\x00%o\x00%s\n", e.rel, e.mode.Perm(), e.sum)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
