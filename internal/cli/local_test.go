package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/store"
)

const localMD = "---\nname: my-skill\ndescription: Under development\n---\n\nFirst draft.\n"

// localDir writes a skill directory somewhere that is neither the store nor an
// agent's skills directory — where somebody would actually be working on one.
func localDir(t *testing.T, files map[string]string) string {
	t.Helper()

	dir := t.TempDir()
	if files == nil {
		files = map[string]string{"SKILL.md": localMD}
	}
	for rel, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// The reason this channel exists: a skill you are editing is live, with no
// reinstall, because the symlink points at your directory rather than a copy.
func TestLinkedSkillIsLiveAsYouEditIt(t *testing.T) {
	h := newHarness(t)
	dir := localDir(t, nil)

	out, err := h.run(t, "link", dir)
	if err != nil {
		t.Fatalf("link: %v\n%s", err, out)
	}
	// No revision to name, so no dangling "@".
	if !strings.Contains(out, "installed my-skill into claude") {
		t.Errorf("output = %q, want the skill and its agents without an empty version", out)
	}

	link := filepath.Join(h.claude, "my-skill")
	before, err := os.ReadFile(filepath.Join(link, "SKILL.md"))
	if err != nil {
		t.Fatalf("read through the link: %v", err)
	}
	if !strings.Contains(string(before), "First draft") {
		t.Fatalf("link resolves to %q, want the source", before)
	}

	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(localMD+"\nSecond draft.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	after, err := os.ReadFile(filepath.Join(link, "SKILL.md"))
	if err != nil {
		t.Fatalf("read through the link after editing: %v", err)
	}
	if !strings.Contains(string(after), "Second draft") {
		t.Error("an edit to the source was not visible through the agent's link; the skill was copied rather than linked")
	}
}

// remove takes away skillsctl's own symlinks and nothing else. The source is
// the user's, and they did not ask for it to be deleted.
func TestRemoveNeverTouchesTheLocalSource(t *testing.T) {
	h := newHarness(t)
	dir := localDir(t, map[string]string{
		"SKILL.md":           localMD,
		"reference/notes.md": "Notes worth keeping.\n",
		"scripts/helper.sh":  "#!/bin/sh\necho hi\n",
	})

	before, err := store.HashDir(dir)
	if err != nil {
		t.Fatalf("hash before: %v", err)
	}

	if out, err := h.run(t, "link", dir); err != nil {
		t.Fatalf("link: %v\n%s", err, out)
	}
	if out, err := h.run(t, "remove", "my-skill"); err != nil {
		t.Fatalf("remove: %v\n%s", err, out)
	}

	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the source directory is gone: %v", err)
	}
	after, err := store.HashDir(dir)
	if err != nil {
		t.Fatalf("hash after: %v", err)
	}
	if before != after {
		t.Error("the source tree changed across link then remove; it must come back byte-identical")
	}
	if _, err := os.Lstat(filepath.Join(h.claude, "my-skill")); !os.IsNotExist(err) {
		t.Error("the symlink survived removal")
	}
}

func TestInstallAndLinkAgreeOnALocalPath(t *testing.T) {
	for _, verb := range []string{"install", "link"} {
		t.Run(verb, func(t *testing.T) {
			h := newHarness(t)
			dir := localDir(t, nil)

			if out, err := h.run(t, verb, dir); err != nil {
				t.Fatalf("%s: %v\n%s", verb, err, out)
			}

			r := h.receipts(t)["my-skill"]
			if r["channel"] != "local" {
				t.Errorf("channel = %v, want local", r["channel"])
			}
			if r["source"] != dir || r["revPath"] != dir {
				t.Errorf("receipt = %v, want the source directory in both source and revPath", r)
			}
			if _, ok := r["slug"]; ok {
				t.Error("a local receipt must record no slug")
			}
			if _, ok := r["contentHash"]; ok {
				t.Error("a local receipt must record no content hash")
			}
		})
	}
}

func TestLinkRefusesASourceThatIsNotAPath(t *testing.T) {
	h := newHarness(t)

	_, err := h.run(t, "link", "owner/repo")
	if err == nil {
		t.Fatal("link accepted a git source")
	}
	if !strings.Contains(err.Error(), "skillsctl install owner/repo") {
		t.Errorf("error = %v, want it to name the command that does work", err)
	}
}

func TestListShowsALocalSkillWithoutAVersion(t *testing.T) {
	h := newHarness(t)
	dir := localDir(t, nil)
	if out, err := h.run(t, "link", dir); err != nil {
		t.Fatalf("link: %v\n%s", err, out)
	}

	out, err := h.run(t, "list")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, out)
	}
	if !strings.Contains(out, "my-skill") || !strings.Contains(out, "local") {
		t.Errorf("list = %q, want the local row", out)
	}
	// A dash rather than a gap: there is no revision, and a blank cell reads
	// as a broken table.
	if !strings.Contains(out, "-") {
		t.Errorf("list = %q, want the missing version rendered as a dash", out)
	}
	if !strings.Contains(out, "claude") {
		t.Errorf("list = %q, want the agents it was linked into", out)
	}
}

func TestUpdateSkipsALocalSkill(t *testing.T) {
	h := newHarness(t)
	dir := localDir(t, nil)
	if out, err := h.run(t, "link", dir); err != nil {
		t.Fatalf("link: %v\n%s", err, out)
	}

	out, err := h.run(t, "update")
	if err != nil {
		t.Fatalf("update over only a local skill should exit 0: %v\n%s", err, out)
	}
	if !strings.Contains(out, "no ref to update from") {
		t.Errorf("update = %q, want it to say why a local skill is skipped", out)
	}
}

// gc reasons about the store, and a local skill puts nothing there. It must
// neither collect the source nor be confused by a receipt with no slug.
func TestGCIgnoresALocalSkillAndStillReclaimsMirrors(t *testing.T) {
	h := newHarness(t)
	dir := localDir(t, nil)
	if out, err := h.run(t, "link", dir); err != nil {
		t.Fatalf("link: %v\n%s", err, out)
	}

	out, err := h.run(t, "gc")
	if err != nil {
		t.Fatalf("gc: %v\n%s", err, out)
	}
	if strings.Contains(out, "no bare mirror could be proven unused") {
		t.Error("a local receipt's empty slug reached store.Collect and disabled mirror collection")
	}
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
		t.Errorf("gc touched the source directory: %v", err)
	}
	if listed, _ := h.run(t, "list"); !strings.Contains(listed, "my-skill") {
		t.Error("gc removed a local receipt")
	}
}

func TestLinkASingleSkillOutOfALocalCheckout(t *testing.T) {
	h := newHarness(t)
	dir := localDir(t, map[string]string{
		"skills/alpha/SKILL.md": "---\nname: alpha\n---\n",
		"skills/beta/SKILL.md":  "---\nname: beta\n---\n",
	})

	out, err := h.run(t, "link", dir)
	if err == nil {
		t.Fatalf("link picked one of two skills without being told which\n%s", out)
	}
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
		t.Errorf("output = %q, want the listing of what could have been asked for", out)
	}

	if out, err := h.run(t, "link", dir, "--skill", "beta"); err != nil {
		t.Fatalf("link --skill: %v\n%s", err, out)
	}
	if _, err := os.Lstat(filepath.Join(h.claude, "beta")); err != nil {
		t.Errorf("beta was not linked: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(h.claude, "alpha")); !os.IsNotExist(err) {
		t.Error("alpha was linked without being asked for")
	}
}

func TestLinkRefusesADirectoryInsideAnAgentsSkills(t *testing.T) {
	h := newHarness(t)
	inside := filepath.Join(h.claude, "already-there")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inside, "SKILL.md"), []byte(localMD), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := h.run(t, "link", inside)
	if err == nil {
		t.Fatal("link accepted a directory inside an agent's own skills directory")
	}
	if !strings.Contains(err.Error(), "skills directory") {
		t.Errorf("error = %v, want it to say why", err)
	}
}
