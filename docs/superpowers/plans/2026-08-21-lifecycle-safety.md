# Lifecycle Safety Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `skillsctl rollback <name>`, `skillsctl diff <name>`, and declared agent-compatibility in `SKILL.md` frontmatter.

**Architecture:** Three independent phases sharing one foundation. Phase 1 adds a `Previous*` triple to `state.Receipt` and a `Channel.Rollback` method (git and OCI implement it by reusing each channel's existing `relink` helper with the roles of "current" and "previous" swapped). Phase 2 adds a read-only `internal/diff` package, modeled on `internal/outdated`, plus two new `gitx.Git` methods (`Diff` for a git mirror, `DiffDirs` for two arbitrary directories — which is what makes an OCI revision diffable with no mirror to compare shas in). Phase 3 adds an `Agents` field to `discover.Meta` and a warning check in `Git.Prepare`/`OCI.Prepare`, printed through the warnings mechanism `install.go` already has.

**Tech Stack:** Go 1.25, cobra, the `git` binary via `internal/gitx`, `go-containerregistry` via `internal/ocix`. Standard library only in tests: table-driven, `t.TempDir()`, `internal/testrepo` fixtures, no mocking library.

**Spec:** `docs/superpowers/specs/2026-08-21-lifecycle-safety-design.md`

## Global Constraints

- Conventional Commits required for every commit (`feat:`, `fix:`, `test:`, `docs:`) — see `AGENTS.md`.
- No `t.Parallel()` anywhere (`t.Setenv` forbids it).
- Tests are in-package (white-box), standard library only, no mocks/testify/golden files.
- `make test && make lint && make tidy-check` must pass before this is done; update `README.md` in the same PR as any user-visible change.
- Every new exported identifier needs a doc comment (revive enforces it).
- Deliberately ignored errors are explicit `_ =`.

---

## Phase 1 — Receipt schema and `rollback`

### Task 1: Add the `Previous*` triple to `state.Receipt`

**Files:**
- Modify: `internal/state/state.go:27-41`
- Test: `internal/state/state_test.go`

**Interfaces:**
- Produces: three new `state.Receipt` fields — `PreviousResolved string`, `PreviousRevPath string`, `PreviousContentHash string` — that every later task in Phase 1 reads and writes.

- [ ] **Step 1: Write the failing test**

Add to `internal/state/state_test.go`, extending `TestCommitThenReopenRoundTrips`'s fixture with the new fields:

```go
func TestCommitThenReopenRoundTripsPreviousRevision(t *testing.T) {
	p := statePath(t)
	now := time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC)

	h, err := Open(p)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	h.DB.Receipts["avoid-ai-writing"] = &Receipt{
		Name:                "avoid-ai-writing",
		Channel:             "git",
		Source:              "https://github.com/conorbronsdon/avoid-ai-writing.git",
		Slug:                "github.com/conorbronsdon/avoid-ai-writing",
		Ref:                 "main",
		Resolved:            "b2c3d4e",
		PreviousResolved:    "a1b2c3d",
		PreviousRevPath:     "/store/rev/x/a1b2c3d",
		PreviousContentHash: "cafef00d",
		RevPath:             "/store/rev/x/b2c3d4e",
		ContentHash:         "deadbeef",
		InstalledAt:         now,
		UpdatedAt:           now,
	}
	if err := h.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	h2, err := Open(p)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = h2.Close() }()

	got, ok := h2.DB.Receipts["avoid-ai-writing"]
	if !ok {
		t.Fatal("receipt did not survive the round trip")
	}
	if got.PreviousResolved != "a1b2c3d" {
		t.Errorf("PreviousResolved = %q, want a1b2c3d", got.PreviousResolved)
	}
	if got.PreviousRevPath != "/store/rev/x/a1b2c3d" {
		t.Errorf("PreviousRevPath = %q, want /store/rev/x/a1b2c3d", got.PreviousRevPath)
	}
	if got.PreviousContentHash != "cafef00d" {
		t.Errorf("PreviousContentHash = %q, want cafef00d", got.PreviousContentHash)
	}
}

func TestReceiptWithNoPreviousRevisionOmitsTheFieldsFromJSON(t *testing.T) {
	blob, err := json.Marshal(Receipt{Name: "fresh", Resolved: "abc", RevPath: "/x"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(blob), "previousResolved") {
		t.Errorf("a receipt with no previous revision should omit it from JSON, got: %s", blob)
	}
}
```

Add `"strings"` to the test file's imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd internal/state && go test ./... -run 'TestCommitThenReopenRoundTripsPreviousRevision|TestReceiptWithNoPreviousRevision' -v`
Expected: FAIL — `Receipt` has no field `PreviousResolved` (compile error).

- [ ] **Step 3: Add the fields**

In `internal/state/state.go`, extend the `Receipt` struct:

```go
// Receipt records how a skill was installed.
type Receipt struct {
	Name        string    `json:"name"`
	Channel     string    `json:"channel"`
	Source      string    `json:"source"`
	Slug        string    `json:"slug,omitempty"`
	Subpath     string    `json:"subpath,omitempty"`
	Ref         string    `json:"ref,omitempty"`
	Resolved    string    `json:"resolved"`
	Pinned      bool      `json:"pinned,omitempty"`
	RevPath     string    `json:"revPath"`
	ContentHash string    `json:"contentHash,omitempty"`
	// PreviousResolved, PreviousRevPath and PreviousContentHash are what
	// Resolved, RevPath and ContentHash held before the last relink — the
	// git and OCI channels populate them, so rollback has something to
	// swap back to. Empty until a skill has been updated at least once.
	PreviousResolved    string    `json:"previousResolved,omitempty"`
	PreviousRevPath     string    `json:"previousRevPath,omitempty"`
	PreviousContentHash string    `json:"previousContentHash,omitempty"`
	Links               []Link    `json:"links"`
	InstalledAt         time.Time `json:"installedAt"`
	UpdatedAt           time.Time `json:"updatedAt"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd internal/state && go test ./... -v`
Expected: PASS, all tests including the two new ones.

- [ ] **Step 5: Commit**

```bash
git add internal/state/state.go internal/state/state_test.go
git commit -m "feat(state): record a receipt's previous revision"
```

---

### Task 2: Populate `Previous*` on a git relink

**Files:**
- Modify: `internal/channel/git.go:321-362` (the `relink` method)
- Test: `internal/update/update_test.go` (extend `TestPlanRelinksEveryLinkOfAMovedRef`)

**Interfaces:**
- Consumes: `state.Receipt.PreviousResolved/PreviousRevPath/PreviousContentHash` (Task 1).
- Produces: every receipt `Git.relink` builds now carries the pre-update `Resolved`/`RevPath`/`ContentHash` in the `Previous*` fields — which `Git.Rollback` (Task 5) and `diff --against previous` (Phase 2) both read.

- [ ] **Step 1: Write the failing test**

Extend `TestPlanRelinksEveryLinkOfAMovedRef` in `internal/update/update_test.go` — add these assertions right after the existing `switch rec.Receipt...` block (still inside the same `switch`, before its closing brace):

```go
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
	case rec.Receipt.PreviousResolved != f.first:
		t.Errorf("recorded PreviousResolved = %q, want the sha it moved from (%q)", rec.Receipt.PreviousResolved, f.first)
	case rec.Receipt.PreviousRevPath != f.receipt.RevPath:
		t.Errorf("recorded PreviousRevPath = %q, want the revision path it moved from (%q)", rec.Receipt.PreviousRevPath, f.receipt.RevPath)
	case rec.Receipt.PreviousContentHash != f.receipt.ContentHash:
		t.Errorf("recorded PreviousContentHash = %q, want the hash it moved from (%q)", rec.Receipt.PreviousContentHash, f.receipt.ContentHash)
	}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd internal/update && go test ./... -run TestPlanRelinksEveryLinkOfAMovedRef -v`
Expected: FAIL — `recorded PreviousResolved = ""`.

- [ ] **Step 3: Populate the fields in `relink`**

In `internal/channel/git.go`, change the tail of `relink`:

```go
	// Everything the user chose at install time survives an update: the name
	// they installed it under, the agents they linked it into, the ref it
	// tracks, and the pin. Only what the new revision decides changes.
	receipt := *r
	receipt.PreviousResolved = r.Resolved
	receipt.PreviousRevPath = r.RevPath
	receipt.PreviousContentHash = r.ContentHash
	receipt.Resolved = sha
	receipt.RevPath = revPath
	receipt.ContentHash = hash
	receipt.UpdatedAt = now
	return ops, receipt, nil
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd internal/update && go test ./... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/channel/git.go internal/update/update_test.go
git commit -m "feat(channel): record the previous revision on a git relink"
```

---

### Task 3: Populate `Previous*` on an OCI relink

**Files:**
- Modify: `internal/channel/oci.go:299-334` (the `relink` method)
- Test: `internal/channel/oci_test.go` (extend `TestOCIUpdateRelinksWhenTheDigestMoved`)

**Interfaces:**
- Consumes: same as Task 2, for the OCI channel.
- Produces: same guarantee as Task 2, for OCI receipts.

- [ ] **Step 1: Write the failing test**

Read the rest of `TestOCIUpdateRelinksWhenTheDigestMoved` in `internal/channel/oci_test.go` first (it continues past line 120) to find where it asserts on the recorded receipt, then add:

```go
	if rec.Receipt.PreviousResolved != "sha256:aaa" {
		t.Errorf("recorded PreviousResolved = %q, want the digest it moved from", rec.Receipt.PreviousResolved)
	}
```

immediately after whatever existing assertion checks `rec.Receipt.Resolved` for the new digest (use the same `rec` variable the existing test already extracted from the plan).

- [ ] **Step 2: Run test to verify it fails**

Run: `cd internal/channel && go test ./... -run TestOCIUpdateRelinksWhenTheDigestMoved -v`
Expected: FAIL — `recorded PreviousResolved = ""`.

- [ ] **Step 3: Populate the fields in `relink`**

In `internal/channel/oci.go`, change the tail of `relink`:

```go
	receipt := *r
	receipt.PreviousResolved = r.Resolved
	receipt.PreviousRevPath = r.RevPath
	receipt.PreviousContentHash = r.ContentHash
	receipt.Resolved = digest
	receipt.RevPath = revPath
	receipt.ContentHash = hash
	receipt.UpdatedAt = now
	return ops, receipt, nil
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd internal/channel && go test ./... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/channel/oci.go internal/channel/oci_test.go
git commit -m "feat(channel): record the previous revision on an oci relink"
```

---

### Task 4: Add `Channel.Rollback`, with a refusing default

**Files:**
- Modify: `internal/channel/channel.go` (interface + sentinel error)
- Modify: `internal/channel/linked.go` (default implementation)
- Modify: `internal/channel/plugin.go` (its own refusal)
- Test: `internal/channel/linked_test.go`, `internal/channel/plugin_test.go`

**Interfaces:**
- Produces: `Channel.Rollback(ctx context.Context, r state.Receipt) (plan.Plan, Verdict, error)` on the interface; `channel.ErrRollbackUnsupported` and `channel.ErrNothingToRollBackTo` sentinel errors. Every channel now compiles against the wider interface — `Local` inherits `linked.Rollback`, `Plugin` defines its own, and `Git`/`OCI` will override it in Tasks 5 and 6.

- [ ] **Step 1: Write the failing test**

Add to `internal/channel/linked_test.go`:

```go
func TestLinkedRollbackRefuses(t *testing.T) {
	c := &Local{}
	_, _, err := c.Rollback(context.Background(), state.Receipt{Name: "demo"})
	if !errors.Is(err, ErrRollbackUnsupported) {
		t.Errorf("Rollback error = %v, want ErrRollbackUnsupported", err)
	}
}
```

Add `"errors"` and `"github.com/richardcase/skillsctl/internal/state"` to its imports if not already present (check the file first — it already imports `state` for other tests in the same file).

Add to `internal/channel/plugin_test.go`:

```go
func TestPluginRollbackRefuses(t *testing.T) {
	c := NewPlugin(&fakePlugins{}, target.Config{})
	_, _, err := c.Rollback(context.Background(), state.Receipt{Name: "demo"})
	if !errors.Is(err, ErrRollbackUnsupported) {
		t.Errorf("Rollback error = %v, want ErrRollbackUnsupported", err)
	}
}
```

Check `plugin_test.go`'s existing imports and top-of-file fakes first — it already defines `fakePlugins` and imports `target` and `state` for its other tests, so only add what is missing (likely nothing beyond confirming `errors` is imported).

- [ ] **Step 2: Run test to verify it fails**

Run: `cd internal/channel && go test ./... -run 'TestLinkedRollbackRefuses|TestPluginRollbackRefuses' -v`
Expected: FAIL — compile error, `Rollback` undefined on `*Local` and `*Plugin`.

- [ ] **Step 3: Add the interface method, sentinels, and default**

In `internal/channel/channel.go`, add near `ErrUnsupported`:

```go
// ErrRollbackUnsupported reports a channel with no revision history to swap
// back to: its files are the user's own, or the agent's.
var ErrRollbackUnsupported = errors.New("rollback is not supported for this channel")

// ErrNothingToRollBackTo reports a receipt that has never been updated, so
// its Previous* fields carry nothing to swap back to.
var ErrNothingToRollBackTo = errors.New("nothing to roll back to")
```

Add to the `Channel` interface, after `Update`:

```go
	// Rollback swaps a receipt back onto the revision Previous* recorded,
	// undoing its last update. A channel with no revision history refuses
	// with ErrRollbackUnsupported; a receipt that has never been updated
	// refuses with ErrNothingToRollBackTo.
	Rollback(ctx context.Context, r state.Receipt) (plan.Plan, Verdict, error)
```

In `internal/channel/linked.go`, add:

```go
// Rollback refuses: this channel's files are the user's own, with no
// revision history skillsctl can swap back to. Git and OCI, which embed
// linked too, override this with the real thing.
func (linked) Rollback(context.Context, state.Receipt) (plan.Plan, Verdict, error) {
	return plan.Plan{}, Verdict{}, ErrRollbackUnsupported
}
```

Add `"context"` to `linked.go`'s imports.

In `internal/channel/plugin.go`, add:

```go
// Rollback refuses: an agent-owned plugin has no revision history skillsctl
// can swap back to — claude decides which version is installed.
func (c *Plugin) Rollback(context.Context, state.Receipt) (plan.Plan, Verdict, error) {
	return plan.Plan{}, Verdict{}, ErrRollbackUnsupported
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd internal/channel && go test ./... -v` (the whole package, since widening the interface can affect other files that construct a `Channel` value)
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/channel/channel.go internal/channel/linked.go internal/channel/plugin.go internal/channel/linked_test.go internal/channel/plugin_test.go
git commit -m "feat(channel): add Rollback to the Channel interface"
```

---

### Task 5: Implement `Git.Rollback`

**Files:**
- Modify: `internal/channel/git.go`
- Test: `internal/channel/git_rollback_test.go` (new file)

**Interfaces:**
- Consumes: `c.relink` (existing, unexported, same file), `ErrNothingToRollBackTo` (Task 4).
- Produces: `(*Git).Rollback(ctx, r) (plan.Plan, Verdict, error)` — overrides the embedded `linked.Rollback`.

- [ ] **Step 1: Write the failing test**

Create `internal/channel/git_rollback_test.go`:

```go
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
```

Now the actual tests:

```go
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
```

Add `"errors"` to the imports too.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd internal/channel && go test ./... -run TestGitRollback -v`
Expected: FAIL — compile error, `(*Git).Rollback` still resolves to `linked.Rollback` which returns `ErrRollbackUnsupported`, so `TestGitRollbackSwapsBackToThePreviousRevision` fails with that error instead of a nil one.

- [ ] **Step 3: Implement `Git.Rollback`**

In `internal/channel/git.go`, add after `relink`:

```go
// Rollback swaps the receipt back onto the revision Previous* recorded — the
// same relink Update would have done to reach it, run in reverse. It is a
// toggle: the receipt's current triple becomes the new Previous*, so a
// second Rollback undoes the first.
func (c *Git) Rollback(ctx context.Context, r state.Receipt) (plan.Plan, Verdict, error) {
	v := Verdict{Name: r.Name, Channel: r.Channel, Current: r.Resolved}

	if r.PreviousResolved == "" {
		return plan.Plan{}, v, ErrNothingToRollBackTo
	}

	ops, receipt, err := c.relink(ctx, &r, r.PreviousResolved, time.Now().UTC())
	if err != nil {
		return plan.Plan{}, v, err
	}

	var p plan.Plan
	p.Add(ops...)
	p.Add(plan.Record{Receipt: receipt})

	v.Latest = r.PreviousResolved
	v.Status = StatusUpdated
	return p, v, nil
}
```

`relink` (Task 2) already does the whole swap on its own: called with `r.PreviousResolved` as the target sha, it copies `r`'s *current* `Resolved`/`RevPath`/`ContentHash` into the returned receipt's `Previous*` fields before overwriting `Resolved`/`RevPath`/`ContentHash` with the target revision — which is exactly the toggle. `Rollback` does not need to touch `Previous*` itself.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd internal/channel && go test ./... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/channel/git.go internal/channel/git_rollback_test.go
git commit -m "feat(channel): implement rollback for the git channel"
```

---

### Task 6: Implement `OCI.Rollback`

**Files:**
- Modify: `internal/channel/oci.go`
- Test: `internal/channel/oci_test.go` (new tests, same file)

**Interfaces:**
- Consumes: `c.relink`, `ociSourceOf` (existing, unexported, same file), `ErrNothingToRollBackTo` (Task 4).
- Produces: `(*OCI).Rollback(ctx, r) (plan.Plan, Verdict, error)`.

- [ ] **Step 1: Write the failing test**

Add to `internal/channel/oci_test.go`:

```go
func TestOCIRollbackSwapsBackToThePreviousDigest(t *testing.T) {
	st := store.New(t.TempDir())
	o := &fakeOCI{digest: "sha256:aaa"}
	c := NewOCI(st, o, &fakeCosign{})

	src, _ := source.Parse("oci://ghcr.io/owner/skills:v1")
	cands, _, err := c.Prepare(context.Background(), Request{Source: src, All: true})
	if err != nil {
		t.Fatal(err)
	}
	tgt := target.Target{Name: "claude", Dir: t.TempDir()}
	req := Request{Source: src, Targets: []target.Target{tgt}, All: true}
	_, receipts, _, err := c.Install(req, cands)
	if err != nil {
		t.Fatal(err)
	}
	r := receipts[0]

	// Move to a second digest, exactly as TestOCIUpdateRelinksWhenTheDigestMoved does.
	o.digest = "sha256:bbb"
	verdicts, p, err := c.Update(context.Background(), []*state.Receipt{&r}, UpdateOptions{})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(verdicts) != 1 || verdicts[0].Status != StatusUpdated {
		t.Fatalf("fixture setup: Update verdicts = %+v, want one StatusUpdated", verdicts)
	}
	for _, op := range p.Ops {
		if rec, ok := op.(plan.Record); ok {
			r = rec.Receipt
		}
	}
	if r.Resolved != "sha256:bbb" || r.PreviousResolved != "sha256:aaa" {
		t.Fatalf("fixture: receipt = %+v, want Resolved sha256:bbb and PreviousResolved sha256:aaa", r)
	}

	rp, v, err := c.Rollback(context.Background(), r)
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if v.Status != StatusUpdated {
		t.Errorf("Status = %q, want %q", v.Status, StatusUpdated)
	}
	if v.Latest != "sha256:aaa" {
		t.Errorf("Latest = %q, want the digest it rolled back to", v.Latest)
	}
	var rec plan.Record
	for _, op := range rp.Ops {
		if r, ok := op.(plan.Record); ok {
			rec = r
		}
	}
	if rec.Receipt.Resolved != "sha256:aaa" {
		t.Errorf("recorded Resolved = %q, want sha256:aaa", rec.Receipt.Resolved)
	}
	if rec.Receipt.PreviousResolved != "sha256:bbb" {
		t.Errorf("recorded PreviousResolved = %q, want the toggle to remember sha256:bbb", rec.Receipt.PreviousResolved)
	}
}

func TestOCIRollbackRefusesWithNothingToRollBackTo(t *testing.T) {
	st := store.New(t.TempDir())
	o := &fakeOCI{digest: "sha256:aaa"}
	c := NewOCI(st, o, &fakeCosign{})

	r := state.Receipt{Name: "demo", Channel: "oci", Source: "oci://ghcr.io/owner/skills:v1", Resolved: "sha256:aaa"}
	_, _, err := c.Rollback(context.Background(), r)
	if !errors.Is(err, ErrNothingToRollBackTo) {
		t.Errorf("Rollback error = %v, want ErrNothingToRollBackTo", err)
	}
}
```

Check `oci_test.go`'s existing imports; add `"github.com/richardcase/skillsctl/internal/plan"` if it is not already imported (it is not, per the file read earlier — its imports were `cosignx`, `source`, `state`, `store`, `target`, plus stdlib).

- [ ] **Step 2: Run test to verify it fails**

Run: `cd internal/channel && go test ./... -run TestOCIRollback -v`
Expected: FAIL — `c.Rollback` resolves to `linked.Rollback`, returning `ErrRollbackUnsupported` instead of swapping the digest.

- [ ] **Step 3: Implement `OCI.Rollback`**

In `internal/channel/oci.go`, add after `relink`:

```go
// Rollback swaps the receipt back onto the digest Previous* recorded.
//
// Unlike Update, which resolves against the tag the receipt tracks, this
// pins the reference to the exact previous digest
// (registry/repository@digest) rather than the tag: a tag can have moved
// since, and a rollback that re-pulled through it could silently land on
// neither the old digest nor the new one.
func (c *OCI) Rollback(ctx context.Context, r state.Receipt) (plan.Plan, Verdict, error) {
	v := Verdict{Name: r.Name, Channel: r.Channel, Current: r.Resolved}

	if r.PreviousResolved == "" {
		return plan.Plan{}, v, ErrNothingToRollBackTo
	}

	src, err := ociSourceOf(&r)
	if err != nil {
		return plan.Plan{}, v, err
	}
	ref := fmt.Sprintf("%s/%s@%s", src.Registry, src.Repository, r.PreviousResolved)

	ops, receipt, err := c.relink(ctx, &r, src, ref, r.PreviousResolved, time.Now().UTC())
	if err != nil {
		return plan.Plan{}, v, err
	}

	var p plan.Plan
	p.Add(ops...)
	p.Add(plan.Record{Receipt: receipt})

	v.Latest = r.PreviousResolved
	v.Status = StatusUpdated
	return p, v, nil
}
```

As with `Git.Rollback` (Task 5), `relink` (Task 3) already performs the whole swap: `receipt.Previous*` comes back set to `r`'s pre-rollback current triple, with no further assignment needed here.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd internal/channel && go test ./... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/channel/oci.go internal/channel/oci_test.go
git commit -m "feat(channel): implement rollback for the oci channel"
```

---

### Task 7: `skillsctl rollback <name>...`

**Files:**
- Create: `internal/cli/rollback.go`
- Modify: `internal/cli/root.go` (register the command)
- Test: `internal/cli/rollback_test.go` (new file)
- Modify: `README.md` (Commands table, a `rollback` example)

**Interfaces:**
- Consumes: `channel.Channel.Rollback` (Tasks 4–6), `state.DB.NotInstalled` (existing), `plan.Executor` (existing).

- [ ] **Step 1: Write the failing test**

Create `internal/cli/rollback_test.go`:

```go
package cli

import (
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/testrepo"
)

func TestRollbackSwapsBackToThePreviousRevision(t *testing.T) {
	h := newHarness(t)
	dir, first := installed(t, h)
	second := testrepo.Commit(t, dir, map[string]string{"SKILL.md": skillMD + "\nMore.\n"})

	out, err := h.run(t, "update")
	if err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	if got := readReceipt(t, h, "demo-skill"); got.Resolved != second {
		t.Fatalf("fixture: Resolved = %q, want %q", got.Resolved, second)
	}

	out, err = h.run(t, "rollback", "demo-skill")
	if err != nil {
		t.Fatalf("rollback: %v\n%s", err, out)
	}
	if !strings.Contains(out, "rolled back demo-skill to "+first[:7]) {
		t.Errorf("rollback did not report the revision it moved to:\n%s", out)
	}
	got := readReceipt(t, h, "demo-skill")
	if got.Resolved != first {
		t.Errorf("Resolved = %q, want the first commit %q", got.Resolved, first)
	}
	if got.PreviousResolved != second {
		t.Errorf("PreviousResolved = %q, want the toggle to remember %q", got.PreviousResolved, second)
	}
}

func TestRollbackTwiceTogglesBack(t *testing.T) {
	h := newHarness(t)
	dir, first := installed(t, h)
	second := testrepo.Commit(t, dir, map[string]string{"SKILL.md": skillMD + "\nMore.\n"})

	if out, err := h.run(t, "update"); err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	if out, err := h.run(t, "rollback", "demo-skill"); err != nil {
		t.Fatalf("rollback: %v\n%s", err, out)
	}
	if out, err := h.run(t, "rollback", "demo-skill"); err != nil {
		t.Fatalf("second rollback: %v\n%s", err, out)
	}

	got := readReceipt(t, h, "demo-skill")
	if got.Resolved != second {
		t.Errorf("Resolved = %q, want the toggle to land back on %q", got.Resolved, second)
	}
	_ = first
}

func TestRollbackRefusesASkillThatHasNeverBeenUpdated(t *testing.T) {
	h := newHarness(t)
	installed(t, h)

	code, out := exitCode(t, "rollback", "demo-skill")
	if code != ExitError {
		t.Errorf("exit = %d, want %d\n%s", code, ExitError, out)
	}
	if !strings.Contains(out, "nothing to roll back to") {
		t.Errorf("the error should say there is nothing to roll back to:\n%s", out)
	}
}

func TestRollbackReportsANameThatIsNotInstalled(t *testing.T) {
	newHarness(t)

	code, out := exitCode(t, "rollback", "never-installed")
	if code != ExitError {
		t.Errorf("exit = %d, want %d\n%s", code, ExitError, out)
	}
	if !strings.Contains(out, "never-installed") {
		t.Errorf("the error should name the skill:\n%s", out)
	}
}

func TestRollbackDryRunChangesNothing(t *testing.T) {
	h := newHarness(t)
	dir, _ := installed(t, h)
	testrepo.Commit(t, dir, map[string]string{"SKILL.md": skillMD + "\nMore.\n"})
	if out, err := h.run(t, "update"); err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	before := readReceipt(t, h, "demo-skill")

	out, err := h.run(t, "rollback", "demo-skill", "--dry-run")
	if err != nil {
		t.Fatalf("rollback --dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "relink") {
		t.Errorf("a dry run should print the plan:\n%s", out)
	}
	if got := readReceipt(t, h, "demo-skill"); got.Resolved != before.Resolved {
		t.Error("a dry run changed the receipt")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd internal/cli && go test ./... -run TestRollback -v`
Expected: FAIL — `unknown command "rollback"` (the command is not registered yet).

- [ ] **Step 3: Implement the command**

Create `internal/cli/rollback.go`:

```go
package cli

import (
	"context"
	"fmt"

	"github.com/richardcase/skillsctl/internal/channel"
	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/spf13/cobra"
)

func newRollbackCmd() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "rollback <name>...",
		Short: "Undo the last update, swapping a skill back onto its previous revision",
		Long: "Point one or more skills back at the revision they were on before their\n" +
			"last update, keeping their name, their agents and their pin.\n\n" +
			"Rollback is a toggle: running it again undoes itself, swapping back to the\n" +
			"revision the first rollback moved away from. A skill that has never been\n" +
			"updated has nothing to roll back to. `skillsctl diff <name> --against\n" +
			"previous` shows what a rollback would change before you run it.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRollback(cmd, args, dryRun)
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change without changing it")
	return cmd
}

// rollbackEntry is what happened to one name: the verdict the channel
// returned, or why nothing could be made of it.
type rollbackEntry struct {
	name string
	v    channel.Verdict
	err  error
}

func runRollback(cmd *cobra.Command, names []string, dryRun bool) error {
	ctx := cmd.Context()

	e, err := newEnv()
	if err != nil {
		return err
	}
	h, err := e.openState()
	if err != nil {
		return err
	}
	defer func() { _ = h.Close() }()

	reg := e.channels()
	entries := make([]rollbackEntry, 0, len(names))
	taken := make(map[string]bool, len(names))

	var p plan.Plan
	for _, name := range names {
		// Naming a skill twice is one request for it, not two.
		if taken[name] {
			continue
		}
		taken[name] = true

		en, ops := rollbackOne(ctx, reg, h.DB, name)
		p.Add(ops.Ops...)
		entries = append(entries, en)
	}

	if dryRun {
		for _, line := range p.Describe() {
			cmd.Println(line)
		}
		reportRollback(cmd, entries, true)
		return rollbackExit(entries)
	}

	if !p.IsEmpty() {
		ex := &plan.Executor{DB: h.DB, Out: cmd.OutOrStdout(), Run: newRunner()}
		if err := ex.Apply(ctx, p); err != nil {
			return err
		}
		if err := h.Commit(); err != nil {
			return fmt.Errorf("%w\nthe skill was re-linked but the receipt was not saved; re-run this command to repair", err)
		}
	}

	reportRollback(cmd, entries, false)
	return rollbackExit(entries)
}

// rollbackOne resolves one name to a verdict and the ops that carry it out.
// Every way it can fail is a verdict rather than an error, so one unknown
// name never hides what could be done with the rest.
func rollbackOne(ctx context.Context, reg channel.Registry, db *state.DB, name string) (rollbackEntry, plan.Plan) {
	en := rollbackEntry{name: name}

	r, ok := db.Receipts[name]
	if !ok {
		en.err = db.NotInstalled(name)
		return en, plan.Plan{}
	}

	ch, err := reg.ForReceipt(r)
	if err != nil {
		en.err = err
		return en, plan.Plan{}
	}

	p, v, err := ch.Rollback(ctx, *r)
	if err != nil {
		en.err = err
		return en, plan.Plan{}
	}
	en.v = v
	return en, p
}

// reportRollback writes one line per name. A dry run has already printed the
// plan, so it adds only the names that produced no op: the failures.
func reportRollback(cmd *cobra.Command, entries []rollbackEntry, dryRun bool) {
	for _, en := range entries {
		switch {
		case en.err != nil:
			cmd.Printf("skipped %s: %s\n", en.name, reason(en.err))
		case dryRun:
		default:
			cmd.Printf("rolled back %s to %s\n", en.name, shortSha(en.v.Latest))
		}
	}
}

// rollbackExit turns the report into an exit code, once the reasons are
// already on screen.
func rollbackExit(entries []rollbackEntry) error {
	var done, skipped int
	for _, en := range entries {
		if en.err != nil {
			skipped++
			continue
		}
		done++
	}

	switch {
	case skipped == 0:
		return nil
	case done > 0:
		return partialf("%s rolled back, %s skipped", count(done, "skill"), count(skipped, "skill"))
	default:
		return fmt.Errorf("nothing was rolled back: %s skipped, for the reasons above", count(skipped, "skill"))
	}
}
```

In `internal/cli/root.go`, add `newRollbackCmd()` to the `root.AddCommand(...)` list, alphabetically between `newRemoveCmd()` and `newSyncCmd()`.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd internal/cli && go test ./... -v`
Expected: PASS.

- [ ] **Step 5: Update the README**

Add a `rollback` row to the Commands table (next to `update`), and a short example under Use, following the existing entries' style — e.g.:

```markdown
| `skillsctl rollback <name>...` | Swap a skill back onto the revision it was on before its last update (a toggle: running it twice undoes itself) |
```

- [ ] **Step 6: Commit**

```bash
git add internal/cli/rollback.go internal/cli/rollback_test.go internal/cli/root.go README.md
git commit -m "feat(cli): add skillsctl rollback"
```

---

## Phase 2 — `diff`

### Task 8: `gitx.Git.Diff` and `gitx.Git.DiffDirs`

**Files:**
- Modify: `internal/gitx/gitx.go`
- Test: `internal/gitx/gitx_test.go`

**Interfaces:**
- Produces: `Git.Diff(ctx, mirrorPath, fromSha, toSha string) (string, error)` and `Git.DiffDirs(ctx, from, to string) (string, error)` on the `Git` interface — both return `""` when there is no difference, which is what lets a caller print "no changes" without inspecting text.

- [ ] **Step 1: Write the failing test**

Add to `internal/gitx/gitx_test.go`:

```go
func TestDiffReportsTheChangeBetweenTwoShas(t *testing.T) {
	url, first := testrepo.New(t, map[string]string{"a.txt": "one\n"})
	second := testrepo.Commit(t, testrepo.Dir(url), map[string]string{"a.txt": "two\n"})

	c := New()
	mirror := filepath.Join(t.TempDir(), "mirror.git")
	if err := c.Mirror(context.Background(), url, mirror); err != nil {
		t.Fatalf("Mirror: %v", err)
	}

	out, err := c.Diff(context.Background(), mirror, first, second)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(out, "-one") || !strings.Contains(out, "+two") {
		t.Errorf("Diff output missing the expected change:\n%s", out)
	}
}

func TestDiffOfIdenticalShasIsEmpty(t *testing.T) {
	url, sha := testrepo.New(t, map[string]string{"a.txt": "one\n"})

	c := New()
	mirror := filepath.Join(t.TempDir(), "mirror.git")
	if err := c.Mirror(context.Background(), url, mirror); err != nil {
		t.Fatalf("Mirror: %v", err)
	}

	out, err := c.Diff(context.Background(), mirror, sha, sha)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if out != "" {
		t.Errorf("Diff of identical shas = %q, want empty", out)
	}
}

func TestDiffDirsReportsTheChangeBetweenTwoDirectories(t *testing.T) {
	from, to := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(from, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(to, "a.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := New()
	out, err := c.DiffDirs(context.Background(), from, to)
	if err != nil {
		t.Fatalf("DiffDirs: %v", err)
	}
	if !strings.Contains(out, "-one") || !strings.Contains(out, "+two") {
		t.Errorf("DiffDirs output missing the expected change:\n%s", out)
	}
}

func TestDiffDirsOfIdenticalDirectoriesIsEmpty(t *testing.T) {
	from, to := t.TempDir(), t.TempDir()
	for _, dir := range []string{from, to} {
		if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	c := New()
	out, err := c.DiffDirs(context.Background(), from, to)
	if err != nil {
		t.Fatalf("DiffDirs: %v", err)
	}
	if out != "" {
		t.Errorf("DiffDirs of identical directories = %q, want empty", out)
	}
}
```

Check the file's existing imports first; add `"os"` if not already present (`"path/filepath"`, `"strings"`, `"context"`, and `testrepo` are already imported, per the tests read earlier in this plan's research).

- [ ] **Step 2: Run test to verify it fails**

Run: `cd internal/gitx && go test ./... -run TestDiff -v`
Expected: FAIL — compile error, `(*CLI).Diff` and `(*CLI).DiffDirs` undefined.

- [ ] **Step 3: Implement `Diff` and `DiffDirs`**

In `internal/gitx/gitx.go`, add `Diff` and `DiffDirs` to the `Git` interface:

```go
	// Diff returns the unified diff between two shas in the mirror at
	// mirrorPath, or "" if they are identical.
	Diff(ctx context.Context, mirrorPath, fromSha, toSha string) (string, error)
	// DiffDirs returns the unified diff between two directories with no git
	// repository backing either of them, or "" if they are identical.
	DiffDirs(ctx context.Context, from, to string) (string, error)
```

Then implement on `*CLI`, after `Extract`:

```go
// Diff returns the unified diff between two shas in the mirror at
// mirrorPath, or "" if they are identical.
func (c *CLI) Diff(ctx context.Context, mirrorPath, fromSha, toSha string) (string, error) {
	return c.diffOutput(ctx, mirrorPath, "diff", fromSha, toSha)
}

// DiffDirs returns the unified diff between two directories with no git
// repository backing either of them, or "" if they are identical. It is
// what lets an OCI revision be diffed: there is no mirror to compare shas
// in, only two extracted trees, and `git diff --no-index` compares two
// paths without needing either to be inside a repository.
func (c *CLI) DiffDirs(ctx context.Context, from, to string) (string, error) {
	return c.diffOutput(ctx, "", "diff", "--no-index", from, to)
}

// diffOutput runs a git diff variant. git exits 1 to report that it found
// differences, which every other command here would treat as a failure —
// here it is the answer, and only 2 or more is an actual error.
func (c *CLI) diffOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, c.Bin, args...)
	cmd.Dir = dir
	cmd.Env = env()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return stdout.String(), nil
	}
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd internal/gitx && go test ./... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/gitx/gitx.go internal/gitx/gitx_test.go
git commit -m "feat(gitx): add Diff and DiffDirs"
```

---

### Task 9: `internal/diff` package

**Files:**
- Create: `internal/diff/diff.go`
- Test: `internal/diff/diff_test.go`

**Interfaces:**
- Consumes: `gitx.Git.Diff`/`DiffDirs` (Task 8), `store.Store.MirrorPath`/`EnsureOCI` (existing), `ocix.OCI.Resolve` (existing).
- Produces: `diff.Against` (`diff.Latest`, `diff.Previous`), `diff.Check(ctx, g gitx.Git, o ocix.OCI, st *store.Store, r *state.Receipt, against Against) (string, error)` — consumed by `cli/diff.go` in Task 10.

- [ ] **Step 1: Write the failing test**

Create `internal/diff/diff_test.go`:

```go
package diff

import (
	"context"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/richardcase/skillsctl/internal/ocix"
	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/store"
	"github.com/richardcase/skillsctl/internal/testrepo"
)

const diffSkillMD = "---\nname: demo\ndescription: A demo\n---\n\nBody.\n"

func gitFixture(t *testing.T) (st *store.Store, g gitx.Git, r *state.Receipt, first, second string) {
	t.Helper()

	url, first := testrepo.New(t, map[string]string{"SKILL.md": diffSkillMD})
	second = testrepo.Commit(t, testrepo.Dir(url), map[string]string{"SKILL.md": diffSkillMD + "\nMore.\n"})

	src, err := source.Parse(url)
	if err != nil {
		t.Fatalf("Parse(%q): %v", url, err)
	}

	st = store.New(t.TempDir())
	g = gitx.New()
	rev, err := st.Ensure(context.Background(), g, src.Slug(), src.RepoURL, first)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	r = &state.Receipt{
		Name:     "demo",
		Channel:  "git",
		Source:   src.RepoURL,
		Slug:     src.Slug(),
		Ref:      "main",
		Resolved: first,
		RevPath:  rev,
	}
	return st, g, r, first, second
}

func TestCheckAgainstLatestReportsWhatUpdateWouldChange(t *testing.T) {
	st, g, r, _, second := gitFixture(t)

	out, err := Check(context.Background(), g, refusingOCI{}, st, r, Latest)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !strings.Contains(out, "More.") {
		t.Errorf("diff missing the change introduced by the second commit:\n%s", out)
	}
	_ = second
}

func TestCheckAgainstLatestIsEmptyWhenCurrent(t *testing.T) {
	st, g, r, _, second := gitFixture(t)
	r.Resolved = second
	r.Ref = "main"

	out, err := Check(context.Background(), g, refusingOCI{}, st, r, Latest)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if out != "" {
		t.Errorf("Check() = %q, want empty: nothing has moved", out)
	}
}

func TestCheckAgainstPreviousUsesThePreviousRevision(t *testing.T) {
	st, g, r, first, second := gitFixture(t)
	r.Resolved = second
	r.PreviousResolved = first

	out, err := Check(context.Background(), g, refusingOCI{}, st, r, Previous)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !strings.Contains(out, "More.") {
		t.Errorf("diff missing the change between the two revisions:\n%s", out)
	}
}

func TestCheckAgainstPreviousRefusesWhenThereIsNone(t *testing.T) {
	st, g, r, _, _ := gitFixture(t)

	_, err := Check(context.Background(), g, refusingOCI{}, st, r, Previous)
	if err == nil {
		t.Fatal("Check accepted Previous with no PreviousResolved recorded")
	}
	if !strings.Contains(err.Error(), "never been updated") {
		t.Errorf("error = %q, want it to say the skill has never been updated", err)
	}
}

func TestCheckRefusesAnUnsupportedChannel(t *testing.T) {
	st, g, r, _, _ := gitFixture(t)
	r.Channel = "local"

	_, err := Check(context.Background(), g, refusingOCI{}, st, r, Latest)
	if err == nil {
		t.Fatal("Check accepted a local receipt, which has no revision history to diff")
	}
}

// refusingOCI fails loudly if diff reaches for a registry during a
// git-only test.
type refusingOCI struct{}

func (refusingOCI) Resolve(context.Context, string) (string, error) { return "", errNoOCI }
func (refusingOCI) Pull(context.Context, string, string) error      { return errNoOCI }
func (refusingOCI) Push(context.Context, string, io.Reader) error   { return errNoOCI }

var errNoOCI = errors.New("this test did not expect an OCI call")
```

Add `"errors"` and `"io"` to the test file's imports alongside `"context"`, `"strings"` and `"testing"`.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd internal/diff && go test ./... -v`
Expected: FAIL — the package does not exist yet (`internal/diff/diff.go` is not created until the next step), so this is a compile/build error.

- [ ] **Step 3: Implement the package**

Create `internal/diff/diff.go`:

```go
// Package diff compares an installed skill's revision against another one —
// what update would move to, or what rollback would move back to — and
// returns the unified diff between them. Like outdated, it performs no
// mutation of its own: comparing two revisions is read-only, so this
// package produces no plan.Plan.
package diff

import (
	"context"
	"fmt"

	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/richardcase/skillsctl/internal/ocix"
	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/store"
)

// Against selects which revision an installed skill is compared to.
type Against string

const (
	// Latest compares against what update would move to.
	Latest Against = "latest"
	// Previous compares against the revision Previous* recorded — what
	// rollback would move back to.
	Previous Against = "previous"
)

// Check diffs r's installed revision against Latest or Previous, and
// returns the unified diff. The result is "" when the two revisions are
// identical, which is what lets a caller print "no changes" without
// inspecting the text.
//
// Only the git and OCI channels have a revision history to diff; any other
// channel is refused by name, the same way outdated skips a local skill's
// ref column.
func Check(ctx context.Context, g gitx.Git, o ocix.OCI, st *store.Store, r *state.Receipt, against Against) (string, error) {
	toSha := r.PreviousResolved
	if against != Previous {
		latest, err := resolveLatest(ctx, g, o, r)
		if err != nil {
			return "", err
		}
		toSha = latest
	} else if toSha == "" {
		return "", fmt.Errorf("%s has never been updated: nothing to diff against its previous revision", r.Name)
	}

	if toSha == r.Resolved {
		return "", nil
	}

	switch r.Channel {
	case string(source.ChannelGit):
		slug, err := gitSlug(r)
		if err != nil {
			return "", err
		}
		return g.Diff(ctx, st.MirrorPath(slug), r.Resolved, toSha)

	case string(source.ChannelOCI):
		src, err := ociSourceOf(r)
		if err != nil {
			return "", err
		}
		slug := r.Slug
		if slug == "" {
			slug = src.Slug()
		}
		fromDir, err := st.EnsureOCI(ctx, o, slug, ociDigestRef(src, r.Resolved), r.Resolved)
		if err != nil {
			return "", err
		}
		toDir, err := st.EnsureOCI(ctx, o, slug, ociDigestRef(src, toSha), toSha)
		if err != nil {
			return "", err
		}
		return g.DiffDirs(ctx, fromDir, toDir)

	default:
		return "", fmt.Errorf("diff is not supported for the %s channel", r.Channel)
	}
}

// resolveLatest reads only, the same promise outdated makes: a git ref is
// read with ls-remote, an OCI tag's manifest is read without pulling a
// layer, and nothing is mirrored or extracted.
func resolveLatest(ctx context.Context, g gitx.Git, o ocix.OCI, r *state.Receipt) (string, error) {
	switch r.Channel {
	case string(source.ChannelGit):
		ref := r.Ref
		if ref == "" {
			ref = "HEAD"
		}
		return g.Resolve(ctx, r.Source, ref)

	case string(source.ChannelOCI):
		src, err := ociSourceOf(r)
		if err != nil {
			return "", err
		}
		return o.Resolve(ctx, src.OCIRef(r.Ref))

	default:
		return "", fmt.Errorf("diff is not supported for the %s channel", r.Channel)
	}
}

// gitSlug is where in the store this receipt's mirror lives. Receipts
// written before the slug was recorded fall back to deriving it from the
// source, the same fallback the git channel's relink uses.
func gitSlug(r *state.Receipt) (string, error) {
	if r.Slug != "" {
		return r.Slug, nil
	}
	src, err := source.Parse(r.Source)
	if err != nil {
		return "", fmt.Errorf("this receipt records no store location and %q cannot be parsed: %w", r.Source, err)
	}
	return src.Slug(), nil
}

// ociSourceOf re-reads the source a receipt was installed from. A receipt
// records the oci:// form precisely so this round trip exists: the bare
// registry/repository:tag a registry client wants is derived from it, never
// the other way about.
func ociSourceOf(r *state.Receipt) (source.Source, error) {
	src, err := source.Parse(r.Source)
	if err != nil {
		return source.Source{}, fmt.Errorf("this receipt records %q, which cannot be parsed as an oci source: %w", r.Source, err)
	}
	return src, nil
}

// ociDigestRef pins a reference to an exact digest rather than a tag: a tag
// can have moved since the receipt was written, and a diff that read
// through it could silently compare against neither revision it was asked
// for.
func ociDigestRef(src source.Source, digest string) string {
	return fmt.Sprintf("%s/%s@%s", src.Registry, src.Repository, digest)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd internal/diff && go test ./... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/diff/diff.go internal/diff/diff_test.go
git commit -m "feat(diff): add the diff package"
```

---

### Task 10: `skillsctl diff <name>`

**Files:**
- Create: `internal/cli/diff.go`
- Modify: `internal/cli/root.go` (register the command)
- Test: `internal/cli/diff_test.go` (new file)
- Modify: `README.md` (Commands table, a `diff` example)

**Interfaces:**
- Consumes: `diff.Check` (Task 9), `gitx.New()`, `newOCI()` (existing seam), `e.store` (existing `env` field).

- [ ] **Step 1: Write the failing test**

Create `internal/cli/diff_test.go`:

```go
package cli

import (
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/testrepo"
)

func TestDiffAgainstLatestShowsWhatUpdateWouldChange(t *testing.T) {
	h := newHarness(t)
	dir, _ := installed(t, h)
	testrepo.Commit(t, dir, map[string]string{"SKILL.md": skillMD + "\nMore.\n"})

	out, err := h.run(t, "diff", "demo-skill")
	if err != nil {
		t.Fatalf("diff: %v\n%s", err, out)
	}
	if !strings.Contains(out, "More.") {
		t.Errorf("diff missing the pending change:\n%s", out)
	}
}

func TestDiffWithNoChangesSaysSo(t *testing.T) {
	h := newHarness(t)
	installed(t, h)

	out, err := h.run(t, "diff", "demo-skill")
	if err != nil {
		t.Fatalf("diff: %v\n%s", err, out)
	}
	if !strings.Contains(out, "no changes") {
		t.Errorf("diff should say there is nothing to show:\n%s", out)
	}
}

func TestDiffAgainstPreviousShowsWhatRollbackWouldUndo(t *testing.T) {
	h := newHarness(t)
	dir, first := installed(t, h)
	testrepo.Commit(t, dir, map[string]string{"SKILL.md": skillMD + "\nMore.\n"})
	if out, err := h.run(t, "update"); err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}

	out, err := h.run(t, "diff", "demo-skill", "--against", "previous")
	if err != nil {
		t.Fatalf("diff --against previous: %v\n%s", err, out)
	}
	if !strings.Contains(out, "More.") {
		t.Errorf("diff missing the change rollback would undo:\n%s", out)
	}
	_ = first
}

func TestDiffAgainstPreviousRefusesASkillThatHasNeverBeenUpdated(t *testing.T) {
	h := newHarness(t)
	installed(t, h)

	code, out := exitCode(t, "diff", "demo-skill", "--against", "previous")
	if code != ExitError {
		t.Errorf("exit = %d, want %d\n%s", code, ExitError, out)
	}
	if !strings.Contains(out, "never been updated") {
		t.Errorf("the error should say the skill has never been updated:\n%s", out)
	}
}

func TestDiffRejectsAnUnknownAgainstValue(t *testing.T) {
	h := newHarness(t)
	installed(t, h)

	code, out := exitCode(t, "diff", "demo-skill", "--against", "yesterday")
	if code != ExitError {
		t.Errorf("exit = %d, want %d\n%s", code, ExitError, out)
	}
	if !strings.Contains(out, "yesterday") {
		t.Errorf("the error should name the value it rejected:\n%s", out)
	}
}

func TestDiffReportsANameThatIsNotInstalled(t *testing.T) {
	newHarness(t)

	code, out := exitCode(t, "diff", "never-installed")
	if code != ExitError {
		t.Errorf("exit = %d, want %d\n%s", code, ExitError, out)
	}
	if !strings.Contains(out, "never-installed") {
		t.Errorf("the error should name the skill:\n%s", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd internal/cli && go test ./... -run TestDiff -v`
Expected: FAIL — `unknown command "diff"`.

- [ ] **Step 3: Implement the command**

Create `internal/cli/diff.go`:

```go
package cli

import (
	"fmt"

	"github.com/richardcase/skillsctl/internal/diff"
	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/spf13/cobra"
)

func newDiffCmd() *cobra.Command {
	var against string

	cmd := &cobra.Command{
		Use:   "diff <name>",
		Short: "Show what an update would change, or what a rollback would undo",
		Long: "Compare an installed skill's revision against another one and print a\n" +
			"unified diff.\n\n" +
			"--against latest (the default) compares against what `update` would move to,\n" +
			"reading refs only — nothing is fetched or installed. --against previous\n" +
			"compares against the revision `rollback` would swap back to.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDiff(cmd, args[0], against)
		},
	}

	cmd.Flags().StringVar(&against, "against", "latest", "revision to compare against: latest or previous")
	return cmd
}

func runDiff(cmd *cobra.Command, name, against string) error {
	var mode diff.Against
	switch against {
	case "latest":
		mode = diff.Latest
	case "previous":
		mode = diff.Previous
	default:
		return fmt.Errorf("--against must be latest or previous, not %q", against)
	}

	e, err := newEnv()
	if err != nil {
		return err
	}
	h, err := e.openState()
	if err != nil {
		return err
	}
	defer func() { _ = h.Close() }()

	r, ok := h.DB.Receipts[name]
	if !ok {
		return h.DB.NotInstalled(name)
	}

	unified, err := diff.Check(cmd.Context(), gitx.New(), newOCI(), e.store, r, mode)
	if err != nil {
		return err
	}
	if unified == "" {
		cmd.Println("no changes")
		return nil
	}
	cmd.Print(unified)
	return nil
}
```

In `internal/cli/root.go`, add `newDiffCmd()` to the `root.AddCommand(...)` list, alphabetically between `newBundleCmd()` and `newDoctorCmd()`.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd internal/cli && go test ./... -v`
Expected: PASS.

- [ ] **Step 5: Update the README**

Add a `diff` row to the Commands table (next to `outdated`), with a `--against` mention, and a short example under Use:

```markdown
| `skillsctl diff <name> [--against latest\|previous]` | Print the unified diff between an installed skill and what `update` would move to, or what `rollback` would move back to |
```

- [ ] **Step 6: Commit**

```bash
git add internal/cli/diff.go internal/cli/diff_test.go internal/cli/root.go README.md
git commit -m "feat(cli): add skillsctl diff"
```

---

## Phase 3 — Agent-compatibility metadata

### Task 11: `discover.Meta.Agents`

**Files:**
- Modify: `internal/discover/discover.go:33-36`
- Test: `internal/discover/discover_test.go`

**Interfaces:**
- Produces: `discover.Meta.Agents []string` — read by `agentWarnings` (Task 12).

- [ ] **Step 1: Write the failing test**

Add to `internal/discover/discover_test.go`:

```go
func TestFrontmatterParsesAgents(t *testing.T) {
	body := []byte("---\nname: demo\ndescription: A demo\nagents:\n  - claude\n  - codex\n---\n")

	got, err := Frontmatter(body)
	if err != nil {
		t.Fatalf("Frontmatter: %v", err)
	}
	want := []string{"claude", "codex"}
	if len(got.Agents) != len(want) {
		t.Fatalf("Agents = %v, want %v", got.Agents, want)
	}
	for i, a := range want {
		if got.Agents[i] != a {
			t.Errorf("Agents[%d] = %q, want %q", i, got.Agents[i], a)
		}
	}
}

func TestFrontmatterWithNoAgentsIsUnrestricted(t *testing.T) {
	got, err := Frontmatter([]byte("---\nname: demo\ndescription: A demo\n---\n"))
	if err != nil {
		t.Fatalf("Frontmatter: %v", err)
	}
	if len(got.Agents) != 0 {
		t.Errorf("Agents = %v, want none: a skill with no agents field is unrestricted", got.Agents)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd internal/discover && go test ./... -run TestFrontmatterParsesAgents -v`
Expected: FAIL — `got.Agents` does not compile, `Meta` has no `Agents` field.

- [ ] **Step 3: Add the field**

In `internal/discover/discover.go`:

```go
// Meta is the frontmatter skillsctl cares about.
type Meta struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	// Agents declares which agents this skill was written for. Empty means
	// unrestricted: every SKILL.md written before this field existed keeps
	// installing exactly as it did before. A non-empty list is advisory —
	// install still links into an agent that is not listed, but warns.
	Agents []string `yaml:"agents,omitempty"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd internal/discover && go test ./... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/discover/discover.go internal/discover/discover_test.go
git commit -m "feat(discover): parse an agents frontmatter field"
```

---

### Task 12: Warn on install into an undeclared agent

**Files:**
- Modify: `internal/channel/git.go` (new `agentWarnings` helper, wired into `Prepare`)
- Modify: `internal/channel/oci.go` (wired into `Prepare`)
- Test: `internal/channel/git_test.go`, `internal/channel/oci_test.go`
- Test: `internal/cli/install_test.go` (new test, end-to-end through the warnings the CLI already prints)
- Modify: `README.md` (document the `agents:` frontmatter field)

**Interfaces:**
- Consumes: `discover.Skill.Meta.Agents` (Task 11), `selection` and `target.Target` (existing, same package).
- Produces: `agentWarnings(sels []selection, targets []target.Target) []string`, called from `Git.Prepare` and `OCI.Prepare`.

- [ ] **Step 1: Write the failing test**

Add to `internal/channel/git_test.go`:

```go
func TestAgentWarningsFlagsAnUndeclaredTarget(t *testing.T) {
	sels := []selection{
		{name: "demo", skill: discover.Skill{Meta: discover.Meta{Agents: []string{"claude"}}}},
	}
	targets := []target.Target{{Name: "claude"}, {Name: "codex"}}

	got := agentWarnings(sels, targets)
	if len(got) != 1 {
		t.Fatalf("got %d warnings, want 1: %v", len(got), got)
	}
	if !strings.Contains(got[0], "codex") || !strings.Contains(got[0], "demo") {
		t.Errorf("warning = %q, want it to name the skill and the undeclared agent", got[0])
	}
}

func TestAgentWarningsEmptyWhenUnrestricted(t *testing.T) {
	sels := []selection{{name: "demo", skill: discover.Skill{}}}
	targets := []target.Target{{Name: "claude"}, {Name: "codex"}}

	if got := agentWarnings(sels, targets); len(got) != 0 {
		t.Errorf("got %v, want no warnings for a skill with no agents declared", got)
	}
}

func TestAgentWarningsEmptyWhenEveryTargetIsDeclared(t *testing.T) {
	sels := []selection{
		{name: "demo", skill: discover.Skill{Meta: discover.Meta{Agents: []string{"claude", "codex"}}}},
	}
	targets := []target.Target{{Name: "claude"}, {Name: "codex"}}

	if got := agentWarnings(sels, targets); len(got) != 0 {
		t.Errorf("got %v, want no warnings when every target is declared", got)
	}
}
```

Check `git_test.go`'s existing imports; add `"strings"` and `"github.com/richardcase/skillsctl/internal/target"` if not already present (`discover` is already imported, used by the existing `resolveNames` tests).

- [ ] **Step 2: Run test to verify it fails**

Run: `cd internal/channel && go test ./... -run TestAgentWarnings -v`
Expected: FAIL — `agentWarnings` undefined.

- [ ] **Step 3: Implement `agentWarnings` and wire it in**

In `internal/channel/git.go`, add near `brief`:

```go
// agentWarnings reports which of targets a chosen skill did not declare
// itself compatible with. A skill with no Agents in its frontmatter is
// unrestricted, so it warns about nothing: this is advisory only, and every
// SKILL.md written before the field existed must keep installing silently.
func agentWarnings(sels []selection, targets []target.Target) []string {
	var warnings []string
	for _, s := range sels {
		if len(s.skill.Agents) == 0 {
			continue
		}
		declared := make(map[string]bool, len(s.skill.Agents))
		for _, a := range s.skill.Agents {
			declared[a] = true
		}
		for _, t := range targets {
			if !declared[t.Name] {
				warnings = append(warnings, fmt.Sprintf(
					"warning: %s declares agents: %s, which does not include %s",
					s.name, strings.Join(s.skill.Agents, ", "), t.Name))
			}
		}
	}
	return warnings
}
```

Add `"strings"` to `git.go`'s imports.

Change `Git.Prepare`'s final lines from:

```go
	cands, err := c.candidates(chosen, revRoot, sha)
	return cands, nil, err
```

to:

```go
	cands, err := c.candidates(chosen, revRoot, sha)
	return cands, agentWarnings(chosen, req.Targets), err
```

In `internal/channel/oci.go`, change `OCI.Prepare`'s final lines from:

```go
	cands, err := c.candidates(chosen, revRoot, digest)
	return cands, warnings, err
```

to:

```go
	cands, err := c.candidates(chosen, revRoot, digest)
	warnings = append(warnings, agentWarnings(chosen, req.Targets)...)
	return cands, warnings, err
```

(`warnings` already exists in that function's scope, from `c.checkSignature`.)

- [ ] **Step 4: Run test to verify it passes**

Run: `cd internal/channel && go test ./... -v`
Expected: PASS.

- [ ] **Step 5: Add the CLI-level test**

Add to `internal/cli/install_test.go` (check its existing imports first — `testrepo` is already used by other tests in that file):

```go
func TestInstallWarnsOnAnUndeclaredAgentButStillLinks(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{
		"SKILL.md": "---\nname: demo-skill\ndescription: A demo\nagents:\n  - claude\n---\n\nBody.\n",
	})

	out, err := h.run(t, "install", url)
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if !strings.Contains(out, "codex") || !strings.Contains(out, "demo-skill") {
		t.Errorf("install did not warn about the undeclared agent:\n%s", out)
	}
	if _, err := os.Lstat(filepath.Join(h.codex, "demo-skill")); err != nil {
		t.Errorf("install should still link into codex despite the warning: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(h.claude, "demo-skill")); err != nil {
		t.Errorf("install should link into the declared agent claude: %v", err)
	}
}

func TestInstallWithNoAgentsFieldWarnsAboutNothing(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	out, err := h.run(t, "install", url)
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if strings.Contains(out, "warning:") {
		t.Errorf("a skill with no agents field should warn about nothing:\n%s", out)
	}
}
```

Add `"os"`, `"path/filepath"`, and `"strings"` to `install_test.go`'s imports if not already present.

- [ ] **Step 6: Run the CLI test to verify it passes**

Run: `cd internal/cli && go test ./... -run TestInstallWarns -v`
Expected: PASS — the harness installs into both `claude` and `codex` by default (Task's prior research: `newHarness` configures both as present targets), so a skill declaring only `claude` triggers exactly the warning `TestInstallWarnsOnAnUndeclaredAgentButStillLinks` checks for.

- [ ] **Step 7: Update the README**

Document the `agents:` frontmatter field wherever `SKILL.md` frontmatter is already described (search the README for `description:` in a frontmatter example and add `agents:` beside it), e.g.:

```markdown
`agents:` (optional) declares which agents a skill was written for, as a
YAML list (`agents: [claude, codex]`). Installing into an agent not in the
list still links it, but prints a warning.
```

- [ ] **Step 8: Commit**

```bash
git add internal/channel/git.go internal/channel/oci.go internal/channel/git_test.go internal/channel/oci_test.go internal/cli/install_test.go README.md
git commit -m "feat(channel): warn when a skill installs into an undeclared agent"
```

---

## Self-Review Notes

- **Spec coverage:** every bullet in `docs/superpowers/specs/2026-08-21-lifecycle-safety-design.md` maps to a task — receipt schema (Task 1), rollback for git/OCI (Tasks 2-3, 5-6), the `rollback` command (Task 7), `gitx.Diff`/`DiffDirs` (Task 8), the `diff` package and command (Tasks 9-10), and agent-compatibility metadata (Tasks 11-12).
- **Design deviation, called out:** the spec's prose says the agent-compatibility check lives in `resolveNames`; `resolveNames` does not have `req.Targets` in scope, so Task 12 places the check where `Prepare` narrows to `chosen` selections instead — same intent (warn once both the skill's declared agents and the request's targets are known), more precise location.
- **Design deviation, called out:** the spec describes `Channel.Rollback(ctx, receipts) (Verdicts, error)` batched like `Update`. Since `rollback <name>...` is invoked per named skill with no shared network round trip to cache (unlike `Update`'s ls-remote caching), Task 4 instead shapes it after `Channel.Pin` — one receipt in, one verdict out — which is what let Task 7's CLI command come out nearly identical to `cli/pin.go`.
