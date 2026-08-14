package claudex

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// bare is what `claude plugin list --json` returns today.
const bare = `[
  {
    "id": "superpowers@claude-plugins-official",
    "version": "6.3.0",
    "scope": "user",
    "enabled": true,
    "installPath": "/home/u/.claude/plugins/cache/claude-plugins-official/superpowers/6.3.0"
  }
]`

// wrapped is the shape --available returns, decoded so that a release which
// settles on it does not break the plugin channel.
const wrapped = `{"installed": [{"id": "a@m", "version": "1.0.0"}], "available": [{"pluginId": "b@m"}]}`

func fake(out string, err error) *CLI {
	c := &CLI{Bin: "claude"}
	c.output = func(context.Context, ...string) (string, error) { return out, err }
	return c
}

func TestListReadsVersionAndInstallPath(t *testing.T) {
	got, err := fake(bare, nil).List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("List returned %d plugins, want 1", len(got))
	}
	if got[0].ID != "superpowers@claude-plugins-official" {
		t.Errorf("ID = %q, want the plugin@marketplace id", got[0].ID)
	}
	if got[0].Version != "6.3.0" {
		t.Errorf("Version = %q, want 6.3.0: it is what the receipt records as Resolved", got[0].Version)
	}
	if !strings.HasSuffix(got[0].InstallPath, "superpowers/6.3.0") {
		t.Errorf("InstallPath = %q, want the directory claude installed into", got[0].InstallPath)
	}
}

func TestListAcceptsTheWrappedShape(t *testing.T) {
	got, err := fake(wrapped, nil).List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].ID != "a@m" {
		t.Errorf("List = %+v, want the installed plugins only", got)
	}
}

func TestListTreatsNoOutputAsNoPlugins(t *testing.T) {
	for _, out := range []string{"", "  \n", "[]"} {
		got, err := fake(out, nil).List(context.Background())
		if err != nil {
			t.Fatalf("List(%q): %v", out, err)
		}
		if len(got) != 0 {
			t.Errorf("List(%q) = %+v, want none", out, got)
		}
	}
}

func TestListRejectsOutputItCannotDecode(t *testing.T) {
	_, err := fake("not json", nil).List(context.Background())
	if err == nil {
		t.Fatal("List accepted output it could not decode")
	}
	if !strings.Contains(err.Error(), "decode plugin list") {
		t.Errorf("error = %v, want it to name the operation", err)
	}
}

func TestListSurfacesAMissingBinary(t *testing.T) {
	_, err := fake("", ErrNotFound).List(context.Background())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want it to wrap ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "git source") {
		t.Errorf("error = %v, want it to name the way out", err)
	}
}

func TestRunReportsAMissingBinaryRatherThanAnExecFailure(t *testing.T) {
	c := &CLI{Bin: "claude-that-is-not-installed"}
	c.output = c.run

	if _, err := c.List(context.Background()); !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestArgvNamesTheScopeItInstallsInto(t *testing.T) {
	c := New()
	for _, tc := range []struct {
		name string
		got  []string
		verb string
	}{
		{name: "install", got: c.InstallArgv("p@m"), verb: "install"},
		{name: "uninstall", got: c.UninstallArgv("p@m"), verb: "uninstall"},
		{name: "update", got: c.UpdateArgv("p@m"), verb: "update"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := "claude plugin " + tc.verb + " p@m --scope user"
			if got := strings.Join(tc.got, " "); got != want {
				t.Errorf("argv = %q, want %q", got, want)
			}
		})
	}
}

// --prune would reclaim dependencies no receipt mentions, which is a decision
// for the user rather than a side effect of removing one skill.
func TestUninstallDoesNotPrune(t *testing.T) {
	for _, arg := range New().UninstallArgv("p@m") {
		if arg == "--prune" {
			t.Error("uninstall must not pass --prune")
		}
	}
}
