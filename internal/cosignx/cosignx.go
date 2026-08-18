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
