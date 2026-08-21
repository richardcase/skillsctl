package channel

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/store"
	"github.com/richardcase/skillsctl/internal/testrepo"
)

const rollbackSkillMD = "---\nname: demo\ndescription: A demo\n---\n\nBody.\n"

// gitRollbackFixture installs the first commit of a two-commit repository,
// then updates it to the second, so PreviousResolved is populated the same
// way a real update would leave it.
func gitRollbackFixture(t *testing.T) (c *Git, r *state.Receipt, first, second string) {
	t.Helper()

	url, first := testrepo.New(t, map[string]string{"SKILL.md": rollbackSkillMD})
	second = testrepo.Commit(t, testrepo.Dir(url), map[string]string{"SKILL.md": rollbackSkillMD + "\nMore.\n"})

	src, err := source.Parse(url)
	if err != nil {
		t.Fatalf("Parse(%q): %v", url, err)
	}

	st := store.New(t.TempDir())
	g := gitx.New()
	c = NewGit(st, g)

	rev, err := st.Ensure(context.Background(), g, src.Slug(), src.RepoURL, first)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	hash, err := store.HashDir(rev)
	if err != nil {
		t.Fatalf("HashDir: %v", err)
	}

	r = &state.Receipt{
		Name:        "demo",
		Channel:     "git",
		Source:      src.RepoURL,
		Slug:        src.Slug(),
		Ref:         "main",
		Resolved:    first,
		RevPath:     rev,
		ContentHash: hash,
		Links:       []state.Link{{Target: "claude", Path: filepath.Join(t.TempDir(), "claude", "demo")}},
	}

	// Move to the second commit exactly the way Update would, so
	// PreviousResolved is populated the same way a real update leaves it.
	// The fixture only needs the receipt's final shape, not a live symlink,
	// so it reads the plan's own Record op by hand rather than pulling in
	// plan.Executor.
	verdicts, p, err := c.Update(context.Background(), []*state.Receipt{r}, UpdateOptions{})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(verdicts) != 1 || verdicts[0].Status != StatusUpdated {
		t.Fatalf("fixture setup: Update verdicts = %+v, want one StatusUpdated", verdicts)
	}
	for _, op := range p.Ops {
		if rec, ok := op.(plan.Record); ok {
			*r = rec.Receipt
		}
	}
	return c, r, first, second
}

func TestGitRollbackSwapsBackToThePreviousRevision(t *testing.T) {
	c, r, first, second := gitRollbackFixture(t)
	if r.Resolved != second {
		t.Fatalf("fixture: Resolved = %q, want the second commit %q", r.Resolved, second)
	}
	if r.PreviousResolved != first {
		t.Fatalf("fixture: PreviousResolved = %q, want the first commit %q", r.PreviousResolved, first)
	}

	p, v, err := c.Rollback(context.Background(), *r)
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if v.Status != StatusUpdated {
		t.Errorf("Status = %q, want %q", v.Status, StatusUpdated)
	}
	if v.Latest != first {
		t.Errorf("Latest = %q, want the sha it rolled back to (%q)", v.Latest, first)
	}

	var rec plan.Record
	for _, op := range p.Ops {
		if r, ok := op.(plan.Record); ok {
			rec = r
		}
	}
	if rec.Receipt.Resolved != first {
		t.Errorf("recorded Resolved = %q, want %q", rec.Receipt.Resolved, first)
	}
	if rec.Receipt.PreviousResolved != second {
		t.Errorf("recorded PreviousResolved = %q, want the toggle to remember %q", rec.Receipt.PreviousResolved, second)
	}
	if !strings.HasSuffix(rec.Receipt.RevPath, first) {
		t.Errorf("recorded RevPath = %q, want it to end in %q", rec.Receipt.RevPath, first)
	}

	var relinked bool
	for _, op := range p.Ops {
		if rl, ok := op.(plan.Relink); ok {
			relinked = true
			if !strings.HasSuffix(rl.RevPath, first) {
				t.Errorf("relink RevPath = %q, want it to end in %q", rl.RevPath, first)
			}
		}
	}
	if !relinked {
		t.Error("rollback planned no relink")
	}
}

func TestGitRollbackTwiceTogglesBack(t *testing.T) {
	c, r, first, second := gitRollbackFixture(t)

	p1, _, err := c.Rollback(context.Background(), *r)
	if err != nil {
		t.Fatalf("first Rollback: %v", err)
	}
	for _, op := range p1.Ops {
		if rec, ok := op.(plan.Record); ok {
			*r = rec.Receipt
		}
	}
	if r.Resolved != first {
		t.Fatalf("after first rollback: Resolved = %q, want %q", r.Resolved, first)
	}

	p2, v2, err := c.Rollback(context.Background(), *r)
	if err != nil {
		t.Fatalf("second Rollback: %v", err)
	}
	if v2.Latest != second {
		t.Errorf("second rollback Latest = %q, want it to swap back to %q", v2.Latest, second)
	}
	var rec2 plan.Record
	for _, op := range p2.Ops {
		if r, ok := op.(plan.Record); ok {
			rec2 = r
		}
	}
	if rec2.Receipt.Resolved != second {
		t.Errorf("after second rollback: recorded Resolved = %q, want %q", rec2.Receipt.Resolved, second)
	}
}

func TestGitRollbackRefusesWithNothingToRollBackTo(t *testing.T) {
	c, r, _, _ := gitRollbackFixture(t)
	r.PreviousResolved = ""
	r.PreviousRevPath = ""
	r.PreviousContentHash = ""

	_, _, err := c.Rollback(context.Background(), *r)
	if !errors.Is(err, ErrNothingToRollBackTo) {
		t.Errorf("Rollback error = %v, want ErrNothingToRollBackTo", err)
	}
}
