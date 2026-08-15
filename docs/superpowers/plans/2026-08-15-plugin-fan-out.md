# Plugin Fan-Out Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Link the skills a Claude Code plugin ships into the agents that cannot install plugins, and keep those links correct when claude moves the plugin to a new version.

**Architecture:** `Channel.Link` widens from "add this receipt to these agents" to "make these agents hold what the receipt says they should" — add, re-point, remove. One post-settle reconcile step in `install` and `update` then serves every channel: an empty plan for git and local, the whole fan-out for plugin. `Receipt.Links` gains an entry per skill per fanned-out agent; nothing else in the receipt changes.

**Tech Stack:** Go 1.25, cobra, standard-library tests only.

**Spec:** `docs/superpowers/specs/2026-08-15-plugin-fan-out-design.md`

## Global Constraints

Copied from `AGENTS.md`; every task's requirements implicitly include these.

- Go 1.25, `GOTOOLCHAIN=local`. Module `github.com/richardcase/skillsctl`.
- Use Makefile targets, not raw `go`/`golangci-lint`: `make test`, `make lint`, `make fmt`, `make tidy-check`.
- **Definition of done for the branch:** `make test && make lint && make tidy-check` all pass, and `README.md` reflects every user-visible change.
- **Conventional Commits are required** for every commit: `type(optional-scope): subject`, lowercase, imperative, no trailing period. Types in use: `feat`, `fix`, `docs`, `test`, `chore`, `ci`, `refactor`, `perf`.
- **No attribution footers.** No `Co-Authored-By:`, no `Claude-Session:`, no "Generated with Claude Code". `Closes #N` / `Refs #N` and `BREAKING CHANGE:` are the only footers this repo uses.
- **Tests use the standard library only.** No testify, no mocks, no golden files. Table-driven subtests with `t.Run`, `t.TempDir()`, `t.Setenv`. **Never call `t.Parallel()`** — `t.Setenv` forbids it. Tests are in-package.
- **Errors:** `fmt.Errorf` with `%w`, lowercase verb-first prefix naming the operation and path. Deliberately ignored errors are explicit `_ =`.
- **Comments** explain rationale and rejected alternatives, not mechanics. Exported identifiers need doc comments — revive enforces it.
- **Path safety:** anything derived from third-party data is validated before it becomes a path, every time it is joined, and ships with a test for the rejection case.
- **No new dependencies.**

---

## File Structure

| File | Change | Responsibility |
| --- | --- | --- |
| `internal/plan/plan.go` | modify | add `Note`, a plan line that changes nothing |
| `internal/plan/executor.go` | modify | `Note` is a no-op case, not `unknown op` |
| `internal/target/target.go` | modify | add `WithoutPlugins`; re-document `Plugins` |
| `internal/channel/channel.go` | modify | widen `Link`; re-document `Ownership` |
| `internal/channel/linked.go` | modify | `Link` returns no skips; `held` keyed by path |
| `internal/channel/plugin.go` | modify | the fan-out: `fan`, `skills`, `linkOpFor`, `Link`, `Remove`, `Agents`, `Install` |
| `internal/cli/relink.go` | **create** | the shared post-settle reconcile step |
| `internal/cli/install.go` | modify | call `relink` after `settle` |
| `internal/cli/update.go` | modify | call `relink` after `settle`, per receipt |
| `internal/cli/link.go` | modify | partition on `ch.Agents`, carry skips |
| `internal/adopt/adopt.go` | modify | find a receipt by link path before by name |
| `internal/outdated/outdated.go` | modify | `StatusStale`, checked against `claude plugin list` |
| `internal/cli/outdated.go` | modify | pass the plugins reader in |
| `README.md` | modify | the user-visible surface |
| `AGENTS.md` | modify | the channel-conventions note |

`relink` gets its own file rather than joining `settle.go`: they run back to back but answer different questions, and `settle.go`'s doc comment is about completing a receipt, not about the filesystem.

---

## Task 1: `plan.Note`

**Files:**
- Modify: `internal/plan/plan.go:79-85` (after `Exec`)
- Modify: `internal/plan/executor.go:46-80` (the op switch)
- Test: `internal/plan/plan_test.go`, `internal/plan/executor_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `plan.Note{Text string}` with `Describe() string` returning `"note    " + Text`. Applying it does nothing and cannot fail.

- [ ] **Step 1: Write the failing tests**

Append to `internal/plan/plan_test.go`:

```go
func TestNoteDescribesItselfAndChangesNothing(t *testing.T) {
	o := Note{Text: "then link the skills it ships into codex"}
	if got, want := o.Describe(), "note    then link the skills it ships into codex"; got != want {
		t.Errorf("Describe() = %q, want %q", got, want)
	}
}
```

Append to `internal/plan/executor_test.go`:

```go
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
```

Check the imports already present in `executor_test.go` and add only what is missing (`io` is likely needed).

- [ ] **Step 2: Run the tests to verify they fail**

Run: `make test 2>&1 | grep -A5 'internal/plan'`
Expected: FAIL — `undefined: Note`.

- [ ] **Step 3: Add the op**

In `internal/plan/plan.go`, after `Exec`'s `Describe` (line 85):

```go
// Note is a line in the plan that changes nothing.
//
// It exists for the one thing this tool cannot predict. A plugin's install path
// is decided by claude and read back afterwards, so the links that follow an
// install or an update cannot be named in the plan that precedes them. Printing
// nothing would leave a --dry-run silently short of what the command does, which
// is worse than printing a sentence that admits the gap.
type Note struct {
	Text string
}

// Describe renders the note, padded to the same column as every other op so a
// plan still reads as one list.
func (o Note) Describe() string { return "note    " + o.Text }
```

In `internal/plan/executor.go`, inside the `switch o := op.(type)` block, immediately before `default:`:

```go
		case Note:
			// A note is the plan saying something it cannot yet do; there is
			// nothing to apply and nothing to roll back.
			_ = o
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `make test && make lint`
Expected: PASS, no lint findings.

- [ ] **Step 5: Commit**

```bash
git add internal/plan
git commit -m "feat(plan): add Note, a plan line that changes nothing"
```

---

## Task 2: Widen `Channel.Link` to carry skip reasons

Mechanical. Every existing test must still pass; this task adds no behaviour.

**Files:**
- Modify: `internal/channel/channel.go:180-186` (the `Link` method on the interface)
- Modify: `internal/channel/linked.go:62-104`
- Modify: `internal/channel/plugin.go:222-233`
- Modify: `internal/cli/link.go:138-141`
- Test: `internal/channel/linked_test.go`, `internal/cli/link_test.go` (call sites only)

**Interfaces:**
- Consumes: nothing.
- Produces: `Link(r state.Receipt, add []target.Target) (plan.Plan, []string, error)`. The `[]string` holds human-readable reasons for links that could not be made — each already a complete line, e.g. `"skipped brainstorming for codex: /x/y already points at /z"`. `nil` means nothing was skipped.

- [ ] **Step 1: Change the interface**

In `internal/channel/channel.go`, replace the `Link` block:

```go
	// Link makes the agents in add hold what this receipt says they should: it
	// adds the links that are missing, re-points the ones whose destination has
	// moved, and takes away the ones whose skill the source no longer ships. An
	// empty plan means they already agreed.
	//
	// It is reconciliation rather than addition because one channel's
	// destination moves under it: claude installs each version of a plugin
	// beside the last. Install, update and link then all reduce to this one
	// question asked of a different set of agents.
	//
	// The reasons come back separately from the error because a name another
	// skill already holds costs one link, not the command.
	//
	// It sits on the interface rather than behind a type assertion so that a
	// channel which cannot serve it has to say so, and a fourth channel is told
	// by the compiler that this is a question it must answer.
	Link(r state.Receipt, add []target.Target) (plan.Plan, []string, error)
```

- [ ] **Step 2: Update the two implementations and the one call site**

`internal/channel/linked.go` — change the signature and every `return`, and re-key `held`:

```go
// Link adds the receipt to the agents in add, and is Remove read backwards: the
// same links that are the removal contract are what this appends to.
//
// A channel that embeds linked has one skill at one known path, so the wider
// reconciliation contract collapses to addition here: there is never a link to
// re-point or take away.
//
// An empty plan means every target already had it, which the caller reports for
// the same reason it reports an empty Remove — it is the one that knows what the
// user typed. Nothing is ever skipped for a reason worth printing, so the
// reasons are always nil.
func (linked) Link(r state.Receipt, add []target.Target) (plan.Plan, []string, error) {
	var p plan.Plan

	// target.Link would create the symlink whether or not anything is on the
	// other end of it, and a dangling entry in a skills directory is worse than
	// a refusal: the agent finds it, fails to load it, and says nothing useful.
	if fi, err := os.Stat(r.RevPath); err != nil || !fi.IsDir() {
		return p, nil, fmt.Errorf("refusing to link %s: %s is not a directory, so every new link would dangle", r.Name, r.RevPath)
	}

	// Links is a set keyed by the path a link sits at: Unlink treats a missing
	// link as success, so two entries naming one path would plan two unlinks of
	// it and swallow the second. A receipt for a single skill has one path per
	// agent, which is why this reads as one link per target.
	held := make(map[string]bool, len(r.Links))
	for _, l := range r.Links {
		held[l.Path] = true
	}

	// The receipt is the caller's, and Links shares its backing array with it.
	updated := r
	updated.Links = make([]state.Link, len(r.Links), len(r.Links)+len(add))
	copy(updated.Links, r.Links)

	for _, t := range add {
		linkPath, err := linkPathFor(t, r.Name)
		if err != nil {
			return plan.Plan{}, nil, err
		}
		if held[linkPath] {
			continue
		}
		p.Add(plan.Link{Target: t.Name, LinkPath: linkPath, RevPath: r.RevPath})
		updated.Links = append(updated.Links, state.Link{Target: t.Name, Path: linkPath})
		held[linkPath] = true
	}
	if p.IsEmpty() {
		return p, nil, nil
	}

	updated.UpdatedAt = time.Now().UTC()
	p.Add(plan.Record{Receipt: updated})
	return p, nil, nil
}
```

`internal/channel/plugin.go` — signature only for now, body unchanged:

```go
func (c *Plugin) Link(r state.Receipt, _ []target.Target) (plan.Plan, []string, error) {
	return plan.Plan{}, nil, fmt.Errorf("%s is a plugin, and a plugin's skills are already visible to %s without a symlink: "+
		"linking one into another agent is not supported yet",
		r.Name, strings.Join(c.Agents(r), ", "))
}
```

`internal/cli/link.go:138` — discard the skips for now, with a marker comment so Task 8 finds it:

```go
	// TODO(task 8): report these.
	p, _, err := ch.Link(*receipt, add)
	if err != nil {
		return err
	}
```

- [ ] **Step 3: Fix the test call sites**

Run `make test 2>&1 | grep 'assignment mismatch\|too many return'` and update each. In `internal/channel/linked_test.go` and anywhere else calling `Link`, change `p, err := ...Link(...)` to `p, _, err := ...Link(...)`. Change nothing else.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `make test && make lint`
Expected: PASS. No behaviour changed, so no assertion should need editing.

- [ ] **Step 5: Commit**

```bash
git add internal/channel internal/cli
git commit -m "refactor(channel): widen Link to reconciliation and carry skip reasons"
```

---

## Task 3: `target.WithoutPlugins`

**Files:**
- Modify: `internal/target/target.go:14-20` (the `Plugins` field doc), `:116-129` (beside `WithPlugins`)
- Test: `internal/target/target_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `target.WithoutPlugins(ts []Target) []Target` — the agents that do *not* install plugins, in the order given.

- [ ] **Step 1: Write the failing test**

Append to `internal/target/target_test.go`:

```go
func TestWithoutPluginsIsTheAgentsToFanOutTo(t *testing.T) {
	ts := []target.Target{
		{Name: "claude", Plugins: true},
		{Name: "codex"},
		{Name: "gemini"},
	}

	got := target.WithoutPlugins(ts)
	if len(got) != 2 || got[0].Name != "codex" || got[1].Name != "gemini" {
		t.Fatalf("WithoutPlugins = %v, want codex then gemini in that order", got)
	}
	if len(target.WithoutPlugins(nil)) != 0 {
		t.Error("no agents means nothing to fan out to, not a panic")
	}
}
```

Check whether `target_test.go` is in-package (`package target`) or external; match it, and drop the `target.` qualifier if in-package.

- [ ] **Step 2: Run the test to verify it fails**

Run: `make test 2>&1 | grep -A3 internal/target`
Expected: FAIL — `undefined: WithoutPlugins`.

- [ ] **Step 3: Implement**

In `internal/target/target.go`, immediately after `WithPlugins`:

```go
// WithoutPlugins narrows targets to the agents that cannot install plugins from
// a marketplace, which is exactly the set a plugin's skills have to be linked
// into. It is the complement of WithPlugins rather than a second flag: an agent
// either fetches a plugin for itself or is shown one, never both.
func WithoutPlugins(ts []Target) []Target {
	var out []Target
	for _, t := range ts {
		if !t.Plugins {
			out = append(out, t)
		}
	}
	return out
}
```

And re-document the field, replacing the bare struct line:

```go
// Target is one agent's skills directory.
type Target struct {
	Name       string `toml:"name"`
	Dir        string `toml:"dir"`
	ProjectDir string `toml:"project_dir"`
	// Plugins marks an agent that installs plugins from a marketplace for
	// itself. It gates installing a plugin, never seeing one: an agent without
	// it is where a plugin's skills are linked, not one they are kept from.
	Plugins bool `toml:"plugins"`
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `make test && make lint`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/target
git commit -m "feat(target): add WithoutPlugins, the agents a plugin fans out to"
```

---

## Task 4: `Plugin.fan` — the reconcile

The core of the feature. Read-only against the filesystem; produces ops and the receipt's new link set.

**Files:**
- Modify: `internal/channel/plugin.go` (add near the bottom, before `find`)
- Test: `internal/channel/plugin_test.go`

**Interfaces:**
- Consumes: `target.WithoutPlugins` (Task 3).
- Produces, all unexported:
  - `const pluginSkillsDir = "skills"`
  - `type pluginSkill struct { name, dir string }`
  - `func (c *Plugin) skills(r state.Receipt) ([]pluginSkill, error)`
  - `func linkOpFor(t target.Target, linkPath, dest string, ours bool) (plan.Op, string)`
  - `func (c *Plugin) fan(r state.Receipt, add []target.Target) (plan.Plan, []state.Link, []string, error)` — returns the ops, the receipt's **complete** new link set (agents not in `add` keep theirs), the skip reasons, and an error.

- [ ] **Step 1: Write the failing tests**

Add to `internal/channel/plugin_test.go`. First a fixture helper, then the tests:

```go
// pluginTree writes a plugin install path holding the named skills, so a
// reconcile has something real to walk and link at.
func pluginTree(t *testing.T, root string, names ...string) string {
	t.Helper()
	for _, n := range names {
		dir := filepath.Join(root, pluginSkillsDir, n)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "---\nname: " + n + "\ndescription: a skill\n---\n\nBody.\n"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// fanCfg is pluginCfg with real directories, for the tests that touch disk.
func fanCfg(t *testing.T) (target.Config, string) {
	t.Helper()
	agents := t.TempDir()
	cfg := target.Config{Targets: []target.Target{
		{Name: "claude", Dir: filepath.Join(agents, "claude"), Plugins: true},
		{Name: "codex", Dir: filepath.Join(agents, "codex")},
	}}
	return cfg, agents
}

func TestFanLinksEverySkillIntoEveryAgentThatCannotInstallPlugins(t *testing.T) {
	cfg, _ := fanCfg(t)
	rev := pluginTree(t, t.TempDir(), "alpha", "beta")
	c := NewPlugin(&fakeClaude{}, cfg)
	r := state.Receipt{Name: "superpowers", RevPath: rev}

	p, links, skipped, err := c.fan(r, cfg.Targets)
	if err != nil {
		t.Fatalf("fan: %v", err)
	}
	if len(skipped) != 0 {
		t.Errorf("nothing was in the way, but got skips: %v", skipped)
	}
	if len(p.Ops) != 2 {
		t.Fatalf("ops = %v, want one link per skill for codex alone", p.Describe())
	}
	for _, l := range links {
		if l.Target != "codex" {
			t.Errorf("linked into %s: claude installed the plugin and can already see it", l.Target)
		}
	}
	if len(links) != 2 {
		t.Fatalf("links = %v, want one per skill", links)
	}
	want := filepath.Join(cfg.Targets[1].Dir, "alpha")
	if links[0].Path != want {
		t.Errorf("links[0].Path = %q, want %q", links[0].Path, want)
	}
}

func TestFanRelinksASkillWhoseVersionDirectoryMoved(t *testing.T) {
	cfg, _ := fanCfg(t)
	old := pluginTree(t, t.TempDir(), "alpha")
	newRev := pluginTree(t, t.TempDir(), "alpha")

	linkPath := filepath.Join(cfg.Targets[1].Dir, "alpha")
	if err := os.MkdirAll(cfg.Targets[1].Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(old, pluginSkillsDir, "alpha"), linkPath); err != nil {
		t.Fatal(err)
	}

	c := NewPlugin(&fakeClaude{}, cfg)
	r := state.Receipt{
		Name:    "superpowers",
		RevPath: newRev,
		Links:   []state.Link{{Target: "codex", Path: linkPath}},
	}

	p, links, _, err := c.fan(r, cfg.Targets)
	if err != nil {
		t.Fatalf("fan: %v", err)
	}
	if len(p.Ops) != 1 {
		t.Fatalf("ops = %v, want one relink", p.Describe())
	}
	op, ok := p.Ops[0].(plan.Relink)
	if !ok {
		t.Fatalf("op = %T, want plan.Relink: a stale link keeps serving the old version rather than dangling", p.Ops[0])
	}
	if op.RevPath != filepath.Join(newRev, pluginSkillsDir, "alpha") {
		t.Errorf("relinked to %q, want the new version directory", op.RevPath)
	}
	if len(links) != 1 {
		t.Errorf("links = %v, want the one it re-pointed", links)
	}
}

func TestFanUnlinksASkillThePluginNoLongerShips(t *testing.T) {
	cfg, _ := fanCfg(t)
	rev := pluginTree(t, t.TempDir(), "alpha")
	gone := filepath.Join(cfg.Targets[1].Dir, "beta")

	c := NewPlugin(&fakeClaude{}, cfg)
	r := state.Receipt{
		Name:    "superpowers",
		RevPath: rev,
		Links: []state.Link{
			{Target: "codex", Path: filepath.Join(cfg.Targets[1].Dir, "alpha")},
			{Target: "codex", Path: gone},
		},
	}

	p, links, _, err := c.fan(r, cfg.Targets)
	if err != nil {
		t.Fatalf("fan: %v", err)
	}

	var unlinked []string
	for _, op := range p.Ops {
		if u, ok := op.(plan.Unlink); ok {
			unlinked = append(unlinked, u.LinkPath)
		}
	}
	if len(unlinked) != 1 || unlinked[0] != gone {
		t.Fatalf("unlinked = %v, want just %q", unlinked, gone)
	}
	for _, l := range links {
		if l.Path == gone {
			t.Error("a skill the plugin stopped shipping stayed in the removal contract")
		}
	}
}

func TestFanSkipsANameSomethingElseAlreadyHolds(t *testing.T) {
	cfg, _ := fanCfg(t)
	rev := pluginTree(t, t.TempDir(), "alpha", "beta")

	if err := os.MkdirAll(cfg.Targets[1].Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	if err := os.Symlink(other, filepath.Join(cfg.Targets[1].Dir, "alpha")); err != nil {
		t.Fatal(err)
	}

	c := NewPlugin(&fakeClaude{}, cfg)
	r := state.Receipt{Name: "superpowers", RevPath: rev}

	p, links, skipped, err := c.fan(r, cfg.Targets)
	if err != nil {
		t.Fatalf("fan: %v", err)
	}
	if len(skipped) != 1 || !strings.Contains(skipped[0], "alpha") {
		t.Fatalf("skipped = %v, want one line naming alpha", skipped)
	}
	if len(p.Ops) != 1 {
		t.Fatalf("ops = %v, want beta linked anyway: one taken name must not cost the others", p.Describe())
	}
	for _, l := range links {
		if strings.HasSuffix(l.Path, "alpha") {
			t.Error("recorded a link to a symlink somebody else made")
		}
	}
}

func TestFanLeavesAgentsItWasNotAskedAboutAlone(t *testing.T) {
	cfg, _ := fanCfg(t)
	rev := pluginTree(t, t.TempDir(), "alpha")
	held := state.Link{Target: "gemini", Path: "/agents/gemini/skills/alpha"}

	c := NewPlugin(&fakeClaude{}, cfg)
	r := state.Receipt{Name: "superpowers", RevPath: rev, Links: []state.Link{held}}

	_, links, _, err := c.fan(r, cfg.Targets)
	if err != nil {
		t.Fatalf("fan: %v", err)
	}

	var kept bool
	for _, l := range links {
		if l == held {
			kept = true
		}
	}
	if !kept {
		t.Error("reconciling codex dropped gemini's link: an agent not in add keeps what it had")
	}
}

func TestFanTreatsAPluginWithNoSkillsDirectoryAsPublishingNone(t *testing.T) {
	cfg, _ := fanCfg(t)
	c := NewPlugin(&fakeClaude{}, cfg)
	r := state.Receipt{Name: "empty", RevPath: t.TempDir()}

	p, _, _, err := c.fan(r, cfg.Targets)
	if err != nil {
		t.Fatalf("a plugin that ships no skills has nothing to fan out, not an error: %v", err)
	}
	if !p.IsEmpty() {
		t.Errorf("ops = %v, want none", p.Describe())
	}
}

func TestFanRefusesAnInstallPathThatIsNotThere(t *testing.T) {
	cfg, _ := fanCfg(t)
	c := NewPlugin(&fakeClaude{}, cfg)
	r := state.Receipt{Name: "superpowers", RevPath: filepath.Join(t.TempDir(), "gone")}

	if _, _, _, err := c.fan(r, cfg.Targets); err == nil {
		t.Error("linking into a directory that is not there would make every link dangle")
	}
}
```

Add `os`, `path/filepath` and `strings` to the test file's imports if missing.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `make test 2>&1 | grep -A5 internal/channel`
Expected: FAIL — `c.fan undefined`, `pluginSkillsDir undefined`.

- [ ] **Step 3: Implement**

Add to `internal/channel/plugin.go`. Imports gain `os`, `path/filepath`, and `github.com/richardcase/skillsctl/internal/discover`.

```go
// pluginSkillsDir is where a plugin keeps the skills it publishes. Only this
// subdirectory is walked: a plugin's root also holds commands, hooks, agents and
// its own tests, and a SKILL.md in any of those is not a skill the plugin
// publishes.
const pluginSkillsDir = "skills"

// pluginSkill is one skill a plugin publishes, under the name it will take in an
// agent's skills directory.
type pluginSkill struct {
	name string
	dir  string
}

// skills reads what a plugin publishes, from the install path claude reported.
//
// A plugin with no skills directory publishes none, which is not an error: it
// has nothing to fan out rather than something that failed to.
func (c *Plugin) skills(r state.Receipt) ([]pluginSkill, error) {
	// target.Link would create a symlink whether or not anything is on the other
	// end of it, and a dangling entry in a skills directory is worse than a
	// refusal: the agent finds it, fails to load it, and says nothing useful.
	if fi, err := os.Stat(r.RevPath); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("refusing to link %s: %s is not a directory, so every link would dangle", r.Name, r.RevPath)
	}

	dir := filepath.Join(r.RevPath, pluginSkillsDir)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil, nil
	}

	found, err := discover.Walk(dir)
	if err != nil {
		return nil, fmt.Errorf("read the skills %s ships: %w", r.Name, err)
	}

	out := make([]pluginSkill, 0, len(found))
	for _, s := range found {
		// A skill's name arrives from a third party's SKILL.md and becomes a
		// path. A plugin shipping one that cannot be a path is malformed rather
		// than merely inconvenient, so it stops the fan-out instead of being
		// skipped like a name that is merely taken.
		name := s.Name
		if name == "" {
			name = filepath.Base(s.Dir)
		}
		if err := target.ValidateSkillName(name); err != nil {
			return nil, fmt.Errorf("%s ships a skill at %s that cannot be linked: %w", r.Name, s.Rel, err)
		}
		out = append(out, pluginSkill{name: name, dir: s.Dir})
	}
	return out, nil
}

// linkOpFor decides what to do about one intended link by looking at what is
// already at its path.
//
// The receipt cannot answer this on its own: a state.Link records where a
// symlink is, not what it points at, and by the time this runs RevPath has
// already moved on to whatever claude last installed. So the filesystem is the
// authority, and the receipt only says which links are ours to re-point.
func linkOpFor(t target.Target, linkPath, dest string, ours bool) (plan.Op, string) {
	got, err := os.Readlink(linkPath)
	switch {
	case os.IsNotExist(err):
		return plan.Link{Target: t.Name, LinkPath: linkPath, RevPath: dest}, ""
	case err != nil:
		return nil, fmt.Sprintf("skipped %s for %s: %s is not a symlink skillsctl can replace",
			filepath.Base(linkPath), t.Name, linkPath)
	case got == dest:
		return nil, ""
	case ours:
		return plan.Relink{Target: t.Name, LinkPath: linkPath, RevPath: dest}, ""
	default:
		return nil, fmt.Sprintf("skipped %s for %s: %s already points at %s",
			filepath.Base(linkPath), t.Name, linkPath, got)
	}
}

// fan reconciles one receipt's links for the agents in add, and is the whole of
// what "this plugin's skills reach that agent" means. Install and Link share it
// so there is one definition of it; they differ only in whether the receipt is
// one they are about to write or one that already exists.
//
// The links it returns are the receipt's complete new set, not only the ones for
// add: an agent this call is not reconciling keeps what it had.
func (c *Plugin) fan(r state.Receipt, add []target.Target) (plan.Plan, []state.Link, []string, error) {
	var p plan.Plan

	// An agent that installs plugins is never linked: it can already see the
	// skills, so a symlink into its own cache would be a second name for
	// something it has.
	fanTo := target.WithoutPlugins(add)
	if len(fanTo) == 0 {
		return p, r.Links, nil, nil
	}

	skills, err := c.skills(r)
	if err != nil {
		return plan.Plan{}, nil, nil, err
	}

	touched := make(map[string]bool, len(fanTo))
	for _, t := range fanTo {
		touched[t.Name] = true
	}
	recorded := make(map[string]bool, len(r.Links))
	for _, l := range r.Links {
		recorded[l.Path] = true
	}

	links := make([]state.Link, 0, len(r.Links))
	for _, l := range r.Links {
		if !touched[l.Target] {
			links = append(links, l)
		}
	}

	var skipped []string
	live := map[string]bool{}
	for _, t := range fanTo {
		for _, s := range skills {
			linkPath, err := linkPathFor(t, s.name)
			if err != nil {
				return plan.Plan{}, nil, nil, err
			}
			// Two skills in one plugin claiming one name would otherwise plan
			// two links at one path and record it twice, which Unlink would
			// then undo once.
			if live[linkPath] {
				skipped = append(skipped, fmt.Sprintf("skipped %s for %s: %s ships two skills under that name",
					s.name, t.Name, r.Name))
				continue
			}
			live[linkPath] = true

			op, why := linkOpFor(t, linkPath, s.dir, recorded[linkPath])
			if why != "" {
				skipped = append(skipped, why)
				// A path that is not ours to take is recorded only if it
				// already was. The receipt must not start claiming a symlink
				// somebody else made, and must not stop claiming one it made
				// that somebody has since replaced.
				if recorded[linkPath] {
					links = append(links, state.Link{Target: t.Name, Path: linkPath})
				}
				continue
			}
			if op != nil {
				p.Add(op)
			}
			links = append(links, state.Link{Target: t.Name, Path: linkPath})
		}
	}

	// A skill the plugin has stopped shipping leaves a link into a version
	// directory claude keeps forever and the agent loads happily. It is the
	// reason this is reconciliation rather than addition.
	for _, l := range r.Links {
		if touched[l.Target] && !live[l.Path] {
			p.Add(plan.Unlink{Target: l.Target, LinkPath: l.Path})
		}
	}
	return p, links, skipped, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `make test && make lint`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/channel
git commit -m "feat(plugin): reconcile a plugin's skills against an agent's links"
```

---

## Task 5: `Plugin.Link` and `Plugin.Agents`

**Files:**
- Modify: `internal/channel/plugin.go:222-233` (`Link`), `:246-256` (`Agents`)
- Test: `internal/channel/plugin_test.go`, `internal/channel/linked_test.go:165` (`TestPluginRefusesToLink` — this test asserts the behaviour being removed and must be replaced)

**Interfaces:**
- Consumes: `Plugin.fan` (Task 4).
- Produces: `Plugin.Link` now returns a real plan. `Plugin.Agents(r)` returns the config's plugin-capable agents followed by the receipt's link targets in config order, deduped.

- [ ] **Step 1: Write the failing tests**

Delete `TestPluginRefusesToLink` from `internal/channel/linked_test.go` — the refusal it asserts is what this task removes. Add to `internal/channel/plugin_test.go`:

```go
func TestPluginLinkPlansTheFanOutAndRecordsIt(t *testing.T) {
	cfg, _ := fanCfg(t)
	rev := pluginTree(t, t.TempDir(), "alpha", "beta")
	c := NewPlugin(&fakeClaude{}, cfg)
	r := state.Receipt{Name: "superpowers", RevPath: rev}

	p, skipped, err := c.Link(r, cfg.Targets)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if len(skipped) != 0 {
		t.Errorf("skipped = %v, want none", skipped)
	}

	rec, ok := p.Ops[len(p.Ops)-1].(plan.Record)
	if !ok {
		t.Fatalf("last op = %T, want plan.Record: the links are the removal contract", p.Ops[len(p.Ops)-1])
	}
	if len(rec.Receipt.Links) != 2 {
		t.Errorf("recorded links = %v, want one per skill", rec.Receipt.Links)
	}
}

func TestPluginLinkIsANoOpWhenTheAgentsAlreadyAgree(t *testing.T) {
	cfg, _ := fanCfg(t)
	rev := pluginTree(t, t.TempDir(), "alpha")
	linkPath := filepath.Join(cfg.Targets[1].Dir, "alpha")
	if err := os.MkdirAll(cfg.Targets[1].Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(rev, pluginSkillsDir, "alpha"), linkPath); err != nil {
		t.Fatal(err)
	}

	c := NewPlugin(&fakeClaude{}, cfg)
	r := state.Receipt{Name: "superpowers", RevPath: rev, Links: []state.Link{{Target: "codex", Path: linkPath}}}

	p, _, err := c.Link(r, cfg.Targets)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if !p.IsEmpty() {
		t.Errorf("ops = %v, want none: reconciling what already agrees must change nothing", p.Describe())
	}
}

func TestPluginAgentsCombinesTheOwnerAndTheLinkedAgents(t *testing.T) {
	cfg, _ := fanCfg(t)
	c := NewPlugin(&fakeClaude{}, cfg)

	if got := c.Agents(state.Receipt{}); len(got) != 1 || got[0] != "claude" {
		t.Errorf("Agents with no links = %v, want just the agent that installed it", got)
	}

	r := state.Receipt{Links: []state.Link{
		{Target: "codex", Path: "/x/alpha"},
		{Target: "codex", Path: "/x/beta"},
	}}
	got := c.Agents(r)
	if len(got) != 2 || got[0] != "claude" || got[1] != "codex" {
		t.Errorf("Agents = %v, want claude then codex, each once", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `make test 2>&1 | grep -A5 internal/channel`
Expected: FAIL — `Link` still returns the refusal error, and `Agents` returns only `claude`.

- [ ] **Step 3: Implement**

Replace `Plugin.Link` and `Plugin.Agents` in `internal/channel/plugin.go`:

```go
// Link makes the agents in add hold the skills this plugin ships.
//
// It is reconciliation rather than addition because the directory it links into
// moves: claude installs each version of a plugin beside the last and keeps the
// old one, so a link left alone would go on serving a version the receipt says
// was replaced. Install, update and link all reduce to this one call.
func (c *Plugin) Link(r state.Receipt, add []target.Target) (plan.Plan, []string, error) {
	p, links, skipped, err := c.fan(r, add)
	if err != nil {
		return plan.Plan{}, nil, err
	}
	if p.IsEmpty() {
		return p, skipped, nil
	}

	updated := r
	updated.Links = links
	updated.UpdatedAt = time.Now().UTC()
	p.Add(plan.Record{Receipt: updated})
	return p, skipped, nil
}

// Agents names the agents this plugin is live in: the one that installed it,
// which only the config knows, together with the ones its skills were linked
// into, which only the receipt knows. Neither answers alone any more — claude
// holds the plugin without a link, and codex holds links without being able to
// install one.
//
// The order is the config's, so that list's agents column does not depend on the
// order the links happened to be made in.
func (c *Plugin) Agents(r state.Receipt) []string {
	linked := make(map[string]bool, len(r.Links))
	for _, l := range r.Links {
		linked[l.Target] = true
	}

	names := make([]string, 0, len(c.cfg.Targets))
	for _, t := range c.cfg.Targets {
		if t.Plugins || linked[t.Name] {
			names = append(names, t.Name)
		}
	}
	return names
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `make test && make lint`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/channel
git commit -m "feat(plugin): link a plugin's skills into agents that cannot install it"
```

---

## Task 6: `Plugin.Remove` — partial removal and the refusal

**Files:**
- Modify: `internal/channel/plugin.go:207-244` (`Remove` and `named`)
- Test: `internal/channel/plugin_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `Remove` behaviour — empty `drop` uninstalls, unlinks everything and forgets; a `drop` naming a plugin-installing agent while any link exists is an error; a `drop` naming only linked agents unlinks those and re-records; a `drop` naming nobody relevant returns an empty plan.

- [ ] **Step 1: Write the failing tests**

Add to `internal/channel/plugin_test.go`:

```go
func fannedReceipt() state.Receipt {
	return state.Receipt{
		Name:    "superpowers",
		Channel: "plugin",
		Source:  "superpowers@claude-plugins-official",
		Links: []state.Link{
			{Target: "codex", Path: "/agents/codex/alpha"},
			{Target: "codex", Path: "/agents/codex/beta"},
		},
	}
}

func TestPluginRemoveWithNoAgentUninstallsAndUnlinksEverything(t *testing.T) {
	c, _ := newPluginChannel()

	p, err := c.Remove(fannedReceipt(), nil)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}

	var execs, unlinks, forgets int
	for _, op := range p.Ops {
		switch op.(type) {
		case plan.Exec:
			execs++
		case plan.Unlink:
			unlinks++
		case plan.Forget:
			forgets++
		}
	}
	if execs != 1 || unlinks != 2 || forgets != 1 {
		t.Errorf("plan = %v, want one uninstall, both links taken away and the receipt forgotten", p.Describe())
	}
}

func TestPluginRemoveFromALinkedAgentKeepsTheReceipt(t *testing.T) {
	c, _ := newPluginChannel()

	p, err := c.Remove(fannedReceipt(), map[string]bool{"codex": true})
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	for _, op := range p.Ops {
		if _, ok := op.(plan.Forget); ok {
			t.Fatal("forgot a receipt for a plugin claude still has installed")
		}
		if _, ok := op.(plan.Exec); ok {
			t.Fatal("uninstalled the plugin when only a linked agent was named")
		}
	}

	rec, ok := p.Ops[len(p.Ops)-1].(plan.Record)
	if !ok {
		t.Fatalf("last op = %T, want plan.Record", p.Ops[len(p.Ops)-1])
	}
	if len(rec.Receipt.Links) != 0 {
		t.Errorf("kept links = %v, want none", rec.Receipt.Links)
	}
}

func TestPluginRemoveFromTheOwningAgentIsRefusedWhileLinksExist(t *testing.T) {
	c, _ := newPluginChannel()

	_, err := c.Remove(fannedReceipt(), map[string]bool{"claude": true})
	if err == nil {
		t.Fatal("uninstalling the plugin would strand codex's links, so -a claude must be refused")
	}
	if !strings.Contains(err.Error(), "skillsctl remove superpowers") {
		t.Errorf("error = %q, want it to name the command that does mean everywhere", err)
	}
}

func TestPluginRemoveFromTheOwningAgentStillWorksWithNoLinks(t *testing.T) {
	c, _ := newPluginChannel()
	r := fannedReceipt()
	r.Links = nil

	p, err := c.Remove(r, map[string]bool{"claude": true})
	if err != nil {
		t.Fatalf("with nothing to strand there is nothing to refuse: %v", err)
	}
	if p.IsEmpty() {
		t.Error("plan is empty, want the uninstall")
	}
}

func TestPluginRemoveFromAnAgentThatHasNothingPlansNothing(t *testing.T) {
	c, _ := newPluginChannel()

	p, err := c.Remove(fannedReceipt(), map[string]bool{"gemini": true})
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !p.IsEmpty() {
		t.Errorf("plan = %v, want none: the caller reports what the user typed", p.Describe())
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `make test 2>&1 | grep -A5 internal/channel`
Expected: FAIL — `Remove` uninstalls unconditionally and never unlinks.

- [ ] **Step 3: Implement**

Replace `Remove` and `named` in `internal/channel/plugin.go`:

```go
// Remove undoes the receipt for the agents in drop. An empty drop means every
// agent: uninstall the plugin through claude, take every link away, forget it.
//
// A subset is two contracts rather than one, so which agents were named decides
// which applies. An agent that only holds links loses them and the receipt
// stays, because the plugin is still installed. The agent that installed it
// cannot be singled out while anything is linked: uninstalling deletes the
// directory every other agent's links point into, so -a claude would have to
// either strand them or silently do more than the user asked for. Naming the
// command that does mean "everywhere" is better than either.
//
// The uninstall goes first so that a claude which refuses stops the whole plan
// with the receipt intact. Exec cannot be rolled back, so an unlink that fails
// after it still leaves the receipt uncommitted and the run reporting why.
func (c *Plugin) Remove(r state.Receipt, drop map[string]bool) (plan.Plan, error) {
	var p plan.Plan

	if len(drop) == 0 {
		p.Add(plan.Exec{Argv: c.claude.UninstallArgv(r.Source)})
		for _, l := range r.Links {
			p.Add(plan.Unlink{Target: l.Target, LinkPath: l.Path})
		}
		p.Add(plan.Forget{Name: r.Name})
		return p, nil
	}

	if owner := c.owner(drop); owner != "" {
		if len(r.Links) > 0 {
			return plan.Plan{}, fmt.Errorf("%s owns the %s plugin, and uninstalling it would strand its skills in %s\n"+
				"run `skillsctl remove %s` to remove it everywhere",
				owner, r.Name, strings.Join(linkedAgents(r), ", "), r.Name)
		}
		p.Add(plan.Exec{Argv: c.claude.UninstallArgv(r.Source)})
		p.Add(plan.Forget{Name: r.Name})
		return p, nil
	}

	var keep []state.Link
	for _, l := range r.Links {
		if !drop[l.Target] {
			keep = append(keep, l)
			continue
		}
		p.Add(plan.Unlink{Target: l.Target, LinkPath: l.Path})
	}
	if p.IsEmpty() {
		return p, nil
	}

	// The receipt survives however many links go, because the plugin itself is
	// still installed for the agent that owns it.
	updated := r
	updated.Links = keep
	updated.UpdatedAt = time.Now().UTC()
	p.Add(plan.Record{Receipt: updated})
	return p, nil
}

// owner names the plugin-installing agent among those the user asked to remove
// from, or "" if none was named.
func (c *Plugin) owner(drop map[string]bool) string {
	for _, t := range target.WithPlugins(c.cfg.Targets) {
		if drop[t.Name] {
			return t.Name
		}
	}
	return ""
}

// linkedAgents names the agents a receipt reaches by symlink, each once and in
// the order the links were made.
func linkedAgents(r state.Receipt) []string {
	seen := map[string]bool{}
	names := make([]string, 0, len(r.Links))
	for _, l := range r.Links {
		if seen[l.Target] {
			continue
		}
		seen[l.Target] = true
		names = append(names, l.Target)
	}
	return names
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `make test && make lint`
Expected: PASS. If an older test asserted the all-or-nothing `Remove`, update it — the behaviour it pinned is what this task changes.

- [ ] **Step 5: Commit**

```bash
git add internal/channel
git commit -m "feat(plugin): remove a plugin from one linked agent without uninstalling it"
```

---

## Task 7: `Plugin.Install` — exact when adopted, honest when not

**Files:**
- Modify: `internal/channel/plugin.go:99-129`
- Test: `internal/channel/plugin_test.go`

**Interfaces:**
- Consumes: `Plugin.fan` (Task 4), `plan.Note` (Task 1), `target.WithoutPlugins` (Task 3).
- Produces: no signature change. `Install` emits the fan-out ops inline when the candidate is adopted, and a single `plan.Note` when it is not.

- [ ] **Step 1: Write the failing tests**

Add to `internal/channel/plugin_test.go`:

```go
func TestPluginInstallOfAnAdoptedPluginPlansTheLinksExactly(t *testing.T) {
	cfg, _ := fanCfg(t)
	rev := pluginTree(t, t.TempDir(), "alpha", "beta")
	src, err := source.Parse("superpowers@claude-plugins-official")
	if err != nil {
		t.Fatal(err)
	}
	c := NewPlugin(&fakeClaude{installed: []claudex.Installed{{
		ID: "superpowers@claude-plugins-official", Version: "6.3.0", InstallPath: rev,
	}}}, cfg)
	req := Request{Source: src, Targets: cfg.Targets}

	cands, err := c.Prepare(context.Background(), req)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	p, receipts, err := c.Install(req, cands)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	var links, notes int
	for _, op := range p.Ops {
		switch op.(type) {
		case plan.Link:
			links++
		case plan.Note:
			notes++
		}
	}
	if links != 2 {
		t.Errorf("plan = %v, want a link per skill: the install path is known, so the dry run is exact", p.Describe())
	}
	if notes != 0 {
		t.Error("a note admits something unknown, and nothing here is unknown")
	}
	if len(receipts[0].Links) != 2 {
		t.Errorf("receipt links = %v, want one per skill", receipts[0].Links)
	}
}

func TestPluginInstallOfAFreshPluginNotesTheLinksItCannotYetName(t *testing.T) {
	cfg, _ := fanCfg(t)
	c := NewPlugin(&fakeClaude{}, cfg)
	src, err := source.Parse("superpowers@claude-plugins-official")
	if err != nil {
		t.Fatal(err)
	}
	req := Request{Source: src, Targets: cfg.Targets}

	cands, err := c.Prepare(context.Background(), req)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	p, _, err := c.Install(req, cands)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	var note string
	for _, op := range p.Ops {
		if n, ok := op.(plan.Note); ok {
			note = n.Text
		}
	}
	if !strings.Contains(note, "codex") {
		t.Errorf("note = %q, want it to name the agent the links are coming for", note)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `make test 2>&1 | grep -A5 internal/channel`
Expected: FAIL — no `plan.Link` and no `plan.Note` in the install plan.

- [ ] **Step 3: Implement**

Replace the body of the loop in `Plugin.Install`:

```go
	for _, s := range chosen {
		if !s.Adopted {
			p.Add(plan.Exec{Argv: c.claude.InstallArgv(id)})
		}

		// No slug and no content hash: nothing of ours is in the store. Resolved
		// and RevPath stay empty until Settle unless the plugin was already
		// installed, in which case they are known now and the plan describes
		// itself exactly.
		receipt := state.Receipt{
			Name:        s.Name,
			Channel:     string(source.ChannelPlugin),
			Source:      id,
			Resolved:    s.Version,
			RevPath:     s.Path,
			InstalledAt: now,
			UpdatedAt:   now,
		}

		// A plugin claude already has is the one case where the install path is
		// known before the plan runs, so its links go in the plan and the dry run
		// is exact. Otherwise claude decides the path and Settle reads it back,
		// and the reconcile that follows the apply is where the links are made —
		// which the plan says rather than leaving a third of its work unmentioned.
		if s.Adopted {
			ops, links, skipped, err := c.fan(receipt, req.Targets)
			if err != nil {
				return plan.Plan{}, nil, err
			}
			p.Add(ops.Ops...)
			for _, why := range skipped {
				p.Add(plan.Note{Text: why})
			}
			receipt.Links = links
		} else if fanTo := target.WithoutPlugins(req.Targets); len(fanTo) > 0 {
			p.Add(plan.Note{Text: fmt.Sprintf("then link the skills %s ships into %s, once claude reports where it put them",
				s.Name, strings.Join(targetNames(fanTo), ", "))})
		}

		p.Add(plan.Record{Receipt: receipt})
		receipts = append(receipts, receipt)
	}
```

And add, beside `linkedAgents`:

```go
// targetNames is the agents' names, for a message.
func targetNames(ts []target.Target) []string {
	names := make([]string, 0, len(ts))
	for _, t := range ts {
		names = append(names, t.Name)
	}
	return names
}
```

Note the adopted branch discards nothing: the skips become notes so a dry run shows them, and the reconcile after the apply reports them again as skipped lines.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `make test && make lint`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/channel
git commit -m "feat(plugin): plan the fan-out inline for a plugin claude already has"
```

---

## Task 8: The shared post-settle reconcile

**Files:**
- Create: `internal/cli/relink.go`
- Modify: `internal/cli/install.go:140-165`
- Modify: `internal/cli/plugin_test.go:49-88` (make `fakePlugins` produce a real install path)
- Test: `internal/cli/plugin_test.go`

**Interfaces:**
- Consumes: `Channel.Link` (Task 2), `Plugin.Link` (Task 5).
- Produces: `func relink(ctx context.Context, ex *plan.Executor, ch channel.Channel, rs []state.Receipt, targetsFor func(state.Receipt) []target.Target) ([]state.Receipt, []string, error)` — returns `rs` with reconciled receipts replaced, the skip reasons, and the first error.

- [ ] **Step 1: Make the fake produce a real install path**

`fakePlugins` currently reports `InstallPath: "/plugins/…"`, which is not on disk, so nothing can be linked out of it. Give it a root and a skill list. In `internal/cli/plugin_test.go`, add fields and change `exec`:

```go
type fakePlugins struct {
	installed []claudex.Installed
	next      string
	listErr   error
	calls     int

	// root is a real directory each install path is built under, and skills
	// names the skills a plugin ships there. Without a tree on disk there is
	// nothing to fan out, so the fake builds one.
	root   string
	skills []string
}
```

Then change the `install`/`update` arm of `exec` to build that tree and report its path:

```go
	case "install", "update":
		version := f.next
		if version == "" {
			version = "1.0.0"
		}
		path, err := f.tree(id, version)
		if err != nil {
			return err
		}
		f.put(claudex.Installed{
			ID: id, Version: version, Scope: "user", Enabled: true,
			InstallPath: path,
		})
```

```go
// tree writes the skills directory claude would have unpacked, so a reconcile
// has something real to link at.
func (f *fakePlugins) tree(id, version string) (string, error) {
	dir := filepath.Join(f.root, strings.ReplaceAll(id, "@", "-"), version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	for _, n := range f.skills {
		sd := filepath.Join(dir, "skills", n)
		if err := os.MkdirAll(sd, 0o755); err != nil {
			return "", err
		}
		body := "---\nname: " + n + "\ndescription: a skill\n---\n\nBody.\n"
		if err := os.WriteFile(filepath.Join(sd, "SKILL.md"), []byte(body), 0o644); err != nil {
			return "", err
		}
	}
	return dir, nil
}
```

`exec` has no `*testing.T`, which is why `tree` returns errors rather than
calling `t.Fatal`. Add `os`, `path/filepath` and `strings` to the file's imports.

In `newHarness` (`internal/cli/cli_test.go:41`), give the fake a root:

```go
		plugins: &fakePlugins{root: filepath.Join(root, "plugins")},
```

A test that wants a fan-out sets `h.plugins.skills = []string{"alpha", "beta"}` before running. Tests that leave `skills` empty get a plugin that ships none, which is the existing behaviour and keeps every current plugin test passing.

- [ ] **Step 2: Write the failing test**

Add to `internal/cli/plugin_test.go`:

```go
func TestInstallPluginFansItsSkillsOutToCodex(t *testing.T) {
	h := newHarness(t)
	h.plugins.skills = []string{"alpha", "beta"}

	out, err := h.run(t, "install", pluginID)
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	for _, name := range []string{"alpha", "beta"} {
		dest, rerr := os.Readlink(filepath.Join(h.codex, name))
		if rerr != nil {
			t.Fatalf("codex has no link for %s: %v", name, rerr)
		}
		if _, serr := os.Stat(filepath.Join(dest, "SKILL.md")); serr != nil {
			t.Errorf("%s -> %s does not hold a SKILL.md", name, dest)
		}
	}

	// claude installed the plugin and can already see it, so there is nothing
	// of ours in its skills directory.
	if _, serr := os.Stat(filepath.Join(h.claude, "alpha")); !os.IsNotExist(serr) {
		t.Error("linked into claude, which can already see the plugin's skills")
	}

	links := h.receipts(t)["superpowers"]["links"].([]any)
	if len(links) != 2 {
		t.Errorf("recorded links = %v, want one per skill: they are the removal contract", links)
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `make test 2>&1 | grep -A10 TestInstallPluginFansItsSkillsOutToCodex`
Expected: FAIL — no link in `h.codex`.

- [ ] **Step 4: Implement `relink`**

Create `internal/cli/relink.go`:

```go
package cli

import (
	"context"

	"github.com/richardcase/skillsctl/internal/channel"
	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/target"
)

// relink makes each receipt's links agree with the receipt, after the plan that
// wrote it has run, and returns rs with the reconciled receipts replaced.
//
// It runs for every channel, which is what keeps install and update free of a
// branch on which one they are serving. A channel that made its links in its own
// plan finds them already right and returns an empty plan, so this costs it
// nothing; a channel whose agent chose the directory only knows where to point
// now, and this is where its links are made.
//
// An error here is not fatal to the command that called it, for the same reason
// a settle's is not: the plugin is installed either way, and a receipt that
// records it is worth more than one nothing wrote.
func relink(
	ctx context.Context,
	ex *plan.Executor,
	ch channel.Channel,
	rs []state.Receipt,
	targetsFor func(state.Receipt) []target.Target,
) ([]state.Receipt, []string, error) {
	out := make([]state.Receipt, 0, len(rs))
	var skipped []string
	var firstErr error

	for _, r := range rs {
		p, skips, err := ch.Link(r, targetsFor(r))
		skipped = append(skipped, skips...)
		if err == nil && !p.IsEmpty() {
			err = ex.Apply(ctx, p)
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			out = append(out, r)
			continue
		}

		// The plan's Record is what holds the new links, so the reconciled
		// receipt is read back from where the executor put it rather than
		// rebuilt here.
		if got, ok := ex.DB.Receipts[r.Name]; ok {
			out = append(out, *got)
			continue
		}
		out = append(out, r)
	}
	return out, skipped, firstErr
}
```

- [ ] **Step 5: Wire it into install**

In `internal/cli/install.go`, after the `settle` call (line 143) and before `h.Commit()`:

```go
	receipts, serr := settle(ctx, ex, ch, receipts)

	// The links come last, because for a channel whose agent chooses the
	// directory this is the first moment there is a directory to point at.
	receipts, linkSkips, lerr := relink(ctx, ex, ch, receipts, func(state.Receipt) []target.Target {
		return targets
	})
```

Then extend the reporting tail:

```go
	reportSkipped(cmd, skipped)
	reportSkipped(cmd, linkSkips)
	if serr != nil {
		return serr
	}
	if lerr != nil {
		return lerr
	}
	if err := skippedErr(skipped, chosen); err != nil {
		return err
	}
	if len(linkSkips) > 0 {
		return partialf("%s could not be linked", count(len(linkSkips), "skill"))
	}
	return nil
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `make test && make lint`
Expected: PASS, including every pre-existing plugin, install and link test.

- [ ] **Step 7: Commit**

```bash
git add internal/cli
git commit -m "feat(install): fan a plugin's skills out to every agent it named"
```

---

## Task 9: `update` reconciles the links too

**Files:**
- Modify: `internal/cli/update.go:64-94` (the apply block), `:103-143` (`settleUpdated`)
- Test: `internal/cli/plugin_test.go`

**Interfaces:**
- Consumes: `relink` (Task 8).
- Produces: `settleUpdated` returns `([]update.Entry, []string, error)`; new helper `func linkedTargets(cfg target.Config, r state.Receipt) []target.Target`.

- [ ] **Step 1: Write the failing test**

Add to `internal/cli/plugin_test.go`:

```go
func TestUpdatePluginRepointsCodexAtTheNewVersion(t *testing.T) {
	h := newHarness(t)
	h.plugins.skills = []string{"alpha"}

	if out, err := h.run(t, "install", pluginID); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	before, err := os.Readlink(filepath.Join(h.codex, "alpha"))
	if err != nil {
		t.Fatal(err)
	}

	h.plugins.next = "2.0.0"
	if out, err := h.run(t, "update"); err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}

	after, err := os.Readlink(filepath.Join(h.codex, "alpha"))
	if err != nil {
		t.Fatalf("codex lost its link across the update: %v", err)
	}
	if after == before {
		t.Fatalf("link still points at %s: claude keeps the old version directory, so a stale link "+
			"goes on serving it rather than dangling", before)
	}
	if !strings.Contains(after, "2.0.0") {
		t.Errorf("link points at %q, want the 2.0.0 directory", after)
	}
}

func TestUpdatePluginUnlinksASkillItStoppedShipping(t *testing.T) {
	h := newHarness(t)
	h.plugins.skills = []string{"alpha", "beta"}

	if out, err := h.run(t, "install", pluginID); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	h.plugins.next = "2.0.0"
	h.plugins.skills = []string{"alpha"}
	if out, err := h.run(t, "update"); err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}

	if _, err := os.Lstat(filepath.Join(h.codex, "beta")); !os.IsNotExist(err) {
		t.Error("beta is still linked into codex, pointing into a version directory nothing will ever collect")
	}
	links := h.receipts(t)["superpowers"]["links"].([]any)
	if len(links) != 1 {
		t.Errorf("recorded links = %v, want only alpha", links)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `make test 2>&1 | grep -A10 TestUpdatePluginRepoints`
Expected: FAIL — the link still points at the 1.0.0 directory.

- [ ] **Step 3: Implement**

Add to `internal/cli/update.go`:

```go
// linkedTargets is the agents a receipt already reaches, as targets, for the
// reconcile that follows an update.
//
// An update re-points what is there; it does not fan out further. An agent that
// has since been deleted from the config is passed over rather than unlinked:
// config drift is not this command's business.
func linkedTargets(cfg target.Config, r state.Receipt) []target.Target {
	held := make(map[string]bool, len(r.Links))
	for _, l := range r.Links {
		held[l.Target] = true
	}

	var out []target.Target
	for _, t := range cfg.Targets {
		if held[t.Name] {
			out = append(out, t)
		}
	}
	return out
}
```

Change `settleUpdated`'s signature and tail:

```go
func settleUpdated(ctx context.Context, ex *plan.Executor, e *env, db *state.DB, entries []update.Entry) ([]update.Entry, []string, error) {
```

and, inside the per-channel loop, reconcile each group right after it settles:

```go
	var settled []state.Receipt
	var skipped []string
	var firstErr error
	for _, name := range order {
		ch, err := reg.For(source.Channel(name))
		if err != nil {
			continue
		}
		got, err := settle(ctx, ex, ch, grouped[name])
		if err != nil && firstErr == nil {
			firstErr = err
		}

		// The links follow the settle for the same reason they follow an
		// install: a channel whose agent chose the directory has only now been
		// told where the new one is.
		got, skips, lerr := relink(ctx, ex, ch, got, func(r state.Receipt) []target.Target {
			return linkedTargets(e.cfg, r)
		})
		skipped = append(skipped, skips...)
		if lerr != nil && firstErr == nil {
			firstErr = lerr
		}

		settled = append(settled, got...)
	}
	return update.Reconcile(entries, settled), skipped, firstErr
}
```

At the call site (`update.go:74`):

```go
					entries, linkSkips, serr = settleUpdated(cmd.Context(), ex, e, h.DB, entries)
```

Declare `var linkSkips []string` beside `var serr error`, and report them with the rest:

```go
				reportUpdate(cmd, entries, dryRun)
				reportSkipped(cmd, linkSkips)
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `make test && make lint`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli
git commit -m "feat(update): re-point a plugin's links at the version claude installed"
```

---

## Task 10: `link <name> -a <agent>` for a plugin

**Files:**
- Modify: `internal/cli/link.go:124-141` (the partition and the `Link` call), `:180-198` (`partitionLinked`)
- Test: `internal/cli/plugin_test.go`

**Interfaces:**
- Consumes: `Plugin.Agents` (Task 5), the widened `Channel.Link` (Task 2).
- Produces: `func partitionLinked(held []string, targets []target.Target) (add []target.Target, already []string)` — now takes the agent names a channel says it is live in, not the receipt's links.

- [ ] **Step 1: Write the failing test**

Add to `internal/cli/plugin_test.go`:

```go
func TestLinkPluginIntoAnAgentThatWasNotThereAtInstallTime(t *testing.T) {
	h := newHarness(t)
	h.plugins.skills = []string{"alpha"}

	if out, err := h.run(t, "install", pluginID, "-a", "claude"); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if _, err := os.Lstat(filepath.Join(h.codex, "alpha")); !os.IsNotExist(err) {
		t.Fatal("codex was not named, so it should hold nothing yet")
	}

	out, err := h.run(t, "link", "superpowers", "-a", "codex")
	if err != nil {
		t.Fatalf("link: %v\n%s", err, out)
	}
	if _, err := os.Readlink(filepath.Join(h.codex, "alpha")); err != nil {
		t.Errorf("codex has no link for alpha: %v", err)
	}
}

func TestLinkPluginIntoTheAgentThatOwnsItSaysItAlreadyHasIt(t *testing.T) {
	h := newHarness(t)
	h.plugins.skills = []string{"alpha"}

	if out, err := h.run(t, "install", pluginID); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	out, err := h.run(t, "link", "superpowers", "-a", "claude")
	if err == nil {
		t.Fatalf("claude can already see the plugin, so there is nothing to add\n%s", out)
	}
	if !strings.Contains(out+err.Error(), "already linked into claude") {
		t.Errorf("output = %q / err = %v, want it to say claude already has it", out, err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `make test 2>&1 | grep -A10 TestLinkPluginInto`
Expected: FAIL — `-a codex` produces no link; `-a claude` produces an empty plan rather than the "already linked" message.

- [ ] **Step 3: Implement**

In `internal/cli/link.go`, replace the partition call and the `Link` call:

```go
	// What counts as "already has it" is the channel's answer, not the
	// receipt's links: claude holds a plugin without a link of ours, so a
	// receipt that records none is not an agent that has nothing.
	add, already := partitionLinked(ch.Agents(*receipt), targets)
	asked := len(targets)
	if len(add) == 0 {
		return fmt.Errorf("%s is already linked into %s", name, strings.Join(already, ", "))
	}
```

```go
	p, linkSkips, err := ch.Link(*receipt, add)
	if err != nil {
		return err
	}
```

Replace `partitionLinked`:

```go
// partitionLinked splits the requested targets into the ones to link and the
// ones the channel says already have it, so that the caller can report the
// second group by name. Link skips them too, but only the caller knows what was
// asked for and so which ones are worth mentioning.
func partitionLinked(held []string, targets []target.Target) (add []target.Target, already []string) {
	has := make(map[string]bool, len(held))
	for _, name := range held {
		has[name] = true
	}

	for _, t := range targets {
		if has[t.Name] {
			already = append(already, t.Name)
			continue
		}
		add = append(add, t)
	}
	return add, already
}
```

Report the skips in both the dry-run and the real branch, beside `reportAlreadyLinked`:

```go
	if o.dryRun {
		for _, line := range p.Describe() {
			cmd.Println(line)
		}
		reportSkipped(cmd, linkSkips)
		reportAlreadyLinked(cmd, name, already)
		return alreadyLinkedErr(already, asked)
	}
```

and after the success line:

```go
	cmd.Printf("linked %s into %s\n", name, strings.Join(names(add), ", "))
	reportSkipped(cmd, linkSkips)
	reportAlreadyLinked(cmd, name, already)
	if err := alreadyLinkedErr(already, asked); err != nil {
		return err
	}
	if len(linkSkips) > 0 {
		return partialf("%s could not be linked", count(len(linkSkips), "skill"))
	}
	return nil
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `make test && make lint`
Expected: PASS. Existing git-channel link tests are unaffected: `linked.Agents` returns the link targets, which is what `partitionLinked` used to read off the receipt.

- [ ] **Step 5: Commit**

```bash
git add internal/cli
git commit -m "feat(link): link a plugin's skills into another agent"
```

---

## Task 11: `adopt` stops offering our own links back

**Files:**
- Modify: `internal/adopt/adopt.go:172-178` (the lookup in `classify`), plus a new helper
- Modify: `internal/adopt/adopt.go:287-289` (the keyed-by-target comment)
- Test: `internal/adopt/adopt_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `func claiming(db *state.DB, path string) (*state.Receipt, bool)` — the receipt that records a link at this exact path.

- [ ] **Step 1: Write the failing test**

Add to `internal/adopt/adopt_test.go`, matching the file's existing fixture style for building a `state.DB` and a target directory:

```go
// A plugin's link is named after the skill and its receipt is named after the
// plugin, so a lookup by name misses it. What follows is worse than a miss: the
// link resolves to a real directory holding a SKILL.md that is not in the store,
// and a plugin's cache directory is a git checkout, so promote would offer to
// adopt skillsctl's own link as a new git skill.
func TestScanTreatsAFannedOutPluginLinkAsManaged(t *testing.T) {
	agents := t.TempDir()
	dir := filepath.Join(agents, "codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	rev := filepath.Join(t.TempDir(), "skills", "brainstorming")
	if err := os.MkdirAll(rev, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rev, "SKILL.md"),
		[]byte("---\nname: brainstorming\ndescription: d\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	linkPath := filepath.Join(dir, "brainstorming")
	if err := os.Symlink(rev, linkPath); err != nil {
		t.Fatal(err)
	}

	db := &state.DB{Receipts: map[string]*state.Receipt{
		"superpowers": {
			Name:    "superpowers",
			Channel: "plugin",
			Source:  "superpowers@claude-plugins-official",
			Links:   []state.Link{{Target: "codex", Path: linkPath}},
		},
	}}

	rep, err := Scan(context.Background(), []target.Target{{Name: "codex", Dir: dir}}, db, fakeGit{}, emptyStore(t))
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(rep.Entries) != 1 {
		t.Fatalf("entries = %v, want one", rep.Entries)
	}
	if rep.Entries[0].Class != ClassManaged {
		t.Errorf("class = %v, want ClassManaged: a link a receipt records is ours, whatever it is named",
			rep.Entries[0].Class)
	}
	if len(rep.Adoptions()) != 0 {
		t.Error("offered skillsctl's own link for adoption")
	}
}
```

Reuse whatever the file already provides for the git and store arguments — check the top of `adopt_test.go` for the existing fake git and store helpers and use those names instead of `fakeGit{}` / `emptyStore(t)`.

- [ ] **Step 2: Run the test to verify it fails**

Run: `make test 2>&1 | grep -A10 TestScanTreatsAFannedOut`
Expected: FAIL — class is `ClassGit` or `ClassLocal`, and `Adoptions()` is non-empty.

- [ ] **Step 3: Implement**

In `internal/adopt/adopt.go`, inside `classify`, replace the receipt lookup:

```go
	// A link is identified by the path it sits at, not by the name of the
	// directory it sits in. One receipt can hold many links under many names —
	// a plugin puts one per skill it ships into each agent — so the question is
	// whether any receipt records this exact path. Asking whether one is named
	// after it is only the common case of that, and asking it first would let a
	// fanned-out plugin link fall through to the git and local checks below and
	// be offered back to the user as something new.
	if r, ok := claiming(db, e.Path); ok {
		return managed(e, r)
	}
	if r, ok := db.Receipts[name]; ok {
		return managed(e, r)
	}
```

And add, beside `agents`:

```go
// claiming finds the receipt that records a link at this path.
//
// It scans rather than consulting an index: the receipts are a map held in
// memory and a skills directory has tens of entries, so the cost is not worth a
// second structure that could disagree with the first.
func claiming(db *state.DB, path string) (*state.Receipt, bool) {
	for _, r := range db.Receipts {
		for _, l := range r.Links {
			if l.Path == path {
				return r, true
			}
		}
	}
	return nil, false
}
```

Finally, correct the stale comment at `adopt.go:287-289` to match `linked.go`:

```go
	// Links is a set keyed by the path a link sits at: Unlink treats a missing
	// link as success, so two entries naming one path would plan two unlinks of
	// it and swallow the second. A receipt this entry reached by name has one
	// link per agent, so a target already there is that same collision.
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `make test && make lint`
Expected: PASS, including the existing second-link tests — `managed`'s first loop already returned `ClassManaged` for an exact path match, so `claiming` reaches the same answer sooner.

- [ ] **Step 5: Commit**

```bash
git add internal/adopt
git commit -m "fix(adopt): find a receipt by link path before by name"
```

---

## Task 12: `outdated` reports a plugin whose install path moved

**Files:**
- Modify: `internal/outdated/outdated.go`
- Modify: `internal/cli/outdated.go:41`
- Test: `internal/outdated/outdated_test.go`, `internal/cli/plugin_test.go`

**Interfaces:**
- Consumes: `claudex.Plugins`.
- Produces: `outdated.StatusStale Status = "stale"`; `func Check(ctx context.Context, g gitx.Git, p claudex.Plugins, receipts []*state.Receipt) []Entry`.

- [ ] **Step 1: Write the failing tests**

In `internal/outdated/outdated_test.go`, add a fake and a test. Update every existing `Check(...)` call in the file to pass the new argument.

```go
type fakePlugins struct {
	installed []claudex.Installed
	err       error
	calls     int
}

func (f *fakePlugins) List(context.Context) ([]claudex.Installed, error) {
	f.calls++
	return f.installed, f.err
}
func (f *fakePlugins) InstallArgv(string) []string   { return nil }
func (f *fakePlugins) UninstallArgv(string) []string { return nil }
func (f *fakePlugins) UpdateArgv(string) []string    { return nil }

func TestCheckReportsAPluginWhoseInstallPathMoved(t *testing.T) {
	receipts := []*state.Receipt{{
		Name: "superpowers", Channel: "plugin",
		Source: "superpowers@claude-plugins-official",
		Resolved: "6.3.0", RevPath: "/cache/superpowers/6.3.0",
	}}
	p := &fakePlugins{installed: []claudex.Installed{{
		ID: "superpowers@claude-plugins-official", Version: "6.4.0",
		InstallPath: "/cache/superpowers/6.4.0",
	}}}

	got := Check(context.Background(), nil, p, receipts)
	if len(got) != 1 {
		t.Fatalf("entries = %v, want one", got)
	}
	if got[0].Status != StatusStale {
		t.Errorf("status = %q, want %q: claude has moved on and the links point at the old directory",
			got[0].Status, StatusStale)
	}
	if got[0].Latest != "6.4.0" {
		t.Errorf("latest = %q, want the version claude has now", got[0].Latest)
	}
}

func TestCheckReportsAPluginThatHasNotMovedAsCurrent(t *testing.T) {
	receipts := []*state.Receipt{{
		Name: "superpowers", Channel: "plugin",
		Source: "superpowers@claude-plugins-official",
		Resolved: "6.3.0", RevPath: "/cache/superpowers/6.3.0",
	}}
	p := &fakePlugins{installed: []claudex.Installed{{
		ID: "superpowers@claude-plugins-official", Version: "6.3.0",
		InstallPath: "/cache/superpowers/6.3.0",
	}}}

	if got := Check(context.Background(), nil, p, receipts); got[0].Status != StatusCurrent {
		t.Errorf("status = %q, want %q", got[0].Status, StatusCurrent)
	}
}

func TestCheckDoesNotAskClaudeWhenNoPluginIsInstalled(t *testing.T) {
	receipts := []*state.Receipt{{Name: "demo", Channel: "local", Source: "/x"}}
	p := &fakePlugins{}

	Check(context.Background(), nil, p, receipts)
	if p.calls != 0 {
		t.Errorf("List was called %d times: outdated must not shell out for a store with no plugins", p.calls)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `make test 2>&1 | grep -A5 internal/outdated`
Expected: FAIL — `too many arguments`, `undefined: StatusStale`.

- [ ] **Step 3: Implement**

In `internal/outdated/outdated.go`, add the status:

```go
	// StatusStale means the agent that owns the files has moved on: the version
	// or the install path it reports is not the one the receipt records, so the
	// links skillsctl made point into a directory it has replaced.
	StatusStale Status = "stale"
```

Widen `Check` and add the plugin branch. The whole per-receipt head becomes:

```go
// Check resolves each receipt's tracked ref against its remote, and each
// plugin's recorded install against what its agent has now.
//
// p is consulted lazily, so a store holding no plugins never shells out to
// claude and the package keeps its promise that it fetches nothing.
func Check(ctx context.Context, g gitx.Git, p claudex.Plugins, receipts []*state.Receipt) []Entry {
	seen := map[string]resolution{}
	entries := make([]Entry, 0, len(receipts))

	var installed []claudex.Installed
	var listErr error
	var listed bool
	plugins := func() ([]claudex.Installed, error) {
		if !listed {
			listed = true
			installed, listErr = p.List(ctx)
		}
		return installed, listErr
	}

	for _, r := range receipts {
		e := Entry{
			Name:    r.Name,
			Channel: r.Channel,
			Source:  r.Source,
			Current: r.Resolved,
			Pinned:  r.Pinned,
		}

		// A plugin tracks no ref, so there is no Ref to report; what it has
		// instead is an install its agent is free to move underneath it.
		if r.Channel == string(source.ChannelPlugin) {
			entries = append(entries, checkPlugin(e, r, plugins))
			continue
		}

		// An empty ref means the repository's default branch. Install records
		// no ref for a pinned skill, so this is also what makes a pin visible
		// rather than silently current.
		ref := r.Ref
		if ref == "" {
			ref = "HEAD"
		}
		e.Ref = ref

		// Only the git channel has a ref that can move.
		if r.Channel != string(source.ChannelGit) {
			e.Status = StatusSkipped
			entries = append(entries, e)
			continue
		}

		// … unchanged from here: the ls-remote cache and the current/outdated verdict.
	}
	return entries
}

// checkPlugin compares what the receipt records against what the agent has now.
//
// This is not the marketplace comparison a plugin's users will eventually want —
// nothing here asks whether a newer version has been published. It answers the
// question the receipt's links raise: is the directory they point into still the
// one claude calls this plugin's install path?
func checkPlugin(e Entry, r *state.Receipt, list func() ([]claudex.Installed, error)) Entry {
	got, err := list()
	if err != nil {
		e.Status = StatusError
		e.Error = err.Error()
		return e
	}

	for _, p := range got {
		if p.ID != r.Source {
			continue
		}
		e.Latest = p.Version
		e.Status = StatusCurrent
		if p.Version != r.Resolved || p.InstallPath != r.RevPath {
			e.Status = StatusStale
		}
		return e
	}

	e.Status = StatusError
	e.Error = fmt.Sprintf("claude no longer has %s installed", r.Source)
	return e
}
```

Add `fmt` and `github.com/richardcase/skillsctl/internal/claudex` to the imports.

In `internal/cli/outdated.go:41`:

```go
				entries := outdated.Check(cmd.Context(), gitx.New(), newPlugins(), receipts)
```

`outdatedExit` needs no change: a stale plugin is not `StatusOutdated`, so it does not on its own make `outdated` exit 2. Add a sentence to that function's doc comment saying so:

```go
// A stale plugin is deliberately not an update: nothing here knows whether a
// newer version exists, only that skillsctl's record of the installed one has
// fallen behind, which `skillsctl update` repairs.
```

- [ ] **Step 4: Add the end-to-end test**

Add to `internal/cli/plugin_test.go`:

```go
func TestOutdatedReportsAPluginClaudeMovedBehindOurBack(t *testing.T) {
	h := newHarness(t)
	h.plugins.skills = []string{"alpha"}

	if out, err := h.run(t, "install", pluginID); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	// What `claude plugin update` on its own would leave behind.
	h.plugins.next = "2.0.0"
	if err := h.plugins.exec([]string{"claude", "plugin", "update", pluginID}); err != nil {
		t.Fatal(err)
	}

	out, _ := h.run(t, "outdated")
	if !strings.Contains(out, "stale") {
		t.Errorf("output = %q, want the plugin reported as stale rather than n/a", out)
	}
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `make test && make lint`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/outdated internal/cli
git commit -m "feat(outdated): report a plugin whose install path has moved"
```

---

## Task 13: The `Ownership` doc comments

Small, but the old comments now say something false.

**Files:**
- Modify: `internal/channel/channel.go:23-43`
- Modify: `internal/channel/linked.go:14-23` (the type's doc)

**Interfaces:** none. Comments only; no test.

- [ ] **Step 1: Rewrite the constants' doc comments**

```go
const (
	// StoreOwned means skillsctl extracted the files into its own store. Its
	// revision and mirror are live roots for gc.
	StoreOwned Ownership = iota
	// AgentOwned means the agent installed the files and owns them: skillsctl
	// records the install and undoes it through the agent, and nothing of ours
	// is in the store, so gc has nothing to count. It may still have made
	// symlinks — a plugin's skills are fanned out to the agents that cannot
	// install plugins — but they point outside the store, so this answer is
	// about what gc counts rather than about whether links exist.
	AgentOwned
	// UserOwned means the files are the user's own, in a directory they chose.
	// …
	UserOwned
)
```

Keep the existing `UserOwned` prose; only `StoreOwned` and `AgentOwned` change.

- [ ] **Step 2: Correct `linked`'s doc**

Its last sentence claims the plugin channel has no links. Replace it:

```go
// Both the git and local channels embed it: they differ in where the files come
// from, not in how they reach an agent or how they stop reaching it. The plugin
// channel does not, because its removal contract is two things at once — the
// agent's uninstall command for the agent that installed it, and links for
// everyone else — and because one plugin receipt holds many links per agent
// rather than one.
```

- [ ] **Step 3: Verify**

Run: `make test && make lint`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/channel
git commit -m "docs(channel): say what Ownership answers now that a plugin has links"
```

---

## Task 14: Documentation

**Files:**
- Modify: `README.md` — Features `:28-29` and `:60-63`; sample `list` output `:119-126`; How it works `:240-246`; Commands prose `:273-284`; Configuration `:335-339`; Status `:341-352`
- Modify: `AGENTS.md` — the Channels convention bullet

**Interfaces:** none.

- [ ] **Step 1: Update the README**

Work through the file top to bottom against the change, not only the sections listed — a fan-out touches the feature list, the examples and the status list at once.

1. **Features, `:28-29`** — the "fetched once and symlinked into Claude Code, Codex and Gemini" claim is now true for plugins too. Leave it alone if it does not exclude them; if it says "a skill", it still reads correctly.
2. **Features, `:60-63`** — extend the plugin bullet, phrased as what the user gets:

```markdown
- **Claude Code plugins too.** `skillsctl install superpowers@claude-plugins-official`
  installs through `claude plugin`, records a receipt, and links every skill the
  plugin ships into the agents that cannot install plugins themselves — so a
  plugin reaches Codex and Gemini like anything else. A plugin Claude already has
  is adopted rather than reinstalled, and `update` re-points those links when
  claude moves the plugin to a new version.
```

3. **Sample `list` output, `:119-126`** — the AGENTS column for a plugin is no longer just `claude`:

```
superpowers  plugin  6.3.0  claude, codex
```

4. **How it works, `:240-246`** — the paragraph says a plugin has "no symlink", which is now wrong. Rewrite the middle of it:

```markdown
A plugin is the second exception, because Claude Code owns it. `skillsctl` records
the `plugin@marketplace` id, the version and the install path claude reported;
there is no revision in the store and no content hash, since the files are the
agent's. What it adds is the fan-out: every skill under the plugin's `skills/`
directory is symlinked into the agents that cannot install plugins for
themselves, and those links are recorded on the receipt like any other. So
`install`, `update` and `remove` run `claude plugin install|update|uninstall`,
read back what claude decided, and then make the links agree with it — which
matters because claude installs each version beside the last, so a link left
alone would go on serving a version that has been replaced. `gc` still leaves a
plugin alone: nothing of it is in the store. `claude` must be on `PATH`; nothing
else needs it.
```

5. **Commands prose, `:273-284`** — two sentences are now false. Replace "A plugin has no links to keep, so removing it uninstalls it through `claude` and forgets the receipt outright" with the split contract, and delete "A plugin is refused, because its skills are the agent's own and there is no symlink to add":

```markdown
Removing a plugin uninstalls it through `claude` and takes away every link its
skills had. Naming only an agent that holds links — `remove superpowers -a codex`
— takes those away and keeps the receipt, since the plugin is still installed.
Naming the agent that owns it is refused while anything is linked, because
uninstalling would strand those links; the error names `skillsctl remove <name>`,
which does mean everywhere.
```

and, for `link`:

```markdown
A plugin is linked skill by skill: `link superpowers -a codex` puts every skill
the plugin ships into codex. The agent that installed the plugin is reported as
already having it, because it can see those skills without a symlink.
```

6. **Configuration, `:335-339`** — sharpen what `plugins` means:

```markdown
`plugins` marks an agent that installs plugins from a marketplace for itself.
It gates installing a plugin, never seeing one: a `name@marketplace` source needs
an agent with `plugins = true` in the set, and naming only agents without it
through `-a` is an error rather than a silent no-op — but it is precisely the
agents *without* it that a plugin's skills are linked into.
```

7. **Status, `:347-349`** — the fan-out comes off the list, and `outdated` changes rather than disappearing:

```markdown
One thing the plugin channel deliberately does not do yet: `outdated` reports a
plugin as `stale` when claude has moved it since skillsctl last looked, but it
cannot tell you whether the marketplace has published a newer version.
```

- [ ] **Step 2: Update AGENTS.md**

In the **Channels are the only place a mechanism differs** bullet, the sentence "It has three values because three channels need three" still holds, but add the plugin's hybrid to the same bullet:

```markdown
  `Ownership()` answers what gc counts, not whether links exist: the plugin
  channel is `AgentOwned` and still records links, because its skills are fanned
  out to the agents that cannot install plugins.
```

- [ ] **Step 3: Check the examples against the code**

Run `make build` and compare every command and flag the README shows against `./skillsctl <cmd> --help`, and the sample outputs against a real run in a scratch `SKILLSCTL_HOME`. Fix any drift.

- [ ] **Step 4: Commit**

```bash
git add README.md AGENTS.md
git commit -m "docs: describe fanning a plugin's skills out to other agents"
```

---

## Task 15: The manual test

**Files:**
- Modify: `internal/channel/plugin_manual_test.go`

**Interfaces:** none.

- [ ] **Step 1: Extend the existing manual test**

It currently installs `superpowers@claude-plugins-official` for a claude-only config and asserts `RevPath` exists. Give it a codex target with a real temp directory and assert the fan-out, then assert removal takes it away. Keep the existing guards: skip when `claude` is absent, skip when the plugin is already installed.

```go
	// A second agent, so the fan-out has somewhere to go. It is a temp
	// directory, so nothing here touches the machine's real codex.
	agents := t.TempDir()
	cfg := target.Config{Targets: []target.Target{
		{Name: "claude", Plugins: true},
		{Name: "codex", Dir: filepath.Join(agents, "codex")},
	}}
```

After the settle assertions:

```go
	p, skipped, err := c.Link(changed[0], cfg.Targets)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if len(skipped) != 0 {
		t.Errorf("skipped = %v, want none in an empty codex", skipped)
	}
	if p.IsEmpty() {
		t.Fatal("superpowers ships skills, so there is something to link")
	}

	ex := &plan.Executor{DB: db, Out: io.Discard}
	if err := ex.Apply(context.Background(), p); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	des, err := os.ReadDir(cfg.Targets[1].Dir)
	if err != nil {
		t.Fatalf("read codex: %v", err)
	}
	if len(des) == 0 {
		t.Fatal("codex holds nothing after the fan-out")
	}
	for _, de := range des {
		dest, rerr := filepath.EvalSymlinks(filepath.Join(cfg.Targets[1].Dir, de.Name()))
		if rerr != nil {
			t.Errorf("%s: %v", de.Name(), rerr)
			continue
		}
		if _, serr := os.Stat(filepath.Join(dest, "SKILL.md")); serr != nil {
			t.Errorf("%s -> %s holds no SKILL.md", de.Name(), dest)
		}
	}
```

Then, in the existing uninstall stage, assert the codex directory is empty afterwards.

Match the file's existing variable names for the channel, receipt and DB rather than inventing new ones.

- [ ] **Step 2: Run it**

Run: `make test-manual`
Expected: PASS, or a clean skip if `claude` is absent or the plugin is already installed. `make test` must be unaffected — the file is behind `//go:build manual`.

- [ ] **Step 3: Commit**

```bash
git add internal/channel
git commit -m "test(plugin): really fan superpowers out to a second agent"
```

---

## Task 16: End-to-end verification

**Files:** none. This is the gate before opening the PR.

- [ ] **Step 1: The full check**

Run: `make test && make lint && make tidy-check`
Expected: all three pass. `.goreleaser.yaml` is untouched, so `goreleaser check` is not needed.

- [ ] **Step 2: Drive it by hand**

```bash
make build
export SKILLSCTL_HOME=$(mktemp -d)
./skillsctl install superpowers@claude-plugins-official --dry-run   # note names codex, gemini
./skillsctl install superpowers@claude-plugins-official
ls -l ~/.codex/skills                                              # links into .../superpowers/<version>/skills/*
./skillsctl list                                                   # AGENTS shows claude, codex
claude plugin update superpowers@claude-plugins-official           # behind skillsctl's back
./skillsctl outdated                                               # superpowers ... stale
./skillsctl update
ls -l ~/.codex/skills | grep -c "$(./skillsctl list --json | ...)"  # every link under the new version dir
./skillsctl remove superpowers -a codex                            # links go, receipt stays
./skillsctl remove superpowers                                     # uninstalls
```

Confirm at the end that `~/.codex/skills` holds nothing skillsctl made and that no link resolves under an old version directory. Then `unset SKILLSCTL_HOME`.

- [ ] **Step 3: Open the PR**

The PR title is the squash-merge subject and must be a Conventional Commit:

```
feat: fan a plugin's skills out to non-Claude agents
```

Body: what changed, and `Closes #18`. No attribution footers.

---

## Self-Review

**Spec coverage** — every section of `2026-08-15-plugin-fan-out-design.md` maps to a task:

| Spec section | Task |
| --- | --- |
| The constraints (version-scoped path, unknowable path, no Exec rollback, `Links` re-keyed) | 2, 4, 6, 7 |
| What the receipt looks like | 4, 5 |
| Ownership stays three-valued | 13 |
| Link becomes reconciliation (the three-call-site table) | 2, 5, 8, 9, 10 |
| What a dry run can say | 1, 7 |
| Choosing what to link, and what to skip | 4 |
| Which agents, and how they are removed | 3, 6 |
| adopt | 11 |
| outdated | 12 |
| Surface (no new flags) | none needed — no flag is added |
| Not in scope | none — nothing implements them |

**Type consistency** — checked across tasks: `Link(r, add) (plan.Plan, []string, error)` is used with three returns in Tasks 2, 5, 8, 10; `fan` returns four values in Tasks 4, 5, 7; `pluginSkillsDir` is spelled the same in Tasks 4, 5, 8; `relink`'s `targetsFor` callback is used identically in Tasks 8 and 9; `settleUpdated` grows its third return in Task 9 only.

**Ordering** — Task 1 precedes 7; Task 2 precedes 5, 8, 10; Task 3 precedes 4; Task 4 precedes 5 and 7; Task 8's harness change precedes Tasks 9, 10 and 12's end-to-end tests. Tasks 11 and 12 are independent of the rest and may be done in any order after Task 5.
