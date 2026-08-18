# Sigstore keyless (Fulcio/Rekor) signing — design

## Context

[Issue #64](https://github.com/richardcase/skillsctl/issues/64) is the filed
follow-up from the sign-OCI-image design
(`docs/superpowers/specs/2026-08-18-sign-oci-image-design.md`, landed in PR
#67). That pass deliberately built only offline keypair-based signing via
`cosign` (`--sign-key`/`--verify-key`) and tracked Sigstore's keyless flow
(OIDC identity, Fulcio-issued cert, Rekor transparency log) as a follow-on
once the keypair flow had landed and proven out — which it now has. This
design adds keyless as an alternative signing/verification mode alongside
the existing keypair one, reusing the same `cosign` binary and the same
`internal/cosignx` seam.

No new Go dependency and no new transparency-log handling in skillsctl: the
keyless OIDC flow, the Fulcio cert issuance, and the Rekor upload/query are
all owned by `cosign` itself, exactly the way it already owns the keypair
signature format. `cosign sign --yes <ref>` with no `--key` drives cosign's
own OIDC flow (interactive browser prompt locally, ambient token in CI —
e.g. GitHub Actions' own OIDC provider); `cosign verify --certificate-identity
<id> --certificate-oidc-issuer <issuer> <ref>` verifies against Rekor.

## Decisions from discussion

- **`--sign-keyless`** — a new bool flag on `package`, mutually exclusive
  with the existing `--sign-key <path>` (`cmd.MarkFlagsMutuallyExclusive`,
  the same helper `list.go` already uses for
  `--include-channel`/`--exclude-channel`).
- **`--verify-identity` + `--verify-issuer`** — two new string flags on
  `install`, required together (`cmd.MarkFlagsRequiredTogether`) and
  mutually exclusive with `--verify-key`. Mirrors cosign's own
  `--certificate-identity`/`--certificate-oidc-issuer` split rather than
  inventing a combined syntax — keyless verification is meaningless without
  pinning trust to *both* a signer identity and an OIDC issuer, so requiring
  them together prevents a silently-too-permissive verify.
- **Signed-but-unverified warning is unchanged.** `Signed` (`cosign tree`)
  already reports a signature subtree the same way for keypair or keyless
  signatures, so the existing "signed but was not verified" warning in
  `channel/oci.go` needs no new branch — it already covers a keyless-signed
  image installed with no verify flags.

## Architecture

### `internal/cosignx`

Add two methods to the `Cosign` interface and `CLI`:

```go
// SignKeyless signs ref using Sigstore's keyless flow: cosign drives OIDC
// (browser locally, ambient token in CI), gets a short-lived cert from
// Fulcio, and uploads the signature to Rekor. No key material touches disk.
SignKeyless(ctx context.Context, ref string) error

// VerifyKeyless checks ref's signature was made by a Fulcio-issued cert
// bound to identity, issued by issuer, and is present in Rekor. Both
// identity and issuer must be given — keyless trust is meaningless pinned
// to only one of them.
VerifyKeyless(ctx context.Context, ref, identity, issuer string) error
```

- `SignKeyless` runs `cosign sign --yes <ref>` (no `--key`) — the absence of
  `--key` is what selects cosign's keyless path.
- `VerifyKeyless` runs `cosign verify --certificate-identity <identity>
  --certificate-oidc-issuer <issuer> <ref>`.
- Both reuse the existing `output`/`run` seam and `ErrNotFound` handling —
  no new plumbing needed there.
- `Signed` is untouched.

### `skillsctl package` (`internal/cli/package.go`)

- `packageOpts` gains `signKeyless bool`.
- New flag: `cmd.Flags().BoolVar(&o.signKeyless, "sign-keyless", false, "sign
  the pushed image using Sigstore's keyless (Fulcio/Rekor) flow")`.
- `cmd.MarkFlagsMutuallyExclusive("sign-key", "sign-keyless")`.
- `runPackage`: after a successful `Push`, if `o.signKeyless`, call
  `newCosign().SignKeyless(cmd.Context(), ref)` and print `signed <ref>
  (keyless)`; existing `--sign-key` branch unchanged.
- Dry-run message: extend the existing `if o.signKey != ""` branch to also
  check `o.signKeyless`, printing `would push %s and sign it (keyless)` for
  that case.

### `skillsctl install` (`internal/cli/install.go`)

- `installOpts` gains `verifyIdentity string` and `verifyIssuer string`.
- New flags: `--verify-identity <identity>` ("the signer identity to verify
  a keyless signature against, e.g. a CI workflow's OIDC subject") and
  `--verify-issuer <issuer>` ("the OIDC issuer that signed the identity's
  certificate, e.g. https://token.actions.githubusercontent.com").
- `cmd.MarkFlagsRequiredTogether("verify-identity", "verify-issuer")`.
- `cmd.MarkFlagsMutuallyExclusive("verify-key", "verify-identity")` (and
  transitively issuer, since identity/issuer are already required
  together).
- `runInstall`: extend the existing `--verify-key only applies to oci://
  sources` check to also cover `o.verifyIdentity != ""`.

### `internal/channel`

- `Request` gains `VerifyIdentity`, `VerifyIssuer string` fields, doc-commented
  the same way `VerifyKey` already is ("only the OCI channel acts on it").
- `internal/cli` threads `o.verifyIdentity`/`o.verifyIssuer` into the
  `channel.Request` next to the existing `VerifyKey: o.verifyKey`.
- `channel/oci.go`'s `checkSignature` gains a third branch, checked in this
  order:
  1. `verifyKey != ""` → offline `Verify` (unchanged).
  2. `verifyIdentity != ""` → `c.cosign.VerifyKeyless(ctx, digestRef,
     verifyIdentity, verifyIssuer)`; a failure returns
     `fmt.Errorf("refusing to install: %w", err)`, same shape as the
     keypair branch, before any store/link mutation.
  3. neither → today's `Signed`-based warning, unchanged.

## Testing

- `internal/cosignx/cosignx_test.go`: argv-construction tests for
  `SignKeyless` (`sign --yes <ref>`) and `VerifyKeyless`
  (`verify --certificate-identity <id> --certificate-oidc-issuer <issuer>
  <ref>`), plus a failure-wrapping test for each — same pattern as the
  existing `Sign`/`Verify` tests.
- `internal/channel/oci_test.go`: add a fake-cosign case for keyless-verify
  success and keyless-verify failure, alongside the existing keypair cases.
- `internal/cli/package_test.go`: cover `--sign-keyless` plumbing and the
  `--sign-key`/`--sign-keyless` mutual-exclusion error.
- `internal/cli/install_test.go`: cover `--verify-identity`/`--verify-issuer`
  plumbing, the required-together error when only one is given, and the
  mutual-exclusion error against `--verify-key`.
- `make test-manual` note: manual testing of a real keyless sign/verify
  needs a real OIDC identity (interactive browser locally, or run inside
  GitHub Actions for the ambient-token path) — call this out next to the
  existing keypair manual-test note.

## README updates

- **Features**: extend the existing signing/verification sentence to mention
  keyless as an alternative to the keypair flow.
- **Use** section: add a `--sign-keyless` example under `package` and a
  `--verify-identity`/`--verify-issuer` example under `install`.
- **Commands table**: add `--sign-keyless` to `package`'s flags column and
  `--verify-identity`/`--verify-issuer` to `install`'s.
- **Status** section: no change — flag addition to already-implemented
  commands.

## Non-goals / follow-ups (unchanged from the keypair pass)

Still out of scope here, still tracked separately:

- Key generation/management help for the keypair flow.
- Config-level "always verify" trust policy per source/registry.
- Verifying anything other than the OCI channel.

## Verification

- `make test && make lint && make tidy-check` (no new Go dependency).
- Manual: with `cosign` installed and an OIDC identity available,
  `skillsctl package ./some-skills-dir <local-registry-ref> --sign-keyless`
  completes the browser OIDC flow and signs; `skillsctl install
  oci://<local-registry-ref> --verify-identity <id> --verify-issuer <issuer>`
  succeeds against the right identity/issuer and fails closed against the
  wrong ones; a keyless-signed image installed with neither `--verify-key`
  nor `--verify-identity` prints the existing signed-but-unverified warning.
