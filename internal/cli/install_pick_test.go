package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/prompt"
	"github.com/richardcase/skillsctl/internal/testrepo"
)

func TestInstallMultiSkillRepoInstallsWhatWasPicked(t *testing.T) {
	h := newHarness(t)
	url, _ := multiRepo(t)
	h.picker.on, h.picker.choose = true, picks(1)

	out, err := h.run(t, "install", url)
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if !linked(t, h, "beta") {
		t.Error("the picked skill should be linked")
	}
	if linked(t, h, "alpha") {
		t.Error("only the picked skill should be linked")
	}
}

func TestInstallPickerIsOfferedEverySkillWithItsDescription(t *testing.T) {
	h := newHarness(t)
	url, _ := multiRepo(t)
	h.picker.on, h.picker.choose = true, picks(0)

	if out, err := h.run(t, "install", url); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	asked := h.picker.asked
	if len(asked.Items) != 2 {
		t.Fatalf("picker was offered %d rows, want 2: %+v", len(asked.Items), asked.Items)
	}
	// The rows carry the same name-and-description the plain listing shows,
	// because both are built from rowLabels.
	if !strings.Contains(asked.Items[0].Label, "alpha") ||
		!strings.Contains(asked.Items[0].Label, "Does the alpha thing") {
		t.Errorf("row = %q, want the name and its description", asked.Items[0].Label)
	}
	if !strings.Contains(strings.Join(asked.Header, "\n"), url) {
		t.Errorf("header = %q, want it to name where the skills came from", asked.Header)
	}
	if asked.Single {
		t.Error("without --as the picker takes several skills")
	}
}

func TestInstallPicksSeveralSkills(t *testing.T) {
	h := newHarness(t)
	url, _ := multiRepo(t)
	h.picker.on, h.picker.choose = true, picks(0, 1)

	out, err := h.run(t, "install", url)
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	for _, name := range []string{"alpha", "beta"} {
		if !linked(t, h, name) {
			t.Errorf("%s should be linked", name)
		}
	}
}

// The tracked ref is what update follows. Selection re-reads the repository
// pinned to the sha it listed, and that pin must not leak into the receipt: a
// skill installed by picking it is no more pinned than one installed by name.
func TestInstallPickedSkillTracksTheRefNotTheSha(t *testing.T) {
	h := newHarness(t)
	url, sha := multiRepo(t)
	h.picker.on, h.picker.choose = true, picks(0)

	if out, err := h.run(t, "install", url); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	got := h.receipts(t)["alpha"]
	if ref, _ := got["ref"].(string); ref != "" {
		t.Errorf("receipt ref = %q, want it empty: picking a skill is not pinning it", ref)
	}
	if pinned, _ := got["pinned"].(bool); pinned {
		t.Error("receipt is pinned; picking a skill is not pinning it")
	}
	if resolved, _ := got["resolved"].(string); resolved != sha {
		t.Errorf("receipt resolved = %q, want the sha that was listed (%s)", resolved, sha)
	}

	// And the proof it is still followed: a commit on top now reads as
	// outdated rather than as current.
	testrepo.Commit(t, testrepo.Dir(url), map[string]string{"NOTES.md": "moved on\n"})
	code, out := exitCode(t, "outdated")
	if code != ExitOutdated {
		t.Fatalf("exit = %d, want %d — a picked skill still follows its ref\n%s", code, ExitOutdated, out)
	}
}

func TestInstallCancelledPickChangesNothing(t *testing.T) {
	h := newHarness(t)
	url, _ := multiRepo(t)
	h.picker.on = true // choose is nil, so the picker cancels

	out, err := h.run(t, "install", url)
	if err == nil {
		t.Fatalf("a cancelled install succeeded\n%s", out)
	}
	if !errors.Is(err, prompt.ErrCancelled) {
		t.Errorf("error = %v, want it to be ErrCancelled", err)
	}
	if linked(t, h, "alpha") || linked(t, h, "beta") {
		t.Error("nothing should have been linked")
	}
	if _, serr := os.Stat(filepath.Join(h.root, "state.json")); !os.IsNotExist(serr) {
		t.Error("nothing should have been recorded")
	}

	// Backing out is still a non-zero exit, so a script wrapping install
	// notices that it did not happen.
	if code, _ := exitCode(t, "install", url); code != ExitError {
		t.Errorf("exit = %d, want %d", code, ExitError)
	}
}

// --skill naming nothing in the repository is a typo, not an unanswered
// question, so it must fail the way it always did rather than open a picker.
func TestInstallUnknownSkillNameNeverPrompts(t *testing.T) {
	h := newHarness(t)
	url, _ := multiRepo(t)
	h.picker.on, h.picker.choose = true, picks(0)

	out, err := h.run(t, "install", url, "--skill", "gamma")
	if err == nil {
		t.Fatalf("install accepted an unknown skill name\n%s", out)
	}
	if len(h.picker.asked.Items) != 0 {
		t.Error("a mistyped --skill should not be answered by asking")
	}
	if linked(t, h, "alpha") || linked(t, h, "beta") {
		t.Error("nothing should have been installed")
	}
}

func TestInstallAllNeverPrompts(t *testing.T) {
	h := newHarness(t)
	url, _ := multiRepo(t)
	h.picker.on, h.picker.choose = true, picks(0)

	if out, err := h.run(t, "install", url, "--all"); err != nil {
		t.Fatalf("install --all: %v\n%s", err, out)
	}
	if len(h.picker.asked.Items) != 0 {
		t.Error("--all has already answered the question")
	}
	for _, name := range []string{"alpha", "beta"} {
		if !linked(t, h, name) {
			t.Errorf("%s should be linked", name)
		}
	}
}

func TestInstallSingleSkillRepoNeverPrompts(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"skills/only/SKILL.md": alphaMD})
	h.picker.on, h.picker.choose = true, picks(0)

	if out, err := h.run(t, "install", url); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if len(h.picker.asked.Items) != 0 {
		t.Error("there was nothing to ask about")
	}
}

// --as renames exactly one skill, so the picker offers one rather than letting
// the user reach a selection the flag cannot accept.
func TestInstallAsPicksASingleSkill(t *testing.T) {
	h := newHarness(t)
	url, _ := multiRepo(t)
	h.picker.on, h.picker.choose = true, picks(1)

	out, err := h.run(t, "install", url, "--as", "renamed")
	if err != nil {
		t.Fatalf("install --as: %v\n%s", err, out)
	}
	if !h.picker.asked.Single {
		t.Error("--as should put the picker in single-select mode")
	}
	if !linked(t, h, "renamed") {
		t.Error("the picked skill should be linked under the --as name")
	}
	if linked(t, h, "beta") {
		t.Error("the skill should carry the --as name, not its own")
	}
}

func TestInstallPickedSkillIsListedAfterTheChoice(t *testing.T) {
	h := newHarness(t)
	url, _ := multiRepo(t)
	h.picker.on, h.picker.choose = true, picks(1)

	out, err := h.run(t, "install", url)
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	// The picker erases itself, so what is left in the scrollback has to say
	// what was chosen and where it came from.
	if !strings.Contains(out, "beta") || !strings.Contains(out, "Does the beta thing") {
		t.Errorf("output should list what was picked:\n%s", out)
	}
	if strings.Contains(out, "alpha") {
		t.Errorf("output should not list what was passed over:\n%s", out)
	}
}

// A repository whose skills span two folders offers a header row per folder,
// so the picker's own semantics (a header selecting its group) can turn one
// toggle into "every skill in cat-a".
func TestInstallOffersHeaderRowsForAMultiCategoryRepo(t *testing.T) {
	h := newHarness(t)
	url, _ := categorizedRepo(t)
	// The rows fakePicker.choose returns stand in for what the real picker
	// would have resolved a header toggle to: cat-a's two members.
	h.picker.on, h.picker.choose = true, picks(1, 2)

	out, err := h.run(t, "install", url)
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	asked := h.picker.asked
	want := []struct {
		header bool
		label  string
	}{
		{true, "cat-a"},
		{false, "one"},
		{false, "two"},
		{true, "cat-b"},
		{false, "three"},
	}
	if len(asked.Items) != len(want) {
		t.Fatalf("picker was offered %d rows, want %d: %+v", len(asked.Items), len(want), asked.Items)
	}
	for i, w := range want {
		if asked.Items[i].Header != w.header || !strings.Contains(asked.Items[i].Label, w.label) {
			t.Errorf("row %d = %+v, want Header=%v containing %q", i, asked.Items[i], w.header, w.label)
		}
	}

	for _, name := range []string{"one", "two"} {
		if !linked(t, h, name) {
			t.Errorf("%s should be linked: choosing cat-a's header picks its whole group", name)
		}
	}
	if linked(t, h, "three") {
		t.Error("cat-b was never chosen, so three should not be linked")
	}
}

func TestInstallPickedSkillDryRunChangesNothing(t *testing.T) {
	h := newHarness(t)
	url, _ := multiRepo(t)
	h.picker.on, h.picker.choose = true, picks(0)

	out, err := h.run(t, "install", url, "--dry-run")
	if err != nil {
		t.Fatalf("install --dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "alpha") {
		t.Errorf("dry run should describe what picking alpha would do:\n%s", out)
	}
	if linked(t, h, "alpha") {
		t.Error("dry run created a symlink")
	}
	if _, err := os.Stat(filepath.Join(h.root, "state.json")); !os.IsNotExist(err) {
		t.Error("dry run wrote the receipts database")
	}
}

// link <path> is install by another name — it shares installOpts and runInstall
// — so a local directory of several skills is picked from the same way.
func TestLinkPathInstallsWhatWasPicked(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	for name, body := range map[string]string{"alpha": alphaMD, "beta": betaMD} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name, "SKILL.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h.picker.on, h.picker.choose = true, picks(1)

	out, err := h.run(t, "link", dir)
	if err != nil {
		t.Fatalf("link: %v\n%s", err, out)
	}
	if !linked(t, h, "beta") {
		t.Error("the picked skill should be linked")
	}
	if linked(t, h, "alpha") {
		t.Error("only the picked skill should be linked")
	}

	var got []struct {
		Name    string `json:"name"`
		Channel string `json:"channel"`
	}
	listOut, _ := h.run(t, "list", "--json")
	if err := json.Unmarshal([]byte(listOut), &got); err != nil {
		t.Fatalf("list --json invalid: %v\n%s", err, listOut)
	}
	if len(got) != 1 || got[0].Name != "beta" || got[0].Channel != "local" {
		t.Errorf("receipts = %+v, want one local receipt for beta", got)
	}
}
