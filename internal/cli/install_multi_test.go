package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/testrepo"
)

const (
	alphaMD = "---\nname: alpha\ndescription: Does the alpha thing\n---\n\nBody.\n"
	betaMD  = "---\nname: beta\ndescription: Does the beta thing\n---\n\nBody.\n"
)

// multiRepo is a fixture repository with two skills under skills/.
func multiRepo(t *testing.T) (url, sha string) {
	t.Helper()
	return testrepo.New(t, map[string]string{
		"skills/alpha/SKILL.md": alphaMD,
		"skills/beta/SKILL.md":  betaMD,
		"README.md":             "# Not a skill\n",
	})
}

// linked reports whether name is linked into the claude target.
func linked(t *testing.T, h *harness, name string) bool {
	t.Helper()
	_, err := os.Lstat(filepath.Join(h.claude, name))
	return err == nil
}

func TestInstallMultiSkillRepoWithoutSelectionListsAndFails(t *testing.T) {
	h := newHarness(t)
	url, _ := multiRepo(t)

	out, err := h.run(t, "install", url)
	if err == nil {
		t.Fatalf("bare install of a multi-skill repo succeeded; it must not guess\n%s", out)
	}
	for _, want := range []string{"alpha", "beta", "Does the alpha thing"} {
		if !strings.Contains(out, want) {
			t.Errorf("output should list what is available (%q missing):\n%s", want, out)
		}
	}
	if !strings.Contains(err.Error(), "--all") || !strings.Contains(err.Error(), "--skill") {
		t.Errorf("error = %v, want it to name --skill and --all", err)
	}
	if linked(t, h, "alpha") || linked(t, h, "beta") {
		t.Error("nothing should have been linked")
	}
	if _, serr := os.Stat(filepath.Join(h.root, "state.json")); !os.IsNotExist(serr) {
		t.Error("nothing should have been recorded")
	}
}

func TestInstallSkillSelectsOne(t *testing.T) {
	h := newHarness(t)
	url, _ := multiRepo(t)

	if out, err := h.run(t, "install", url, "--skill", "alpha"); err != nil {
		t.Fatalf("install --skill alpha: %v\n%s", err, out)
	}
	if !linked(t, h, "alpha") {
		t.Error("alpha should be linked")
	}
	if linked(t, h, "beta") {
		t.Error("beta was not asked for")
	}
}

func TestInstallSkillRepeatedSelectsSeveralSharingOneRevision(t *testing.T) {
	h := newHarness(t)
	url, sha := multiRepo(t)

	out, err := h.run(t, "install", url, "--skill", "alpha", "--skill", "beta")
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	for _, name := range []string{"alpha", "beta"} {
		dest, rerr := os.Readlink(filepath.Join(h.claude, name))
		if rerr != nil {
			t.Fatalf("%s link missing: %v", name, rerr)
		}
		if _, serr := os.Stat(filepath.Join(dest, "SKILL.md")); serr != nil {
			t.Errorf("%s link does not resolve to a skill: %v", name, serr)
		}
		want := filepath.Join("skills", name)
		if !strings.HasSuffix(dest, want) {
			t.Errorf("%s link target = %q, want it to end in %q", name, dest, want)
		}
		if !strings.Contains(dest, sha) {
			t.Errorf("%s link target %q should sit under the revision %s", name, dest, sha)
		}
	}

	// Both skills come from one commit, so they share one revision directory:
	// each receipt's RevPath is that directory plus the skill's own subpath.
	listOut, err := h.run(t, "list", "--json")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, listOut)
	}
	var got []struct {
		Name    string `json:"name"`
		RevPath string `json:"revPath"`
		Subpath string `json:"subpath"`
	}
	if err := json.Unmarshal([]byte(listOut), &got); err != nil {
		t.Fatalf("list --json invalid: %v\n%s", err, listOut)
	}
	if len(got) != 2 {
		t.Fatalf("got %d receipts, want 2", len(got))
	}

	roots := make(map[string]bool, 2)
	for _, r := range got {
		root := strings.TrimSuffix(r.RevPath, string(filepath.Separator)+filepath.FromSlash(r.Subpath))
		if root == r.RevPath {
			t.Fatalf("%s: revPath %q does not end in its subpath %q", r.Name, r.RevPath, r.Subpath)
		}
		if !strings.HasSuffix(root, sha) {
			t.Errorf("%s: revision root = %q, want it to end in the sha %s", r.Name, root, sha)
		}
		roots[root] = true
	}
	if len(roots) != 1 {
		t.Errorf("skills from one commit landed in %d revision directories, want 1: %v", len(roots), roots)
	}
}

func TestInstallAllInstallsEverySkill(t *testing.T) {
	h := newHarness(t)
	url, _ := multiRepo(t)

	out, err := h.run(t, "install", url, "--all")
	if err != nil {
		t.Fatalf("install --all: %v\n%s", err, out)
	}
	for _, name := range []string{"alpha", "beta"} {
		if !linked(t, h, name) {
			t.Errorf("%s should be linked", name)
		}
	}

	listOut, err := h.run(t, "list", "--json")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, listOut)
	}
	var got []struct {
		Name    string `json:"name"`
		Subpath string `json:"subpath"`
	}
	if err := json.Unmarshal([]byte(listOut), &got); err != nil {
		t.Fatalf("list --json invalid: %v\n%s", err, listOut)
	}
	if len(got) != 2 {
		t.Fatalf("got %d receipts, want 2", len(got))
	}
	for _, r := range got {
		if want := "skills/" + r.Name; r.Subpath != want {
			t.Errorf("%s receipt subpath = %q, want %q", r.Name, r.Subpath, want)
		}
	}
}

func TestInstallUnknownSkillNameListsWhatIsAvailable(t *testing.T) {
	h := newHarness(t)
	url, _ := multiRepo(t)

	out, err := h.run(t, "install", url, "--skill", "gamma")
	if err == nil {
		t.Fatalf("install accepted an unknown skill name\n%s", out)
	}
	if !strings.Contains(err.Error(), "gamma") {
		t.Errorf("error = %v, want it to name the missing skill", err)
	}
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
		t.Errorf("output should list what is available:\n%s", out)
	}
}

func TestInstallAllRejectsAs(t *testing.T) {
	h := newHarness(t)
	url, _ := multiRepo(t)

	if _, err := h.run(t, "install", url, "--all", "--as", "x"); err == nil {
		t.Fatal("--all with --as succeeded; one name cannot rename several skills")
	}
}

func TestInstallAllRejectsSkill(t *testing.T) {
	h := newHarness(t)
	url, _ := multiRepo(t)

	if _, err := h.run(t, "install", url, "--all", "--skill", "alpha"); err == nil {
		t.Fatal("--all with --skill succeeded; they contradict each other")
	}
}

func TestInstallAllSkipsAlreadyInstalledAndReportsIt(t *testing.T) {
	h := newHarness(t)
	url, _ := multiRepo(t)

	if out, err := h.run(t, "install", url, "--skill", "alpha"); err != nil {
		t.Fatalf("first install: %v\n%s", err, out)
	}

	out, err := h.run(t, "install", url, "--all")
	if err == nil {
		t.Fatalf("install --all with a collision exited zero; want a non-zero exit\n%s", out)
	}
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "skipped") {
		t.Errorf("output should report the skipped skill:\n%s", out)
	}
	if !linked(t, h, "beta") {
		t.Error("beta should still have been installed alongside the collision")
	}

	listOut, _ := h.run(t, "list")
	if !strings.Contains(listOut, "alpha") || !strings.Contains(listOut, "beta") {
		t.Errorf("both skills should be recorded:\n%s", listOut)
	}
}

func TestInstallAllWithEveryNameTakenChangesNothing(t *testing.T) {
	h := newHarness(t)
	url, _ := multiRepo(t)

	if _, err := h.run(t, "install", url, "--all"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(h.root, "state.json"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := h.run(t, "install", url, "--all"); err == nil {
		t.Fatal("re-running --all with every name taken succeeded; want an error")
	}
	after, err := os.ReadFile(filepath.Join(h.root, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("a fully-colliding --all must not touch the receipts")
	}
}

func TestInstallAllDryRunChangesNothing(t *testing.T) {
	h := newHarness(t)
	url, _ := multiRepo(t)

	out, err := h.run(t, "install", url, "--all", "--dry-run")
	if err != nil {
		t.Fatalf("install --all --dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
		t.Errorf("dry run should describe both skills:\n%s", out)
	}
	if linked(t, h, "alpha") || linked(t, h, "beta") {
		t.Error("dry run created a symlink")
	}
	if _, err := os.Stat(filepath.Join(h.root, "state.json")); !os.IsNotExist(err) {
		t.Error("dry run wrote the receipts database")
	}
}

func TestInstallSubpathThatIsASkillNeedsNoSelection(t *testing.T) {
	h := newHarness(t)
	url, _ := multiRepo(t)

	if out, err := h.run(t, "install", url+"//skills/alpha"); err != nil {
		t.Fatalf("subpath install: %v\n%s", err, out)
	}
	if !linked(t, h, "alpha") {
		t.Error("alpha should be linked")
	}
	if linked(t, h, "beta") {
		t.Error("a subpath install should reach only that subpath")
	}
}

func TestInstallSubpathScopesTheWalk(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{
		"skills/alpha/SKILL.md": alphaMD,
		"skills/beta/SKILL.md":  betaMD,
		"vendor/gamma/SKILL.md": "---\nname: gamma\n---\n",
	})

	// The subpath is above two skills, so the choice is still ambiguous, but
	// the third skill outside it is not on offer.
	out, err := h.run(t, "install", url+"//skills")
	if err == nil {
		t.Fatalf("bare install under a multi-skill subpath succeeded\n%s", out)
	}
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
		t.Errorf("listing should cover the subpath:\n%s", out)
	}
	if strings.Contains(out, "gamma") {
		t.Errorf("listing reached outside the subpath:\n%s", out)
	}

	if out, err := h.run(t, "install", url+"//skills", "--all"); err != nil {
		t.Fatalf("install --all under a subpath: %v\n%s", err, out)
	}
	for _, name := range []string{"alpha", "beta"} {
		if !linked(t, h, name) {
			t.Errorf("%s should be linked", name)
		}
	}
	if linked(t, h, "gamma") {
		t.Error("--all installed a skill outside the subpath")
	}
}

func TestInstallSubpathSharesTheRepositoryRevision(t *testing.T) {
	h := newHarness(t)
	url, _ := multiRepo(t)

	if _, err := h.run(t, "install", url+"//skills/alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.run(t, "install", url+"//skills/beta"); err != nil {
		t.Fatal(err)
	}

	alpha, aerr := os.Readlink(filepath.Join(h.claude, "alpha"))
	beta, berr := os.Readlink(filepath.Join(h.claude, "beta"))
	if aerr != nil || berr != nil {
		t.Fatalf("links missing: %v %v", aerr, berr)
	}
	if filepath.Dir(alpha) != filepath.Dir(beta) {
		t.Errorf("two subpath installs of one commit landed in different revisions:\n%s\n%s", alpha, beta)
	}
}

func TestInstallSingleSkillRepoNeedsNoSelection(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"skills/only/SKILL.md": alphaMD})

	if out, err := h.run(t, "install", url); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if !linked(t, h, "alpha") {
		t.Error("a repository with exactly one skill installs without flags")
	}
}

func TestInstallSkillMatchesByPath(t *testing.T) {
	h := newHarness(t)
	url, _ := multiRepo(t)

	if out, err := h.run(t, "install", url, "--skill", "skills/beta"); err != nil {
		t.Fatalf("install --skill skills/beta: %v\n%s", err, out)
	}
	if !linked(t, h, "beta") {
		t.Error("beta should be linked when selected by path")
	}
}

func TestInstallSingleSelectionKeepsTheHardCollisionError(t *testing.T) {
	h := newHarness(t)
	url, _ := multiRepo(t)

	if _, err := h.run(t, "install", url, "--skill", "alpha"); err != nil {
		t.Fatal(err)
	}
	_, err := h.run(t, "install", url, "--skill", "alpha")
	if err == nil {
		t.Fatal("re-installing one named skill succeeded; want a collision error")
	}
	if !strings.Contains(err.Error(), "--as") {
		t.Errorf("error = %v, want the existing single-skill collision error suggesting --as", err)
	}
}

func TestInstallRepoWithNoSkillsErrors(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"README.md": "# Nothing here\n"})

	if _, err := h.run(t, "install", url, "--all"); err == nil {
		t.Fatal("install of a repository with no skills succeeded; want an error")
	}
}
