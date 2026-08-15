package adopt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/store"
	"github.com/richardcase/skillsctl/internal/target"
)

const skillMD = "---\nname: demo\ndescription: A demo\n---\n\nBody.\n"

// fakeGit answers Describe from a table keyed by directory. Everything else
// panics: a scan that fetched anything would not be a scan.
type fakeGit struct {
	origins map[string]gitx.Origin
}

func (f *fakeGit) Resolve(context.Context, string, string) (string, error) {
	panic("adopt must not resolve a remote")
}

func (f *fakeGit) Mirror(context.Context, string, string) error {
	panic("adopt must not mirror")
}

func (f *fakeGit) Extract(context.Context, string, string, string) error {
	panic("adopt must not extract")
}

func (f *fakeGit) Describe(_ context.Context, dir string) (gitx.Origin, error) {
	o, ok := f.origins[dir]
	if !ok {
		return gitx.Origin{}, gitx.ErrNotRepo
	}
	return o, nil
}

// fixture is one agent's skills directory plus somewhere for its links to point.
type fixture struct {
	t      *testing.T
	skills string
	src    string
	store  *store.Store
	git    *fakeGit
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{
		t:      t,
		skills: filepath.Join(root, ".claude", "skills"),
		src:    filepath.Join(root, "src"),
		store:  store.New(filepath.Join(root, "store")),
		git:    &fakeGit{origins: map[string]gitx.Origin{}},
	}
	for _, d := range []string{f.skills, f.src} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// skill creates a real skill directory under src and returns its path.
func (f *fixture) skill(name string) string {
	f.t.Helper()
	dir := filepath.Join(f.src, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		f.t.Fatal(err)
	}
	return dir
}

// link puts a symlink in the skills directory, the way a hand install does.
func (f *fixture) link(name, dest string) {
	f.t.Helper()
	if err := os.Symlink(dest, filepath.Join(f.skills, name)); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) scan(db *state.DB) Report {
	f.t.Helper()
	if db == nil {
		db = &state.DB{Receipts: map[string]*state.Receipt{}}
	}
	ts := []target.Target{{Name: "claude", Dir: f.skills}}
	rep, err := Scan(context.Background(), ts, db, f.git, f.store)
	if err != nil {
		f.t.Fatalf("Scan: %v", err)
	}
	return rep
}

func only(t *testing.T, rep Report) Entry {
	t.Helper()
	if len(rep.Entries) != 1 {
		t.Fatalf("want 1 entry, got %d: %+v", len(rep.Entries), rep.Entries)
	}
	return rep.Entries[0]
}

func TestScanClassifiesWhatItFinds(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(f *fixture)
		want   Class
		reason string // substring of the reason, when there should be one
	}{
		{
			name:  "a hand-made symlink to a skill",
			setup: func(f *fixture) { f.link("demo", f.skill("demo")) },
			want:  ClassLocal,
		},
		{
			name: "a symlink into a clean checkout with a remote",
			setup: func(f *fixture) {
				dir := f.skill("demo")
				f.git.origins[dir] = gitx.Origin{
					Prefix: "skills/demo", RepoURL: "https://example.com/owner/repo.git",
					Ref: "main", SHA: "aaaaaaa",
				}
				f.link("demo", dir)
			},
			want: ClassGit,
		},
		{
			name: "a symlink into a checkout with uncommitted changes",
			setup: func(f *fixture) {
				dir := f.skill("demo")
				f.git.origins[dir] = gitx.Origin{RepoURL: "https://example.com/owner/repo.git", SHA: "aaaaaaa", Dirty: true}
				f.link("demo", dir)
			},
			want:   ClassLocal,
			reason: "uncommitted changes",
		},
		{
			name: "a checkout with no remote",
			setup: func(f *fixture) {
				dir := f.skill("demo")
				f.git.origins[dir] = gitx.Origin{SHA: "aaaaaaa"}
				f.link("demo", dir)
			},
			want:   ClassLocal,
			reason: "no remote",
		},
		{
			name: "a real directory",
			setup: func(f *fixture) {
				dir := filepath.Join(f.skills, "demo")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					f.t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
					f.t.Fatal(err)
				}
			},
			want:   ClassSkipped,
			reason: "not a symlink",
		},
		{
			name:   "a dangling symlink",
			setup:  func(f *fixture) { f.link("demo", filepath.Join(f.src, "gone")) },
			want:   ClassSkipped,
			reason: "dangling symlink",
		},
		{
			name: "a symlink to a file",
			setup: func(f *fixture) {
				p := filepath.Join(f.src, "note.md")
				if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
					f.t.Fatal(err)
				}
				f.link("demo", p)
			},
			want:   ClassSkipped,
			reason: "not a directory",
		},
		{
			name: "a directory with no SKILL.md",
			setup: func(f *fixture) {
				dir := filepath.Join(f.src, "demo")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					f.t.Fatal(err)
				}
				f.link("demo", dir)
			},
			want:   ClassSkipped,
			reason: "SKILL.md",
		},
		{
			name: "a symlink into the store",
			setup: func(f *fixture) {
				dir := filepath.Join(f.store.Root, "rev", "slug", "aaaa")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					f.t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
					f.t.Fatal(err)
				}
				f.link("demo", dir)
			},
			want:   ClassSkipped,
			reason: "skillsctl store",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			tt.setup(f)

			got := only(t, f.scan(nil))

			if got.Class != tt.want {
				t.Errorf("Class = %q, want %q (reason: %s)", got.Class, tt.want, got.Reason)
			}
			if tt.reason == "" {
				return
			}
			if !strings.Contains(got.Reason, tt.reason) {
				t.Errorf("Reason = %q, want it to mention %q", got.Reason, tt.reason)
			}
		})
	}
}

func TestScanFollowsARelativeSymlink(t *testing.T) {
	f := newFixture(t)
	f.skill("demo")
	if err := os.Symlink(filepath.Join("..", "..", "src", "demo"), filepath.Join(f.skills, "demo")); err != nil {
		t.Fatal(err)
	}

	got := only(t, f.scan(nil))

	if got.Class != ClassLocal {
		t.Fatalf("Class = %q, want local (reason: %s)", got.Class, got.Reason)
	}
	if got.Dest != filepath.Join(f.src, "demo") {
		t.Errorf("Dest = %q, want %q", got.Dest, filepath.Join(f.src, "demo"))
	}
}

func TestScanLeavesAManagedSkillAlone(t *testing.T) {
	f := newFixture(t)
	dest := f.skill("demo")
	f.link("demo", dest)

	db := &state.DB{Receipts: map[string]*state.Receipt{
		"demo": {
			Name:  "demo",
			Links: []state.Link{{Target: "claude", Path: filepath.Join(f.skills, "demo")}},
		},
	}}

	rep := f.scan(db)

	if got := only(t, rep).Class; got != ClassManaged {
		t.Errorf("Class = %q, want managed", got)
	}
	if n := rep.Managed(); n != 1 {
		t.Errorf("Managed() = %d, want 1", n)
	}
	if len(rep.Adoptions()) != 0 {
		t.Errorf("a managed skill was adopted again: %+v", rep.Adoptions())
	}
}

// A hand-made link into a second agent, pointing where the receipt already
// says its files are, is a link that receipt should have recorded. This is the
// case `skillsctl link <name> -a <agent>` writes, found retroactively.
func TestScanAdoptsASecondLinkForAManagedSkill(t *testing.T) {
	f := newFixture(t)
	dest := f.skill("demo")
	f.link("demo", dest)

	db := &state.DB{Receipts: map[string]*state.Receipt{
		"demo": {
			Name:    "demo",
			RevPath: dest,
			Links:   []state.Link{{Target: "codex", Path: "/somewhere/else/demo"}},
		},
	}}

	rep := f.scan(db)
	if got := only(t, rep).Class; got != ClassLink {
		t.Fatalf("Class = %q, want link", got)
	}

	additions := rep.Additions()
	if len(additions) != 1 {
		t.Fatalf("want 1 addition, got %+v", additions)
	}
	if additions[0].Name != "demo" || len(additions[0].Links) != 1 {
		t.Fatalf("addition = %+v, want one claude link for demo", additions[0])
	}
	if l := additions[0].Links[0]; l.Target != "claude" || l.Path != filepath.Join(f.skills, "demo") {
		t.Errorf("link = %+v, want the symlink that is already on disk", l)
	}
	if len(rep.Adoptions()) != 0 {
		t.Errorf("adoptions = %+v, want none: the receipt already exists", rep.Adoptions())
	}
	if len(rep.Skipped()) != 0 {
		t.Errorf("skipped = %+v, want none", rep.Skipped())
	}
}

// A receipt says where its links point. Recording one that points elsewhere
// would make update re-point a directory the user never named and remove
// delete a symlink skillsctl did not create.
func TestScanRefusesASecondLinkPointingSomewhereElse(t *testing.T) {
	f := newFixture(t)
	f.link("demo", f.skill("demo"))

	db := &state.DB{Receipts: map[string]*state.Receipt{
		"demo": {
			Name:    "demo",
			RevPath: filepath.Join(f.src, "somewhere-else"),
			Links:   []state.Link{{Target: "codex", Path: "/somewhere/else/demo"}},
		},
	}}

	got := only(t, f.scan(db))

	if got.Class != ClassSkipped {
		t.Fatalf("Class = %q, want skipped", got.Class)
	}
	for _, want := range []string{filepath.Join(f.src, "demo"), filepath.Join(f.src, "somewhere-else")} {
		if !strings.Contains(got.Reason, want) {
			t.Errorf("Reason = %q, want it to name %s", got.Reason, want)
		}
	}
}

// Links is a set keyed by target: Remove builds its drop filter from the
// target name, so a receipt with two links for one agent would plan two
// unlinks of one path and swallow the second.
func TestScanRefusesASecondLinkForAnAgentTheReceiptAlreadyRecords(t *testing.T) {
	f := newFixture(t)
	dest := f.skill("demo")
	f.link("demo", dest)

	db := &state.DB{Receipts: map[string]*state.Receipt{
		"demo": {
			Name:    "demo",
			RevPath: dest,
			Links:   []state.Link{{Target: "claude", Path: "/a/different/path/demo"}},
		},
	}}

	got := only(t, f.scan(db))

	if got.Class != ClassSkipped {
		t.Fatalf("Class = %q, want skipped", got.Class)
	}
	if !strings.Contains(got.Reason, "claude") {
		t.Errorf("Reason = %q, want it to name the agent already recorded", got.Reason)
	}
}

// Receipts are keyed by name, so two agents hand-linked to one managed skill
// are one addition carrying two links rather than two that would overwrite
// each other — the same reason Adoptions groups.
func TestAdditionsMergeTwoAgentsHandLinkedToOneManagedSkill(t *testing.T) {
	f := newFixture(t)
	dest := f.skill("demo")
	f.link("demo", dest)

	codex := filepath.Join(filepath.Dir(filepath.Dir(f.skills)), ".codex", "skills")
	if err := os.MkdirAll(codex, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dest, filepath.Join(codex, "demo")); err != nil {
		t.Fatal(err)
	}

	db := &state.DB{Receipts: map[string]*state.Receipt{
		"demo": {
			Name:    "demo",
			RevPath: dest,
			Links:   []state.Link{{Target: "gemini", Path: "/elsewhere/demo"}},
		},
	}}

	ts := []target.Target{{Name: "claude", Dir: f.skills}, {Name: "codex", Dir: codex}}
	rep, err := Scan(context.Background(), ts, db, f.git, f.store)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	additions := rep.Additions()
	if len(additions) != 1 {
		t.Fatalf("want 1 addition, got %+v", additions)
	}
	if len(additions[0].Links) != 2 {
		t.Errorf("links = %+v, want one per agent", additions[0].Links)
	}
}

func TestAdoptionsMergeOneSkillLinkedIntoTwoAgents(t *testing.T) {
	f := newFixture(t)
	dest := f.skill("demo")
	f.link("demo", dest)

	codex := filepath.Join(filepath.Dir(filepath.Dir(f.skills)), ".codex", "skills")
	if err := os.MkdirAll(codex, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dest, filepath.Join(codex, "demo")); err != nil {
		t.Fatal(err)
	}

	ts := []target.Target{{Name: "claude", Dir: f.skills}, {Name: "codex", Dir: codex}}
	rep, err := Scan(context.Background(), ts, &state.DB{Receipts: map[string]*state.Receipt{}}, f.git, f.store)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	adoptions := rep.Adoptions()
	if len(adoptions) != 1 {
		t.Fatalf("want 1 adoption, got %d: %+v", len(adoptions), adoptions)
	}
	if len(adoptions[0].Links) != 2 {
		t.Fatalf("want 2 links, got %+v", adoptions[0].Links)
	}
	if len(rep.Skipped()) != 0 {
		t.Errorf("nothing should be skipped: %+v", rep.Skipped())
	}
}

func TestAdoptionsRefuseOneNameWithTwoDestinations(t *testing.T) {
	f := newFixture(t)
	f.link("demo", f.skill("demo"))

	codex := filepath.Join(filepath.Dir(filepath.Dir(f.skills)), ".codex", "skills")
	if err := os.MkdirAll(codex, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(f.skill("other"), filepath.Join(codex, "demo")); err != nil {
		t.Fatal(err)
	}

	ts := []target.Target{{Name: "claude", Dir: f.skills}, {Name: "codex", Dir: codex}}
	rep, err := Scan(context.Background(), ts, &state.DB{Receipts: map[string]*state.Receipt{}}, f.git, f.store)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	if got := rep.Adoptions(); len(got) != 0 {
		t.Fatalf("a name pointing at two directories was adopted: %+v", got)
	}
	skipped := rep.Skipped()
	if len(skipped) != 2 {
		t.Fatalf("want both halves reported, got %+v", skipped)
	}
	if !strings.Contains(skipped[0].Reason, "different directory") {
		t.Errorf("Reason = %q, want it to explain the collision", skipped[0].Reason)
	}
}

func TestScanIgnoresAMissingSkillsDirectory(t *testing.T) {
	f := newFixture(t)
	ts := []target.Target{{Name: "gemini", Dir: filepath.Join(f.skills, "..", "..", ".gemini", "skills")}}

	rep, err := Scan(context.Background(), ts, &state.DB{Receipts: map[string]*state.Receipt{}}, f.git, f.store)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(rep.Entries) != 0 {
		t.Errorf("want no entries, got %+v", rep.Entries)
	}
}

func TestScanRecordsWhereAPromotedSkillCameFrom(t *testing.T) {
	f := newFixture(t)
	dir := f.skill("demo")
	f.git.origins[dir] = gitx.Origin{
		Prefix: "skills/demo", RepoURL: "https://example.com/owner/repo.git",
		Ref: "main", SHA: "0123456789abcdef0123456789abcdef01234567",
	}
	f.link("demo", dir)

	got := only(t, f.scan(nil))

	if got.Class != ClassGit {
		t.Fatalf("Class = %q, want git (reason: %s)", got.Class, got.Reason)
	}
	if got.Repo == nil {
		t.Fatal("no provenance recorded for a promoted entry")
	}
	if got.Repo.SHA != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("SHA = %q", got.Repo.SHA)
	}
	if got.Repo.Subpath != "skills/demo" {
		t.Errorf("Subpath = %q, want skills/demo", got.Repo.Subpath)
	}
	if got.Repo.Repo.RepoURL != "https://example.com/owner/repo.git" {
		t.Errorf("RepoURL = %q", got.Repo.Repo.RepoURL)
	}
}

func TestScanKeepsACheckoutWhoseRemoteIsNotAGitSourceLocal(t *testing.T) {
	f := newFixture(t)
	dir := f.skill("demo")
	// git is happy with a filesystem remote; skillsctl could not install from it.
	f.git.origins[dir] = gitx.Origin{RepoURL: "/srv/git/repo.git", SHA: "aaaa"}
	f.link("demo", dir)

	got := only(t, f.scan(nil))

	if got.Class != ClassLocal {
		t.Fatalf("Class = %q, want local", got.Class)
	}
	if !strings.Contains(got.Reason, "not a git source") {
		t.Errorf("Reason = %q, want it to explain the remote", got.Reason)
	}
}

func TestScanRecognisesAManagedSkillBeforeJudgingWhereItPoints(t *testing.T) {
	f := newFixture(t)
	dest := filepath.Join(f.store.Root, "rev", "slug", "aaaa")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		t.Fatal(err)
	}
	f.link("demo", dest)

	db := &state.DB{Receipts: map[string]*state.Receipt{
		"demo": {
			Name:  "demo",
			Links: []state.Link{{Target: "claude", Path: filepath.Join(f.skills, "demo")}},
		},
	}}

	// Everything skillsctl installs points into the store; that must not read
	// as the orphan case.
	if got := only(t, f.scan(db)).Class; got != ClassManaged {
		t.Errorf("Class = %q, want managed", got)
	}
}

func TestScanIgnoresTheAgentsOwnDotDirectories(t *testing.T) {
	f := newFixture(t)
	// codex keeps one of these in its skills directory.
	if err := os.MkdirAll(filepath.Join(f.skills, ".system"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.link("demo", f.skill("demo"))

	rep := f.scan(nil)

	if got := only(t, rep).Name; got != "demo" {
		t.Errorf("scanned %q; a dot entry is the agent's bookkeeping, not a skill", got)
	}
	if len(rep.Skipped()) != 0 {
		t.Errorf("a dot entry was reported as unadoptable: %+v", rep.Skipped())
	}
}
