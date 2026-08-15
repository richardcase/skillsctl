# skillsctl

Homebrew for agent skills: install, update and remove agent skills from git
repositories, with a receipt for every install so update and removal are
deterministic. One store, symlinked into every agent you use.

[![CI](https://github.com/richardcase/skillsctl/actions/workflows/ci.yml/badge.svg)](https://github.com/richardcase/skillsctl/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/richardcase/skillsctl?sort=semver)](https://github.com/richardcase/skillsctl/releases/latest)
[![Downloads](https://img.shields.io/github/downloads/richardcase/skillsctl/total)](https://github.com/richardcase/skillsctl/releases)
[![Homebrew](https://img.shields.io/badge/homebrew-richardcase%2Ftap-orange)](https://github.com/richardcase/homebrew-tap)

[![Go](https://img.shields.io/github/go-mod/go-version/richardcase/skillsctl)](go.mod)
[![Go Reference](https://pkg.go.dev/badge/github.com/richardcase/skillsctl.svg)](https://pkg.go.dev/github.com/richardcase/skillsctl)
[![License](https://img.shields.io/badge/license-AGPL--3.0-blue)](LICENSE)

## Why

Skills spread by copy-paste. You clone a repo into `~/.claude/skills`, copy the
same directory into `~/.codex/skills`, and a month later nothing records where
any of it came from, which commit you took, or what to delete to undo it.

`skillsctl` makes that a package-manager problem instead: one fetch, one copy on
disk, symlinks into every agent, and a receipt that makes the reverse operation
exact.

## Features

- **One store, every agent.** A skill is fetched once and symlinked into Claude
  Code, Codex and Gemini. One copy to update, not three to keep in sync.
- **A receipt for every install.** `skillsctl list` shows what is installed, at
  which commit, and in which agents. `remove` unlinks exactly what was created —
  it never guesses.
- **`--dry-run` that is exact.** Commands build a plan of the mutations and
  print it. What you see is what runs; the dry run is not a separate code path.
- **Updates that keep your choices.** `skillsctl update` moves a skill to the
  head of the ref it tracks, keeping the name you installed it under, the agents
  you linked it into, and its pin. A skill you edited through its symlink is
  reported rather than overwritten.
- **Pin to an immutable commit.** `--ref v1.2.0 --pin` freezes the resolved sha
  so a later update skips it. `skillsctl pin` and `skillsctl unpin` add and
  remove a pin after the fact, without a remove and reinstall.
- **Safe by construction.** Path-escaping skill names, subpaths and tar entries
  are rejected; an existing file is never clobbered; nothing but its own
  symlinks is ever deleted; and links created by a failed apply are rolled back.
- **Fast on repeats.** A git mirror cache plus a content-addressed revision
  store means reinstalling a commit you already have does no network work.
- **Develop a skill in place.** `skillsctl link ./my-skill` registers a
  directory you are working in, linked rather than copied, so every edit is live
  in every agent immediately. `remove` takes away the symlinks and never the
  directory.
- **Reach an agent you installed something before you had.**
  `skillsctl link avoid-ai-writing -a gemini` adds a link to the revision that
  skill is already on, without fetching anything or disturbing a pin. It is the
  exact inverse of `remove -a`.
- **Takes over what is already there.** `skillsctl adopt` records the skills
  already sitting in each agent's skills directory, so hand-made symlinks stop
  being invisible. One that leads into a clean git checkout is recorded with the
  sha it is at, pinned; one into a second agent for a skill already managed is
  added to its receipt. Nothing is moved, copied or deleted.
- **Claude Code plugins too.** `skillsctl install superpowers@claude-plugins-official`
  installs through `claude plugin` and records a receipt, so a plugin shows up in
  `list` and comes out with `remove` alongside everything else. A plugin Claude
  already has is adopted rather than reinstalled.
- **Repositories of many skills.** `--skill` takes the ones you name, `--all`
  takes every one it finds, and they share a single copy of the repository. A
  bare `install` on such a repository lists what is there rather than guessing.
- **Disk you can get back.** `skillsctl gc` deletes the revisions and mirrors no
  installed skill references, and reports what it freed. Nothing shared is
  collected while any skill still points at it.
- **Scriptable.** `skillsctl list --json` emits the raw receipts, and a partial
  install exits `2` so a script can tell it from having installed nothing.
- **One static binary.** No runtime dependency beyond `git`, and `claude` only
  if you install plugins.

## Install

```bash
brew install richardcase/tap/skillsctl
```

(macOS only — the Homebrew formula publishes a cask. On Linux, use the `.deb`/`.rpm`
packages or the tarball below.)

Or grab a binary or `.deb`/`.rpm` from the [releases page](https://github.com/richardcase/skillsctl/releases),
or build from source with `go install github.com/richardcase/skillsctl/cmd/skillsctl@latest`.

## Use

```bash
skillsctl install conorbronsdon/avoid-ai-writing   # link into every agent found
skillsctl install owner/repo/path/to/skill         # a skill inside a monorepo
skillsctl install owner/repo//path/to/skill        # the same, boundary spelled out
skillsctl install owner/repo --skill web-research  # pick one (repeat for more)
skillsctl install owner/repo --all                 # every skill in the repo
skillsctl install owner/repo -a claude             # just one agent
skillsctl install owner/repo --ref v1.2.0 --pin    # pin a version
skillsctl install owner/repo --dry-run             # show what would change
skillsctl install superpowers@claude-plugins-official  # a Claude Code plugin
skillsctl link ./my-skill                          # a skill you are writing
skillsctl install ./my-skill                       # the same thing
skillsctl link avoid-ai-writing -a gemini          # into an agent that missed it
skillsctl list                                     # what's installed
skillsctl list --json                              # the raw receipts
skillsctl outdated                                 # what has moved upstream
skillsctl update                                   # move everything to its ref's head
skillsctl update avoid-ai-writing                  # just this one, pin or not
skillsctl update --dry-run                         # show what would change
skillsctl pin brainstorming                        # freeze it where it is
skillsctl unpin brainstorming                      # let it follow its ref again
skillsctl unpin brainstorming --ref develop        # ...this ref, from now on
skillsctl remove avoid-ai-writing                  # unlink everywhere
skillsctl adopt --dry-run                          # what is already in your agents
skillsctl adopt                                    # take it over
skillsctl gc                                       # reclaim disk nothing uses
skillsctl gc --dry-run                             # show what it would free
skillsctl version
```

```
$ skillsctl list
NAME              CHANNEL  VERSION           AGENTS
avoid-ai-writing  git      a1b2c3d           claude,codex
brainstorming     git      9f8e7d6 (pinned)  claude
superpowers       plugin   6.3.0             claude
my-skill          local    -                 claude
```

A source can be `owner/repo`, `owner/repo/path/to/skill`, any git URL
(https, ssh or scp-style), or a local path. `//` separates a repository from a
subpath inside it — the only way to name one in a `.git`-suffixed or `git@host:`
URL, where the repository boundary is otherwise the whole path.

A repository holding several skills needs `--skill <name>` (repeatable, matching
a skill's name or its path) or `--all`. Without one of them, `install` lists what
it found and stops rather than guessing:

```
$ skillsctl install vercel-labs/agent-skills
skills in https://github.com/vercel-labs/agent-skills.git @ 7c41bf0:
  pdf-forms     Extract and fill PDF forms
  web-research  Research a topic against primary sources
error: this repository holds 2 skills: pass --skill <name> (repeatable) or --all
```

`outdated` compares each skill against its remote, reading refs only — nothing is
fetched. It exits `3` when an update is available, so it works as a CI check:

```
$ skillsctl outdated
NAME              CHANNEL  REF   CURRENT  LATEST   STATUS
avoid-ai-writing  git      HEAD  3c0fd8a  3c0fd8a  current
brainstorming     git      main  525e31b  9071811  outdated
pinned-one        git      HEAD  525e31b  9071811  outdated (pinned)
note: 1 update available
```

Pinned skills are listed and marked, so a pin never hides the fact that something
moved, but they do not set that exit code on their own — `update` skips them.

`update` re-points each symlink at the new revision and rewrites the receipt,
keeping the name, the agents and the pin:

```
$ skillsctl update
updated avoid-ai-writing 3c0fd8a -> 9071811
skipped brainstorming: edited since it was installed; pass --force to update it anyway
skipped pinned-one: pinned at 525e31b; name it explicitly to update it
1 revision (4.1 MB) now unreferenced; run `skillsctl gc` to reclaim
```

Naming a skill updates it even when it is pinned, re-pinning it at the new
commit. Revision directories carry no `.git`, so a skill edited through its
symlink is spotted by re-hashing it against what was recorded at install time,
and skipped rather than overwritten — `--force` updates it anyway, discarding
the edit. The old revision stays on disk until `skillsctl gc`, so a failed
update leaves the previous one linked and the receipt untouched.

A pin can be added and removed after the fact, so changing your mind costs one
command rather than a remove and a reinstall:

```
$ skillsctl pin brainstorming
pinned brainstorming at 9f8e7d6 (it no longer tracks main)

$ skillsctl unpin brainstorming
unpinned brainstorming; it now tracks the repository's default branch
```

Neither fetches anything or moves a symlink: both write one field on the
receipt, which is why `--dry-run` on them prints a `record` line and nothing
else. A pinned skill tracks no ref, so `pin` says which one it dropped and
`unpin` says what the skill follows now — `--ref` names another, and is checked
before it is recorded so a typo fails here rather than in the next `update`.
Only skills fetched from git can be pinned: a local skill is whatever is in its
directory right now, and a plugin is at whichever version Claude installed.

## How it works

Skills are fetched once into `~/.local/share/skillsctl` and symlinked into each
agent's skills directory, so one copy serves Claude Code, Codex and Gemini.

```
~/.local/share/skillsctl/
  cache/<slug>.git      bare git mirror, reused across installs and refs
  rev/<slug>/<sha>/     the extracted tree at one commit
  state.json            receipts
```

A receipt records the source, channel, requested ref, resolved sha, whether it
is pinned, the revision path, a content hash of the tree, and every symlink the
install created — which is what makes `remove` deterministic.

A local skill is recorded but never copied: the receipt holds the directory you
gave and the symlinks point straight at it, so edits are live and there is
nothing in the store. It has no revision, no content hash and nothing to update
from — `list` shows a `-` for its version and `update` says so. Removing it
takes away skillsctl's own symlinks and leaves your directory exactly as it was.
A directory inside the store, or already inside an agent's skills directory, is
refused rather than linked.

`adopt` is how a skill that was installed by hand becomes one of these. A
symlink is recorded exactly as `link` would have recorded it — the same receipt,
so removing it later takes away the symlink and leaves its target alone. One
that leads into a git checkout with a remote is recorded on the `git` channel
instead, at the sha the checkout is at and pinned, so `outdated` still reports
when the ref moves while `update` re-points it only when you name it. A checkout
with uncommitted changes stays local, because the sha would not describe the
files on disk. A real directory sitting in a skills directory is reported rather
than adopted: there is no symlink to record as the removal contract, and adopt
moves nothing. Nor does it touch anything already managed, anything dangling, or
anything without a `SKILL.md` — it says what it found and why.

A hand-made link into a second agent, for a skill that is already managed, is
added to the receipt that manages it — the same amendment
`skillsctl link <name> -a <agent>` makes, found after the fact. It has to point
where that receipt already says its files are, since a receipt is what `update`
re-points and `remove` deletes; one that leads somewhere else is reported
instead.

A plugin is the second exception, because Claude Code owns it. `skillsctl` records the
`plugin@marketplace` id, the version and the install path claude reported, and
nothing else: there is no revision in the store, no content hash and no symlink,
since a plugin's skills are already visible to the agent that installed it. So
`install`, `update` and `remove` run `claude plugin install|update|uninstall`
and read back what claude decided, `list` shows the plugin's version, and `gc`
leaves it alone. `claude` must be on `PATH`; nothing else needs it.

Locations can be overridden with environment variables:

| Variable | Overrides | Falls back to |
| --- | --- | --- |
| `SKILLSCTL_HOME` | the store | `$XDG_DATA_HOME/skillsctl`, then `~/.local/share/skillsctl` |
| `SKILLSCTL_CONFIG` | the config file | `$XDG_CONFIG_HOME/skillsctl/config.toml`, then `~/.config/skillsctl/config.toml` |

## Commands

| Command | Flags | Does |
| --- | --- | --- |
| `install <source>` | `--skill`, `--all`, `-a/--agent`, `--ref`, `--as`, `--pin`, `--dry-run` | Fetch one or more skills and link them into each agent |
| `install <p>@<m>` | `-a/--agent`, `--as`, `--dry-run` | Install a Claude Code plugin through `claude plugin` |
| `link <name>` | `-a/--agent`, `--dry-run` | Link an installed skill into another agent |
| `link <path>` | `-a/--agent`, `--skill`, `--all`, `--as`, `--dry-run` | Link a skill you are working on, where it already is |
| `adopt` | `-a/--agent`, `--dry-run`, `--json` | Record the skills already in an agent's skills directory |
| `list` | `--json` | Show installed skills, versions and agents |
| `outdated` | `--json` | Report skills whose tracked ref has moved |
| `update [name...]` | `--force`, `--dry-run` | Move skills to the head of the ref they track |
| `pin <name>...` | `--dry-run` | Freeze skills at the revision they are installed at |
| `unpin <name>...` | `--ref`, `--dry-run` | Release the pin, so `update` moves them again |
| `remove <name>` | `-a/--agent`, `--dry-run` | Unlink from every agent, or just the named ones |
| `gc` | `--dry-run`, `--json` | Delete revisions and mirrors no receipt references |
| `version` | | Print version, commit and build date |

`remove` also answers to `uninstall` and `rm`. Removing from some agents keeps
the receipt; removing the last link forgets it. A plugin has no links to keep,
so removing it uninstalls it through `claude` and forgets the receipt outright.

`link <name> -a <agent>` is its inverse, for the agent that was not on the
machine when something was installed: it adds a link to the revision the receipt
already has, without fetching anything. Which of the two forms you meant is
decided by looking the argument up in the receipts, so an installed name takes
the first and everything else takes the path. Naming an agent that already has
the skill links the rest and says so, exiting 2; naming only agents that already
have it does nothing and exits 1. A plugin is refused, because its skills are
the agent's own and there is no symlink to add.

`--skill`, `--all`, `--ref` and `--pin` mean nothing for a plugin — it is
installed whole, at whichever version its marketplace publishes — and are
refused rather than ignored. For the same reason `outdated` reports a plugin as
`n/a`: `claude plugin` offers no way to see a newer version without installing
it, so `skillsctl update` is what finds out.

Nothing in the store is deleted until you ask. `remove` unlinks a skill and
forgets its receipt, and `update` moves it off the revision it was on, but both
leave the copy on disk, because another skill may be installed from the same
commit — which is the normal case for a repository installed with `--all`. `gc`
reclaims what no receipt references: the revision, and the bare mirror once no
revision of that repository is left.

```
$ skillsctl gc --dry-run
rev/github.com/obra/superpowers/9f8e7d6c5b4a39281706f5e4d3c2b1a09f8e7d6c  4.1 MB
cache/github.com/obra/superpowers.git                                     2.7 MB
would reclaim 1 revision and 1 mirror, 6.8 MB
```

Exit codes: `0` everything asked for was done, `1` nothing was, `2` part of it
was and the rest is reported — `install --all` where one name is already taken
installs the others and exits `2`, `outdated` exits `2` when it could not reach
some of the remotes, `update` exits `2` when it updated some skills and skipped
others (and `1` when it updated none of them), `gc` exits `2` when it freed
some of what it found but could not remove the rest, and `adopt` exits `2` when
it adopted some of what it found and skipped the rest (and `1` when it could
adopt none of it). `3` is a finding rather
than a verdict on the work:
`outdated` ran to completion and something has moved.

## Configuration

Agents are configured in `~/.config/skillsctl/config.toml`. Without one,
skillsctl uses built-in defaults for Claude Code, Codex and Gemini, and installs
into whichever of them exist.

```toml
[[target]]
name = "claude"
dir = "~/.claude/skills"
project_dir = ".claude/skills"
plugins = true

[[target]]
name = "codex"
dir = "~/.codex/skills"
```

`dir` is the agent's user-level skills directory, `project_dir` the
repository-relative one, and `plugins` marks an agent that also supports plugin
marketplaces. That last one is what the plugin channel installs for: a
`name@marketplace` source needs an agent with `plugins = true`, and naming one
without it through `-a` is an error rather than a silent no-op.

## Status

All three channels are implemented: `git`, `plugin` (`name@marketplace`) and
`local` (`./path`), and `link` serves both of its forms. `bundle`, `sync` and
`doctor` are designed but not built.

Two things the plugin channel deliberately does not do yet: `outdated` reports a
plugin as `n/a`, and a plugin's skills are not fanned out to agents other than
the one that installed it.

See [the design spec](docs/superpowers/specs/2026-08-13-skillsctl-design.md) for
the full intended surface.

## Development

Tooling is pinned in `mise.toml` and installed with [mise](https://mise.jdx.dev):

```bash
mise install     # go, golangci-lint, goreleaser at the pinned versions
make test
make lint
make tidy-check
make snapshot    # build release artifacts locally, into dist/
```

CI installs the same `mise.toml`, so local and CI tool versions never drift.

## Contributing

Issues and pull requests are welcome. Before opening one, run `make test`,
`make lint` and `make tidy-check`, and write commit messages and PR titles as
[Conventional Commits](https://www.conventionalcommits.org/) — the release
changelog is generated from them.

[AGENTS.md](AGENTS.md) has the full conventions, architecture map and commit
rules, for both human contributors and AI agents.

## License

[AGPL-3.0](LICENSE).
