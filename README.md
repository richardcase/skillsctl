# skillsctl

Homebrew for agent skills: install, update and remove agent skills from git
repositories, with a receipt for every install so update and removal are
deterministic. One store, symlinked into every agent you use.

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
skillsctl install owner/repo -a claude             # just one agent
skillsctl install owner/repo --ref v1.2.0 --pin    # pin a version
skillsctl install owner/repo --dry-run             # show what would change
skillsctl list                                     # what's installed
skillsctl remove avoid-ai-writing                  # unlink everywhere
```

Skills are fetched once into `~/.local/share/skillsctl` and symlinked into each
agent's skills directory, so one copy serves Claude Code, Codex and Gemini.

## Configuration

Agents are configured in `~/.config/skillsctl/config.toml`. Without one,
skillsctl uses built-in defaults for Claude Code, Codex and Gemini, and installs
into whichever of them exist.

```toml
[[target]]
name = "claude"
dir = "~/.claude/skills"
plugins = true

[[target]]
name = "codex"
dir = "~/.codex/skills"
```

## Development

Tooling is pinned in `mise.toml` and installed with [mise](https://mise.jdx.dev):

```bash
mise install     # go, golangci-lint, goreleaser at the pinned versions
make test
make lint
make snapshot    # build release artifacts locally, into dist/
```

CI installs the same `mise.toml`, so local and CI tool versions never drift.

## Design

See [the design spec](docs/superpowers/specs/2026-08-13-skillsctl-design.md).
