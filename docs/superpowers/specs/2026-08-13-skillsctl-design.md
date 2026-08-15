# skillsctl — a package manager for agent skills

## Context

Agent skills today are installed by hand through mutually incompatible mechanisms:
`git clone` into an agent's skills directory, `npx skills add owner/repo`, and
`claude plugin install name@marketplace`. None of them records *how* a skill
arrived, so there is no reliable way to update or remove one. On this machine the
evidence is concrete: `~/.claude/skills/*` are hand-made symlinks into
`~/.agents/skills/`, which is not a git repo and carries no provenance for any of
its ~20 skills.

`skillsctl` is Homebrew for agent skills: install, update and remove skills from
any of those sources, with a receipt for every install that makes update and
removal deterministic. It targets multiple agents (Claude Code, Codex, Gemini)
from one store, so a skill is fetched once and linked everywhere.

**Decided up front** (user, this session): Go; central store + symlinks; native
git and registry handling with the plugin channel delegated to `claude plugin`;
multi-agent from day one; receipts DB as source of truth with a manifest as an
export; global-first with `--project`; track a ref and record the resolved sha;
adopt existing hand-managed skills.

## Key insight: three sources, two channels

"Clone a repo that *is* a skill" and "install a skill *from* a repo" are the same
operation — fetch a git repo, take a subtree — differing only in whether the
subtree is `/` or `/skills/<name>`. They collapse into one `git` channel where
`--skill` is a subpath selector. The genuinely distinct channel is `plugin`,
because Claude Code owns that state. Plus `local` for adopted and
under-development skills.

| Channel | Fetch | Update | Remove |
|---|---|---|---|
| `git` | bare mirror + archive extract to `rev/<sha>/` | resolve ref → new rev dir → re-link → gc | unlink + gc |
| `plugin` | `claude plugin install <p>@<m>` | `claude plugin update` | `claude plugin uninstall` |
| `local` | none (path recorded) | none | unlink only, never delete source |

## Architecture: plan/apply over a shared pipeline

Every command produces a **Plan** — an ordered list of primitive ops — which an
executor applies. Channels differ *only* at resolve/fetch; discovery, linking,
state and gc are shared.

```
Resolve(sha) → Ensure(rev in store) → Discover(SKILL.md) → Plan{Link,Record} → Apply
```

Ops: `Link`, `Unlink`, `Record`, `Forget`, `Exec`. A plan contains only
user-visible mutations. Fetching and extracting are store cache population
(`store.Ensure`) performed *before* planning — a skill's name comes from its
extracted `SKILL.md`, so it cannot be known until the revision is on disk.
Populating a content-addressed cache is idempotent, so this keeps `--dry-run`
exact rather than speculative.

Why: `--dry-run` is free (print the plan), a failed apply leaves state
uncommitted rather than half-written, and tests assert over plans instead of
poking at the filesystem.

### Store layout

```
$SKILLSCTL_HOME (default $XDG_DATA_HOME/skillsctl, else ~/.local/share/skillsctl)
  cache/github.com/vercel-labs/agent-skills.git     # bare mirror, cheap re-fetch
  rev/github.com/vercel-labs/agent-skills/9f8e7d6/  # git archive extract, no .git
  state.json                                         # receipts; atomic write + flock
$XDG_CONFIG_HOME/skillsctl/config.toml               # target agents
```

A revision directory holds the *whole repository* at that sha. Subpath selection
happens at link time, so two skills taken from different subpaths of one commit
share a single revision directory.

Revision directories are immutable, so update is *extract new → re-point symlink
→ gc old*: atomic from the agent's point of view, rollback is re-pointing. No
`.git` inside a rev dir, so in-place edits can't silently break an update —
development uses `skillsctl link ./path` (a `local` skill) instead.

Honour `$XDG_*` with `~/.local/share` / `~/.config` fallbacks rather than Go's
`os.UserConfigDir()`, which resolves to `~/Library/Application Support` on macOS.

### Data model

```go
type Receipt struct {
    Name      string    // link name in each agent's skills dir
    Channel   Channel   // git | plugin | local
    Source    string    // canonical URL, plugin@marketplace, or abs path
    Subpath   string    // "" for repo-is-a-skill
    Ref       string    // tracked branch/tag; "" when pinned to a sha
    Resolved  string    // sha, or plugin version
    Pinned    bool
    RevPath   string    // store rev dir, or plugin install path
    ContentHash string  // hash of the extracted subtree at install time
    Links     []Link    // {Target, Path} — exactly what to unlink
    InstalledAt, UpdatedAt time.Time
}
```

`Links` is the removal contract: remove undoes precisely what install did, with
no inference at removal time.

`ContentHash` exists because rev dirs have no `.git` and so cannot report their
own dirtiness. It is a hash over the extracted subtree's file paths and contents,
recorded at extract time; re-hashing before an update is how `skillsctl` detects
that someone edited a skill through the symlink.

### Source syntax

| Input | Channel |
|---|---|
| `owner/repo` | git, subpath `""` |
| `owner/repo/path/to/skill` | git, subpath `path/to/skill` |
| `<any git source>//path/to/skill` | git, subpath `path/to/skill` |
| `https://…`, `git@…`, any git URL (incl. GitLab) | git |
| `name@marketplace` (`@` is the discriminator) | plugin |
| `./path`, `/path` | local |

`--from git|plugin|local` forces the channel when inference is wrong.

`//` separates a repository from a subpath within it, and wins over whatever the
shape of the URL implied. Inference alone cannot reach every case: a `.git`
suffix declares the whole path to be the repository, which is what makes a
GitLab subgroup installable, and an scp-form URL has no path structure to split
at all — so without the separator, neither can name a subpath. The scheme's own
`//` is not a separator, and the subpath still shares the repository's slug, so
skills taken from different subpaths of one commit share one revision directory.

### Discovery

After extracting a revision, walk it (bounded depth, skip `.git`/`node_modules`)
for `SKILL.md`, parsing YAML frontmatter for `name`/`description`. Also read
`.claude-plugin/marketplace.json` and `.claude-plugin/plugin.json` when present.

- root `SKILL.md` → single skill, name from frontmatter else repo name
- multiple → require `--skill <name>…` or `--all`; bare invocation lists what's
  available and exits non-zero
- link name = frontmatter `name`, overridable with `--as`
- name collision with an existing receipt → error naming the current owner,
  suggest `--as`

Refinements made while implementing this:

- A directory holding a `SKILL.md` **is** a skill and is not descended into. That
  one rule gives "root `SKILL.md` → single skill" for free, and stops example
  directories inside a skill from becoming skills of their own.
- `.claude-plugin/marketplace.json` and `plugin.json` are **display metadata
  only** — a heading above the listing saying which repository the skills came
  from. They never affect which skills are discovered or what they are named, and
  a missing or malformed file is not an error: decoration must not fail an
  install.
- `--skill <name>` matches a skill's resolved name first, then its path within
  the repository, so a skill whose frontmatter is missing or ambiguous can still
  be asked for.
- A name that is already installed is a hard error when a single skill was
  selected — the user asked for that one in particular. When several were
  selected the request is for whatever is missing, so collisions are reported and
  skipped, the rest install, and the command exits 2 (see Exit codes). Every name
  colliding is an error that changes nothing.
- The link name for a nameless skill falls back to the source only for a skill
  that is the walk root; a nested one falls back to its own directory name.

### Targets

```toml
# config.toml
[[target]]
name = "claude"
dir = "~/.claude/skills"
project_dir = ".claude/skills"
plugins = true

[[target]]
name = "codex"
dir = "~/.codex/skills"
```

A target counts as present when its *parent* directory exists (`~/.claude`,
`~/.codex`), not its skills directory — the skills dir is created on demand at
first install, since a fresh agent won't have one. The default target set is
every present configured target; `-a` narrows it. The `plugin` channel applies
to Claude only in v1 (a plugin's skills are
already visible to Claude Code); fanning a plugin's `skills/` subdir out to other
agents is explicitly deferred.

**Superseded:** the fan-out is now in scope, and a plugin's default target set is
every present agent like every other channel's. See
[the plugin fan-out design](2026-08-15-plugin-fan-out-design.md), which keeps
`plugins = true` meaning "can install a plugin" rather than "can see one".

### Scope

Global by default. `--project` links into `<repo>/.claude/skills` and writes
receipts to `<repo>/skills.lock` beside a human-editable `skills.toml`. Same
receipt schema, different file; the store stays shared and global.

`skillsctl bundle` emits exactly the `skills.toml` schema — one manifest format
for both the project scope and the global export, so `bundle > skills.toml`
followed by `sync skills.toml` on another machine is a closed loop.

## CLI surface

```
skillsctl install <source>… [--skill N…|--all] [--ref R] [--pin] [-a claude,codex]
                            [--project] [--as NAME] [--dry-run]
skillsctl list [--json]              skillsctl info <name>
skillsctl outdated                   skillsctl update [name…] [--dry-run]
skillsctl remove <name> [-a codex]   skillsctl link <name> -a gemini
skillsctl link ./path                skillsctl adopt [--dry-run]
skillsctl pin <name>…                skillsctl unpin <name>… [--ref R]
skillsctl bundle                     skillsctl sync <file>
skillsctl doctor                     skillsctl gc
skillsctl version
```

`version` prints the ldflags-injected version when built by GoReleaser, and falls
back to `runtime/debug.ReadBuildInfo()` (module version + VCS stamp) so
`go install`ed builds still report something truthful rather than `dev`.

- `update` on a skill whose rev dir is dirty (re-hash disagrees with
  `ContentHash`) warns and skips unless `--force`; pinned skills are skipped
  unless named explicitly.
- `outdated` compares `Resolved` against `git ls-remote` for the tracked ref —
  no fetch required. Pinned skills are still listed, resolved against the
  repository's default branch and marked `pinned`, so a pin never hides the fact
  that something moved.
- `pin` freezes a skill at the revision it is already installed at and `unpin`
  releases it, both by writing the receipt and nothing else — no fetch, no
  re-link. A pinned receipt records no ref, so an unpinned skill tracks the
  repository's default branch unless `--ref` names one, and that ref is resolved
  before it is recorded. Only the git channel can be pinned; the others refuse.
  See [the pin and unpin design](2026-08-15-pin-and-unpin-design.md).
- `adopt` scans each target's skills dir, follows symlinks, records anything
  unmanaged as `local` (listed, never auto-updated), and promotes entries with a
  detectable git remote to the `git` channel.
- `doctor` reports dangling symlinks, receipts whose links are missing, rev dirs
  with no receipt, and name collisions across targets.
- `gc` deletes rev dirs and bare mirrors that no receipt references.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | everything asked for was done |
| 1 | nothing was done; the message says why |
| 2 | some of it was done, and what was skipped is reported |

Code 2 exists because a single failure code cannot express a partial result:
`install --all` over a repository where one name is already taken installs the
rest, and a script has to be able to tell that from having installed nothing. It
is rendered as `note:` rather than `error:`, since the work stands. `update`
across several skills will report the same way.

## Package layout

```
cmd/skillsctl/            thin main(); calls cli.Execute()
internal/cli/             cobra commands + output rendering
internal/source/          parse source strings → Source
internal/channel/         Channel interface; git.go, plugin.go, local.go
internal/gitx/            Git interface + exec-backed impl (mirror, ls-remote, archive)
internal/discover/        SKILL.md walk + frontmatter parse
internal/store/           rev dirs, mirrors, gc
internal/state/           Receipt, load/save (flock + temp-file rename), schema version
internal/target/          config, agent dirs, symlink create/remove
internal/plan/            Op types, Plan, Executor, dry-run renderer
```

**Shell out to the `git` binary, not go-git.** Auth is the reason: SSH keys,
credential helpers, `gh`, private repos and proxies all work with no code. Kept
behind the `internal/gitx.Git` interface so tests run against fixture repos over
`file://` with no network.

## CI

All tooling — Go, `golangci-lint`, GoReleaser — is pinned in a checked-in
`mise.toml` and installed by mise both locally and in CI (`jdx/mise-action`), so
there is one source of truth for versions and no drift between a developer's
machine and the runners. `actions/setup-go` and tool-specific actions are
deliberately not used.

`.github/workflows/ci.yml`, on pull request and pushes to `main`. Three jobs,
run in parallel, all required to merge:

- **test** — matrix `ubuntu-latest` × `macos-latest`, `go test -race -cover ./...`.
  Both runners ship `git`, which the integration tests need; the tests use
  `file://` fixture repos so no network and no credentials are involved.
- **lint** — `golangci-lint run`, config checked in at `.golangci.yml` (govet,
  staticcheck, errcheck, revive, gofumpt, misspell). Plus a `go mod tidy` check:
  run it and fail if the tree is dirty.
- **build** — `goreleaser build --snapshot --clean` so a broken release config is
  caught on the PR that breaks it, not months later at tag time. Also the
  cheapest proof that every target platform compiles.

mise-action caches the tools; a separate `actions/cache` step covers the Go
module and build caches. Pin action versions by tag and enable Dependabot for
`gomod` and `github-actions`.

## Release

`.github/workflows/release.yml`, triggered by pushed tags matching `v*.*.*`
(the workflow re-validates the tag against a semver regex and fails fast on a
malformed tag, since the glob alone is loose). Steps: checkout with
`fetch-depth: 0` (GoReleaser needs full history for the changelog), install the
pinned toolchain with `jdx/mise-action`, then `goreleaser release --clean` — the
same GoReleaser version that validated the config on the pull request.

Secrets: the default `GITHUB_TOKEN` for the release itself, plus a
`HOMEBREW_TAP_TOKEN` PAT with write access to the tap repo.

`.goreleaser.yaml`:

- **builds** — `CGO_ENABLED=0`; `linux` and `darwin` × `amd64` and `arm64`;
  ldflags injecting `version`, `commit`, `date` into `main`.
- **universal_binaries** — `replace: true`, so macOS ships one fat
  `darwin_all` archive instead of two.
- **archives** — `.tar.gz` with README and LICENSE included.
- **nfpms** — `deb` and `rpm` for linux amd64/arm64, attached to the GitHub
  Release as downloadable assets. No hosted apt/rpm repository, so no signing
  keys and no repo metadata to maintain.
- **brews** — publish a formula to the `homebrew-tap` repo, giving
  `brew install richardcase/tap/skillsctl`. (The tap repo needs to exist before
  the first release.)
- **checksums** and an auto-generated **changelog** grouped by conventional
  commit prefix, excluding `docs:`/`test:`/`chore:`.

Windows is deliberately out of scope.

## Build phases

0. **Scaffold + CI** — `mise.toml`, module, cobra root, `version`,
   `.golangci.yml`, `ci.yml`, `.goreleaser.yaml` and `release.yml`. Prove the
   release config with
   `goreleaser release --snapshot --clean` locally, then cut `v0.0.1` to
   exercise the real pipeline while there is nothing to break.
1. **Walking skeleton** — `state`, `target`, `plan`, `store`; `install owner/repo`
   for a repo-is-a-skill, `list`, `remove`. Proves the receipt contract end to end.
2. **git channel completed** — subpaths, `--skill`/`--all`, multi-skill discovery,
   `--ref`/`--pin`, `outdated`, `update` with re-link and gc.
3. **plugin + local channels** — `claude plugin` exec wrapper, `link ./path`.
4. **Machine reality** — `adopt`, `doctor`, `gc`.
5. **Sharing** — `bundle`/`sync`, `--project`.

## Verification

- **Unit**: source parsing table tests; frontmatter/discovery over fixture trees;
  plan construction asserted as op sequences (the primary correctness surface).
- **Integration, no network**: `git init` fixture repos in `t.TempDir()`, install
  from `file:///…`; cover single-skill repo, multi-skill repo, subpath install,
  update across two commits (assert old rev gc'd and symlink re-pointed), pinned
  skill skipped by update, remove leaves zero dangling links.
- **Plugin channel**: `gitx`-style fake exec; one opt-in test tagged `manual`
  that really runs `claude plugin install|uninstall`.
- **Manual smoke** against the three real sources, in a throwaway
  `SKILLSCTL_HOME` and throwaway target dirs:
  1. `skillsctl install conorbronsdon/avoid-ai-writing`
  2. `skillsctl install vercel-labs/agent-skills --skill <one>`
  3. `skillsctl install superpowers@claude-plugins-official`
  then `list`, `outdated`, `update --dry-run`, `remove` each, and confirm
  `doctor` is clean and the target dirs are back to their original contents.
- **Pipeline**: `goreleaser release --snapshot --clean` locally must produce the
  `darwin_all` archive, both linux archives, and the four deb/rpm packages.
  Install the built `.deb` in a Debian container and the `.rpm` in a Fedora
  container and run `skillsctl version` in each. After the first real tag,
  verify the GitHub Release assets and `brew install richardcase/tap/skillsctl`
  on this machine.
- **Adopt safety**: run `skillsctl adopt --dry-run` against the real
  `~/.claude/skills` and confirm all ~20 entries classify as `local` and no
  destructive op appears in the plan, before ever running it for real.

## Plan scope

This spec covers the whole tool, but phases are independently shippable and each
warrants its own implementation plan. The first plan covers **phases 0 and 1
only** — CI, release pipeline, and the walking skeleton that proves the receipt
contract end to end. Later phases get their own plans once that shape is real.
