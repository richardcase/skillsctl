# adopt

Design of record for `skillsctl adopt`, which takes over skills already sitting
in an agent's skills directory. Extends
[the skillsctl design](2026-08-13-skillsctl-design.md), which specifies the
command in phase 4 and states the safety property it must satisfy.

Issue: [#13](https://github.com/richardcase/skillsctl/issues/13).

## Why

A skill installed by hand — `git clone`d into an agent's skills directory, or
symlinked there from somewhere else — is invisible to `skillsctl`. Worse,
`install` refuses to take over a name that already exists on disk, so the only
route into management today is to delete the thing first and hope the source is
recoverable.

On the machine this tool was designed for that is around twenty skills under
`~/.claude/skills`, all hand-made symlinks into `~/.agents/skills/`, which is
not a git repository and carries no provenance for any of them.

`adopt` is the missing takeover path: it reads what is there, decides what it
can honestly manage, and writes receipts. It moves nothing.

## The three constraints

**`Links` is the removal contract.** `linked.Remove` returns an empty plan for a
receipt with no links, and an empty plan means "nothing in `drop` was linked" —
so a linkless receipt could never be removed *or* forgotten. Every receipt
`adopt` writes therefore records at least one symlink. This is what decides the
question of the hand-made directory below, rather than any judgement about
tidiness.

**A hand-made symlink adopts exactly.** `~/.claude/skills/foo →
~/.agents/skills/foo` produces the same receipt `skillsctl link
~/.agents/skills/foo` would have written: source and revision path are the
symlink's target, and the link is the one already on disk. `remove` then takes
away the symlink and leaves the target directory untouched. No new ownership
mode, no new removal contract — adoption of a symlink is a retroactive `link`.

**The plan is `Record` alone.** The symlink already exists and already points
where the receipt says, so there is nothing to link. The plugin channel reached
the same shape from the other direction: a plugin Claude already has is adopted
with a `Record` and no `Exec`. This is what makes the spec's verification step —
"no destructive op appears in the plan" — a property of the design rather than
something a reviewer has to check by eye.

## What adopt does with what it finds

Per present target, per direct child of the skills directory:

| Found | Class | Why |
| --- | --- | --- |
| Symlink to a skill directory | `local` | The receipt `link` would have written |
| Symlink into a clean git checkout with a remote | `git`, pinned | Provenance is recoverable, so record it |
| Symlink into a checkout with uncommitted changes | `local` | See below |
| A real directory | skipped | No symlink means no removal contract |
| Dangling symlink, or a link to a non-directory | skipped | `doctor` reports these |
| A symlink into the store with no receipt | skipped | An orphan; the slug does not reverse into a source |
| A name already in the receipts | managed | Nothing to do |
| No `SKILL.md` | skipped | Not a skill |

### The real directory

A directory `git clone`d straight into `~/.claude/skills` has no symlink, so
there is nothing to record as the removal contract, and taking it over would
mean moving it into the store — which the spec's safety requirement forbids.
`adopt` reports it with the remedy instead: move it out and `skillsctl link` it.
Saying so is more useful than a receipt that cannot be removed.

### Promotion, and why it is pinned

The spec says entries with a detectable git remote are promoted to the `git`
channel. The hazard is that the checkout on the other end of the symlink may be
one the user develops in, and an unpinned `git` receipt would let the next plain
`update` re-point their symlink into the store.

So a promoted receipt is **pinned to the sha it is already at**. `outdated`
still reports it when the tracked ref moves — a pin never hides that — but
`update` skips a pinned receipt unless it is named explicitly. The takeover
completes only when the user asks for it: naming it in `update` extracts that
revision into the store and re-points the link, which is the first moment the
files stop being theirs.

Two fields go with that decision. `RevPath` is the working copy rather than a
store path, because `RevPath` records where the linked files actually are and
that is where they are. `ContentHash` is empty, because the working copy is not
an immutable extraction and re-hashing it would be comparing a directory against
itself; `inspect` already documents an empty hash as the by-hand case and stands
its dirty check down.

### The dirty checkout

Promotion records the working copy's HEAD sha. If the skill's directory has
uncommitted changes, that sha describes something other than the files on disk,
and `list` would report a provenance that is not true. Such an entry stays
`local`, with the reason printed.

Dirtiness is scoped to the skill's own directory, not the whole repository:
unrelated churn elsewhere in a monorepo says nothing about the subtree being
adopted, and the sha still describes it correctly.

## Structure

`internal/adopt` is a pure classifier in the shape of `internal/outdated`: it
reads the filesystem and the receipts, decides, and returns a report. It mutates
nothing, and it never imports `channel` or `plan` — turning classifications into
receipts and ops is the caller's job.

Receipt construction stays in `internal/channel`, next to the install receipt
for the same channel, so the adopted and installed shapes cannot drift apart.

`gitx` gains one read-only method, `Describe`, returning the repository root,
remote URL, current branch, HEAD sha and whether the directory is dirty. One
method rather than four keeps the interface — and every fake implementing it —
to a single change, and the existing unexported `output` helper already takes
the working directory that `git -C` needs.

## Surface

```
skillsctl adopt [-a claude,codex] [--dry-run] [--json]
```

The spec lists only `--dry-run`; `-a` and `--json` are the conventions every
other command already follows.

Exit codes follow the shared table: everything adopted or already managed is 0,
a mix of adopted and skipped is 2 with the skipped entries reported, and finding
nothing adoptable among entries that were skipped is 1 — nothing was done, and
the reasons say why.

## Deliberately not in scope

`doctor`, which fixes what `adopt` only reports: dangling links, rev directories
with no receipt, name collisions across targets. Adding a second agent's link to
an already-managed skill, which is `skillsctl link <name> -a <agent>`. And any
takeover that moves a real directory into the store.
