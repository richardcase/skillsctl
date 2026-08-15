package channel

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/store"
)

const pinSha = "9f8e7d6c5b4a39281706f5e4d3c2b1a098765432"

// gitReceipt is a receipt as install would have written it: tracking a ref, at
// a revision in the store, linked into one agent.
func gitReceipt(st *store.Store) state.Receipt {
	return state.Receipt{
		Name:        "alpha",
		Channel:     "git",
		Source:      "https://example.com/o/r.git",
		Slug:        "example.com/o/r",
		Ref:         "develop",
		Resolved:    pinSha,
		RevPath:     st.RevPath("example.com/o/r", pinSha),
		ContentHash: "sha256:abc",
		Links:       []state.Link{{Target: "claude", Path: "/agents/claude/skills/alpha"}},
	}
}

func gitChannel(t *testing.T) (*Git, *store.Store) {
	t.Helper()
	st := store.New(t.TempDir())
	return NewGit(st, nil), st
}

// The whole plan is the record. Nothing is fetched, nothing is re-linked, and
// there is no op in it that could damage an install.
func TestPinPlansTheRecordAndNothingElse(t *testing.T) {
	c, st := gitChannel(t)

	p, _, err := c.Pin(gitReceipt(st), PinOptions{On: true})
	if err != nil {
		t.Fatalf("Pin: %v", err)
	}
	if len(p.Ops) != 1 {
		t.Fatalf("plan = %v, want one op", p.Describe())
	}
	if _, ok := p.Ops[0].(plan.Record); !ok {
		t.Fatalf("op = %T (%s), want a plan.Record", p.Ops[0], p.Ops[0].Describe())
	}
}

func TestPinFreezesTheInstalledRevisionAndDropsTheRef(t *testing.T) {
	c, st := gitChannel(t)
	before := gitReceipt(st)

	_, res, err := c.Pin(before, PinOptions{On: true})
	if err != nil {
		t.Fatalf("Pin: %v", err)
	}

	switch {
	case !res.Changed:
		t.Error("Changed = false, want a pin on an unpinned receipt to be a change")
	case !res.Receipt.Pinned:
		t.Error("Pinned = false, want the receipt pinned")
	case res.Receipt.Ref != "":
		t.Errorf("Ref = %q, want it cleared: a pinned receipt tracks nothing", res.Receipt.Ref)
	case res.Receipt.Resolved != before.Resolved:
		t.Errorf("Resolved = %q, want %q: a pin freezes what is installed", res.Receipt.Resolved, before.Resolved)
	case res.Receipt.RevPath != before.RevPath:
		t.Errorf("RevPath = %q, want it untouched: nothing is re-linked", res.Receipt.RevPath)
	case len(res.Receipt.Links) != len(before.Links):
		t.Errorf("Links = %v, want the agents a pin does not change", res.Receipt.Links)
	case !res.Receipt.UpdatedAt.After(before.UpdatedAt):
		t.Error("UpdatedAt was not stamped")
	}
}

func TestUnpinTracksTheRefItIsGiven(t *testing.T) {
	c, st := gitChannel(t)
	pinned := gitReceipt(st)
	pinned.Pinned, pinned.Ref = true, ""

	for _, tc := range []struct {
		name string
		ref  string
		want string
	}{
		{name: "named ref", ref: "develop", want: "develop"},
		{name: "no ref", ref: "", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, res, err := c.Pin(pinned, PinOptions{Ref: tc.ref})
			if err != nil {
				t.Fatalf("Pin: %v", err)
			}
			if !res.Changed {
				t.Error("Changed = false, want an unpin of a pinned receipt to be a change")
			}
			if res.Receipt.Pinned {
				t.Error("Pinned = true, want it released")
			}
			// An empty ref is the default branch, which is what Update
			// already reads it as.
			if res.Receipt.Ref != tc.want {
				t.Errorf("Ref = %q, want %q", res.Receipt.Ref, tc.want)
			}
		})
	}
}

// Asking for the state a receipt is already in is not an error and not work:
// it plans nothing, so there is nothing to roll back and nothing to commit.
func TestPinIsANoOpWhenNothingWouldChange(t *testing.T) {
	c, st := gitChannel(t)
	unpinned := gitReceipt(st)
	pinned := gitReceipt(st)
	pinned.Pinned, pinned.Ref = true, ""

	for _, tc := range []struct {
		name string
		r    state.Receipt
		o    PinOptions
	}{
		{name: "pin an already pinned skill", r: pinned, o: PinOptions{On: true}},
		{name: "unpin a skill that is not pinned", r: unpinned, o: PinOptions{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, res, err := c.Pin(tc.r, tc.o)
			if err != nil {
				t.Fatalf("Pin: %v", err)
			}
			if res.Changed {
				t.Error("Changed = true, want the receipt reported as already in that state")
			}
			if !p.IsEmpty() {
				t.Errorf("plan = %v, want nothing planned", p.Describe())
			}
		})
	}
}

// adopt pins a skill it found in a working copy so that a plain update cannot
// re-point the user's symlink into the store. Unpinning it is the user asking
// for exactly that, so it is allowed — and said out loud, because it is the one
// case where writing a receipt field changes what a later command does to files
// the user owns.
func TestUnpinNotesAWorkingCopyOutsideTheStore(t *testing.T) {
	c, st := gitChannel(t)
	checkout := filepath.Join(t.TempDir(), "src", "repo", "skills", "alpha")

	adopted := gitReceipt(st)
	adopted.Pinned, adopted.Ref, adopted.RevPath, adopted.ContentHash = true, "", checkout, ""

	_, res, err := c.Pin(adopted, PinOptions{})
	if err != nil {
		t.Fatalf("Pin: %v", err)
	}
	if !strings.Contains(res.Note, checkout) {
		t.Errorf("Note = %q, want it to name the working copy at %s", res.Note, checkout)
	}
	if !strings.Contains(res.Note, "update") {
		t.Errorf("Note = %q, want it to say what the next update will do", res.Note)
	}
}

func TestUnpinSaysNothingAboutARevisionInTheStore(t *testing.T) {
	c, st := gitChannel(t)
	pinned := gitReceipt(st)
	pinned.Pinned, pinned.Ref = true, ""

	_, res, err := c.Pin(pinned, PinOptions{})
	if err != nil {
		t.Fatalf("Pin: %v", err)
	}
	if res.Note != "" {
		t.Errorf("Note = %q, want none: the store owns this revision already", res.Note)
	}
}

// A channel with no revision to freeze refuses rather than recording a pin that
// would mean nothing, in the voice install already uses to refuse --pin.
func TestPinIsRefusedByTheChannelsWithNoRevision(t *testing.T) {
	plugin, _ := newPluginChannel()

	for _, tc := range []struct {
		name string
		ch   Channel
		r    state.Receipt
		want string
	}{
		{
			name: "local",
			ch:   NewLocal(store.New(t.TempDir())),
			r:    state.Receipt{Name: "mine", Channel: "local", RevPath: "/home/me/skills/mine"},
			want: "no revision to pin",
		},
		{
			name: "plugin",
			ch:   plugin,
			r:    state.Receipt{Name: "pack", Channel: "plugin", Resolved: "1.2.0"},
			want: "claude decides which version",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, on := range []bool{true, false} {
				p, _, err := tc.ch.Pin(tc.r, PinOptions{On: on})
				if err == nil {
					t.Fatalf("Pin(On=%v) was accepted; want a refusal", on)
				}
				if !strings.Contains(err.Error(), tc.want) {
					t.Errorf("error = %v, want it to say why this channel has nothing to pin", err)
				}
				if !p.IsEmpty() {
					t.Errorf("plan = %v, want nothing planned by a refusal", p.Describe())
				}
			}
		})
	}
}
