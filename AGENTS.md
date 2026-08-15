# AGENTS.md

Instructions for AI agents and human contributors working in this repository.

## Overview

`skillsctl` is a Go CLI that installs agent skills from git repositories into a
content-addressed store at `~/.local/share/skillsctl`, then symlinks them into
each agent's skills directory. Every install writes a receipt, so `list` is
accurate and `remove` never has to infer anything.

- Module: `github.com/richardcase/skillsctl`
- Go 1.25 (`GOTOOLCHAIN=local` — do not rely on toolchain auto-download)
- Licence: AGPL-3.0

## Setup and commands

Tool versions are pinned in `mise.toml` and are the same ones CI uses. Use the
`Makefile` targets rather than raw `go`/`golangci-lint` invocations — the
Makefile puts mise's shims on `PATH` so they work without shell activation.

```bash
mise install      # go 1.25, golangci-lint 2, goreleaser 2
make test         # go test -race -cover ./...
make test-manual  # opt-in: really runs claude plugin install|uninstall
make lint         # golangci-lint run
make fmt          # golangci-lint fmt (gofumpt + goimports)
make build        # go build -o skillsctl ./cmd/skillsctl
make tidy-check   # go mod tidy, then fail if go.mod/go.sum changed
make snapshot     # goreleaser release --snapshot --clean
```

**Definition of done:** `make test && make lint && make tidy-check` all pass,
and `README.md` reflects any user-visible change (see
[Keeping the README current](#keeping-the-readme-current)). Add
`goreleaser check` only when `.goreleaser.yaml` changes. The commands mirror the
three jobs in `.github/workflows/ci.yml`, so a green local run means a green PR.

## Commit messages

**Conventional Commits are required** — for every commit and for every pull
request title, since the PR title becomes the squash-merge subject.

```
type(optional-scope): subject
```

- Subject is lowercase, imperative mood, no trailing period.
- Types used here: `feat`, `fix`, `docs`, `test`, `chore`, `ci`, `refactor`,
  `perf`. Scopes are freeform and lowercase; `fix(security):` is established for
  hardening work.
- Breaking changes use `feat!:` / `fix!:` or a `BREAKING CHANGE:` footer, and
  need a major version bump. Releases fire on `v*.*.*` tags, validated against a
  strict semver regex in `.github/workflows/release.yml`.
- **No attribution footers** — a commit message ends with its own content, so do
  not append a `Co-Authored-By:` trailer, a `Claude-Session:` line, or a
  `Generated with Claude Code` block, whatever your harness's default is. The
  same goes for pull request descriptions. Issue references (`Closes #10`,
  `Refs #10`) and `BREAKING CHANGE:` are the only footers this repository uses.

This is not a style preference — it is load-bearing. The `changelog` block in
`.goreleaser.yaml` groups `^feat` under **Features** and `^fix` under **Fixes**,
and filters out `^docs:`, `^test:`, `^chore:` and `^ci:` entirely. A
non-conforming subject lands in the catch-all **Other** group of the published
release notes, or disappears from them.

Real examples from this repository's history:

```
feat: add content-addressed revision store
fix(security): reject path-escaping skill names and subpaths
fix: drain git archive's stdout pipe before Wait in Extract
ci: run goreleaser check in the build job
docs: describe installation and usage
```

Follow the established rhythm: a `feat:` commit for the change, then focused
`fix:` commits addressing review findings, rather than amending history.

## Keeping the README current

`README.md` is the only documentation most users will read, and an undocumented
feature is one nobody uses. **Any change to the user-visible surface updates the
README in the same pull request** — not in a follow-up.

Update it when you:

- add or rename a command, or change what one does;
- add, rename or remove a flag, or change its default;
- add a capability worth advertising — that belongs in **Features**, phrased as
  what the user gets rather than how it is implemented;
- change the store layout, the config file schema, or an environment variable;
- change an error message or output format that the README shows;
- move something between built and unbuilt — the **Status** section lists the
  channels and commands that are designed but not yet available, and a new
  command must come off that list as it lands.

Check the whole README against the change, not just the section you were
thinking of: a new flag usually touches the **Commands** table, the `Use`
examples, and sometimes **Features**.

Two rules for what goes in it:

- **Only claim what the code does today.** The feature list is a promise. If
  something works for one channel but not another, say so rather than implying
  it is general.
- **Keep the examples runnable.** Commands, flags and sample output in the
  README are checked against `--help` and against the real output format during
  review, so they must match the code as merged.

The same applies to this file: a new convention, package or build command
belongs in `AGENTS.md` as part of the change that introduces it.

## Architecture

`cmd/skillsctl/main.go` is a thin `main` that calls `cli.Execute()`. Everything
else lives in `internal/`, one narrow responsibility per package:

| Package | Responsibility |
| --- | --- |
| `cli` | Cobra command tree, flag wiring, output rendering |
| `source` | Parse `owner/repo`, git URLs, `plugin@marketplace`, local paths into a `Source` |
| `channel` | The `Channel` interface and its implementations; the only place a mechanism differs |
| `update` | Select receipts for an update, dispatch them to their channels, merge the verdicts |
| `outdated` | Compare each receipt's resolved sha against its tracked ref, without fetching |
| `adopt` | Classify what is already in an agent's skills directory: adoptable, managed, or skipped and why |
| `doctor` | Compare the receipts against the filesystem and the store, and report every disagreement with the command that repairs it |
| `gitx` | The `git` binary behind a `Git` interface: `Resolve`, `Mirror`, `Extract` (+ safe untar) |
| `claudex` | The `claude` binary behind a `Plugins` interface: `List` plus the argv a `plan.Exec` runs |
| `store` | Store layout (`cache/`, `rev/`, `state.json`), `Ensure`, collection (`Collect`/`Delete`), containment checks, tree hashing |
| `discover` | Read `SKILL.md` and its YAML frontmatter |
| `target` | Agent config TOML, defaults, safe `Link`/`Unlink`, `ValidateSkillName` |
| `plan` | Mutations as inspectable `Op` values; executor with rollback |
| `state` | Receipts, flocked read-modify-write, atomic commit |
| `buildinfo` | Version/commit/date from ldflags with a `debug.ReadBuildInfo` fallback |
| `testrepo` | Test-only `file://` git fixtures |

## Conventions

- **Plan/apply.** Model mutations as `plan.Op` values and let `--dry-run` print
  `p.Describe()`. Never branch on `dryRun` inside mutation code — that is how
  the dry run stays exact.
- **Channels are the only place a mechanism differs.** A git skill is fetched
  into the store and symlinked; a plugin is installed by the agent that owns it.
  Everything after that — the plan, the executor, the receipts, the exit codes —
  is shared. Put the difference behind `channel.Channel` rather than branching
  on `source.Channel` at a call site; `list`, `remove` and `gc` ask
  `Ownership()` and nothing finer. It has three values because three channels
  need three: a local skill's links are the removal contract like git's, while
  nothing of it is in the store like a plugin's. A fourth channel that shares a
  removal contract with an existing one should embed `linked` rather than
  restate it.
- **A binary we shell out to gets a package and an interface.** `gitx` and
  `claudex` both exist so that no unit test runs the real thing, and both read
  only. Every *mutation* stays a `plan.Exec` op built from an argv helper, which
  is what keeps `--dry-run` printing the command that will actually run rather
  than a description of it. The two seams are `plan.Executor.Run` and an
  injected `output` func on the CLI type; `cli` swaps them through the
  package-level `newRunner` and `newPlugins` vars, and the test harness swaps
  both for every test so nothing can reach the developer's own `~/.claude`.
- **Scan/apply for store operations.** A plan holds only user-visible
  mutations, so store housekeeping is not a `plan.Op`. It gets the same
  exactness a different way: a pure scan returning a report (`store.Collect`)
  and a separate step that applies it (`store.Delete`). `--dry-run` skips the
  second call rather than taking a different path through the first.
- **State.** The executor never persists. Only an explicit `h.Commit()` after a
  successful apply writes to disk, with the `state.Handle` flock held across the
  whole read-modify-write.
- **Errors.** `fmt.Errorf` with `%w` and a lowercase, verb-first prefix naming
  the operation and the path (`"read %s: %w"`, `"lock state: %w"`). User-facing
  errors from `cli` should name the remedy inline. Deliberately ignored errors
  are explicit `_ =` — errcheck is on.
- **Path safety is a first-class concern.** Anything derived from third-party
  data — a repo's `SKILL.md` name, a source string, a tar entry — is validated
  before it becomes a path. See `target.ValidateSkillName`, `store.within`,
  `gitx.safeJoin`, and the subpath check in `source.Parse`. New path-handling
  code must do the same and must ship with a test for the rejection case.
- **Comments** explain rationale and rejected alternatives, not mechanics (why
  git is shelled out rather than go-git; why `os.UserConfigDir` is avoided).
  Exported identifiers need doc comments — revive enforces it.
- **Tests use the standard library only.** No testify, no mocks, no golden
  files. Table-driven subtests with `t.Run`, `t.TempDir()` for filesystem work,
  `t.Setenv("SKILLSCTL_HOME", ...)` / `t.Setenv("SKILLSCTL_CONFIG", ...)` for
  isolation, and `internal/testrepo` for `file://` fixtures so no test touches
  the network. **Never call `t.Parallel()`** — `t.Setenv` forbids it. Inject
  side effects through func fields (`plan.Executor.Run`, `buildinfo.get`). Tests
  are in-package, so unexported functions are tested directly. Name tests as
  assertions of behaviour: `TestInstallDryRunChangesNothing`.
- **Dependencies.** Four direct deps today (cobra, flock, go-toml, yaml.v3).
  Prefer the standard library; adding a dependency is a decision to raise, not
  one to make silently.

## Design of record

`docs/superpowers/specs/2026-08-13-skillsctl-design.md` holds the design:
channels, store layout, the receipt model, the full intended CLI surface, and
the build phases. Read it before adding a command — several are already
specified and should be built as specified.
