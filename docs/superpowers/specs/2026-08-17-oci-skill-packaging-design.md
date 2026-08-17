# OCI Skill Packaging & Install — Design

## Context

[Issue #51](https://github.com/richardcase/skillsctl/issues/51) asks for two
new capabilities: packaging skills into an OCI image, and installing skills
from one. Tag/sha changes on the image must surface through the existing
`outdated` machinery, and registry auth should reuse Docker's own auth rather
than skillsctl inventing its own. The original design doc's "Decided up
front" list already names "native git and registry handling" alongside the
plugin channel, so an OCI channel is filling in a gap the architecture always
expected, not bolting on something new. This spec adds that channel plus the
new `package` command that produces the artifacts it installs.

## Decisions from discussion

- **Package scope:** one OCI image can hold **multiple skills** — a
  directory of skill subdirectories (or a `skills.toml`-described set),
  matching the existing multi-skill-repo picker UX used for git sources.
- **OCI library:** `github.com/google/go-containerregistry` — the de facto
  Go OCI library (backs `crane`, `ko`), reads Docker's config/credential
  helpers out of the box for auth, and supports both registry v2 and OCI
  artifact push/pull.
- **Source syntax:** explicit `oci://registry/repo:tag` scheme. Avoids
  collision with the existing owner/repo shorthand and `plugin@marketplace`
  parsing in `source.Parse`.
- **`package` does build + push in one step:** `skillsctl package <dir>
  oci://<ref>` builds the image and pushes it, mirroring `docker build &&
  docker push` combined. A local-only `--output <path>` mode can be added
  later if needed; not in scope now.
- **Full directory tree per skill:** packaging captures every file and
  subdirectory alongside each skill's `SKILL.md` (reference docs, scripts,
  assets) — not just the `SKILL.md` file itself.
- **`.gitignore`-aware, `.git`-excluded:** the packaging walk excludes any
  `.git` directory unconditionally, and honors `.gitignore` rules found in
  the source tree so ignored files (build output, secrets, local config)
  never end up in the image layer.

## Architecture

### New package: `internal/ocix`

Wraps `go-containerregistry` behind a narrow, read-mostly interface —
matching the `gitx.Git` and `claudex.Plugins` pattern (AGENTS.md: "a binary
we shell out to gets a package and an interface" — here it's an OCI registry
client rather than a binary, but the seam-for-testability rationale is the
same):

```go
type OCI interface {
    Resolve(ctx context.Context, ref string) (digest string, err error)
    Pull(ctx context.Context, ref, destDir string) error
    Push(ctx context.Context, srcDir, ref string) error
}
```

- `Resolve` does a manifest-only HEAD/GET (no layer pull) — the same
  "without fetching" contract `outdated.Check` relies on for git's
  `ls-remote`.
- `Pull` fetches and extracts the single layer into `destDir`.
- `Push` tars `srcDir` into a layer, builds a minimal OCI image, and pushes.
- Auth is `go-containerregistry`'s default keychain (Docker config +
  credential helpers) — no new auth code.

### `internal/channel`: new `oci` channel

A fourth implementation of `channel.Channel`, `StoreOwned` like git — it
extracts into skillsctl's own content-addressed store, so `gc` counts it and
removal is symmetric with git's. `channel.Registry` gains an `OCI` field and
a case in `For` alongside Git/Plugin/Local.

### `internal/source`: `ChannelOCI`

- `Channel = "oci"`, recognized only via the explicit `oci://` prefix in
  `parseURL`/`parseChannel`.
- New per-channel fields on `Source`: `Registry`, `Repository`, `Tag` (kept
  separate rather than reusing git's `RepoURL`, matching how Plugin/Local
  already get their own fields rather than overloading git's).
- `Slug()` and `DefaultName()` get an OCI case (e.g. slug derived from
  `registry/repository`, name defaulting to the chosen skill's directory
  name once extracted).

### Image format

- One layer, a tarball of the packaged directory tree (post gitignore/`.git`
  filtering), one subdirectory per skill.
- Media type: a custom OCI artifact type (e.g.
  `application/vnd.skillsctl.skills.layer.v1.tar+gzip`) rather than a
  Docker image layer, since this isn't a runnable container — reduces
  ambiguity in registries that distinguish artifact types.
- No manifest-level skill list is required beyond what's on disk — install
  extracts the layer, then walks it via the existing `discover` package
  (`SKILL.md` + frontmatter) as it does for git today, keeping the OCI path
  from needing an index format that could drift from the actual content.

### Packaging walk (`skillsctl package`)

1. Walk the given source directory.
2. Skip any `.git` directory unconditionally, at any depth.
3. Load `.gitignore` file(s) in the tree (root and nested, standard
   precedence) and exclude matching paths.
4. Reuse the same path-safety checks (`store.within`-equivalent) already
   applied to git/tar extraction — belt-and-braces even though this is the
   *build* side, not extraction, since a future contributor could otherwise
   assume packaging is "trusted" when the destination registry isn't.
5. Tar the remaining tree, build the OCI artifact, `ocix.Push`.

**New dependency to raise:** a small, focused `.gitignore` matcher (e.g.
`github.com/sabhiram/go-gitignore` — single-purpose, no transitive deps) in
preference to pulling in `go-git`'s `gitignore` subpackage, which drags in
`go-billy` filesystem abstractions skillsctl doesn't otherwise need.

### Install flow

Mirrors `store.Ensure`'s git shape:

1. `ocix.Resolve(tag)` → digest.
2. `ocix.Pull` into a temp dir, extract into `rev/<slug>/<digest>` via the
   same atomic-rename-after-extract pattern `store.Ensure` uses for git.
3. If the image contains multiple skills, reuse the existing multi-skill
   picker (`prompt` package) so the user selects which to install, same as
   the multi-skill-repo flow for git sources.
4. Receipt: `Channel = "oci"`, `Ref` = tag, `Resolved` = digest, `Source` =
   the `oci://` string — no new receipt fields needed, reusing `Ref`/
   `Resolved` the way the explore step confirmed git already does.

### Outdated / update

- `outdated.Check` gains an OCI branch: `ocix.Resolve(tag)` → compare digest
  to `r.Resolved`, cached per unique `(source, tag)` pair like git's `seen`
  map — same "no fetch" cost profile.
- `update` re-pulls when the digest has changed, structurally identical to
  git tracking a moving ref.

### CLI

- New `internal/cli/package.go`: `skillsctl package <source-dir> <oci-ref>`.
- Push is an inherent network side effect and isn't expressible as a local
  `plan.Op` — same exception git's `Mirror`/`Extract` already are. `--dry-run`
  should still report what would be packaged (skill list, excluded paths)
  without pushing.
- `install`/`update`/`outdated`/`list`/`remove`/`gc` need no new flags —
  they already dispatch generically via `channel.Registry` and `Ownership()`.

## Testing

- `internal/ocix`: interface behind a fake in tests, per existing
  `gitx`/`claudex` convention — no test touches a real registry.
- Packaging walk: table-driven tests for `.gitignore` exclusion (root and
  nested), unconditional `.git` exclusion, and path-safety rejection, using
  `t.TempDir()` fixtures (same style as `testrepo`).
- Consider a `testregistry`-style in-process registry fixture (there are
  standard lightweight fakes for `go-containerregistry`, e.g. its own
  `pkg/registry` test server) so `package`→`install` round-trips can be
  tested without network, mirroring how `internal/testrepo` gives git tests
  a `file://` fixture.

## Non-goals

- Image signing/provenance (cosign) — not requested, can follow later.
- Local-only package output without push — deferred per the "package does
  both" decision above.
- A manifest/index format inside the image beyond on-disk `SKILL.md`
  discovery.

## Open items for the implementation plan

- Confirm final custom media type string and whether it should be
  versioned from the start (`v1`).
- Decide exact slug/name derivation rules for OCI sources in
  `source.Slug()`/`DefaultName()`.
- Confirm `sabhiram/go-gitignore` (or an alternative) after checking its
  license and maintenance status meets the bar the other four direct
  dependencies were held to.
