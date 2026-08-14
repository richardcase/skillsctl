//go:build manual

// This file is the one test that really runs `claude plugin install` and
// `claude plugin uninstall`. Everything else in the suite goes through a fake,
// because a unit test must never touch the plugins of the machine it runs on.
//
// Run it with `make test-manual`. CI does not.
package channel

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/richardcase/skillsctl/internal/claudex"
	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/target"
)

// manualPlugin is the plugin this test installs and removes. Override it with
// SKILLSCTL_MANUAL_PLUGIN to try another marketplace.
const manualPlugin = "swift-lsp@claude-plugins-official"

// TestManualPluginInstallAndUninstall walks install, read-back and uninstall
// against the actual binary, which is the only way to find out that the argv we
// build is the argv claude accepts.
//
// It skips rather than runs when the plugin is already installed: the point is
// to leave the machine as it was found, and it cannot do that if it did not do
// the installing.
func TestManualPluginInstallAndUninstall(t *testing.T) {
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude is not on PATH")
	}

	id := manualPlugin
	if env := os.Getenv("SKILLSCTL_MANUAL_PLUGIN"); env != "" {
		id = env
	}

	ctx := context.Background()
	cli := claudex.New()

	before, err := cli.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if _, ok := find(before, id); ok {
		t.Skipf("%s is already installed; this test will not touch a plugin it did not install", id)
	}

	cfg := target.Config{Targets: []target.Target{{Name: "claude", Plugins: true}}}
	ch := NewPlugin(cli, cfg)

	// The real runner, which is what the CLI would use.
	ex := &plan.Executor{DB: &state.DB{Receipts: map[string]*state.Receipt{}}, Out: os.Stderr}

	t.Cleanup(func() {
		p, _ := ch.Remove(state.Receipt{Name: "manual", Source: id}, nil)
		if err := ex.Apply(ctx, p); err != nil {
			t.Errorf("cleanup uninstall failed, %s may still be installed: %v", id, err)
		}
	})

	install := plan.Plan{}
	install.Add(plan.Exec{Argv: cli.InstallArgv(id)})
	if err := ex.Apply(ctx, install); err != nil {
		t.Fatalf("install %s: %v", id, err)
	}

	changed, err := ch.Settle(ctx, []state.Receipt{{Name: "manual", Source: id}})
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if len(changed) != 1 {
		t.Fatalf("Settle returned %d receipts, want the one it completed", len(changed))
	}
	if changed[0].Resolved == "" {
		t.Error("Resolved is empty: claude reported no version for a plugin it just installed")
	}
	if changed[0].RevPath == "" {
		t.Error("RevPath is empty: claude reported no install path")
	}
	if _, err := os.Stat(changed[0].RevPath); err != nil {
		t.Errorf("install path %q does not exist: %v", changed[0].RevPath, err)
	}
}
