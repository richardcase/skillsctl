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
make lint         # golangci-lint run
make fmt          # golangci-lint fmt (gofumpt + goimports)
make build        # go build -o skillsctl ./cmd/skillsctl
make tidy-check   # go mod tidy, then fail if go.mod/go.sum changed
make snapshot     # goreleaser release --snapshot --clean
```

**Definition of done:** `make test && make lint && make tidy-check` all pass.
Add `goreleaser check` only when `.goreleaser.yaml` changes. These mirror the
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

## Architecture

`cmd/skillsctl/main.go` is a thin `main` that calls `cli.Execute()`. Everything
else lives in `internal/`, one narrow responsibility per package:

| Package | Responsibility |
| --- | --- |
| `cli` | Cobra command tree, flag wiring, output rendering |
| `source` | Parse `owner/repo`, git URLs, `plugin@marketplace`, local paths into a `Source` |
| `gitx` | The `git` binary behind a `Git` interface: `Resolve`, `Mirror`, `Extract` (+ safe untar) |
| `store` | Store layout (`cache/`, `rev/`, `state.json`), `Ensure`, containment checks, tree hashing |
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
