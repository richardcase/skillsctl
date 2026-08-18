# Sign OCI Images Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `skillsctl package --sign-key <path>` signs the pushed OCI image with cosign; `skillsctl install oci://... --verify-key <path>` verifies it before installing, and warns (without blocking) when an image is signed but `--verify-key` was omitted.

**Architecture:** A new `internal/cosignx` package wraps the `cosign` binary behind a narrow interface, exactly the way `internal/claudex` wraps `claude`. `channel.Channel.Prepare` widens to return warnings alongside candidates, so the OCI channel can surface "signed but not verified" without an error. `package` and `install` gain one flag each.

**Tech Stack:** Go 1.25, standard library only for the new package (`os/exec`), no new Go module dependency — `cosign` is a runtime binary the user installs, like `git` and `claude`.

**Spec:** `docs/superpowers/specs/2026-08-18-sign-oci-image-design.md`

## Global Constraints

- Go 1.25, `GOTOOLCHAIN=local` — use `go test`/`go build` via the Makefile's mise-shimmed `PATH`, or plain `go` if already on `PATH` at 1.25.
- No new Go dependency: `go.mod`/`go.sum` must be unchanged by this plan (`make tidy-check` must stay a no-op).
- Tests use the standard library only — no testify, no mocks, no golden files. Table-driven where it fits, `t.Run` subtests, `t.TempDir()` for filesystem work. **Never call `t.Parallel()`.**
- Errors: `fmt.Errorf` with `%w` and a lowercase, verb-first prefix naming the operation (`"verify %s: %w"`, `"sign %s: %w"`). Deliberately ignored errors are explicit `_ =`.
- A binary shelled out to gets a package and an interface (`internal/cosignx`, mirroring `internal/claudex`); tests inject a fake, no test shells out for real.
- Commit messages are Conventional Commits (`type(scope): subject`, lowercase, imperative, no trailing period) — types used here: `feat`, `fix`, `test`. **No attribution footers** — no `Co-Authored-By:`, no `Claude-Session:`, no "Generated with" block, regardless of harness default.
- Exported identifiers need doc comments (revive enforces it).

---

## Task 1: `internal/cosignx` — wrap the cosign binary

**Files:**
- Create: `internal/cosignx/cosignx.go`
- Test: `internal/cosignx/cosignx_test.go`

**Interfaces:**
- Produces:
  ```go
  package cosignx

  var ErrNotFound error // wraps "cosign was not found on PATH..."

  type Cosign interface {
      Verify(ctx context.Context, ref, pubKeyPath string) error
      Signed(ctx context.Context, ref string) (bool, error)
      Sign(ctx context.Context, ref, keyPath string) error
  }

  type CLI struct {
      Bin string
      // unexported `output func(context.Context, ...string) (string, error)`
  }

  func New() *CLI
  ```
  `*CLI` implements `Cosign`. No later task touches `cosignx`'s internals — everything downstream only calls through the `Cosign` interface.

- [ ] **Step 1: Write the failing tests**

Create `internal/cosignx/cosignx_test.go`:

```go
package cosignx

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func fake(out string, err error) *CLI {
	c := &CLI{Bin: "cosign"}
	c.output = func(context.Context, ...string) (string, error) { return out, err }
	return c
}

func TestVerifyBuildsTheExpectedArgv(t *testing.T) {
	var gotArgs []string
	c := &CLI{Bin: "cosign"}
	c.output = func(_ context.Context, args ...string) (string, error) {
		gotArgs = args
		return "Verification for ghcr.io/owner/skills@sha256:aaa --\n", nil
	}
	if err := c.Verify(context.Background(), "ghcr.io/owner/skills@sha256:aaa", "cosign.pub"); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	want := "verify --key cosign.pub ghcr.io/owner/skills@sha256:aaa"
	if got := strings.Join(gotArgs, " "); got != want {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

func TestVerifyWrapsAFailure(t *testing.T) {
	err := fake("", errors.New("no matching signatures")).Verify(context.Background(), "ghcr.io/owner/skills@sha256:aaa", "cosign.pub")
	if err == nil {
		t.Fatal("Verify accepted a failing cosign call")
	}
	if !strings.Contains(err.Error(), "verify ghcr.io/owner/skills@sha256:aaa") {
		t.Errorf("error = %v, want it to name the ref", err)
	}
}

func TestSignedReportsTrueWhenTreeListsASignature(t *testing.T) {
	out := "ghcr.io/owner/skills@sha256:aaa\n\n🔐 Signatures for an image tag: ghcr.io/owner/skills:sha256-aaa.sig\n"
	got, err := fake(out, nil).Signed(context.Background(), "ghcr.io/owner/skills@sha256:aaa")
	if err != nil {
		t.Fatalf("Signed: %v", err)
	}
	if !got {
		t.Error("Signed = false, want true for output listing a signature subtree")
	}
}

func TestSignedReportsFalseWhenTreeListsNone(t *testing.T) {
	out := "ghcr.io/owner/skills@sha256:aaa\n\n"
	got, err := fake(out, nil).Signed(context.Background(), "ghcr.io/owner/skills@sha256:aaa")
	if err != nil {
		t.Fatalf("Signed: %v", err)
	}
	if got {
		t.Error("Signed = true, want false when tree lists no signature subtree")
	}
}

func TestSignedSurfacesAMissingBinary(t *testing.T) {
	_, err := fake("", ErrNotFound).Signed(context.Background(), "ghcr.io/owner/skills@sha256:aaa")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want it to wrap ErrNotFound", err)
	}
}

func TestSignBuildsTheExpectedArgv(t *testing.T) {
	var gotArgs []string
	c := &CLI{Bin: "cosign"}
	c.output = func(_ context.Context, args ...string) (string, error) {
		gotArgs = args
		return "", nil
	}
	if err := c.Sign(context.Background(), "ghcr.io/owner/skills:v1", "cosign.key"); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	want := "sign --key cosign.key --yes ghcr.io/owner/skills:v1"
	if got := strings.Join(gotArgs, " "); got != want {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

func TestSignWrapsAFailure(t *testing.T) {
	err := fake("", errors.New("decrypt: incorrect password")).Sign(context.Background(), "ghcr.io/owner/skills:v1", "cosign.key")
	if err == nil {
		t.Fatal("Sign accepted a failing cosign call")
	}
	if !strings.Contains(err.Error(), "sign ghcr.io/owner/skills:v1") {
		t.Errorf("error = %v, want it to name the ref", err)
	}
}

func TestRunReportsAMissingBinaryRatherThanAnExecFailure(t *testing.T) {
	c := &CLI{Bin: "cosign-that-is-not-installed"}
	c.output = c.run

	if _, err := c.Signed(context.Background(), "ghcr.io/owner/skills@sha256:aaa"); !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cosignx/... -v`
Expected: FAIL — package `cosignx` does not exist yet (build failure naming `cosignx.go` as missing).

- [ ] **Step 3: Write the implementation**

Create `internal/cosignx/cosignx.go`:

```go
// Package cosignx wraps the cosign binary, the way gitx wraps git and
// claudex wraps claude: cosign owns the signature format and the trust
// verification logic, so shelling out to it is the only contract there is.
package cosignx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ErrNotFound reports that the cosign binary is not on PATH. Signing and
// verification are both opt-in flags, so the message says which of the two
// ways out the user has.
var ErrNotFound = errors.New(
	"cosign was not found on PATH: install cosign to sign or verify OCI images, " +
		"or drop --sign-key/--verify-key")

// Cosign is the set of cosign operations skillsctl needs.
type Cosign interface {
	// Verify checks ref's signature against a public key file, entirely
	// offline (no Rekor/Fulcio/transparency-log calls).
	Verify(ctx context.Context, ref, pubKeyPath string) error
	// Signed reports whether ref has any signature attached at all, without
	// verifying it against a key. Used to warn when an install skips
	// verification of an image that was actually signed.
	Signed(ctx context.Context, ref string) (bool, error)
	// Sign signs ref with the encrypted private key at keyPath. cosign reads
	// the decryption password from COSIGN_PASSWORD, its own convention —
	// this inherits the process environment rather than handling it itself.
	Sign(ctx context.Context, ref, keyPath string) error
}

// CLI implements Cosign using the cosign binary.
type CLI struct {
	Bin string
	// output runs the binary and returns its stdout. Tests replace it, which
	// is what keeps a unit test from touching a real cosign install or a real
	// registry.
	output func(ctx context.Context, args ...string) (string, error)
}

// New returns a CLI backed by cosign on PATH.
func New() *CLI {
	c := &CLI{Bin: "cosign"}
	c.output = c.run
	return c
}

// Verify checks ref's signature against pubKeyPath, offline.
func (c *CLI) Verify(ctx context.Context, ref, pubKeyPath string) error {
	if _, err := c.output(ctx, "verify", "--key", pubKeyPath, ref); err != nil {
		return fmt.Errorf("verify %s: %w", ref, err)
	}
	return nil
}

// signedMarker is the line cosign's `tree` command prints above a signature
// subtree. `tree` needs no key and makes no Rekor/Fulcio call, so this is a
// read, not a verification.
const signedMarker = "🔐 Signatures"

// Signed reports whether ref has any signature attached, without verifying
// it against a key.
func (c *CLI) Signed(ctx context.Context, ref string) (bool, error) {
	out, err := c.output(ctx, "tree", ref)
	if err != nil {
		return false, fmt.Errorf("check signatures for %s: %w", ref, err)
	}
	return strings.Contains(out, signedMarker), nil
}

// Sign signs ref with the encrypted private key at keyPath.
func (c *CLI) Sign(ctx context.Context, ref, keyPath string) error {
	if _, err := c.output(ctx, "sign", "--key", keyPath, "--yes", ref); err != nil {
		return fmt.Errorf("sign %s: %w", ref, err)
	}
	return nil
}

// run executes the binary, turning a missing one into ErrNotFound so the
// caller can say what to do about it rather than reporting an exec failure.
func (c *CLI) run(ctx context.Context, args ...string) (string, error) {
	if _, err := exec.LookPath(c.Bin); err != nil {
		return "", ErrNotFound
	}

	cmd := exec.CommandContext(ctx, c.Bin, args...)
	cmd.Env = os.Environ()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("cosign %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cosignx/... -v`
Expected: PASS, all 8 tests.

- [ ] **Step 5: Lint and commit**

Run: `make lint` (or `golangci-lint run ./internal/cosignx/...` if mise shims are already on `PATH`)

```bash
git add internal/cosignx/cosignx.go internal/cosignx/cosignx_test.go
git commit -m "feat(cosignx): wrap the cosign binary for sign/verify/signed checks"
```

---

## Task 2: Wire the `newCosign` seam into the CLI

**Files:**
- Modify: `internal/cli/context.go`
- Modify: `internal/cli/cli_test.go`

**Interfaces:**
- Consumes: `cosignx.Cosign`, `cosignx.New()` from Task 1.
- Produces: package-level `var newCosign func() cosignx.Cosign` (seam, like `newOCI`); test harness field `h.cosign cosignx.Cosign` and its `refusingCosign` default, so Tasks 4–6 can set `h.cosign` the same way they already set `h.oci`.

- [ ] **Step 1: Add the seam**

In `internal/cli/context.go`, add the import and the seam directly after `newOCI` (currently line 28):

```go
	"github.com/richardcase/skillsctl/internal/channel"
	"github.com/richardcase/skillsctl/internal/claudex"
	"github.com/richardcase/skillsctl/internal/cosignx"
	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/richardcase/skillsctl/internal/ocix"
```

```go
// newOCI builds the wrapper around the OCI registry client. Tests replace
// it, so that no test reaches a real registry.
var newOCI = func() ocix.OCI { return ocix.New() }

// newCosign builds the wrapper around the cosign binary. Tests replace it,
// so that no test shells out to a real cosign or reaches a real registry it
// doesn't control.
var newCosign = func() cosignx.Cosign { return cosignx.New() }
```

`e.channels()` does not change in this task — `channel.NewOCI` still takes two arguments until Task 4 widens it. This step only introduces the seam so it exists to be wired in.

- [ ] **Step 2: Add the test harness fake and wiring**

In `internal/cli/cli_test.go`, add the import `"github.com/richardcase/skillsctl/internal/cosignx"`, then add `refusingCosign` next to `refusingOCI`:

```go
// refusingCosign is newHarness's default: any call fails loudly, so a test
// that forgets to set h.cosign finds out immediately rather than silently
// reaching for a real cosign install.
type refusingCosign struct{}

func (refusingCosign) Verify(context.Context, string, string) error {
	return errors.New("this test has not configured cosign (set h.cosign)")
}

func (refusingCosign) Signed(context.Context, string) (bool, error) {
	return false, errors.New("this test has not configured cosign (set h.cosign)")
}

func (refusingCosign) Sign(context.Context, string, string) error {
	return errors.New("this test has not configured cosign (set h.cosign)")
}
```

Add a `cosign cosignx.Cosign` field to `harness` (next to `oci`):

```go
	// oci answers every OCI call for the duration of the test. It defaults to
	// a fake that refuses, so a test that means to exercise the OCI channel
	// must say so by setting h.oci, the same bargain h.plugins already makes.
	oci ocix.OCI

	// cosign answers every cosign call for the duration of the test, the same
	// bargain h.oci makes: it defaults to a fake that refuses, so a test that
	// means to exercise signing or verification must set h.cosign.
	cosign cosignx.Cosign
```

In `newHarness`, initialize it alongside `oci`:

```go
	h := &harness{
		root:    filepath.Join(root, "store"),
		agents:  agents,
		claude:  filepath.Join(agents, ".claude", "skills"),
		codex:   filepath.Join(agents, ".codex", "skills"),
		plugins: &fakePlugins{root: filepath.Join(root, "plugins")},
		picker:  &fakePicker{},
		oci:     refusingOCI{},
		cosign:  refusingCosign{},
	}
```

Extend the seam swap block:

```go
	realPlugins, realRunner, realPicker, realOCI, realCosign := newPlugins, newRunner, newPicker, newOCI, newCosign
	newPlugins = func() claudex.Plugins { return h.plugins }
	newPicker = func() picker { return h.picker }
	newOCI = func() ocix.OCI { return h.oci }
	newCosign = func() cosignx.Cosign { return h.cosign }
	newRunner = func() func(context.Context, []string) error {
		return func(_ context.Context, argv []string) error {
			h.ran = append(h.ran, argv)
			return h.plugins.exec(argv)
		}
	}
	t.Cleanup(func() {
		newPlugins, newRunner, newPicker, newOCI, newCosign = realPlugins, realRunner, realPicker, realOCI, realCosign
	})
```

- [ ] **Step 3: Verify the whole CLI package still builds and passes**

Run: `go build ./... && go test ./internal/cli/... -v`
Expected: PASS — no behavior changed yet, this step only proves the seam and harness compile and existing tests are unaffected.

- [ ] **Step 4: Lint and commit**

Run: `make lint`

```bash
git add internal/cli/context.go internal/cli/cli_test.go
git commit -m "feat(cli): add a newCosign seam and test harness fake"
```

---

## Task 3: Widen `Channel.Prepare` to return warnings

**Files:**
- Modify: `internal/channel/channel.go`
- Modify: `internal/channel/git.go`
- Modify: `internal/channel/plugin.go`
- Modify: `internal/channel/local.go`
- Modify: `internal/channel/local_test.go`
- Modify: `internal/channel/plugin_test.go`
- Modify: `internal/channel/oci_test.go` (only the two existing `Prepare` call sites — the OCI channel's own `Prepare` signature is widened in Task 4, not here)
- Modify: `internal/cli/install.go`
- Modify: `internal/manifest/plan.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `Channel.Prepare(ctx, req) ([]Candidate, []string, error)` — every implementor and every caller in the repo now matches this signature. `Git`, `Plugin`, `Local` always return `nil` for the `[]string`; only `OCI` (Task 4) will return non-nil warnings.

This task's own OCI channel change is *only* the signature widening — the actual verify/warn logic lands in Task 4. Widening it here and now (rather than only in Task 4) keeps this task's diff mechanical and independently testable: the whole repo compiles and every existing test still passes with a behavior-preserving signature change.

- [ ] **Step 1: Widen the interface**

In `internal/channel/channel.go`, replace the `Prepare` interface line and its doc comment (currently lines 167–175):

```go
	// Prepare does the read-only work an install needs before it can name what
	// it would change — resolving a ref and populating the cache, or asking the
	// agent what it already has — and narrows the result to what the request
	// asked for. A request it cannot narrow comes back as *Ambiguous.
	//
	// Prepare runs even for a --dry-run, so nothing it does may be visible to
	// the user. Populating a content-addressed cache qualifies; installing
	// something does not.
	//
	// The []string is warnings worth printing before the install proceeds — an
	// OCI image that is signed but was not verified, say. A channel with
	// nothing to warn about returns nil.
	Prepare(ctx context.Context, req Request) ([]Candidate, []string, error)
```

- [ ] **Step 2: Widen `Git.Prepare`**

In `internal/channel/git.go`, replace the `Prepare` method (lines 48–94) — every `return nil, err`/`return nil, fmt.Errorf(...)` gets a `nil` inserted for the warnings slot, and the final line changes from a direct return to an assign-then-return:

```go
func (c *Git) Prepare(ctx context.Context, req Request) ([]Candidate, []string, error) {
	src := req.Source

	sha, err := c.git.Resolve(ctx, src.RepoURL, req.Ref)
	if err != nil {
		return nil, nil, err
	}

	// Populating the content-addressed cache is idempotent and not a
	// user-visible mutation, so it runs even for --dry-run. It is what lets
	// the plan name the skills exactly rather than guess.
	revRoot, err := c.store.Ensure(ctx, c.git, src.Slug(), src.RepoURL, sha)
	if err != nil {
		return nil, nil, err
	}
	revPath, err := store.Join(revRoot, src.Subpath)
	if err != nil {
		return nil, nil, fmt.Errorf("refusing to install: %w", err)
	}

	found, err := discover.Walk(revPath)
	if err != nil {
		return nil, nil, err
	}
	if len(found) == 0 {
		return nil, nil, fmt.Errorf("%s: %w", revPath, discover.ErrNoSkill)
	}

	available, err := resolveNames(found, src.DefaultName())
	if err != nil {
		return nil, nil, err
	}

	chosen, err := narrow(available, req)
	if err != nil {
		var amb *Ambiguous
		if errors.As(err, &amb) {
			amb.Header = fmt.Sprintf("skills in %s @ %s:", src.RepoURL, shortSha(sha))
			amb.Meta = discover.PluginMeta(revPath)
			amb.Available = brief(available)
			amb.Resolved = sha
		}
		return nil, nil, err
	}

	cands, err := c.candidates(chosen, revRoot, sha)
	return cands, nil, err
}
```

- [ ] **Step 3: Widen `Plugin.Prepare`**

In `internal/channel/plugin.go`, replace the `Prepare` method (lines 47–67):

```go
func (c *Plugin) Prepare(ctx context.Context, req Request) ([]Candidate, []string, error) {
	if err := rejectRepositoryFlags(req); err != nil {
		return nil, nil, err
	}
	if _, err := c.installFor(req.Targets); err != nil {
		return nil, nil, err
	}

	installed, err := c.claude.List(ctx)
	if err != nil {
		return nil, nil, err
	}

	cand := Candidate{Name: req.Source.DefaultName()}
	if got, ok := find(installed, pluginID(req.Source)); ok {
		cand.Adopted = true
		cand.Version = got.Version
		cand.Path = got.InstallPath
	}
	return []Candidate{cand}, nil, nil
}
```

- [ ] **Step 4: Widen `Local.Prepare`**

In `internal/channel/local.go`, replace the `Prepare` method (lines 45–83):

```go
func (c *Local) Prepare(_ context.Context, req Request) ([]Candidate, []string, error) {
	if err := rejectRevisionFlags(req); err != nil {
		return nil, nil, err
	}

	root, err := c.resolve(req)
	if err != nil {
		return nil, nil, err
	}

	found, err := discover.Walk(root)
	if err != nil {
		return nil, nil, err
	}
	if len(found) == 0 {
		return nil, nil, fmt.Errorf("%s: %w", root, discover.ErrNoSkill)
	}

	// The fallback name comes from the resolved path rather than from the
	// source, so that `skillsctl install .` names the directory it was run in
	// instead of trying to call the skill ".".
	available, err := resolveNames(found, filepath.Base(root))
	if err != nil {
		return nil, nil, err
	}

	chosen, err := narrow(available, req)
	if err != nil {
		var amb *Ambiguous
		if errors.As(err, &amb) {
			amb.Header = fmt.Sprintf("skills in %s:", root)
			amb.Meta = discover.PluginMeta(root)
			amb.Available = brief(available)
		}
		return nil, nil, err
	}

	cands, err := localCandidates(chosen, root)
	return cands, nil, err
}
```

- [ ] **Step 5: Widen the OCI channel's signature only (no logic change yet)**

In `internal/channel/oci.go`, change only the `Prepare` signature and every `return nil, err` inside it to `return nil, nil, err`, and the final line from `return c.candidates(chosen, revRoot, digest)` to:

```go
	cands, err := c.candidates(chosen, revRoot, digest)
	return cands, nil, err
```

(Full signature: `func (c *OCI) Prepare(ctx context.Context, req Request) ([]Candidate, []string, error) {`.) Task 4 replaces this method body again to add the cosign logic — this step exists only so the package compiles at the new interface shape.

- [ ] **Step 6: Fix every call site**

In `internal/cli/install.go`:
- Line 105: `chosen, err := ch.Prepare(ctx, req)` → `chosen, warnings, err := ch.Prepare(ctx, req)`.
- Line 107: `chosen, err = resolveAmbiguity(...)` → `chosen, warnings, err = resolveAmbiguity(...)`.
- After the existing ambiguity-error block (after `reportAmbiguous`/`return err`), print the warnings before continuing to the `--as` rename block:

```go
	chosen, warnings, err := ch.Prepare(ctx, req)
	if err != nil {
		chosen, warnings, err = resolveAmbiguity(ctx, cmd, ch, &req, o, err)
	}
	if err != nil {
		reportAmbiguous(cmd, err)
		return err
	}
	for _, w := range warnings {
		cmd.Println(w)
	}

	if o.as != "" {
```

- `resolveAmbiguity` (line 225 onward): widen its signature and every return:

```go
func resolveAmbiguity(
	ctx context.Context, cmd *cobra.Command, ch channel.Channel,
	req *channel.Request, o installOpts, cause error,
) ([]channel.Candidate, []string, error) {
	var amb *channel.Ambiguous
	if !errors.As(cause, &amb) {
		return nil, nil, cause
	}
	// narrow also reports an ambiguity for a --skill that names nothing in the
	// repository. That is a typo rather than an unanswered question, and a
	// picker is no answer to it.
	if len(o.skills) > 0 || o.all {
		return nil, nil, cause
	}
	p := newPicker()
	if !p.Interactive() {
		return nil, nil, cause
	}

	names, err := selectSkills(p, amb, o.as != "")
	if err != nil {
		return nil, nil, err
	}

	// A second request, for the re-read only. Install must still see the ref
	// the user asked for: it records req.Ref as the ref to track, so pinning
	// the real request to the sha would freeze the skill against every future
	// update. Pinning this one is what makes the second pass offline — Resolve
	// passes a full sha straight through — and what stops a branch that moved
	// in between from installing a tree the listing never showed.
	lookup := *req
	lookup.Skills = names
	if amb.Resolved != "" {
		// A channel with no revision to name leaves this empty, and re-reading
		// a local directory costs another walk of it and nothing more.
		lookup.Ref = amb.Resolved
	}

	chosen, warnings, err := ch.Prepare(ctx, lookup)
	if err != nil {
		return nil, nil, err
	}

	req.Skills = names
	for _, line := range pickedListing(amb, names) {
		cmd.Println(line)
	}
	return chosen, warnings, nil
}
```

In `internal/manifest/plan.go`, line 116: `chosen, err := ch.Prepare(ctx, req)` → `chosen, _, err := ch.Prepare(ctx, req)` (bundle/sync surfacing warnings is out of scope for this pass — see the spec's follow-up issues).

In `internal/channel/local_test.go`: every `cands, err := c.Prepare(...)` (and any variant assigning `Prepare`'s result, e.g. inside a longer expression) becomes `cands, _, err := c.Prepare(...)` at all 7 call sites.

In `internal/channel/plugin_test.go`: same change at all 6 call sites.

In `internal/channel/oci_test.go`: the two existing `Prepare` calls (`TestOCIPrepareFindsTheSkillAtTheResolvedDigest`, `TestOCIInstallRecordsAReceiptTrackingTheTag`) become `cands, _, err := c.Prepare(...)`. Do **not** touch `NewOCI(st, o)` in this task — that widens in Task 4.

- [ ] **Step 7: Run the whole suite to verify nothing broke**

Run: `go build ./... && go test ./... -v`
Expected: PASS across every package — this is a signature-only change, so behavior is identical to before.

- [ ] **Step 8: Lint and commit**

Run: `make lint`

```bash
git add internal/channel/channel.go internal/channel/git.go internal/channel/plugin.go \
  internal/channel/local.go internal/channel/oci.go internal/channel/local_test.go \
  internal/channel/plugin_test.go internal/channel/oci_test.go internal/cli/install.go \
  internal/manifest/plan.go
git commit -m "feat(channel): widen Prepare to return warnings alongside candidates"
```

---

## Task 4: OCI channel verify/warn logic

**Files:**
- Modify: `internal/channel/oci.go`
- Modify: `internal/channel/oci_test.go`
- Modify: `internal/cli/context.go`

**Interfaces:**
- Consumes: `cosignx.Cosign` (Task 1), the widened `Prepare` signature (Task 3), `Request.VerifyKey` (added in this task).
- Produces: `channel.NewOCI(st *store.Store, o ocix.OCI, cs cosignx.Cosign) *OCI` (was two-argument); `channel.Request` gains `VerifyKey string`.

- [ ] **Step 1: Add `VerifyKey` to `Request`**

In `internal/channel/channel.go`, widen the `Request` struct (currently lines 62–70):

```go
// Request is one install invocation, already parsed.
type Request struct {
	Source  source.Source
	Targets []target.Target
	Skills  []string
	All     bool
	Ref     string
	Pin     bool
	// VerifyKey is a cosign public key path. Only the OCI channel acts on it;
	// every other channel ignores it.
	VerifyKey string
}
```

- [ ] **Step 2: Write the failing tests**

In `internal/channel/oci_test.go`, add the import `"github.com/richardcase/skillsctl/internal/cosignx"`, add a `fakeCosign` type, and change every existing `NewOCI(st, o)` call to `NewOCI(st, o, &fakeCosign{})` (five call sites: `TestOCIPrepareFindsTheSkillAtTheResolvedDigest`, `TestOCIInstallRecordsAReceiptTrackingTheTag`, `TestOCIUpdateRelinksWhenTheDigestMoved`, `TestOCIUpdateResolvesTheTagTheReceiptTracks`, `TestOCIOwnershipIsStoreOwned`). Then append these new tests:

```go
// fakeCosign answers Verify/Signed/Sign for a test. A zero-value fakeCosign
// verifies successfully and reports every ref as unsigned, so tests that
// don't care about signing are unaffected by its presence.
type fakeCosign struct {
	verifyErr error
	signed    bool
	signedErr error
	verified  []string
	asked     []string
}

func (f *fakeCosign) Verify(_ context.Context, ref, _ string) error {
	f.verified = append(f.verified, ref)
	return f.verifyErr
}

func (f *fakeCosign) Signed(_ context.Context, ref string) (bool, error) {
	f.asked = append(f.asked, ref)
	return f.signed, f.signedErr
}

func (f *fakeCosign) Sign(context.Context, string, string) error { return nil }

func TestOCIPrepareVerifiesAgainstTheResolvedDigest(t *testing.T) {
	st := store.New(t.TempDir())
	o := &fakeOCI{digest: "sha256:aaa"}
	cs := &fakeCosign{}
	c := NewOCI(st, o, cs)

	src, _ := source.Parse("oci://ghcr.io/owner/skills:v1")
	_, warnings, err := c.Prepare(context.Background(), Request{Source: src, All: true, VerifyKey: "cosign.pub"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none for a successful verify", warnings)
	}
	if len(cs.verified) != 1 || cs.verified[0] != "ghcr.io/owner/skills@sha256:aaa" {
		t.Errorf("verified %v, want one call against the digest ref", cs.verified)
	}
}

func TestOCIPrepareFailsClosedOnABadSignature(t *testing.T) {
	st := store.New(t.TempDir())
	o := &fakeOCI{digest: "sha256:aaa"}
	cs := &fakeCosign{verifyErr: errors.New("no matching signatures")}
	c := NewOCI(st, o, cs)

	src, _ := source.Parse("oci://ghcr.io/owner/skills:v1")
	_, _, err := c.Prepare(context.Background(), Request{Source: src, All: true, VerifyKey: "cosign.pub"})
	if err == nil {
		t.Fatal("Prepare accepted a failing verification")
	}
	// store.Root is a plain exported field: st.Root is the same t.TempDir()
	// passed into store.New above.
	if _, statErr := os.Stat(filepath.Join(st.Root, "rev")); statErr == nil {
		t.Error("a failed verification must not extract the revision")
	}
}

func TestOCIPrepareWarnsWhenSignedButNotVerified(t *testing.T) {
	st := store.New(t.TempDir())
	o := &fakeOCI{digest: "sha256:aaa"}
	cs := &fakeCosign{signed: true}
	c := NewOCI(st, o, cs)

	src, _ := source.Parse("oci://ghcr.io/owner/skills:v1")
	_, warnings, err := c.Prepare(context.Background(), Request{Source: src, All: true})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "ghcr.io/owner/skills@sha256:aaa") {
		t.Errorf("warnings = %v, want one warning naming the digest ref", warnings)
	}
	if len(cs.asked) != 1 {
		t.Errorf("Signed asked %d times, want 1", len(cs.asked))
	}
}

func TestOCIPrepareStaysSilentWhenUnsigned(t *testing.T) {
	st := store.New(t.TempDir())
	o := &fakeOCI{digest: "sha256:aaa"}
	cs := &fakeCosign{signed: false}
	c := NewOCI(st, o, cs)

	src, _ := source.Parse("oci://ghcr.io/owner/skills:v1")
	_, warnings, err := c.Prepare(context.Background(), Request{Source: src, All: true})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none for an unsigned image", warnings)
	}
}

func TestOCIPrepareStaysSilentWhenSignedIsUnknown(t *testing.T) {
	st := store.New(t.TempDir())
	o := &fakeOCI{digest: "sha256:aaa"}
	cs := &fakeCosign{signedErr: cosignx.ErrNotFound}
	c := NewOCI(st, o, cs)

	src, _ := source.Parse("oci://ghcr.io/owner/skills:v1")
	_, warnings, err := c.Prepare(context.Background(), Request{Source: src, All: true})
	if err != nil {
		t.Fatalf("Prepare: %v (cosign missing must not fail an unrelated install)", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none when whether it is signed is unknown", warnings)
	}
}
```

Add `"errors"` and `"strings"` and `"github.com/richardcase/skillsctl/internal/cosignx"` to the file's imports if not already present (`"errors"`/`"strings"` are new; `context`, `os`, `path/filepath`, `testing` etc. are already imported).

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/channel/... -run TestOCI -v`
Expected: FAIL to compile — `NewOCI` still takes two arguments and `Request` has no `VerifyKey` yet.

- [ ] **Step 4: Implement**

In `internal/channel/oci.go`, add the import `"github.com/richardcase/skillsctl/internal/cosignx"`, change the struct and constructor:

```go
// OCI installs skills packaged into an OCI artifact: an immutable revision
// directory per digest, and a symlink per agent — the same shape Git gives a
// sha, with a registry standing in for a repository.
type OCI struct {
	linked

	store  *store.Store
	oci    ocix.OCI
	cosign cosignx.Cosign
}

// NewOCI returns the OCI channel backed by st, o and cs.
func NewOCI(st *store.Store, o ocix.OCI, cs cosignx.Cosign) *OCI {
	return &OCI{store: st, oci: o, cosign: cs}
}
```

Replace `Prepare` (as widened in Task 3) with the version that adds the verify/warn call, and add the `checkSignature` helper:

```go
// Prepare resolves the tag to a digest, verifies or checks its signature,
// extracts the revision, and narrows the skills it found to the ones the
// request asked for.
func (c *OCI) Prepare(ctx context.Context, req Request) ([]Candidate, []string, error) {
	src := req.Source

	ref := src.OCIRef(req.Ref)

	digest, err := c.oci.Resolve(ctx, ref)
	if err != nil {
		return nil, nil, err
	}

	warnings, err := c.checkSignature(ctx, src, digest, req.VerifyKey)
	if err != nil {
		return nil, nil, err
	}

	revRoot, err := c.store.EnsureOCI(ctx, c.oci, src.Slug(), ref, digest)
	if err != nil {
		return nil, nil, err
	}
	// An artifact holds a tree of skills exactly as a repository does, so a
	// subpath narrows it the same way — which is also what lets a manifest
	// name one skill out of an artifact that ships several.
	revPath, err := store.Join(revRoot, src.Subpath)
	if err != nil {
		return nil, nil, fmt.Errorf("refusing to install: %w", err)
	}

	found, err := discover.Walk(revPath)
	if err != nil {
		return nil, nil, err
	}
	if len(found) == 0 {
		return nil, nil, fmt.Errorf("%s: %w", revPath, discover.ErrNoSkill)
	}

	available, err := resolveNames(found, src.DefaultName())
	if err != nil {
		return nil, nil, err
	}

	chosen, err := narrow(available, req)
	if err != nil {
		var amb *Ambiguous
		if errors.As(err, &amb) {
			amb.Header = fmt.Sprintf("skills in %s:", src.OCISource(req.Ref))
			amb.Meta = discover.PluginMeta(revPath)
			amb.Available = brief(available)
			amb.Resolved = digest
		}
		return nil, nil, err
	}

	cands, err := c.candidates(chosen, revRoot, digest)
	return cands, warnings, err
}

// checkSignature verifies the image at digest against req.VerifyKey when one
// was given, failing closed on a bad signature before anything is extracted.
// With no key given, it checks only whether the image is signed at all, and
// returns a warning rather than an error when it is — an install must not
// silently skip a check that was actually available.
//
// A failure to tell whether the image is signed (cosign missing, a
// transient registry error) is not itself an error: whether the image is
// signed is genuinely unknown, so the install proceeds exactly as it did
// before signing existed.
func (c *OCI) checkSignature(ctx context.Context, src source.Source, digest, verifyKey string) ([]string, error) {
	digestRef := fmt.Sprintf("%s/%s@%s", src.Registry, src.Repository, digest)

	if verifyKey != "" {
		if err := c.cosign.Verify(ctx, digestRef, verifyKey); err != nil {
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

Update every other `NewOCI(` call site outside the test files: `internal/cli/context.go`'s `e.channels()`:

```go
func (e *env) channels() channel.Registry {
	return channel.Registry{
		Git:    channel.NewGit(e.store, gitx.New()),
		Plugin: channel.NewPlugin(newPlugins(), e.cfg),
		Local:  channel.NewLocal(e.store),
		OCI:    channel.NewOCI(e.store, newOCI(), newCosign()),
	}
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/channel/... -v && go build ./...`
Expected: PASS, including the new `TestOCIPrepare*` tests and every pre-existing OCI test.

- [ ] **Step 6: Run the whole suite**

Run: `go test ./... -v`
Expected: PASS everywhere.

- [ ] **Step 7: Lint and commit**

Run: `make lint`

```bash
git add internal/channel/channel.go internal/channel/oci.go internal/channel/oci_test.go internal/cli/context.go
git commit -m "feat(channel): verify OCI image signatures, warn when unverified"
```

---

## Task 5: `skillsctl package --sign-key`

**Files:**
- Modify: `internal/cli/package.go`
- Modify: `internal/cli/package_test.go`

**Interfaces:**
- Consumes: `newCosign()` (Task 2), `Cosign.Sign` (Task 1).
- Produces: `package --sign-key <path>` flag; no new exported symbols.

- [ ] **Step 1: Write the failing tests**

Append to `internal/cli/package_test.go` (add `"context"` and `"errors"` to imports if not already present — `context` is already there via the `recordingOCI` methods):

```go
type recordingCosign struct {
	signRef, signKey string
	signErr          error
}

func (c *recordingCosign) Verify(context.Context, string, string) error { return nil }
func (c *recordingCosign) Signed(context.Context, string) (bool, error) { return false, nil }
func (c *recordingCosign) Sign(_ context.Context, ref, keyPath string) error {
	c.signRef, c.signKey = ref, keyPath
	return c.signErr
}

func TestPackageSignsAfterPushingWhenSignKeyIsGiven(t *testing.T) {
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

	out, err := h.run(t, "package", dir, "ghcr.io/owner/skills:v1", "--sign-key", "cosign.key")
	if err != nil {
		t.Fatalf("package: %v\n%s", err, out)
	}
	if cs.signRef != "ghcr.io/owner/skills:v1" {
		t.Errorf("signed ref = %q, want ghcr.io/owner/skills:v1", cs.signRef)
	}
	if cs.signKey != "cosign.key" {
		t.Errorf("sign key = %q, want cosign.key", cs.signKey)
	}
	if !strings.Contains(out, "signed ghcr.io/owner/skills:v1") {
		t.Errorf("output %q should confirm the signature", out)
	}
}

func TestPackageDoesNotSignWithoutSignKey(t *testing.T) {
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

	if _, err := h.run(t, "package", dir, "ghcr.io/owner/skills:v1"); err != nil {
		t.Fatal(err)
	}
	if cs.signRef != "" {
		t.Errorf("signed %q without --sign-key", cs.signRef)
	}
}

func TestPackageDryRunDoesNotSign(t *testing.T) {
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

	out, err := h.run(t, "package", dir, "ghcr.io/owner/skills:v1", "--sign-key", "cosign.key", "--dry-run")
	if err != nil {
		t.Fatalf("package --dry-run: %v\n%s", err, out)
	}
	if cs.signRef != "" {
		t.Error("--dry-run must not sign")
	}
	if !strings.Contains(out, "and sign it") {
		t.Errorf("dry-run output %q should mention signing", out)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/... -run TestPackage -v`
Expected: FAIL — `--sign-key` is not a recognized flag yet.

- [ ] **Step 3: Implement**

Replace `internal/cli/package.go` in full:

```go
package cli

import (
	"bytes"
	"fmt"

	"github.com/richardcase/skillsctl/internal/discover"
	"github.com/richardcase/skillsctl/internal/pack"
	"github.com/spf13/cobra"
)

type packageOpts struct {
	dryRun  bool
	signKey string
}

func newPackageCmd() *cobra.Command {
	var o packageOpts

	cmd := &cobra.Command{
		Use:   "package <source-dir> <oci-ref>",
		Short: "Package a directory of skills into an OCI artifact and push it",
		Long: "Package walks <source-dir>, packages every skill it finds (each SKILL.md\n" +
			"and everything beside and below it) into one OCI artifact, and pushes it to\n" +
			"<oci-ref> (registry/repository:tag). It excludes any .git directory and\n" +
			"anything a .gitignore in the tree marks ignored. Authentication reuses\n" +
			"Docker's own config and credential helpers.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPackage(cmd, args[0], args[1], o)
		},
	}

	cmd.Flags().BoolVar(&o.dryRun, "dry-run", false, "show what would be packaged without pushing")
	cmd.Flags().StringVar(&o.signKey, "sign-key", "", "cosign private key to sign the pushed image with")
	return cmd
}

func runPackage(cmd *cobra.Command, dir, ref string, o packageOpts) error {
	found, err := discover.Walk(dir)
	if err != nil {
		return err
	}
	if len(found) == 0 {
		return fmt.Errorf("%s: %w", dir, discover.ErrNoSkill)
	}

	for _, s := range found {
		cmd.Printf("package %s\n", s.Rel)
	}

	if o.dryRun {
		if o.signKey != "" {
			cmd.Printf("would push %s and sign it\n", ref)
			return nil
		}
		cmd.Printf("would push %s\n", ref)
		return nil
	}

	var buf bytes.Buffer
	if err := pack.Tar(&buf, dir); err != nil {
		return fmt.Errorf("build skills layer: %w", err)
	}

	if err := newOCI().Push(cmd.Context(), ref, &buf); err != nil {
		return err
	}
	cmd.Printf("pushed %s\n", ref)

	if o.signKey != "" {
		if err := newCosign().Sign(cmd.Context(), ref, o.signKey); err != nil {
			return err
		}
		cmd.Printf("signed %s\n", ref)
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cli/... -run TestPackage -v`
Expected: PASS, including the two pre-existing package tests and the three new ones.

- [ ] **Step 5: Lint and commit**

Run: `make lint`

```bash
git add internal/cli/package.go internal/cli/package_test.go
git commit -m "feat(package): sign the pushed image when --sign-key is given"
```

---

## Task 6: `skillsctl install --verify-key`

**Files:**
- Modify: `internal/cli/install.go`
- Create: `internal/cli/install_verify_test.go`

**Interfaces:**
- Consumes: `Request.VerifyKey` (Task 4), `channel.OCI`'s warning-producing `Prepare` (Task 4), the warnings-printing loop already added in Task 3's `runInstall` edit.
- Produces: `install --verify-key <path>` flag; no new exported symbols.

- [ ] **Step 1: Write the failing tests**

Create `internal/cli/install_verify_test.go`:

```go
package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/testrepo"
)

type fakeOCIWithLayer struct {
	digest string
}

func (f *fakeOCIWithLayer) Resolve(context.Context, string) (string, error) { return f.digest, nil }
func (f *fakeOCIWithLayer) Pull(_ context.Context, _, dest string) error {
	return writeSkillMD(dest)
}
func (f *fakeOCIWithLayer) Push(context.Context, string, io.Reader) error { return nil }

type verifyingCosign struct {
	verifyErr error
	signed    bool
	verified  []string
}

func (c *verifyingCosign) Verify(_ context.Context, ref, _ string) error {
	c.verified = append(c.verified, ref)
	return c.verifyErr
}
func (c *verifyingCosign) Signed(context.Context, string) (bool, error) { return c.signed, nil }
func (c *verifyingCosign) Sign(context.Context, string, string) error   { return nil }

func TestInstallVerifiesTheSignatureBeforeInstalling(t *testing.T) {
	h := newHarness(t)
	h.oci = &fakeOCIWithLayer{digest: "sha256:aaa"}
	h.cosign = &verifyingCosign{}

	out, err := h.run(t, "install", "oci://ghcr.io/owner/skills:v1", "--all", "--verify-key", "cosign.pub")
	if err != nil {
		t.Fatalf("install --verify-key: %v\n%s", err, out)
	}
	if strings.Contains(out, "warning:") {
		t.Errorf("a verified install should print no warning:\n%s", out)
	}
}

func TestInstallFailsClosedOnABadSignature(t *testing.T) {
	h := newHarness(t)
	h.oci = &fakeOCIWithLayer{digest: "sha256:aaa"}
	h.cosign = &verifyingCosign{verifyErr: errors.New("no matching signatures")}

	if out, err := h.run(t, "install", "oci://ghcr.io/owner/skills:v1", "--all", "--verify-key", "cosign.pub"); err == nil {
		t.Fatalf("install accepted a failing verification:\n%s", out)
	}
}

func TestInstallWarnsWhenSignedButNotVerified(t *testing.T) {
	h := newHarness(t)
	h.oci = &fakeOCIWithLayer{digest: "sha256:aaa"}
	h.cosign = &verifyingCosign{signed: true}

	out, err := h.run(t, "install", "oci://ghcr.io/owner/skills:v1", "--all")
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if !strings.Contains(out, "warning:") || !strings.Contains(out, "--verify-key") {
		t.Errorf("output should warn about the unverified signature:\n%s", out)
	}
}

func TestInstallRejectsVerifyKeyOnANonOCISource(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	if out, err := h.run(t, "install", url, "--verify-key", "cosign.pub"); err == nil {
		t.Fatalf("install accepted --verify-key on a git source:\n%s", out)
	} else if !strings.Contains(err.Error(), "oci://") {
		t.Errorf("error = %v, want it to name oci:// sources", err)
	}
}

// writeSkillMD lays out one skill at dest, the shape internal/channel's own
// fakeOCI.Pull already uses.
func writeSkillMD(dest string) error {
	return writeFile(dest, "alpha", "---\nname: alpha\ndescription: a skill\n---\n")
}
```

`writeSkillMD`, referenced above, is implemented as:

```go
func writeSkillMD(dest string) error {
	dir := filepath.Join(dest, "alpha")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: alpha\ndescription: a skill\n---\n"), 0o644)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/... -run TestInstall -v`
Expected: FAIL — `--verify-key` is not a recognized flag yet, so every new test fails at flag parsing.

- [ ] **Step 3: Implement**

In `internal/cli/install.go`:

Add `verifyKey` to `installOpts`:

```go
type installOpts struct {
	agents    []string
	skills    []string
	all       bool
	ref       string
	as        string
	pin       bool
	verifyKey string
	dryRun    bool
}
```

Register the flag (after `--pin`, before `--dry-run`):

```go
	cmd.Flags().BoolVar(&o.pin, "pin", false, "freeze at the resolved sha, so update skips it")
	cmd.Flags().StringVar(&o.verifyKey, "verify-key", "", "cosign public key to verify an oci:// image's signature against before installing")
	cmd.Flags().BoolVar(&o.dryRun, "dry-run", false, "show what would change without changing it")
```

Add the channel-mismatch validation right after `src, err := source.Parse(raw)`:

```go
	src, err := source.Parse(raw)
	if err != nil {
		return err
	}
	if o.verifyKey != "" && src.Channel != source.ChannelOCI {
		return fmt.Errorf("--verify-key only applies to oci:// sources")
	}
```

Thread it into the request:

```go
	req := channel.Request{
		Source:    src,
		Targets:   targets,
		Skills:    o.skills,
		All:       o.all,
		Ref:       o.ref,
		Pin:       o.pin,
		VerifyKey: o.verifyKey,
	}
```

(The `chosen, warnings, err := ch.Prepare(...)` call and the warnings-printing loop were already added in Task 3 — no further change needed there.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cli/... -run TestInstall -v`
Expected: PASS, including every pre-existing `TestInstall*` test and the four new ones.

- [ ] **Step 5: Run the whole suite**

Run: `go test ./... -v`
Expected: PASS everywhere.

- [ ] **Step 6: Lint and commit**

Run: `make lint`

```bash
git add internal/cli/install.go internal/cli/install_verify_test.go
git commit -m "feat(install): verify OCI signatures with --verify-key"
```

---

## Task 7: README updates

**Files:**
- Modify: `README.md`

**Interfaces:** none — documentation only.

- [ ] **Step 1: Update the Features bullet**

Replace the existing "Package skills into a container image" bullet (currently lines 89–93):

```markdown
- **Package skills into a container image.** `skillsctl package <source-dir> <oci-ref>`
  bundles a directory of skills into an OCI artifact and pushes it to any
  registry `docker` can reach; `skillsctl install oci://registry/repo:tag`
  installs from one, and `outdated`/`update` follow a moved tag the same way
  they follow a moved git ref. `package --sign-key <path>` signs the pushed
  image with cosign, and `install --verify-key <path>` verifies it before
  installing.
```

- [ ] **Step 2: Add Use examples**

After the existing two lines (currently lines 129–130), add:

```
skillsctl install oci://ghcr.io/owner/skills:v1    # from a packaged OCI artifact
skillsctl package ./my-skills ghcr.io/owner/skills:v1  # push a directory of skills as one
skillsctl package ./my-skills ghcr.io/owner/skills:v1 --sign-key cosign.key  # ...and sign it
skillsctl install oci://ghcr.io/owner/skills:v1 --verify-key cosign.pub  # verify before installing
```

- [ ] **Step 3: Update the Commands table**

Replace the two existing rows (currently lines 370–371):

```markdown
| `install oci://<ref>` | `--skill`, `--all`, `-a/--agent`, `--ref`, `--as`, `--pin`, `--verify-key`, `--dry-run` | Install one or more skills from an OCI artifact |
| `package <source-dir> <oci-ref>` | `--sign-key`, `--dry-run` | Package a directory of skills into an OCI artifact and push it |
```

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m "docs: document --sign-key and --verify-key"
```

---

## Task 8: Full verification pass

**Files:** none — this task runs the repo's own definition of done and does a manual smoke test.

- [ ] **Step 1: Run the full automated gate**

Run: `make test && make lint && make tidy-check`
Expected: all three pass. `make tidy-check` passing confirms no new Go dependency was introduced, per the Global Constraints.

- [ ] **Step 2: Manual smoke test (requires a local `cosign` install and a local registry)**

```bash
cosign generate-key-pair          # produces cosign.key / cosign.pub, prompts for a password
export COSIGN_PASSWORD=<the password you just chose>
go run ./cmd/skillsctl package ./some-skills-dir localhost:5000/skills:v1 --sign-key cosign.key
go run ./cmd/skillsctl install oci://localhost:5000/skills:v1 --verify-key cosign.pub --all
```

Expected: the install succeeds and prints no warning. Then verify the fail-closed and warn paths:

```bash
go run ./cmd/skillsctl package ./some-skills-dir localhost:5000/skills:v2   # push WITHOUT signing
go run ./cmd/skillsctl install oci://localhost:5000/skills:v2 --verify-key cosign.pub --all
```

Expected: this second install fails with a "refusing to install" error (an unsigned image cannot be verified). Then:

```bash
go run ./cmd/skillsctl install oci://localhost:5000/skills:v1 --all   # no --verify-key, image IS signed
```

Expected: this install succeeds but prints a `warning: ... is signed but was not verified` line.

If `cosign tree`'s real output does not contain the literal `🔐 Signatures` substring on the cosign version installed locally (this was flagged as an open item in the design spec), adjust `signedMarker` in `internal/cosignx/cosignx.go` to match, re-run `go test ./internal/cosignx/... -v`, and amend the Task 1 commit with a `fix(cosignx):` follow-up commit rather than rewriting history.

- [ ] **Step 3: Final commit if Step 2 required a fix**

```bash
git add internal/cosignx/cosignx.go internal/cosignx/cosignx_test.go
git commit -m "fix(cosignx): match cosign tree's actual signature marker"
```

(Skip this step entirely if Step 2 needed no change.)
