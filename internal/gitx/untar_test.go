package gitx

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// tarEntry is one header/body pair to write into a test tar stream.
type tarEntry struct {
	name     string // hdr.Name
	linkname string // hdr.Linkname, for symlinks
	typeflag byte
	body     string
}

func buildTar(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		hdr := &tar.Header{
			Name:     e.name,
			Linkname: e.linkname,
			Typeflag: e.typeflag,
			Mode:     0o644,
			Size:     int64(len(e.body)),
		}
		if e.typeflag == tar.TypeDir {
			hdr.Mode = 0o755
			hdr.Size = 0
		}
		switch e.typeflag {
		case tar.TypeSymlink, tar.TypeLink, tar.TypeChar, tar.TypeBlock, tar.TypeFifo:
			hdr.Size = 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header for %q: %v", e.name, err)
		}
		if e.typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatalf("write body for %q: %v", e.name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	return buf.Bytes()
}

// TestUntarRejectsMaliciousEntries proves untar refuses every entry that would
// write outside dest, and that nothing is ever created at the escaped
// location — this is the security-critical path archive entries from an
// arbitrary git repository flow through, so it is tested directly rather than
// only through the full Extract pipeline.
func TestUntarRejectsMaliciousEntries(t *testing.T) {
	tests := []struct {
		name    string
		entries []tarEntry
		// mustNotExist is computed per-case below, relative to dest, plus any
		// fixed absolute paths a bug would have written to.
		mustNotExist func(dest string) []string
	}{
		{
			name: "regular file named ../escape",
			entries: []tarEntry{
				{name: "../escape", typeflag: tar.TypeReg, body: "pwned"},
			},
			mustNotExist: func(dest string) []string {
				return []string{filepath.Join(filepath.Dir(dest), "escape")}
			},
		},
		{
			name: "regular file named a/../../escape",
			entries: []tarEntry{
				{name: "a/../../escape", typeflag: tar.TypeReg, body: "pwned"},
			},
			mustNotExist: func(dest string) []string {
				// a/../../escape cleans to ../escape relative to dest.
				return []string{filepath.Join(filepath.Dir(dest), "escape")}
			},
		},
		{
			name: "regular file with an absolute name",
			entries: []tarEntry{
				{name: "/tmp/skillsctl-untar-test-escape", typeflag: tar.TypeReg, body: "pwned"},
			},
			mustNotExist: func(string) []string {
				return []string{"/tmp/skillsctl-untar-test-escape"}
			},
		},
		{
			name: "symlink with an absolute linkname",
			entries: []tarEntry{
				{name: "link", typeflag: tar.TypeSymlink, linkname: "/etc/passwd"},
			},
			mustNotExist: func(dest string) []string {
				return []string{filepath.Join(dest, "link")}
			},
		},
		{
			name: "symlink whose linkname escapes relatively",
			entries: []tarEntry{
				{name: "link", typeflag: tar.TypeSymlink, linkname: "../../escape"},
			},
			mustNotExist: func(dest string) []string {
				return []string{filepath.Join(dest, "link")}
			},
		},
		{
			name: "hardlink whose linkname escapes relatively",
			entries: []tarEntry{
				{name: "link", typeflag: tar.TypeLink, linkname: "../../escape"},
			},
			mustNotExist: func(dest string) []string {
				return []string{filepath.Join(dest, "link")}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tempRoot := t.TempDir()
			dest := filepath.Join(tempRoot, "dest")
			if err := os.MkdirAll(dest, 0o755); err != nil {
				t.Fatal(err)
			}

			data := buildTar(t, tc.entries)
			if err := Untar(bytes.NewReader(data), dest); err == nil {
				t.Fatalf("untar accepted a malicious entry: %+v", tc.entries)
			}

			for _, bad := range tc.mustNotExist(dest) {
				if _, err := os.Lstat(bad); err == nil {
					t.Errorf("found a file at %s: the malicious entry escaped dest", bad)
					_ = os.RemoveAll(bad)
				}
			}
		})
	}
}

// TestUntarExtractsWellBehavedEntries is the positive control for
// TestUntarRejectsMaliciousEntries: ordinary nested files and a relative
// symlink that stays inside dest must both extract successfully.
func TestUntarExtractsWellBehavedEntries(t *testing.T) {
	dest := t.TempDir()
	entries := []tarEntry{
		{name: "a", typeflag: tar.TypeDir},
		{name: "a/b.md", typeflag: tar.TypeReg, body: "hello"},
		{name: "a/link", typeflag: tar.TypeSymlink, linkname: "b.md"},
		{name: "a/hardlink", typeflag: tar.TypeLink, linkname: "a/b.md"},
	}

	if err := Untar(bytes.NewReader(buildTar(t, entries)), dest); err != nil {
		t.Fatalf("untar of well-behaved entries failed: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(dest, "a", "b.md"))
	if err != nil {
		t.Fatalf("a/b.md missing: %v", err)
	}
	if string(body) != "hello" {
		t.Errorf("a/b.md = %q, want %q", body, "hello")
	}

	linkTarget, err := os.Readlink(filepath.Join(dest, "a", "link"))
	if err != nil {
		t.Fatalf("a/link missing: %v", err)
	}
	if linkTarget != "b.md" {
		t.Errorf("a/link -> %q, want %q", linkTarget, "b.md")
	}
	resolved, err := os.ReadFile(filepath.Join(dest, "a", "link"))
	if err != nil {
		t.Fatalf("a/link does not resolve: %v", err)
	}
	if string(resolved) != "hello" {
		t.Errorf("a/link resolves to %q, want %q", resolved, "hello")
	}

	original, err := os.Stat(filepath.Join(dest, "a", "b.md"))
	if err != nil {
		t.Fatalf("a/b.md missing: %v", err)
	}
	hardlink, err := os.Stat(filepath.Join(dest, "a", "hardlink"))
	if err != nil {
		t.Fatalf("a/hardlink missing: %v", err)
	}
	if !os.SameFile(original, hardlink) {
		t.Errorf("a/hardlink is not the same file as a/b.md")
	}
}

// TestUntarEntryTypes proves every tar entry type skillsctl distinguishes is
// handled deliberately: representable types extract, and types with no
// filesystem representation (device nodes, FIFOs) fail loudly instead of
// being silently dropped.
func TestUntarEntryTypes(t *testing.T) {
	tests := []struct {
		name      string
		typeflag  byte
		wantError bool
	}{
		{name: "regular file", typeflag: tar.TypeReg},
		{name: "directory", typeflag: tar.TypeDir},
		{name: "symlink", typeflag: tar.TypeSymlink},
		{name: "hardlink", typeflag: tar.TypeLink},
		{name: "character device", typeflag: tar.TypeChar, wantError: true},
		{name: "block device", typeflag: tar.TypeBlock, wantError: true},
		{name: "fifo", typeflag: tar.TypeFifo, wantError: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dest := t.TempDir()
			entries := []tarEntry{
				{name: "base", typeflag: tar.TypeReg, body: "hello"},
			}
			switch tc.typeflag {
			case tar.TypeDir:
				entries = []tarEntry{{name: "entry", typeflag: tar.TypeDir}}
			case tar.TypeSymlink, tar.TypeLink:
				entries = append(entries, tarEntry{name: "entry", typeflag: tc.typeflag, linkname: "base"})
			default:
				entries = append(entries, tarEntry{name: "entry", typeflag: tc.typeflag})
			}

			err := Untar(bytes.NewReader(buildTar(t, entries)), dest)
			if tc.wantError {
				if err == nil {
					t.Fatalf("untar accepted a %s entry", tc.name)
				}
				if _, statErr := os.Lstat(filepath.Join(dest, "entry")); statErr == nil {
					t.Errorf("untar created %s for a %s entry it should have rejected", filepath.Join(dest, "entry"), tc.name)
				}
				return
			}
			if err != nil {
				t.Fatalf("untar of a %s entry failed: %v", tc.name, err)
			}
		})
	}
}
