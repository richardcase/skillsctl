package channel

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/target"
)

// linkFixture is a receipt whose revision directory exists, linked into claude
// alone, plus the two targets a second link could go into.
func linkFixture(t *testing.T) (state.Receipt, target.Target, target.Target) {
	t.Helper()

	rev := t.TempDir()
	agents := t.TempDir()
	claude := target.Target{Name: "claude", Dir: filepath.Join(agents, "claude", "skills")}
	codex := target.Target{Name: "codex", Dir: filepath.Join(agents, "codex", "skills")}

	r := state.Receipt{
		Name:     "demo-skill",
		Channel:  "git",
		RevPath:  rev,
		Resolved: "0123456789abcdef",
		Links:    []state.Link{{Target: claude.Name, Path: filepath.Join(claude.Dir, "demo-skill")}},
	}
	return r, claude, codex
}

func TestLinkAddsOneLinkPerTargetAndRecordsTheReceipt(t *testing.T) {
	r, _, codex := linkFixture(t)

	p, err := linked{}.Link(r, []target.Target{codex})
	if err != nil {
		t.Fatalf("Link: %v", err)
	}

	if len(p.Ops) != 2 {
		t.Fatalf("ops = %v, want a Link and a Record", p.Describe())
	}

	link, ok := p.Ops[0].(plan.Link)
	if !ok {
		t.Fatalf("first op = %T, want plan.Link", p.Ops[0])
	}
	if want := filepath.Join(codex.Dir, "demo-skill"); link.LinkPath != want {
		t.Errorf("LinkPath = %s, want %s", link.LinkPath, want)
	}
	if link.RevPath != r.RevPath {
		t.Errorf("RevPath = %s, want the receipt's own %s", link.RevPath, r.RevPath)
	}
	if link.Target != codex.Name {
		t.Errorf("Target = %s, want %s", link.Target, codex.Name)
	}

	rec, ok := p.Ops[1].(plan.Record)
	if !ok {
		t.Fatalf("second op = %T, want plan.Record", p.Ops[1])
	}
	if len(rec.Receipt.Links) != 2 {
		t.Fatalf("links = %v, want the existing one and the new one", rec.Receipt.Links)
	}
	if rec.Receipt.Links[1].Target != codex.Name {
		t.Errorf("appended link = %v, want one for codex", rec.Receipt.Links[1])
	}
	if !rec.Receipt.UpdatedAt.After(r.UpdatedAt) {
		t.Errorf("UpdatedAt = %v, want it moved on from %v", rec.Receipt.UpdatedAt, r.UpdatedAt)
	}
}

// The receipt handed in is the caller's, and a plan is inspectable precisely
// because building it changes nothing. Links shares a backing array with the
// receipt it was copied from, so this is the one place that could go wrong
// without anybody noticing.
func TestLinkLeavesTheReceiptItWasGivenAlone(t *testing.T) {
	r, _, codex := linkFixture(t)
	before := r.UpdatedAt

	if _, err := (linked{}).Link(r, []target.Target{codex}); err != nil {
		t.Fatalf("Link: %v", err)
	}

	if len(r.Links) != 1 {
		t.Errorf("links = %v, want the one it started with", r.Links)
	}
	if !r.UpdatedAt.Equal(before) {
		t.Errorf("UpdatedAt = %v, want it untouched", r.UpdatedAt)
	}
}

// Remove keys its drop filter by target name, and Unlink treats a missing link
// as success, so a receipt holding two links for one agent would plan two
// unlinks of one path and fail silently. Links stays a set keyed by target.
func TestLinkSkipsATargetTheReceiptAlreadyHas(t *testing.T) {
	r, claude, codex := linkFixture(t)

	p, err := linked{}.Link(r, []target.Target{claude, codex})
	if err != nil {
		t.Fatalf("Link: %v", err)
	}

	rec := p.Ops[len(p.Ops)-1].(plan.Record)
	if len(rec.Receipt.Links) != 2 {
		t.Fatalf("links = %v, want claude's kept once and codex added", rec.Receipt.Links)
	}
	for _, line := range p.Describe() {
		if strings.HasPrefix(line, "link") && strings.Contains(line, "[claude]") {
			t.Errorf("planned %q, want nothing for the agent that already has it", line)
		}
	}
}

// An empty plan is how the caller learns there was nothing to do, which is the
// same contract Remove has for a drop that matched no link.
func TestLinkPlansNothingWhenEveryTargetAlreadyHasIt(t *testing.T) {
	r, claude, _ := linkFixture(t)

	p, err := linked{}.Link(r, []target.Target{claude})
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if !p.IsEmpty() {
		t.Errorf("plan = %v, want an empty one", p.Describe())
	}
}

// target.Link would create the symlink happily and the user would find a dead
// entry in their skills directory. Refusing says what is actually wrong.
func TestLinkRefusesAReceiptWhoseFilesAreGone(t *testing.T) {
	r, _, codex := linkFixture(t)
	r.RevPath = filepath.Join(r.RevPath, "gone")

	_, err := linked{}.Link(r, []target.Target{codex})
	if err == nil {
		t.Fatal("Link: nil error, want a refusal")
	}
	if !strings.Contains(err.Error(), r.RevPath) {
		t.Errorf("error = %v, want it to name the missing directory", err)
	}
}

// The name reaches a receipt from a third party's SKILL.md, so it is checked
// again here rather than trusted because install checked it once.
func TestLinkRejectsANameThatEscapesTheSkillsDirectory(t *testing.T) {
	r, _, codex := linkFixture(t)
	r.Name = filepath.Join("..", "evil")

	_, err := linked{}.Link(r, []target.Target{codex})
	if err == nil {
		t.Fatal("Link: nil error, want a refusal")
	}
	if !strings.Contains(err.Error(), codex.Dir) {
		t.Errorf("error = %v, want it to name the directory the link would have left", err)
	}
}

// A plugin's skills are the agent's own, so there is no symlink of ours to
// duplicate. The error has to say that rather than "unsupported", because
// fanning a plugin out to other agents is a real feature that is merely
// deferred.
func TestPluginRefusesToLink(t *testing.T) {
	c, _ := newPluginChannel()

	_, err := c.Link(state.Receipt{Name: "demo", Channel: "plugin"}, []target.Target{{Name: "codex"}})
	if err == nil {
		t.Fatal("Link: nil error, want a refusal")
	}
	for _, want := range []string{"demo", "plugin"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to mention %q", err, want)
		}
	}
}

func TestLinkPathForRejectsNamesThatLeaveTheDirectory(t *testing.T) {
	dir := target.Target{Name: "codex", Dir: filepath.Join(t.TempDir(), "skills")}

	for _, name := range []string{"../evil", "nested/skill", ".."} {
		t.Run(name, func(t *testing.T) {
			if _, err := linkPathFor(dir, name); err == nil {
				t.Errorf("linkPathFor(%q): nil error, want a refusal", name)
			}
		})
	}

	got, err := linkPathFor(dir, "demo-skill")
	if err != nil {
		t.Fatalf("linkPathFor: %v", err)
	}
	if want := filepath.Join(dir.Dir, "demo-skill"); got != want {
		t.Errorf("linkPathFor = %s, want %s", got, want)
	}
}

// Guards the ordering assumption the whole design rests on: the timestamp a
// link writes has to be readable as "later than install".
func TestLinkStampsAFreshUpdatedAt(t *testing.T) {
	r, _, codex := linkFixture(t)
	r.InstalledAt = time.Now().UTC().Add(-time.Hour)
	r.UpdatedAt = r.InstalledAt

	p, err := linked{}.Link(r, []target.Target{codex})
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	rec := p.Ops[len(p.Ops)-1].(plan.Record)
	if !rec.Receipt.InstalledAt.Equal(r.InstalledAt) {
		t.Errorf("InstalledAt = %v, want it left at %v", rec.Receipt.InstalledAt, r.InstalledAt)
	}
	if !rec.Receipt.UpdatedAt.After(r.InstalledAt) {
		t.Errorf("UpdatedAt = %v, want it after InstalledAt", rec.Receipt.UpdatedAt)
	}
}
