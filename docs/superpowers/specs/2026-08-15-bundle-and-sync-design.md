# bundle and sync

Design of record for `skillsctl bundle` and `skillsctl sync <file>`, which move
a set of skills between machines through a human-editable `skills.toml`. Extends
[the skillsctl design](2026-08-13-skillsctl-design.md), which names the closed
loop and the file but never says what is in it.

Issue: [#15](https://github.com/richardcase/skillsctl/issues/15).

## Why

Receipts are the source of truth and `state.json` is where they live: keyed by
name, full of absolute paths, content hashes and install timestamps. That is the
right shape for a store and the wrong shape for a human. Nobody edits it, nobody
commits it, and there is no way to say "these are the skills I use" to another
machine, or to a colleague.

The parent spec settled the answer before any of this was built:

> `skillsctl bundle` emits exactly the `skills.toml` schema — one manifest format
> for both the project scope and the global export, so `bundle > skills.toml`
> followed by `sync skills.toml` on another machine is a closed loop.

So there is one file format, not two, and `--project`
([#16](https://github.com/richardcase/skillsctl/issues/16)) reads the same one.
What this design owes is the schema, and a statement of what `sync` will and
will not do to a machine that does not already match it.

## A receipt is already portable, minus three fields

Most of the schema is decided by what a receipt already holds. `Subpath` is
computed as `filepath.Rel(revRoot, skill.Dir)` whatever selection flags produced
it, and `Git.Prepare` walks from `revRoot/subpath` — so a source and a subpath
name exactly one skill directory, and an entry reconstitutes an install without
replaying the `--skill` or `--as` that originally chose it. That is the whole
reason a manifest can be this small.

`RevPath`, `ContentHash` and the timestamps are machine-local and are dropped.
`Slug` is dropped too: it is derived from the source, and `slugFor` already
falls back to re-deriving it.

`Resolved` is the interesting one. The issue lists it with the machine-local
fields, and for an unpinned skill it is — the point of tracking a ref is that
the sha moves. But **`install --pin` deliberately records no `Ref`**, so a
pinned skill's revision lives in `Resolved` and nowhere else. A manifest that
dropped it could not round-trip the one state a user most wants to be exact.

## The schema

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

[[skill]]
name = 'some-plugin'
source = 'some-plugin@marketplace'
```

`[[skill]]` array-of-tables, the shape `config.toml` already uses for
`[[target]]`, through the `go-toml/v2` dependency that is already here. The
single quotes are `toml.Marshal`'s own output for a string needing no escapes;
every example in the README is written the way the encoder actually writes it.

```go
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
```

### Decisions

**A pinned entry puts its sha in `ref`, with `pinned = true` beside it.** One
field answers "which revision", so syncing a pinned entry is exactly
`install --ref <sha> --pin` — semantics that already exist and are already
tested. The alternative, a second `rev` field mirroring the receipt's
`Ref`/`Resolved` split, buys nothing a reader of this file needs: the receipt
splits them because it records a request and its answer separately, and a
manifest records only the request. It also makes `pinned` derivable from `rev`
being set, which is two ways to say one thing.

This is not a lockfile pretending to be a manifest. An unpinned entry names a
branch and resolves it at sync time, which is the whole difference; `skills.lock`
remains the receipt schema, in a different file, when `--project` lands.

It has a pleasant consequence. `gitx.Resolve` short-circuits a 40-hex ref
without an `ls-remote`, so **a fully pinned manifest syncs without resolving
anything** — no network at all when the store already holds the revisions.

**`agents` is omitted when it covers every present target.** An omitted `agents`
means what an omitted `-a` means to `install`: every agent present on this
machine. Emitting it only where the user chose something narrower keeps the
common manifest clean, keeps it portable onto a machine with a different set of
agents, and preserves a deliberate choice as a deliberate choice.

The comparison is against `cfg.Present()`, not `cfg.Targets`. Against the
configured set, a machine without Gemini would stamp `agents` onto every entry
it ever bundled, which is the opposite of the intent.

A plugin receipt has no links at all — Claude Code installed the plugin and can
already see its skills — so a plugin entry never carries `agents`.

**No `channel` field.** `source.Parse` infers the channel from the shape of the
source exactly as `install` does, and a git URL, an `owner/repo` shorthand and a
`plugin@marketplace` are three unambiguous shapes. Recording the channel would
create a second answer that could disagree with the first. Revisit if
`--from` ([#31](https://github.com/richardcase/skillsctl/issues/31)) ever makes
inference overridable, since that is the same question asked at the CLI.

**`name` and `source` are required.** The name is the receipt key and the thing
`sync` compares against what is installed; without it `sync` cannot tell "this
is already here" from "install this". An ambiguous source at a terminal prints a
listing to a human who is standing there; an ambiguous entry in a file has
nobody to ask. So an entry names its skill, and the way to write a manifest by
hand is to install once and bundle.

**`subpath` is its own field, and `source` may carry one instead.** `bundle`
always writes them separately, because the receipt holds a repo-root-relative
path and that is more precise than whatever shorthand was typed. But `source` is
just a source string, so a hand-written `source = 'o/r//skills/alpha'` parses
the way it would on the command line. What is refused is saying it twice and
differently.

**`version = 1`, always emitted, absent means 1.** A version the build does not
understand is refused in the words `state.Open` uses for the same situation. The
cost is one integer;
[#20](https://github.com/richardcase/skillsctl/issues/20) is what the cost of
not having one looks like.

**Entries are sorted by name.** `DB.List()` already sorts, and a `skills.toml`
that is committed to a repository must produce a stable diff.

## bundle

```
skillsctl bundle
```

No flags, no arguments. The manifest goes to `cmd.OutOrStdout()`, because it is
the command's product rather than its commentary — the same rule that puts
`list --json` on stdout so `list --json > skills.json` captures it. Everything
else goes through `cmd.Println`, which cobra resolves to stderr.

**A `local` skill is left out, and named.** Its source is an absolute path on
this machine and means nothing on another, and `bundle`'s output exists to be
carried elsewhere. Emitting it would put an entry in the file that is knowingly
wrong for the file's only purpose.

```
$ skillsctl bundle > skills.toml
warning: 1 local skill left out of the manifest: my-skill (/Users/me/code/my-skill)
```

Naming it matters more than the exclusion: a silent drop is how a user
discovers on the new machine that something they rely on was never in the file.
Excluding everything is still success — an empty skill list plus the warning is
a truthful account of a machine holding only local skills.

`sync` does not refuse a local entry. `bundle` will not write one, but a
hand-written manifest naming a path that exists on this machine is a coherent
thing to ask for, and the local channel already installs it.

## sync

```
skillsctl sync <file> [--dry-run]
```

**The invariant is that `sync` only ever adds.** It installs what the manifest
names and this machine lacks, and it adds agent links an entry names and a
receipt lacks. It never re-points a ref, never applies or lifts a pin, and never
removes anything. Everything it will not do, it says.

```
$ skillsctl sync skills.toml
installed alpha @ 9f8e7d6 into claude, codex
linked beta into gemini
beta differs: the manifest tracks develop, the install tracks main
not in the manifest: gamma (installed from github.com/x/y)

2 of 3 entries applied
```

That invariant is the design. A verb that installs, updates, re-pins, unlinks
and deletes to make a machine match a file is a much larger promise, and every
one of those mutations already has a command that does it deliberately and says
what it did. Adding is the part with no other spelling. It also makes
idempotence a property rather than an effort: a second run finds every entry
installed with its links complete, and has nothing to add.

Per entry, against the receipts:

| State | What happens |
| --- | --- |
| Not installed | Installed, at the entry's ref and pin, into the entry's agents |
| Installed, matching, links complete | Nothing, and nothing printed |
| Installed, an agent it names has no link | Linked, through the existing `Channel.Link` |
| Installed from a different source, subpath, ref or pin state | Reported; nothing changed |

An install goes through the ordinary path: `source.Parse` of the source and
subpath, a `channel.Request` carrying the ref, the pin and the targets,
`Prepare`, then the single candidate renamed to the entry's name exactly as
`--as` renames it. Nothing about `sync` is a second way to install a skill.

Skills installed here and absent from the manifest are reported after the
entries and **never affect the exit code**. The manifest is not a statement
about what must not be installed, and `sync` has no business having an opinion
about a skill it was not asked about. Reporting them is what makes the omission
visible if it was a mistake.

### The one place "only adds" has teeth

An entry with no `agents` means every present agent. So a skill you deliberately
`remove`d from one agent, on a machine whose manifest does not name agents for
it, is linked back. That follows from the invariant rather than working around
it, the escape hatch is naming `agents`, and the re-link prints its own line —
but it is the one addition a user might not have wanted, and it is written down
here so that it is a decision rather than a surprise.

### Reporting and exit codes

One entry in, one verdict out, in the order the file gave them — `update.Plan`'s
contract, for `update.Plan`'s reason: one unreachable remote must not hide the
rest of the report. Only a request that cannot be interpreted at all fails the
command, which here means an unreadable file, a TOML error, a missing `name` or
`source`, or a version from the future.

| Outcome | Exit |
| --- | --- |
| Every entry satisfied, including a run with nothing to do | 0 |
| Some entries applied, some failed or differ | 2 |
| Nothing applied and at least one entry failed | 1 |

Extras never move that code, and neither does a run that found everything
already in place — a manifest that is already true is the success case, not an
empty one.

A difference is a partial result rather than a failure, and its line names the
remedy inline, because there is no `set-ref` verb today: remove the skill and
run `sync` again, or bring the manifest in line with the machine. This is the
one recurring exit 2 the design accepts, and it is why `--converge` is a real
follow-up rather than a hypothetical one.

The whole run is one plan applied by one executor and one `Commit`, so
`--dry-run` prints `p.Describe()` and returns without a second code path.
Building that plan populates the store and reaches the network, as every other
command's dry run does today
([#23](https://github.com/richardcase/skillsctl/issues/23)).

The channel registry is built once for the run, so `Git`'s per-repository
resolution cache makes N entries from one repository cost one `ls-remote`.

Channels refuse what they already refuse. A plugin entry carrying `ref` or
`pinned` is rejected by `Plugin.Prepare`, in the voice
`rejectRepositoryFlags` already uses, rather than by the decoder — a mechanism
difference belongs behind the interface, and the decoder has no business knowing
which channels can be pinned.

## Structure

`internal/manifest` holds the format and the diff:

```go
func Encode(w io.Writer, f File) error
func Decode(b []byte) (File, error)

// FromReceipts projects receipts into a manifest, returning the local skills
// it left out.
func FromReceipts(rs []*state.Receipt, reg channel.Registry, present []target.Target) (File, []string)

// Plan says what each entry needs and returns the ops that provide it.
func Plan(ctx context.Context, reg channel.Registry, f File, db *state.DB, cfg target.Config) (Report, plan.Plan)
```

`Plan` returns no error, which is `outdated.Check`'s shape rather than
`update.Plan`'s. Everything that could fail the whole command — an unreadable
file, a TOML error, a missing `name` or `source`, a version from the future — is
`Decode`'s job and has already happened by the time `Plan` is called. What is
left is per-entry, and every bit of it is a verdict.

`Plan` takes the whole `target.Config` rather than a resolved target list,
because an entry chooses its own agents: a named `agents` goes through `Select`
and an omitted one through `Present`, which is the same pair `install` resolves
`-a` with.

Its per-entry answer is its own type rather than `channel.Verdict`. That type
describes what an update did to a revision — `updated`, `current`, `pinned`,
`dirty` — and has no way to say `installed`, `linked` or `differs`, which are
the three outcomes this command exists to report:

```go
type Status string

const (
	StatusInstalled Status = "installed" // the entry was not here and now is
	StatusLinked    Status = "linked"    // it was here, and an agent it names now has it
	StatusPresent   Status = "present"   // already satisfied; nothing to say
	StatusDiffers   Status = "differs"   // installed, but not as the entry describes
	StatusError     Status = "error"     // this entry failed; the rest still ran
)

type Verdict struct {
	Name    string
	Status  Status
	Agents  []string // the agents installed or linked, for the report
	Detail  string   // what differs, or why it failed
	Version string   // the resolved sha, when there is one
}
```

A skill absent from the manifest is not an entry and has no verdict, so it sits
beside them rather than among them — the shape `adopt.Report` already uses for
the same reason:

```go
type Report struct {
	Verdicts []Verdict // one per manifest entry, in the file's order
	Extra    []*state.Receipt // installed here, named nowhere in the manifest
}
```

The diff lives with the format rather than in an `internal/sync`, for two
reasons. What it decides is a question about a manifest and a receipt set, so
the manifest is what it is about; and a package named `sync` shadows the
standard library at every import site that needs both, which is a cost paid
forever for a name.

Like `update.Plan`, it is not a pure function — it asks each channel to
`Prepare`, which resolves refs and populates the store. What it does not do is
mutate anything the user can see; that stays in the plan it returns.

`Encode` is the first `toml.Marshal` in this codebase — `target.Load` has only
ever unmarshalled.

`internal/cli/bundle.go` and `internal/cli/sync.go` do flag wiring and rendering
and nothing else, and `sync` carries the three companions every command in this
tree has: `reportSync`, `syncLine` returning `""` for an entry with nothing to
say, and `syncExit` folding the counts into `nil`, `partialf` or an error.

No new `plan.Op`, no new `Channel` method, and no state schema change. `sync` is
`Prepare` and `Install` and `Link` in a loop over a file, which is the strongest
evidence that the schema is the right one: if the manifest needed a mechanism
that did not exist yet, it would be recording something a receipt does not.

## Deliberately not in scope

**Converging on a difference.** A `--converge` that re-points refs and applies
pins is the natural next command, and it is a different contract — worth its own
issue once this lands, so that the invariant above stays legible.

**Pruning.** `sync` never removes a skill the manifest omits. `--prune` reverses
the direction of trust between the file and the machine and nobody has asked for
it.

**`sync -` reading stdin**, which would make `bundle | ssh host skillsctl sync -`
work. Cheap, and not asked for.

**Entries that name no skill** — an `all = true`, or a bare source — which would
let a manifest be written by hand without knowing skill names in advance. It
trades the certainty that an entry means one skill for convenience, and that
certainty is what lets `sync` compare a file against a receipt set at all.

**`--project`** ([#16](https://github.com/richardcase/skillsctl/issues/16)),
which reads this schema but changes where links go for every command at once.
