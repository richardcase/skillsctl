// Package registry fetches the curated list of skills `skillsctl search`
// matches against, from a JSON file this repository publishes, with a local
// cache so search still works when the network or GitHub is unavailable.
package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// DefaultURL is where the registry file is fetched from when no override is
// configured.
const DefaultURL = "https://raw.githubusercontent.com/richardcase/skillsctl/main/registry/skills.json"

// DefaultTTL is how long a successful fetch is trusted before Fetch tries the
// network again.
const DefaultTTL = 24 * time.Hour

// Entry is one skill in the registry.
type Entry struct {
	// Name is the skill's display name, matched by search.
	Name string `json:"name"`
	// Source is a plain source.Parse-shaped string: owner/repo or
	// owner/repo/subpath.
	Source string `json:"source"`
	// Description is matched by search alongside Name and Tags.
	Description string `json:"description"`
	// Tags are free-form keywords, matched by search.
	Tags []string `json:"tags,omitempty"`
	// Agents lists which agents the skill declares support for.
	Agents []string `json:"agents,omitempty"`
}

// Registry fetches the current set of registry entries.
type Registry interface {
	// Fetch returns the current registry entries.
	Fetch(ctx context.Context) ([]Entry, error)
}

// HTTP fetches the registry file over HTTP, caching it locally so a later
// call can fall back to the last-known-good set when the network or GitHub
// is unavailable.
type HTTP struct {
	// URL is where the registry file is fetched from. Empty means DefaultURL.
	URL string
	// CachePath is where the last successful fetch is written. Empty means no
	// caching: every call fetches, and a failed fetch has nothing to fall
	// back to.
	CachePath string
	// TTL is how long a cached fetch is trusted before a new one is
	// attempted. Zero means DefaultTTL.
	TTL time.Duration
	// Client makes the HTTP request. Nil means http.DefaultClient.
	Client *http.Client
	// Now reports the current time. Nil means time.Now; tests override it to
	// control TTL expiry without sleeping.
	Now func() time.Time
}

// cacheFile is what CachePath holds: the entries from the last successful
// fetch, and when that fetch happened.
type cacheFile struct {
	FetchedAt time.Time `json:"fetched_at"`
	Entries   []Entry   `json:"entries"`
}

// Fetch returns the current registry entries: from cache when the cache is
// still within TTL, from the network otherwise, and from a stale cache as a
// last resort when the network fails. It errors only when the network fails
// and there is no cache to fall back to.
func (h *HTTP) Fetch(ctx context.Context) ([]Entry, error) {
	cached, cacheErr := h.readCache()
	now := h.now()

	if cacheErr == nil && now.Sub(cached.FetchedAt) < h.ttl() {
		return cached.Entries, nil
	}

	entries, err := h.fetchRemote(ctx)
	if err != nil {
		if cacheErr == nil {
			return cached.Entries, nil
		}
		return nil, fmt.Errorf("fetch registry from %s: %w", h.url(), err)
	}

	// A cache write failure (read-only disk, full disk) doesn't invalidate a
	// successful fetch: the cache is an optimization for the next call, not
	// a requirement for this one, so it is silently best-effort here.
	if h.CachePath != "" {
		_ = h.writeCache(cacheFile{FetchedAt: now, Entries: entries})
	}
	return entries, nil
}

func (h *HTTP) url() string {
	if h.URL != "" {
		return h.URL
	}
	return DefaultURL
}

func (h *HTTP) ttl() time.Duration {
	if h.TTL != 0 {
		return h.TTL
	}
	return DefaultTTL
}

func (h *HTTP) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h *HTTP) client() *http.Client {
	if h.Client != nil {
		return h.Client
	}
	return http.DefaultClient
}

func (h *HTTP) fetchRemote(ctx context.Context) ([]Entry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.url(), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := h.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %s", resp.Status)
	}

	blob, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var entries []Entry
	if err := json.Unmarshal(blob, &entries); err != nil {
		return nil, fmt.Errorf("parse registry: %w", err)
	}
	return entries, nil
}

// readCache errors whenever there is nothing usable to fall back to: no
// CachePath configured, no file yet written, or a file Fetch cannot parse.
func (h *HTTP) readCache() (cacheFile, error) {
	if h.CachePath == "" {
		return cacheFile{}, fmt.Errorf("no cache configured")
	}
	blob, err := os.ReadFile(h.CachePath)
	if err != nil {
		return cacheFile{}, err
	}
	var cf cacheFile
	if err := json.Unmarshal(blob, &cf); err != nil {
		return cacheFile{}, fmt.Errorf("parse cache %s: %w", h.CachePath, err)
	}
	return cf, nil
}

func (h *HTTP) writeCache(cf cacheFile) error {
	blob, err := json.Marshal(cf)
	if err != nil {
		return fmt.Errorf("encode cache: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(h.CachePath), 0o755); err != nil {
		return fmt.Errorf("create cache directory: %w", err)
	}
	return os.WriteFile(h.CachePath, blob, 0o644)
}

// Load reads a registry file directly from disk, with no network call and no
// cache. It is what the registry-check maintenance tool uses to read this
// repository's own registry/skills.json.
func Load(path string) ([]Entry, error) {
	blob, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var entries []Entry
	if err := json.Unmarshal(blob, &entries); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return entries, nil
}
