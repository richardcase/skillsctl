package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/testrepo"
)

// handLink puts a symlink in an agent's skills directory the way somebody
// installing a skill by hand does: no receipt, no store, just a link.
func handLink(t *testing.T, skillsDir, name, dest string) string {
	t.Helper()
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(skillsDir, name)
	if err := os.Symlink(dest, link); err != nil {
		t.Fatal(err)
	}
	return link
}

func TestAdoptRecordsAHandMadeSymlink(t *testing.T) {
	h := newHarness(t)
	dir := localDir(t, nil)
	link := handLink(t, h.claude, "my-skill", dir)

	out, err := h.run(t, "adopt")
	if err != nil {
		t.Fatalf("adopt: %v\n%s", err, out)
	}
	if !strings.Contains(out, "adopted 1 skill") {
		t.Errorf("output = %q, want it to say what it adopted", out)
	}

	r := h.receipts(t)["my-skill"]
	if r == nil {
		t.Fatalf("no receipt for my-skill: %v", h.receipts(t))
	}
	if r["channel"] != "local" {
		t.Errorf("channel = %v, want local", r["channel"])
	}
	if r["source"] != dir {
		t.Errorf("source = %v, want %s", r["source"], dir)
	}
	if r["revPath"] != dir {
		t.Errorf("revPath = %v, want %s", r["revPath"], dir)
	}

	links, _ := r["links"].([]any)
	if len(links) != 1 {
		t.Fatalf("links = %v, want the one symlink that was already there", r["links"])
	}
	got, _ := links[0].(map[string]any)
	if got["path"] != link || got["target"] != "claude" {
		t.Errorf("link = %v, want %s in claude", got, link)
	}

	// The symlink is untouched: adopt records, it does not relink.
	dest, err := os.Readlink(link)
	if err != nil || dest != dir {
		t.Errorf("readlink = %q, %v; want %s untouched", dest, err, dir)
	}
}

func TestAdoptWritesTheSameReceiptAsLink(t *testing.T) {
	linked := newHarness(t)
	dir := localDir(t, nil)
	if out, err := linked.run(t, "link", dir); err != nil {
		t.Fatalf("link: %v\n%s", err, out)
	}
	want := linked.receipts(t)["my-skill"]

	adopted := newHarness(t)
	handLink(t, adopted.claude, "my-skill", dir)
	if out, err := adopted.run(t, "adopt"); err != nil {
		t.Fatalf("adopt: %v\n%s", err, out)
	}
	got := adopted.receipts(t)["my-skill"]

	// Timestamps and the link path differ by harness; everything that says
	// what the skill *is* must not.
	for _, field := range []string{"channel", "source", "revPath", "slug", "resolved", "contentHash", "ref", "pinned"} {
		if got[field] != want[field] {
			t.Errorf("%s: adopt recorded %v, link recorded %v", field, got[field], want[field])
		}
	}
}

func TestAdoptDryRunChangesNothing(t *testing.T) {
	h := newHarness(t)
	dir := localDir(t, nil)
	handLink(t, h.claude, "my-skill", dir)

	out, err := h.run(t, "adopt", "--dry-run")
	if err != nil {
		t.Fatalf("adopt --dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "record") || !strings.Contains(out, "would adopt 1 skill") {
		t.Errorf("output = %q, want the plan and a would-adopt summary", out)
	}
	if _, err := os.Stat(filepath.Join(h.root, "state.json")); !os.IsNotExist(err) {
		t.Error("dry run wrote the receipts database")
	}
}

// The safety property the spec asks to be checked by hand before adopt is ever
// run for real. It is asserted here so it cannot regress.
func TestAdoptPlansNothingDestructive(t *testing.T) {
	h := newHarness(t)
	handLink(t, h.claude, "my-skill", localDir(t, nil))
	handLink(t, h.codex, "other", localDir(t, map[string]string{"SKILL.md": "---\nname: other\n---\n"}))

	out, err := h.run(t, "adopt", "--dry-run")
	if err != nil {
		t.Fatalf("adopt --dry-run: %v\n%s", err, out)
	}

	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.HasPrefix(line, "would adopt") {
			continue
		}
		if !strings.HasPrefix(line, "record") {
			t.Errorf("plan line %q is not a record: adopt must plan nothing else", line)
		}
	}
	if len(h.ran) != 0 {
		t.Errorf("adopt shelled out: %v", h.ran)
	}
}

func TestAdoptSkipsARealDirectory(t *testing.T) {
	h := newHarness(t)
	dir := filepath.Join(h.claude, "my-skill")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(localMD), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out := exitCode(t, "adopt")

	if code != ExitError {
		t.Errorf("exit = %d, want %d when nothing could be adopted", code, ExitError)
	}
	if !strings.Contains(out, "not a symlink") || !strings.Contains(out, "skillsctl link") {
		t.Errorf("output = %q, want the reason and the remedy", out)
	}
	// The directory is still there, whole.
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
		t.Errorf("adopt disturbed a real directory: %v", err)
	}
}

func TestAdoptIsIdempotent(t *testing.T) {
	h := newHarness(t)
	handLink(t, h.claude, "my-skill", localDir(t, nil))

	if out, err := h.run(t, "adopt"); err != nil {
		t.Fatalf("first adopt: %v\n%s", err, out)
	}
	before := h.receipts(t)["my-skill"]["updatedAt"]

	out, err := h.run(t, "adopt")
	if err != nil {
		t.Fatalf("second adopt: %v\n%s", err, out)
	}
	if !strings.Contains(out, "1 already managed") {
		t.Errorf("output = %q, want the second run to report it as managed", out)
	}
	if after := h.receipts(t)["my-skill"]["updatedAt"]; after != before {
		t.Errorf("the receipt was rewritten: %v -> %v", before, after)
	}
}

func TestRemoveAfterAdoptLeavesTheSourceDirectory(t *testing.T) {
	h := newHarness(t)
	dir := localDir(t, nil)
	link := handLink(t, h.claude, "my-skill", dir)

	if out, err := h.run(t, "adopt"); err != nil {
		t.Fatalf("adopt: %v\n%s", err, out)
	}
	if out, err := h.run(t, "remove", "my-skill"); err != nil {
		t.Fatalf("remove: %v\n%s", err, out)
	}

	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Errorf("the symlink survived remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
		t.Errorf("remove took the source directory with it: %v", err)
	}
	if _, ok := h.receipts(t)["my-skill"]; ok {
		t.Error("the receipt outlived the removal")
	}
}

func TestAdoptMergesOneSkillLinkedIntoTwoAgents(t *testing.T) {
	h := newHarness(t)
	dir := localDir(t, nil)
	handLink(t, h.claude, "my-skill", dir)
	handLink(t, h.codex, "my-skill", dir)

	if out, err := h.run(t, "adopt"); err != nil {
		t.Fatalf("adopt: %v\n%s", err, out)
	}

	links, _ := h.receipts(t)["my-skill"]["links"].([]any)
	if len(links) != 2 {
		t.Fatalf("links = %v, want one per agent", links)
	}

	out, err := h.run(t, "list")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, out)
	}
	if !strings.Contains(out, "claude,codex") {
		t.Errorf("list = %q, want both agents", out)
	}
}

func TestAdoptNarrowsToTheNamedAgent(t *testing.T) {
	h := newHarness(t)
	dir := localDir(t, nil)
	handLink(t, h.claude, "my-skill", dir)
	handLink(t, h.codex, "my-skill", dir)

	if out, err := h.run(t, "adopt", "-a", "codex"); err != nil {
		t.Fatalf("adopt -a codex: %v\n%s", err, out)
	}

	links, _ := h.receipts(t)["my-skill"]["links"].([]any)
	if len(links) != 1 {
		t.Fatalf("links = %v, want only the agent that was asked for", links)
	}
	if got, _ := links[0].(map[string]any); got["target"] != "codex" {
		t.Errorf("link = %v, want codex", got)
	}
}

func TestAdoptPromotesACleanCheckoutPinnedToItsHead(t *testing.T) {
	h := newHarness(t)
	url, sha := testrepo.New(t, map[string]string{"skills/demo/SKILL.md": skillMD})
	clone := testrepo.Clone(t, url)
	handLink(t, h.claude, "demo-skill", filepath.Join(clone, "skills", "demo"))

	if out, err := h.run(t, "adopt"); err != nil {
		t.Fatalf("adopt: %v\n%s", err, out)
	}

	r := h.receipts(t)["demo-skill"]
	if r["channel"] != "git" {
		t.Fatalf("channel = %v, want git", r["channel"])
	}
	if r["source"] != url {
		t.Errorf("source = %v, want %s", r["source"], url)
	}
	if r["resolved"] != sha {
		t.Errorf("resolved = %v, want %s", r["resolved"], sha)
	}
	if r["subpath"] != "skills/demo" {
		t.Errorf("subpath = %v, want skills/demo", r["subpath"])
	}
	if r["pinned"] != true {
		t.Error("a promoted checkout must be pinned, so update cannot re-point it unasked")
	}
	if _, ok := r["contentHash"]; ok {
		t.Errorf("contentHash = %v, want none for a working copy", r["contentHash"])
	}

	// Pinned means update leaves it exactly where it is.
	out, err := h.run(t, "update")
	if err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	if !strings.Contains(out, "pinned") {
		t.Errorf("update = %q, want it to report the skill as pinned", out)
	}
}

func TestAdoptKeepsADirtyCheckoutLocal(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"skills/demo/SKILL.md": skillMD})
	clone := testrepo.Clone(t, url)
	demo := filepath.Join(clone, "skills", "demo")
	if err := os.WriteFile(filepath.Join(demo, "SKILL.md"), []byte(skillMD+"\nEdited.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	handLink(t, h.claude, "demo-skill", demo)

	out, err := h.run(t, "adopt")
	if err != nil {
		t.Fatalf("adopt: %v\n%s", err, out)
	}

	if got := h.receipts(t)["demo-skill"]["channel"]; got != "local" {
		t.Errorf("channel = %v, want local for a checkout with uncommitted changes", got)
	}
}

func TestAdoptExitsPartialWhenSomeEntriesAreSkipped(t *testing.T) {
	h := newHarness(t)
	handLink(t, h.claude, "my-skill", localDir(t, nil))
	handLink(t, h.claude, "gone", filepath.Join(t.TempDir(), "missing"))

	code, out := exitCode(t, "adopt")

	if code != ExitPartial {
		t.Errorf("exit = %d, want %d", code, ExitPartial)
	}
	if !strings.Contains(out, "adopted 1 skill") || !strings.Contains(out, "dangling symlink") {
		t.Errorf("output = %q, want both the work done and the reason for the rest", out)
	}
}

func TestAdoptReportsNothingToDoOnACleanMachine(t *testing.T) {
	h := newHarness(t)

	out, err := h.run(t, "adopt")
	if err != nil {
		t.Fatalf("adopt: %v\n%s", err, out)
	}
	if !strings.Contains(out, "adopted 0 skills") {
		t.Errorf("output = %q, want it to say it found nothing", out)
	}
}

// An adopted git receipt points at a working copy, not the store. gc must not
// mistake that for a live root, nor let its slug confuse mirror collection.
func TestGCIsUnaffectedByAnAdoptedCheckout(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"skills/demo/SKILL.md": skillMD})
	clone := testrepo.Clone(t, url)
	handLink(t, h.claude, "demo-skill", filepath.Join(clone, "skills", "demo"))

	if out, err := h.run(t, "adopt"); err != nil {
		t.Fatalf("adopt: %v\n%s", err, out)
	}

	out, err := h.run(t, "gc", "--dry-run")
	if err != nil {
		t.Fatalf("gc --dry-run: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(clone, "skills", "demo", "SKILL.md")); err != nil {
		t.Errorf("gc reached outside the store: %v", err)
	}
}

// The case a hand-check found: everything skillsctl installs points into the
// store, so a managed skill must be recognised as managed before it is judged
// on where it points.
func TestAdoptLeavesAnInstalledSkillAlone(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})
	if out, err := h.run(t, "install", url); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	before := h.receipts(t)["demo-skill"]["updatedAt"]

	out, err := h.run(t, "adopt")
	if err != nil {
		t.Fatalf("adopt: %v\n%s", err, out)
	}

	if strings.Contains(out, "skipped") {
		t.Errorf("output = %q, want an installed skill treated as managed, not skipped", out)
	}
	if !strings.Contains(out, "already managed") {
		t.Errorf("output = %q, want it counted as managed", out)
	}
	if after := h.receipts(t)["demo-skill"]["updatedAt"]; after != before {
		t.Errorf("adopt rewrote an installed skill's receipt: %v -> %v", before, after)
	}
}

// A hand-made symlink into a second agent, pointing at the revision the receipt
// is already on, is the link `skillsctl link <name> -a <agent>` would have
// written. adopt records it rather than reporting it as unadoptable.
func TestAdoptAddsAHandMadeSecondLinkToAManagedSkill(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})
	if out, err := h.run(t, "install", url, "-a", "claude"); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	rev, err := os.Readlink(filepath.Join(h.claude, "demo-skill"))
	if err != nil {
		t.Fatal(err)
	}
	link := handLink(t, h.codex, "demo-skill", rev)

	code, out := exitCode(t, "adopt")
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitOK, out)
	}
	if !strings.Contains(out, "linked demo-skill into codex") {
		t.Errorf("output = %q, want it to name the link it added", out)
	}

	links, _ := h.receipts(t)["demo-skill"]["links"].([]any)
	if len(links) != 2 {
		t.Fatalf("links = %v, want claude's and the hand-made codex one", links)
	}
	got, _ := links[1].(map[string]any)
	if got["target"] != "codex" || got["path"] != link {
		t.Errorf("added link = %v, want %s in codex", got, link)
	}

	// The symlink is recorded, not remade: adopt still plans nothing but Records.
	if dest, rerr := os.Readlink(link); rerr != nil || dest != rev {
		t.Errorf("readlink = %q, %v; want %s untouched", dest, rerr, rev)
	}

	// And it is now a link like any other, so remove -a takes it away.
	if out, rerr := h.run(t, "remove", "demo-skill", "-a", "codex"); rerr != nil {
		t.Fatalf("remove: %v\n%s", rerr, out)
	}
	if _, serr := os.Lstat(link); !os.IsNotExist(serr) {
		t.Errorf("codex link still there (%v), want the adopted link removable", serr)
	}
}

// A receipt says where its links point, so a symlink that leads somewhere else
// is not one of its links however much the name matches.
func TestAdoptSkipsASecondLinkPointingSomewhereElse(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})
	if out, err := h.run(t, "install", url, "-a", "claude"); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	handLink(t, h.codex, "demo-skill", localDir(t, nil))

	code, out := exitCode(t, "adopt")
	if code != ExitError {
		t.Fatalf("exit = %d, want %d\n%s", code, ExitError, out)
	}
	if !strings.Contains(out, "skipped demo-skill") {
		t.Errorf("output = %q, want the entry reported as skipped", out)
	}

	links, _ := h.receipts(t)["demo-skill"]["links"].([]any)
	if len(links) != 1 {
		t.Errorf("links = %v, want the receipt left with claude's alone", links)
	}
}

// The classifier and the plan agree: a dry run that would add a link records
// nothing, and the ops it prints are Records and nothing else.
func TestAdoptDryRunDoesNotAddASecondLink(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})
	if out, err := h.run(t, "install", url, "-a", "claude"); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	rev, err := os.Readlink(filepath.Join(h.claude, "demo-skill"))
	if err != nil {
		t.Fatal(err)
	}
	handLink(t, h.codex, "demo-skill", rev)

	out, err := h.run(t, "adopt", "--dry-run")
	if err != nil {
		t.Fatalf("adopt --dry-run: %v\n%s", err, out)
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.HasPrefix(line, "link ") || strings.HasPrefix(line, "unlink") {
			t.Errorf("planned %q, want adopt to plan nothing but records", line)
		}
	}

	links, _ := h.receipts(t)["demo-skill"]["links"].([]any)
	if len(links) != 1 {
		t.Errorf("links = %v, want the receipt untouched by a dry run", links)
	}
}
