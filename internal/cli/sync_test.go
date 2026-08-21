package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/testrepo"
)

// writeManifest puts a manifest in a temp file and returns its path.
func writeManifest(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "skills.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The issue's own test: install, bundle, wipe everything, sync, and get the
// same links and the same receipts back.
func TestBundleSyncRoundTrip(t *testing.T) {
	h := newHarness(t)
	url, sha := testrepo.New(t, map[string]string{
		"a/SKILL.md": "---\nname: a\ndescription: A\n---\n",
		"b/SKILL.md": "---\nname: b\ndescription: B\n---\n",
	})

	if out, err := h.run(t, "install", url, "--all"); err != nil {
		t.Fatalf("install --all: %v\n%s", err, out)
	}
	before := h.receipts(t)
	if len(before) != 2 {
		t.Fatalf("want 2 receipts before the round trip, got %d", len(before))
	}

	stdout, _, err := h.runSplit(t, "bundle")
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	path := writeManifest(t, stdout)

	// Wipe the store and both agent directories: this is the other machine.
	if err := os.RemoveAll(h.root); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{h.claude, h.codex} {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
	}

	out, err := h.run(t, "sync", path)
	if err != nil {
		t.Fatalf("sync: %v\n%s", err, out)
	}

	for _, name := range []string{"a", "b"} {
		for agent, dir := range map[string]string{"claude": h.claude, "codex": h.codex} {
			dest, rerr := os.Readlink(filepath.Join(dir, name))
			if rerr != nil {
				t.Fatalf("%s link for %s missing after sync: %v", agent, name, rerr)
			}
			if !strings.Contains(dest, sha) {
				t.Errorf("%s link for %s points at %q, want the bundled sha", agent, name, dest)
			}
		}
	}

	after := h.receipts(t)
	if len(after) != len(before) {
		t.Fatalf("got %d receipts after sync, want %d", len(after), len(before))
	}
	for name, was := range before {
		is, ok := after[name]
		if !ok {
			t.Fatalf("%s was not reinstalled", name)
		}
		// Everything the manifest carries has to come back identical. RevPath,
		// contentHash and the timestamps are deliberately not compared: they are
		// what the manifest drops.
		for _, field := range []string{"source", "subpath", "ref", "resolved", "channel", "pinned"} {
			if was[field] != is[field] {
				t.Errorf("%s.%s = %v after sync, was %v", name, field, is[field], was[field])
			}
		}
	}
}

func TestSyncFromARemoteProfileRepo(t *testing.T) {
	h := newHarness(t)
	skillURL, skillSHA := testrepo.New(t, map[string]string{"SKILL.md": skillMD})
	manifestBody := "version = 1\n\n[[skill]]\nname = 'demo-skill'\nsource = '" + skillURL + "'\n"
	profileURL, _ := testrepo.New(t, map[string]string{"skills.toml": manifestBody})

	out, err := h.run(t, "sync", profileURL)
	if err != nil {
		t.Fatalf("sync %s: %v\n%s", profileURL, err, out)
	}

	for _, dir := range []string{h.claude, h.codex} {
		dest, rerr := os.Readlink(filepath.Join(dir, "demo-skill"))
		if rerr != nil {
			t.Fatalf("link missing after remote sync: %v", rerr)
		}
		if !strings.Contains(dest, skillSHA) {
			t.Errorf("link points at %q, want the fetched sha %s", dest, skillSHA)
		}
	}
}

func TestSyncRefSelectsTheProfileReposBranch(t *testing.T) {
	h := newHarness(t)
	profileURL, sha1 := testrepo.New(t, map[string]string{"skills.toml": "version = 1\n"})
	dir := testrepo.Dir(profileURL)
	skillURL, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})
	testrepo.Commit(t, dir, map[string]string{
		"skills.toml": "version = 1\n\n[[skill]]\nname = 'demo-skill'\nsource = '" + skillURL + "'\n",
	})

	out, err := h.run(t, "sync", profileURL, "--ref", sha1)
	if err != nil {
		t.Fatalf("sync --ref %s: %v\n%s", sha1, err, out)
	}
	if !strings.Contains(out, "Everything the manifest names is already installed.") {
		t.Errorf("syncing the old sha should have found an empty manifest, got:\n%s", out)
	}
}

// A relative filename that also happens to look like an owner/repo source
// still reads as a local file: os.Stat is tried first and always wins.
func TestSyncPrefersALocalFileEvenWhenItsNameLooksLikeASource(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll(filepath.Join(dir, "owner"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "owner", "repo"), []byte("version = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := h.run(t, "sync", "owner/repo")
	if err != nil {
		t.Fatalf("sync owner/repo: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Everything the manifest names is already installed.") {
		t.Errorf("want the local (empty) manifest read, not a remote fetch attempted, got:\n%s", out)
	}
}

// A missing local path that does not even parse as a git source (no slash,
// so it matches none of owner/repo, scp, or plugin shape) must fail fast with
// the plain "no such file" error rather than attempting a remote fetch. This
// is the case source.Parse can actually tell apart from a real git shorthand;
// a typo that happens to contain a slash is syntactically indistinguishable
// from a real owner/repo source and still falls through to FetchRemote, which
// then fails with git's own error instead.
func TestSyncReportsMissingFileRatherThanGuessingRemote(t *testing.T) {
	h := newHarness(t)

	out, err := h.run(t, "sync", "nope-does-not-exist.toml")
	if err == nil {
		t.Fatalf("want an error, got success:\n%s", out)
	}
	if !strings.Contains(err.Error(), "no such file") {
		t.Errorf("want a \"no such file\" error, got: %v", err)
	}
	if strings.Contains(err.Error(), "ls-remote") || strings.Contains(err.Error(), "resolve") {
		t.Errorf("want no sign of a remote fetch attempt, got: %v", err)
	}
}

func TestSyncIsIdempotent(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	if out, err := h.run(t, "install", url); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	stdout, _, err := h.runSplit(t, "bundle")
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	path := writeManifest(t, stdout)

	first, err := h.run(t, "sync", path)
	if err != nil {
		t.Fatalf("first sync: %v\n%s", err, first)
	}
	second, err := h.run(t, "sync", path)
	if err != nil {
		t.Fatalf("second sync: %v\n%s", err, second)
	}
	if !strings.Contains(second, "already installed") {
		t.Errorf("a second sync should say there was nothing to do:\n%s", second)
	}
}

func TestSyncCarriesAPinAcross(t *testing.T) {
	h := newHarness(t)
	url, sha := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	if out, err := h.run(t, "install", url, "--pin"); err != nil {
		t.Fatalf("install --pin: %v\n%s", err, out)
	}
	stdout, _, err := h.runSplit(t, "bundle")
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	path := writeManifest(t, stdout)

	if err := os.RemoveAll(h.root); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{h.claude, h.codex} {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
	}

	if out, serr := h.run(t, "sync", path); serr != nil {
		t.Fatalf("sync: %v\n%s", serr, out)
	}

	got := h.receipts(t)["demo-skill"]
	if got["pinned"] != true {
		t.Errorf("the pin was lost: %+v", got)
	}
	if got["resolved"] != sha {
		t.Errorf("resolved = %v, want the pinned sha %s", got["resolved"], sha)
	}
}

func TestSyncReportsADifferenceWithoutChangingIt(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	if out, err := h.run(t, "install", url); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	path := writeManifest(t, "version = 1\n\n[[skill]]\nname = 'demo-skill'\nsource = '"+url+"'\nref = 'develop'\n")

	code, out := exitCode(t, "sync", path)
	if code != ExitPartial {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitPartial, out)
	}
	if !strings.Contains(out, "differs") || !strings.Contains(out, "develop") {
		t.Errorf("the difference should be reported and named:\n%s", out)
	}
	if !strings.Contains(out, "remove it and run sync again") {
		t.Errorf("the report should name the remedy:\n%s", out)
	}
	// Nothing moved: the receipt still tracks what it tracked.
	if got := h.receipts(t)["demo-skill"]["ref"]; got == "develop" {
		t.Error("sync re-pointed a ref, and it only ever adds")
	}
}

// A skill the manifest never names is information, not a failure.
func TestSyncReportsExtrasWithoutFailing(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	if out, err := h.run(t, "install", url); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	path := writeManifest(t, "version = 1\n")

	code, out := exitCode(t, "sync", path)
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d — an extra is not a failure\n%s", code, ExitOK, out)
	}
	if !strings.Contains(out, "not in the manifest") || !strings.Contains(out, "demo-skill") {
		t.Errorf("the extra should be named:\n%s", out)
	}
	if _, err := os.Lstat(filepath.Join(h.claude, "demo-skill")); err != nil {
		t.Error("sync removed a skill the manifest did not name, and it never removes anything")
	}
}

func TestSyncLinksAMissingAgent(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	if out, err := h.run(t, "install", url, "-a", "claude"); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	path := writeManifest(t, "version = 1\n\n[[skill]]\nname = 'demo-skill'\nsource = '"+url+
		"'\nagents = ['claude', 'codex']\n")

	out, err := h.run(t, "sync", path)
	if err != nil {
		t.Fatalf("sync: %v\n%s", err, out)
	}
	if !strings.Contains(out, "linked demo-skill into codex") {
		t.Errorf("want the new link reported:\n%s", out)
	}
	if _, lerr := os.Lstat(filepath.Join(h.codex, "demo-skill")); lerr != nil {
		t.Errorf("codex link missing after sync: %v", lerr)
	}
}

func TestSyncDryRunChangesNothing(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})
	path := writeManifest(t, "version = 1\n\n[[skill]]\nname = 'demo-skill'\nsource = '"+url+"'\n")

	out, err := h.run(t, "sync", path, "--dry-run")
	if err != nil {
		t.Fatalf("sync --dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "link") {
		t.Errorf("the dry run should describe the link ops:\n%s", out)
	}
	if _, lerr := os.Lstat(filepath.Join(h.claude, "demo-skill")); !os.IsNotExist(lerr) {
		t.Error("the dry run created a symlink")
	}
	if _, serr := os.Stat(filepath.Join(h.root, "state.json")); !os.IsNotExist(serr) {
		t.Error("the dry run wrote the receipts database")
	}
}

// sync's per-entry resilience is a plan-time property: manifest.Plan keeps
// going after an entry that cannot be planned. Applying the plan is not
// per-entry — plan.Executor rolls back everything on the first failed op, and
// sync only commits after Apply succeeds — so one occupied link path aborts
// the whole run, including the entries that would otherwise have gone in
// cleanly. This test pins that behaviour rather than asking for it to change;
// see the design doc's "Deliberately not in scope" section.
func TestSyncIsAllOrNothingWhenALinkPathIsOccupied(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{
		"a/SKILL.md": "---\nname: a\ndescription: A\n---\n",
		"b/SKILL.md": "---\nname: b\ndescription: B\n---\n",
	})
	path := writeManifest(t, "version = 1\n\n"+
		"[[skill]]\nname = 'a'\nsource = '"+url+"//a'\n\n"+
		"[[skill]]\nname = 'b'\nsource = '"+url+"//b'\n")

	// A real directory, not a symlink, at the path entry b would occupy in
	// claude — the way something other than skillsctl might already have put
	// something there.
	occupied := filepath.Join(h.claude, "b")
	if err := os.MkdirAll(occupied, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := h.run(t, "sync", path)
	if err == nil {
		t.Fatal("sync succeeded despite an occupied link path; want it to fail")
	}

	for _, name := range []string{"a", "b"} {
		for _, dir := range []string{h.claude, h.codex} {
			if _, lerr := os.Readlink(filepath.Join(dir, name)); lerr == nil {
				t.Errorf("%s in %s is a symlink after a failed sync; want the rollback to have undone it", name, dir)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(h.root, "state.json")); !os.IsNotExist(err) {
		t.Error("sync committed receipts despite failing: want nothing written")
	}
}

func TestSyncOnAnUnreadableFile(t *testing.T) {
	h := newHarness(t)
	if _, err := h.run(t, "sync", filepath.Join(t.TempDir(), "nope.toml")); err == nil {
		t.Fatal("sync accepted a file that does not exist")
	}
}

func TestSyncOnAManifestFromTheFuture(t *testing.T) {
	h := newHarness(t)
	path := writeManifest(t, "version = 99\n")

	_, err := h.run(t, "sync", path)
	if err == nil {
		t.Fatal("sync accepted a manifest version it cannot understand")
	}
	if !strings.Contains(err.Error(), "upgrade skillsctl") {
		t.Errorf("the error should name the remedy, got: %v", err)
	}
}

// Nothing applied and something asked for is a failure, not a partial result.
func TestSyncExitsErrorWhenNothingCouldBeApplied(t *testing.T) {
	newHarness(t)
	path := writeManifest(t, "version = 1\n\n[[skill]]\nname = 'demo-skill'\nsource = 'file:///nonexistent/repo.git'\n")

	code, out := exitCode(t, "sync", path)
	if code != ExitError {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitError, out)
	}
}

// A difference is not a failure to apply: the skill is installed, just not as
// the manifest describes it. Mixed with a genuine failure and nothing else
// applied, the run has still accomplished nothing — that combination is exit 1.
func TestSyncExitsErrorWhenNothingAppliesAndOneEntryFails(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	if out, err := h.run(t, "install", url); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	path := writeManifest(t, "version = 1\n\n"+
		"[[skill]]\nname = 'demo-skill'\nsource = '"+url+"'\nref = 'develop'\n\n"+
		"[[skill]]\nname = 'missing'\nsource = 'file:///nonexistent/repo.git'\n")

	code, out := exitCode(t, "sync", path)
	if code != ExitError {
		t.Fatalf("exit = %d, want %d — a difference plus a failure, and nothing applied\n%s", code, ExitError, out)
	}
}

// The plugin channel cannot name the version it installed until claude has
// run, so the report has to reflect what settleSynced read back rather than
// what the plan could see before it ran.
func TestSyncReportsAPluginVersionOnceItIsKnown(t *testing.T) {
	h := newHarness(t)
	h.plugins.next = "6.3.0"
	path := writeManifest(t, "version = 1\n\n[[skill]]\nname = 'superpowers'\nsource = '"+pluginID+"'\n")

	out, err := h.run(t, "sync", path)
	if err != nil {
		t.Fatalf("sync: %v\n%s", err, out)
	}
	if !strings.Contains(out, "6.3.0") {
		t.Errorf("output = %q, want the version claude settled on, not an empty one", out)
	}
	if strings.Contains(out, "@ into") || strings.Contains(out, "@  into") {
		t.Errorf("output = %q, want no version-shaped gap where the version belongs", out)
	}
	if got := h.receipts(t)["superpowers"]["resolved"]; got != "6.3.0" {
		t.Errorf("resolved = %v, want the version read back from claude", got)
	}
	// codex does not install plugins in this harness's config, so it never
	// received the skill. Naming it would claim a link that does not exist.
	if strings.Contains(out, "codex") {
		t.Errorf("output = %q, want only the agents that actually received the plugin", out)
	}
}
