# remote profile sync and tags

Design of record for two extensions to `skillsctl sync`/`bundle`: syncing
against a `skills.toml` held in a git repository instead of only a local file,
and tags for organizing a large skill set. Extends
[bundle and sync](2026-08-15-bundle-and-sync-design.md), which settled the
manifest schema and `sync`'s "only ever adds" contract; this design changes
where the manifest comes from and adds one optional field to it.

Issue: [#88](https://github.com/richardcase/skillsctl/issues/88).

## Why

`bundle`/`sync` already give a team one manifest format for moving skills
between machines, but `sync` only reads a local file — a team keeping one
canonical list has to pass it around by hand (Slack, a wiki page, a shared
drive) instead of pointing at the git repo where it actually lives. And a
manifest with dozens of entries has no way to say which of them belong
together, so `list`/`bundle` have no way to work on a subset.

Both are additive to the bundle-and-sync design rather than a revision of it:
the schema, the "only ever adds" invariant, `manifest.Plan`'s report and exit
codes are all unchanged. What changes is where `sync` gets its `manifest.File`
from, and one new field on `Entry`/`Receipt`.

## Remote profile sync

### The interface: no new flag, no new subcommand

```
skillsctl sync <file-or-source> [--dry-run]
```

`sync team/skills-profile` and `sync ./skills.toml` are the same command. The
argument is tried as a local path first — `os.Stat` — and only falls back to
being parsed as a source if no file exists there:

```go
if _, err := os.Stat(arg); err == nil {
    blob, err = os.ReadFile(arg)
} else {
    blob, err = fetchManifest(ctx, arg, e)
}
```

This is the whole reason the fallback is safe to add: it changes behavior for
exactly the arguments that name nothing on disk today, which is every
argument that currently fails with "no such file or directory". Every
existing `sync skills.toml` keeps reading that file, even if the string would
also parse as a plausible source — a local match always wins, so there is no
new ambiguity to resolve for existing users.

**Why not an explicit `--repo` flag.** `install`, `add` and every other
command that takes a source already accept the same handful of shapes —
`owner/repo`, a full git URL, `plugin@marketplace`, a local path — and pick
the channel by the shape of the string, not by which flag introduced it.
Making the profile source an exception, requiring `--repo` where every other
command infers, would be a second convention for the one command that most
wants to feel like the others. The cost is the stat-first dispatch above,
which is a few lines once.

**What shapes are accepted.** Only the shapes that name a git repository —
`owner/repo`, a full git URL, scp-form. Not `oci://`, not `plugin@marketplace`:
a profile is a git repository with a file in it, not an installable skill, and
`source.Parse`'s OCI/plugin channels have no notion of "a file at the root of
this thing." Reusing `source.Parse`'s own shape-detection (rather than writing
a second regex) keeps "what does this string name" answered in one place.

### Fetching the manifest

`skills.toml` always lives at the repo root — no subpath, unlike an installed
skill. A team's profile repo is a repo whose job is the manifest (and,
optionally, the skills it lists, if they live in the same place); it is not a
path inside an unrelated monorepo. This keeps the fetch a fixed, un-parameterized
operation and matches how `bundle`'s output is meant to be used: `bundle >
skills.toml`, commit it at the root of some git-hosted place, `sync` that
place from anywhere.

`source.Parse` has no ref syntax of its own — an owner/repo shorthand names a
repository, never a branch, and `install` already takes the ref it wants as a
separate `--ref` flag rather than embedding one in the source string. `sync`
gains the identical flag, wired the same way
(`cmd.Flags().StringVar(&ref, "ref", "", "branch, tag or sha the profile
repository tracks (default: its HEAD)")`), used only when the argument
resolves to a remote source; a local file ignores it.

The fetch reuses `store.Ensure` — the same call `channel.Git.Prepare` makes
for every install — rather than driving `gitx` directly:

1. `source.Parse(arg)` resolves the shape (must be `ChannelGit`; `ChannelOCI`
   and `ChannelPlugin` are refused, since a profile is a git repository with a
   file in it, not an installable skill).
2. `gitx.Resolve(ctx, src.RepoURL, ref)` turns the `--ref` flag (or "") into a
   sha, exactly as `Git.Prepare` resolves an install's ref.
3. `store.Ensure(ctx, git, src.Slug(), src.RepoURL, sha)` mirrors the
   repository and extracts that sha into the content-addressed store,
   returning the revision's path — a no-op if either is already cached. This
   is the same idempotent call an install of a skill *from* that same
   repository would make, so a profile repo that also hosts the skills it
   lists shares its mirror and revision cache with them, and a second `sync`
   against an unchanged profile (by any user, or the same user twice) touches
   neither the network nor the disk beyond one `ls-remote`.

`<revision>/skills.toml` is then read and decoded exactly as the local-file
path decodes it today. A missing file, an unreadable repo, or a TOML error
fails the whole command before planning starts — the same failure class
`Decode` already owns for a local file.

No new `gitx` method, no new store method. This is the evidence the mechanism
was already there: fetching a file out of a resolved git tree is what
`store.Ensure` plus a filesystem read already does for every skill install,
and a manifest fetch is a smaller case of the same operation, not a different
one.

## Tags

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

and the matching field on `state.Receipt`. `entryFor` (`internal/manifest/bundle.go`)
copies `Receipt.Tags` into `Entry.Tags`; `manifest.Plan`'s install path copies
`Entry.Tags` into the receipt it creates for a `StatusInstalled` verdict.

**Tags are metadata, not identity.** They join `Agents` rather than `Ref`,
`Subpath` or `Pinned`: never part of the differs-comparison, and — following
the same "only ever adds" invariant that governs links — never rewritten on a
skill that is already installed. A second `sync` that changes only the `tags`
of an already-present entry changes nothing; the remedy, if that is wanted, is
the same one `StatusDiffers` already names for any other field: remove the
skill and sync again, or edit the manifest to match. This keeps tags out of
the one part of the design (`Plan`'s per-entry verdict) that has a carefully
scoped contract already, at the cost of tags only ever being set at first
install — acceptable, since re-tagging a manifest a team already syncs from is
the common case, and re-tagging what is already on one machine is not what
this feature is for.

### `list --tag` and `bundle --tag`

Both take a repeatable `--tag <name>` flag, OR semantics (a receipt matching
any given tag is included), mirroring `list`'s existing `--channel`/
`--exclude-channel` and its `filterReceipts` helper exactly — a third
predicate over the same receipt slice, not a new filtering mechanism.

`list` gains a `TAGS` column (table output) and the field already appears in
JSON output once it exists on `state.Receipt`. `bundle --tag frontend` filters
which receipts `FromReceipts` projects, so `skillsctl bundle --tag frontend >
frontend.toml` produces a scoped manifest a team can sync independently — the
concrete answer to "organizing a large skill set" the issue asks for, built
entirely from the two existing commands.

No tag vocabulary, taxonomy or validation. A tag is any string a receipt or
entry carries; `skillsctl` never enumerates or constrains the set in use, the
same way it has never constrained a skill name beyond `target.ValidateSkillName`.

## Deliberately not in scope

**Diff/rollback (#85).** The issue that proposed this design explicitly notes
it benefits from #85 landing first: a team pulling someone else's profile
needs a way back out if it names something unwanted. This design does not add
one. The existing invariant — `sync` only ever adds, never removes, never
re-points a ref or a pin — is the interim safety net: a bad remote profile can
over-install, never destructively change what is already there. `--dry-run`
already lets a user preview a remote sync's plan before applying it, which is
today's answer to "what would pulling this profile do."

**A configurable manifest path within the profile repo.** Root-only, fixed
name, matching the local-file convention exactly. A team that wants the
manifest inside an existing monorepo can put a thin repo (or a git submodule,
or a subtree) at the root instead; teaching the fetch a subpath is a small
addition later if that turns out to be wanted, not a decision this design
needs to make now.

**Tag-based `sync` filtering** (`sync <profile> --tag frontend`, installing
only entries carrying a given tag). Nothing here blocks it, but it is a
separate question from where the manifest comes from, and the issue's
"tags/grouping in list/bundle" does not ask for it. Worth its own issue if a
team wants a partial sync of a shared profile.

**A separate cache for the fetched manifest.** None is needed: `store.Ensure`
already caches both the mirror and the extracted revision, keyed by slug and
sha, so a second `sync` against an unchanged profile reads `skills.toml`
straight from disk.

## Structure

`internal/manifest`:

```go
// FetchRemote resolves raw as a git repository at ref (empty meaning HEAD)
// and returns the decoded skills.toml at its root.
func FetchRemote(ctx context.Context, raw, ref string, g gitx.Git, st *store.Store) (File, error)
```

in `internal/manifest`, called from `internal/cli/sync.go` only when
`os.Stat(arg)` fails, in place of today's unconditional `os.ReadFile`. It is a
thin wrapper: `source.Parse`, `gitx.Resolve`, `store.Ensure`, then
`os.ReadFile` + `Decode` on `<revision>/skills.toml` — no behaviour beyond
composing calls that already exist elsewhere. `Entry.Tags` and `Receipt.Tags`
are plain
field additions with no new methods; `entryFor` and `Plan`'s install path each
gain one line copying the field across.

`internal/cli/list.go` and `internal/cli/bundle.go` each gain a repeatable
`--tag` flag wired the way `list`'s `--channel` already is, and `list`'s
tabwriter header gains a `TAGS` column.

No new package, no new `plan.Op`, no new `Channel` method, no state schema
migration beyond the one field — `state.DB`'s receipts are already read from
JSON by field name, so an old `state.json` with no `tags` key decodes with a
nil slice, and a receipt written before this change is indistinguishable from
one installed with no tags.
