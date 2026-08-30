package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/richardcase/skillsctl/internal/registry"
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

	report, err := run(context.Background(), path, g, http.DefaultClient, readmeServer(t, ""), false)
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

	report, err := run(context.Background(), path, g, http.DefaultClient, readmeServer(t, readme), false)
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

	report, err := run(context.Background(), path, g, http.DefaultClient, readmeServer(t, readme), false)
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

func TestRunReportsCandidateWithSameNameAsDifferentOwner(t *testing.T) {
	// Registry has owner A's "pdf-tool"; the README links owner B's
	// "pdf-tool", a distinct, unregistered skill. The bare-name "known"
	// check used to swallow this silently — it must be reported.
	path := writeRegistry(t, `[{"name":"pdf-tool","source":"ownerA/pdf-tool"}]`)
	g := &fakeGit{resolve: func(context.Context, string, string) (string, error) { return "sha", nil }}
	readme := `[ownerB/pdf-tool](https://agent-skill.co/ownerB/skills/pdf-tool) - a different owner's skill`

	report, err := run(context.Background(), path, g, http.DefaultClient, readmeServer(t, readme), false)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(report, "New candidates") || !strings.Contains(report, "ownerB/pdf-tool") {
		t.Errorf("report = %q, want ownerB/pdf-tool reported as a new candidate", report)
	}
}

func TestRunReturnsEmptyReportWhenClean(t *testing.T) {
	path := writeRegistry(t, `[{"name":"skill-creator","source":"anthropics/skills/skill-creator"}]`)
	g := &fakeGit{resolve: func(context.Context, string, string) (string, error) { return "sha", nil }}
	readme := `[anthropics/skill-creator](https://agent-skill.co/anthropics/skills/skill-creator) - already known`

	report, err := run(context.Background(), path, g, http.DefaultClient, readmeServer(t, readme), false)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if report != "" {
		t.Errorf("report = %q, want empty when nothing to flag", report)
	}
}

func TestRunListsDistinctCandidatesWithSameSkilName(t *testing.T) {
	path := writeRegistry(t, `[]`)
	g := &fakeGit{resolve: func(context.Context, string, string) (string, error) { return "sha", nil }}
	readme := `[anthropics/pdf-tool](https://agent-skill.co/anthropics/skills/pdf-tool) - anthropics version
[openai/pdf-tool](https://agent-skill.co/openai/skills/pdf-tool) - openai version`

	report, err := run(context.Background(), path, g, http.DefaultClient, readmeServer(t, readme), false)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Count(report, "anthropics/pdf-tool") != 1 {
		t.Errorf("report = %q, want anthropics/pdf-tool listed exactly once", report)
	}
	if strings.Count(report, "openai/pdf-tool") != 1 {
		t.Errorf("report = %q, want openai/pdf-tool listed exactly once", report)
	}
}

func TestReportCapsNewCandidates(t *testing.T) {
	candidates := make([]string, 0, maxReportedCandidates+7)
	for i := 0; i < maxReportedCandidates+7; i++ {
		candidates = append(candidates, fmt.Sprintf("owner/skill-%02d", i))
	}

	out := report(nil, candidates)

	if got := strings.Count(out, "owner/skill-"); got != maxReportedCandidates {
		t.Errorf("listed %d candidates, want the cap of %d", got, maxReportedCandidates)
	}
	if !strings.Contains(out, "…and 7 more") {
		t.Errorf("report = %q, want a trailing note for the 7 elided candidates", out)
	}
}

func TestRunWithoutFixLeavesRegistryUntouched(t *testing.T) {
	path := writeRegistry(t, `[{"name":"gone","source":"owner/gone"}]`)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	g := &fakeGit{resolve: func(context.Context, string, string) (string, error) {
		return "", errors.New("repository not found")
	}}

	if _, err := run(context.Background(), path, g, http.DefaultClient, readmeServer(t, ""), false); err != nil {
		t.Fatalf("run: %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("registry file changed without --fix: before %q, after %q", before, after)
	}
}

func TestRunFixRemovesBrokenEntry(t *testing.T) {
	path := writeRegistry(t, `[{"name":"gone","source":"owner/gone"},{"name":"ok","source":"owner/ok"}]`)
	g := &fakeGit{resolve: func(_ context.Context, repoURL, _ string) (string, error) {
		if strings.Contains(repoURL, "gone") {
			return "", errors.New("repository not found")
		}
		return "sha", nil
	}}

	if _, err := run(context.Background(), path, g, http.DefaultClient, readmeServer(t, ""), true); err != nil {
		t.Fatalf("run: %v", err)
	}

	entries, err := registry.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "ok" {
		t.Errorf("entries = %+v, want only the surviving entry", entries)
	}
}

func TestRunFixAddsCandidateStub(t *testing.T) {
	path := writeRegistry(t, `[]`)
	g := &fakeGit{resolve: func(context.Context, string, string) (string, error) { return "sha", nil }}
	readme := `[anthropics/skill-creator](https://agent-skill.co/anthropics/skills/skill-creator) - some skill`

	if _, err := run(context.Background(), path, g, http.DefaultClient, readmeServer(t, readme), true); err != nil {
		t.Fatalf("run: %v", err)
	}

	entries, err := registry.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %+v, want exactly one stub", entries)
	}
	got := entries[0]
	if got.Name != "skill-creator" || got.Source != "anthropics/skill-creator" {
		t.Errorf("stub = %+v, want name/source derived from the candidate", got)
	}
	if !strings.Contains(got.Description, "TODO") {
		t.Errorf("stub description = %q, want it flagged as needing review", got.Description)
	}
	if len(got.Tags) != 0 || len(got.Agents) != 0 {
		t.Errorf("stub = %+v, want no tags/agents guessed", got)
	}
}

func TestReportDoesNotCapBrokenEntries(t *testing.T) {
	broken := make([]brokenEntry, 0, maxReportedCandidates+7)
	for i := 0; i < maxReportedCandidates+7; i++ {
		broken = append(broken, brokenEntry{Name: fmt.Sprintf("skill-%02d", i), Source: "owner/repo", Reason: "gone"})
	}

	out := report(broken, nil)

	if got := strings.Count(out, "gone"); got != len(broken) {
		t.Errorf("listed %d broken entries, want all %d uncapped", got, len(broken))
	}
	if strings.Contains(out, "more") {
		t.Errorf("report = %q, broken entries should never be elided", out)
	}
}
