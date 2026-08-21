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
