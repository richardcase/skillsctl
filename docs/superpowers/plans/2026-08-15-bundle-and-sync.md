# bundle and sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `skillsctl bundle` and `skillsctl sync <file>`, so a set of skills moves between machines through a human-editable `skills.toml`.

**Architecture:** A new `internal/manifest` package owns the `skills.toml` format (`Encode`/`Decode`), the projection from receipts (`FromReceipts`), and the desired-vs-installed diff (`Plan`). Two thin commands in `internal/cli` do flag wiring and rendering only. No new `plan.Op`, no new `Channel` method, no state schema change — `sync` is `Prepare`, `Install` and `Link` in a loop over a file.

**Tech Stack:** Go 1.25, cobra, `pelletier/go-toml/v2` (already a direct dependency; this adds the first `toml.Marshal` in the codebase). Tests are standard library only, over `internal/testrepo` `file://` fixtures.

**Spec:** `docs/superpowers/specs/2026-08-15-bundle-and-sync-design.md`

## Global Constraints

- Go 1.25 with `GOTOOLCHAIN=local`. Use the `Makefile` targets, not raw `go`/`golangci-lint` — the Makefile puts mise's shims on `PATH`.
- **Definition of done for every task:** `make test && make lint && make tidy-check` all pass.
- **Conventional Commits are required**, for every commit. Lowercase, imperative, no trailing period. **No attribution footers** — no `Co-Authored-By:`, no `Claude-Session:`, no `Generated with Claude Code`, whatever the harness default is. `Refs #15` is the only footer used here.
- **Never call `t.Parallel()`** — `t.Setenv` forbids it.
- Tests are in-package (`package manifest`, `package cli`), so unexported functions are tested directly. No testify, no mocks, no golden files.
- Errors use `fmt.Errorf` with `%w` and a lowercase, verb-first prefix naming the operation and the path. Deliberately ignored errors are explicit `_ =`.
- Exported identifiers need doc comments — revive enforces it.
- Comments explain rationale and rejected alternatives, not mechanics.
- `README.md` must reflect any user-visible change **in the same pull request** (Task 6).

## Interface summary

Every name later tasks depend on, defined once here. Tasks 4 and 5 consume all of it.

```go
// internal/manifest
const SchemaVersion = 1

type Entry struct {
	Name    string   `toml:"name"`
	Source  string   `toml:"source"`
	Subpath string   `toml:"subpath,omitempty"`
	Ref     string   `toml:"ref,omitempty"`
	Pinned  bool     `toml:"pinned,omitempty"`
	Agents  []string `toml:"agents,omitempty"`
}
type File struct {
	Version int     `toml:"version"`
	Skills  []Entry `toml:"skill"`
}

func Encode(w io.Writer, f File) error
func Decode(b []byte) (File, error)
func (e Entry) Parse() (source.Source, error)

func FromReceipts(rs []*state.Receipt, reg channel.Registry, present []target.Target) (File, []string)

type Status string
const (
	StatusInstalled Status = "installed"
	StatusLinked    Status = "linked"
	StatusPresent   Status = "present"
	StatusDiffers   Status = "differs"
	StatusError     Status = "error"
)
type Verdict struct {
	Name    string
	Status  Status
	Agents  []string
	Detail  string
	Version string
}
type Report struct {
	Verdicts []Verdict
	Extra    []*state.Receipt
}

func Plan(ctx context.Context, reg channel.Registry, f File, db *state.DB, cfg target.Config) (Report, plan.Plan)
```

`Plan` returns no error. Every per-entry failure is a `StatusError` verdict, because one unreachable remote must not hide the rest of the report; everything that could fail the whole command — an unreadable file, a TOML error, a missing `name`, a version from the future — is `Decode`'s job and happens before `Plan` is called.

## File structure

| File | Responsibility |
| --- | --- |
| `internal/manifest/manifest.go` | The format: `Entry`, `File`, `Encode`, `Decode`, entry validation, `Entry.Parse` |
| `internal/manifest/bundle.go` | `FromReceipts` — the receipts-to-manifest projection |
| `internal/manifest/plan.go` | `Status`, `Verdict`, `Report`, `Plan` — the desired-vs-installed diff |
| `internal/manifest/manifest_test.go` | Format tests: round trip, omitted fields, every rejection |
| `internal/manifest/bundle_test.go` | Projection tests: pins, agents, local exclusion, ordering |
| `internal/manifest/plan_test.go` | Diff tests over real `testrepo` fixtures |
| `internal/cli/bundle.go` | `newBundleCmd` |
| `internal/cli/sync.go` | `newSyncCmd`, `settleSynced`, `reportSync`, `syncLine`, `syncExit` |
| `internal/cli/bundle_test.go` | stdout/stderr split, empty store, local warning |
| `internal/cli/sync_test.go` | Round trip, idempotence, drift, extras, exit codes, dry run |
| `internal/cli/root.go` | Register both commands, alphabetically |
| `README.md` | Commands table, Features, `skills.toml` section, Status |

---

### Task 1: The manifest format

**Files:**
- Create: `internal/manifest/manifest.go`
- Test: `internal/manifest/manifest_test.go`

**Interfaces:**
- Consumes: `internal/source` (`source.Parse`, `source.SubpathSep`), `internal/target` (`target.ValidateSkillName`), `github.com/pelletier/go-toml/v2`.
- Produces: `SchemaVersion`, `Entry`, `File`, `Encode`, `Decode`, `Entry.Parse`.

- [ ] **Step 1: Write the failing tests**

Create `internal/manifest/manifest_test.go`:

```go
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
			name: "unparseable source",
			toml: "[[skill]]\nname = 'alpha'\nsource = ''\n",
			want: "has no source",
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `make test`
Expected: FAIL — `internal/manifest` does not exist, so the package will not build.

- [ ] **Step 3: Write the implementation**

Create `internal/manifest/manifest.go`:

```go
// Package manifest is the skills.toml format: the portable projection of a
// receipt set that bundle writes and sync reads.
//
// A receipt is the private record of an install, full of absolute paths and
// content hashes. A manifest is what survives being carried to another machine:
// which skill, from where, at which revision, for which agents. Everything else
// a receipt holds is either machine-local or derivable from those.
package manifest

import (
	"fmt"
	"io"

	"github.com/pelletier/go-toml/v2"
	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/target"
)

// SchemaVersion is the manifest format version. Bump it only for a breaking
// change, and add a migration when you do.
const SchemaVersion = 1

// Entry is one skill a manifest names.
//
// Ref carries a branch or tag for a skill that tracks one, and the frozen sha
// for a pinned skill: install --pin records no ref, so the sha is the only
// place a pin's revision lives, and one field answering "which revision" is
// what makes syncing a pinned entry exactly `install --ref <sha> --pin`.
//
// Agents is empty when the skill is in every agent present on the machine,
// which is what an omitted -a means to install. Naming them is for a choice
// that was narrower than the default.
type Entry struct {
	Name    string   `toml:"name"`
	Source  string   `toml:"source"`
	Subpath string   `toml:"subpath,omitempty"`
	Ref     string   `toml:"ref,omitempty"`
	Pinned  bool     `toml:"pinned,omitempty"`
	Agents  []string `toml:"agents,omitempty"`
}

// File is a whole skills.toml.
type File struct {
	Version int     `toml:"version"`
	Skills  []Entry `toml:"skill"`
}

// Encode writes f as TOML, supplying the version so that no manifest this
// build produces is missing one.
func Encode(w io.Writer, f File) error {
	if f.Version == 0 {
		f.Version = SchemaVersion
	}
	blob, err := toml.Marshal(f)
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	if _, err := w.Write(blob); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}

// Decode parses a manifest and checks that every entry names one installable
// skill. A manifest is read with nobody standing by to answer a question, so an
// entry that could be read two ways is refused here rather than guessed at
// later.
func Decode(b []byte) (File, error) {
	var f File
	if err := toml.Unmarshal(b, &f); err != nil {
		return File{}, fmt.Errorf("parse manifest: %w", err)
	}

	switch {
	case f.Version == 0:
		// No version field: a hand-written file, or one from a build that
		// predates the field.
		f.Version = SchemaVersion
	case f.Version < 0:
		return File{}, fmt.Errorf("manifest version %d is not a version", f.Version)
	case f.Version > SchemaVersion:
		return File{}, fmt.Errorf("this manifest is version %d and this build understands %d: upgrade skillsctl",
			f.Version, SchemaVersion)
	}

	seen := make(map[string]bool, len(f.Skills))
	for i, e := range f.Skills {
		if err := e.validate(); err != nil {
			return File{}, fmt.Errorf("skill %d: %w", i+1, err)
		}
		if seen[e.Name] {
			return File{}, fmt.Errorf("skill %d: %q is named twice, and one name is one install", i+1, e.Name)
		}
		seen[e.Name] = true
	}
	return f, nil
}

// Parse turns an entry into the source an install would be given.
//
// The subpath has two spellings — its own field, which is what bundle writes,
// and inside the source string, which is what somebody would type at the
// command line. Folding the field in only when the source names none is what
// keeps them from being concatenated into a path that is neither.
func (e Entry) Parse() (source.Source, error) {
	bare, err := source.Parse(e.Source)
	if err != nil {
		return source.Source{}, err
	}
	if e.Subpath == "" || bare.Subpath != "" {
		return bare, nil
	}
	return source.Parse(e.Source + source.SubpathSep + e.Subpath)
}

// validate refuses an entry sync could not act on unambiguously.
func (e Entry) validate() error {
	if e.Name == "" {
		return fmt.Errorf("has no name: an entry names its skill, because that name is what sync compares against what is installed")
	}
	// The name becomes a receipt key and a path segment in an agent's skills
	// directory, and a manifest is third-party data like any other.
	if err := target.ValidateSkillName(e.Name); err != nil {
		return err
	}
	if e.Source == "" {
		return fmt.Errorf("%q has no source", e.Name)
	}

	bare, err := source.Parse(e.Source)
	if err != nil {
		return fmt.Errorf("%q: %w", e.Name, err)
	}
	if e.Subpath != "" && bare.Subpath != "" && bare.Subpath != e.Subpath {
		return fmt.Errorf("%q names subpath %q in its source and %q in its subpath field: they must agree",
			e.Name, bare.Subpath, e.Subpath)
	}
	if _, err := e.Parse(); err != nil {
		return fmt.Errorf("%q: %w", e.Name, err)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `make test`
Expected: PASS.

If `TestDecodeRejections/escaping_name` fails because `target.ValidateSkillName`'s message does not contain "escaped", read the actual message and change the test's `want` to a substring the real error contains — do not weaken the validation.

- [ ] **Step 5: Lint and commit**

```bash
make lint && make tidy-check
git add internal/manifest/manifest.go internal/manifest/manifest_test.go
git commit -m "feat(manifest): the skills.toml format

Refs #15"
```

---

### Task 2: Project receipts into a manifest

**Files:**
- Create: `internal/manifest/bundle.go`
- Test: `internal/manifest/bundle_test.go`

**Interfaces:**
- Consumes: `Entry`, `File`, `SchemaVersion` from Task 1; `channel.Registry.Agents`, `state.Receipt`, `target.Target`.
- Produces: `FromReceipts(rs []*state.Receipt, reg channel.Registry, present []target.Target) (File, []string)` — the manifest, and one `"name (source)"` string per local skill left out.

- [ ] **Step 1: Write the failing tests**

Create `internal/manifest/bundle_test.go`:

```go
package manifest

import (
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/channel"
	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/store"
	"github.com/richardcase/skillsctl/internal/target"
)

// registry builds a real registry. Nothing here reaches the network: the only
// method these tests call is Agents, which reads the receipt.
func registry(t *testing.T) channel.Registry {
	t.Helper()
	st := store.New(t.TempDir())
	return channel.Registry{
		Git:   channel.NewGit(st, gitx.New()),
		Local: channel.NewLocal(st),
	}
}

func present() []target.Target {
	return []target.Target{{Name: "claude"}, {Name: "codex"}}
}

func gitReceipt(name string, targets ...string) *state.Receipt {
	r := &state.Receipt{
		Name:     name,
		Channel:  "git",
		Source:   "https://github.com/owner/repo.git",
		Ref:      "main",
		Resolved: "9f8e7d6c5b4a39281706f5e4d3c2b1a098765432",
	}
	for _, tn := range targets {
		r.Links = append(r.Links, state.Link{Target: tn, Path: "/agents/" + tn + "/" + name})
	}
	return r
}

func TestFromReceiptsOmitsAgentsWhenEveryPresentAgentHasIt(t *testing.T) {
	f, excluded := FromReceipts([]*state.Receipt{gitReceipt("alpha", "claude", "codex")}, registry(t), present())

	if len(excluded) != 0 {
		t.Errorf("nothing should have been excluded, got %v", excluded)
	}
	if len(f.Skills) != 1 {
		t.Fatalf("got %d skills, want 1", len(f.Skills))
	}
	if f.Skills[0].Agents != nil {
		t.Errorf("agents = %v, want it omitted when it repeats the default", f.Skills[0].Agents)
	}
	if f.Skills[0].Ref != "main" {
		t.Errorf("ref = %q, want main", f.Skills[0].Ref)
	}
}

func TestFromReceiptsKeepsANarrowerAgentSet(t *testing.T) {
	f, _ := FromReceipts([]*state.Receipt{gitReceipt("alpha", "claude")}, registry(t), present())

	if len(f.Skills) != 1 || strings.Join(f.Skills[0].Agents, ",") != "claude" {
		t.Errorf("agents = %v, want [claude] preserved as a deliberate choice", f.Skills[0].Agents)
	}
}

// A pinned receipt records no ref, so the sha is the only thing that can carry
// the pin to another machine.
func TestFromReceiptsPutsThePinnedShaInRef(t *testing.T) {
	r := gitReceipt("alpha", "claude", "codex")
	r.Pinned = true
	r.Ref = ""

	f, _ := FromReceipts([]*state.Receipt{r}, registry(t), present())

	if len(f.Skills) != 1 {
		t.Fatalf("got %d skills, want 1", len(f.Skills))
	}
	if !f.Skills[0].Pinned {
		t.Error("the pin was lost")
	}
	if f.Skills[0].Ref != r.Resolved {
		t.Errorf("ref = %q, want the resolved sha %q", f.Skills[0].Ref, r.Resolved)
	}
}

func TestFromReceiptsExcludesLocalSkills(t *testing.T) {
	local := &state.Receipt{
		Name:    "mine",
		Channel: "local",
		Source:  "/Users/me/code/mine",
		Links:   []state.Link{{Target: "claude", Path: "/agents/claude/mine"}},
	}

	f, excluded := FromReceipts([]*state.Receipt{local, gitReceipt("alpha", "claude", "codex")}, registry(t), present())

	if len(f.Skills) != 1 || f.Skills[0].Name != "alpha" {
		t.Errorf("skills = %+v, want only the git skill", f.Skills)
	}
	if len(excluded) != 1 || !strings.Contains(excluded[0], "mine") || !strings.Contains(excluded[0], "/Users/me/code/mine") {
		t.Errorf("excluded = %v, want the local skill named with its path", excluded)
	}
}

// A committed skills.toml has to produce a stable diff.
func TestFromReceiptsSortsByName(t *testing.T) {
	f, _ := FromReceipts([]*state.Receipt{
		gitReceipt("gamma", "claude", "codex"),
		gitReceipt("alpha", "claude", "codex"),
		gitReceipt("beta", "claude", "codex"),
	}, registry(t), present())

	var got []string
	for _, s := range f.Skills {
		got = append(got, s.Name)
	}
	if strings.Join(got, ",") != "alpha,beta,gamma" {
		t.Errorf("order = %v, want sorted by name", got)
	}
}

// A plugin's skills reach its agent without a symlink of ours, so which agents
// have it was never a choice the user made.
func TestFromReceiptsGivesAPluginNoAgents(t *testing.T) {
	p := &state.Receipt{Name: "some-plugin", Channel: "plugin", Source: "some-plugin@marketplace", Resolved: "1.2.0"}

	f, excluded := FromReceipts([]*state.Receipt{p}, registry(t), present())

	if len(excluded) != 0 {
		t.Errorf("a plugin is portable and must not be excluded, got %v", excluded)
	}
	if len(f.Skills) != 1 {
		t.Fatalf("got %d skills, want 1", len(f.Skills))
	}
	if f.Skills[0].Agents != nil {
		t.Errorf("agents = %v, want none for a plugin", f.Skills[0].Agents)
	}
	if f.Skills[0].Source != "some-plugin@marketplace" {
		t.Errorf("source = %q, want the plugin id", f.Skills[0].Source)
	}
}

func TestFromReceiptsCarriesTheSubpath(t *testing.T) {
	r := gitReceipt("alpha", "claude", "codex")
	r.Subpath = "skills/alpha"

	f, _ := FromReceipts([]*state.Receipt{r}, registry(t), present())

	if len(f.Skills) != 1 || f.Skills[0].Subpath != "skills/alpha" {
		t.Errorf("subpath = %q, want it carried", f.Skills[0].Subpath)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `make test`
Expected: FAIL — `undefined: FromReceipts`.

- [ ] **Step 3: Write the implementation**

Create `internal/manifest/bundle.go`:

```go
package manifest

import (
	"fmt"
	"sort"

	"github.com/richardcase/skillsctl/internal/channel"
	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/target"
)

// FromReceipts projects receipts into a manifest, returning the local skills it
// left out, each as "name (source)".
//
// A local skill's source is an absolute path on this machine and means nothing
// on another, so it is left out rather than written down knowing it is wrong for
// the file's only purpose. The caller names them: a silent drop is how somebody
// finds out on the new machine that something they rely on was never in the
// file.
//
// present is the agent set an install with no -a would have chosen, which is
// exactly what an omitted agents field means.
func FromReceipts(rs []*state.Receipt, reg channel.Registry, present []target.Target) (File, []string) {
	f := File{Version: SchemaVersion}
	var excluded []string

	for _, r := range rs {
		if r.Channel == string(source.ChannelLocal) {
			excluded = append(excluded, fmt.Sprintf("%s (%s)", r.Name, r.Source))
			continue
		}
		f.Skills = append(f.Skills, entryFor(r, reg, present))
	}

	sort.Slice(f.Skills, func(i, j int) bool { return f.Skills[i].Name < f.Skills[j].Name })
	return f, excluded
}

// entryFor projects one receipt.
func entryFor(r *state.Receipt, reg channel.Registry, present []target.Target) Entry {
	e := Entry{
		Name:    r.Name,
		Source:  r.Source,
		Subpath: r.Subpath,
		Ref:     r.Ref,
		Pinned:  r.Pinned,
	}
	// A pinned receipt records no ref, so its revision lives only in Resolved.
	// Dropping it would carry the pin across as a freeze at whatever HEAD the
	// other machine happens to see, which is not the same install.
	if r.Pinned {
		e.Ref = r.Resolved
	}
	// A receipt with no links is one whose agent installed the files itself, so
	// which agents have it was never a choice to preserve.
	if len(r.Links) > 0 {
		e.Agents = narrowerThan(reg.Agents(r), present)
	}
	return e
}

// narrowerThan returns agents when it is a deliberate subset of the present
// set, and nil when it covers all of it. An omitted field means install's own
// default, so emitting one that merely repeats the default would tie a manifest
// to the machine that produced it.
func narrowerThan(agents []string, present []target.Target) []string {
	have := make(map[string]bool, len(agents))
	for _, a := range agents {
		have[a] = true
	}
	for _, t := range present {
		if !have[t.Name] {
			return agents
		}
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `make test`
Expected: PASS.

- [ ] **Step 5: Lint and commit**

```bash
make lint && make tidy-check
git add internal/manifest/bundle.go internal/manifest/bundle_test.go
git commit -m "feat(manifest): project receipts into a manifest

Refs #15"
```

---

### Task 3: `skillsctl bundle`

**Files:**
- Create: `internal/cli/bundle.go`
- Modify: `internal/cli/root.go` (add `newBundleCmd()` to the `AddCommand` list, alphabetically first)
- Test: `internal/cli/bundle_test.go`

**Interfaces:**
- Consumes: `manifest.FromReceipts`, `manifest.Encode` from Tasks 1–2; the existing `newEnv`, `env.openState`, `env.targets`, `env.channels`, and `count` from `internal/cli/outdated.go`.
- Produces: `newBundleCmd() *cobra.Command`.

- [ ] **Step 1: Write the failing tests**

Create `internal/cli/bundle_test.go`:

```go
package cli

import (
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/manifest"
	"github.com/richardcase/skillsctl/internal/testrepo"
)

// `bundle > skills.toml` has to capture the manifest and nothing else.
func TestBundleWritesTheManifestToStdout(t *testing.T) {
	h := newHarness(t)
	url, sha := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	if out, err := h.run(t, "install", url); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	stdout, stderr, err := h.runSplit(t, "bundle")
	if err != nil {
		t.Fatalf("bundle: %v\n%s", err, stderr)
	}
	if stderr != "" {
		t.Errorf("bundle wrote to stderr with nothing to warn about:\n%s", stderr)
	}

	f, derr := manifest.Decode([]byte(stdout))
	if derr != nil {
		t.Fatalf("bundle did not emit a decodable manifest: %v\n%s", derr, stdout)
	}
	if len(f.Skills) != 1 || f.Skills[0].Name != "demo-skill" {
		t.Fatalf("skills = %+v, want demo-skill", f.Skills)
	}
	// Installed into every present agent, so the field carries no choice.
	if f.Skills[0].Agents != nil {
		t.Errorf("agents = %v, want it omitted", f.Skills[0].Agents)
	}
	if f.Skills[0].Pinned {
		t.Error("the skill was not pinned")
	}
	if strings.Contains(stdout, sha) {
		t.Errorf("an unpinned entry should track its ref, not freeze the sha:\n%s", stdout)
	}
}

func TestBundleCarriesAPin(t *testing.T) {
	h := newHarness(t)
	url, sha := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	if out, err := h.run(t, "install", url, "--pin"); err != nil {
		t.Fatalf("install --pin: %v\n%s", err, out)
	}

	stdout, _, err := h.runSplit(t, "bundle")
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	f, derr := manifest.Decode([]byte(stdout))
	if derr != nil {
		t.Fatalf("decode: %v\n%s", derr, stdout)
	}
	if len(f.Skills) != 1 || !f.Skills[0].Pinned || f.Skills[0].Ref != sha {
		t.Errorf("entry = %+v, want pinned at %s", f.Skills, sha)
	}
}

func TestBundleNamesTheLocalSkillsItLeftOut(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	testrepo.Write(t, dir, map[string]string{"SKILL.md": skillMD})

	if out, err := h.run(t, "link", dir); err != nil {
		t.Fatalf("link: %v\n%s", err, out)
	}

	stdout, stderr, err := h.runSplit(t, "bundle")
	if err != nil {
		t.Fatalf("bundle: %v\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "demo-skill") || !strings.Contains(stderr, dir) {
		t.Errorf("the excluded skill should be named on stderr with its path:\n%s", stderr)
	}
	if strings.Contains(stdout, "demo-skill") {
		t.Errorf("a local skill must not reach the manifest:\n%s", stdout)
	}
	// Excluding everything is still success: an empty manifest plus the warning
	// is a truthful account of a machine holding only local skills.
	f, derr := manifest.Decode([]byte(stdout))
	if derr != nil {
		t.Fatalf("decode: %v\n%s", derr, stdout)
	}
	if len(f.Skills) != 0 {
		t.Errorf("skills = %+v, want none", f.Skills)
	}
}

func TestBundleOnAnEmptyStore(t *testing.T) {
	h := newHarness(t)

	stdout, _, err := h.runSplit(t, "bundle")
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	f, derr := manifest.Decode([]byte(stdout))
	if derr != nil {
		t.Fatalf("an empty store must still emit a valid manifest: %v\n%s", derr, stdout)
	}
	if f.Version != manifest.SchemaVersion || len(f.Skills) != 0 {
		t.Errorf("manifest = %+v, want a versioned empty file", f)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `make test`
Expected: FAIL — `unknown command "bundle" for "skillsctl"`.

- [ ] **Step 3: Write the implementation**

Create `internal/cli/bundle.go`:

```go
package cli

import (
	"strings"

	"github.com/richardcase/skillsctl/internal/manifest"
	"github.com/spf13/cobra"
)

func newBundleCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "bundle",
		Short: "Write the installed skills as a portable skills.toml",
		Long: "Project the current receipts into the skills.toml manifest format and write it\n" +
			"to stdout, so that `skillsctl bundle > skills.toml` on one machine and\n" +
			"`skillsctl sync skills.toml` on another install the same set.\n\n" +
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

			present, err := e.targets(nil)
			if err != nil {
				return err
			}

			f, excluded := manifest.FromReceipts(h.DB.List(), e.channels(), present)

			// cmd.Print and friends resolve to stderr unless a writer was set,
			// so the manifest is written to stdout by hand. It is the command's
			// product: `bundle > skills.toml` has to capture it, and only it.
			if err := manifest.Encode(cmd.OutOrStdout(), f); err != nil {
				return err
			}

			if len(excluded) > 0 {
				cmd.Printf("warning: %s left out of the manifest: %s\n",
					count(len(excluded), "local skill"), strings.Join(excluded, ", "))
			}
			return nil
		},
	}
}
```

Modify `internal/cli/root.go` — add `newBundleCmd()` as the first entry in the `AddCommand` call, keeping the list alphabetical:

```go
	root.AddCommand(
		newAdoptCmd(),
		newBundleCmd(),
		newGCCmd(),
		newInstallCmd(),
		newLinkCmd(),
		newListCmd(),
		newOutdatedCmd(),
		newPinCmd(),
		newRemoveCmd(),
		newSyncCmd(),
		newUnpinCmd(),
		newUpdateCmd(),
		newVersionCmd(),
	)
```

**Note:** `newSyncCmd()` does not exist until Task 5. Add only `newBundleCmd()` in this task; add `newSyncCmd()` in Task 5. The list above shows the final state.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `make test`
Expected: PASS.

- [ ] **Step 5: Lint and commit**

```bash
make lint && make tidy-check
git add internal/cli/bundle.go internal/cli/bundle_test.go internal/cli/root.go
git commit -m "feat: bundle the installed skills as skills.toml

Refs #15"
```

---

### Task 4: The desired-vs-installed diff

**Files:**
- Create: `internal/manifest/plan.go`
- Test: `internal/manifest/plan_test.go`

**Interfaces:**
- Consumes: `Entry`, `File`, `Entry.Parse` from Task 1; `channel.Registry`, `channel.Request`, `channel.Channel`, `channel.AgentOwned`, `plan.Plan`, `state.DB`, `target.Config`.
- Produces: `Status` and its five constants, `Verdict`, `Report`, `Plan(ctx, reg, f, db, cfg) (Report, plan.Plan)`.

- [ ] **Step 1: Write the failing tests**

Create `internal/manifest/plan_test.go`:

```go
package manifest

import (
	"context"
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
```

Add this helper at the bottom of `plan_test.go` (the fixture uses it, and importing `os` for one call in a fixture is what it is for):

```go
func mkdirAll(dir string) error { return os.MkdirAll(dir, 0o755) }
```

and add `"os"` to the test file's imports.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `make test`
Expected: FAIL — `undefined: Plan`, `undefined: StatusInstalled`.

- [ ] **Step 3: Write the implementation**

Create `internal/manifest/plan.go`:

```go
package manifest

import (
	"context"
	"fmt"

	"github.com/richardcase/skillsctl/internal/channel"
	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/target"
)

// Status is what sync did about one entry, or could not do.
type Status string

const (
	// StatusInstalled means the entry was not installed here and now is.
	StatusInstalled Status = "installed"
	// StatusLinked means it was installed, and an agent it names now has it.
	StatusLinked Status = "linked"
	// StatusPresent means the entry was already satisfied.
	StatusPresent Status = "present"
	// StatusDiffers means a skill under this name is installed, but not as the
	// entry describes it. sync only ever adds, so the difference is reported.
	StatusDiffers Status = "differs"
	// StatusError means this entry could not be acted on.
	StatusError Status = "error"
)

// Verdict is the answer for one manifest entry.
type Verdict struct {
	Name string
	// Agents are the agents installed or linked, for the report.
	Agents []string
	// Detail says what differs, or why the entry failed.
	Detail string
	// Version is the resolved revision, when there is one to name.
	Version string
	Status  Status
}

// Report is what a sync found: one verdict per entry, in the file's order,
// plus the installed skills the manifest never mentioned.
//
// Extra sits beside the verdicts rather than among them because a skill the
// manifest does not name is not an entry and has no verdict — the shape
// adopt.Report uses, for the same reason.
type Report struct {
	Verdicts []Verdict
	Extra    []*state.Receipt
}

// Plan says what each entry needs and returns the ops that provide it.
//
// sync only ever adds: an entry that is not installed is installed, an entry
// whose agents are incomplete is linked, and an entry that disagrees with the
// receipt under its name is reported. Nothing here re-points a ref, moves a pin
// or removes anything, which is what makes running it twice change nothing the
// second time.
//
// One entry in, one verdict out, in the file's order. There is no error to
// return: everything that could fail the whole command — an unreadable file, a
// TOML error, a missing name, a version from the future — is Decode's job and
// has already happened. A single unreachable remote is one entry's error, and
// must not hide the rest of the report.
func Plan(ctx context.Context, reg channel.Registry, f File, db *state.DB, cfg target.Config) (Report, plan.Plan) {
	var p plan.Plan
	rep := Report{Verdicts: make([]Verdict, 0, len(f.Skills))}
	named := make(map[string]bool, len(f.Skills))

	for _, e := range f.Skills {
		named[e.Name] = true
		v, ops := planEntry(ctx, reg, e, db, cfg)
		p.Ops = append(p.Ops, ops.Ops...)
		rep.Verdicts = append(rep.Verdicts, v)
	}

	for _, r := range db.List() {
		if !named[r.Name] {
			rep.Extra = append(rep.Extra, r)
		}
	}
	return rep, p
}

// planEntry answers one entry, against the receipt under its name if there is
// one.
func planEntry(ctx context.Context, reg channel.Registry, e Entry, db *state.DB, cfg target.Config) (Verdict, plan.Plan) {
	targets, err := agentsFor(e, cfg)
	if err != nil {
		return errorVerdict(e, err), plan.Plan{}
	}
	if r, ok := db.Receipts[e.Name]; ok {
		return planInstalled(reg, e, r, targets)
	}
	return planMissing(ctx, reg, e, targets)
}

// planMissing installs an entry this machine does not have, through exactly the
// path install takes. Nothing about sync is a second way to install a skill.
func planMissing(ctx context.Context, reg channel.Registry, e Entry, targets []target.Target) (Verdict, plan.Plan) {
	src, err := e.Parse()
	if err != nil {
		return errorVerdict(e, err), plan.Plan{}
	}
	ch, err := reg.For(src.Channel)
	if err != nil {
		return errorVerdict(e, err), plan.Plan{}
	}

	req := channel.Request{Source: src, Targets: targets, Ref: e.Ref, Pin: e.Pinned}
	chosen, err := ch.Prepare(ctx, req)
	if err != nil {
		return errorVerdict(e, err), plan.Plan{}
	}
	// An entry names one skill. A source that resolves to several is a manifest
	// that cannot be acted on without guessing, and the remedy is a subpath.
	if len(chosen) != 1 {
		return errorVerdict(e, fmt.Errorf("names %d skills, and an entry installs one: give it a subpath", len(chosen))), plan.Plan{}
	}
	// The entry's name is the receipt key, so it wins over what SKILL.md says —
	// the same override --as applies at install time.
	chosen[0].Name = e.Name

	p, receipts, err := ch.Install(req, chosen)
	if err != nil {
		return errorVerdict(e, err), plan.Plan{}
	}

	v := Verdict{Name: e.Name, Status: StatusInstalled, Agents: names(targets)}
	if len(receipts) == 1 {
		v.Version = receipts[0].Resolved
	}
	return v, p
}

// planInstalled compares an entry against the receipt already under its name.
// The only mutation it will plan is a link, because sync only ever adds.
func planInstalled(reg channel.Registry, e Entry, r *state.Receipt, targets []target.Target) (Verdict, plan.Plan) {
	if d := differs(e, r); d != "" {
		return Verdict{Name: e.Name, Status: StatusDiffers, Detail: d, Version: r.Resolved}, plan.Plan{}
	}

	ch, err := reg.ForReceipt(r)
	if err != nil {
		return errorVerdict(e, err), plan.Plan{}
	}
	// A plugin's skills reach its agent without a symlink of ours, so there is
	// no link for an entry to be missing. Ownership is the question list, remove
	// and gc already ask, and it is the right grain here too.
	if ch.Ownership() == channel.AgentOwned {
		return Verdict{Name: e.Name, Status: StatusPresent, Version: r.Resolved}, plan.Plan{}
	}

	add := missingLinks(r, targets)
	if len(add) == 0 {
		return Verdict{Name: e.Name, Status: StatusPresent, Version: r.Resolved}, plan.Plan{}
	}

	p, err := ch.Link(*r, add)
	if err != nil {
		return errorVerdict(e, err), plan.Plan{}
	}
	if p.IsEmpty() {
		return Verdict{Name: e.Name, Status: StatusPresent, Version: r.Resolved}, plan.Plan{}
	}
	return Verdict{Name: e.Name, Status: StatusLinked, Agents: names(add), Version: r.Resolved}, p
}

// differs says how the receipt under this name disagrees with the entry, or ""
// when it does not.
//
// The channel and the source come first: a name pointing at different files is
// the sharpest disagreement there is, and saying "the ref differs" about an
// entirely different skill would be misleading. Shas are reported in full,
// because the remedy for a pin difference is editing the manifest and a
// seven-character prefix is not what anybody pastes into one.
func differs(e Entry, r *state.Receipt) string {
	src, err := e.Parse()
	if err != nil {
		return fmt.Sprintf("its source cannot be parsed: %v", err)
	}
	if string(src.Channel) != r.Channel {
		return fmt.Sprintf("the manifest installs it through the %s channel, the receipt through %s", src.Channel, r.Channel)
	}
	if want := canonicalSource(src); want != "" && want != r.Source {
		return fmt.Sprintf("the manifest installs it from %s, the receipt from %s", want, r.Source)
	}
	if src.Subpath != r.Subpath {
		return fmt.Sprintf("the manifest names subpath %q, the receipt %q", src.Subpath, r.Subpath)
	}

	switch {
	case e.Pinned && !r.Pinned:
		return fmt.Sprintf("the manifest pins it at %s, the install tracks %s", e.Ref, describeRef(r.Ref))
	case !e.Pinned && r.Pinned:
		return fmt.Sprintf("the manifest tracks %s, the install is pinned at %s", describeRef(e.Ref), r.Resolved)
	case e.Pinned && r.Pinned && e.Ref != r.Resolved:
		return fmt.Sprintf("the manifest pins it at %s, the install at %s", e.Ref, r.Resolved)
	case !e.Pinned && !r.Pinned && e.Ref != r.Ref:
		return fmt.Sprintf("the manifest tracks %s, the install tracks %s", describeRef(e.Ref), describeRef(r.Ref))
	}
	return ""
}

// canonicalSource renders what a receipt installed from src would record in its
// Source field, so an entry can be compared with one.
//
// Local is the exception: its receipt records an absolute path resolved against
// the working directory, which only the local channel can produce, so a local
// entry is compared no further than its channel.
func canonicalSource(src source.Source) string {
	switch src.Channel {
	case source.ChannelGit:
		return src.RepoURL
	case source.ChannelPlugin:
		return src.Plugin + "@" + src.Marketplace
	default:
		return ""
	}
}

// describeRef names what something tracks, in the words pin and update already
// use for an empty ref.
func describeRef(ref string) string {
	if ref == "" {
		return "the repository's default branch"
	}
	return ref
}

// agentsFor resolves the agents an entry names, or the default set when it
// names none — the same pair install resolves -a with.
func agentsFor(e Entry, cfg target.Config) ([]target.Target, error) {
	if len(e.Agents) > 0 {
		return cfg.Select(e.Agents)
	}
	present := cfg.Present()
	if len(present) == 0 {
		return nil, fmt.Errorf("no agent directories found: create one (for example ~/.claude) or configure targets")
	}
	return present, nil
}

// missingLinks returns the targets an entry names that the receipt does not
// already record. Links is a set keyed by target, so one already there is never
// a second link.
func missingLinks(r *state.Receipt, targets []target.Target) []target.Target {
	held := make(map[string]bool, len(r.Links))
	for _, l := range r.Links {
		held[l.Target] = true
	}

	var add []target.Target
	for _, t := range targets {
		if !held[t.Name] {
			add = append(add, t)
		}
	}
	return add
}

func errorVerdict(e Entry, err error) Verdict {
	return Verdict{Name: e.Name, Status: StatusError, Detail: err.Error()}
}

func names(ts []target.Target) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.Name)
	}
	return out
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `make test`
Expected: PASS.

If `TestPlanLinksAnAgentTheEntryNamesAndTheReceiptLacks` fails with a `RevPath` complaint, it is because `linked.Link` refuses to plan a link whose `RevPath` is not a directory — `t.TempDir()` in `installedReceipt` satisfies that. If it still fails, print the verdict's `Detail` to see the real reason before changing anything.

- [ ] **Step 5: Lint and commit**

```bash
make lint && make tidy-check
git add internal/manifest/plan.go internal/manifest/plan_test.go
git commit -m "feat(manifest): diff a manifest against the installed skills

Refs #15"
```

---

### Task 5: `skillsctl sync`

**Files:**
- Create: `internal/cli/sync.go`
- Modify: `internal/cli/root.go` (add `newSyncCmd()`, alphabetically between `newRemoveCmd()` and `newUnpinCmd()`)
- Test: `internal/cli/sync_test.go`

**Interfaces:**
- Consumes: everything from Tasks 1–4; the existing `newEnv`, `env.openState`, `env.channels`, `env.cfg`, `newRunner`, `settle`, `shortSha`, `partialf`.
- Produces: `newSyncCmd() *cobra.Command`, and the file-local `settleSynced`, `reportSync`, `syncLine`, `syncExit`.

- [ ] **Step 1: Write the failing tests**

Create `internal/cli/sync_test.go`:

```go
package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/testrepo"
)

// writeManifest puts a manifest in a temp file and returns its path.
func writeManifest(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "skills.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The issue's own test: install, bundle, wipe everything, sync, and get the
// same links and the same receipts back.
func TestBundleSyncRoundTrip(t *testing.T) {
	h := newHarness(t)
	url, sha := testrepo.New(t, map[string]string{
		"a/SKILL.md": "---\nname: a\ndescription: A\n---\n",
		"b/SKILL.md": "---\nname: b\ndescription: B\n---\n",
	})

	if out, err := h.run(t, "install", url, "--all"); err != nil {
		t.Fatalf("install --all: %v\n%s", err, out)
	}
	before := h.receipts(t)
	if len(before) != 2 {
		t.Fatalf("want 2 receipts before the round trip, got %d", len(before))
	}

	stdout, _, err := h.runSplit(t, "bundle")
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	path := writeManifest(t, stdout)

	// Wipe the store and both agent directories: this is the other machine.
	if err := os.RemoveAll(h.root); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{h.claude, h.codex} {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
	}

	out, err := h.run(t, "sync", path)
	if err != nil {
		t.Fatalf("sync: %v\n%s", err, out)
	}

	for _, name := range []string{"a", "b"} {
		for agent, dir := range map[string]string{"claude": h.claude, "codex": h.codex} {
			dest, rerr := os.Readlink(filepath.Join(dir, name))
			if rerr != nil {
				t.Fatalf("%s link for %s missing after sync: %v", agent, name, rerr)
			}
			if !strings.Contains(dest, sha) {
				t.Errorf("%s link for %s points at %q, want the bundled sha", agent, name, dest)
			}
		}
	}

	after := h.receipts(t)
	if len(after) != len(before) {
		t.Fatalf("got %d receipts after sync, want %d", len(after), len(before))
	}
	for name, was := range before {
		is, ok := after[name]
		if !ok {
			t.Fatalf("%s was not reinstalled", name)
		}
		// Everything the manifest carries has to come back identical. RevPath,
		// contentHash and the timestamps are deliberately not compared: they are
		// what the manifest drops.
		for _, field := range []string{"source", "subpath", "ref", "resolved", "channel", "pinned"} {
			if was[field] != is[field] {
				t.Errorf("%s.%s = %v after sync, was %v", name, field, is[field], was[field])
			}
		}
	}
}

func TestSyncIsIdempotent(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	if out, err := h.run(t, "install", url); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	stdout, _, err := h.runSplit(t, "bundle")
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	path := writeManifest(t, stdout)

	first, err := h.run(t, "sync", path)
	if err != nil {
		t.Fatalf("first sync: %v\n%s", err, first)
	}
	second, err := h.run(t, "sync", path)
	if err != nil {
		t.Fatalf("second sync: %v\n%s", err, second)
	}
	if !strings.Contains(second, "already installed") {
		t.Errorf("a second sync should say there was nothing to do:\n%s", second)
	}
}

func TestSyncCarriesAPinAcross(t *testing.T) {
	h := newHarness(t)
	url, sha := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	if out, err := h.run(t, "install", url, "--pin"); err != nil {
		t.Fatalf("install --pin: %v\n%s", err, out)
	}
	stdout, _, err := h.runSplit(t, "bundle")
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	path := writeManifest(t, stdout)

	if err := os.RemoveAll(h.root); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{h.claude, h.codex} {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
	}

	if out, serr := h.run(t, "sync", path); serr != nil {
		t.Fatalf("sync: %v\n%s", serr, out)
	}

	got := h.receipts(t)["demo-skill"]
	if got["pinned"] != true {
		t.Errorf("the pin was lost: %+v", got)
	}
	if got["resolved"] != sha {
		t.Errorf("resolved = %v, want the pinned sha %s", got["resolved"], sha)
	}
}

func TestSyncReportsADifferenceWithoutChangingIt(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	if out, err := h.run(t, "install", url); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	path := writeManifest(t, "version = 1\n\n[[skill]]\nname = 'demo-skill'\nsource = '"+url+"'\nref = 'develop'\n")

	code, out := exitCode(t, "sync", path)
	if code != ExitPartial {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitPartial, out)
	}
	if !strings.Contains(out, "differs") || !strings.Contains(out, "develop") {
		t.Errorf("the difference should be reported and named:\n%s", out)
	}
	if !strings.Contains(out, "remove it and run sync again") {
		t.Errorf("the report should name the remedy:\n%s", out)
	}
	// Nothing moved: the receipt still tracks what it tracked.
	if got := h.receipts(t)["demo-skill"]["ref"]; got == "develop" {
		t.Error("sync re-pointed a ref, and it only ever adds")
	}
}

// A skill the manifest never names is information, not a failure.
func TestSyncReportsExtrasWithoutFailing(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	if out, err := h.run(t, "install", url); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	path := writeManifest(t, "version = 1\n")

	code, out := exitCode(t, "sync", path)
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d — an extra is not a failure\n%s", code, ExitOK, out)
	}
	if !strings.Contains(out, "not in the manifest") || !strings.Contains(out, "demo-skill") {
		t.Errorf("the extra should be named:\n%s", out)
	}
	if _, err := os.Lstat(filepath.Join(h.claude, "demo-skill")); err != nil {
		t.Error("sync removed a skill the manifest did not name, and it never removes anything")
	}
}

func TestSyncLinksAMissingAgent(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	if out, err := h.run(t, "install", url, "-a", "claude"); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	path := writeManifest(t, "version = 1\n\n[[skill]]\nname = 'demo-skill'\nsource = '"+url+
		"'\nagents = ['claude', 'codex']\n")

	out, err := h.run(t, "sync", path)
	if err != nil {
		t.Fatalf("sync: %v\n%s", err, out)
	}
	if !strings.Contains(out, "linked demo-skill into codex") {
		t.Errorf("want the new link reported:\n%s", out)
	}
	if _, lerr := os.Lstat(filepath.Join(h.codex, "demo-skill")); lerr != nil {
		t.Errorf("codex link missing after sync: %v", lerr)
	}
}

func TestSyncDryRunChangesNothing(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})
	path := writeManifest(t, "version = 1\n\n[[skill]]\nname = 'demo-skill'\nsource = '"+url+"'\n")

	out, err := h.run(t, "sync", path, "--dry-run")
	if err != nil {
		t.Fatalf("sync --dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "link") {
		t.Errorf("the dry run should describe the link ops:\n%s", out)
	}
	if _, lerr := os.Lstat(filepath.Join(h.claude, "demo-skill")); !os.IsNotExist(lerr) {
		t.Error("the dry run created a symlink")
	}
	if _, serr := os.Stat(filepath.Join(h.root, "state.json")); !os.IsNotExist(serr) {
		t.Error("the dry run wrote the receipts database")
	}
}

func TestSyncOnAnUnreadableFile(t *testing.T) {
	h := newHarness(t)
	if _, err := h.run(t, "sync", filepath.Join(t.TempDir(), "nope.toml")); err == nil {
		t.Fatal("sync accepted a file that does not exist")
	}
}

func TestSyncOnAManifestFromTheFuture(t *testing.T) {
	h := newHarness(t)
	path := writeManifest(t, "version = 99\n")

	_, err := h.run(t, "sync", path)
	if err == nil {
		t.Fatal("sync accepted a manifest version it cannot understand")
	}
	if !strings.Contains(err.Error(), "upgrade skillsctl") {
		t.Errorf("the error should name the remedy, got: %v", err)
	}
}

// Nothing applied and something asked for is a failure, not a partial result.
func TestSyncExitsErrorWhenNothingCouldBeApplied(t *testing.T) {
	h := newHarness(t)
	path := writeManifest(t, "version = 1\n\n[[skill]]\nname = 'demo-skill'\nsource = 'file:///nonexistent/repo.git'\n")

	code, out := exitCode(t, "sync", path)
	if code != ExitError {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitError, out)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `make test`
Expected: FAIL — `unknown command "sync" for "skillsctl"`.

- [ ] **Step 3: Write the implementation**

Create `internal/cli/sync.go`:

```go
package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/richardcase/skillsctl/internal/manifest"
	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/spf13/cobra"
)

func newSyncCmd() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "sync <file>",
		Short: "Install the skills a skills.toml names",
		Long: "Read a manifest and add what this machine is missing: skills it does not have,\n" +
			"and links into the agents an entry names.\n\n" +
			"sync only ever adds. It never re-points a ref, never moves a pin and never\n" +
			"removes a skill, so running it twice changes nothing the second time. A\n" +
			"difference between the manifest and an install is reported rather than\n" +
			"resolved, and so is a skill installed here that the manifest never mentions.",
		Args: cobra.ExactArgs(1),
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

			// The state lock is taken before anything is written to the store
			// and held until this command exits, so a concurrent gc can never
			// collect a revision this sync is about to link.
			h, err := e.openState()
			if err != nil {
				return err
			}
			defer func() { _ = h.Close() }()

			rep, p := manifest.Plan(cmd.Context(), e.channels(), f, h.DB, e.cfg)

			if dryRun {
				for _, line := range p.Describe() {
					cmd.Println(line)
				}
				reportSync(cmd, rep, true)
				return syncExit(rep)
			}

			var serr error
			if !p.IsEmpty() {
				ex := &plan.Executor{DB: h.DB, Out: cmd.OutOrStdout(), Run: newRunner()}
				if err := ex.Apply(cmd.Context(), p); err != nil {
					return err
				}

				// A channel whose agent chooses the version can only be asked
				// once it has run, so the receipts are completed before they are
				// committed rather than after.
				serr = settleSynced(cmd.Context(), ex, e, h.DB, rep)

				if err := h.Commit(); err != nil {
					return fmt.Errorf("%w\nthe skills were linked but the receipts were not saved; re-run this command to repair", err)
				}
			}

			reportSync(cmd, rep, false)
			if serr != nil {
				cmd.Printf("warning: %v\n", serr)
			}
			if err := syncExit(rep); err != nil {
				return err
			}
			if serr != nil {
				return partialf("the sync ran, but a version could not be read back")
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change without changing it")
	return cmd
}

// settleSynced completes the receipts sync just wrote, for the channels that
// cannot know a version until their agent has run.
//
// It groups by channel for the reason update does: a channel is asked once, and
// answers for everything it owns.
func settleSynced(ctx context.Context, ex *plan.Executor, e *env, db *state.DB, rep manifest.Report) error {
	reg := e.channels()
	grouped := map[string][]state.Receipt{}
	var order []string

	for _, v := range rep.Verdicts {
		if v.Status != manifest.StatusInstalled {
			continue
		}
		r, ok := db.Receipts[v.Name]
		if !ok {
			continue
		}
		if _, seen := grouped[r.Channel]; !seen {
			order = append(order, r.Channel)
		}
		grouped[r.Channel] = append(grouped[r.Channel], *r)
	}

	var firstErr error
	for _, name := range order {
		ch, err := reg.For(source.Channel(name))
		if err != nil {
			continue
		}
		if _, err := settle(ctx, ex, ch, grouped[name]); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// reportSync writes one line per entry that is not simply already satisfied,
// then the skills the manifest never mentioned. A run where everything was
// already in place still says so, rather than printing nothing and leaving the
// user wondering whether it ran.
func reportSync(cmd *cobra.Command, rep manifest.Report, dryRun bool) {
	var interesting int
	for _, v := range rep.Verdicts {
		line := syncLine(v, dryRun)
		if line == "" {
			continue
		}
		interesting++
		cmd.Println(line)
	}
	if interesting == 0 {
		cmd.Println("Everything the manifest names is already installed.")
	}

	// An extra is reported after the entries and never changes the exit code:
	// the manifest is not a statement about what must not be installed.
	for _, r := range rep.Extra {
		cmd.Printf("not in the manifest: %s (installed from %s)\n", r.Name, r.Source)
	}
}

// syncLine renders one verdict, or "" for an entry with nothing to say.
func syncLine(v manifest.Verdict, dryRun bool) string {
	where := strings.Join(v.Agents, ", ")

	switch v.Status {
	case manifest.StatusInstalled:
		verb := "installed"
		if dryRun {
			verb = "would install"
		}
		// A local skill has no revision to name, and a plugin's is unknown until
		// its agent has run. "@" with nothing after it reads as something
		// missing rather than something absent.
		if v.Version == "" {
			return fmt.Sprintf("%s %s into %s", verb, v.Name, where)
		}
		return fmt.Sprintf("%s %s @ %s into %s", verb, v.Name, shortSha(v.Version), where)

	case manifest.StatusLinked:
		verb := "linked"
		if dryRun {
			verb = "would link"
		}
		return fmt.Sprintf("%s %s into %s", verb, v.Name, where)

	case manifest.StatusDiffers:
		// The remedy is named inline because there is no verb that retargets a
		// ref: pin and unpin move a pin, and nothing moves a ref on its own.
		return fmt.Sprintf("%s differs: %s; remove it and run sync again, or bring the manifest in line",
			v.Name, v.Detail)

	case manifest.StatusError:
		return fmt.Sprintf("skipped %s: %s", v.Name, v.Detail)

	default:
		return ""
	}
}

// syncExit turns the report into an exit code. A difference and a failure are
// both work the run was asked for and could not do, which is what ExitPartial
// exists for; an entry already satisfied is not, and neither is a skill the
// manifest never named.
func syncExit(rep manifest.Report) error {
	var applied, skipped int
	for _, v := range rep.Verdicts {
		switch v.Status {
		case manifest.StatusInstalled, manifest.StatusLinked:
			applied++
		case manifest.StatusDiffers, manifest.StatusError:
			skipped++
		}
	}

	switch {
	case skipped == 0:
		return nil
	case applied > 0:
		return partialf("%d of %d entries applied, for the reasons above", applied, len(rep.Verdicts))
	default:
		return fmt.Errorf("nothing was applied: %d of %d entries could not be, for the reasons above",
			skipped, len(rep.Verdicts))
	}
}
```

Modify `internal/cli/root.go` to add `newSyncCmd()` between `newRemoveCmd()` and `newUnpinCmd()`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `make test`
Expected: PASS.

Two failures to expect and read carefully rather than paper over:
- If `TestSyncIsIdempotent` fails on the "already installed" string, check `reportSync`'s wording matches the assertion.
- If `TestBundleSyncRoundTrip` fails because `ref` differs, remember `install --all` records no ref (it tracks the default branch), so both sides should be empty; print the two receipt maps before changing the assertion.

- [ ] **Step 5: Lint and commit**

```bash
make lint && make tidy-check
git add internal/cli/sync.go internal/cli/sync_test.go internal/cli/root.go
git commit -m "feat: sync the skills a manifest names

Refs #15"
```

---

### Task 6: Documentation

**Files:**
- Modify: `README.md` (Features, Commands table, a new `skills.toml` section, Status)

**Interfaces:**
- Consumes: the finished behaviour of Tasks 3 and 5.
- Produces: nothing code depends on.

- [ ] **Step 1: Capture the real output**

Run these and keep the output — every example in the README is checked against the real thing during review:

```bash
make build
./skillsctl bundle --help
./skillsctl sync --help
```

- [ ] **Step 2: Add a Features bullet**

In `README.md`, after the "Develop a skill in place" bullet, add:

```markdown
- **Move your skills to another machine.** `skillsctl bundle > skills.toml`
  writes a small, human-editable manifest of what you have installed;
  `skillsctl sync skills.toml` installs it somewhere else, pins and all. `sync`
  only ever adds — it reports a difference or a skill the manifest does not
  name, and never removes anything.
```

- [ ] **Step 3: Add the two rows to the Commands table**

Insert alphabetically-consistently with the existing table's ordering (which follows the workflow, not the alphabet — put `bundle` and `sync` after `gc` and before `version`):

```markdown
| `bundle` | | Write the installed skills as a portable `skills.toml` |
| `sync <file>` | `--dry-run` | Install the skills a manifest names, and report the rest |
```

- [ ] **Step 4: Add a `skills.toml` section**

Add before `## Status`. Use single quotes: that is what the encoder emits, and the README's examples have to match the real output.

````markdown
## skills.toml

`skillsctl bundle` writes the skills you have installed as a manifest, and
`skillsctl sync` installs one. It is meant to be read and edited by hand, and
committed.

```toml
version = 1

[[skill]]
name = 'alpha'
source = 'https://github.com/owner/repo.git'
subpath = 'skills/alpha'
ref = '9f8e7d6c5b4a39281706f5e4d3c2b1a098765432'
pinned = true

[[skill]]
name = 'beta'
source = 'https://github.com/owner/repo.git'
ref = 'develop'
agents = ['claude']
```

- `ref` is the branch or tag a skill tracks, or the frozen sha when `pinned` is
  set — an `install --pin` records no ref, so the sha is the only thing that can
  carry the pin to another machine.
- `agents` is omitted when the skill is in every agent present on the machine,
  which is what an omitted `-a` means to `install`. Name them only for a
  narrower choice.
- `subpath` locates a skill inside a repository holding several. You can write
  it in the source instead, as `owner/repo//skills/alpha`.
- `local` skills — a directory you linked with `skillsctl link ./path` — are
  left out of a bundle and named on stderr, because an absolute path on one
  machine means nothing on another.

`sync` only ever adds:

```
$ skillsctl sync skills.toml
installed alpha @ 9f8e7d6 into claude, codex
linked beta into gemini
beta differs: the manifest tracks develop, the install tracks main; remove it and run sync again, or bring the manifest in line
not in the manifest: gamma (installed from https://github.com/x/y.git)

note: 2 of 3 entries applied, for the reasons above
```

It installs what is missing and links the agents an entry names. It never
re-points a ref, never moves a pin and never removes a skill, so a second run
changes nothing. A difference exits 2; a skill the manifest does not name is
reported and changes the exit code not at all.
````

- [ ] **Step 5: Update Status**

Replace the first paragraph of `## Status` with:

```markdown
All three channels are implemented: `git`, `plugin` (`name@marketplace`) and
`local` (`./path`), and `link` serves both of its forms. `doctor` is designed
but not built.
```

- [ ] **Step 6: Check the whole README against the change**

Re-read the Features list, the Commands table, the `Use` examples and Status as a whole. Confirm the sample `sync` output above matches what `syncLine` and `syncExit` actually print — if it does not, fix the README, not the test.

- [ ] **Step 7: Verify and commit**

```bash
make test && make lint && make tidy-check
git add README.md
git commit -m "docs: describe bundle and sync

Refs #15"
```

---

## Final verification

Run the whole definition of done, then the manual round trip:

```bash
make test && make lint && make tidy-check
make build

# A real round trip against a real repository.
./skillsctl install obra/superpowers --skill brainstorming
./skillsctl bundle | tee /tmp/skills.toml
./skillsctl sync /tmp/skills.toml        # says everything is already installed
./skillsctl sync /tmp/skills.toml        # and says it again: idempotent
```

Confirm by eye that:
- `bundle`'s output is valid TOML with `version = 1` first, and no `agents` field for a skill in every agent.
- The second and third `sync` runs print "Everything the manifest names is already installed." and exit 0.
- `./skillsctl bundle > /tmp/x.toml` produces a file with no warning text in it.

## Self-review notes

Checked against the spec:

- Schema, every field and every decision — Task 1.
- `bundle` to stdout, local excluded and named on stderr — Tasks 2 and 3.
- `sync` only ever adds; install / link / present / differs / error; extras — Task 4.
- Exit codes 0/2/1 and the inline remedy — Task 5.
- `Plan` returns `(Report, plan.Plan)` with **no error** — every per-entry failure is a verdict, and `Decode` owns everything that could fail the command. Writing this plan caught the spec claiming a third `error` return; the spec has been corrected to match, so the two agree.
- README — Task 6.
