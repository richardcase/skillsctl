// Package claudex wraps the claude binary, the way gitx wraps git and for the
// same reason: Claude Code owns its plugin state, and the layout of
// ~/.claude/plugins is not ours to depend on. Asking the CLI is the only
// contract there is.
//
// Everything here reads. Installing, updating and uninstalling are plan.Exec
// ops built from the argv helpers below, so a --dry-run prints the command that
// will actually run rather than a description of it.
package claudex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ErrNotFound reports that the claude binary is not on PATH. The plugin
// channel is the only thing that needs it, so the message says which of the
// two ways out the user has.
var ErrNotFound = errors.New(
	"claude was not found on PATH: the plugin channel installs through Claude Code's CLI, " +
		"so install claude or use a git source instead")

// Installed is one entry of `claude plugin list --json`.
type Installed struct {
	ID          string `json:"id"` // plugin@marketplace
	Version     string `json:"version"`
	Scope       string `json:"scope"`
	Enabled     bool   `json:"enabled"`
	InstallPath string `json:"installPath"`
}

// Plugins is the set of claude plugin operations skillsctl needs.
type Plugins interface {
	// List returns the plugins claude has installed.
	List(ctx context.Context) ([]Installed, error)
	// InstallArgv, UninstallArgv and UpdateArgv build the argv for a plan.Exec
	// op. They live here so the command line exists in one place and a plan
	// can print it verbatim.
	InstallArgv(id string) []string
	UninstallArgv(id string) []string
	UpdateArgv(id string) []string
}

// Scope is the installation scope claude installs into. Only user scope is
// used today; project and local pair with the --project flag, which is not
// built yet.
const Scope = "user"

// CLI implements Plugins using the claude binary.
type CLI struct {
	Bin string
	// output runs the binary and returns its stdout. Tests replace it, which
	// is what keeps a unit test from touching the developer's own plugins.
	output func(ctx context.Context, args ...string) (string, error)
}

// New returns a CLI backed by claude on PATH.
func New() *CLI {
	c := &CLI{Bin: "claude"}
	c.output = c.run
	return c
}

// InstallArgv is the command that installs id.
func (c *CLI) InstallArgv(id string) []string {
	return []string{c.Bin, "plugin", "install", id, "--scope", Scope}
}

// UninstallArgv is the command that removes id. --prune is deliberately not
// passed: reclaiming a dependency no skillsctl receipt mentions is the user's
// decision to make with `claude plugin prune`, not a side effect of a remove.
func (c *CLI) UninstallArgv(id string) []string {
	return []string{c.Bin, "plugin", "uninstall", id, "--scope", Scope}
}

// UpdateArgv is the command that moves id to the latest version.
func (c *CLI) UpdateArgv(id string) []string {
	return []string{c.Bin, "plugin", "update", id, "--scope", Scope}
}

// listOutput is the shape `claude plugin list --json` returns. It is a bare
// array today, and an object with an "installed" key when --available is
// passed. Both are decoded so that a future release which settles on the
// object form does not break the plugin channel.
type listOutput struct {
	Installed []Installed `json:"installed"`
}

// List returns the plugins claude has installed.
func (c *CLI) List(ctx context.Context) ([]Installed, error) {
	out, err := c.output(ctx, "plugin", "list", "--json")
	if err != nil {
		return nil, fmt.Errorf("list plugins: %w", err)
	}
	return parseList([]byte(out))
}

func parseList(blob []byte) ([]Installed, error) {
	trimmed := bytes.TrimSpace(blob)
	if len(trimmed) == 0 {
		return nil, nil
	}

	if trimmed[0] == '[' {
		var installed []Installed
		if err := json.Unmarshal(trimmed, &installed); err != nil {
			return nil, fmt.Errorf("decode plugin list: %w", err)
		}
		return installed, nil
	}

	var wrapped listOutput
	if err := json.Unmarshal(trimmed, &wrapped); err != nil {
		return nil, fmt.Errorf("decode plugin list: %w", err)
	}
	return wrapped.Installed, nil
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
		return "", fmt.Errorf("claude %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
