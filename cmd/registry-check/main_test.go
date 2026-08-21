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
