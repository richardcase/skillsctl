# `skillsctl search` — finding skills without already knowing owner/repo

## Context

Issue [#94](https://github.com/richardcase/skillsctl/issues/94) splits skill
discovery out of #87: `install` requires already knowing an `owner/repo` —
there is no way to discover a skill you don't know exists. The issue
explicitly flagged the registry/curation question as open and blocking, to be
resolved before any code: who curates the index, where does it live, how does
an author get listed.

Three external directories were evaluated as possible backends:

- **agenticskills.io** — human-reviewed web submission form, no public API or
  JSON endpoint, HTML-only. Findings posted to
  [issue #94](https://github.com/richardcase/skillsctl/issues/94#issuecomment-5369988994).
- **mcpservers.org/agent-skills** — also no API/sitemap, and its skill pages
  serve a ZIP download rather than fetching the origin `owner/repo` — doesn't
  match skillsctl's git-source install model. Author skill counts (e.g.
  "Microsoft: 1577 skills") look implausible for a hand-curated list.
- **heilcheng/awesome-agent-skills** — MIT-licensed, 6.1k-star, actively
  maintained markdown list on GitHub, with a companion site at agent-skill.co.
  A real git repo, not a scrape target — but its README entries display as
  `owner/skill-name` while linking to `agent-skill.co/owner/skills/skill-name`,
  not `github.com/owner/repo` directly. Several are monorepo subpaths (e.g.
  Anthropic's `skill-creator` lives under `anthropics/skills`, not its own
  repo), so the display text cannot be regex-parsed into a real install
  source.

None of the three exposes a stable, authorized, git-source-shaped API today.

**Decided (user, this session):**

1. skillsctl owns a **hand-maintained `registry/skills.json`** in this repo,
   curated via PR. `awesome-agent-skills` is used as a **seed/curation
   source** — each entry manually resolved to a real `owner/repo[/subpath]` —
   skillsctl never depends on their repo, format, or website at runtime.
2. `search` **fetches the registry file from GitHub at runtime** (not
   embedded in the binary), so new entries are available without a skillsctl
   release, with a local cache for offline/failure fallback.
3. A **scheduled CI task** keeps the registry from rotting: it validates
   existing entries still resolve and proposes new candidate names from the
   awesome-list, but never auto-merges an unresolved entry — resolving a name
   to a real repo stays a human/PR step.

## `registry/skills.json`

A JSON array, one object per skill, fields drawn from the same concepts
`internal/discover.Meta` already parses from `SKILL.md` frontmatter:

```json
[
  {
    "name": "skill-creator",
    "source": "anthropics/skills/skill-creator",
    "description": "Guide for creating skills that extend Claude's capabilities",
    "tags": ["meta", "authoring"],
    "agents": ["claude"]
  }
]
```

`source` is a plain string in exactly the shorthand `source.Parse` already
accepts (`owner/repo` or `owner/repo/subpath`) — no new parsing, no change to
the `Source` struct. Curation is a PR that appends an entry someone has
manually verified resolves to a real, installable skill.

## `internal/registry` (new package)

Follows the existing "a thing skillsctl talks to over the network gets a
package and an interface" pattern (`gitx`, `claudex`, `ocix`):

- `Entry{ Name, Source, Description string; Tags, Agents []string }`
- `Registry` interface: `Fetch(ctx.Context) ([]Entry, error)`
- Real implementation: HTTP GET of a configurable URL, default
  `https://raw.githubusercontent.com/richardcase/skillsctl/main/registry/skills.json`.
  On success, writes the response to a local cache file
  (`<store root>/registry-cache.json`, a sibling of `state.json` — **not**
  inside `cache/`, since that directory's layout and `gc` pruning logic are
  specific to git mirrors keyed by host/owner/repo). On network failure, falls
  back to the cache file if present; errors only if both fail, with a message
  naming the remedy.
- Cache has a TTL (e.g. 24h) before a fresh fetch is attempted again; a
  stale-but-present cache is still used as the fallback on fetch failure
  regardless of TTL.
- Registry URL is overridable via config (`internal/target` config.toml gets a
  new `[registry]` table with a `url` field, following the existing
  `Config`/`Load`/`Default` pattern in `target.go`) and via
  `SKILLSCTL_REGISTRY_URL` env var, mainly so tests and self-hosted mirrors
  don't depend on GitHub.

## `internal/cli/search.go` (new command)

- `newSearchCmd()` / `runSearch(cmd, args, o)`, registered in `root.go`
  alongside the other subcommands.
- Takes a query argument, matches case-insensitively by substring across
  `Name`, `Description`, and `Tags` — no ranking/index needed at this scale.
- Output follows the existing convention: plain text listing (`owner/repo` +
  description) by default, `--json` marshals matched entries with
  `json.MarshalIndent`, matching `list.go`/`outdated.go`.
- Read-only command — no `plan.Op`, no store/state interaction. Results are
  informational; the user copies a `source` value into
  `skillsctl install <source>`.
- Testability: a package-level `newRegistry` var added to
  `internal/cli/context.go` alongside the existing `newRunner`/`newPlugins`/
  `newPicker` seams, swapped in tests for a fixture `Registry` — no test hits
  the real network.

## Keeping `registry/skills.json` current: scheduled CI task

Per-PR curation only handles additions someone thinks to propose. A scheduled
task keeps the registry from silently rotting, without ever auto-merging
unresolved entries:

- New internal tool `cmd/registry-check` (a separate `main`, not part of the
  public `skillsctl` CLI — a maintenance script, not a user command). Two
  checks:
  1. **Validate existing entries** — for every `registry/skills.json` entry,
     reuse `gitx.Git.Resolve` (already used by `install`) to confirm the
     source's ref still resolves, catching renamed/deleted/moved repos.
  2. **Propose new candidates** — fetch `heilcheng/awesome-agent-skills`'s
     `README.md` (raw.githubusercontent.com, same pattern as the runtime
     registry fetch), extract the linked `owner/skill-name` display names, and
     diff against `registry/skills.json` `name` fields. Names not yet present
     are reported as candidates — the tool never guesses their real
     `owner/repo`, since that ambiguity (monorepo subpaths etc.) is exactly
     what a human has to resolve.
  - Output: a single Markdown report (broken entries needing fix/removal; new
    candidate names needing manual resolution).
- `.github/workflows/registry-refresh.yml`: scheduled (weekly `cron`), follows
  `ci.yml`'s existing checkout/mise-action pattern. Runs
  `go run ./cmd/registry-check`, and if the report is non-empty,
  creates-or-updates (idempotent, so a quiet week doesn't spam) a single
  tracking issue via `gh issue create`/`gh issue edit` with the report body.
  It never opens a PR that changes `registry/skills.json` directly — turning a
  flagged entry into an actual registry change is still a human-curated PR,
  same as any other addition.

## Out of scope

- Populating `registry/skills.json` with real seeded entries — a curation
  task, tracked separately from this design, not a code task.
- Auto-install-from-search-result — `search` only prints candidates; feeding a
  result straight into `install` is a possible later enhancement.
- Any change to `agenticskills.io`, `mcpservers.org`, or
  `awesome-agent-skills` — none are depended on by the running binary.

## Files touched

- `registry/skills.json` (new, starts as `[]` or a handful of seed entries)
- `internal/registry/registry.go` (+ `_test.go`) — new package
- `internal/cli/search.go` (+ `_test.go`) — new command
- `internal/cli/root.go` — register `search`
- `internal/cli/context.go` — add `newRegistry` seam
- `internal/target/target.go` — add `[registry]` config table
- `README.md` — document `search` per AGENTS.md's "keep the README current" rule
- `cmd/registry-check/main.go` (+ package tests) — new maintenance tool
- `.github/workflows/registry-refresh.yml` (new, scheduled)

## Verification

- `internal/registry`: table-driven tests against an `httptest.Server`
  fixture (success, 404/5xx with a warm cache present, 404/5xx with no cache,
  TTL respected vs. bypassed).
- `internal/cli`: `TestSearchXxx` using the `newRegistry` seam with a fixture
  registry, asserting both plain-text and `--json` output shapes, and the
  no-match case.
- `cmd/registry-check`: unit tests against fixture registry files and an
  `httptest.Server` standing in for both the git-resolve check (reusing
  `internal/testrepo` fixtures) and the awesome-agent-skills README fetch.
- `make test && make lint && make tidy-check` per AGENTS.md's definition of
  done.
- Manual: `go run ./cmd/skillsctl search <term>` against a real seeded
  registry once one exists, confirming a returned `source` string installs
  cleanly via `skillsctl install <source>`. Manual: run the refresh workflow
  via `gh workflow run registry-refresh.yml` (or `act`) once merged, confirm
  it produces/updates the tracking issue on a repo with a deliberately broken
  entry.
