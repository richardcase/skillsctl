package cli

import (
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/channel"
	"github.com/richardcase/skillsctl/internal/discover"
)

func TestCategoryIsTheFirstSubpathSegment(t *testing.T) {
	tests := []struct {
		subpath string
		want    string
	}{
		{"", ""},
		{"only", ""},
		{"cat-a/one", "cat-a"},
		{"cat-a/nested/one", "cat-a"},
	}
	for _, tt := range tests {
		if got := category(channel.Candidate{Subpath: tt.subpath}); got != tt.want {
			t.Errorf("category(%q) = %q, want %q", tt.subpath, got, tt.want)
		}
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
