# lifecycle safety: diff, rollback, agent-compatibility metadata

Design of record for `skillsctl diff`, `skillsctl rollback`, and declared
agent-compatibility in `SKILL.md` frontmatter.

Issue: [#85](https://github.com/richardcase/skillsctl/issues/85).

## Why

skillsctl already keeps enough state to make moving between revisions
safer, it just doesn't expose it yet. `outdated` already resolves the
latest ref for a receipt without fetching. The git channel's bare mirror
(`cache/<slug>.git`) is retained for as long as a skill stays installed —
`gc` only reclaims it when the skill is removed entirely, so full history
is always available locally for a git receipt. `plan.Op`'s `Relink` doc
comment already describes rollback conceptually: "putting the previous
revision back rather than removing the link."

What's missing is a way to look before an `update` lands, and a way to
undo one after it does. Separately, `SKILL.md` has no way to say which
agents it was written for, so installing into an unsupported one links
silently.

These three are scoped into one design because `diff` and `rollback` share
the same receipt-history foundation, even though they land as separate
PRs. Agent-compatibility metadata is independent of the other two but
touches the same install-time surface, so it's designed alongside them
rather than in a follow-up issue.

## Receipt schema — shared foundation

`state.Receipt` gains three fields mirroring the existing `Resolved` /
`RevPath` / `ContentHash` triple:

```go
PreviousResolved    string // sha or digest; empty until the first update
PreviousRevPath     string
PreviousContentHash string
```

Every channel's `relink` (`internal/channel/git.go`, `internal/channel/oci.go`)
copies the *current* triple into the `Previous*` fields before overwriting
them with the new revision, as part of the receipt it already builds. No
new `plan.Op` — `plan.Record` already replaces the whole receipt, so this
is a field addition, not a new mutation shape.

## `skillsctl rollback <name>`

`rollback` undoes the last `update`: it points the link back at the
revision the receipt remembers as `Previous*`, and swaps the two triples so
a second `rollback` undoes the first. Running it twice returns to where you
started — a toggle, not a stack, because the receipt only ever remembers
one step back.

It is a new `Channel.Rollback(ctx, receipts) (Verdicts, error)` method, git
and OCI implement it, `local` and `plugin` return "not supported for this
channel" since neither has revision history to roll back through. It's
dispatched the same way `internal/update` dispatches `Update` per channel
today.

`rollback` errors clearly if `PreviousResolved == ""`: "nothing to roll
back — install or update this skill first."

Before building the plan, `rollback` makes sure the previous revision is
actually available:

- git: `store.Ensure` against the mirror. The mirror is always present, so
  this is a local `git archive`, no network.
- OCI: `store.EnsureOCI`. A no-op if the extraction is still on disk (not
  yet `gc`'d); otherwise it re-pulls by digest from the registry — the same
  fallback `EnsureOCI` already has for a fresh install.

Then it builds a `plan.Relink` (link path → previous `RevPath`) and a
`plan.Record` carrying the swapped receipt — the same op shapes
`channel.Git.relink` produces for a normal update, just pointed backwards.
`--dry-run` needs no special-casing: it already stops before `Commit`.

## `skillsctl diff <name>`

`diff` is read-only — it produces no `plan.Plan`, the same posture as
`outdated`. A `--against latest|previous` flag picks what to compare the
installed revision to, defaulting to `latest`:

- `latest`: resolved the same way `outdated.Check` does — git does
  `ls-remote` only, no fetch; OCI calls `Resolve`.
- `previous`: uses the receipt's `PreviousResolved`/`PreviousRevPath`
  directly; errors clearly if empty.

For git, a new `gitx.Git.Diff(ctx, mirrorPath, oldSha, newSha) (string, error)`
wraps `git diff` against the bare mirror. No extraction needed — the
mirror already has full history, so this never touches `rev/`.

OCI has no mirror equivalent, so `diff` calls `store.EnsureOCI` for both
revisions (the installed one is already on disk; the other pulls only if
not cached) and does a filesystem diff between the two `rev/` directories.

`diff` prints a unified diff to stdout, or "no changes" and exits 0 if the
two revisions match. `local` and `plugin` channels: "not supported for
this channel", same as `rollback`.

## Agent-compatibility metadata

`discover.Meta` gains `Agents []string` (`yaml:"agents,omitempty"`).
Empty or absent means unrestricted — every `SKILL.md` written before this
lands keeps working exactly as it does today.

The check runs in each channel's name-resolution step (`resolveNames` in
`git.go`, and the analogous point in `oci.go`/`plugin.go`), which already
has both the parsed `discover.Skill` and `req.Targets` in scope, right
alongside the loop that builds `plan.Link` ops. For each target whose
`Name` isn't listed in a non-empty `Meta.Agents`, it appends a warning to
the `warnings` slice `Prepare` already returns to its caller — reusing the
print loop `install.go` already has, no new plumbing.

The skill still links. This is advisory, not enforcement: "warns instead
of silently linking" describes what changes for the *operator* — they now
get told — not a new way for `install` to fail. Agent names stay free-form
strings matched against `target.Target.Name`; target config is
user-extensible with no closed enum, so there's no separate validation
table to keep in sync.

## Files touched (representative, not exhaustive)

- `internal/state/state.go` — `Receipt` schema (`Previous*` fields).
- `internal/channel/git.go`, `internal/channel/oci.go` — populate
  `Previous*` on relink; implement `Rollback`; agent-compatibility check in
  the name-resolution step.
- `internal/channel/channel.go` — add `Rollback` to the `Channel`
  interface.
- `internal/gitx/gitx.go` — add `Diff`.
- `internal/store` — no layout changes; `Ensure`/`EnsureOCI` reused as-is.
- `internal/discover/discover.go` — `Meta.Agents` field.
- `internal/cli/rollback.go`, `internal/cli/diff.go` — new commands.
- `internal/cli/install.go` — no change beyond the warnings loop already
  reusing existing plumbing.
- `README.md` — new `rollback`/`diff` commands in the Commands table and
  Use examples; `agents:` frontmatter documented; any Status-section
  entries these commands satisfy come off the list.

## Testing

- Receipt round-trip with `Previous*` populated and swapped.
- `rollback` toggles twice back to the original state, for both git and
  OCI receipts.
- `rollback` on a receipt with no `PreviousResolved` errors clearly.
- `diff --against latest` and `diff --against previous`, including the
  "no changes" case.
- `Meta.Agents` unknown-target warning fires and the skill still links;
  empty `Agents` produces no warning (regression check against today's
  behaviour).

All new tests follow the existing standard-library-only, table-driven
style: `internal/testrepo` git fixtures, `t.TempDir()`, no network.
