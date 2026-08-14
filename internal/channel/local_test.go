package channel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/store"
	"github.com/richardcase/skillsctl/internal/target"
)

const localSkillMD = "---\nname: my-skill\ndescription: A skill under development\n---\n\nBody.\n"

// localFixture is a skill directory outside the store, and the request that
// would link it.
type localFixture struct {
	ch    *Local
	dir   string
	store *store.Store
	agent target.Target
}

func newLocalFixture(t *testing.T, files map[string]string) *localFixture {
	t.Helper()

	dir := t.TempDir()
	if files == nil {
		files = map[string]string{"SKILL.md": localSkillMD}
	}
	for rel, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	st := store.New(t.TempDir())
	return &localFixture{
		ch:    NewLocal(st),
		dir:   dir,
		store: st,
		agent: target.Target{Name: "claude", Dir: filepath.Join(t.TempDir(), "claude", "skills")},
	}
}

func (f *localFixture) request(path string) Request {
	src, err := source.Parse(path)
	if err != nil {
		panic(err)
	}
	return Request{Source: src, Targets: []target.Target{f.agent}}
}

func TestLocalOwnershipIsTheUsers(t *testing.T) {
	f := newLocalFixture(t, nil)
	if f.ch.Ownership() != UserOwned {
		t.Error("a local skill's files are the user's own, so gc must not count them and remove must still unlink")
	}
}

func TestLocalInstallLinksInPlaceAndRecordsNothingFromTheStore(t *testing.T) {
	f := newLocalFixture(t, nil)
	req := f.request(f.dir)

	cands, err := f.ch.Prepare(context.Background(), req)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if len(cands) != 1 || cands[0].Name != "my-skill" {
		t.Fatalf("candidates = %+v, want the one skill named from its frontmatter", cands)
	}

	p, receipts, err := f.ch.Install(req, cands)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	link, ok := p.Ops[0].(plan.Link)
	if !ok {
		t.Fatalf("op 0 is %T, want plan.Link", p.Ops[0])
	}
	if link.RevPath != f.dir {
		t.Errorf("link points at %q, want the source directory %q: nothing is copied", link.RevPath, f.dir)
	}

	r := receipts[0]
	if r.Channel != string(source.ChannelLocal) {
		t.Errorf("channel = %q, want local", r.Channel)
	}
	if r.Source != f.dir || r.RevPath != f.dir {
		t.Errorf("receipt = {Source:%q RevPath:%q}, want both the source directory", r.Source, r.RevPath)
	}
	if r.Slug != "" {
		t.Error("a local receipt must record no slug: a slug says where in the store something lives, and nothing is there")
	}
	if r.Resolved != "" {
		t.Error("a local skill has no resolved revision")
	}
	if r.ContentHash != "" {
		t.Error("a local skill is meant to be edited, so hashing it would manufacture a dirtiness that means nothing")
	}
	if len(r.Links) != 1 || r.Links[0].Target != "claude" {
		t.Errorf("links = %+v, want the removal contract recorded like any other linked channel", r.Links)
	}
}

func TestLocalResolvesRelativeAndTildePathsToSomethingStable(t *testing.T) {
	f := newLocalFixture(t, nil)

	// A receipt outlives the shell that wrote it, so a relative path has to
	// become absolute before it is recorded.
	t.Chdir(filepath.Dir(f.dir))
	req := f.request("./" + filepath.Base(f.dir))

	cands, err := f.ch.Prepare(context.Background(), req)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !filepath.IsAbs(cands[0].Path) {
		t.Errorf("path = %q, want it absolute", cands[0].Path)
	}
}

// `skillsctl install .` inside a skill directory should name the skill after
// that directory, not call it ".".
func TestLocalNamesADotPathAfterItsDirectory(t *testing.T) {
	f := newLocalFixture(t, map[string]string{"SKILL.md": "# No frontmatter\n"})
	t.Chdir(f.dir)

	cands, err := f.ch.Prepare(context.Background(), f.request("."))
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if cands[0].Name != filepath.Base(f.dir) {
		t.Errorf("name = %q, want the directory's own name %q", cands[0].Name, filepath.Base(f.dir))
	}
}

func TestLocalRefusesPathsThatCannotMeanASkillOfYourOwn(t *testing.T) {
	f := newLocalFixture(t, nil)

	inStore := filepath.Join(f.store.Root, "rev", "github.com", "o", "r", "abc")
	if err := os.MkdirAll(inStore, 0o755); err != nil {
		t.Fatal(err)
	}
	inAgent := filepath.Join(f.agent.Dir, "already-there")
	if err := os.MkdirAll(inAgent, 0o755); err != nil {
		t.Fatal(err)
	}
	notADir := filepath.Join(f.dir, "SKILL.md")

	for _, tc := range []struct {
		name string
		path string
		want string
	}{
		{name: "missing", path: filepath.Join(f.dir, "nope"), want: "no such file"},
		{name: "not a directory", path: notADir, want: "not a directory"},
		{name: "inside the store", path: inStore, want: "install it from its source"},
		{name: "inside an agent's skills dir", path: inAgent, want: "already inside claude's skills directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.ch.Prepare(context.Background(), f.request(tc.path))
			if err == nil {
				t.Fatalf("Prepare accepted %s", tc.path)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestLocalRejectsRevisionFlags(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  Request
		want string
	}{
		{name: "ref", req: Request{Ref: "main"}, want: "--ref"},
		{name: "pin", req: Request{Pin: true}, want: "--pin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := rejectRevisionFlags(tc.req)
			if err == nil {
				t.Fatalf("%s was accepted for a local skill", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to name %s", err, tc.want)
			}
		})
	}
}

// A local directory may be a checkout holding several skills, and it goes
// through the same selection git uses rather than a second one that drifts.
func TestLocalSelectsSkillsInACheckoutTheWayGitDoes(t *testing.T) {
	f := newLocalFixture(t, map[string]string{
		"skills/alpha/SKILL.md": "---\nname: alpha\n---\n",
		"skills/beta/SKILL.md":  "---\nname: beta\n---\n",
	})

	if _, err := f.ch.Prepare(context.Background(), f.request(f.dir)); err == nil {
		t.Error("a directory holding two skills must not be installed by guessing")
	}

	req := f.request(f.dir)
	req.Skills = []string{"beta"}
	cands, err := f.ch.Prepare(context.Background(), req)
	if err != nil {
		t.Fatalf("Prepare with --skill: %v", err)
	}
	if len(cands) != 1 || cands[0].Name != "beta" {
		t.Fatalf("candidates = %+v, want just beta", cands)
	}
	if cands[0].Subpath != "skills/beta" {
		t.Errorf("subpath = %q, want it relative to the directory that was linked", cands[0].Subpath)
	}

	req.Skills, req.All = nil, true
	if cands, err = f.ch.Prepare(context.Background(), req); err != nil || len(cands) != 2 {
		t.Errorf("--all gave %d candidates (%v), want both", len(cands), err)
	}
}

func TestLocalUpdateHasNothingToDo(t *testing.T) {
	f := newLocalFixture(t, nil)

	verdicts, p, err := f.ch.Update(context.Background(), []*state.Receipt{
		{Name: "my-skill", Channel: "local"},
	}, UpdateOptions{})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if verdicts[0].Status != StatusSkipped {
		t.Errorf("status = %q, want %q: a local skill is already whatever its directory says", verdicts[0].Status, StatusSkipped)
	}
	if !p.IsEmpty() {
		t.Error("nothing may be planned for a local skill")
	}
}

func TestLocalRemoveUnlinksAndForgets(t *testing.T) {
	f := newLocalFixture(t, nil)

	p, err := f.ch.Remove(state.Receipt{
		Name:  "my-skill",
		Links: []state.Link{{Target: "claude", Path: "/agents/claude/skills/my-skill"}},
	}, nil)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(p.Ops) != 2 {
		t.Fatalf("plan = %v, want an unlink and a forget", p.Describe())
	}
	if _, ok := p.Ops[0].(plan.Unlink); !ok {
		t.Errorf("op 0 is %T, want plan.Unlink — never anything that touches the source", p.Ops[0])
	}
	// The whole plan is symlink removal. Nothing here can reach the user's
	// directory, which is the guarantee this channel makes.
	for _, op := range p.Ops {
		if _, ok := op.(plan.Exec); ok {
			t.Error("removing a local skill must never shell out")
		}
	}
}
