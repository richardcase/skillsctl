# Sign OCI Images — Design

## Context

[Issue #62](https://github.com/richardcase/skillsctl/issues/62) asks for the
ability to sign an OCI artifact at `package` time, so a later `install` can
verify it first. Today `skillsctl package` pushes a plain, unsigned artifact
(`internal/ocix.Registry.Push`), and `skillsctl install oci://...`
(`internal/channel/oci.go`, `Prepare`) trusts whatever a registry returns with
no verification step at all. This was called out as a non-goal in the
original OCI packaging design
([2026-08-17-oci-skill-packaging-design.md](2026-08-17-oci-skill-packaging-design.md)):
"Image signing/provenance (cosign) — not requested, can follow later." This is
that follow-on.

There is no existing signing infrastructure to build on: nothing in
`docs/superpowers/specs/2026-08-13-skillsctl-design.md`, and no related
dependency in `go.mod`.

## Decisions from discussion

- **Shell out to the `cosign` binary**, not embed a Go library. Embedding
  `sigstore/cosign/v2` was checked and is technically workable for offline
  keypair sign/verify (`pkg/cosign.VerifyImageSignatures` with
  `CheckOpts{IgnoreTlog: true}`; signing via
  `cmd/cosign/cli/sign.SignCmd` with `SignOptions.TlogUpload = false`), but it
  pulls in ~224 transitive modules against this repo's 7 direct dependencies
  today, and cosign's own `VERSIONING.md` states there is no API stability
  guarantee for the Go library surface. A pure-stdlib `crypto/ed25519` scheme
  with a format we invent ourselves was also considered — zero dependencies,
  but not interoperable with any tooling outside skillsctl. Shelling out
  follows the precedent already established twice in this codebase (`gitx`
  wraps `git`, `claudex` wraps `claude`): zero new Go dependencies, no
  library-stability risk, and signatures verify with the standard `cosign
  verify` outside skillsctl too.
- **`cosign` is a runtime dependency, not a build-tool pin.** Unlike
  `mise.toml`'s pins (go, golangci-lint, goreleaser), `cosign` is expected on
  `PATH` the same way `git` and `claude` are — the user installs it
  themselves, and skillsctl reports an actionable error if it's missing and a
  signing/verification flag was used.
- **Keypair-based signing only, offline.** No Sigstore keyless (Fulcio/Rekor
  OIDC) in this pass — tracked as a follow-up (see below).
- **Verification is opt-in to enforce, not opt-in to warn.** Existing
  unsigned installs and existing receipts are unaffected by default. But an
  image that *is* signed always surfaces that fact — a bare `install` on a
  signed image without `--verify-key` prints a warning rather than installing
  silently as if nothing were checkable.

## Architecture

### New package: `internal/cosignx`

Mirrors `internal/claudex`'s shape — a binary wrapped behind a narrow
interface, per AGENTS.md's "a binary we shell out to gets a package and an
interface":

```go
// Package cosignx wraps the cosign binary, the way gitx wraps git and
// claudex wraps claude: cosign owns the signature format and the trust
// verification logic, so shelling out to it is the only contract there is.
package cosignx

// Cosign is the set of cosign operations skillsctl needs.
type Cosign interface {
    // Verify checks ref's signature against a public key file, entirely
    // offline (no Rekor/Fulcio/transparency-log calls).
    Verify(ctx context.Context, ref, pubKeyPath string) error
    // Signed reports whether ref has any signature attached at all, without
    // verifying it against a key. Used to warn when an install skips
    // verification of an image that was actually signed.
    Signed(ctx context.Context, ref string) (bool, error)
    // SignArgv builds the argv for the sign mutation, so package.go's
    // dry-run branch can print the command that would run.
    SignArgv(ref, keyPath string) []string
}
```

- `CLI` struct implements it, `Bin: "cosign"`, `exec.LookPath` gated the same
  way `claudex.run` is, producing an `ErrNotFound` that names the remedy
  (install cosign, or drop `--sign-key`/`--verify-key`).
- `Verify` runs `cosign verify --key <pubKeyPath> <ref>` — a read, like
  `ocix.Resolve`, not a `plan.Op`.
- `Signed` runs `cosign tree <ref>` (read-only, no key needed) and reports
  whether it lists a signature. Best-effort and non-fatal: if `cosign` is not
  on `PATH` at all, `Signed` returns `cosignx.ErrNotFound` and the caller
  treats that as "unknown" rather than failing the install — someone without
  `cosign` installed sees no change from today's behaviour, signed or not.
- Signing is a mutation (it pushes a new artifact to the registry), but
  `package.go` does not use the `plan`/executor system at all today — its
  existing `Push` call is already a direct call gated by an `if o.dryRun`
  early return, not a `plan.Op` (the same exception the OCI packaging design
  already notes for `Push` itself: "an inherent network side effect ... isn't
  expressible as a local `plan.Op`"). The sign step follows that same
  established local shape: `runPackage` calls a direct `Sign(ctx, ref,
  keyPath string) error` method (`cosign sign --key <keyPath> --yes <ref>`)
  after a successful push, gated by the same `if o.dryRun` branch that
  already guards `Push`.
- Password for an encrypted private key is read by `cosign` itself from
  `COSIGN_PASSWORD` (its own established convention) — `cosignx` does nothing
  special with it beyond inheriting `os.Environ()`, the same as
  `claudex.run`.

Add a `newCosign` seam in `internal/cli/context.go` next to `newOCI` /
`newPlugins`, so tests inject a fake and never shell out for real.

### `skillsctl package` changes

`internal/cli/package.go`:

- New flag: `--sign-key <path>` — path to a cosign-format encrypted private
  key PEM (produced by `cosign generate-key-pair`, out of scope for skillsctl
  to generate — see follow-ups).
- After a successful `Push`, if `--sign-key` was given, call
  `newCosign().Sign(cmd.Context(), ref, o.signKey)` and print `signed <ref>`.
- The existing dry-run early return (`would push %s`) extends to `would push
  %s and sign it` when `--sign-key` is set, rather than adding a second
  dry-run branch.

### `skillsctl install` changes

- New flag on `install`: `--verify-key <path>` — path to the cosign public
  key to verify against. Validated in `runInstall` against the parsed
  `source.Source`: erroring immediately (`--verify-key only applies to
  oci:// sources`) if the source's channel is not OCI.
- `channel.Request` gains a `VerifyKey string` field, threaded through from
  the CLI the same way `Pin`/`Ref` already are. Every other channel ignores
  it.
- `channel.NewOCI` gains a `cosignx.Cosign` argument (`channel.NewOCI(st, o,
  newCosign())` from `env.channels()`).
- The `Channel.Prepare` interface (`internal/channel/channel.go:175`) gains a
  warnings return value: `Prepare(ctx context.Context, req Request)
  ([]Candidate, []string, error)`. `Git`, `Plugin` and `Local` return `nil`
  for it — this is the only channel with anything to warn about, so the other
  three just widen their return statements. `runInstall` prints each warning
  with `cmd.Printf` after a successful `Prepare`.
- In `(*OCI).Prepare`, right after `c.oci.Resolve` returns `digest`, build the
  immutable `repo@sha256:<digest>` reference (not the moving tag — verifying
  the tag would leave a TOCTOU window between resolve and verify):
  - If `req.VerifyKey != ""`: call `c.cosign.Verify(ctx, digestRef,
    req.VerifyKey)` before `c.store.EnsureOCI` pulls anything. A verification
    failure returns before any store/link mutation happens, so a bad
    signature never partially installs.
  - Otherwise: call `c.cosign.Signed(ctx, digestRef)`. If it reports `true`,
    `Prepare` still returns the candidates, but with a warning — e.g.
    `warning: <ref> is signed but was not verified (pass --verify-key to
    verify it)` — so a signed image never installs silently unverified. If
    `Signed` errors (cosign not installed, or a transient registry error),
    the install proceeds exactly as it does today: no warning, since whether
    the image is signed is genuinely unknown.

## Testing

- `internal/cosignx`: unit tests inject a fake `output`/`run` func (like
  `claudex_test.go`), asserting argv construction and error wrapping — no
  real `cosign` binary invoked.
- `internal/channel`: `oci_test.go` gets a fake `cosignx.Cosign` (verify
  success, verify failure, signed-but-unverified warning, unsigned, and
  cosign-not-found cases). `git_test.go` / `plugin_test.go` / `local_test.go`
  need a one-line update each for the widened `Prepare` return signature.
- `internal/cli`: `package_test.go` and `install_test.go` set `newCosign` to
  a fake, covering `--sign-key`/`--verify-key` flag plumbing and the
  channel-mismatch validation error.
- `make test-manual` gains a note (not a new target) that manual testing of
  real `cosign sign`/`cosign verify` against a real registry requires cosign
  installed locally — mirroring the existing manual-test caveat for `claude
  plugin install|uninstall`.

## README updates

- **Features**: extend the existing "Package skills into a container image"
  bullet with signing/verification in one sentence.
- **Use** section: add a `--sign-key` example under `package` and a
  `--verify-key` example under `install`.
- **Commands table**: add `--sign-key` to `package`'s flags column and
  `--verify-key` to `install`'s.
- **Status** section: no change needed — this lands as a flag addition to
  already-implemented commands, not a new unbuilt channel.

## Non-goals / follow-ups

Each of these is filed as its own GitHub issue against
`richardcase/skillsctl`, referencing #62, rather than silently dropped:

- Key generation/management help (`cosign generate-key-pair` stays external
  for this pass — a follow-up issue tracks a `skillsctl` convenience wrapper
  if that turns out to be worth it).
- Sigstore keyless (Fulcio/Rekor OIDC) signing, as an alternative to the
  keypair flow this pass builds.
- Config-level "always verify" trust policy per source/registry, so
  verification can be pinned once instead of passed as `--verify-key` on
  every install.
- Verifying anything other than the OCI channel (git/plugin/local channels
  have no equivalent trust gap in scope here, but the question is worth its
  own issue rather than an assumption).

## Open items for the implementation plan

- Confirm exact `cosign tree` output shape to parse for `Signed` (or find a
  more structured/`--json` option if `cosign` offers one, to avoid parsing
  human-readable output).
- Confirm the exact warning/error message wording for `--verify-key`
  mismatches and the signed-but-unverified case.

## Verification

- `make test && make lint && make tidy-check` (no new Go dependency, so
  `tidy-check` should be a no-op).
- Manual: with `cosign` installed locally, `cosign generate-key-pair`, then
  `skillsctl package ./some-skills-dir <local-registry-ref> --sign-key
  cosign.key`, then `skillsctl install oci://<local-registry-ref>
  --verify-key cosign.pub` succeeds; verify a tampered/unsigned artifact
  correctly fails closed; verify a signed image installed without
  `--verify-key` prints the warning.
