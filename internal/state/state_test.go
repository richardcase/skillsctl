package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func statePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "nested", "state.json")
}

func TestOpenMissingFileGivesEmptyDB(t *testing.T) {
	h, err := Open(statePath(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = h.Close() }()

	if h.DB.Version != SchemaVersion {
		t.Errorf("Version = %d, want %d", h.DB.Version, SchemaVersion)
	}
	if len(h.DB.Receipts) != 0 {
		t.Errorf("Receipts = %v, want empty", h.DB.Receipts)
	}
}

func TestCommitThenReopenRoundTrips(t *testing.T) {
	p := statePath(t)
	now := time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC)

	h, err := Open(p)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	h.DB.Receipts["avoid-ai-writing"] = &Receipt{
		Name:        "avoid-ai-writing",
		Channel:     "git",
		Source:      "https://github.com/conorbronsdon/avoid-ai-writing.git",
		Slug:        "github.com/conorbronsdon/avoid-ai-writing",
		Ref:         "main",
		Resolved:    "a1b2c3d",
		RevPath:     "/store/rev/x/a1b2c3d",
		ContentHash: "deadbeef",
		Links:       []Link{{Target: "claude", Path: "/home/x/.claude/skills/avoid-ai-writing"}},
		InstalledAt: now,
		UpdatedAt:   now,
	}
	if err := h.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	h2, err := Open(p)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = h2.Close() }()

	got, ok := h2.DB.Receipts["avoid-ai-writing"]
	if !ok {
		t.Fatal("receipt did not survive the round trip")
	}
	if got.Resolved != "a1b2c3d" {
		t.Errorf("Resolved = %q, want a1b2c3d", got.Resolved)
	}
	if len(got.Links) != 1 || got.Links[0].Target != "claude" {
		t.Errorf("Links = %+v, want one claude link", got.Links)
	}
	if !got.InstalledAt.Equal(now) {
		t.Errorf("InstalledAt = %v, want %v", got.InstalledAt, now)
	}
}

func TestCloseWithoutCommitDiscardsChanges(t *testing.T) {
	p := statePath(t)

	h, _ := Open(p)
	h.DB.Receipts["ghost"] = &Receipt{Name: "ghost"}
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	h2, err := Open(p)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = h2.Close() }()
	if _, ok := h2.DB.Receipts["ghost"]; ok {
		t.Error("uncommitted change was persisted")
	}
}

func TestOpenRejectsNewerSchema(t *testing.T) {
	p := statePath(t)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	blob, _ := json.Marshal(map[string]any{"version": SchemaVersion + 1, "receipts": map[string]any{}})
	if err := os.WriteFile(p, blob, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Open(p); err == nil {
		t.Fatal("Open accepted a newer schema version; want an error telling the user to upgrade")
	}
}

func TestOpenAcceptsVersionlessFile(t *testing.T) {
	p := statePath(t)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"receipts":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	h, err := Open(p)
	if err != nil {
		t.Fatalf("Open rejected a file with no version field: %v", err)
	}
	defer func() { _ = h.Close() }()

	if h.DB.Version != SchemaVersion {
		t.Errorf("Version = %d, want %d", h.DB.Version, SchemaVersion)
	}
}

func TestListIsSortedByName(t *testing.T) {
	db := &DB{Version: SchemaVersion, Receipts: map[string]*Receipt{
		"zulu":  {Name: "zulu"},
		"alpha": {Name: "alpha"},
		"mike":  {Name: "mike"},
	}}
	got := db.List()
	want := []string{"alpha", "mike", "zulu"}
	for i, r := range got {
		if r.Name != want[i] {
			t.Fatalf("List()[%d] = %q, want %q", i, r.Name, want[i])
		}
	}
}

func TestCommitThenReopenRoundTripsPreviousRevision(t *testing.T) {
	p := statePath(t)
	now := time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC)

	h, err := Open(p)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	h.DB.Receipts["avoid-ai-writing"] = &Receipt{
		Name:                "avoid-ai-writing",
		Channel:             "git",
		Source:              "https://github.com/conorbronsdon/avoid-ai-writing.git",
		Slug:                "github.com/conorbronsdon/avoid-ai-writing",
		Ref:                 "main",
		Resolved:            "b2c3d4e",
		PreviousResolved:    "a1b2c3d",
		PreviousRevPath:     "/store/rev/x/a1b2c3d",
		PreviousContentHash: "cafef00d",
		RevPath:             "/store/rev/x/b2c3d4e",
		ContentHash:         "deadbeef",
		InstalledAt:         now,
		UpdatedAt:           now,
	}
	if err := h.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	h2, err := Open(p)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = h2.Close() }()

	got, ok := h2.DB.Receipts["avoid-ai-writing"]
	if !ok {
		t.Fatal("receipt did not survive the round trip")
	}
	if got.PreviousResolved != "a1b2c3d" {
		t.Errorf("PreviousResolved = %q, want a1b2c3d", got.PreviousResolved)
	}
	if got.PreviousRevPath != "/store/rev/x/a1b2c3d" {
		t.Errorf("PreviousRevPath = %q, want /store/rev/x/a1b2c3d", got.PreviousRevPath)
	}
	if got.PreviousContentHash != "cafef00d" {
		t.Errorf("PreviousContentHash = %q, want cafef00d", got.PreviousContentHash)
	}
}

func TestReceiptWithNoPreviousRevisionOmitsTheFieldsFromJSON(t *testing.T) {
	blob, err := json.Marshal(Receipt{Name: "fresh", Resolved: "abc", RevPath: "/x"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(blob), "previousResolved") {
		t.Errorf("a receipt with no previous revision should omit it from JSON, got: %s", blob)
	}
}
