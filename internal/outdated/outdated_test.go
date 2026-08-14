package outdated

import (
	"context"
	"fmt"
	"testing"

	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/richardcase/skillsctl/internal/state"
)

// fakeGit answers Resolve from a table keyed by repoURL + "\x00" + ref, and
// counts the calls so the dedup behaviour can be asserted. Mirror and Extract
// are never reached: outdated must not fetch anything.
type fakeGit struct {
	shas  map[string]string
	calls int
}

func (f *fakeGit) Resolve(_ context.Context, repoURL, ref string) (string, error) {
	f.calls++
	sha, ok := f.shas[repoURL+"\x00"+ref]
	if !ok {
		return "", fmt.Errorf("ref %q not found in %s", ref, repoURL)
	}
	return sha, nil
}

func (f *fakeGit) Mirror(context.Context, string, string) error {
	panic("outdated must not mirror")
}

func (f *fakeGit) Extract(context.Context, string, string, string) error {
	panic("outdated must not extract")
}

func (f *fakeGit) Describe(context.Context, string) (gitx.Origin, error) {
	panic("outdated must not describe a working copy")
}

func TestCheckReportsAMovedRefAsOutdated(t *testing.T) {
	g := &fakeGit{shas: map[string]string{"https://example.com/repo\x00main": "bbbb"}}
	receipts := []*state.Receipt{
		{Name: "demo", Channel: "git", Source: "https://example.com/repo", Ref: "main", Resolved: "aaaa"},
	}

	got := Check(context.Background(), g, receipts)

	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d", len(got))
	}
	if got[0].Status != StatusOutdated {
		t.Errorf("status = %q, want %q", got[0].Status, StatusOutdated)
	}
	if got[0].Latest != "bbbb" {
		t.Errorf("latest = %q, want bbbb", got[0].Latest)
	}
	if got[0].Current != "aaaa" {
		t.Errorf("current = %q, want aaaa", got[0].Current)
	}
}

func TestCheckReportsAnUnmovedRefAsCurrent(t *testing.T) {
	g := &fakeGit{shas: map[string]string{"https://example.com/repo\x00main": "aaaa"}}
	receipts := []*state.Receipt{
		{Name: "demo", Channel: "git", Source: "https://example.com/repo", Ref: "main", Resolved: "aaaa"},
	}

	got := Check(context.Background(), g, receipts)

	if got[0].Status != StatusCurrent {
		t.Errorf("status = %q, want %q", got[0].Status, StatusCurrent)
	}
}

func TestCheckSkipsNonGitChannelsWithoutResolving(t *testing.T) {
	g := &fakeGit{}
	receipts := []*state.Receipt{
		{Name: "mine", Channel: "local", Source: "/home/me/skills/mine"},
	}

	got := Check(context.Background(), g, receipts)

	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d", len(got))
	}
	if got[0].Status != StatusSkipped {
		t.Errorf("status = %q, want %q", got[0].Status, StatusSkipped)
	}
	if g.calls != 0 {
		t.Errorf("resolved a non-git receipt: %d calls", g.calls)
	}
}

func TestCheckKeepsGoingWhenOneRemoteFails(t *testing.T) {
	g := &fakeGit{shas: map[string]string{"https://example.com/good\x00main": "bbbb"}}
	receipts := []*state.Receipt{
		{Name: "broken", Channel: "git", Source: "https://example.com/gone", Ref: "main", Resolved: "aaaa"},
		{Name: "fine", Channel: "git", Source: "https://example.com/good", Ref: "main", Resolved: "aaaa"},
	}

	got := Check(context.Background(), g, receipts)

	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d", len(got))
	}
	if got[0].Status != StatusError {
		t.Errorf("broken status = %q, want %q", got[0].Status, StatusError)
	}
	if got[0].Error == "" {
		t.Error("broken entry carries no error message")
	}
	if got[1].Status != StatusOutdated {
		t.Errorf("a dead remote hid the rest of the report: %q", got[1].Status)
	}
}

// A pinned receipt records no Ref, so it is resolved against the repository's
// default branch: a pin must never hide the fact that something moved.
func TestCheckResolvesAPinnedReceiptAgainstTheDefaultBranch(t *testing.T) {
	g := &fakeGit{shas: map[string]string{"https://example.com/repo\x00HEAD": "bbbb"}}
	receipts := []*state.Receipt{
		{Name: "demo", Channel: "git", Source: "https://example.com/repo", Resolved: "aaaa", Pinned: true},
	}

	got := Check(context.Background(), g, receipts)

	if got[0].Status != StatusOutdated {
		t.Errorf("status = %q, want %q", got[0].Status, StatusOutdated)
	}
	if !got[0].Pinned {
		t.Error("entry lost the pinned flag")
	}
	if got[0].Ref != "HEAD" {
		t.Errorf("ref = %q, want HEAD", got[0].Ref)
	}
}

func TestCheckResolvesEachSourceAndRefOnce(t *testing.T) {
	g := &fakeGit{shas: map[string]string{
		"https://example.com/repo\x00main": "bbbb",
		"https://example.com/repo\x00v1":   "cccc",
	}}
	receipts := []*state.Receipt{
		{Name: "one", Channel: "git", Source: "https://example.com/repo", Ref: "main", Resolved: "aaaa"},
		{Name: "two", Channel: "git", Source: "https://example.com/repo", Ref: "main", Resolved: "aaaa"},
		{Name: "three", Channel: "git", Source: "https://example.com/repo", Ref: "v1", Resolved: "cccc"},
	}

	got := Check(context.Background(), g, receipts)

	if g.calls != 2 {
		t.Errorf("resolved %d times, want 2 (one per source+ref)", g.calls)
	}
	for _, want := range []struct {
		name   string
		status Status
	}{{"one", StatusOutdated}, {"two", StatusOutdated}, {"three", StatusCurrent}} {
		for _, e := range got {
			if e.Name == want.name && e.Status != want.status {
				t.Errorf("%s status = %q, want %q", want.name, e.Status, want.status)
			}
		}
	}
}
