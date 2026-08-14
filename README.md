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
- **Pin to an immutable commit.** `--ref v1.2.0 --pin` freezes the resolved sha
  so a later update skips it.
- **Safe by construction.** Path-escaping skill names, subpaths and tar entries
  are rejected; an existing file is never clobbered; nothing but its own
  symlinks is ever deleted; and links created by a failed apply are rolled back.
- **Fast on repeats.** A git mirror cache plus a content-addressed revision
  store means reinstalling a commit you already have does no network work.
- **Scriptable.** `skillsctl list --json` emits the raw receipts.
- **One static binary.** No runtime dependency beyond `git`.

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
skillsctl install owner/repo                       # lists the skills it finds
skillsctl install owner/repo --skill web-research  # pick one (repeat for more)
skillsctl install owner/repo --all                 # every skill in the repo
skillsctl install owner/repo -a claude             # just one agent
skillsctl install owner/repo/path/to/skill         # a skill inside a monorepo
skillsctl install owner/repo --ref v1.2.0 --pin    # pin a version
skillsctl install owner/repo --dry-run             # show what would change
skillsctl list                                     # what's installed
skillsctl list --json                              # the raw receipts
skillsctl remove avoid-ai-writing                  # unlink everywhere
skillsctl version
```

```
$ skillsctl list
NAME              CHANNEL  VERSION           AGENTS
avoid-ai-writing  git      a1b2c3d           claude,codex
brainstorming     git      9f8e7d6 (pinned)  claude
```

A source can be `owner/repo`, `owner/repo/path/to/skill`, any git URL
(https, ssh or scp-style), or a local path.

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

Locations can be overridden with environment variables:

| Variable | Overrides | Falls back to |
| --- | --- | --- |
| `SKILLSCTL_HOME` | the store | `$XDG_DATA_HOME/skillsctl`, then `~/.local/share/skillsctl` |
| `SKILLSCTL_CONFIG` | the config file | `$XDG_CONFIG_HOME/skillsctl/config.toml`, then `~/.config/skillsctl/config.toml` |

## Commands

| Command | Flags | Does |
| --- | --- | --- |
| `install <source>` | `-a/--agent`, `--ref`, `--as`, `--pin`, `--dry-run` | Fetch a skill and link it into each agent |
| `list` | `--json` | Show installed skills, versions and agents |
| `remove <name>` | `-a/--agent`, `--dry-run` | Unlink from every agent, or just the named ones |
| `version` | | Print version, commit and build date |

`remove` also answers to `uninstall` and `rm`. Removing from some agents keeps
the receipt; removing the last link forgets it.

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
marketplaces.

## Status

The `git` channel is implemented and is what the examples above use. The
`plugin` (`name@marketplace`) and `local` path channels are parsed but not yet
installable — they report that the channel is not supported yet. `update`,
`outdated`, `link`, `adopt`, `bundle`, `sync`, `doctor` and `gc` are designed
but not built.

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
