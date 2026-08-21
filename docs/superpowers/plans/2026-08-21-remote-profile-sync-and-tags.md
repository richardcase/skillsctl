# Remote Profile Sync and Tags Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let `skillsctl sync` read `skills.toml` from a git-hosted "profile repo" instead of only a local file, and add `tags` so `list`/`bundle` can filter a large skill set.

**Architecture:** `sync <arg>` tries `os.Stat(arg)` first; only when nothing local exists does it treat `arg` as a git source and fetch `skills.toml` from the repo root through a new `manifest.FetchRemote`, which composes `source.Parse` + `gitx.Resolve` + the existing `store.Ensure` cache — the same three calls an install already makes, no new mechanism. `Tags []string` is added to `manifest.Entry` and `state.Receipt`, copied entry→receipt only at first install (never rewritten on an already-installed skill, mirroring `sync`'s "only ever adds"), and exposed as a repeatable `--tag` filter on `list` and `bundle`.

**Tech Stack:** Go 1.25, `github.com/pelletier/go-toml/v2`, `github.com/spf13/cobra`, standard library only in tests.

**Spec:** `docs/superpowers/specs/2026-08-21-remote-profile-sync-and-tags-design.md`

## Global Constraints

- Go 1.25, `GOTOOLCHAIN=local`. Use `make build` / `make test` / `make lint` / `make tidy-check`, not raw `go`/`golangci-lint`.
- Definition of done for this plan: `make test && make lint && make tidy-check` all pass, and `README.md` is updated in the same PR wherever the user-visible surface changed (Task 6).
- Conventional Commits required, no attribution footers. Use `feat:` for the two new capabilities; a later `fix:` only if review turns one up.
- Tests: standard library only, table-driven with `t.Run`, `t.TempDir()`, `t.Setenv`/`t.Chdir` for isolation, `internal/testrepo` for git fixtures — no network, no testify, no mocks. **Never call `t.Parallel()`**.
- Errors: `fmt.Errorf` with `%w` and a lowercase, verb-first prefix naming the operation and the path.
- Path/source safety: reuse `source.Parse`'s existing validation; do not hand-roll a second one.
- Run `make fmt` before each commit that touches struct field alignment (`Entry`, `Receipt`) — `gofmt`/`gofumpt` re-align struct tags, and a hand-typed alignment will otherwise fail `make lint`.

---

### Task 1: `Tags` on `Entry` and `Receipt`, threaded through bundle and sync

**Files:**
- Modify: `internal/manifest/manifest.go` (the `Entry` struct)
- Modify: `internal/state/state.go` (the `Receipt` struct)
- Modify: `internal/manifest/bundle.go` (`entryFor`)
- Modify: `internal/manifest/plan.go` (`planMissing`, new `applyTags` helper)
- Test: `internal/manifest/manifest_test.go` (TOML round trip)
- Test: `internal/manifest/bundle_test.go` (receipt → entry projection)
- Test: `internal/manifest/tags_plan_test.go` (new file: entry → receipt on install)

**Interfaces:**
- Produces: `Entry.Tags []string` (toml `tags,omitempty`), `state.Receipt.Tags []string` (json `tags,omitempty`) — later tasks (`list --tag`, `bundle --tag`) read `Receipt.Tags` directly.

- [ ] **Step 1: Write the failing TOML round-trip test**

Add to `internal/manifest/manifest_test.go`:

```go
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
```

- [ ] **Step 2: Run it and confirm it fails to compile**

Run: `make test`
Expected: FAIL — `Entry.Tags` is undefined (compile error), since the field does not exist yet.

- [ ] **Step 3: Add `Tags` to `Entry` and `Receipt`**

In `internal/manifest/manifest.go`, extend `Entry`:

```go
type Entry struct {
	Name    string   `toml:"name"`
	Source  string   `toml:"source"`
	Subpath string   `toml:"subpath,omitempty"`
	Ref     string   `toml:"ref,omitempty"`
	Pinned  bool     `toml:"pinned,omitempty"`
	Agents  []string `toml:"agents,omitempty"`
	Tags    []string `toml:"tags,omitempty"`
}
```

In `internal/state/state.go`, extend `Receipt` (add the field after `Pinned`, before `RevPath`):

```go
	Pinned      bool      `json:"pinned,omitempty"`
	Tags        []string  `json:"tags,omitempty"`
	RevPath     string    `json:"revPath"`
```

- [ ] **Step 4: Run the test and confirm it passes**

Run: `make test`
Expected: PASS

- [ ] **Step 5: Write the failing `FromReceipts` test**

Add to `internal/manifest/bundle_test.go`:

```go
func TestFromReceiptsCarriesTagsIntoTheEntry(t *testing.T) {
	r := gitReceipt("alpha", "claude", "codex")
	r.Tags = []string{"frontend"}

	f, _ := FromReceipts([]*state.Receipt{r}, registry(t), present())
	if len(f.Skills) != 1 {
		t.Fatalf("got %d skills, want 1", len(f.Skills))
	}
	if strings.Join(f.Skills[0].Tags, ",") != "frontend" {
		t.Errorf("Tags = %v, want [frontend]", f.Skills[0].Tags)
	}
}
```

- [ ] **Step 6: Run it and confirm it fails**

Run: `make test`
Expected: FAIL — `f.Skills[0].Tags` is empty, because `entryFor` does not copy it yet.

- [ ] **Step 7: Copy `Tags` in `entryFor`**

In `internal/manifest/bundle.go`, add the field to the `Entry{}` literal `entryFor` builds:

```go
	e := Entry{
		Name:    r.Name,
		Source:  r.Source,
		Subpath: r.Subpath,
		Ref:     r.Ref,
		Pinned:  r.Pinned,
		Tags:    r.Tags,
	}
```

- [ ] **Step 8: Run the test and confirm it passes**

Run: `make test`
Expected: PASS

- [ ] **Step 9: Write the failing install-time test**

Create `internal/manifest/tags_plan_test.go`:

```go
package manifest

import (
	"context"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/state"
)

// A tag is manifest metadata with no channel-specific meaning, so it is
// stamped onto the Record op planMissing already built rather than teaching
// every channel's Install about a field install itself never sets.
func TestPlanCarriesEntryTagsIntoTheNewReceipt(t *testing.T) {
	f := newPlanFixture(t).agents(t)

	_, p := Plan(context.Background(), f.reg, File{Skills: []Entry{
		{Name: "demo", Source: f.url, Tags: []string{"frontend", "team-a"}},
	}}, &state.DB{Receipts: map[string]*state.Receipt{}}, f.cfg)

	var found bool
	for _, op := range p.Ops {
		rec, ok := op.(plan.Record)
		if !ok {
			continue
		}
		found = true
		if strings.Join(rec.Receipt.Tags, ",") != "frontend,team-a" {
			t.Errorf("receipt tags = %v, want [frontend team-a]", rec.Receipt.Tags)
		}
	}
	if !found {
		t.Fatal("no Record op in the plan")
	}
}

// sync only ever adds, so an entry's tags are stamped once, at first
// install, and never rewritten on a receipt that already exists.
func TestPlanDoesNotRewriteTagsOnAnAlreadyInstalledEntry(t *testing.T) {
	f := newPlanFixture(t).agents(t)
	existing := f.installedReceipt(t, "claude", "codex")
	existing.Tags = []string{"backend"}
	db := &state.DB{Receipts: map[string]*state.Receipt{"demo": existing}}

	rep, p := Plan(context.Background(), f.reg, File{Skills: []Entry{
		{Name: "demo", Source: f.url, Tags: []string{"frontend"}},
	}}, db, f.cfg)

	if len(rep.Verdicts) != 1 || rep.Verdicts[0].Status != StatusPresent {
		t.Fatalf("verdicts = %+v, want one present", rep.Verdicts)
	}
	if !p.IsEmpty() {
		t.Errorf("plan = %+v, want empty: tags never rewrite an existing receipt", p.Ops)
	}
}
```

- [ ] **Step 10: Run the tests and confirm the first fails, the second already passes**

Run: `make test`
Expected: `TestPlanCarriesEntryTagsIntoTheNewReceipt` FAILS (no Record op carries tags yet — `planMissing` never sets them); `TestPlanDoesNotRewriteTagsOnAnAlreadyInstalledEntry` already PASSES, because `planInstalled` never touches an existing receipt's fields today. Keep both; the second is a regression guard for the "only ever adds" invariant.

- [ ] **Step 11: Stamp tags onto the Record op in `planMissing`**

In `internal/manifest/plan.go`, after the existing `p, receipts, skips, err := ch.Install(req, chosen)` block in `planMissing` (right before `v := Verdict{...}`):

```go
	p, receipts, skips, err := ch.Install(req, chosen)
	if err != nil {
		return errorVerdict(e, err), plan.Plan{}
	}
	if len(e.Tags) > 0 {
		applyTags(&p, e.Tags)
	}
```

Add the helper near `errorVerdict` at the bottom of the file:

```go
// applyTags stamps tags onto every Record op an install just planned. Tags
// are manifest metadata with no channel-specific meaning, so this stays
// entirely inside the manifest package rather than teaching every channel
// about a field install itself never sets.
func applyTags(p *plan.Plan, tags []string) {
	for i, op := range p.Ops {
		rec, ok := op.(plan.Record)
		if !ok {
			continue
		}
		rec.Receipt.Tags = tags
		p.Ops[i] = rec
	}
}
```

- [ ] **Step 12: Run the tests and confirm both pass**

Run: `make test`
Expected: PASS

- [ ] **Step 13: Commit**

```bash
git add internal/manifest/manifest.go internal/state/state.go internal/manifest/bundle.go \
  internal/manifest/plan.go internal/manifest/manifest_test.go internal/manifest/bundle_test.go \
  internal/manifest/tags_plan_test.go
git commit -m "feat(manifest): carry tags from skills.toml into installed receipts"
```

---

### Task 2: `manifest.FetchRemote`

**Files:**
- Create: `internal/manifest/remote.go`
- Test: `internal/manifest/remote_test.go`

**Interfaces:**
- Consumes: `source.Parse(raw string) (source.Source, error)`, `source.Source.Channel`, `.RepoURL`, `.Subpath`, `.Slug()` (`internal/source/source.go`); `gitx.Git.Resolve(ctx, repoURL, ref) (string, error)` (`internal/gitx/gitx.go:21`); `store.Store.Ensure(ctx, g gitx.Git, slug, repoURL, sha string) (string, error)` (`internal/store/store.go:94`); `manifest.Decode(b []byte) (File, error)` (already in this package).
- Produces: `func FetchRemote(ctx context.Context, raw, ref string, g gitx.Git, st *store.Store) (File, error)` — consumed by Task 3's `sync` wiring.

- [ ] **Step 1: Write the failing tests**

Create `internal/manifest/remote_test.go`:

```go
package manifest

import (
	"context"
	"testing"

	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/richardcase/skillsctl/internal/store"
	"github.com/richardcase/skillsctl/internal/testrepo"
)

func TestFetchRemoteReadsSkillsTomlAtHEAD(t *testing.T) {
	url, _ := testrepo.New(t, map[string]string{
		"skills.toml": "version = 1\n\n[[skill]]\nname = 'alpha'\nsource = 'https://github.com/owner/repo.git'\n",
	})
	st := store.New(t.TempDir())

	f, err := FetchRemote(context.Background(), url, "", gitx.New(), st)
	if err != nil {
		t.Fatalf("FetchRemote: %v", err)
	}
	if len(f.Skills) != 1 || f.Skills[0].Name != "alpha" {
		t.Fatalf("got %+v, want one skill named alpha", f.Skills)
	}
}

func TestFetchRemoteAtAPinnedSha(t *testing.T) {
	url, sha1 := testrepo.New(t, map[string]string{"skills.toml": "version = 1\n"})
	dir := testrepo.Dir(url)
	testrepo.Commit(t, dir, map[string]string{
		"skills.toml": "version = 1\n\n[[skill]]\nname = 'alpha'\nsource = 'https://github.com/owner/repo.git'\n",
	})
	st := store.New(t.TempDir())

	old, err := FetchRemote(context.Background(), url, sha1, gitx.New(), st)
	if err != nil {
		t.Fatalf("FetchRemote at %s: %v", sha1, err)
	}
	if len(old.Skills) != 0 {
		t.Errorf("got %d skills at the old sha, want 0", len(old.Skills))
	}

	head, err := FetchRemote(context.Background(), url, "", gitx.New(), st)
	if err != nil {
		t.Fatalf("FetchRemote at HEAD: %v", err)
	}
	if len(head.Skills) != 1 {
		t.Errorf("got %d skills at HEAD, want 1", len(head.Skills))
	}
}

func TestFetchRemoteRefusesANonGitSource(t *testing.T) {
	st := store.New(t.TempDir())
	if _, err := FetchRemote(context.Background(), "some-plugin@marketplace", "", gitx.New(), st); err == nil {
		t.Fatal("want an error for a non-git profile source")
	}
}

func TestFetchRemoteRefusesASubpath(t *testing.T) {
	st := store.New(t.TempDir())
	if _, err := FetchRemote(context.Background(), "owner/repo/some/subpath", "", gitx.New(), st); err == nil {
		t.Fatal("want an error for a profile source naming a subpath: skills.toml is always at the root")
	}
}

func TestFetchRemoteErrorsWhenTheRepoHasNoManifest(t *testing.T) {
	url, _ := testrepo.New(t, map[string]string{"README.md": "hi\n"})
	st := store.New(t.TempDir())

	if _, err := FetchRemote(context.Background(), url, "", gitx.New(), st); err == nil {
		t.Fatal("want an error when the repository has no skills.toml at its root")
	}
}
```

- [ ] **Step 2: Run and confirm it fails to compile**

Run: `make test`
Expected: FAIL — `FetchRemote` is undefined.

- [ ] **Step 3: Implement `FetchRemote`**

Create `internal/manifest/remote.go`:

```go
package manifest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/store"
)

// FetchRemote resolves raw as a git repository at ref (empty meaning its
// HEAD) and returns the skills.toml decoded from its root.
//
// It composes source.Parse, gitx.Resolve and store.Ensure — the same three
// calls an install of a skill from raw would make — because fetching a file
// out of a resolved git tree is what those already do; a manifest fetch is a
// smaller case of the same operation, not a different one. store.Ensure's own
// cache means a second FetchRemote against an unchanged repository touches
// neither the network nor the disk beyond one ls-remote.
func FetchRemote(ctx context.Context, raw, ref string, g gitx.Git, st *store.Store) (File, error) {
	src, err := source.Parse(raw)
	if err != nil {
		return File{}, fmt.Errorf("%s: %w", raw, err)
	}
	if src.Channel != source.ChannelGit {
		return File{}, fmt.Errorf("%s: a profile source must be a git repository, not %s", raw, src.Channel)
	}
	if src.Subpath != "" {
		return File{}, fmt.Errorf("%s: names subpath %q, but skills.toml is always read from the repository root", raw, src.Subpath)
	}

	sha, err := g.Resolve(ctx, src.RepoURL, ref)
	if err != nil {
		return File{}, fmt.Errorf("resolve %s: %w", raw, err)
	}
	rev, err := st.Ensure(ctx, g, src.Slug(), src.RepoURL, sha)
	if err != nil {
		return File{}, err
	}

	path := filepath.Join(rev, "skills.toml")
	blob, err := os.ReadFile(path)
	if err != nil {
		return File{}, fmt.Errorf("read %s: %w", path, err)
	}
	f, err := Decode(blob)
	if err != nil {
		return File{}, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}
```

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `make test`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/manifest/remote.go internal/manifest/remote_test.go
git commit -m "feat(manifest): fetch skills.toml from a git-hosted profile repository"
```

---

### Task 3: Wire `sync` to accept a remote profile source

**Files:**
- Modify: `internal/cli/sync.go`
- Test: `internal/cli/sync_test.go`

**Interfaces:**
- Consumes: `manifest.FetchRemote` (Task 2), `e.store` (`internal/cli/context.go:56`, the `env.store *store.Store` field, same package), `gitx.New()` (`internal/gitx/gitx.go:59`).

- [ ] **Step 1: Write the failing CLI tests**

Add to `internal/cli/sync_test.go`:

```go
func TestSyncFromARemoteProfileRepo(t *testing.T) {
	h := newHarness(t)
	skillURL, skillSHA := testrepo.New(t, map[string]string{"SKILL.md": skillMD})
	manifestBody := "version = 1\n\n[[skill]]\nname = 'demo-skill'\nsource = '" + skillURL + "'\n"
	profileURL, _ := testrepo.New(t, map[string]string{"skills.toml": manifestBody})

	out, err := h.run(t, "sync", profileURL)
	if err != nil {
		t.Fatalf("sync %s: %v\n%s", profileURL, err, out)
	}

	for _, dir := range []string{h.claude, h.codex} {
		dest, rerr := os.Readlink(filepath.Join(dir, "demo-skill"))
		if rerr != nil {
			t.Fatalf("link missing after remote sync: %v", rerr)
		}
		if !strings.Contains(dest, skillSHA) {
			t.Errorf("link points at %q, want the fetched sha %s", dest, skillSHA)
		}
	}
}

func TestSyncRefSelectsTheProfileReposBranch(t *testing.T) {
	h := newHarness(t)
	profileURL, sha1 := testrepo.New(t, map[string]string{"skills.toml": "version = 1\n"})
	dir := testrepo.Dir(profileURL)
	skillURL, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})
	testrepo.Commit(t, dir, map[string]string{
		"skills.toml": "version = 1\n\n[[skill]]\nname = 'demo-skill'\nsource = '" + skillURL + "'\n",
	})

	out, err := h.run(t, "sync", profileURL, "--ref", sha1)
	if err != nil {
		t.Fatalf("sync --ref %s: %v\n%s", sha1, err, out)
	}
	if !strings.Contains(out, "Everything the manifest names is already installed.") {
		t.Errorf("syncing the old sha should have found an empty manifest, got:\n%s", out)
	}
}

// A relative filename that also happens to look like an owner/repo source
// still reads as a local file: os.Stat is tried first and always wins.
func TestSyncPrefersALocalFileEvenWhenItsNameLooksLikeASource(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll(filepath.Join(dir, "owner"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "owner", "repo"), []byte("version = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := h.run(t, "sync", "owner/repo")
	if err != nil {
		t.Fatalf("sync owner/repo: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Everything the manifest names is already installed.") {
		t.Errorf("want the local (empty) manifest read, not a remote fetch attempted, got:\n%s", out)
	}
}
```

Add `"os"`, `"path/filepath"`, `"strings"` to the existing import block in `internal/cli/sync_test.go` if not already present (`os` and `path/filepath` already are; `strings` needs adding).

- [ ] **Step 2: Run and confirm they fail**

Run: `make test`
Expected: `TestSyncFromARemoteProfileRepo` and `TestSyncRefSelectsTheProfileReposBranch` FAIL — `sync` has no `--ref` flag and `os.ReadFile(args[0])` errors on a URL. `TestSyncPrefersALocalFileEvenWhenItsNameLooksLikeASource` already PASSES against the current code (a plain local read), which is fine — it becomes a regression guard once the dispatch changes.

- [ ] **Step 3: Rewrite the manifest-reading half of `sync`**

In `internal/cli/sync.go`, replace:

```go
		RunE: func(cmd *cobra.Command, args []string) error {
			blob, err := os.ReadFile(args[0])
			if err != nil {
				return fmt.Errorf("read %s: %w", args[0], err)
			}
			f, err := manifest.Decode(blob)
			if err != nil {
				return fmt.Errorf("%s: %w", args[0], err)
			}

			e, err := newEnv()
			if err != nil {
				return err
			}
```

with:

```go
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := newEnv()
			if err != nil {
				return err
			}

			var f manifest.File
			if _, statErr := os.Stat(args[0]); statErr == nil {
				blob, rerr := os.ReadFile(args[0])
				if rerr != nil {
					return fmt.Errorf("read %s: %w", args[0], rerr)
				}
				f, err = manifest.Decode(blob)
				if err != nil {
					return fmt.Errorf("%s: %w", args[0], err)
				}
			} else {
				f, err = manifest.FetchRemote(cmd.Context(), args[0], ref, gitx.New(), e.store)
				if err != nil {
					return err
				}
			}
```

Add the `ref` flag variable next to `dryRun`:

```go
func newSyncCmd() *cobra.Command {
	var dryRun bool
	var ref string
```

and register it beside the existing flag:

```go
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change without changing it")
	cmd.Flags().StringVar(&ref, "ref", "", "branch, tag or sha the profile repository tracks (default: its HEAD); ignored for a local file")
	return cmd
```

Add `"github.com/richardcase/skillsctl/internal/gitx"` to the import block.

Update `Use` and `Long` to document the new form:

```go
		Use:   "sync <file-or-source>",
		Short: "Install the skills a skills.toml names",
		Long: "Read a manifest and add what this machine is missing: skills it does not have,\n" +
			"and links into the agents an entry names.\n\n" +
			"<file-or-source> is a local path if one exists there, and otherwise a git\n" +
			"source (owner/repo, a git URL, or scp-form) whose repository root holds\n" +
			"skills.toml — the same shapes install already accepts for a skill, minus\n" +
			"plugin and OCI sources, which name no file to read. --ref chooses that\n" +
			"repository's branch, tag or sha and is ignored for a local file.\n\n" +
			"sync only ever adds. It never re-points a ref, never moves a pin and never\n" +
			"removes a skill, so running it twice changes nothing the second time. A\n" +
			"difference between the manifest and an install is reported rather than\n" +
			"resolved, and so is a skill installed here that the manifest never mentions.",
```

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `make test`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/cli/sync.go internal/cli/sync_test.go
git commit -m "feat(cli): sync against a git-hosted profile repo, not just a local file"
```

---

### Task 4: `list --tag`

**Files:**
- Modify: `internal/cli/list.go`
- Test: `internal/cli/cli_test.go`

**Interfaces:**
- Produces: `filterByTags(receipts []*state.Receipt, tags []string) []*state.Receipt` and `hasAnyTag(r *state.Receipt, tags []string) bool` in `internal/cli/list.go` — Task 5 (`bundle --tag`) reuses `filterByTags`.

- [ ] **Step 1: Write the failing test**

Add to `internal/cli/cli_test.go`, near `TestListFilterByChannel`:

```go
func TestListFilterByTag(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{
		"a/SKILL.md": "---\nname: a\ndescription: A\n---\n",
		"b/SKILL.md": "---\nname: b\ndescription: B\n---\n",
	})
	if out, err := h.run(t, "sync", writeManifest(t,
		"version = 1\n\n"+
			"[[skill]]\nname = 'a'\nsource = '"+url+"'\nsubpath = 'a'\ntags = ['frontend']\n\n"+
			"[[skill]]\nname = 'b'\nsource = '"+url+"'\nsubpath = 'b'\ntags = ['backend']\n")); err != nil {
		t.Fatalf("sync: %v\n%s", err, out)
	}

	out, err := h.run(t, "list", "--tag", "frontend")
	if err != nil {
		t.Fatalf("list --tag frontend: %v\n%s", err, out)
	}
	if !strings.Contains(out, "a") || strings.Contains(out, "\nb\t") {
		t.Errorf("--tag frontend should show only a, got:\n%s", out)
	}
}
```

Add `"strings"` to `internal/cli/cli_test.go`'s imports if not already present (it is, for other tests in the file — verify before editing).

- [ ] **Step 2: Run and confirm it fails**

Run: `make test`
Expected: FAIL — `list` has no `--tag` flag (cobra reports "unknown flag").

- [ ] **Step 3: Add the flag, the filter, and the column**

In `internal/cli/list.go`, add the flag variable:

```go
	var asJSON bool
	var includeChannel, excludeChannel []string
	var tags []string
```

After the existing channel-filter `switch` block, filter by tag unconditionally (the helper is a no-op when `tags` is empty):

```go
			receipts = filterByTags(receipts, tags)
```

Register the flag beside the others:

```go
	cmd.Flags().StringSliceVar(&tags, "tag", nil, "only list skills carrying any of these tags (repeatable)")
```

Add the column to the table header and each row:

```go
			_, _ = fmt.Fprintln(w, "NAME\tCHANNEL\tVERSION\tAGENTS\tTAGS")
			for _, r := range receipts {
				agents := reg.Agents(r)
				version := shortSha(r.Resolved)
				if version == "" {
					version = "-"
				}
				if r.Pinned {
					version += " (pinned)"
				}
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", r.Name, r.Channel, version, strings.Join(agents, ","), strings.Join(r.Tags, ","))
			}
```

Add the two helpers near `filterReceipts`:

```go
// filterByTags keeps receipts carrying at least one of tags, preserving
// order. An empty tags is a no-op, so a caller can call this unconditionally.
func filterByTags(receipts []*state.Receipt, tags []string) []*state.Receipt {
	if len(tags) == 0 {
		return receipts
	}
	kept := receipts[:0:0]
	for _, r := range receipts {
		if hasAnyTag(r, tags) {
			kept = append(kept, r)
		}
	}
	return kept
}

// hasAnyTag reports whether r carries at least one of tags — OR semantics,
// the same rule --include-channel applies to a set of channels.
func hasAnyTag(r *state.Receipt, tags []string) bool {
	for _, have := range r.Tags {
		for _, want := range tags {
			if have == want {
				return true
			}
		}
	}
	return false
}
```

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `make test`
Expected: PASS. Also re-run `TestListJSON` and `TestListFilterByChannel` explicitly to confirm the new column and field don't break their assertions: `go test ./internal/cli/... -run TestList -v` (via `make test`'s underlying `go test`, or targeted with `mise exec -- go test ./internal/cli/... -run TestList -v` if `make test` output is too broad to scan).

- [ ] **Step 5: Commit**

```bash
git add internal/cli/list.go internal/cli/cli_test.go
git commit -m "feat(cli): filter list by tag, and show tags in its output"
```

---

### Task 5: `bundle --tag`

**Files:**
- Modify: `internal/cli/bundle.go`
- Test: `internal/cli/cli_test.go`

**Interfaces:**
- Consumes: `filterByTags` (Task 4, same package).

- [ ] **Step 1: Write the failing test**

Add to `internal/cli/cli_test.go`:

```go
func TestBundleFilterByTag(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{
		"a/SKILL.md": "---\nname: a\ndescription: A\n---\n",
		"b/SKILL.md": "---\nname: b\ndescription: B\n---\n",
	})
	if out, err := h.run(t, "sync", writeManifest(t,
		"version = 1\n\n"+
			"[[skill]]\nname = 'a'\nsource = '"+url+"'\nsubpath = 'a'\ntags = ['frontend']\n\n"+
			"[[skill]]\nname = 'b'\nsource = '"+url+"'\nsubpath = 'b'\ntags = ['backend']\n")); err != nil {
		t.Fatalf("sync: %v\n%s", err, out)
	}

	stdout, _, err := h.runSplit(t, "bundle", "--tag", "frontend")
	if err != nil {
		t.Fatalf("bundle --tag frontend: %v", err)
	}
	if !strings.Contains(stdout, "name = 'a'") || strings.Contains(stdout, "name = 'b'") {
		t.Errorf("bundle --tag frontend should emit only a, got:\n%s", stdout)
	}
}
```

- [ ] **Step 2: Run and confirm it fails**

Run: `make test`
Expected: FAIL — `bundle` has no `--tag` flag.

- [ ] **Step 3: Add the flag and filter the receipts before projecting**

In `internal/cli/bundle.go`:

```go
func newBundleCmd() *cobra.Command {
	var tags []string

	cmd := &cobra.Command{
		Use:   "bundle",
		Short: "Write the installed skills as a portable skills.toml",
		Long: "Project the current receipts into the skills.toml manifest format and write it\n" +
			"to stdout, so that `skillsctl bundle > skills.toml` on one machine and\n" +
			"`skillsctl sync skills.toml` on another install the same set.\n\n" +
			"--tag keeps only receipts carrying at least one of the given tags, for\n" +
			"writing a scoped manifest out of a larger set.\n\n" +
			"A local skill is left out and named on stderr: its source is a path on this\n" +
			"machine, which means nothing on another.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := newEnv()
			if err != nil {
				return err
			}
			h, err := e.openState()
			if err != nil {
				return err
			}
			defer func() { _ = h.Close() }()

			receipts := filterByTags(h.DB.List(), tags)
			f, excluded := manifest.FromReceipts(receipts, e.channels(), e.cfg.Present())

			if err := manifest.Encode(cmd.OutOrStdout(), f); err != nil {
				return err
			}

			if len(excluded) > 0 {
				cmd.PrintErrf("warning: %s left out of the manifest: %s\n",
					count(len(excluded), "local skill"), strings.Join(excluded, ", "))
			}
			return nil
		},
	}

	cmd.Flags().StringSliceVar(&tags, "tag", nil, "only bundle skills carrying any of these tags (repeatable)")
	return cmd
}
```

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `make test`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/cli/bundle.go internal/cli/cli_test.go
git commit -m "feat(cli): filter bundle's output by tag"
```

---

### Task 6: README

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Update the Commands table**

In the `## Commands` table, change the `sync` row and add `--tag` to `list` and `bundle`:

```
| `list` | `--json`, `--include-channel`, `--exclude-channel`, `--tag` | Show installed skills, versions and agents |
```

```
| `bundle` | `--tag` | Write the installed skills as a portable `skills.toml` |
| `sync <file-or-source>` | `--ref`, `--dry-run` | Install the skills a manifest names, from a local file or a git-hosted profile repo, and report the rest |
```

- [ ] **Step 2: Add examples to the `## Use` section**

After the existing `sync` lines:

```
skillsctl bundle > skills.toml                     # write what's installed as a manifest
skillsctl bundle --tag frontend > frontend.toml    # ...just the skills tagged frontend
skillsctl sync skills.toml                         # install what it names, and report the rest
skillsctl sync skills.toml --dry-run               # show what would change
skillsctl sync team/skills-profile                 # sync from a git-hosted skills.toml
skillsctl sync team/skills-profile --ref develop   # ...at a specific branch
skillsctl list --tag frontend                      # only skills tagged frontend
```

- [ ] **Step 3: Document `tags` and remote sync in the `## skills.toml` section**

After the existing bullet list (the one ending "...left out of a bundle and named on stderr..."), add:

```
- `tags` groups skills for `list --tag`/`bundle --tag` to filter by. A tag is
  any string; skillsctl imposes no vocabulary. Tags are set from the manifest
  only when `sync` installs a skill for the first time — like `agents`, they
  are metadata rather than identity, so re-syncing a manifest with different
  tags for an already-installed skill changes nothing.
```

Before the `## Status` section, add a new subsection:

```
### Syncing against a remote profile repo

`<file-or-source>` is a local path if one exists there, and otherwise a git
source — `owner/repo`, a full URL, or scp-form, the same shapes `install`
accepts minus plugin and OCI, which name no file to read. `skills.toml` is
always read from that repository's root:

```
$ skillsctl sync team/skills-profile
$ skillsctl sync git@github.com:team/skills-profile.git --ref develop
```

`--ref` chooses the profile repository's branch, tag or sha (default: its
HEAD) and is ignored when the argument is a local file. A profile repo that
also hosts the skills it lists shares its mirror and revision cache with
them, so a second sync against an unchanged profile touches neither the
network nor the disk beyond one `ls-remote`.
```

- [ ] **Step 4: Verify the README against the code**

Run: `skillsctl sync --help`, `skillsctl list --help`, `skillsctl bundle --help` (via `go run ./cmd/skillsctl <cmd> --help` if a built binary isn't handy) and confirm every flag and default named in the README matches. Confirm the table of contents still lists the right anchors (`## skills.toml` gained a subsection heading; verify no anchor text needs updating in `## Table of Contents` since GitHub-style anchors for `###` headings aren't listed there today — check before assuming).

- [ ] **Step 5: Run the full definition-of-done and commit**

```bash
make test && make lint && make tidy-check
git add README.md
git commit -m "docs: document remote profile sync and tags"
```

---

## Verification

After all six tasks:

1. `make test && make lint && make tidy-check` — must all pass (mirrors CI).
2. Manual end-to-end check with a real (or `file://`) pair of repos:
   ```bash
   go build -o /tmp/skillsctl ./cmd/skillsctl
   # in a scratch HOME:
   /tmp/skillsctl sync <a git URL to a repo whose root has skills.toml>
   /tmp/skillsctl sync <same URL> --ref <a branch/tag/sha>
   /tmp/skillsctl list --tag <a tag present in that manifest>
   /tmp/skillsctl bundle --tag <that tag>
   ```
3. Confirm `skillsctl sync <existing-local-file>` is byte-for-byte unchanged in behavior (Task 3's stat-first test already covers this, but a manual `sync ./skills.toml` against a real store is worth one run).
