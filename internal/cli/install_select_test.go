package cli

import (
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/channel"
	"github.com/richardcase/skillsctl/internal/discover"
)

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
