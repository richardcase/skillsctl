package plan

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/richardcase/skillsctl/internal/state"
)

func newExecutor() *Executor {
	return &Executor{
		DB:  &state.DB{Version: state.SchemaVersion, Receipts: map[string]*state.Receipt{}},
		Out: io.Discard,
	}
}

func TestApplyLinksAndRecords(t *testing.T) {
	root := t.TempDir()
	rev := filepath.Join(root, "rev")
	if err := os.MkdirAll(rev, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "agent", "skills", "foo")

	e := newExecutor()
	var p Plan
	p.Add(
		Link{Target: "claude", LinkPath: link, RevPath: rev},
		Record{Receipt: state.Receipt{Name: "foo", Resolved: "abc"}},
	)

	if err := e.Apply(context.Background(), p); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Readlink(link); err != nil {
		t.Errorf("symlink was not created: %v", err)
	}
	if _, ok := e.DB.Receipts["foo"]; !ok {
		t.Error("receipt was not recorded")
	}
}

func TestApplyRollsBackLinksOnFailure(t *testing.T) {
	root := t.TempDir()
	rev := filepath.Join(root, "rev")
	if err := os.MkdirAll(rev, 0o755); err != nil {
		t.Fatal(err)
	}

	good := filepath.Join(root, "a", "skills", "foo")
	// A real directory at the second link path makes that Link op fail.
	bad := filepath.Join(root, "b", "skills", "foo")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}

	e := newExecutor()
	var p Plan
	p.Add(
		Link{Target: "claude", LinkPath: good, RevPath: rev},
		Link{Target: "codex", LinkPath: bad, RevPath: rev},
		Record{Receipt: state.Receipt{Name: "foo"}},
	)

	if err := e.Apply(context.Background(), p); err == nil {
		t.Fatal("Apply succeeded despite a failing Link op")
	}
	if _, err := os.Lstat(good); !os.IsNotExist(err) {
		t.Error("the first symlink must be rolled back when a later op fails")
	}
	if _, ok := e.DB.Receipts["foo"]; ok {
		t.Error("no receipt should be recorded when apply fails")
	}
}

func TestApplyForgetAndUnlink(t *testing.T) {
	root := t.TempDir()
	rev := filepath.Join(root, "rev")
	if err := os.MkdirAll(rev, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "skills", "foo")

	e := newExecutor()
	e.DB.Receipts["foo"] = &state.Receipt{Name: "foo"}

	var setup Plan
	setup.Add(Link{Target: "claude", LinkPath: link, RevPath: rev})
	if err := e.Apply(context.Background(), setup); err != nil {
		t.Fatal(err)
	}

	var p Plan
	p.Add(Unlink{Target: "claude", LinkPath: link}, Forget{Name: "foo"})
	if err := e.Apply(context.Background(), p); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Error("symlink was not removed")
	}
	if _, ok := e.DB.Receipts["foo"]; ok {
		t.Error("receipt was not forgotten")
	}
}

func TestApplyExecUsesInjectedRunner(t *testing.T) {
	var got []string
	e := newExecutor()
	e.Run = func(_ context.Context, argv []string) error {
		got = argv
		return nil
	}

	var p Plan
	p.Add(Exec{Argv: []string{"claude", "plugin", "install", "x@y"}})
	if err := e.Apply(context.Background(), p); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(got) != 4 || got[0] != "claude" {
		t.Errorf("runner received %v, want the exec argv", got)
	}
}

func TestApplyRelinksToTheNewRevision(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, "old")
	fresh := filepath.Join(root, "new")
	for _, d := range []string{old, fresh} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(root, "skills", "foo")

	e := newExecutor()
	var setup Plan
	setup.Add(Link{Target: "claude", LinkPath: link, RevPath: old})
	if err := e.Apply(context.Background(), setup); err != nil {
		t.Fatal(err)
	}

	var p Plan
	p.Add(Relink{Target: "claude", LinkPath: link, RevPath: fresh})
	if err := e.Apply(context.Background(), p); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got, err := os.Readlink(link); err != nil || got != fresh {
		t.Errorf("Readlink = %q, %v; want %q", got, err, fresh)
	}
}

// A failed update must leave the old revision linked: that is the whole
// rollback story for immutable revision directories.
func TestApplyRollsBackARelinkToTheOldRevision(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, "old")
	fresh := filepath.Join(root, "new")
	for _, d := range []string{old, fresh} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	link := filepath.Join(root, "a", "skills", "foo")
	e := newExecutor()
	var setup Plan
	setup.Add(Link{Target: "claude", LinkPath: link, RevPath: old})
	if err := e.Apply(context.Background(), setup); err != nil {
		t.Fatal(err)
	}

	// A real directory at the second link path makes that op fail.
	blocked := filepath.Join(root, "b", "skills", "foo")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatal(err)
	}

	var p Plan
	p.Add(
		Relink{Target: "claude", LinkPath: link, RevPath: fresh},
		Relink{Target: "codex", LinkPath: blocked, RevPath: fresh},
		Record{Receipt: state.Receipt{Name: "foo", Resolved: "new"}},
	)

	if err := e.Apply(context.Background(), p); err == nil {
		t.Fatal("Apply succeeded despite a failing Relink op")
	}
	if got, err := os.Readlink(link); err != nil || got != old {
		t.Errorf("Readlink = %q, %v; want the old revision %q", got, err, old)
	}
	if _, ok := e.DB.Receipts["foo"]; ok {
		t.Error("no receipt should be recorded when apply fails")
	}
}

func TestApplyRollbackKeepsPreExistingLinks(t *testing.T) {
	root := t.TempDir()
	rev := filepath.Join(root, "rev")
	if err := os.MkdirAll(rev, 0o755); err != nil {
		t.Fatal(err)
	}

	// Already exists and already points at rev: target.Link treats this as a
	// successful no-op, so the executor must not record it as its own work.
	existing := filepath.Join(root, "a", "skills", "foo")
	if err := os.MkdirAll(filepath.Dir(existing), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(rev, existing); err != nil {
		t.Fatal(err)
	}

	// A real directory here makes the second Link op fail.
	blocked := filepath.Join(root, "b", "skills", "foo")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatal(err)
	}

	e := newExecutor()
	var p Plan
	p.Add(
		Link{Target: "claude", LinkPath: existing, RevPath: rev},
		Link{Target: "codex", LinkPath: blocked, RevPath: rev},
	)

	if err := e.Apply(context.Background(), p); err == nil {
		t.Fatal("Apply succeeded despite a failing Link op")
	}
	if _, err := os.Lstat(existing); err != nil {
		t.Errorf("rollback removed a symlink that existed before this apply: %v", err)
	}
}

// A Note is the plan admitting it cannot name something yet, so applying one
// must be a no-op rather than the executor's "unknown op" error.
func TestApplyNoteIsANoOp(t *testing.T) {
	db := &state.DB{Receipts: map[string]*state.Receipt{}}
	ex := &Executor{DB: db, Out: io.Discard}

	var p Plan
	p.Add(Note{Text: "something that happens later"})

	if err := ex.Apply(context.Background(), p); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(db.Receipts) != 0 {
		t.Errorf("a note changed the receipts: %v", db.Receipts)
	}
}
