# Sigstore Keyless (Fulcio/Rekor) Signing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Sigstore keyless (Fulcio/Rekor) signing and verification of OCI
skill artifacts as an alternative to the existing offline keypair flow
(`--sign-key`/`--verify-key`).

**Architecture:** `internal/cosignx.Cosign` gains `SignKeyless` and
`VerifyKeyless`, both shelling out to `cosign` exactly like the existing
`Sign`/`Verify` (no new dependency, no new transparency-log handling —
`cosign` owns the OIDC/Fulcio/Rekor flow entirely). `package` gains a
`--sign-keyless` bool flag mutually exclusive with `--sign-key`. `install`
gains `--verify-identity`/`--verify-issuer` string flags, required together,
mutually exclusive with `--verify-key`. `internal/channel.OCI.checkSignature`
gains a third branch for the keyless-verify case; its existing
signed-but-unverified warning already covers a keyless-signed image
installed with no verify flags, since `cosign tree` reports a signature
subtree the same way for either signing mode.

**Tech Stack:** Go 1.25, Cobra (`spf13/cobra` v1.10.2 — `MarkFlagsRequiredTogether`
and `MarkFlagsMutuallyExclusive` are already used elsewhere in this repo),
standard library `testing`.

**Spec:** `docs/superpowers/specs/2026-08-18-sigstore-keyless-signing-design.md`

## Global Constraints

- No new Go dependency — this is a `cosign` argv extension, not a library
  integration. `make tidy-check` must be a no-op.
- Tests use the standard library only: table-driven where the existing files
  already are, `t.Run` subtests, no testify/mocks/golden files (`AGENTS.md`).
- **Never call `t.Parallel()`** in this repo's tests.
- Errors: `fmt.Errorf` with `%w` and a lowercase, verb-first prefix naming
  the operation and the ref/path.
- Commit messages: Conventional Commits, lowercase imperative subject, no
  trailing period, no attribution footers (`AGENTS.md`).
- `make test && make lint && make tidy-check` must pass before any commit
  that claims a task done.

---

## Task 1: `internal/cosignx` — `SignKeyless` and `VerifyKeyless`

**Files:**
- Modify: `internal/cosignx/cosignx.go`
- Test: `internal/cosignx/cosignx_test.go`

**Interfaces:**
- Produces: `Cosign.SignKeyless(ctx context.Context, ref string) error` and
  `Cosign.VerifyKeyless(ctx context.Context, ref, identity, issuer string) error`,
  both on the `Cosign` interface and the `*CLI` type that implements it.
  Every other implementer of `Cosign` in the repo (`internal/channel/oci_test.go`'s
  `fakeCosign`, `internal/cli/cli_test.go`'s `refusingCosign`,
  `internal/cli/package_test.go`'s `recordingCosign`,
  `internal/cli/install_verify_test.go`'s `verifyingCosign`) must add both
  methods before the repo compiles again — that is Task 2.

- [ ] **Step 1: Write the failing tests**

Append to `internal/cosignx/cosignx_test.go`:

```go
func TestSignKeylessBuildsTheExpectedArgv(t *testing.T) {
	var gotArgs []string
	c := &CLI{Bin: "cosign"}
	c.output = func(_ context.Context, args ...string) (string, error) {
		gotArgs = args
		return "", nil
	}
	if err := c.SignKeyless(context.Background(), "ghcr.io/owner/skills:v1"); err != nil {
		t.Fatalf("SignKeyless: %v", err)
	}
	want := "sign --yes ghcr.io/owner/skills:v1"
	if got := strings.Join(gotArgs, " "); got != want {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

func TestSignKeylessWrapsAFailure(t *testing.T) {
	err := fake("", errors.New("no oidc token available")).SignKeyless(context.Background(), "ghcr.io/owner/skills:v1")
	if err == nil {
		t.Fatal("SignKeyless accepted a failing cosign call")
	}
	if !strings.Contains(err.Error(), "sign ghcr.io/owner/skills:v1") {
		t.Errorf("error = %v, want it to name the ref", err)
	}
}

func TestVerifyKeylessBuildsTheExpectedArgv(t *testing.T) {
	var gotArgs []string
	c := &CLI{Bin: "cosign"}
	c.output = func(_ context.Context, args ...string) (string, error) {
		gotArgs = args
		return "Verification for ghcr.io/owner/skills@sha256:aaa --\n", nil
	}
	err := c.VerifyKeyless(context.Background(), "ghcr.io/owner/skills@sha256:aaa",
		"https://github.com/owner/repo/.github/workflows/release.yml@refs/heads/main",
		"https://token.actions.githubusercontent.com")
	if err != nil {
		t.Fatalf("VerifyKeyless: %v", err)
	}
	want := "verify --certificate-identity " +
		"https://github.com/owner/repo/.github/workflows/release.yml@refs/heads/main " +
		"--certificate-oidc-issuer https://token.actions.githubusercontent.com " +
		"ghcr.io/owner/skills@sha256:aaa"
	if got := strings.Join(gotArgs, " "); got != want {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

func TestVerifyKeylessWrapsAFailure(t *testing.T) {
	err := fake("", errors.New("no matching signatures")).VerifyKeyless(context.Background(),
		"ghcr.io/owner/skills@sha256:aaa", "signer@example.com", "https://accounts.google.com")
	if err == nil {
		t.Fatal("VerifyKeyless accepted a failing cosign call")
	}
	if !strings.Contains(err.Error(), "verify ghcr.io/owner/skills@sha256:aaa") {
		t.Errorf("error = %v, want it to name the ref", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/cosignx/... -run 'Keyless' -v`
Expected: FAIL — `c.SignKeyless` / `c.VerifyKeyless` undefined (method does
not exist on `*CLI`).

- [ ] **Step 3: Implement `SignKeyless` and `VerifyKeyless`**

In `internal/cosignx/cosignx.go`, add both methods to the `Cosign` interface,
right after `Sign`:

```go
	// SignKeyless signs ref using Sigstore's keyless flow: cosign drives
	// OIDC (browser locally, ambient token in CI), gets a short-lived cert
	// from Fulcio, and uploads the signature to Rekor. No key material
	// touches disk on either side of this call.
	SignKeyless(ctx context.Context, ref string) error
	// VerifyKeyless checks ref's signature was made by a Fulcio-issued cert
	// bound to identity, issued by issuer, and is present in Rekor. Both
	// identity and issuer must be given — keyless trust pinned to only one
	// of them is not trust at all.
	VerifyKeyless(ctx context.Context, ref, identity, issuer string) error
```

Then add the implementations on `*CLI`, right after `Sign`:

```go
// SignKeyless signs ref using Sigstore's keyless flow.
func (c *CLI) SignKeyless(ctx context.Context, ref string) error {
	if _, err := c.output(ctx, "sign", "--yes", ref); err != nil {
		return fmt.Errorf("sign %s: %w", ref, err)
	}
	return nil
}

// VerifyKeyless checks ref's signature against a Fulcio-issued cert bound
// to identity and issued by issuer.
func (c *CLI) VerifyKeyless(ctx context.Context, ref, identity, issuer string) error {
	if _, err := c.output(ctx, "verify",
		"--certificate-identity", identity,
		"--certificate-oidc-issuer", issuer,
		ref); err != nil {
		return fmt.Errorf("verify %s: %w", ref, err)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/cosignx/... -v`
Expected: PASS (all tests in the package, including the four new ones and
the pre-existing `Sign`/`Verify`/`Signed` ones).

- [ ] **Step 5: Commit**

```bash
git add internal/cosignx/cosignx.go internal/cosignx/cosignx_test.go
git commit -m "feat(cosignx): add keyless sign and verify"
```

---

## Task 2: Keep the widened `Cosign` interface compiling everywhere

Task 1 added two methods to the `Cosign` interface. Every test-only fake that
implements it now fails to compile until it grows those two methods. This
task is pure scaffolding — it adds the minimal methods needed to build and
pass today's tests unchanged; Tasks 3-5 give those methods real assertions.

**Files:**
- Modify: `internal/channel/oci_test.go` (`fakeCosign`)
- Modify: `internal/cli/cli_test.go` (`refusingCosign`)
- Modify: `internal/cli/package_test.go` (`recordingCosign`)
- Modify: `internal/cli/install_verify_test.go` (`verifyingCosign`)

**Interfaces:**
- Consumes: `cosignx.Cosign` from Task 1 (`SignKeyless`, `VerifyKeyless`).
- Produces: nothing new for later tasks to consume directly — Tasks 3-5 add
  fields/behavior to these same fakes.

- [ ] **Step 1: Confirm the compile failure**

Run: `go build ./... 2>&1 | grep -i cosign`
Expected: four `does not implement cosignx.Cosign` errors, one per fake
listed above (missing `SignKeyless` and/or `VerifyKeyless`).

- [ ] **Step 2: Extend `fakeCosign` in `internal/channel/oci_test.go`**

Add fields and methods right after the existing `Sign`:

```go
type fakeCosign struct {
	verifyErr        error
	signed           bool
	signedErr        error
	verifyKeylessErr error
	verified         []string
	verifiedKeyless  []string
	asked            []string
}
```

```go
func (f *fakeCosign) SignKeyless(context.Context, string) error { return nil }

func (f *fakeCosign) VerifyKeyless(_ context.Context, ref, _, _ string) error {
	f.verifiedKeyless = append(f.verifiedKeyless, ref)
	return f.verifyKeylessErr
}
```

- [ ] **Step 3: Extend `refusingCosign` in `internal/cli/cli_test.go`**

```go
func (refusingCosign) SignKeyless(context.Context, string) error {
	return errors.New("this test has not configured cosign (set h.cosign)")
}

func (refusingCosign) VerifyKeyless(context.Context, string, string, string) error {
	return errors.New("this test has not configured cosign (set h.cosign)")
}
```

- [ ] **Step 4: Extend `recordingCosign` in `internal/cli/package_test.go`**

```go
type recordingCosign struct {
	signRef, signKey       string
	signErr                error
	signKeylessRef         string
	signKeylessErr         error
}
```

```go
func (c *recordingCosign) SignKeyless(_ context.Context, ref string) error {
	c.signKeylessRef = ref
	return c.signKeylessErr
}

func (c *recordingCosign) VerifyKeyless(context.Context, string, string, string) error { return nil }
```

- [ ] **Step 5: Extend `verifyingCosign` in `internal/cli/install_verify_test.go`**

```go
type verifyingCosign struct {
	verifyErr        error
	signed           bool
	verified         []string
	verifyKeylessErr error
	verifiedKeyless  []string
}
```

```go
func (c *verifyingCosign) SignKeyless(context.Context, string) error { return nil }

func (c *verifyingCosign) VerifyKeyless(_ context.Context, ref, _, _ string) error {
	c.verifiedKeyless = append(c.verifiedKeyless, ref)
	return c.verifyKeylessErr
}
```

- [ ] **Step 6: Run the full test suite to confirm nothing broke**

Run: `make test`
Expected: PASS, same test count as before Task 1 plus the four new
`cosignx` tests — no behavior changed yet, only compilation restored.

- [ ] **Step 7: Commit**

```bash
git add internal/channel/oci_test.go internal/cli/cli_test.go internal/cli/package_test.go internal/cli/install_verify_test.go
git commit -m "test: implement the widened Cosign interface in every fake"
```

---

## Task 3: `internal/channel` — keyless verification in `OCI.checkSignature`

**Files:**
- Modify: `internal/channel/channel.go:63-73` (`Request` struct)
- Modify: `internal/channel/oci.go` (`checkSignature`)
- Test: `internal/channel/oci_test.go`

**Interfaces:**
- Consumes: `cosignx.Cosign.VerifyKeyless` from Task 1;
  `fakeCosign.VerifyKeyless`/`verifiedKeyless`/`verifyKeylessErr` from Task 2.
- Produces: `Request.VerifyIdentity`, `Request.VerifyIssuer string` fields
  that Task 5 (`install.go`) populates from CLI flags.

- [ ] **Step 1: Write the failing tests**

Append to `internal/channel/oci_test.go`, right after
`TestOCIPrepareFailsClosedOnABadSignature`:

```go
func TestOCIPrepareVerifiesKeylessAgainstTheResolvedDigest(t *testing.T) {
	st := store.New(t.TempDir())
	o := &fakeOCI{digest: "sha256:aaa"}
	cs := &fakeCosign{}
	c := NewOCI(st, o, cs)

	src, _ := source.Parse("oci://ghcr.io/owner/skills:v1")
	_, warnings, err := c.Prepare(context.Background(), Request{
		Source: src, All: true,
		VerifyIdentity: "signer@example.com",
		VerifyIssuer:   "https://accounts.google.com",
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none for a successful keyless verify", warnings)
	}
	if len(cs.verifiedKeyless) != 1 || cs.verifiedKeyless[0] != "ghcr.io/owner/skills@sha256:aaa" {
		t.Errorf("verifiedKeyless = %v, want one call against the digest ref", cs.verifiedKeyless)
	}
}

func TestOCIPrepareFailsClosedOnABadKeylessSignature(t *testing.T) {
	st := store.New(t.TempDir())
	o := &fakeOCI{digest: "sha256:aaa"}
	cs := &fakeCosign{verifyKeylessErr: errors.New("no matching signatures")}
	c := NewOCI(st, o, cs)

	src, _ := source.Parse("oci://ghcr.io/owner/skills:v1")
	_, _, err := c.Prepare(context.Background(), Request{
		Source: src, All: true,
		VerifyIdentity: "signer@example.com",
		VerifyIssuer:   "https://accounts.google.com",
	})
	if err == nil {
		t.Fatal("Prepare accepted a failing keyless verification")
	}
	if _, statErr := os.Stat(filepath.Join(st.Root, "rev")); statErr == nil {
		t.Error("a failed keyless verification must not extract the revision")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/channel/... -run 'Keyless' -v`
Expected: FAIL — `Request` has no field `VerifyIdentity`/`VerifyIssuer` (a
compile error), or once that's stubbed in, both tests fail because
`checkSignature` never calls `VerifyKeyless`.

- [ ] **Step 3: Add the fields to `Request`**

In `internal/channel/channel.go`, right after the existing `VerifyKey`
field:

```go
	// VerifyKey is a cosign public key path. Only the OCI channel acts on it;
	// every other channel ignores it.
	VerifyKey string
	// VerifyIdentity and VerifyIssuer pin trust for a Sigstore keyless
	// verification: the signer identity a Fulcio-issued cert must be bound
	// to, and the OIDC issuer that must have issued it. Only the OCI
	// channel acts on either; every other channel ignores them. Set
	// together or not at all — the CLI layer enforces that.
	VerifyIdentity string
	VerifyIssuer   string
```

- [ ] **Step 4: Add the keyless branch to `checkSignature`**

In `internal/channel/oci.go`, replace the body of `checkSignature`:

```go
func (c *OCI) checkSignature(ctx context.Context, src source.Source, digest, verifyKey, verifyIdentity, verifyIssuer string) ([]string, error) {
	digestRef := fmt.Sprintf("%s/%s@%s", src.Registry, src.Repository, digest)

	if verifyKey != "" {
		if err := c.cosign.Verify(ctx, digestRef, verifyKey); err != nil {
			return nil, fmt.Errorf("refusing to install: %w", err)
		}
		return nil, nil
	}

	if verifyIdentity != "" {
		if err := c.cosign.VerifyKeyless(ctx, digestRef, verifyIdentity, verifyIssuer); err != nil {
			return nil, fmt.Errorf("refusing to install: %w", err)
		}
		return nil, nil
	}

	signed, err := c.cosign.Signed(ctx, digestRef)
	if err != nil {
		return nil, nil
	}
	if !signed {
		return nil, nil
	}
	return []string{fmt.Sprintf("warning: %s is signed but was not verified (pass --verify-key to verify it)", digestRef)}, nil
}
```

Update its call site in `Prepare` (the only caller) to pass the two new
arguments:

```go
	warnings, err := c.checkSignature(ctx, src, digest, req.VerifyKey, req.VerifyIdentity, req.VerifyIssuer)
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/channel/... -v`
Expected: PASS (all `internal/channel` tests, including the two new ones).

- [ ] **Step 6: Commit**

```bash
git add internal/channel/channel.go internal/channel/oci.go internal/channel/oci_test.go
git commit -m "feat(channel): verify keyless signatures on oci:// installs"
```

---

## Task 4: `skillsctl package --sign-keyless`

**Files:**
- Modify: `internal/cli/package.go`
- Test: `internal/cli/package_test.go`

**Interfaces:**
- Consumes: `cosignx.Cosign.SignKeyless`; `recordingCosign.signKeylessRef`
  from Task 2.
- Produces: `packageOpts.signKeyless bool`, consumed only within this file.

- [ ] **Step 1: Write the failing tests**

Append to `internal/cli/package_test.go`:

```go
func TestPackageSignsKeylesslyAfterPushingWhenSignKeylessIsGiven(t *testing.T) {
	h := newHarness(t)
	rec := &recordingOCI{}
	cs := &recordingCosign{}
	h.oci = rec
	h.cosign = cs

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "alpha", "SKILL.md"), []byte("---\nname: alpha\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := h.run(t, "package", dir, "ghcr.io/owner/skills:v1", "--sign-keyless")
	if err != nil {
		t.Fatalf("package: %v\n%s", err, out)
	}
	if cs.signKeylessRef != "ghcr.io/owner/skills:v1" {
		t.Errorf("signed keyless ref = %q, want ghcr.io/owner/skills:v1", cs.signKeylessRef)
	}
	if cs.signRef != "" {
		t.Errorf("keypair Sign was also called: signRef = %q", cs.signRef)
	}
	if !strings.Contains(out, "signed ghcr.io/owner/skills:v1") {
		t.Errorf("output %q should confirm the signature", out)
	}
}

func TestPackageDryRunMentionsKeylessSigning(t *testing.T) {
	h := newHarness(t)
	rec := &recordingOCI{}
	cs := &recordingCosign{}
	h.oci = rec
	h.cosign = cs

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "alpha", "SKILL.md"), []byte("---\nname: alpha\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := h.run(t, "package", dir, "ghcr.io/owner/skills:v1", "--sign-keyless", "--dry-run")
	if err != nil {
		t.Fatalf("package --dry-run: %v\n%s", err, out)
	}
	if cs.signKeylessRef != "" {
		t.Error("--dry-run must not sign")
	}
	if !strings.Contains(out, "and sign it") {
		t.Errorf("dry-run output %q should mention signing", out)
	}
}

func TestPackageRejectsSignKeyAndSignKeylessTogether(t *testing.T) {
	h := newHarness(t)
	h.oci = &recordingOCI{}
	h.cosign = &recordingCosign{}

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "alpha", "SKILL.md"), []byte("---\nname: alpha\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := h.run(t, "package", dir, "ghcr.io/owner/skills:v1", "--sign-key", "cosign.key", "--sign-keyless")
	if err == nil {
		t.Fatalf("package accepted --sign-key and --sign-keyless together:\n%s", out)
	}
	if !strings.Contains(err.Error(), "sign-key") || !strings.Contains(err.Error(), "sign-keyless") {
		t.Errorf("error = %v, want it to name both flags", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/cli/... -run 'SignKeyless|RejectsSignKey' -v`
Expected: FAIL — `--sign-keyless` is an unknown flag.

- [ ] **Step 3: Add the flag and the sign/dry-run branches**

In `internal/cli/package.go`, add the field to `packageOpts`:

```go
type packageOpts struct {
	dryRun      bool
	signKey     string
	signKeyless bool
}
```

Register the flag and the mutual-exclusion in `newPackageCmd`, right after
the existing `--sign-key` registration:

```go
	cmd.Flags().StringVar(&o.signKey, "sign-key", "", "cosign private key to sign the pushed image with")
	cmd.Flags().BoolVar(&o.signKeyless, "sign-keyless", false, "sign the pushed image using Sigstore's keyless (Fulcio/Rekor) flow")
	cmd.MarkFlagsMutuallyExclusive("sign-key", "sign-keyless")
	return cmd
```

In `runPackage`, extend the dry-run branch:

```go
	if o.dryRun {
		if o.signKey != "" || o.signKeyless {
			cmd.Printf("would push %s and sign it\n", ref)
			return nil
		}
		cmd.Printf("would push %s\n", ref)
		return nil
	}
```

And extend the post-push signing branch:

```go
	if o.signKey != "" {
		if err := newCosign().Sign(cmd.Context(), ref, o.signKey); err != nil {
			return err
		}
		cmd.Printf("signed %s\n", ref)
	}
	if o.signKeyless {
		if err := newCosign().SignKeyless(cmd.Context(), ref); err != nil {
			return err
		}
		cmd.Printf("signed %s\n", ref)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/cli/... -v`
Expected: PASS (all `internal/cli` tests, including the three new ones).

- [ ] **Step 5: Commit**

```bash
git add internal/cli/package.go internal/cli/package_test.go
git commit -m "feat(package): support keyless signing with --sign-keyless"
```

---

## Task 5: `skillsctl install --verify-identity`/`--verify-issuer`

**Files:**
- Modify: `internal/cli/install.go`
- Test: `internal/cli/install_verify_test.go`

**Interfaces:**
- Consumes: `channel.Request.VerifyIdentity`/`VerifyIssuer` from Task 3;
  `verifyingCosign.VerifyKeyless`/`verifiedKeyless`/`verifyKeylessErr` from
  Task 2.
- Produces: `installOpts.verifyIdentity`, `verifyIssuer string`, consumed
  only within this file.

- [ ] **Step 1: Write the failing tests**

Append to `internal/cli/install_verify_test.go`:

```go
func TestInstallVerifiesKeylessBeforeInstalling(t *testing.T) {
	h := newHarness(t)
	h.oci = &fakeOCIWithLayer{digest: "sha256:aaa"}
	h.cosign = &verifyingCosign{}

	out, err := h.run(t, "install", "oci://ghcr.io/owner/skills:v1", "--all",
		"--verify-identity", "signer@example.com", "--verify-issuer", "https://accounts.google.com")
	if err != nil {
		t.Fatalf("install --verify-identity/--verify-issuer: %v\n%s", err, out)
	}
	if strings.Contains(out, "warning:") {
		t.Errorf("a verified install should print no warning:\n%s", out)
	}
}

func TestInstallFailsClosedOnABadKeylessSignature(t *testing.T) {
	h := newHarness(t)
	h.oci = &fakeOCIWithLayer{digest: "sha256:aaa"}
	h.cosign = &verifyingCosign{verifyKeylessErr: errors.New("no matching signatures")}

	out, err := h.run(t, "install", "oci://ghcr.io/owner/skills:v1", "--all",
		"--verify-identity", "signer@example.com", "--verify-issuer", "https://accounts.google.com")
	if err == nil {
		t.Fatalf("install accepted a failing keyless verification:\n%s", out)
	}
}

func TestInstallRequiresIdentityAndIssuerTogether(t *testing.T) {
	h := newHarness(t)
	h.oci = &fakeOCIWithLayer{digest: "sha256:aaa"}
	h.cosign = &verifyingCosign{}

	out, err := h.run(t, "install", "oci://ghcr.io/owner/skills:v1", "--all",
		"--verify-identity", "signer@example.com")
	if err == nil {
		t.Fatalf("install accepted --verify-identity without --verify-issuer:\n%s", out)
	}
	if !strings.Contains(err.Error(), "verify-issuer") {
		t.Errorf("error = %v, want it to name --verify-issuer", err)
	}
}

func TestInstallRejectsVerifyKeyAndVerifyIdentityTogether(t *testing.T) {
	h := newHarness(t)
	h.oci = &fakeOCIWithLayer{digest: "sha256:aaa"}
	h.cosign = &verifyingCosign{}

	out, err := h.run(t, "install", "oci://ghcr.io/owner/skills:v1", "--all",
		"--verify-key", "cosign.pub",
		"--verify-identity", "signer@example.com", "--verify-issuer", "https://accounts.google.com")
	if err == nil {
		t.Fatalf("install accepted --verify-key and --verify-identity together:\n%s", out)
	}
}

func TestInstallRejectsVerifyIdentityOnANonOCISource(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	out, err := h.run(t, "install", url, "--verify-identity", "signer@example.com", "--verify-issuer", "https://accounts.google.com")
	if err == nil {
		t.Fatalf("install accepted --verify-identity on a git source:\n%s", out)
	}
	if !strings.Contains(err.Error(), "oci://") {
		t.Errorf("error = %v, want it to name oci:// sources", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/cli/... -run 'Keyless|IdentityAndIssuer|VerifyKeyAndVerifyIdentity|VerifyIdentityOnANonOCI' -v`
Expected: FAIL — `--verify-identity`/`--verify-issuer` are unknown flags.

- [ ] **Step 3: Add the flags, validation, and request plumbing**

In `internal/cli/install.go`, add the fields to `installOpts`:

```go
type installOpts struct {
	agents         []string
	skills         []string
	all            bool
	ref            string
	as             string
	pin            bool
	verifyKey      string
	verifyIdentity string
	verifyIssuer   string
	dryRun         bool
}
```

Register the flags in `newInstallCmd`, right after the existing
`--verify-key` registration:

```go
	cmd.Flags().StringVar(&o.verifyKey, "verify-key", "", "cosign public key to verify an oci:// image's signature against before installing")
	cmd.Flags().StringVar(&o.verifyIdentity, "verify-identity", "", "signer identity to verify a Sigstore keyless signature against (e.g. a CI workflow's OIDC subject)")
	cmd.Flags().StringVar(&o.verifyIssuer, "verify-issuer", "", "OIDC issuer that must have signed --verify-identity's certificate (e.g. https://token.actions.githubusercontent.com)")
	cmd.MarkFlagsRequiredTogether("verify-identity", "verify-issuer")
	cmd.MarkFlagsMutuallyExclusive("verify-key", "verify-identity")
```

Extend the OCI-only validation in `runInstall`:

```go
	if (o.verifyKey != "" || o.verifyIdentity != "") && src.Channel != source.ChannelOCI {
		return fmt.Errorf("--verify-key/--verify-identity only apply to oci:// sources")
	}
```

Thread the two new fields into the request:

```go
	req := channel.Request{
		Source:         src,
		Targets:        targets,
		Skills:         o.skills,
		All:            o.all,
		Ref:            o.ref,
		Pin:            o.pin,
		VerifyKey:      o.verifyKey,
		VerifyIdentity: o.verifyIdentity,
		VerifyIssuer:   o.verifyIssuer,
	}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/cli/... -v`
Expected: PASS. Note `TestInstallRejectsVerifyKeyOnANonOCISource` (existing,
in the same file) still passes: its assertion is `strings.Contains(err.Error(), "oci://")`,
which the updated error message still satisfies.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/install.go internal/cli/install_verify_test.go
git commit -m "feat(install): verify keyless signatures with --verify-identity/--verify-issuer"
```

---

## Task 6: README updates

**Files:**
- Modify: `README.md`

**Interfaces:** none — documentation only.

- [ ] **Step 1: Extend the Features bullet**

In `README.md`, replace the OCI packaging bullet (around line 89):

```markdown
- **Package skills into a container image.** `skillsctl package <source-dir> <oci-ref>`
  bundles a directory of skills into an OCI artifact and pushes it to any
  registry `docker` can reach; `skillsctl install oci://registry/repo:tag`
  installs from one, and `outdated`/`update` follow a moved tag the same way
  they follow a moved git ref. `package --sign-key <path>` signs the pushed
  image with cosign, and `install --verify-key <path>` verifies it before
  installing — or sign and verify keylessly with `--sign-keyless` and
  `--verify-identity`/`--verify-issuer`, using Sigstore's Fulcio/Rekor flow
  instead of a keypair.
```

- [ ] **Step 2: Extend the Use section examples**

In `README.md`, right after the existing `--sign-key`/`--verify-key`
example lines (around line 133-134):

```markdown
skillsctl package ./my-skills ghcr.io/owner/skills:v1 --sign-key cosign.key  # ...and sign it
skillsctl install oci://ghcr.io/owner/skills:v1 --verify-key cosign.pub  # verify before installing
skillsctl package ./my-skills ghcr.io/owner/skills:v1 --sign-keyless  # sign via Sigstore's Fulcio/Rekor flow
skillsctl install oci://ghcr.io/owner/skills:v1 \
  --verify-identity signer@example.com --verify-issuer https://accounts.google.com  # verify a keyless signature
```

- [ ] **Step 3: Extend the Commands table**

In `README.md`, update the `install oci://<ref>` and `package` rows (around
lines 374-375):

```markdown
| `install oci://<ref>` | `--skill`, `--all`, `-a/--agent`, `--ref`, `--as`, `--pin`, `--verify-key`, `--verify-identity`, `--verify-issuer`, `--dry-run` | Install one or more skills from an OCI artifact |
| `package <source-dir> <oci-ref>` | `--sign-key`, `--sign-keyless`, `--dry-run` | Package a directory of skills into an OCI artifact and push it |
```

- [ ] **Step 4: Verify the examples against `--help`**

Run: `go build -o skillsctl ./cmd/skillsctl && ./skillsctl install --help && ./skillsctl package --help`
Expected: `--sign-keyless`, `--verify-identity`, `--verify-issuer` all appear
with the exact descriptions written in Tasks 4-5. Remove the built binary
afterward: `rm skillsctl`.

- [ ] **Step 5: Commit**

```bash
git add README.md
git commit -m "docs: document --sign-keyless and --verify-identity/--verify-issuer"
```

---

## Final Verification

- [ ] Run `make test && make lint && make tidy-check` from the repo root —
  all three must pass with no new Go dependency (`tidy-check` is a no-op).
- [ ] Manual (needs `cosign` installed and a real OIDC identity — interactive
  browser locally, or run inside GitHub Actions for the ambient-token path):
  `skillsctl package ./some-skills-dir <local-registry-ref> --sign-keyless`
  completes the OIDC flow and signs; `skillsctl install
  oci://<local-registry-ref> --verify-identity <id> --verify-issuer <issuer>`
  succeeds against the right identity/issuer and fails closed against the
  wrong ones; a keyless-signed image installed with neither `--verify-key`
  nor `--verify-identity` prints the existing signed-but-unverified warning.
