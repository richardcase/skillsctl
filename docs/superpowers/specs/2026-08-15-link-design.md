# link

Design of record for `skillsctl link <name> -a <agent>`, which adds a link to a
skill that is already installed. Extends
[the skillsctl design](2026-08-13-skillsctl-design.md), which lists the command
opposite `remove <name> [-a codex]` without saying anything more about it, and
completes the takeover path [adopt](2026-08-14-adopt-design.md) explicitly hands
over.

Issue: [#12](https://github.com/richardcase/skillsctl/issues/12).

## Why

A skill installed before an agent existed on the machine stays invisible to that
agent. `remove <name> -a codex` takes a link away and keeps the receipt; nothing
puts one back. The only route to a second agent's link today is to remove the
skill and install it again, which for a `local` skill discards a receipt the
user cannot reconstruct — the directory it pointed at is the only record of
where it came from — and for a pinned `git` skill silently moves it to the head
of its ref.

`adopt` made this worse before it made it better. It now reports a hand-made
symlink whose name is already managed as unadoptable, with a reason naming this
very command, because adding a second link to an existing receipt was the one
thing it could not do.

The mutation is the smallest in the tool. `Links` is the removal contract, so
appending to it *is* the state change, and `RevPath` already names the skill
directory the new symlink must point at. Nothing is fetched, extracted or
hashed. The design questions are all at the edges: which argument form was
typed, which channels can be served, and what a partial result means.

## The constraints

**`Links` is a set keyed by target.** `linked.Remove` builds its `drop` filter
from `l.Target`, so a receipt holding two links for one agent would plan two
unlinks of one path — the second of which is not an error, because
`target.Unlink` treats a missing link as success, so the damage would be silent.
Every path that appends to `Links` therefore refuses a target already there.
This is what decides the "already linked" case below, rather than any judgement
about convenience.

**A receipt says where its links point.** `update` re-points every link a
receipt records, and `remove` deletes every one of them. So a link may be
recorded on a receipt only when it points at that receipt's `RevPath`. The
command satisfies this by construction — it creates the symlink itself — but
`adopt` finds symlinks it did not create, and for those the condition has to be
checked. It is the whole of what separates the second-link case from a name
collision.

**The plan is `Link` then `Record`.** Both ops already exist, the executor
already rolls `Link` back by unlinking, and `target.Link` is already idempotent
against a symlink pointing where it should. Nothing new is needed to make this
safe; what is needed is refusing to plan a `Link` whose `RevPath` is not there,
since `target.Link` would create a dangling symlink rather than complain.

## Which channels it can serve

| Channel | Ownership | `link <name> -a <agent>` |
| --- | --- | --- |
| `git` | `StoreOwned` | A second symlink into the same revision directory |
| `local` | `UserOwned` | A second symlink into the user's own directory |
| `plugin` | `AgentOwned` | Refused |

The first two rows are one implementation. They differ in where the files came
from, not in how they reach an agent, which is exactly the split `linked`
already exists to express — `Remove` and `Agents` are shared there for the same
reason, and `Link` is `Remove` read backwards.

A plugin has no link to duplicate. Claude Code installed the plugin's skills
into its own cache and can already see them; there is no symlink of skillsctl's
anywhere, which is what `AgentOwned` means. Fanning a plugin's `skills/` subtree
out to other agents is a real feature and a different one — the parent spec
defers it explicitly — so the refusal says that rather than "unsupported".

## Telling `<name>` from `<path>`

`link` already means `install` restricted to a local path. The spec puts both
spellings under one command, so the argument has to be classified before
anything else happens.

The discriminator is a receipt lookup, not the shape of the string. Shape almost
works — `ValidateSkillName` rejects a name containing `/`, and every path form
begins with `.`, `..`, `/` or `~` — but a skill named `foo@bar` parses as a
plugin source and would be shadowed by a rule that asked the string. Asking the
receipts is both correct and the question the user is really asking, and it
matches how `remove` opens.

| Argument | Goes to |
| --- | --- |
| Names a receipt | the name form |
| Parses as a local source | `install` from a path, unchanged |
| Parses as a git or plugin source | today's `link takes a path…` error, unchanged |
| Neither | not installed, and not a path to a directory |

The hazard is the lock. `state.Open` takes an exclusive flock, and `install`
opens its own handle; holding one across the delegation would deadlock the
process against itself, since flock is held per descriptor and a second open in
the same process blocks. So the lookup opens and closes on its own, and the name
form re-opens and looks up again under the handle it will keep. The window
between the two is benign: the second lookup is the one that decides, and it is
the one holding the lock when the receipt is written.

## What happens to each agent

Targets resolve exactly as they do everywhere else: `-a` names them through
`Select`, and an empty `-a` means every *present* target. Presence filters the
default set only — `-a gemini` works whether or not `~/.gemini` exists, because
that is already what `install -a` and `remove -a` mean, and a command that
disagreed with its own inverse about what an agent is would be worse than one
that lets the user name an agent they are about to install.

Per target, then: already in `Links` is reported and skipped, anything else is
linked.

| Situation | Exit |
| --- | --- |
| Every requested target linked | 0 |
| Some linked, some already were | 2 |
| Every requested target already was | 1 |
| A plugin receipt, an unknown name, or a missing `RevPath` | 1 |

That middle row is the reason exit code 2 exists: `link foo -a claude,gemini`
where claude already has it must be distinguishable from having linked nothing,
and the work stands, so it is a `note:` rather than an `error:`. Nothing done
and the reasons on screen is 1, which is the same shape `adopt` settled on.

An agent counts as skipped only when the user named it. Every receipt has at
least one link, so an agent that already has the skill is the ordinary content
of the default set rather than a request that could not be met — counting those
would make the bare `link <name>` exit 2 every time anybody ran it, which is the
same permanent-code-2 trap the dot-entry rule avoids in `adopt`. So `-a claude`
against an agent that has it is a partial result, and no `-a` at all is not.

A `--dry-run` prints the plan and the skipped lines and carries the same code
the real run would, as `install --dry-run` already does. The dry run is exact
because it is the same pass, not a different branch.

## Structure

`Channel` gains one method, next to `Remove` and shaped as its mirror:

```go
// Link adds this receipt to the agents in add, and returns the ops that put it
// there. An empty plan means there was nothing to add.
Link(r state.Receipt, add []target.Target) (plan.Plan, error)
```

It goes on the interface rather than being type-asserted at the call site,
because that is where a mechanism difference belongs — `list`, `remove` and `gc`
ask `Ownership()` and nothing finer, and a fourth channel should be told about
this by the compiler. `linked` implements it once; `Plugin` refuses.

`linked` also takes over the containment guard. `filepath.Dir(linkPath) !=
filepath.Clean(t.Dir)` is currently copied verbatim into `Git.Install` and
`Local.Install`, and `Link` would be a third copy of a check that exists to stop
a name off a third party's `SKILL.md` from escaping the skills directory. One
implementation, one rejection test.

`internal/adopt` stays a pure classifier that imports neither `channel` nor
`plan`. It gains a class, not a capability.

## adopt, completed

`adopt` classifies a symlink whose name is already a managed receipt as
`ClassLink` — an addition to a receipt that already exists — when the
destination it already points at equals that receipt's `RevPath`. A mismatch
keeps today's skip, now with both destinations in the reason, and so does a
target the receipt already records under a different path.

Additions group by name beside `Adoptions()`, so one skill hand-linked into two
agents is one addition carrying two links rather than two that would overwrite
each other — the same reason `Adoptions` groups. There is no interaction with
the name-collision logic: once a receipt claims a name, every entry under that
name goes through the managed path, so an addition and an adoption can never be
proposed for the same name.

The plan stays `Record` alone. The symlink is already on disk and already points
where the receipt will say, so there is nothing to link — the property that
makes `adopt` structurally incapable of planning something destructive is
preserved rather than weakened, and reading a dry run is still the whole of the
check.

An `adopt` run that only adds links has done its job, so it exits 0.

## Surface

```
skillsctl link <name> [-a claude,codex] [--dry-run]
skillsctl link <path> [-a claude,codex] [--skill N…|--all] [--as NAME] [--dry-run]
```

`--skill`, `--all` and `--as` belong to the path form alone and are rejected
with a message naming the form they belong to. A receipt's name is the same in
every agent — that is what makes it the receipt key — so there is nothing for
`--as` to rename, and the skill was chosen when it was installed.

## Deliberately not in scope

Fanning a plugin's `skills/` subtree out to agents other than the one that owns
plugins, which is the other reading of `link <name> -a <agent>` and is deferred
by the parent spec. `unlink`, which does not exist and does not need to:
`remove <name> -a <agent>` is already the inverse, and having two spellings for
it would leave the question of which one forgets the last receipt. And
`--project`, which changes where links go for every command at once rather than
this one.
