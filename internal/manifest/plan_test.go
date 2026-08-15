package manifest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/channel"
	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/store"
	"github.com/richardcase/skillsctl/internal/target"
	"github.com/richardcase/skillsctl/internal/testrepo"
)

const planSkillMD = "---\nname: demo\ndescription: A demo\n---\n\nBody.\n"

// planFixture is a real repository, a real store and a real git channel. Like
// internal/update's tests, nothing is faked and nothing reaches the network:
// testrepo builds a file:// repository on disk.
type planFixture struct {
	reg  channel.Registry
	cfg  target.Config
	url  string
	sha  string
	dirs map[string]string
}

func newPlanFixture(t *testing.T) *planFixture {
	t.Helper()

	url, sha := testrepo.New(t, map[string]string{"SKILL.md": planSkillMD})
	agents := t.TempDir()
	claude := filepath.Join(agents, "claude")
	codex := filepath.Join(agents, "codex")

	st := store.New(t.TempDir())
	return &planFixture{
		reg: channel.Registry{Git: channel.NewGit(st, gitx.New()), Local: channel.NewLocal(st)},
		cfg: target.Config{Targets: []target.Target{
			{Name: "claude", Dir: claude},
			{Name: "codex", Dir: codex},
		}},
		url:  url,
		sha:  sha,
		dirs: map[string]string{"claude": claude, "codex": codex},
	}
}

// agents makes both target directories exist, so Present returns both. A target
// counts as present when its parent directory exists.
func (f *planFixture) agents(t *testing.T) *planFixture {
	t.Helper()
	for _, d := range f.dirs {
		if err := mkdirAll(filepath.Dir(d)); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *planFixture) plan(t *testing.T, file File, db *state.DB) (Report, int) {
	t.Helper()
	if db == nil {
		db = &state.DB{Receipts: map[string]*state.Receipt{}}
	}
	rep, p := Plan(context.Background(), f.reg, file, db, f.cfg)
	return rep, len(p.Ops)
}

// installedReceipt is the receipt an install of the fixture repository writes.
func (f *planFixture) installedReceipt(t *testing.T, targets ...string) *state.Receipt {
	t.Helper()
	src, err := source.Parse(f.url)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	r := &state.Receipt{
		Name:     "demo",
		Channel:  "git",
		Source:   src.RepoURL,
		Slug:     src.Slug(),
		Resolved: f.sha,
		RevPath:  t.TempDir(),
	}
	for _, tn := range targets {
		r.Links = append(r.Links, state.Link{Target: tn, Path: filepath.Join(f.dirs[tn], "demo")})
	}
	return r
}

func TestPlanInstallsAnEntryThisMachineLacks(t *testing.T) {
	f := newPlanFixture(t).agents(t)

	rep, ops := f.plan(t, File{Skills: []Entry{{Name: "demo", Source: f.url}}}, nil)

	if len(rep.Verdicts) != 1 {
		t.Fatalf("verdicts = %+v, want 1", rep.Verdicts)
	}
	v := rep.Verdicts[0]
	if v.Status != StatusInstalled {
		t.Fatalf("status = %q (%s), want %q", v.Status, v.Detail, StatusInstalled)
	}
	if v.Version != f.sha {
		t.Errorf("version = %q, want %q", v.Version, f.sha)
	}
	// Two links plus one receipt.
	if ops != 3 {
		t.Errorf("plan has %d ops, want 3", ops)
	}
}

// The entry's name is the receipt key, so it wins over SKILL.md, exactly as
// --as does at install time.
func TestPlanInstallsUnderTheEntryName(t *testing.T) {
	f := newPlanFixture(t).agents(t)

	rep, _ := f.plan(t, File{Skills: []Entry{{Name: "renamed", Source: f.url}}}, nil)

	if len(rep.Verdicts) != 1 || rep.Verdicts[0].Status != StatusInstalled {
		t.Fatalf("verdicts = %+v, want one installed", rep.Verdicts)
	}
	if rep.Verdicts[0].Name != "renamed" {
		t.Errorf("name = %q, want renamed", rep.Verdicts[0].Name)
	}
}

func TestPlanSaysNothingAboutAnEntryAlreadySatisfied(t *testing.T) {
	f := newPlanFixture(t).agents(t)
	db := &state.DB{Receipts: map[string]*state.Receipt{
		"demo": f.installedReceipt(t, "claude", "codex"),
	}}

	rep, ops := f.plan(t, File{Skills: []Entry{{Name: "demo", Source: f.url}}}, db)

	if len(rep.Verdicts) != 1 || rep.Verdicts[0].Status != StatusPresent {
		t.Fatalf("verdicts = %+v, want one present", rep.Verdicts)
	}
	if ops != 0 {
		t.Errorf("plan has %d ops, want none for a satisfied entry", ops)
	}
}

func TestPlanLinksAnAgentTheEntryNamesAndTheReceiptLacks(t *testing.T) {
	f := newPlanFixture(t).agents(t)
	r := f.installedReceipt(t, "claude")
	// Link refuses to plan a link whose RevPath is not a directory, so the
	// receipt has to point at one that exists.
	db := &state.DB{Receipts: map[string]*state.Receipt{"demo": r}}

	rep, ops := f.plan(t, File{Skills: []Entry{{Name: "demo", Source: f.url}}}, db)

	if len(rep.Verdicts) != 1 {
		t.Fatalf("verdicts = %+v, want 1", rep.Verdicts)
	}
	v := rep.Verdicts[0]
	if v.Status != StatusLinked {
		t.Fatalf("status = %q (%s), want %q", v.Status, v.Detail, StatusLinked)
	}
	if strings.Join(v.Agents, ",") != "codex" {
		t.Errorf("agents = %v, want the one that was missing", v.Agents)
	}
	// One link plus one receipt.
	if ops != 2 {
		t.Errorf("plan has %d ops, want 2", ops)
	}
}

func TestPlanReportsADifferenceAndChangesNothing(t *testing.T) {
	f := newPlanFixture(t).agents(t)
	r := f.installedReceipt(t, "claude", "codex")
	r.Ref = "main"
	db := &state.DB{Receipts: map[string]*state.Receipt{"demo": r}}

	rep, ops := f.plan(t, File{Skills: []Entry{{Name: "demo", Source: f.url, Ref: "develop"}}}, db)

	if len(rep.Verdicts) != 1 || rep.Verdicts[0].Status != StatusDiffers {
		t.Fatalf("verdicts = %+v, want one differs", rep.Verdicts)
	}
	if !strings.Contains(rep.Verdicts[0].Detail, "develop") || !strings.Contains(rep.Verdicts[0].Detail, "main") {
		t.Errorf("detail = %q, want both refs named", rep.Verdicts[0].Detail)
	}
	if ops != 0 {
		t.Errorf("sync only ever adds, but the plan has %d ops", ops)
	}
}

func TestPlanReportsAPinDifference(t *testing.T) {
	f := newPlanFixture(t).agents(t)
	r := f.installedReceipt(t, "claude", "codex")
	db := &state.DB{Receipts: map[string]*state.Receipt{"demo": r}}

	rep, ops := f.plan(t, File{Skills: []Entry{{Name: "demo", Source: f.url, Ref: f.sha, Pinned: true}}}, db)

	if len(rep.Verdicts) != 1 || rep.Verdicts[0].Status != StatusDiffers {
		t.Fatalf("verdicts = %+v, want one differs", rep.Verdicts)
	}
	if ops != 0 {
		t.Errorf("plan has %d ops, want none", ops)
	}
}

func TestPlanReportsSkillsTheManifestNeverNamed(t *testing.T) {
	f := newPlanFixture(t).agents(t)
	db := &state.DB{Receipts: map[string]*state.Receipt{
		"demo":  f.installedReceipt(t, "claude", "codex"),
		"other": {Name: "other", Channel: "git", Source: "https://github.com/x/y.git"},
	}}

	rep, _ := f.plan(t, File{Skills: []Entry{{Name: "demo", Source: f.url}}}, db)

	if len(rep.Extra) != 1 || rep.Extra[0].Name != "other" {
		t.Errorf("extra = %+v, want the skill the manifest never named", rep.Extra)
	}
}

// One entry that cannot be installed must not hide the rest of the report.
func TestPlanKeepsGoingAfterAnEntryThatFails(t *testing.T) {
	f := newPlanFixture(t).agents(t)

	rep, _ := f.plan(t, File{Skills: []Entry{
		{Name: "broken", Source: "file:///nonexistent/repo.git"},
		{Name: "demo", Source: f.url},
	}}, nil)

	if len(rep.Verdicts) != 2 {
		t.Fatalf("verdicts = %+v, want one per entry", rep.Verdicts)
	}
	if rep.Verdicts[0].Status != StatusError {
		t.Errorf("verdict 0 = %+v, want an error", rep.Verdicts[0])
	}
	if rep.Verdicts[0].Detail == "" {
		t.Error("an error verdict has to say why")
	}
	if rep.Verdicts[1].Status != StatusInstalled {
		t.Errorf("verdict 1 = %+v, want the good entry still installed", rep.Verdicts[1])
	}
}

func TestPlanInstallsIntoOnlyTheAgentsAnEntryNames(t *testing.T) {
	f := newPlanFixture(t).agents(t)

	rep, ops := f.plan(t, File{Skills: []Entry{
		{Name: "demo", Source: f.url, Agents: []string{"claude"}},
	}}, nil)

	if len(rep.Verdicts) != 1 || rep.Verdicts[0].Status != StatusInstalled {
		t.Fatalf("verdicts = %+v, want one installed", rep.Verdicts)
	}
	if strings.Join(rep.Verdicts[0].Agents, ",") != "claude" {
		t.Errorf("agents = %v, want only claude", rep.Verdicts[0].Agents)
	}
	// One link plus one receipt.
	if ops != 2 {
		t.Errorf("plan has %d ops, want 2", ops)
	}
}

func TestPlanReportsAnUnknownAgent(t *testing.T) {
	f := newPlanFixture(t).agents(t)

	rep, _ := f.plan(t, File{Skills: []Entry{
		{Name: "demo", Source: f.url, Agents: []string{"nosuchagent"}},
	}}, nil)

	if len(rep.Verdicts) != 1 || rep.Verdicts[0].Status != StatusError {
		t.Fatalf("verdicts = %+v, want one error", rep.Verdicts)
	}
	if !strings.Contains(rep.Verdicts[0].Detail, "nosuchagent") {
		t.Errorf("detail = %q, want the unknown agent named", rep.Verdicts[0].Detail)
	}
}

func mkdirAll(dir string) error { return os.MkdirAll(dir, 0o755) }
