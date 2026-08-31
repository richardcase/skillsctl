// Package state persists one receipt per installed skill. A receipt records
// exactly what an install created, so removal never has to infer anything.
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/gofrs/flock"
)

// SchemaVersion is the on-disk format version. Bump it only for a breaking
// change, and add a migration when you do.
const SchemaVersion = 1

// lockTimeout bounds how long Open waits for a contended lock before giving
// up. lockPollInterval is how often it checks. Both are variables so a test
// can shrink them rather than actually waiting out the default.
var (
	lockTimeout      = 30 * time.Second
	lockPollInterval = 50 * time.Millisecond
)

// holder is written into the lock file's own content — independent of the
// advisory flock itself — so a waiting process can say who it is waiting
// for.
type holder struct {
	PID   int       `json:"pid"`
	Since time.Time `json:"since"`
}

// readHolder best-effort reads whoever currently holds path. A missing or
// unparsable file (an older skillsctl, or a race with the write below) just
// means the wait message has no name to give.
func readHolder(path string) (holder, bool) {
	blob, err := os.ReadFile(path)
	if err != nil {
		return holder{}, false
	}
	var h holder
	if err := json.Unmarshal(blob, &h); err != nil {
		return holder{}, false
	}
	return h, true
}

// writeHolder records the current process as the lock's holder.
func writeHolder(path string) error {
	blob, err := json.Marshal(holder{PID: os.Getpid(), Since: time.Now()})
	if err != nil {
		return err
	}
	return os.WriteFile(path, blob, 0o644)
}

// Link is a symlink an install created in one agent's skills directory.
type Link struct {
	Target string `json:"target"`
	Path   string `json:"path"`
}

// Receipt records how a skill was installed.
type Receipt struct {
	Name        string   `json:"name"`
	Channel     string   `json:"channel"`
	Source      string   `json:"source"`
	Slug        string   `json:"slug,omitempty"`
	Subpath     string   `json:"subpath,omitempty"`
	Ref         string   `json:"ref,omitempty"`
	Resolved    string   `json:"resolved"`
	Pinned      bool     `json:"pinned,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	RevPath     string   `json:"revPath"`
	ContentHash string   `json:"contentHash,omitempty"`
	// PreviousResolved, PreviousRevPath and PreviousContentHash are what
	// Resolved, RevPath and ContentHash held before the last relink — the
	// git and OCI channels populate them, so rollback has something to
	// swap back to. Empty until a skill has been updated at least once.
	//
	// Only PreviousResolved is load-bearing: rollback resolves and re-extracts
	// that revision, recomputing the path and the hash rather than trusting
	// the two recorded alongside it, which are there to be read.
	PreviousResolved    string    `json:"previousResolved,omitempty"`
	PreviousRevPath     string    `json:"previousRevPath,omitempty"`
	PreviousContentHash string    `json:"previousContentHash,omitempty"`
	Links               []Link    `json:"links"`
	InstalledAt         time.Time `json:"installedAt"`
	UpdatedAt           time.Time `json:"updatedAt"`
}

// DB is the in-memory receipt set.
type DB struct {
	Version  int                 `json:"version"`
	Receipts map[string]*Receipt `json:"receipts"`
}

// List returns every receipt, sorted by name.
func (d *DB) List() []*Receipt {
	out := make([]*Receipt, 0, len(d.Receipts))
	for _, r := range d.Receipts {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Handle is an exclusive session over the receipt DB. The lock is held from
// Open until Close so that read-modify-write is atomic across processes.
type Handle struct {
	DB *DB

	path string
	lock *flock.Flock
}

// Open acquires the lock and loads the DB, treating a missing file as empty.
//
// It tries the lock immediately; if that fails, it writes one line to notify
// (unless nil) naming who holds it, then polls until either it acquires the
// lock, ctx is done, or lockTimeout elapses — whichever comes first — so a
// contended lock reports why it is waiting and eventually gives up instead of
// hanging forever.
func Open(ctx context.Context, path string, notify io.Writer) (*Handle, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}

	lockPath := path + ".lock"
	lock := flock.New(lockPath)
	if err := acquire(ctx, lock, lockPath, notify); err != nil {
		return nil, err
	}
	if err := writeHolder(lockPath); err != nil {
		_ = lock.Unlock()
		return nil, fmt.Errorf("record lock holder: %w", err)
	}

	h := &Handle{path: path, lock: lock}

	blob, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		h.DB = &DB{Version: SchemaVersion, Receipts: map[string]*Receipt{}}
		return h, nil
	case err != nil:
		_ = lock.Unlock()
		return nil, fmt.Errorf("read state: %w", err)
	}

	var db DB
	if err := json.Unmarshal(blob, &db); err != nil {
		_ = lock.Unlock()
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	switch {
	case db.Version == 0:
		// No version field: a hand-written file, or one from a pre-versioned build.
		db.Version = SchemaVersion
	case db.Version > SchemaVersion:
		_ = lock.Unlock()
		return nil, fmt.Errorf("%s was written by a newer skillsctl (schema %d, this build understands %d): upgrade skillsctl", path, db.Version, SchemaVersion)
	case db.Version < SchemaVersion:
		_ = lock.Unlock()
		return nil, fmt.Errorf("%s uses schema %d and this build understands %d, but no migration exists", path, db.Version, SchemaVersion)
	}
	if db.Receipts == nil {
		db.Receipts = map[string]*Receipt{}
	}

	h.DB = &db
	return h, nil
}

// acquire takes lock, reporting via notify and waiting up to lockTimeout if
// it is already held.
func acquire(ctx context.Context, lock *flock.Flock, lockPath string, notify io.Writer) error {
	ok, err := lock.TryLock()
	if err != nil {
		return fmt.Errorf("lock state: %w", err)
	}
	if ok {
		return nil
	}

	if notify != nil {
		if h, found := readHolder(lockPath); found {
			_, _ = fmt.Fprintf(notify, "waiting for the skillsctl lock at %s (held by pid %d since %s)...\n", lockPath, h.PID, h.Since.Format(time.RFC3339))
		} else {
			_, _ = fmt.Fprintf(notify, "waiting for the skillsctl lock at %s...\n", lockPath)
		}
	}

	waitCtx, cancel := context.WithTimeout(ctx, lockTimeout)
	defer cancel()

	ok, waitErr := lock.TryLockContext(waitCtx, lockPollInterval)
	if ok {
		return nil
	}

	// TryLockContext returns the *derived* waitCtx's error (canceled or
	// deadline-exceeded) whenever it gives up without a lock — never nil —
	// so waitErr alone can't tell our own timeout apart from the caller's
	// cancellation. Check the caller's ctx directly for that.
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("lock state: %w", err)
	}
	if waitErr != nil && !errors.Is(waitErr, context.DeadlineExceeded) {
		return fmt.Errorf("lock state: %w", waitErr)
	}
	return fmt.Errorf("timed out after %s waiting for the skillsctl lock at %s: if the holding process is no longer running, remove that file", lockTimeout, lockPath)
}

// Commit writes the DB atomically. Changes are lost unless Commit is called.
func (h *Handle) Commit() error {
	blob, err := json.MarshalIndent(h.DB, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	blob = append(blob, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(h.path), ".state-*.json")
	if err != nil {
		return fmt.Errorf("create temp state: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	if _, err := tmp.Write(blob); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp state: %w", err)
	}
	if err := os.Rename(tmp.Name(), h.path); err != nil {
		return fmt.Errorf("replace state: %w", err)
	}
	return nil
}

// Close releases the lock. Safe to call more than once.
func (h *Handle) Close() error {
	if h.lock == nil {
		return nil
	}
	err := h.lock.Unlock()
	h.lock = nil
	return err
}
