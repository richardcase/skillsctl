# skillsctl

Homebrew for agent skills: install, update and remove agent skills from git
repositories, Claude plugins, OCI images with a receipt for every install so update and removal are
deterministic. One store, symlinked into every agent you use.

[![CI](https://github.com/richardcase/skillsctl/actions/workflows/ci.yml/badge.svg)](https://github.com/richardcase/skillsctl/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/richardcase/skillsctl?sort=semver)](https://github.com/richardcase/skillsctl/releases/latest)
[![Downloads](https://img.shields.io/github/downloads/richardcase/skillsctl/total)](https://github.com/richardcase/skillsctl/releases)
[![Homebrew](https://img.shields.io/badge/homebrew-richardcase%2Ftap-orange)](https://github.com/richardcase/homebrew-tap)

[![Go](https://img.shields.io/github/go-mod/go-version/richardcase/skillsctl)](go.mod)
[![Go Reference](https://pkg.go.dev/badge/github.com/richardcase/skillsctl.svg)](https://pkg.go.dev/github.com/richardcase/skillsctl)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)

<p align="center">
  <img src="assets/demo.gif" alt="skillsctl demo: install, list, info, update --dry-run, remove" width="720">
</p>

## Table of Contents

- [Why](#why)
- [Features](#features)
- [Install](#install)
- [Use](#use)
- [How it works](#how-it-works)
- [Commands](#commands)
- [Configuration](#configuration)
- [skills.toml](#skillstoml)
- [Status](#status)
- [Development](#development)
- [Contributing](#contributing)
- [License](#license)

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
- **The whole receipt, when you need it.** `skillsctl info <name>` prints what a
  skill is for, where it came from, which revision is installed and where its
  files are — and checks each symlink against the disk, so one that has been
  deleted, broken or re-pointed is named rather than assumed to work.
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
- **Move your skills to another machine.** `skillsctl bundle > skills.toml`
  writes a small, human-editable manifest of what you have installed;
  `skillsctl sync skills.toml` installs it somewhere else, pins and all. `sync`
  only ever adds — it reports a difference or a skill the manifest does not
  name, and never removes anything.
- **Reach an agent you installed something before you had.**
  `skillsctl link avoid-ai-writing -a gemini` adds a link to the revision that
  skill is already on, without fetching anything or disturbing a pin. It is the
  exact inverse of `remove -a`.
- **Takes over what is already there.** `skillsctl adopt` records the skills
  already sitting in each agent's skills directory, so hand-made symlinks stop
  being invisible. One that leads into a clean git checkout is recorded with the
  sha it is at, pinned; one into a second agent for a skill already managed is
  added to its receipt. Nothing is moved, copied or deleted.
- **Tells you when something has rotted.** `skillsctl doctor` reports links a
  receipt records that are gone, links pointing at nothing, one name resolving
  differently in two agents, skills edited in place, and revisions no receipt
  references. It changes nothing and names the command that repairs each finding,
  and it exits non-zero, so it works as a check in CI.
- **Claude Code plugins too.** `skillsctl install superpowers@claude-plugins-official`
  installs through `claude plugin`, records a receipt, and links every skill the
  plugin ships into the agents that cannot install plugins themselves — so a
  plugin reaches Codex and Gemini like anything else. A plugin Claude already has
  is adopted rather than reinstalled, and `update` re-points those links when
  claude moves the plugin to a new version.
- **Repositories of many skills.** `--skill` takes the ones you name, `--all`
  takes every one it finds, and they share a single copy of the repository. A
  bare `install` on such a repository never guesses: at a terminal it lists what
  it found and lets you tick the ones you want, and anywhere else — a pipe, a CI
  job — it prints the same list and stops, so a script still has to say.
- **Package skills into a container image.** `skillsctl package <source-dir> <oci-ref>`
  bundles a directory of skills into an OCI artifact and pushes it to any
  registry `docker` can reach; `skillsctl install oci://registry/repo:tag`
  installs from one, and `outdated`/`update` follow a moved tag the same way
  they follow a moved git ref. `package --sign-key <path>` signs the pushed
  image with cosign, and `install --verify-key <path>` verifies it before
  installing — or sign and verify keylessly with `--sign-keyless` and
  `--verify-identity`/`--verify-issuer`, using Sigstore's Fulcio/Rekor flow
  instead of a keypair.
- **Disk you can get back.** `skillsctl gc` deletes the revisions and mirrors no
  installed skill references, and reports what it freed. Nothing shared is
  collected while any skill still points at it.
- **Scriptable.** `skillsctl list --json` emits the raw receipts, `info --json`
  emits one of them with everything derived from it, and a partial install exits
  `2` so a script can tell it from having installed nothing.
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
skillsctl install owner/repo                       # pick from a list of its skills
skillsctl install owner/repo --skill web-research  # name one (repeat for more)
skillsctl install owner/repo --all                 # every skill in the repo
skillsctl install owner/repo -a claude             # just one agent
skillsctl install owner/repo --ref v1.2.0 --pin    # pin a version
skillsctl install owner/repo --dry-run             # show what would change
skillsctl install superpowers@claude-plugins-official  # a Claude Code plugin
skillsctl install https://gitlab.com/group/subgroup/repo.git  # any git host, incl. GitLab subgroups
skillsctl install oci://ghcr.io/owner/skills:v1    # from a packaged OCI artifact
skillsctl package ./my-skills ghcr.io/owner/skills:v1  # push a directory of skills as one
skillsctl package ./my-skills ghcr.io/owner/skills:v1 --sign-key cosign.key  # ...and sign it
skillsctl install oci://ghcr.io/owner/skills:v1 --verify-key cosign.pub  # verify before installing
skillsctl package ./my-skills ghcr.io/owner/skills:v1 --sign-keyless  # sign via Sigstore's Fulcio/Rekor flow
skillsctl install oci://ghcr.io/owner/skills:v1 \
  --verify-identity signer@example.com --verify-issuer https://accounts.google.com  # verify a keyless signature
skillsctl link ./my-skill                          # a skill you are writing
skillsctl install ./my-skill                       # the same thing
skillsctl link avoid-ai-writing -a gemini          # into an agent that missed it
skillsctl list                                     # what's installed
skillsctl list --json                              # the raw receipts
skillsctl list --include-channel git               # only skills fetched via git
skillsctl list --exclude-channel local             # everything except skills you are editing
skillsctl info brainstorming                       # one skill's receipt in full
skillsctl info brainstorming --json                # the same, for a script
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
skillsctl bundle > skills.toml                     # write what's installed as a manifest
skillsctl sync skills.toml                         # install what it names, and report the rest
skillsctl sync skills.toml --dry-run               # show what would change
skillsctl version
```

```
$ skillsctl list
NAME              CHANNEL  VERSION           AGENTS
avoid-ai-writing  git      a1b2c3d           claude,codex
brainstorming     git      9f8e7d6 (pinned)  claude
superpowers       plugin   6.3.0             claude,codex
my-skill          local    -                 claude
```

`info` prints everything the receipt records, together with the description from
the skill's `SKILL.md`. Each link is checked against the disk, so a symlink that
has been deleted, broken or re-pointed is named as such — nothing is fetched and
nothing is repaired:

```
$ skillsctl info brainstorming
brainstorming
Explores user intent, requirements and design before implementation.

channel    git
source     https://github.com/obra/superpowers.git
subpath    skills/brainstorming
ref        the repository's default branch
revision   b36e0829c6d0140e93cfef2ca599b1b07d4a7797
files      ~/.local/share/skillsctl/rev/github.com/obra/superpowers/b36e082…/skills/brainstorming
           (skillsctl's store)
installed  2026-08-15 08:50:14 UTC
updated    2026-08-15 08:50:14 UTC

links
  claude   ~/.claude/skills/brainstorming
  codex    ~/.codex/skills/brainstorming  (missing)
```

A name that is not installed is an error naming the closest ones that are:

```
$ skillsctl info brainstorm
error: "brainstorm" is not installed; did you mean brainstorming?
```

A source can be `owner/repo`, `owner/repo/path/to/skill`, any git URL
(https, ssh or scp-style), a local path, or `oci://registry/repository:tag` for
a skill packaged with `skillsctl package`. `//` separates a repository or an
artifact from a subpath inside it — the only way to name one in a
`.git`-suffixed or `git@host:` URL, where the repository boundary is otherwise
the whole path, and in an `oci://` reference, where the tag ends it:
`oci://ghcr.io/owner/skills:v1//pdf-forms`.

The `owner/repo` shorthand is GitHub-specific, but any other git host — GitLab,
Bitbucket, a self-hosted server — works with its full URL, `.git` suffix
included: `skillsctl install https://gitlab.com/group/subgroup/repo.git`. The
suffix matters more on GitLab than GitHub, since GitLab projects can nest
inside subgroups (`group/subgroup/repo`), and without an explicit `.git`
boundary that path is indistinguishable from `owner/repo/path/to/skill`. Add
`//path/to/skill` after the `.git` to name a skill inside such a repository.

A repository holding several skills can be narrowed with `--skill <name>`
(repeatable, matching a skill's name or its path) or `--all`. Without one of
them, `install` asks rather than guessing — at a terminal, that is a list to
pick from:

```
$ skillsctl install vercel-labs/agent-skills
skills in https://github.com/vercel-labs/agent-skills.git @ 7c41bf0:

  ❯ ◉ pdf-forms     Extract and fill PDF forms
    ◯ web-research  Research a topic against primary sources

  ↑/↓ move · space toggle · a all · enter install · q cancel
```

`↑`/`↓` (or `k`/`j`) move, space ticks a skill, `a` ticks every one, enter
installs what is ticked and `q` backs out. Backing out, or confirming with
nothing ticked, exits `1` and changes nothing. With `--as`, which renames a
single skill, the list takes one choice instead of several.

There has to be someone to ask: when stdin or stderr is not a terminal — a
pipe, a CI job, `< /dev/null` — the same list is printed and the command stops,
so an unattended run can never install something nobody chose:

```
$ skillsctl install vercel-labs/agent-skills < /dev/null
skills in https://github.com/vercel-labs/agent-skills.git @ 7c41bf0:
  pdf-forms     Extract and fill PDF forms
  web-research  Research a topic against primary sources
error: this repository holds 2 skills: pass --skill <name> (repeatable) or --all
```

`link <path>` on a directory of several skills asks the same question.

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

A plugin is the second exception, because Claude Code owns it. `skillsctl` records
the `plugin@marketplace` id, the version and the install path claude reported;
there is no revision in the store and no content hash, since the files are the
agent's. What it adds is the fan-out: every skill under the plugin's `skills/`
directory is symlinked into the agents that cannot install plugins for
themselves, and those links are recorded on the receipt like any other. So
`install`, `update` and `remove` run `claude plugin install|update|uninstall`,
read back what claude decided, and then make the links agree with it — which
matters because claude installs each version beside the last, so a link left
alone would go on serving a version that has been replaced. `gc` still leaves a
plugin alone: nothing of it is in the store. `claude` must be on `PATH`; nothing
else needs it.

Locations can be overridden with environment variables:

| Variable | Overrides | Falls back to |
| --- | --- | --- |
| `SKILLSCTL_HOME` | the store | `$XDG_DATA_HOME/skillsctl`, then `~/.local/share/skillsctl` |
| `SKILLSCTL_CONFIG` | the config file | `$XDG_CONFIG_HOME/skillsctl/config.toml`, then `~/.config/skillsctl/config.toml` |

### Signing and verification

`package`/`install` support two independent, mutually exclusive ways to sign
and verify an OCI artifact, both by shelling out to `cosign` — install it
separately and have it on `PATH`, or `skillsctl` reports as much and names
the offline flags instead. Neither is the default; pick whichever fits how
the image was built.

**Keypair**, for images signed by hand or by a pipeline that already manages
its own keys. Generate one with `cosign generate-key-pair`, then:

```bash
skillsctl package ./my-skills ghcr.io/owner/skills:v1 --sign-key cosign.key
skillsctl install oci://ghcr.io/owner/skills:v1 --verify-key cosign.pub
```

Signing runs `cosign sign --key <path> --yes <ref>`; cosign reads the key's
decryption password from `COSIGN_PASSWORD` in the environment, not from a
flag. Verification runs `cosign verify --key <path> <ref>` and never makes a
network call beyond the registry pull itself — it works with no access to
Sigstore's infrastructure. Losing the private key stops you signing new
images under that identity; it does not invalidate images already signed.

**Keyless**, for images signed in CI, where there is no key to hold or
rotate — the signer's identity is the workflow itself, backed by Sigstore's
Fulcio (short-lived certificate issuance) and Rekor (transparency log):

```bash
skillsctl package ./my-skills ghcr.io/owner/skills:v1 --sign-keyless
skillsctl install oci://ghcr.io/owner/skills:v1 \
  --verify-identity signer@example.com --verify-issuer https://accounts.google.com
```

`--sign-keyless` runs `cosign sign --yes <ref>` with no `--key`, so cosign
drives its own OIDC flow: an interactive browser login when run on a
workstation, or the CI platform's ambient OIDC token when run unattended —
GitHub Actions' own `https://token.actions.githubusercontent.com` issuer is
picked up automatically, with nothing extra to configure. The resulting
certificate's identity (an email address, or a CI workflow's OIDC subject
like `https://github.com/owner/repo/.github/workflows/release.yml@refs/heads/main`)
and issuer are what `--verify-identity`/`--verify-issuer` check against —
both are required together, and must match the signer's certificate exactly.
`--verify-identity`/`--verify-issuer` runs
`cosign verify --certificate-identity <identity> --certificate-oidc-issuer <issuer> <ref>`,
which — unlike `--verify-key` — is an online check: it queries Rekor's
transparency log for the signature.

`install` refuses the install outright, before anything is extracted or
linked, if verification is requested and fails. If an image is signed but
`install` was given neither `--verify-key` nor
`--verify-identity`/`--verify-issuer`, it still installs — skipping
verification is opt-in, not silent, so it prints a warning naming both ways
to verify.

## Commands

| Command | Flags | Does |
| --- | --- | --- |
| `install <source>` | `--skill`, `--all`, `-a/--agent`, `--ref`, `--as`, `--pin`, `--dry-run` | Fetch one or more skills and link them into each agent |
| `install <p>@<m>` | `-a/--agent`, `--as`, `--dry-run` | Install a Claude Code plugin through `claude plugin` |
| `install oci://<ref>` | `--skill`, `--all`, `-a/--agent`, `--ref`, `--as`, `--pin`, `--verify-key`, `--verify-identity`, `--verify-issuer`, `--dry-run` | Install one or more skills from an OCI artifact |
| `package <source-dir> <oci-ref>` | `--sign-key`, `--sign-keyless`, `--dry-run` | Package a directory of skills into an OCI artifact and push it |
| `link <name>` | `-a/--agent`, `--dry-run` | Link an installed skill into another agent |
| `link <path>` | `-a/--agent`, `--skill`, `--all`, `--as`, `--dry-run` | Link a skill you are working on, where it already is |
| `adopt` | `-a/--agent`, `--dry-run`, `--json` | Record the skills already in an agent's skills directory |
| `list` | `--json`, `--include-channel`, `--exclude-channel` | Show installed skills, versions and agents |
| `info <name>` | `--json` | Show one skill's receipt in full, and whether its links are live |
| `outdated` | `--json` | Report skills whose tracked ref has moved |
| `update [name...]` | `--force`, `--dry-run` | Move skills to the head of the ref they track |
| `pin <name>...` | `--dry-run` | Freeze skills at the revision they are installed at |
| `unpin <name>...` | `--ref`, `--dry-run` | Release the pin, so `update` moves them again |
| `remove <name>` | `-a/--agent`, `--dry-run` | Unlink from every agent, or just the named ones |
| `doctor` | `--json` | Report where the receipts and the filesystem disagree |
| `gc` | `--dry-run`, `--json` | Delete revisions and mirrors no receipt references |
| `bundle` | | Write the installed skills as a portable `skills.toml` |
| `sync <file>` | `--dry-run` | Install the skills a manifest names, and report the rest |
| `version` | | Print version, commit and build date |

`remove` also answers to `uninstall` and `rm`. Removing from some agents keeps
the receipt; removing the last link forgets it.

Removing a plugin uninstalls it through `claude` and takes away every link its
skills had. Naming only an agent that holds links — `remove superpowers -a codex`
— takes those away and keeps the receipt, since the plugin is still installed.
Naming the agent that owns it is refused if that would strand a linked agent's
skills — one holding links that was not also named in the same command; naming
both together takes both away in one command rather than being refused. The
error names `skillsctl remove <name>`, which does mean everywhere.

`link <name> -a <agent>` is its inverse, for the agent that was not on the
machine when something was installed: it adds a link to the revision the receipt
already has, without fetching anything. Which of the two forms you meant is
decided by looking the argument up in the receipts, so an installed name takes
the first and everything else takes the path. Naming an agent that already has
the skill links the rest and says so, exiting 2; naming only agents that already
have it does nothing and exits 1.

A plugin is linked skill by skill: `link superpowers -a codex` puts every skill
the plugin ships into codex, repairing any of them a link was missing for —
codex holding some but not all of a plugin's skills is not "already has it".
The agent that installed the plugin is reported as already having it, because
it can see those skills without a symlink.

`--skill`, `--all`, `--ref` and `--pin` mean nothing for a plugin — it is
installed whole, at whichever version its marketplace publishes — and are
refused rather than ignored. `outdated` cannot ask the marketplace whether a
newer version exists, so instead it compares the receipt against what claude
has installed now: a plugin claude has moved since skillsctl last looked comes
back `stale`, which `skillsctl update` repairs.

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

`doctor` checks that on-disk reality still matches the receipts: links a receipt
records that are gone, links pointing at nothing or at something other than
what the receipt says, one name resolving differently in two agents, revisions
missing from the store, skills edited in place through their symlink, and
revisions no receipt references. It changes nothing — every finding names the
command that repairs it, and the decision stays yours. Every configured agent is
scanned, with no way to narrow it: a health check that skipped an agent would
report a clean bill of health for a broken one.

It also warns, without failing, when `cosign` is not on `PATH` — signing and
verifying packages both depend on it, and the warning names where to install
it.

```
$ skillsctl doctor
missing links
  tdd  codex  ~/.codex/skills/tdd is recorded but not on disk
  fix: skillsctl remove tdd -a codex, then skillsctl link tdd -a codex

dangling links
  brainstorming  claude  points at ~/.local/share/skillsctl/rev/…/9f8e7d6c, which is gone
  brainstorming  codex   points at ~/.local/share/skillsctl/rev/…/9f8e7d6c, which is gone
  fix: skillsctl remove brainstorming, then skillsctl install obra/superpowers

orphan revisions
  rev/github.com/obra/superpowers/9f8e7d6c5b4a39281706f5e4d3c2b1a09f8e7d6c  4.1 MB
  fix: skillsctl gc
note: 4 problems in 2 skills
```

The repairs are deliberately not `skillsctl update`: update moves a skill to the
head of the ref it tracks and stops at *current* when the ref has not moved,
which is the usual state of a skill whose link somebody deleted. Putting a link
back is `remove -a` followed by `link -a`, and replacing store content is a
reinstall — with a `gc` in between when the revision was edited in place, since
`install` reuses a revision directory that is already there.

Exit codes: `0` everything asked for was done, `1` nothing was, `2` part of it
was and the rest is reported — `install --all` where one name is already taken
installs the others and exits `2`, `outdated` exits `2` when it could not reach
some of the remotes, `update` exits `2` when it updated some skills and skipped
others (and `1` when it updated none of them), `gc` exits `2` when it freed
some of what it found but could not remove the rest, `adopt` exits `2` when
it adopted some of what it found and skipped the rest (and `1` when it could
adopt none of it), and `doctor` exits `2` when an agent's skills directory could
not be read. The codes above `2` are findings rather
than a verdict on the work:
`3` means `outdated` ran to completion and something has moved, and `4` that
`doctor` ran to completion and something is wrong. A stale plugin does not set
`3`: it is not an available update, and `skillsctl update` repairs it on its own.

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
repository-relative one, and `plugins` marks an agent that installs plugins from
a marketplace for itself. It gates installing a plugin, never seeing one: a
`name@marketplace` source needs an agent with `plugins = true` in the set, and
naming only agents without it through `-a` is an error rather than a silent
no-op — but it is precisely the agents *without* it that a plugin's skills are
linked into.

## skills.toml

`skillsctl bundle` writes the skills you have installed as a manifest, and
`skillsctl sync` installs one. It is meant to be read and edited by hand, and
committed.

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
```

- `ref` is the branch or tag a skill tracks, or the frozen sha when `pinned` is
  set — an `install --pin` records no ref, so the sha is the only thing that can
  carry the pin to another machine.
- `agents` is omitted when the skill is in every agent present on the machine,
  which is what an omitted `-a` means to `install`. Name them only for a
  narrower choice. For a plugin this counts the agent that installed it plus
  the ones its skills were fanned out to, so a plugin narrowed with `-a` carries
  its agents like anything else.
- `subpath` locates a skill inside a repository holding several. You can write
  it in the source instead, as `owner/repo//skills/alpha`. `sync` compares an
  entry's subpath against what the receipt it installed actually recorded, so
  a hand-written entry that omits `subpath` for a skill that lives at one
  reports a difference rather than syncing — install once and `bundle` to get
  the subpath right, rather than guessing at it by hand.
- `local` skills — a directory you linked with `skillsctl link ./path` — are
  left out of a bundle and named on stderr, because an absolute path on one
  machine means nothing on another.

`sync` only ever adds:

```
$ skillsctl sync skills.toml
installed alpha @ a1b2c3d into claude, codex, gemini
linked beta into claude
gamma differs: the manifest tracks develop, the install tracks main; remove it and run sync again, or bring the manifest in line
not in the manifest: epsilon (installed from https://github.com/owner/epsilon.git)
note: 2 of 3 entries applied, for the reasons above
```

It installs what is missing and links the agents an entry names. It never
re-points a ref, never moves a pin and never removes a skill, so a second run
changes nothing. A difference exits 2; a skill the manifest does not name is
reported and changes the exit code not at all.

## Status

All four channels are implemented: `git`, `plugin` (`name@marketplace`),
`local` (`./path`) and `oci` (`oci://registry/repo:tag`), and `link` serves
both of its forms. `bundle` and `sync` are also implemented, and `doctor`
reports without a `--fix`.

One thing the plugin channel deliberately does not do yet: `outdated` reports a
plugin as `stale` when claude has moved it since skillsctl last looked, but it
cannot tell you whether the marketplace has published a newer version.

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

[Apache-2.0](LICENSE).
