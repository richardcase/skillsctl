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
		if e.typeflag == tar.TypeSymlink {
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
}
