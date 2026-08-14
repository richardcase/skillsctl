# The plugin channel

Design of record for installing, updating and removing Claude Code plugins
through `skillsctl`, and for the `internal/channel` package the rest of the
channels move into. Extends
[the skillsctl design](2026-08-13-skillsctl-design.md), which specifies the
channel table and puts this work in phase 3.

Issue: [#10](https://github.com/richardcase/skillsctl/issues/10).

## Why

`skillsctl install superpowers@claude-plugins-official` parses and then fails:
`install` rejects every channel but `git`. Everything the plugin channel needs
was built in phase 1 and has sat unused since — the `plan.Exec` op with its
`Executor.Run` injection point, `Source.Plugin`/`Source.Marketplace`, and the
`plugins = true` flag on the claude target, which nothing reads.

Meanwhile channel behaviour has spread. `install` resolves, extracts and
discovers inline; `remove` walks `Links` inline; `internal/update` resolves and
re-links inline and skips anything that is not git with a message that assumes
git is the only channel that can move. Adding a second channel by branching in
each of those places would put the same `switch` in four files. This design
introduces the `internal/channel` package the original spec called for, so each
command asks one question instead.

## The constraint that shapes everything

Claude Code owns plugin state, and its CLI answers only after the fact.

```
$ claude plugin list --json
[ { "id": "superpowers@claude-plugins-official", "version": "6.3.0",
    "scope": "user", "enabled": true,
    "installPath": ".../plugins/cache/claude-plugins-official/superpowers/6.3.0",
    "installedAt": "...", "lastUpdated": "..." } ]
```

`list --json` gives exactly the two facts a receipt needs: `version` and
`installPath`. Nothing gives them in advance. `list --json --available` returns
a different shape whose entries carry the marketplace's `source.ref` and `sha`
but no plugin version, and `claude plugin details` fails on anything not
installed.

So there is no analogue of git's `Resolve`: the plugin channel cannot name the
version it is about to install, and cannot know whether an update will move
anything until `claude plugin update` has run. That is the one place it departs
from the shared shape, and the `Settle` step exists for it and nothing else.

## Ownership

The distinction the rest of the design hangs off:

| | `StoreOwned` (git, local) | `AgentOwned` (plugin) |
| --- | --- | --- |
| Files live in | the skillsctl store, under `rev/` | the agent's own cache |
| Reaching an agent | a symlink per target | already visible to the agent |
| Removal contract | `Links` | the agent's uninstall command |
| `gc` | counts `RevPath` and `Slug` as live roots | ignores the receipt entirely |

`Ownership` is the only thing `list`, `remove` and `gc` need to know about a
channel, and all they need to know. Anything finer belongs behind a method.

That `gc` line is load-bearing rather than tidiness. `store.Collect` treats an
empty `Slug` as "repository identity unknown" and aborts *all* mirror
collection; a plugin receipt reaching it would silently disable mirror gc for
the whole store.

## `internal/channel`

One interface, implemented by `git.go` and `plugin.go`. `local.go` waits for
the local channel; `Registry.For` returns `ErrUnsupported` for it, which is
what keeps today's "the local channel is not supported yet" message alive.

```go
type Channel interface {
    Ownership() Ownership

    // Prepare does the read-only work an install needs before it can name what
    // it would change — resolving a ref and populating the cache, or asking the
    // agent what it already has — and narrows the result to what was asked for.
    Prepare(ctx context.Context, req Request) ([]Candidate, error)

    // Install turns the candidates that survived the name-collision check into
    // the plan and the receipts that plan will write.
    Install(req Request, chosen []Candidate) (plan.Plan, []state.Receipt, error)

    // Update decides what each of these receipts should become and returns the
    // mutations. Every receipt given belongs to this channel.
    Update(ctx context.Context, rs []*state.Receipt, o UpdateOptions) ([]Verdict, plan.Plan, error)

    // Settle completes receipt fields knowable only after the plan has been
    // applied, and returns only the receipts it changed.
    Settle(ctx context.Context, rs []state.Receipt) ([]state.Receipt, error)

    // Remove turns a receipt, narrowed to the agents named in drop, into ops.
    Remove(r state.Receipt, drop map[string]bool) (plan.Plan, error)

    // Agents names the agents a receipt is live in, for list.
    Agents(r state.Receipt, cfg target.Config) []string
}
```

Four properties are deliberate.

**`Prepare` runs under `--dry-run`.** Populating a content-addressed cache and
asking an agent what it already has are both idempotent and invisible, and doing
them first is what lets a plan name exact skills and exact revisions rather
than guess. Nothing a user would notice happens before the plan is printed.

**`Update` is a batch.** Git's one-`ls-remote`-per-repository cache becomes a
local inside the method rather than state on the value, and the plugin channel
answers every receipt from a single `claude plugin list`. `internal/update`
groups receipts by channel, calls each channel once, and merges the verdicts
back into the order the receipts came in.

**`Settle` returns only what it changed**, so a channel that knows everything up
front returns nil and costs nothing. Its results are recorded by applying a
second one-op plan, not by writing to the DB, so the executor stays the only
thing that mutates receipts.

**Rendering stays in `cli`.** A channel that finds a request ambiguous returns
`*channel.Ambiguous` carrying the candidates; `cli` decides how to print them.
No channel holds a `*cobra.Command`.

The verdict vocabulary — `Status`, `Verdict` — lives here rather than in
`internal/update`, because deciding a verdict is what a channel does.
`internal/update` keeps `Entry` and `Status` as aliases so its callers are
undisturbed, and shrinks to what it is actually for: selecting receipts by
name, dispatching, and merging.

## A plugin receipt

The receipt schema already anticipated this; no version bump is needed.

| field | value | why |
| --- | --- | --- |
| `Name` | `--as`, else the plugin name | the skillsctl label, not the plugin id |
| `Channel` | `plugin` | |
| `Source` | `<plugin>@<marketplace>` | the id every `claude plugin` call needs |
| `Resolved` | the plugin version | the spec's "sha, or plugin version" |
| `RevPath` | the agent's install path | the spec's "store rev dir, or plugin install path" |
| `Slug` | empty | nothing of ours is in the store |
| `Links` | empty | the agent already sees it; there is nothing to unlink |
| `ContentHash` | empty | the agent owns the tree; its dirtiness is not ours to judge |
| `Ref`, `Subpath`, `Pinned` | empty / false | a plugin tracks no ref and pins to nothing |

`Resolved` and `RevPath` are empty between the plan being applied and `Settle`
returning. That window is the whole reason `Settle` exists.

Empty `Links` is the honest record, and the alternative was considered and
rejected: a synthetic link entry pointing at the agent's install directory
would make `list` and `remove` work with no changes, at the cost of a receipt
claiming a symlink that is not one — which `doctor` and `gc` would later
believe.

## Behaviour

**`install <plugin>@<marketplace>`** narrows targets to those with
`plugins = true`, then asks the agent what it already has. Naming an agent
without that flag through `-a` is an error rather than a silent narrowing, and
so is having no such agent present — both say `plugins = true` so the remedy is
in the message. A plugin Claude
already has is **adopted**: the plan is a `Record` alone, the version and path
are known up front, and the dry-run is exact. Otherwise the plan is
`Exec{claude plugin install …}` then `Record`, and `Settle` fills in what
claude chose. `--skill`, `--all`, `--ref` and `--pin` have no meaning here and
are rejected by name; `--as` is allowed, because the receipt name is a
skillsctl label and the plugin id lives in `Source`.

**`update`** cannot know in advance whether anything will move, so it always
plans `Exec{claude plugin update …}` + `Record`, with the verdict's `Latest`
empty. After the plan is applied, `Settle` reads the real version and
`update.Reconcile` turns "updated to the version it already had" back into
`current` — so a no-op update prints nothing and does not move the exit code. A
receipt claude no longer has is an error verdict naming the remedy, not a
silent skip.

**`remove`** plans `Exec{claude plugin uninstall …}` + `Forget`. It never
touches the agent's files itself; `target.Unlink` refuses non-symlinks and
would be wrong here anyway.

**`list`** takes the agent column from `Channel.Agents`, so a plugin shows
`claude` without the caller knowing why.

**`gc`** and the reclaim hint skip `AgentOwned` receipts.

## Errors

- A missing `claude` binary is a typed error naming the remedy, not an exec
  failure.
- A failed `claude plugin install` fails the apply with the argv in the
  message. No receipt is written and there is nothing to roll back.
- A failed `Settle` **still commits the receipt**, with the version unknown, a
  warning, and exit 2. A tracked install whose version we do not know beats an
  untracked one that `remove` cannot undo.

`plan.Exec` has no compensating action, and gains none here. Rolling back an
install by uninstalling would be a second mutation of the agent's state on a
path that is already failing.

## Testing

`internal/claudex` wraps the `claude` binary the way `internal/gitx` wraps
`git`, and for the same reason: the agent owns that state and its on-disk
layout is not ours to depend on. It reads only. Every mutation stays a
`plan.Exec` op, which is what keeps `--dry-run` exact — the line printed is the
command that will run.

That leaves two seams, both already the house pattern: `plan.Executor.Run` for
mutations and an injected `output` func on `claudex.CLI` for reads. No unit
test shells out, touches the network, or reads the developer's `~/.claude`.

One opt-in test behind `//go:build manual` really runs
`claude plugin install|uninstall`, per the parent spec. It skips when `claude`
is absent, and skips when the plugin it would use is already installed, so it
can never clobber the state of the machine it runs on. `make test-manual` runs
it; CI does not.

## Not in scope

- **`outdated` for plugins.** They report `n/a` and will continue to: the CLI
  offers no way to see a newer version without installing it.
- **Fanning a plugin's `skills/` out to other agents.** A plugin's skills are
  already visible to Claude Code, which is why `plugins = true` exists; other
  agents are a separate problem.
- **The `local` channel** and `--from git|plugin|local`.
- **`--scope project|local`**, which pairs with the unbuilt `--project` flag.
  Installs use the CLI's default `user` scope.
