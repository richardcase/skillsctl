package manifest

import (
	"context"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/state"
)

// A tag is manifest metadata with no channel-specific meaning, so it is
// stamped onto the Record op planMissing already built rather than teaching
// every channel's Install about a field install itself never sets.
func TestPlanCarriesEntryTagsIntoTheNewReceipt(t *testing.T) {
	f := newPlanFixture(t).agents(t)

	_, p := Plan(context.Background(), f.reg, File{Skills: []Entry{
		{Name: "demo", Source: f.url, Tags: []string{"frontend", "team-a"}},
	}}, &state.DB{Receipts: map[string]*state.Receipt{}}, f.cfg)

	var found bool
	for _, op := range p.Ops {
		rec, ok := op.(plan.Record)
		if !ok {
			continue
		}
		found = true
		if strings.Join(rec.Receipt.Tags, ",") != "frontend,team-a" {
			t.Errorf("receipt tags = %v, want [frontend team-a]", rec.Receipt.Tags)
		}
	}
	if !found {
		t.Fatal("no Record op in the plan")
	}
}

// sync only ever adds, so an entry's tags are stamped once, at first
// install, and never rewritten on a receipt that already exists.
func TestPlanDoesNotRewriteTagsOnAnAlreadyInstalledEntry(t *testing.T) {
	f := newPlanFixture(t).agents(t)
	existing := f.installedReceipt(t, "claude", "codex")
	existing.Tags = []string{"backend"}
	db := &state.DB{Receipts: map[string]*state.Receipt{"demo": existing}}

	rep, p := Plan(context.Background(), f.reg, File{Skills: []Entry{
		{Name: "demo", Source: f.url, Tags: []string{"frontend"}},
	}}, db, f.cfg)

	if len(rep.Verdicts) != 1 || rep.Verdicts[0].Status != StatusPresent {
		t.Fatalf("verdicts = %+v, want one present", rep.Verdicts)
	}
	if !p.IsEmpty() {
		t.Errorf("plan = %+v, want empty: tags never rewrite an existing receipt", p.Ops)
	}
}
