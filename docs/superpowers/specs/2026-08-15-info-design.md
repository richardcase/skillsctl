# info

Design of record for `skillsctl info <name>`, which prints one receipt in full.
Extends [the skillsctl design](2026-08-13-skillsctl-design.md), which lists the
command opposite `list [--json]` without saying anything more about it.

Issue: [#17](https://github.com/richardcase/skillsctl/issues/17).

## Why

`info` is the one command in the spec's CLI surface that was neither built nor
deferred. `bundle`, `sync` and `doctor` are named in the phase 0–1 plan as
things it deliberately leaves out; `info` is in neither list. It fell through a
gap rather than being scheduled.

`list` prints four columns. A receipt holds eleven fields, and the seven `list`
drops are the ones a user reaches for when something is wrong: which repository
this came from, which subpath inside it, which ref it follows, the whole sha
rather than seven characters of it, where the files are, when it arrived, and
which symlinks it created. Today the only way to see them is to read
`state.json`, which means knowing where the store is and that the file exists.

`discover.Meta.Description` has been parsed and tested since the first commit
and rendered nowhere. A receipt records what an install did; the description
records what the skill is *for*, and it is the first thing a user wants when
they read a name in `list` and cannot remember why they installed it.

## A receipt is not the whole answer

The receipt says a symlink was created. Whether it is still there, and still
points where it was pointed, is a different question — and it is the question
someone opens `info` to ask, because a receipt that describes a link the agent
cannot see looks exactly like one that works.

So `info` reads the disk: one `lstat` and one `readlink` per link, naming a link
that is missing, dangling, or pointing somewhere else. That is the whole of it.
It does not re-hash the revision directory to report a tree edited since
install, though `git.Update` already has the check and it would be two lines
here — reporting damage and repairing it belong to the same command, and that
command is `doctor`. Drawing the line at "what a receipt claims versus what is
there" keeps `info` a description of one skill rather than half a health check.

For the same reason a dangling link is exit `0`. `info` answers a question, and
"one of your links is broken" is a successful answer, not a partial one. The
codes in `exit.go` grade how much of the work got done; `info` always does all
of it. `outdated` earns its own code because CI consumes the finding — nothing
consumes `info` that way.

## Decisions

**`--json` is a superset of `list --json`.** `list --json` marshals receipts
verbatim, so the receipt's own fields keep their spelling and their place at the
top level, and `description`, `agents` and `ownership` join them. A script that
does `info X --json | jq .resolved` gets what `list --json` would have given it.
The alternative — a `{"receipt": …}` envelope — is more honest about recorded
versus derived, and breaks that parity for a distinction no caller has asked
for.

The one field that changes meaning is `links`. Its entries carry `state` and
`dest` alongside the recorded `target` and `path`, which makes the JSON view as
useful as the text one. Both views render from a single struct so they cannot
drift.

**The text view shows what the receipt records, not what the store thinks.**
`Slug` and `ContentHash` are store bookkeeping: the slug is visible inside the
revision path, and the hash means nothing without the re-hash `info` does not
do. Both stay in `--json`, where a tool that wants them can have them.

**A pinned receipt must not be told it tracks a default branch.** `pin` clears
`Ref`, and everywhere else in the tool an empty `Ref` means the repository's
default branch — `pin.go`'s `tracked()` says exactly that, and `Update` resolves
against it. For a pinned receipt that reading is wrong twice over: it tracks
nothing, and `update` will not move it. `info` renders the pin first and the ref
as `none — a pin tracks no ref`. Reusing `tracked()` unconditionally would have
made `info` the only command that lies about a pin.

**Ownership is a rendered fact, not a branch.** `ch.Ownership()` already answers
"who owns these files" in three values, and those three are exactly the three
ways this report differs: a store-owned skill has a ref and a revision, an
agent-owned one has a version the agent chose and no ref behind it, and a
user-owned one has neither — whatever is in the directory right now is the
version. `list`, `remove` and `gc` ask `Ownership()` and nothing finer, and so
does this. A receipt whose channel is not registered still prints, falling back
to whatever the receipt recorded — the degradation `Registry.Agents` already
makes, for the reason its comment gives: what is on disk is a fact, and a
command that reports facts should not refuse to describe one.

One qualification on the owner line: `adopt` records a git skill whose `RevPath`
is the user's own working copy, and printing "skillsctl's store" above a path
that plainly is not in it would be a lie the line above contradicts.
`store.Contains` is the test, the same one `pin` uses to decide whether to warn
about the same receipts.

**A plugin has no links, so it names agents instead.** `Plugin.Agents` answers
from the config because the agent that installed a plugin can already see its
skills. The links block is replaced by an `agents` line for that channel, which
is the same distinction `Ownership` draws, shown where a user will read it.

**A missing `SKILL.md` is not an error.** The description is read with
`discover.Root(RevPath)`, and a plugin's install path is a plugin root with no
`SKILL.md` at the top of it. Omitting a line is the right answer; failing the
command because a decoration is absent is not, and it is the same rule the
design already applies to `marketplace.json`.

## Near-misses

`skillsctl info brainstorm` should not answer `"brainstorm" is not installed`
and stop, when `brainstorming` is right there. Five places say some version of
that sentence today — `remove`, `pin`, `unpin`, `link` and `update` — in four
different wordings, none of which suggest anything.

One error type serves all of them:

```go
type NotInstalledError struct { Name string; Suggestions []string }
func (d *DB) NotInstalled(name string) error
```

It lives in `state` rather than `cli` because `internal/update` is one of the
callers and cannot import `cli`, and because the DB is the thing that knows
which names exist. Matching is case-insensitive containment in both directions,
so a truncated guess and an over-long one both hit, capped at three in the order
`DB.List` already sorts. The empty name is guarded: `strings.Contains(x, "")`
is true for every `x`, and without the guard an empty argument would suggest
the whole store.

The message is one line, because `pin` renders these inside `skipped %s: %v`
and a multi-line error would break that frame:

```
"brainstorm" is not installed; did you mean brainstorming?
"zzz" is not installed; run `skillsctl list` to see what is
```

A second method, `Hint`, drops the name for a caller whose own frame already
says which skill this is about — `skipped brainstorm: not installed; did you
mean brainstorming?` rather than the name twice in one sentence. `link` uses it
too, in the branch reached when the argument is neither a receipt's name nor
anything `source.Parse` recognises, which is where a bare mistyped word lands
and so the message where near-misses matter most.

Levenshtein distance was the alternative. It catches `brainstroming` and misses
`brain`; containment catches `brain` and misses `brainstroming`. Containment is
fifteen lines against twenty-five, and the truncated guess is the mistake people
actually make at a shell that offers no completion for these names.

## Surface

```
skillsctl info <name> [--json]
```

```
$ skillsctl info brainstorming
brainstorming
Use this before any creative work - creating features, building components,
adding functionality, or modifying behavior.

channel     git
source      https://github.com/obra/superpowers.git
subpath     skills/brainstorming
ref         main
revision    a1b2c3d4e5f67890a1b2c3d4e5f67890a1b2c3d4
files       ~/.local/share/skillsctl/rev/…/skills/brainstorming
            (skillsctl's store)
installed   2026-08-14 09:12:03 UTC
updated     2026-08-15 11:40:55 UTC

links
  claude    ~/.claude/skills/brainstorming
  codex     ~/.codex/skills/brainstorming  (dangling)
```

Exactly one name: the spec spells it `info <name>`, and unlike `pin` or `update`
there is no batch of them a user wants to act on at once. A line per field, with
`subpath` and `revision` omitted when the receipt holds neither — a `local`
skill has no revision, and an empty cell would read as a missing one rather than
an absent one, which is the reason `list` prints a dash.

`updated` is printed even when it matches `installed`. "Never updated" and
"updated back to where it started" are different facts about a skill, and only
the two timestamps side by side tell them apart.

## Structure

`internal/cli/info.go` holds the command. `internal/target/inspect.go` gains a
read-only `Inspect(linkPath, want)` beside `Link`, `Relink` and `Unlink`, which
already own this package's `Lstat`/`Readlink` vocabulary; a relative target is
resolved against the link's own directory, which is how the filesystem reads it.
`internal/state/suggest.go` holds the error above. `channel.Ownership` gains a
`String`.

No state schema change: `info` writes nothing. It takes the state lock like
every other command, since `state.Open` is the only way in and holding it for a
read is what makes the read consistent.

## Deliberately not in scope

Re-hashing the tree to report a skill edited since install, repairing a broken
link, and reporting a revision directory that has gone missing from the store —
all three are `doctor`. `info` on a name that is *not* installed but is sitting
unmanaged in an agent's skills directory says only that it is not installed;
classifying what is in those directories is `adopt`, and pointing at it from
here would duplicate that judgement in a second place. Folding `adopt.classify`
onto `target.Inspect` is a refactor worth doing and not part of this change.
