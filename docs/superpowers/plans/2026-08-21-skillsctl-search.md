# skillsctl search Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a user find a skill without already knowing its `owner/repo`, via `skillsctl search <query>` against a registry this repo curates and publishes.

**Architecture:** A new `internal/registry` package fetches `registry/skills.json` over HTTP with a local cache, following the existing "network-facing thing gets a package and an interface" pattern (`gitx`, `claudex`, `ocix`). A new `search` CLI command matches the query against the fetched entries and prints candidate sources. A new `cmd/registry-check` maintenance tool, run by a scheduled GitHub Actions workflow, validates existing entries and flags new candidates from `heilcheng/awesome-agent-skills` — it never writes to `registry/skills.json` itself.

**Tech Stack:** Go 1.25, `net/http` (stdlib, no new dependency), Cobra, `encoding/json`, GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-08-21-skillsctl-search-design.md`

## Global Constraints

- No new Go dependency — `net/http` and `encoding/json` are stdlib; do not add an HTTP client library.
- Tests use the standard library only: table-driven, `t.TempDir()`, `t.Setenv`, `httptest.Server`. Never `t.Parallel()`.
- Every exported identifier gets a doc comment (revive is enforced by `golangci-lint`).
- Errors: `fmt.Errorf` with `%w`, lowercase verb-first prefix naming the operation and the path.
- `registry/skills.json` is never written by any code in this plan — only `Load`/`Fetch` (read paths). Populating it with real entries is curation work tracked separately.
- `make test && make lint && make tidy-check` must pass before each commit that changes Go code.

---

## Task 1: `internal/registry` package

**Files:**
- Create: `internal/registry/registry.go`
- Test: `internal/registry/registry_test.go`

**Interfaces:**
- Produces:
  - `type Entry struct { Name, Source, Description string; Tags, Agents []string }` (JSON tags: `name`, `source`, `description`, `tags,omitempty`, `agents,omitempty`)
  - `type Registry interface { Fetch(ctx context.Context) ([]Entry, error) }`
  - `type HTTP struct { URL, CachePath string; TTL time.Duration; Client *http.Client; Now func() time.Time }`, implementing `Registry`
  - `const DefaultURL = "https://raw.githubusercontent.com/richardcase/skillsctl/main/registry/skills.json"`
  - `const DefaultTTL = 24 * time.Hour`
  - `func Load(path string) ([]Entry, error)` — reads a registry file directly from disk, no network, no cache

- [ ] **Step 1: Write the failing tests**

Create `internal/registry/registry_test.go`:

```go
package registry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHTTPFetchFromNetworkAndCaches(t *testing.T) {
	entries := []Entry{{Name: "demo", Source: "owner/repo", Description: "A demo skill"}}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(entries)
	}))
	defer srv.Close()

	cache := filepath.Join(t.TempDir(), "cache.json")
	h := &HTTP{URL: srv.URL, CachePath: cache}

	got, err := h.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got) != 1 || got[0].Name != "demo" {
		t.Errorf("Fetch = %+v, want one entry named demo", got)
	}
	if calls != 1 {
		t.Errorf("server called %d times, want 1", calls)
	}
	if _, err := os.Stat(cache); err != nil {
		t.Errorf("cache file not written: %v", err)
	}
}

func TestHTTPFetchUsesCacheWithinTTL(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		http.Error(w, "should not be called", http.StatusInternalServerError)
	}))
	defer srv.Close()

	cache := filepath.Join(t.TempDir(), "cache.json")
	writeCacheFixture(t, cache, cacheFile{
		FetchedAt: time.Now(),
		Entries:   []Entry{{Name: "cached", Source: "owner/repo"}},
	})

	h := &HTTP{URL: srv.URL, CachePath: cache, TTL: time.Hour}
	got, err := h.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got) != 1 || got[0].Name != "cached" {
		t.Errorf("Fetch = %+v, want the cached entry", got)
	}
	if calls != 0 {
		t.Errorf("server called %d times, want 0 (should have used the cache)", calls)
	}
}

func TestHTTPFetchFallsBackToStaleCacheOnNetworkFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	cache := filepath.Join(t.TempDir(), "cache.json")
	writeCacheFixture(t, cache, cacheFile{
		FetchedAt: time.Now().Add(-48 * time.Hour),
		Entries:   []Entry{{Name: "stale", Source: "owner/repo"}},
	})

	h := &HTTP{URL: srv.URL, CachePath: cache, TTL: time.Hour}
	got, err := h.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v, want a fallback to the stale cache instead of an error", err)
	}
	if len(got) != 1 || got[0].Name != "stale" {
		t.Errorf("Fetch = %+v, want the stale cached entry", got)
	}
}

func TestHTTPFetchErrorsWhenNoCacheAndNetworkFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	h := &HTTP{URL: srv.URL, CachePath: filepath.Join(t.TempDir(), "cache.json")}
	if _, err := h.Fetch(context.Background()); err == nil {
		t.Fatal("Fetch: want an error, got nil")
	}
}

func TestHTTPFetchWithoutCachePathAlwaysHitsNetwork(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode([]Entry{{Name: "demo", Source: "owner/repo"}})
	}))
	defer srv.Close()

	h := &HTTP{URL: srv.URL}
	if _, err := h.Fetch(context.Background()); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if _, err := h.Fetch(context.Background()); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if calls != 2 {
		t.Errorf("server called %d times, want 2 (no cache configured)", calls)
	}
}

func TestLoadReadsRegistryFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skills.json")
	if err := os.WriteFile(path, []byte(`[{"name":"demo","source":"owner/repo","description":"A demo"}]`), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 || got[0].Name != "demo" {
		t.Errorf("Load = %+v, want one entry named demo", got)
	}
}

func TestLoadRejectsInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skills.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("Load: want an error for invalid JSON, got nil")
	}
}

// writeCacheFixture writes a cacheFile directly, bypassing HTTP.writeCache,
// so a test can set up a cache the code under test did not itself produce.
func writeCacheFixture(t *testing.T, path string, cf cacheFile) {
	t.Helper()
	blob, err := json.Marshal(cf)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, blob, 0o644); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/registry/...`
Expected: FAIL — the package does not exist yet (`no such file or directory` / build failure, since `registry.go` has not been created).

- [ ] **Step 3: Write the implementation**

Create `internal/registry/registry.go`:

```go
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

	if h.CachePath != "" {
		if werr := h.writeCache(cacheFile{FetchedAt: now, Entries: entries}); werr != nil {
			return nil, fmt.Errorf("write registry cache %s: %w", h.CachePath, werr)
		}
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
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/registry/...`
Expected: PASS, all 7 tests.

- [ ] **Step 5: Commit**

```bash
git add internal/registry/registry.go internal/registry/registry_test.go
git commit -m "feat(registry): add the skill registry fetch-and-cache client"
```

---

## Task 2: `registry/skills.json` and the `[registry]` config table

**Files:**
- Create: `registry/skills.json`
- Modify: `internal/target/target.go`
- Test: `internal/target/target_test.go` (existing file — add cases)

**Interfaces:**
- Consumes: nothing new (this task only touches config plumbing)
- Produces:
  - `type RegistryConfig struct { URL string }` (TOML tag: `url`)
  - `Config.Registry RegistryConfig` (TOML tag: `registry`), zero value when absent from `config.toml`

- [ ] **Step 1: Create the seed registry file**

Create `registry/skills.json`:

```json
[]
```

- [ ] **Step 2: Write the failing test**

Find the existing `TestLoad...` tests in `internal/target/target_test.go` (run `grep -n "^func Test" internal/target/target_test.go` to see current names — add a new test alongside them rather than editing existing ones) and append:

```go
func TestLoadParsesRegistryTable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := "[[target]]\nname = \"claude\"\ndir = \"" + filepath.Join(dir, "skills") + "\"\n\n" +
		"[registry]\nurl = \"https://example.com/skills.json\"\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Registry.URL != "https://example.com/skills.json" {
		t.Errorf("Registry.URL = %q, want %q", cfg.Registry.URL, "https://example.com/skills.json")
	}
}

func TestLoadLeavesRegistryEmptyWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := "[[target]]\nname = \"claude\"\ndir = \"" + filepath.Join(dir, "skills") + "\"\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Registry.URL != "" {
		t.Errorf("Registry.URL = %q, want empty", cfg.Registry.URL)
	}
}
```

(If `internal/target/target_test.go` does not already import `"os"` and `"path/filepath"`, they are already used elsewhere in that file per `target.go`'s own imports — check with `head -15 internal/target/target_test.go` and add only what is missing.)

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/target/... -run TestLoadParsesRegistryTable`
Expected: FAIL — `cfg.Registry` does not exist yet (build failure).

- [ ] **Step 4: Add the config field**

In `internal/target/target.go`, add after the `Target` struct (around line 23) and update `Config`:

```go
// RegistryConfig configures where `skillsctl search` fetches the skill
// registry from.
type RegistryConfig struct {
	URL string `toml:"url"`
}
```

Change the `Config` struct (target.go:26-28) to:

```go
// Config is the set of agents skillsctl knows about, plus where it fetches
// the skill registry from.
type Config struct {
	Targets  []Target       `toml:"target"`
	Registry RegistryConfig `toml:"registry"`
}
```

No change is needed to `Default()`, `Load()`, `Present()`, `WithPlugins()`, `WithoutPlugins()`, `Resolve()` or `Select()` — an absent `[registry]` table decodes to the zero-value `RegistryConfig{}`, and `internal/registry.HTTP` already treats an empty `URL` as `DefaultURL`.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/target/...`
Expected: PASS, including every pre-existing test in the package.

- [ ] **Step 6: Commit**

```bash
git add registry/skills.json internal/target/target.go internal/target/target_test.go
git commit -m "feat(target): add a [registry] config table for skillsctl search"
```

---

## Task 3: `skillsctl search` command

**Files:**
- Create: `internal/cli/search.go`
- Create: `internal/cli/search_test.go`
- Modify: `internal/cli/context.go`
- Modify: `internal/cli/root.go`
- Modify: `internal/cli/cli_test.go`
- Modify: `README.md`

**Interfaces:**
- Consumes: `registry.Registry`, `registry.Entry` (Task 1); `target.Config.Registry.URL` (Task 2); `env.cfg`, `env.store.Root`, `newEnv()` (existing, `internal/cli/context.go`)
- Produces: `newSearchCmd() *cobra.Command`; `runSearch(cmd *cobra.Command, query string, asJSON bool) error`; `matchEntries(entries []registry.Entry, query string) []registry.Entry`; `(*env).registry() registry.Registry`; package var `newRegistry func(cfg target.Config, storeRoot string) registry.Registry`

- [ ] **Step 1: Write the failing tests**

Create `internal/cli/search_test.go`:

```go
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/registry"
)

// fakeRegistry stands in for the network. h.registry defaults to
// refusingRegistry, so a test that forgets to set it finds out immediately
// rather than silently reaching for the real network.
type fakeRegistry struct {
	entries []registry.Entry
	err     error
}

func (f *fakeRegistry) Fetch(context.Context) ([]registry.Entry, error) {
	return f.entries, f.err
}

// refusingRegistry is newHarness's default for the registry seam.
type refusingRegistry struct{}

func (refusingRegistry) Fetch(context.Context) ([]registry.Entry, error) {
	return nil, errors.New("this test has not configured a registry (set h.registry)")
}

func TestSearchPrintsMatchingEntries(t *testing.T) {
	h := newHarness(t)
	h.registry = &fakeRegistry{entries: []registry.Entry{
		{Name: "frontend-design", Source: "anthropics/skills/frontend-design", Description: "Generates distinctive frontend interfaces"},
		{Name: "terraform-style-guide", Source: "hashicorp/skills/terraform-style-guide", Description: "Terraform HCL style conventions"},
	}}

	out, err := h.run(t, "search", "frontend")
	if err != nil {
		t.Fatalf("search: %v\n%s", err, out)
	}
	if !strings.Contains(out, "frontend-design") {
		t.Errorf("output missing the match:\n%s", out)
	}
	if strings.Contains(out, "terraform-style-guide") {
		t.Errorf("output should not include a non-match:\n%s", out)
	}
}

func TestSearchMatchesByDescriptionAndTag(t *testing.T) {
	h := newHarness(t)
	h.registry = &fakeRegistry{entries: []registry.Entry{
		{Name: "alpha", Source: "owner/alpha", Description: "Does nothing special"},
		{Name: "beta", Source: "owner/beta", Description: "Reviews pull requests", Tags: []string{"code-review"}},
	}}

	out, err := h.run(t, "search", "review")
	if err != nil {
		t.Fatalf("search: %v\n%s", err, out)
	}
	if !strings.Contains(out, "beta") {
		t.Errorf("output missing the description match:\n%s", out)
	}

	out, err = h.run(t, "search", "code-review")
	if err != nil {
		t.Fatalf("search: %v\n%s", err, out)
	}
	if !strings.Contains(out, "beta") {
		t.Errorf("output missing the tag match:\n%s", out)
	}
}

func TestSearchReportsNoMatches(t *testing.T) {
	h := newHarness(t)
	h.registry = &fakeRegistry{entries: []registry.Entry{{Name: "alpha", Source: "owner/alpha"}}}

	out, err := h.run(t, "search", "nonexistent")
	if err != nil {
		t.Fatalf("search: %v\n%s", err, out)
	}
	if !strings.Contains(out, "No skills found") {
		t.Errorf("output = %q, want a no-matches message", out)
	}
}

func TestSearchJSON(t *testing.T) {
	h := newHarness(t)
	h.registry = &fakeRegistry{entries: []registry.Entry{
		{Name: "alpha", Source: "owner/alpha", Description: "A skill"},
	}}

	out, err := h.run(t, "search", "alpha", "--json")
	if err != nil {
		t.Fatalf("search: %v\n%s", err, out)
	}

	var got []registry.Entry
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode JSON output: %v\n%s", err, out)
	}
	if len(got) != 1 || got[0].Name != "alpha" {
		t.Errorf("decoded = %+v, want one entry named alpha", got)
	}
}

func TestSearchPropagatesRegistryError(t *testing.T) {
	h := newHarness(t)
	h.registry = &fakeRegistry{err: errors.New("network unreachable")}

	_, err := h.run(t, "search", "anything")
	if err == nil {
		t.Fatal("search: want an error, got nil")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/cli/... -run TestSearch`
Expected: FAIL — `newSearchCmd`, `h.registry` and the `search` subcommand do not exist yet (build failure).

- [ ] **Step 3: Add the `registry` seam to the test harness**

In `internal/cli/cli_test.go`:

Add `"github.com/richardcase/skillsctl/internal/registry"` and
`"github.com/richardcase/skillsctl/internal/target"` to the import block
(around line 14-19, alphabetically among the internal imports — `target` is
not yet imported in this file, only in `context.go`).

Add a field to the `harness` struct (after the `cosign` field, around line 48):

```go
	// registry answers every registry.Fetch call for the duration of the
	// test. It defaults to a fake that refuses, so a test that means to
	// exercise search must say so by setting h.registry.
	registry registry.Registry
```

Initialize it in `newHarness` where `oci` and `cosign` are initialized (around line 128):

```go
		registry: refusingRegistry{},
```

Add it to the seam swap-and-restore block (around lines 139-152):

```go
	realPlugins, realRunner, realPicker, realOCI, realCosign, realRegistry := newPlugins, newRunner, newPicker, newOCI, newCosign, newRegistry
	newPlugins = func() claudex.Plugins { return h.plugins }
	newPicker = func() picker { return h.picker }
	newOCI = func() ocix.OCI { return h.oci }
	newCosign = func() cosignx.Cosign { return h.cosign }
	newRegistry = func(target.Config, string) registry.Registry { return h.registry }
	newRunner = func() func(context.Context, []string) error {
		return func(_ context.Context, argv []string) error {
			h.ran = append(h.ran, argv)
			return h.plugins.exec(argv)
		}
	}
	t.Cleanup(func() {
		newPlugins, newRunner, newPicker, newOCI, newCosign, newRegistry = realPlugins, realRunner, realPicker, realOCI, realCosign, realRegistry
	})
```


- [ ] **Step 4: Add the `newRegistry` seam**

In `internal/cli/context.go`, add to the imports (alphabetically, around line 7-15):

```go
	"path/filepath"

	"github.com/richardcase/skillsctl/internal/registry"
```

(`"path/filepath"` joins the stdlib group at the top; `registry` joins the internal-package group.)

Add after `newCosign` (around line 34):

```go
// newRegistry builds the client search fetches the skill registry through.
// Tests replace it, so no test reaches the real network. SKILLSCTL_REGISTRY_URL
// overrides both the config file and the built-in default, mainly so tests
// and self-hosted mirrors do not depend on GitHub.
var newRegistry = func(cfg target.Config, storeRoot string) registry.Registry {
	url := os.Getenv("SKILLSCTL_REGISTRY_URL")
	if url == "" {
		url = cfg.Registry.URL
	}
	return &registry.HTTP{URL: url, CachePath: filepath.Join(storeRoot, "registry-cache.json")}
}
```

Add a method on `env` alongside `channels()` (at the end of the file):

```go
// registry builds the client search fetches the skill registry through, bound
// to this environment's config and store, the same way channels() is bound to
// them.
func (e *env) registry() registry.Registry {
	return newRegistry(e.cfg, e.store.Root)
}
```

- [ ] **Step 5: Write the command**

Create `internal/cli/search.go`:

```go
package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/richardcase/skillsctl/internal/registry"
	"github.com/spf13/cobra"
)

func newSearchCmd() *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Find skills in the registry by name, description or tag",
		Long: "Search skillsctl's curated registry for skills matching query, printing a\n" +
			"source for each match that can be passed straight to `skillsctl install`.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSearch(cmd, args[0], asJSON)
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the matches as JSON")
	return cmd
}

func runSearch(cmd *cobra.Command, query string, asJSON bool) error {
	e, err := newEnv()
	if err != nil {
		return err
	}

	entries, err := e.registry().Fetch(cmd.Context())
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}
	matches := matchEntries(entries, query)

	out := cmd.OutOrStdout()

	if asJSON {
		blob, err := json.MarshalIndent(matches, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, string(blob))
		return err
	}

	if len(matches) == 0 {
		_, err := fmt.Fprintf(out, "No skills found matching %q.\n", query)
		return err
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tSOURCE\tDESCRIPTION")
	for _, e := range matches {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", e.Name, e.Source, e.Description)
	}
	return w.Flush()
}

// matchEntries returns the entries whose name, description or any tag
// contains query, case-insensitively, preserving the registry's order.
func matchEntries(entries []registry.Entry, query string) []registry.Entry {
	q := strings.ToLower(query)
	var out []registry.Entry
	for _, e := range entries {
		if strings.Contains(strings.ToLower(e.Name), q) ||
			strings.Contains(strings.ToLower(e.Description), q) ||
			matchesAnyTag(e.Tags, q) {
			out = append(out, e)
		}
	}
	return out
}

func matchesAnyTag(tags []string, q string) bool {
	for _, t := range tags {
		if strings.Contains(strings.ToLower(t), q) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 6: Register the command**

In `internal/cli/root.go`, add `newSearchCmd(),` to the `root.AddCommand(...)` list (line 18-40), alphabetically between `newRollbackCmd(),` and `newSyncCmd(),`.

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test ./internal/cli/... -run TestSearch`
Expected: PASS, all 5 tests.

Run: `go test ./...`
Expected: PASS — confirms the harness change did not break any other command's tests.

- [ ] **Step 8: Update the README**

In `README.md`:

Add a bullet to **Features** (after the "One store, every agent." bullet, around line 49):

```markdown
- **Find a skill without knowing owner/repo.** `skillsctl search <query>`
  matches against a curated registry by name, description and tags, printing
  a source for each match that can be passed straight to `skillsctl install`.
```

Add a row to the **Commands** table (line ~644-666), as the first row:

```markdown
| `search <query>` | `--json` | Find skills in the registry by name, description or tag |
```

Add to **Configuration** (after the existing `config.toml` example and its explanation, around line 807):

```markdown
`skillsctl search` fetches its registry from GitHub, configurable via a
`[registry]` table:

```toml
[registry]
url = "https://raw.githubusercontent.com/richardcase/skillsctl/main/registry/skills.json"
```

`SKILLSCTL_REGISTRY_URL` overrides both the config file and the built-in
default, mainly for testing against a self-hosted mirror. A successful fetch
is cached at `<store root>/registry-cache.json`, used when the network or
GitHub is unavailable.
```

- [ ] **Step 9: Commit**

```bash
git add internal/cli/search.go internal/cli/search_test.go internal/cli/context.go internal/cli/root.go internal/cli/cli_test.go README.md
git commit -m "feat(cli): add skillsctl search"
```

---

## Task 4: `cmd/registry-check` maintenance tool

**Files:**
- Create: `cmd/registry-check/main.go`
- Test: `cmd/registry-check/main_test.go`

**Interfaces:**
- Consumes: `registry.Load` (Task 1); `gitx.Git`, `gitx.New()`, `gitx.Origin` (existing, `internal/gitx`); `source.Parse`, `source.ChannelGit` (existing, `internal/source`)
- Produces: `func run(ctx context.Context, registryPath string, g gitx.Git, client *http.Client, readmeURL string) (string, error)` — the tool's testable core, called by `main()`

- [ ] **Step 1: Write the failing tests**

Create `cmd/registry-check/main_test.go`:

```go
package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/gitx"
)

// fakeGit implements gitx.Git with only Resolve wired up — the only method
// registry-check calls.
type fakeGit struct {
	resolve func(ctx context.Context, repoURL, ref string) (string, error)
}

func (f *fakeGit) Resolve(ctx context.Context, repoURL, ref string) (string, error) {
	return f.resolve(ctx, repoURL, ref)
}
func (f *fakeGit) Mirror(context.Context, string, string) error { panic("not used by registry-check") }
func (f *fakeGit) Extract(context.Context, string, string, string) error {
	panic("not used by registry-check")
}
func (f *fakeGit) Describe(context.Context, string) (gitx.Origin, error) {
	panic("not used by registry-check")
}
func (f *fakeGit) Diff(context.Context, string, string, string, ...string) (string, error) {
	panic("not used by registry-check")
}
func (f *fakeGit) DiffDirs(context.Context, string, string) (string, error) {
	panic("not used by registry-check")
}

func writeRegistry(t *testing.T, entriesJSON string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "skills.json")
	if err := os.WriteFile(path, []byte(entriesJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readmeServer(t *testing.T, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestRunReportsBrokenEntry(t *testing.T) {
	path := writeRegistry(t, `[{"name":"gone","source":"owner/gone"}]`)
	g := &fakeGit{resolve: func(context.Context, string, string) (string, error) {
		return "", errors.New("repository not found")
	}}

	report, err := run(context.Background(), path, g, http.DefaultClient, readmeServer(t, ""))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(report, "Broken entries") || !strings.Contains(report, "gone") {
		t.Errorf("report = %q, want it to flag the broken entry", report)
	}
}

func TestRunReportsNewCandidates(t *testing.T) {
	path := writeRegistry(t, `[]`)
	g := &fakeGit{resolve: func(context.Context, string, string) (string, error) { return "sha", nil }}
	readme := `[anthropics/skill-creator](https://agent-skill.co/anthropics/skills/skill-creator) - some skill`

	report, err := run(context.Background(), path, g, http.DefaultClient, readmeServer(t, readme))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(report, "New candidates") || !strings.Contains(report, "anthropics/skill-creator") {
		t.Errorf("report = %q, want it to list the new candidate", report)
	}
}

func TestRunSkipsKnownCandidatesAndDedupes(t *testing.T) {
	path := writeRegistry(t, `[{"name":"skill-creator","source":"anthropics/skills/skill-creator"}]`)
	g := &fakeGit{resolve: func(context.Context, string, string) (string, error) { return "sha", nil }}
	readme := `[anthropics/skill-creator](https://agent-skill.co/anthropics/skills/skill-creator) - one
[anthropics/skill-creator](https://agent-skill.co/anthropics/skills/skill-creator) - duplicate link
[openai/pdf](https://agent-skill.co/openai/skills/pdf) - a different, new one`

	report, err := run(context.Background(), path, g, http.DefaultClient, readmeServer(t, readme))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(report, "skill-creator") {
		t.Errorf("report = %q, should not list a candidate already in the registry", report)
	}
	if strings.Count(report, "openai/pdf") != 1 {
		t.Errorf("report = %q, want openai/pdf listed exactly once", report)
	}
}

func TestRunReturnsEmptyReportWhenClean(t *testing.T) {
	path := writeRegistry(t, `[{"name":"skill-creator","source":"anthropics/skills/skill-creator"}]`)
	g := &fakeGit{resolve: func(context.Context, string, string) (string, error) { return "sha", nil }}
	readme := `[anthropics/skill-creator](https://agent-skill.co/anthropics/skills/skill-creator) - already known`

	report, err := run(context.Background(), path, g, http.DefaultClient, readmeServer(t, readme))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if report != "" {
		t.Errorf("report = %q, want empty when nothing to flag", report)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./cmd/registry-check/...`
Expected: FAIL — the package does not exist yet (build failure).

- [ ] **Step 3: Write the implementation**

Create `cmd/registry-check/main.go`:

```go
// Command registry-check validates registry/skills.json against the network
// and reports candidate skills seen in heilcheng/awesome-agent-skills but not
// yet in the registry, for the scheduled registry-refresh workflow to turn
// into a tracking issue. It never modifies registry/skills.json: resolving a
// candidate name to a real owner/repo stays a human, PR-reviewed decision,
// since several of that list's entries are monorepo subpaths a name alone
// cannot disambiguate.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/richardcase/skillsctl/internal/registry"
	"github.com/richardcase/skillsctl/internal/source"
)

// awesomeListReadmeURL is the curation source new candidates are read from.
const awesomeListReadmeURL = "https://raw.githubusercontent.com/heilcheng/awesome-agent-skills/main/README.md"

func main() {
	registryPath := flag.String("registry", "registry/skills.json", "path to the registry file to check")
	flag.Parse()

	report, err := run(context.Background(), *registryPath, gitx.New(), http.DefaultClient, awesomeListReadmeURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "registry-check: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(report)
}

// run loads registryPath, validates every entry's source against the
// network via g, finds candidates in the README at readmeURL not already
// present, and renders both as a Markdown report.
func run(ctx context.Context, registryPath string, g gitx.Git, client *http.Client, readmeURL string) (string, error) {
	entries, err := registry.Load(registryPath)
	if err != nil {
		return "", fmt.Errorf("load %s: %w", registryPath, err)
	}

	broken := validate(ctx, g, entries)
	candidates, err := newCandidates(ctx, client, readmeURL, entries)
	if err != nil {
		return "", fmt.Errorf("find new candidates: %w", err)
	}

	return report(broken, candidates), nil
}

// brokenEntry is a registry entry whose source no longer resolves.
type brokenEntry struct {
	Name   string
	Source string
	Reason string
}

// validate resolves every git-channel entry's source against the network,
// returning the ones that no longer do. A plugin or local source names
// nothing this tool can check over the network, so it is skipped rather than
// flagged.
func validate(ctx context.Context, g gitx.Git, entries []registry.Entry) []brokenEntry {
	var broken []brokenEntry
	for _, e := range entries {
		src, err := source.Parse(e.Source)
		if err != nil {
			broken = append(broken, brokenEntry{Name: e.Name, Source: e.Source, Reason: err.Error()})
			continue
		}
		if src.Channel != source.ChannelGit {
			continue
		}
		if _, err := g.Resolve(ctx, src.RepoURL, ""); err != nil {
			broken = append(broken, brokenEntry{Name: e.Name, Source: e.Source, Reason: err.Error()})
		}
	}
	return broken
}

// awesomeListLinkRe matches heilcheng/awesome-agent-skills' README link
// shape specifically — [owner/name](https://agent-skill.co/... — rather than
// any markdown link, so an unrelated link elsewhere in the README (an
// official directory, a screenshot) is never mistaken for a skill entry.
var awesomeListLinkRe = regexp.MustCompile(`\[([A-Za-z0-9._-]+/[A-Za-z0-9._-]+)\]\(https://agent-skill\.co/`)

// newCandidates fetches the awesome-list README at readmeURL and returns the
// linked skill names not yet present in entries, sorted and deduplicated.
func newCandidates(ctx context.Context, client *http.Client, readmeURL string, entries []registry.Entry) ([]string, error) {
	body, err := fetchReadme(ctx, client, readmeURL)
	if err != nil {
		return nil, err
	}

	known := make(map[string]bool, len(entries))
	for _, e := range entries {
		known[strings.ToLower(e.Name)] = true
	}

	seen := make(map[string]bool)
	var out []string
	for _, m := range awesomeListLinkRe.FindAllStringSubmatch(body, -1) {
		name := m[1]
		key := strings.ToLower(name)
		if known[key] || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

func fetchReadme(ctx context.Context, client *http.Client, readmeURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, readmeURL, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", readmeURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch %s: unexpected status %s", readmeURL, resp.Status)
	}
	blob, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", readmeURL, err)
	}
	return string(blob), nil
}

// report renders broken entries and new candidates as Markdown for the
// tracking issue, or "" when there is nothing to flag.
func report(broken []brokenEntry, candidates []string) string {
	if len(broken) == 0 && len(candidates) == 0 {
		return ""
	}

	var b strings.Builder
	if len(broken) > 0 {
		fmt.Fprintf(&b, "## Broken entries (%d)\n\n", len(broken))
		for _, e := range broken {
			fmt.Fprintf(&b, "- **%s** (`%s`): %s\n", e.Name, e.Source, e.Reason)
		}
		b.WriteString("\n")
	}
	if len(candidates) > 0 {
		fmt.Fprintf(&b, "## New candidates (%d)\n\n", len(candidates))
		b.WriteString("Seen in [heilcheng/awesome-agent-skills](https://github.com/heilcheng/awesome-agent-skills), " +
			"not yet in `registry/skills.json`. Each needs a human to resolve its real " +
			"`owner/repo[/subpath]` before it can be added.\n\n")
		for _, c := range candidates {
			fmt.Fprintf(&b, "- %s\n", c)
		}
	}
	return b.String()
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./cmd/registry-check/...`
Expected: PASS, all 4 tests.

Run: `make lint`
Expected: PASS — `flag`-based `main` with a separate testable `run` is the standard shape; confirm `errcheck`/`revive` are clean.

- [ ] **Step 5: Commit**

```bash
git add cmd/registry-check/main.go cmd/registry-check/main_test.go
git commit -m "feat: add the registry-check maintenance tool"
```

---

## Task 5: scheduled registry-refresh workflow

**Files:**
- Create: `.github/workflows/registry-refresh.yml`

**Interfaces:**
- Consumes: `cmd/registry-check` (Task 4), via `go run ./cmd/registry-check`
- Produces: nothing consumed by later tasks — this is the plan's last task

- [ ] **Step 1: Write the workflow**

Create `.github/workflows/registry-refresh.yml`:

```yaml
name: Registry refresh

on:
  schedule:
    - cron: "0 6 * * 1"
  workflow_dispatch: {}

permissions:
  contents: read
  issues: write

jobs:
  check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - uses: jdx/mise-action@v4

      - name: Run registry-check
        id: check
        run: |
          go run ./cmd/registry-check > report.md
          if [ -s report.md ]; then
            echo "empty=false" >> "$GITHUB_OUTPUT"
          else
            echo "empty=true" >> "$GITHUB_OUTPUT"
          fi

      - name: Create or update tracking issue
        if: steps.check.outputs.empty == 'false'
        env:
          GH_TOKEN: ${{ github.token }}
        run: |
          existing=$(gh issue list --repo "$GITHUB_REPOSITORY" --search "Registry refresh report in:title" \
            --state open --json number --jq '.[0].number')
          if [ -n "$existing" ]; then
            gh issue edit "$existing" --repo "$GITHUB_REPOSITORY" --body-file report.md
          else
            gh issue create --repo "$GITHUB_REPOSITORY" --title "Registry refresh report" --body-file report.md
          fi
```

There is no Go test for a workflow file — verification is manual (see below).

- [ ] **Step 2: Validate the YAML**

Run: `python3 -c "import yaml; yaml.safe_load(open('.github/workflows/registry-refresh.yml'))"`
Expected: no output (valid YAML). If `python3`/`pyyaml` is unavailable, visually re-check indentation against `ci.yml`'s structure instead.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/registry-refresh.yml
git commit -m "ci: schedule a weekly registry-check refresh"
```

---

## Final Verification

- [ ] Run `make test && make lint && make tidy-check` at the repo root; all three must pass.
- [ ] Run `go run ./cmd/skillsctl search anything` locally — since `registry/skills.json` is still `[]` at this point, expect the network fetch to succeed (fetching the just-pushed empty array from `main`, once merged) and a "No skills found" message; this confirms the whole path (config → registry fetch → cache write → match → print) runs without error even with zero entries.
- [ ] Once merged, manually trigger the workflow with `gh workflow run registry-refresh.yml`, then `gh run watch` to confirm `registry-check` runs to completion. With `registry/skills.json` still empty, the report is entirely "New candidates" (every awesome-list entry), which confirms the fetch-and-diff path works end-to-end.
- [ ] Add at least one real, manually-resolved seed entry to `registry/skills.json` in a follow-up PR (tracked separately per the design doc's "Out of scope"), then confirm `skillsctl search <term>` returns it and `skillsctl install <its source>` installs cleanly.
