// Package state persists one receipt per installed skill. A receipt records
// exactly what an install created, so removal never has to infer anything.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/gofrs/flock"
)

// SchemaVersion is the on-disk format version. Bump it only for a breaking
// change, and add a migration when you do.
const SchemaVersion = 1

// Link is a symlink an install created in one agent's skills directory.
type Link struct {
	Target string `json:"target"`
	Path   string `json:"path"`
}

// Receipt records how a skill was installed.
type Receipt struct {
	Name        string    `json:"name"`
	Channel     string    `json:"channel"`
	Source      string    `json:"source"`
	Slug        string    `json:"slug,omitempty"`
	Subpath     string    `json:"subpath,omitempty"`
	Ref         string    `json:"ref,omitempty"`
	Resolved    string    `json:"resolved"`
	Pinned      bool      `json:"pinned,omitempty"`
	Tags        []string  `json:"tags,omitempty"`
	RevPath     string    `json:"revPath"`
	ContentHash string    `json:"contentHash,omitempty"`
	Links       []Link    `json:"links"`
	InstalledAt time.Time `json:"installedAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
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
func Open(path string) (*Handle, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}

	lock := flock.New(path + ".lock")
	if err := lock.Lock(); err != nil {
		return nil, fmt.Errorf("lock state: %w", err)
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
