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
