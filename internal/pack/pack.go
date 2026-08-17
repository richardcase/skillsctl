// Package pack builds an uncompressed tar stream from a directory tree,
// excluding .git and anything .gitignore marks ignored — the input to an OCI
// skills layer.
package pack

import (
	"archive/tar"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	ignore "github.com/sabhiram/go-gitignore"
)

// scope is one .gitignore's reach: it applies to root and everything under
// it, never to a sibling — the same reach git itself gives a nested
// .gitignore.
type scope struct {
	root string
	m    *ignore.GitIgnore
}

// Tar writes dir's tree into w as an uncompressed tar stream. A .git
// directory is skipped unconditionally, at any depth. A .gitignore file is
// honoured for its own directory and below.
func Tar(w io.Writer, dir string) error {
	tw := tar.NewWriter(w)
	var scopes []scope

	if m, err := loadGitignore(dir); err != nil {
		return err
	} else if m != nil {
		scopes = append(scopes, scope{root: dir, m: m})
	}

	walk := func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == dir {
			return nil
		}
		if d.IsDir() && d.Name() == ".git" {
			return fs.SkipDir
		}
		if ignored(scopes, p) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		if d.IsDir() {
			if m, err := loadGitignore(p); err != nil {
				return err
			} else if m != nil {
				scopes = append(scopes, scope{root: p, m: m})
			}
			return writeHeader(tw, rel+"/", d)
		}
		return writeEntry(tw, p, rel, d)
	}

	if err := filepath.WalkDir(dir, walk); err != nil {
		return err
	}
	return tw.Close()
}

// ignored reports whether p falls under any scope whose .gitignore matches
// it, checked with each scope's patterns applied relative to that scope's
// own root rather than the tar root — which is what keeps a nested
// .gitignore from reaching into a sibling directory.
func ignored(scopes []scope, p string) bool {
	for _, sc := range scopes {
		rel, err := filepath.Rel(sc.root, p)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if sc.m.MatchesPath(filepath.ToSlash(rel)) {
			return true
		}
	}
	return false
}

func loadGitignore(dir string) (*ignore.GitIgnore, error) {
	p := filepath.Join(dir, ".gitignore")
	if fi, err := os.Stat(p); err != nil || fi.IsDir() {
		return nil, nil
	}
	m, err := ignore.CompileIgnoreFile(p)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	return m, nil
}

func writeHeader(tw *tar.Writer, name string, d fs.DirEntry) error {
	info, err := d.Info()
	if err != nil {
		return err
	}
	hdr, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	hdr.Name = name
	return tw.WriteHeader(hdr)
}

func writeEntry(tw *tar.Writer, p, rel string, d fs.DirEntry) error {
	info, err := d.Info()
	if err != nil {
		return err
	}

	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(p)
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(info, target)
		if err != nil {
			return err
		}
		hdr.Name = rel
		return tw.WriteHeader(hdr)
	}
	if !info.Mode().IsRegular() {
		return nil
	}

	hdr, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	hdr.Name = rel
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}

	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(tw, f)
	return err
}
