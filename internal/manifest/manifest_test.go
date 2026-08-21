package manifest

import (
	"bytes"
	"strings"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	want := File{Version: SchemaVersion, Skills: []Entry{
		{
			Name:    "alpha",
			Source:  "https://github.com/owner/repo.git",
			Subpath: "skills/alpha",
			Ref:     "9f8e7d6c5b4a39281706f5e4d3c2b1a098765432",
			Pinned:  true,
		},
		{Name: "beta", Source: "https://github.com/owner/repo.git", Ref: "develop", Agents: []string{"claude"}},
		{Name: "some-plugin", Source: "some-plugin@marketplace"},
	}}

	var buf bytes.Buffer
	if err := Encode(&buf, want); err != nil {
		t.Fatalf("Encode: %v", err)
	}

	got, err := Decode(buf.Bytes())
	if err != nil {
		t.Fatalf("Decode: %v\n%s", err, buf.String())
	}
	if len(got.Skills) != len(want.Skills) {
		t.Fatalf("got %d skills, want %d\n%s", len(got.Skills), len(want.Skills), buf.String())
	}
	for i, w := range want.Skills {
		g := got.Skills[i]
		if g.Name != w.Name || g.Source != w.Source || g.Subpath != w.Subpath ||
			g.Ref != w.Ref || g.Pinned != w.Pinned || strings.Join(g.Agents, ",") != strings.Join(w.Agents, ",") {
			t.Errorf("skill %d = %+v, want %+v", i, g, w)
		}
	}
	if got.Version != SchemaVersion {
		t.Errorf("version = %d, want %d", got.Version, SchemaVersion)
	}
}

// An omitted field is how the manifest says "the default", so an encoder that
// wrote every field would make every manifest a statement about the machine
// that produced it.
func TestEncodeOmitsTheFieldsThatCarryNoChoice(t *testing.T) {
	var buf bytes.Buffer
	if err := Encode(&buf, File{Skills: []Entry{
		{Name: "alpha", Source: "https://github.com/owner/repo.git"},
	}}); err != nil {
		t.Fatalf("Encode: %v", err)
	}

	out := buf.String()
	for _, absent := range []string{"subpath", "ref", "pinned", "agents"} {
		if strings.Contains(out, absent) {
			t.Errorf("encoder wrote %q for an entry that made no such choice:\n%s", absent, out)
		}
	}
	// The version has to precede the first [[skill]] table, or the file is not
	// the TOML it looks like.
	if !strings.HasPrefix(strings.TrimSpace(out), "version = 1") {
		t.Errorf("version is not the first thing in the file:\n%s", out)
	}
}

// Encode fills in the version so no caller can produce a manifest without one.
func TestEncodeSuppliesTheVersion(t *testing.T) {
	var buf bytes.Buffer
	if err := Encode(&buf, File{}); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !strings.Contains(buf.String(), "version = 1") {
		t.Errorf("Encode did not supply a version:\n%s", buf.String())
	}
}

func TestDecodeTreatsAMissingVersionAsTheCurrentOne(t *testing.T) {
	f, err := Decode([]byte("[[skill]]\nname = 'alpha'\nsource = 'owner/repo'\n"))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if f.Version != SchemaVersion {
		t.Errorf("version = %d, want %d", f.Version, SchemaVersion)
	}
}

func TestDecodeRefusesAVersionFromTheFuture(t *testing.T) {
	_, err := Decode([]byte("version = 99\n"))
	if err == nil {
		t.Fatal("Decode accepted a version this build cannot understand")
	}
	if !strings.Contains(err.Error(), "upgrade skillsctl") {
		t.Errorf("the error should name the remedy, got: %v", err)
	}
}

func TestDecodeRejections(t *testing.T) {
	cases := []struct {
		name string
		toml string
		want string
	}{
		{
			name: "no name",
			toml: "[[skill]]\nsource = 'owner/repo'\n",
			want: "has no name",
		},
		{
			name: "no source",
			toml: "[[skill]]\nname = 'alpha'\n",
			want: "has no source",
		},
		{
			name: "duplicate name",
			toml: "[[skill]]\nname = 'alpha'\nsource = 'owner/repo'\n\n" +
				"[[skill]]\nname = 'alpha'\nsource = 'owner/other'\n",
			want: "named twice",
		},
		{
			name: "escaping name",
			toml: "[[skill]]\nname = '../escaped'\nsource = 'owner/repo'\n",
			want: "escaped",
		},
		{
			name: "subpath said twice, differently",
			toml: "[[skill]]\nname = 'alpha'\nsource = 'owner/repo//skills/a'\nsubpath = 'skills/b'\n",
			want: "must agree",
		},
		{
			// A non-empty source that source.Parse itself rejects, so this
			// exercises validate's "%q: %w" wrapping of source.Parse's error
			// rather than re-testing the earlier empty-source check above.
			name: "unparseable source",
			toml: "[[skill]]\nname = 'alpha'\nsource = '!!!'\n",
			want: "unrecognised source",
		},
		{
			name: "escaping subpath field",
			toml: "[[skill]]\nname = 'alpha'\nsource = 'owner/repo'\nsubpath = '../../evil'\n",
			want: "may not contain",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Decode([]byte(tc.toml))
			if err == nil {
				t.Fatalf("Decode accepted %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// The two spellings of a subpath must land on the same source, so that a
// hand-written entry installs what a bundled one does.
func TestEntryParseFoldsTheSubpathIn(t *testing.T) {
	split := Entry{Name: "a", Source: "owner/repo", Subpath: "skills/alpha"}
	joined := Entry{Name: "a", Source: "owner/repo//skills/alpha"}

	a, err := split.Parse()
	if err != nil {
		t.Fatalf("Parse split: %v", err)
	}
	b, err := joined.Parse()
	if err != nil {
		t.Fatalf("Parse joined: %v", err)
	}
	if a.RepoURL != b.RepoURL || a.Subpath != b.Subpath {
		t.Errorf("the two spellings disagree: %+v vs %+v", a, b)
	}
	if a.Subpath != "skills/alpha" {
		t.Errorf("Subpath = %q, want %q", a.Subpath, "skills/alpha")
	}
}

// A subpath named the same way twice is agreement, not a contradiction, and
// must not be appended to itself.
func TestEntryParseAcceptsAgreeingSubpaths(t *testing.T) {
	e := Entry{Name: "a", Source: "owner/repo//skills/alpha", Subpath: "skills/alpha"}
	got, err := e.Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.Subpath != "skills/alpha" {
		t.Errorf("Subpath = %q, want %q", got.Subpath, "skills/alpha")
	}
}

func TestEntryTagsRoundTripThroughTOML(t *testing.T) {
	want := File{Version: SchemaVersion, Skills: []Entry{
		{Name: "alpha", Source: "https://github.com/owner/repo.git", Tags: []string{"frontend", "team-a"}},
	}}

	var buf bytes.Buffer
	if err := Encode(&buf, want); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := Decode(buf.Bytes())
	if err != nil {
		t.Fatalf("Decode: %v\n%s", err, buf.String())
	}
	if strings.Join(got.Skills[0].Tags, ",") != "frontend,team-a" {
		t.Errorf("Tags = %v, want [frontend team-a]\n%s", got.Skills[0].Tags, buf.String())
	}
}
