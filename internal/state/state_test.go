package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/flock"
)

func statePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "nested", "state.json")
}

func TestOpenMissingFileGivesEmptyDB(t *testing.T) {
	h, err := Open(context.Background(), statePath(t), nil)
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

	h, err := Open(context.Background(), p, nil)
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

	h2, err := Open(context.Background(), p, nil)
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

	h, _ := Open(context.Background(), p, nil)
	h.DB.Receipts["ghost"] = &Receipt{Name: "ghost"}
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	h2, err := Open(context.Background(), p, nil)
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

	if _, err := Open(context.Background(), p, nil); err == nil {
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

	h, err := Open(context.Background(), p, nil)
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

	h, err := Open(context.Background(), p, nil)
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

	h2, err := Open(context.Background(), p, nil)
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

// shrinkLockWait lowers lockTimeout and lockPollInterval for a test so it
// does not have to wait out the real defaults, restoring them afterwards.
func shrinkLockWait(t *testing.T) {
	t.Helper()
	oldTimeout, oldPoll := lockTimeout, lockPollInterval
	lockTimeout, lockPollInterval = 200*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { lockTimeout, lockPollInterval = oldTimeout, oldPoll })
}

func TestOpenTimesOutWithUsefulMessageWhenLockIsHeld(t *testing.T) {
	shrinkLockWait(t)
	p := statePath(t)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}

	held := flock.New(p + ".lock")
	ok, err := held.TryLock()
	if err != nil || !ok {
		t.Fatalf("TryLock: ok=%v err=%v", ok, err)
	}
	defer func() { _ = held.Unlock() }()

	var notify bytes.Buffer
	start := time.Now()
	_, err = Open(context.Background(), p, &notify)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Open succeeded against a held lock; want a timeout error")
	}
	if !strings.Contains(err.Error(), p+".lock") {
		t.Errorf("error %q does not name the lock file %s.lock", err, p)
	}
	if elapsed > time.Second {
		t.Errorf("Open took %s; want it bounded by the shrunk lockTimeout", elapsed)
	}
	if !strings.Contains(notify.String(), "waiting for the skillsctl lock") {
		t.Errorf("notify = %q, want a waiting message", notify.String())
	}
}

func TestOpenHonoursContextCancellation(t *testing.T) {
	shrinkLockWait(t)
	// A longer timeout than shrinkLockWait sets, so a pass proves
	// cancellation cut the wait short rather than the timeout doing it.
	lockTimeout = time.Minute
	p := statePath(t)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}

	held := flock.New(p + ".lock")
	ok, err := held.TryLock()
	if err != nil || !ok {
		t.Fatalf("TryLock: ok=%v err=%v", ok, err)
	}
	defer func() { _ = held.Unlock() }()

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)

	start := time.Now()
	_, err = Open(ctx, p, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Open succeeded against a held lock; want a cancellation error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want it to wrap context.Canceled", err)
	}
	if elapsed > time.Second {
		t.Errorf("Open took %s; want cancellation to cut the wait short", elapsed)
	}
}

func TestOpenReportsHoldingPID(t *testing.T) {
	p := statePath(t)

	h, err := Open(context.Background(), p, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = h.Close() }()

	got, found := readHolder(p + ".lock")
	if !found {
		t.Fatal("readHolder found no holder info after Open")
	}
	if got.PID != os.Getpid() {
		t.Errorf("holder PID = %d, want %d", got.PID, os.Getpid())
	}
}
