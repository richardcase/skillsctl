# Fanning a plugin's skills out

Design of record for linking the skills a Claude Code plugin ships into the
agents that cannot install plugins. Extends
[the skillsctl design](2026-08-13-skillsctl-design.md), which defers this
explicitly, and completes
[the plugin channel](2026-08-14-plugin-channel-design.md) and
[link](2026-08-15-link-design.md), both of which name it as the feature they are
not.

Issue: [#18](https://github.com/richardcase/skillsctl/issues/18).

## Why

The first thing the README promises is that a skill is fetched once and
symlinked into Claude Code, Codex and Gemini. For a plugin that is not true. The
plugin channel records the install and stops, because claude can already see the
skills in its own cache, and every other agent is left with nothing. A user who
installs `superpowers@claude-plugins-official` gets fourteen skills in Claude
Code and none anywhere else, from a tool whose whole claim is that it puts one
copy in front of every agent.

Nothing about the mechanism is hard. The skills are on disk at
`<installPath>/skills/<name>/`, `discover.Walk` already finds every `SKILL.md`
in a tree, and `target.Link` already makes symlinks safely. What makes this a
design rather than a patch is that it gives an `AgentOwned` channel a removal
contract it did not have, and that the directory it links into moves under it.

## The constraints

**A plugin's install path is version-scoped, and old versions stay on disk.**
The path claude reports is `.../superpowers/6.3.0`, and after an update
`.../superpowers/6.4.0` exists beside it. A link left pointing at the old one
does not dangle — it keeps working, serving a version the receipt says was
replaced. A silently stale skill is worse than a broken one, and it is the
reason the update path re-points links rather than leaving them for a repair
command that does not exist.

**The path is unknowable until after the exec.** `claude plugin list` reports a
version and an install path only for a plugin it has already installed, which is
what `Settle` exists for. So the ops that create the links cannot always be in
the plan that precedes them, and the tool's commitment to an exact `--dry-run`
has to be met some other way for that one case.

**`plan.Exec` has no rollback.** The executor unwinds `Link` and `Relink` and
nothing else, deliberately. A link failure discovered at apply time would
therefore roll back the *record* of a plugin claude has really installed, which
is the one outcome worse than not linking at all. Every reason a link cannot be
made has to be found before the plan is built.

**`Links` stops being keyed by target.** One plugin puts fourteen symlinks in
one agent's skills directory, so a receipt now holds several links per agent.
The invariant stated at `linked.go:72` and `adopt.go:287` was never about
targets as such — it was about two entries naming one *path*, which would plan
two unlinks of it and swallow the second. Keyed by (target, path), the reasoning
survives intact and the machinery does not change.

## What the receipt looks like

One receipt per plugin, as before. `Links` fills in.

```json
{ "name": "superpowers",
  "channel": "plugin",
  "source": "superpowers@claude-plugins-official",
  "resolved": "6.3.0",
  "revPath": "~/.claude/plugins/cache/claude-plugins-official/superpowers/6.3.0",
  "links": [
    {"target": "codex",  "path": "~/.codex/skills/brainstorming"},
    {"target": "gemini", "path": "~/.gemini/skills/brainstorming"} ] }
```

The alternative — a receipt per skill — was rejected because the fourteen would
share one `Resolved` that only one command can move, and `remove brainstorming`
would have to mean either "uninstall the whole plugin for everyone" or something
new. A second link list beside `Links` was rejected because `remove`, `adopt`
and `gc` would each have to learn about both, for a distinction only the plugin
channel can see.

`RevPath` stays the plugin's install root. The links point at subdirectories of
it, which is a relationship `adopt` has to be taught (below) but which is the
honest record: the root is what claude owns and what `Settle` reads back, and
the subdirectories are derived from it by a walk.

`Ownership` stays three-valued and the plugin stays `AgentOwned`. The split is
about where the files live and what `gc` counts as live, not about whether
skillsctl made a symlink — `liveRoots` must go on skipping a `RevPath` that is
outside the store. Only the doc comments change, because the old ones explain
the split in terms of links.

## Link becomes reconciliation

`Channel.Link` currently means "add this receipt to these agents". It widens to
mean **make these agents hold what the receipt says they should**: add what is
missing, re-point what moved, drop what the source no longer ships.

```go
// Link makes the agents in add hold what this receipt says they should: it adds
// the links that are missing, re-points the ones whose destination has moved,
// and removes the ones whose skill the source no longer ships. An empty plan
// means they already agreed.
//
// The reasons are separate from the error because a name another skill already
// holds skips one link rather than failing the command.
Link(r state.Receipt, add []target.Target) (plan.Plan, []string, error)
```

Git and local satisfy the wider contract for free — one skill, one known path,
so "already agreed" is the only case they have — and `linked.Link` changes only
in what it keys its held-set by. But the wider contract is what lets one method
serve all three of the moments a plugin's links have to be made or mended:

| Call site | `add` is | What the plugin channel does |
| --- | --- | --- |
| `install`, after `Settle` | the requested targets | links every skill it ships into every agent that cannot install plugins |
| `update`, after `Settle` | the targets already in `Links` | re-points the survivors at the new version directory, unlinks the skills the plugin dropped, links the ones it gained |
| `link <name> -a <agent>` | the named agents | links every skill into an agent that did not have them |

So neither `install` nor `update` grows a branch on the channel. Both grow the
same post-settle step, which for every channel but this one produces an empty
plan and applies nothing. That step is where the fan-out lives, and it is one
call at each of two call sites.

`Settle` keeps its signature. Widening it to return a plan was the obvious
alternative and is worse: the install case has no links to reconcile *from*, so
`Settle` would need the requested target set threaded into it, and a method that
completes a receipt's fields would have become a method that also mutates the
filesystem.

## What a dry run can say

| Case | Path known before the plan | Dry run |
| --- | --- | --- |
| A plugin claude already has | yes, from `Prepare` | exact: every link op is in the plan |
| A fresh install | no | exec, record, and a note naming the agents |
| An update | no, the new one | exec, record, and a note |

The first row is the one that matters most, because adopting a plugin claude
already has is how most people will meet this, and it costs nothing to be exact
there: `Prepare` already reads `InstallPath`, so `Install` can walk it and emit
the links itself.

For the other two, the plan gains one op:

```go
// Note is a line in the plan that changes nothing. It exists for the one thing
// this tool cannot predict: a plugin's install path is decided by claude and
// read back afterwards, so the links that follow an install or an update cannot
// be named in the plan that precedes them. Printing nothing would make the dry
// run silently short of what the command does, which is worse than printing a
// sentence that admits it.
type Note struct{ Text string }
```

Saying "then link the skills it ships into codex, gemini, once claude reports
where it put them" is not exactness, and the doc comment should not pretend
otherwise. It is the same admission `update` already prints when it says it will
move a plugin "to whatever version its source publishes" — the honest shape for
a fact that does not exist yet.

## Choosing what to link, and what to skip

The walk is `discover.Walk` over `<RevPath>/skills`. Only that subdirectory: a
plugin's root holds commands, hooks, agents and its own tests, and a `SKILL.md`
in any of them is not a skill the plugin publishes. A plugin with no `skills/`
directory is not an error: the install succeeds and records the plugin as it
does today, because there is nothing to fan out rather than something that
failed to. `link <name> -a codex` against one says so rather than reporting a
silent success.

The name is the frontmatter `name`, falling back to the directory name, which is
the rule the parent spec already sets for a walked tree. It is third-party data
becoming a path, so it goes through `ValidateSkillName` and `linkPathFor` on
every join rather than once.

Per intended link path, before anything is planned:

| What is there | Plan |
| --- | --- |
| Nothing | `Link` |
| A symlink already pointing at the intended destination | nothing |
| A symlink this receipt records, pointing elsewhere | `Relink` |
| A symlink or directory that is somebody else's | skip, with a reason |
| Recorded, but the skill is gone from the plugin | `Unlink` |

The fourth row is why `Link` returns reasons. One name taken in one agent must
not cost the other thirteen skills, so it is a skipped line and exit 2 — the
shape `dropInstalled` already has for a partial install — rather than an error.
Letting `target.Link` discover it at apply time is not an option, because of
what `Exec` cannot roll back.

## Which agents, and how they are removed

The default target set becomes every present agent, which is what it already is
for git. The set must still contain an agent with `plugins = true`, and `-a
codex` alone stays the error it is today: nobody in that set can fetch the
plugin, so there would be nothing to link. `plugins = true` therefore keeps its
meaning exactly — it gates *installing* a plugin, never *seeing* one — and the
issue's suggestion that non-Claude targets opt in through it is not followed.
That suggestion predates the flag having a reader.

`Agents` answers from the config's plugin-capable agents together with the
receipt's link targets. Neither alone is right any more: claude holds the plugin
without a link, and codex holds links without being able to install one.

Removal is two contracts in one receipt, so it splits by what was named:

| Command | Effect |
| --- | --- |
| `remove <name>` | uninstall through claude, unlink every agent, forget the receipt |
| `remove <name> -a codex` | unlink codex's links, keep the receipt |
| `remove <name> -a claude` | refused, while any link exists |
| `remove <name> -a <an agent with no links>` | empty plan, reported by the caller |

The refusal is the only interesting row. Uninstalling the plugin deletes the
directory every other agent's links point into, so `-a claude` would either have
to strand them or silently do more than the user named. Both are worse than an
error that names the command which does mean that:

```
error: claude owns the superpowers plugin, and uninstalling it would strand its
  skills in codex, gemini.
  Run `skillsctl remove superpowers` to remove it everywhere.
```

The refusal is conditional on links existing. A plugin nobody has fanned out has
nothing to strand, and `-a claude` on one goes on meaning what it means today.

## adopt, which would otherwise offer our own links back

`adopt` classifies by looking a receipt up under the entry's directory name. A
fanned-out link is called `brainstorming` and its receipt is called
`superpowers`, so the lookup misses. What follows is worse than a miss: the link
resolves to a real directory holding a `SKILL.md`, it is not in the store, and a
plugin's cache directory is itself a git checkout — so `promote` reaches it,
`gitx.Describe` succeeds, and `adopt` offers to record skillsctl's own link as a
new git skill.

The fix is to ask the stronger question first. Before the name lookup, ask
whether any receipt's `Links` records this exact path; if one does, the entry is
managed. A link's identity is its path, not the name of the directory it sits
in, and that has been true all along — the existing first loop in `managed` is
this same question asked of one receipt instead of all of them, and is subsumed
by it.

`adopt` gains no capability and still imports neither `channel` nor `plan`. The
second-link case is untouched: it is reached only through the name lookup, which
a fanned-out link no longer arrives at.

## outdated, which stops saying n/a

`outdated` reports every plugin as `n/a` today, on the ground that only a git
ref can move. That was true when a plugin receipt described nothing but itself.
It now describes symlinks, and there is a real question to ask: does the path
those links point into still exist as the plugin's install path?

So a plugin receipt is checked against `claude plugin list`, and a receipt whose
`RevPath` or version disagrees with what claude has now is `stale`, with the
version claude has as `Latest`. That is exactly the case a `claude plugin
update` run outside skillsctl produces, and it is the only way that case becomes
visible before the next `skillsctl update` repairs it.

This is not the marketplace comparison that [#36] wants, and must not be
described as one: nothing here asks whether a newer version has been published.
`Check` grows a `claudex.Plugins` parameter, called lazily so that a store with
no plugin receipts never shells out to claude, which keeps the package's promise
that it fetches nothing.

[#36]: https://github.com/richardcase/skillsctl/issues/36

## Surface

```
skillsctl install <plugin>@<marketplace> [-a claude,codex] [--as NAME] [--dry-run]
skillsctl link <name> -a <agent> [--dry-run]
skillsctl remove <name> [-a codex] [--dry-run]
skillsctl outdated
```

No new flags. `--skill`, `--all`, `--ref` and `--pin` go on being rejected by
name for a plugin source: a plugin is still installed whole, and which of its
skills reach an agent is not a choice this design adds.

## Deliberately not in scope

Reporting a plugin whose marketplace has published a newer version, which is
[#36] and needs a network read this design does not do. Project-level fan-out,
which is [#16] and changes where links go for every command at once. `doctor`,
which the reconcile makes less necessary rather than unnecessary — a link
somebody deletes by hand is still nobody's job to notice. And unlinking an agent
that has been deleted from the config since install, which is a question about
config drift rather than about plugins.

[#16]: https://github.com/richardcase/skillsctl/issues/16
