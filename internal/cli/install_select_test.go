package cli

import (
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/discover"
)

func skill(rel, name, desc string) discover.Skill {
	return discover.Skill{Meta: discover.Meta{Name: name, Description: desc}, Dir: "/rev/" + rel, Rel: rel}
}

func names(sels []selection) []string {
	out := make([]string, len(sels))
	for i, s := range sels {
		out[i] = s.name
	}
	return out
}

func TestResolveNamesUsesFrontmatter(t *testing.T) {
	got, err := resolveNames([]discover.Skill{
		skill("skills/alpha", "alpha-skill", ""),
		skill("skills/beta", "beta-skill", ""),
	}, "repo")
	if err != nil {
		t.Fatalf("resolveNames: %v", err)
	}
	if want := []string{"alpha-skill", "beta-skill"}; strings.Join(names(got), ",") != strings.Join(want, ",") {
		t.Errorf("names = %v, want %v", names(got), want)
	}
}

func TestResolveNamesFallsBackToTheSourceForARootSkill(t *testing.T) {
	got, err := resolveNames([]discover.Skill{skill(".", "", "")}, "my-repo")
	if err != nil {
		t.Fatalf("resolveNames: %v", err)
	}
	if names(got)[0] != "my-repo" {
		t.Errorf("name = %q, want my-repo: a nameless root skill falls back to the source", names(got)[0])
	}
}

func TestResolveNamesFallsBackToTheDirectoryName(t *testing.T) {
	got, err := resolveNames([]discover.Skill{
		skill("skills/alpha", "", ""),
		skill("skills/beta", "beta-skill", ""),
	}, "repo")
	if err != nil {
		t.Fatalf("resolveNames: %v", err)
	}
	if names(got)[0] != "alpha" {
		t.Errorf("name = %q, want alpha: a nameless nested skill falls back to its directory", names(got)[0])
	}
}

func TestResolveNamesRejectsDuplicates(t *testing.T) {
	_, err := resolveNames([]discover.Skill{
		skill("skills/alpha", "same", ""),
		skill("skills/beta", "same", ""),
	}, "repo")
	if err == nil {
		t.Fatal("resolveNames accepted two skills with one name; want an error")
	}
	for _, want := range []string{"skills/alpha", "skills/beta", "same"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to mention %q", err, want)
		}
	}
}

func TestResolveNamesRejectsAnUnusableName(t *testing.T) {
	_, err := resolveNames([]discover.Skill{skill("skills/alpha", "../escape", "")}, "repo")
	if err == nil {
		t.Fatal("resolveNames accepted a name with a separator; want an error")
	}
	if !strings.Contains(err.Error(), "--as") {
		t.Errorf("error = %v, want it to suggest --as", err)
	}
}

func TestPickSkillsMatchesNameThenPath(t *testing.T) {
	all, err := resolveNames([]discover.Skill{
		skill("skills/alpha", "alpha-skill", ""),
		skill("skills/beta", "beta-skill", ""),
	}, "repo")
	if err != nil {
		t.Fatal(err)
	}

	got, err := pickSkills(all, []string{"beta-skill", "skills/alpha"})
	if err != nil {
		t.Fatalf("pickSkills: %v", err)
	}
	if want := "beta-skill,alpha-skill"; strings.Join(names(got), ",") != want {
		t.Errorf("names = %v, want %s", names(got), want)
	}
}

func TestPickSkillsIgnoresARepeatedRequest(t *testing.T) {
	all, err := resolveNames([]discover.Skill{skill("skills/alpha", "alpha-skill", "")}, "repo")
	if err != nil {
		t.Fatal(err)
	}

	got, err := pickSkills(all, []string{"alpha-skill", "skills/alpha"})
	if err != nil {
		t.Fatalf("pickSkills: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("pickSkills = %v, want one entry: the same skill asked for twice installs once", names(got))
	}
}

func TestPickSkillsRejectsAnUnknownName(t *testing.T) {
	all, err := resolveNames([]discover.Skill{skill("skills/alpha", "alpha-skill", "")}, "repo")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := pickSkills(all, []string{"nope"}); err == nil {
		t.Fatal("pickSkills accepted an unknown name; want an error")
	} else if !strings.Contains(err.Error(), "nope") {
		t.Errorf("error = %v, want it to name the unmatched skill", err)
	}
}

func TestListingRendersNamesAndDescriptions(t *testing.T) {
	all, err := resolveNames([]discover.Skill{
		skill("skills/alpha", "alpha-skill", "Does the alpha thing"),
		skill("skills/beta", "beta-skill", ""),
	}, "repo")
	if err != nil {
		t.Fatal(err)
	}

	got := strings.Join(listing(discover.Metadata{}, "skills in repo:", all), "\n")
	for _, want := range []string{"skills in repo:", "alpha-skill", "Does the alpha thing", "beta-skill"} {
		if !strings.Contains(got, want) {
			t.Errorf("listing = %q, want it to contain %q", got, want)
		}
	}
}

func TestListingIncludesPluginMetadataWhenPresent(t *testing.T) {
	all, err := resolveNames([]discover.Skill{skill("skills/alpha", "alpha-skill", "")}, "repo")
	if err != nil {
		t.Fatal(err)
	}

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
