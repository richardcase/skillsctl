package plan

import (
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/state"
)

func TestDescribeRendersEveryOp(t *testing.T) {
	var p Plan
	p.Add(
		Link{Target: "claude", LinkPath: "/h/.claude/skills/foo", RevPath: "/s/rev/x/abc"},
		Relink{Target: "claude", LinkPath: "/h/.claude/skills/foo", RevPath: "/s/rev/x/def"},
		Unlink{Target: "codex", LinkPath: "/h/.codex/skills/foo"},
		Record{Receipt: state.Receipt{Name: "foo", Resolved: "abc1234"}},
		Forget{Name: "bar"},
		Exec{Argv: []string{"claude", "plugin", "install", "x@y"}},
	)

	got := p.Describe()
	if len(got) != 6 {
		t.Fatalf("Describe() produced %d lines, want 6", len(got))
	}

	wantFragments := []string{
		"/h/.claude/skills/foo",
		"/s/rev/x/def",
		"/h/.codex/skills/foo",
		"foo",
		"bar",
		"claude plugin install x@y",
	}
	for i, frag := range wantFragments {
		if !strings.Contains(got[i], frag) {
			t.Errorf("Describe()[%d] = %q, want it to mention %q", i, got[i], frag)
		}
	}
}

func TestIsEmpty(t *testing.T) {
	var p Plan
	if !p.IsEmpty() {
		t.Error("a plan with no ops must be empty")
	}
	p.Add(Forget{Name: "x"})
	if p.IsEmpty() {
		t.Error("a plan with an op must not be empty")
	}
}

func TestNoteDescribesItselfAndChangesNothing(t *testing.T) {
	o := Note{Text: "then link the skills it ships into codex"}
	if got, want := o.Describe(), "note    then link the skills it ships into codex"; got != want {
		t.Errorf("Describe() = %q, want %q", got, want)
	}
}
