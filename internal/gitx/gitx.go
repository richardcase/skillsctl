// Package gitx wraps the git binary. Shelling out rather than using a Go git
// library is deliberate: SSH keys, credential helpers and proxies then work
// with no code on our side.
package gitx

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Git is the set of git operations skillsctl needs.
type Git interface {
	// Resolve returns the commit sha for ref (empty ref means HEAD).
	Resolve(ctx context.Context, repoURL, ref string) (string, error)
	// Mirror creates or updates a bare mirror of repoURL at mirrorPath.
	Mirror(ctx context.Context, repoURL, mirrorPath string) error
	// Extract writes the tree at sha into dest, without a .git directory.
	Extract(ctx context.Context, mirrorPath, sha, dest string) error
}

// CLI implements Git using the git binary.
type CLI struct{ Bin string }

// New returns a CLI backed by git on PATH.
func New() *CLI { return &CLI{Bin: "git"} }

var shaRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Resolve returns the commit sha for ref (empty ref means HEAD).
func (c *CLI) Resolve(ctx context.Context, repoURL, ref string) (string, error) {
	if shaRe.MatchString(ref) {
		return ref, nil
	}
	query := ref
	if query == "" {
		query = "HEAD"
	}

	out, err := c.output(ctx, "", "ls-remote", repoURL, query)
	if err != nil {
		return "", err
	}

	// Prefer the dereferenced line for annotated tags: refs/tags/v1^{}.
	var fallback string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if strings.HasSuffix(fields[1], "^{}") {
			return fields[0], nil
		}
		if fallback == "" {
			fallback = fields[0]
		}
	}
	if fallback == "" {
		return "", fmt.Errorf("ref %q not found in %s", query, repoURL)
	}
	return fallback, nil
}

// Mirror creates or updates a bare mirror of repoURL at mirrorPath.
func (c *CLI) Mirror(ctx context.Context, repoURL, mirrorPath string) error {
	if _, err := os.Stat(filepath.Join(mirrorPath, "HEAD")); err == nil {
		// Re-point origin before fetching. The mirror is a cache keyed by a
		// derived slug, so a stale or colliding entry must not silently serve
		// another repository's objects — and this also handles a repo whose
		// URL legitimately changed (ssh <-> https).
		if _, err := c.output(ctx, mirrorPath, "remote", "set-url", "origin", repoURL); err != nil {
			return err
		}
		_, err := c.output(ctx, mirrorPath, "fetch", "--prune", "--tags", "origin")
		return err
	}

	if err := os.MkdirAll(filepath.Dir(mirrorPath), 0o755); err != nil {
		return fmt.Errorf("create mirror directory: %w", err)
	}
	_, err := c.output(ctx, "", "clone", "--mirror", "--quiet", repoURL, mirrorPath)
	return err
}

// Extract writes the tree at sha into dest, without a .git directory.
func (c *CLI) Extract(ctx context.Context, mirrorPath, sha, dest string) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("create destination: %w", err)
	}

	cmd := exec.CommandContext(ctx, c.Bin, "-C", mirrorPath, "archive", "--format=tar", sha)
	cmd.Env = env()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("pipe git archive: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start git archive: %w", err)
	}

	untarErr := untar(stdout, dest)
	if untarErr != nil {
		// Stop git before draining: calling Wait with an undrained pipe
		// deadlocks, because git blocks writing into a full pipe buffer
		// while we block waiting for it to exit.
		_ = cmd.Process.Kill()
	}
	// On the success path untar has already read to EOF, so this is a no-op.
	_, _ = io.Copy(io.Discard, stdout)

	waitErr := cmd.Wait()
	if untarErr != nil {
		return fmt.Errorf("extract %s: %w", sha, untarErr)
	}
	if waitErr != nil {
		return fmt.Errorf("git archive %s: %w: %s", sha, waitErr, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (c *CLI) output(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, c.Bin, args...)
	cmd.Dir = dir
	cmd.Env = env()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// env keeps git non-interactive so a missing credential never hangs the CLI.
func env() []string {
	return append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ADVICE=0",
	)
}
