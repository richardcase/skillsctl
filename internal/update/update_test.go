package update

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/channel"
	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/store"
	"github.com/richardcase/skillsctl/internal/testrepo"
)

const skillMD = "---\nname: demo\ndescription: A demo\n---\n\nBody.\n"

// countingGit resolves through the real git binary but counts the calls, so
// the per-repository caching can be asserted without a second fixture.
type countingGit struct {
	gitx.Git
	calls int
}

func (c *countingGit) Resolve(ctx context.Context, repoURL, ref string) (string, error) {
	c.calls++
	return c.Git.Resolve(ctx, repoURL, ref)
}

// fixture builds a two-commit repository and the receipt an install of its
// first commit would have written, with the first revision already extracted.
type fixture struct {
	store   *store.Store
	git     *countingGit
	url     string
	dir     string
	first   string
	second  string
	receipt *state.Receipt
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	url, first := testrepo.New(t, map[string]string{"SKILL.md": skillMD})
	second := testrepo.Commit(t, testrepo.Dir(url), map[string]string{"SKILL.md": skillMD + "\nMore.\n"})

	src, err := source.Parse(url)
	if err != nil {
		t.Fatalf("Parse(%q): %v", url, err)
	}

	f := &fixture{
		store:  store.New(t.TempDir()),
		git:    &countingGit{Git: gitx.New()},
		url:    url,
		dir:    testrepo.Dir(url),
		first:  first,
		second: second,
	}

	rev, err := f.store.Ensure(context.Background(), f.git, src.Slug(), src.RepoURL, first)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	hash, err := store.HashDir(rev)
	if err != nil {
		t.Fatalf("HashDir: %v", err)
	}

	f.receipt = &state.Receipt{
		Name:        "demo",
		Channel:     "git",
		Source:      src.RepoURL,
		Slug:        src.Slug(),
		Ref:         "main",
		Resolved:    first,
		RevPath:     rev,
		ContentHash: hash,
		Links: []state.Link{
			{Target: "claude", Path: filepath.Join(t.TempDir(), "claude", "demo")},
			{Target: "codex", Path: filepath.Join(t.TempDir(), "codex", "demo")},
		},
	}
	return f
}

func (f *fixture) plan(t *testing.T, o Options, receipts ...*state.Receipt) ([]Entry, plan.Plan) {
	t.Helper()
	if len(receipts) == 0 {
		receipts = []*state.Receipt{f.receipt}
	}
	reg := channel.Registry{Git: channel.NewGit(f.store, f.git)}
	entries, p, err := Plan(context.Background(), reg, receipts, o)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	return entries, p
}

func TestPlanRelinksEveryLinkOfAMovedRef(t *testing.T) {
	f := newFixture(t)

	entries, p := f.plan(t, Options{})

	if len(entries) != 1 || entries[0].Status != StatusUpdated {
		t.Fatalf("entries = %+v, want one updated", entries)
	}
	if entries[0].Latest != f.second {
		t.Errorf("Latest = %q, want the second commit %q", entries[0].Latest, f.second)
	}

	// Two links plus one receipt.
	if len(p.Ops) != 3 {
		t.Fatalf("plan has %d ops, want 3:\n%s", len(p.Ops), strings.Join(p.Describe(), "\n"))
	}
	for i, want := range []string{"claude", "codex"} {
		op, ok := p.Ops[i].(plan.Relink)
		if !ok {
			t.Fatalf("op %d is %T, want plan.Relink", i, p.Ops[i])
		}
		if op.Target != want {
			t.Errorf("op %d targets %q, want %q", i, op.Target, want)
		}
		if !strings.HasSuffix(op.RevPath, f.second) {
			t.Errorf("op %d re-points at %q, want the second revision", i, op.RevPath)
		}
	}

	rec, ok := p.Ops[2].(plan.Record)
	if !ok {
		t.Fatalf("last op is %T, want plan.Record", p.Ops[2])
	}
	switch {
	case rec.Receipt.Resolved != f.second:
		t.Errorf("recorded Resolved = %q, want %q", rec.Receipt.Resolved, f.second)
	case rec.Receipt.ContentHash == f.receipt.ContentHash:
		t.Error("recorded ContentHash was not re-computed from the new revision")
	case !rec.Receipt.UpdatedAt.After(f.receipt.UpdatedAt):
		t.Error("recorded UpdatedAt did not move")
	case rec.Receipt.Ref != "main":
		t.Errorf("recorded Ref = %q, want the tracked ref to survive", rec.Receipt.Ref)
	case len(rec.Receipt.Links) != 2:
		t.Errorf("recorded %d links, want the install's two to survive", len(rec.Receipt.Links))
	}
}

func TestPlanLeavesACurrentSkillAlone(t *testing.T) {
	f := newFixture(t)
	f.receipt.Resolved = f.second

	entries, p := f.plan(t, Options{})

	if entries[0].Status != StatusCurrent {
		t.Errorf("status = %q, want %q", entries[0].Status, StatusCurrent)
	}
	if !p.IsEmpty() {
		t.Errorf("plan should be empty, got:\n%s", strings.Join(p.Describe(), "\n"))
	}
}

func TestPlanSkipsADirtySkillUnlessForced(t *testing.T) {
	f := newFixture(t)
	if err := os.WriteFile(filepath.Join(f.receipt.RevPath, "SKILL.md"), []byte(skillMD+"\nEdited.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, p := f.plan(t, Options{})
	if entries[0].Status != StatusDirty {
		t.Errorf("status = %q, want %q", entries[0].Status, StatusDirty)
	}
	if !p.IsEmpty() {
		t.Errorf("a dirty skill must plan nothing, got:\n%s", strings.Join(p.Describe(), "\n"))
	}

	entries, p = f.plan(t, Options{Force: true})
	if entries[0].Status != StatusUpdated {
		t.Errorf("with --force: status = %q, want %q", entries[0].Status, StatusUpdated)
	}
	if p.IsEmpty() {
		t.Error("with --force: the update must be planned")
	}
}

func TestPlanUpdatesAReceiptWithNoRecordedHash(t *testing.T) {
	f := newFixture(t)
	f.receipt.ContentHash = ""

	entries, _ := f.plan(t, Options{})

	if entries[0].Status != StatusUpdated {
		t.Errorf("status = %q, want %q: a receipt with no hash cannot be dirty", entries[0].Status, StatusUpdated)
	}
}

func TestPlanUpdatesAReceiptWhoseRevisionIsGone(t *testing.T) {
	f := newFixture(t)
	if err := os.RemoveAll(f.receipt.RevPath); err != nil {
		t.Fatal(err)
	}

	entries, _ := f.plan(t, Options{})

	if entries[0].Status != StatusUpdated {
		t.Fatalf("status = %q, want %q: a missing revision has no edits to lose", entries[0].Status, StatusUpdated)
	}
	if entries[0].Note == "" {
		t.Error("a missing revision directory is worth saying out loud")
	}
}

func TestPlanSkipsAPinUnlessItIsNamed(t *testing.T) {
	f := newFixture(t)
	f.receipt.Pinned = true
	f.receipt.Ref = "" // install records no ref for a pinned skill

	entries, p := f.plan(t, Options{})
	if entries[0].Status != StatusPinned {
		t.Errorf("status = %q, want %q", entries[0].Status, StatusPinned)
	}
	if !p.IsEmpty() {
		t.Error("a pinned skill must plan nothing when it was not named")
	}

	entries, p = f.plan(t, Options{Names: []string{"demo"}})
	if entries[0].Status != StatusUpdated {
		t.Fatalf("named: status = %q, want %q", entries[0].Status, StatusUpdated)
	}
	rec, ok := p.Ops[len(p.Ops)-1].(plan.Record)
	if !ok {
		t.Fatalf("last op is %T, want plan.Record", p.Ops[len(p.Ops)-1])
	}
	if !rec.Receipt.Pinned || rec.Receipt.Ref != "" {
		t.Errorf("recorded receipt = {Pinned:%v Ref:%q}, want it re-pinned at the new sha",
			rec.Receipt.Pinned, rec.Receipt.Ref)
	}
}

func TestPlanResolvesOneRepositoryOnce(t *testing.T) {
	f := newFixture(t)
	second := *f.receipt
	second.Name = "demo-two"

	entries, _ := f.plan(t, Options{}, f.receipt, &second)

	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if f.git.calls != 1 {
		t.Errorf("Resolve was called %d times, want 1 for two skills from one repository", f.git.calls)
	}
}

func TestPlanReportsAnUnreachableRemoteWithoutHidingTheRest(t *testing.T) {
	f := newFixture(t)
	broken := *f.receipt
	broken.Name = "broken"
	broken.Source = "file:///nowhere-at-all"
	broken.Slug = "file/nowhere-at-all"

	entries, p := f.plan(t, Options{}, &broken, f.receipt)

	if entries[0].Status != StatusError {
		t.Errorf("status = %q, want %q", entries[0].Status, StatusError)
	}
	if entries[0].Error == "" {
		t.Error("an error entry must carry its reason")
	}
	if entries[1].Status != StatusUpdated {
		t.Errorf("the reachable skill was not planned: status = %q", entries[1].Status)
	}
	if p.IsEmpty() {
		t.Error("one unreachable remote must not empty the plan")
	}
}

func TestPlanReportsASkillThatIsGoneUpstream(t *testing.T) {
	f := newFixture(t)
	f.receipt.Subpath = "skills/alpha"

	entries, p := f.plan(t, Options{})

	if entries[0].Status != StatusError {
		t.Fatalf("status = %q, want %q", entries[0].Status, StatusError)
	}
	if !strings.Contains(entries[0].Error, "skills/alpha") {
		t.Errorf("error %q should name the subpath that no longer holds a skill", entries[0].Error)
	}
	if !p.IsEmpty() {
		t.Error("nothing may be planned for a skill that is gone upstream")
	}
}

func TestPlanRejectsANameThatIsNotInstalled(t *testing.T) {
	f := newFixture(t)

	reg := channel.Registry{Git: channel.NewGit(f.store, f.git)}
	_, _, err := Plan(context.Background(), reg,
		[]*state.Receipt{f.receipt}, Options{Names: []string{"nope"}})

	if err == nil {
		t.Fatal("Plan accepted a name that is not installed")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("error %q should name what was not found", err)
	}
}

func TestPlanTakesANameOnceHoweverOftenItIsGiven(t *testing.T) {
	f := newFixture(t)

	entries, p := f.plan(t, Options{Names: []string{"demo", "demo"}})

	if len(entries) != 1 {
		t.Errorf("got %d entries, want 1", len(entries))
	}
	if len(p.Ops) != 3 {
		t.Errorf("plan has %d ops, want the same 3 as a single name:\n%s", len(p.Ops), strings.Join(p.Describe(), "\n"))
	}
}

func TestPlanSkipsANonGitChannel(t *testing.T) {
	f := newFixture(t)
	f.receipt.Channel = "local"

	entries, p := f.plan(t, Options{})

	if entries[0].Status != StatusSkipped {
		t.Errorf("status = %q, want %q", entries[0].Status, StatusSkipped)
	}
	if !p.IsEmpty() {
		t.Error("a local skill has no ref to update from")
	}
}
