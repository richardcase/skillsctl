package cli

import (
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/channel"
	"github.com/richardcase/skillsctl/internal/discover"
)

func TestCategoryIsTheFirstSubpathSegmentAfterAnyCommonWrapper(t *testing.T) {
	tests := []struct {
		name     string
		subpaths []string
		want     []string
	}{
		{
			name:     "root-level skills have no category",
			subpaths: []string{"", "only"},
			want:     []string{"", ""},
		},
		{
			name:     "no shared wrapper: first segment is the category",
			subpaths: []string{"cat-a/one", "cat-a/two", "cat-b/three"},
			want:     []string{"cat-a", "cat-a", "cat-b"},
		},
		{
			name:     "nesting within a category that isn't universal still collapses to it",
			subpaths: []string{"cat-a/nested/one", "cat-a/nested/two", "cat-b/three"},
			want:     []string{"cat-a", "cat-a", "cat-b"},
		},
		{
			name:     "a wrapper shared by every skill is stripped",
			subpaths: []string{"skills/engineering/tdd", "skills/productivity/grill-me", "skills/misc/x"},
			want:     []string{"engineering", "productivity", "misc"},
		},
		{
			name:     "a wrapper shared by every skill with nothing beyond it stays uncategorized",
			subpaths: []string{"skills/alpha", "skills/beta"},
			want:     []string{"", ""},
		},
		{
			name:     "mixing a root-level skill with nested ones leaves nesting unstripped",
			subpaths: []string{"only", "skills/alpha"},
			want:     []string{"", "skills"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cands := make([]channel.Candidate, len(tt.subpaths))
			for i, s := range tt.subpaths {
				cands[i] = channel.Candidate{Subpath: s}
			}
			prefixLen := categoryPrefixLen(cands)
			for i, c := range cands {
				if got := category(c, prefixLen); got != tt.want[i] {
					t.Errorf("category(%q, %d) = %q, want %q", c.Subpath, prefixLen, got, tt.want[i])
				}
			}
		})
	}
}

func TestPickerItemsStaysFlatWithFewerThanTwoCategories(t *testing.T) {
	cands := []channel.Candidate{
		{Name: "alpha", Subpath: "skills/alpha"},
		{Name: "beta", Subpath: "skills/beta"},
	}
	items, member := pickerItems(cands)
	if len(items) != 2 {
		t.Fatalf("items = %+v, want exactly the two candidates and no header", items)
	}
	for _, it := range items {
		if it.Header {
			t.Errorf("items = %+v, want no header row for a single-category repo", items)
		}
	}
	if member[0] != 0 || member[1] != 1 {
		t.Errorf("member = %v, want [0 1]", member)
	}
}

func TestPickerItemsGroupsByCategoryWhenThereAreTwoOrMore(t *testing.T) {
	cands := []channel.Candidate{
		{Name: "one", Subpath: "cat-a/one"},
		{Name: "two", Subpath: "cat-a/two"},
		{Name: "three", Subpath: "cat-b/three"},
	}
	items, member := pickerItems(cands)

	want := []struct {
		header bool
		member int
	}{
		{true, -1},
		{false, 0},
		{false, 1},
		{true, -1},
		{false, 2},
	}
	if len(items) != len(want) {
		t.Fatalf("items = %+v, want %d rows", items, len(want))
	}
	for i, w := range want {
		if items[i].Header != w.header || member[i] != w.member {
			t.Errorf("row %d = {Header:%v member:%d}, want {Header:%v member:%d}",
				i, items[i].Header, member[i], w.header, w.member)
		}
	}
	if items[0].Label != "cat-a" || items[3].Label != "cat-b" {
		t.Errorf("header labels = %q, %q, want the category names", items[0].Label, items[3].Label)
	}
}

func TestPickerItemsGroupsByCategoryUnderASharedWrapperFolder(t *testing.T) {
	cands := []channel.Candidate{
		{Name: "tdd", Subpath: "skills/engineering/tdd"},
		{Name: "grill-me", Subpath: "skills/productivity/grill-me"},
		{Name: "scaffold-exercises", Subpath: "skills/misc/scaffold-exercises"},
	}
	items, _ := pickerItems(cands)

	var headers []string
	for _, it := range items {
		if it.Header {
			headers = append(headers, it.Label)
		}
	}
	want := []string{"engineering", "productivity", "misc"}
	if len(headers) != len(want) {
		t.Fatalf("headers = %v, want %v", headers, want)
	}
	for i, w := range want {
		if headers[i] != w {
			t.Errorf("headers = %v, want %v", headers, want)
		}
	}
}

func TestListingRendersNamesAndDescriptions(t *testing.T) {
	all := []channel.Candidate{
		{Name: "alpha-skill", Desc: "Does the alpha thing"},
		{Name: "beta-skill"},
	}

	got := strings.Join(listing(discover.Metadata{}, "skills in repo:", all), "\n")
	for _, want := range []string{"skills in repo:", "alpha-skill", "Does the alpha thing", "beta-skill"} {
		if !strings.Contains(got, want) {
			t.Errorf("listing = %q, want it to contain %q", got, want)
		}
	}
}

func TestListingIncludesPluginMetadataWhenPresent(t *testing.T) {
	all := []channel.Candidate{{Name: "alpha-skill"}}

	got := strings.Join(listing(discover.Metadata{Name: "agent-skills", Description: "A pile"}, "", all), "\n")
	if !strings.Contains(got, "agent-skills") || !strings.Contains(got, "A pile") {
		t.Errorf("listing = %q, want the plugin metadata rendered", got)
	}
}

func TestFirstLineTruncates(t *testing.T) {
	long := strings.Repeat("x", maxDescription+20)
	got := firstLine(long + "\nsecond line")
	if len(got) > maxDescription+3 {
		t.Errorf("firstLine returned %d chars, want it truncated near %d", len(got), maxDescription)
	}
	if strings.Contains(got, "second line") {
		t.Errorf("firstLine = %q, want only the first line", got)
	}
}
