package target

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInspectNamesEachLinkState(t *testing.T) {
	root := t.TempDir()

	want := filepath.Join(root, "rev", "my-skill")
	other := filepath.Join(root, "elsewhere")
	for _, d := range []string{want, other, filepath.Join(root, "links")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	link := func(name, dest string) string {
		t.Helper()
		p := filepath.Join(root, "links", name)
		if err := os.Symlink(dest, p); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// A relative target is resolved against the directory holding the link, so
	// this one names the same directory as `want` and must read as LinkOK.
	rel, err := filepath.Rel(filepath.Join(root, "links"), want)
	if err != nil {
		t.Fatal(err)
	}

	plain := filepath.Join(root, "links", "a-real-directory")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}

	// The dangling case needs a revision path that is not there, so it carries
	// its own want: a link is dangling only when it points where it was told to
	// and that directory has gone.
	vanished := filepath.Join(root, "rev", "vanished")

	cases := []struct {
		name     string
		path     string
		want     string
		state    LinkState
		wantDest string
	}{
		{"points at the revision", link("ok", want), want, LinkOK, want},
		{"relative target", link("rel", rel), want, LinkOK, want},
		{"points somewhere else", link("elsewhere", other), want, LinkElsewhere, other},
		{"target is gone", link("dangling", vanished), vanished, LinkDangling, vanished},
		{"nothing is there", filepath.Join(root, "links", "absent"), want, LinkMissing, ""},
		{"not a symlink", plain, want, LinkForeign, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, dest, err := Inspect(tc.path, tc.want)
			if err != nil {
				t.Fatalf("Inspect: unexpected error %v", err)
			}
			if got != tc.state {
				t.Errorf("state = %v, want %v", got, tc.state)
			}
			if dest != tc.wantDest {
				t.Errorf("dest = %q, want %q", dest, tc.wantDest)
			}
		})
	}
}

// A dangling link that never pointed at the revision is reported as elsewhere:
// "this is not ours" is the stronger fact, and the destination is returned
// either way so the user can see what it is.
func TestInspectPrefersElsewhereOverDangling(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(root, "my-skill")
	dest := filepath.Join(root, "somewhere", "gone")
	if err := os.Symlink(dest, link); err != nil {
		t.Fatal(err)
	}

	got, gotDest, err := Inspect(link, filepath.Join(root, "rev"))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if got != LinkElsewhere {
		t.Errorf("state = %v, want LinkElsewhere", got)
	}
	if gotDest != dest {
		t.Errorf("dest = %q, want %q", gotDest, dest)
	}
}

func TestInspectStatesRenderAsWords(t *testing.T) {
	for _, s := range []LinkState{LinkOK, LinkElsewhere, LinkDangling, LinkMissing, LinkForeign, LinkUnreadable} {
		if s.String() == "" {
			t.Errorf("LinkState(%d) renders as an empty string", s)
		}
	}
}
