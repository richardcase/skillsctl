package discover

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFrontmatter(t *testing.T) {
	body := []byte("---\nname: avoid-ai-writing\ndescription: Write like a person\n---\n\n# Heading\n")

	got, err := Frontmatter(body)
	if err != nil {
		t.Fatalf("Frontmatter: %v", err)
	}
	if got.Name != "avoid-ai-writing" {
		t.Errorf("Name = %q, want avoid-ai-writing", got.Name)
	}
	if got.Description != "Write like a person" {
		t.Errorf("Description = %q", got.Description)
	}
}

func TestFrontmatterCRLF(t *testing.T) {
	body := []byte("---\r\nname: windows-authored\r\n---\r\nbody\r\n")

	got, err := Frontmatter(body)
	if err != nil {
		t.Fatalf("Frontmatter: %v", err)
	}
	if got.Name != "windows-authored" {
		t.Errorf("Name = %q, want windows-authored", got.Name)
	}
}

func TestFrontmatterMissingBlockIsNotAnError(t *testing.T) {
	got, err := Frontmatter([]byte("# Just a heading\n"))
	if err != nil {
		t.Fatalf("Frontmatter: %v", err)
	}
	if got.Name != "" {
		t.Errorf("Name = %q, want empty so the caller can fall back", got.Name)
	}
}

func TestFrontmatterUnterminatedBlockErrors(t *testing.T) {
	if _, err := Frontmatter([]byte("---\nname: broken\n")); err == nil {
		t.Fatal("Frontmatter accepted an unterminated block; want an error")
	}
}

func TestFrontmatterInvalidYAMLErrors(t *testing.T) {
	if _, err := Frontmatter([]byte("---\nname: [unclosed\n---\n")); err == nil {
		t.Fatal("Frontmatter accepted invalid YAML; want an error")
	}
}

func TestRoot(t *testing.T) {
	dir := t.TempDir()
	body := "---\nname: demo\ndescription: A demo\n---\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Root(dir)
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	if got.Name != "demo" {
		t.Errorf("Name = %q, want demo", got.Name)
	}
	if got.Dir != dir {
		t.Errorf("Dir = %q, want %q", got.Dir, dir)
	}
}

func TestRootMissingSkillFile(t *testing.T) {
	_, err := Root(t.TempDir())
	if !errors.Is(err, ErrNoSkill) {
		t.Fatalf("Root error = %v, want ErrNoSkill", err)
	}
}

// writeSkill creates dir/rel/SKILL.md with frontmatter naming the skill.
func writeSkill(t *testing.T, root, rel, name string) {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: The " + name + " skill\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// rels returns the Rel of each skill, for comparing against a want list.
func rels(skills []Skill) []string {
	out := make([]string, len(skills))
	for i, s := range skills {
		out[i] = s.Rel
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestWalkRootSkillWinsAndStopsDescending(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, ".", "demo")
	writeSkill(t, dir, "examples/nested", "nested")

	got, err := Walk(dir)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Walk found %v, want just the root skill: a skill directory is never descended into", rels(got))
	}
	if got[0].Rel != "." {
		t.Errorf("Rel = %q, want \".\"", got[0].Rel)
	}
	if got[0].Name != "demo" {
		t.Errorf("Name = %q, want demo", got[0].Name)
	}
	if got[0].Dir != dir {
		t.Errorf("Dir = %q, want %q", got[0].Dir, dir)
	}
}

func TestWalkFindsSeveralSkillsSorted(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "skills/beta", "beta")
	writeSkill(t, dir, "skills/alpha", "alpha")

	got, err := Walk(dir)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	want := []string{"skills/alpha", "skills/beta"}
	if !equal(rels(got), want) {
		t.Fatalf("Rel = %v, want %v", rels(got), want)
	}
	if got[0].Name != "alpha" || got[1].Name != "beta" {
		t.Errorf("names = %q/%q, want alpha/beta", got[0].Name, got[1].Name)
	}
	if got[0].Dir != filepath.Join(dir, "skills", "alpha") {
		t.Errorf("Dir = %q, want the skill directory", got[0].Dir)
	}
}

func TestWalkSkipsGitAndNodeModules(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "skills/real", "real")
	writeSkill(t, dir, ".git/skills/ghost", "ghost")
	writeSkill(t, dir, "node_modules/pkg", "vendored")

	got, err := Walk(dir)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if !equal(rels(got), []string{"skills/real"}) {
		t.Fatalf("Rel = %v, want just skills/real", rels(got))
	}
}

func TestWalkStopsAtMaxDepth(t *testing.T) {
	dir := t.TempDir()
	deep := "a/b/c/d/e/f/g"
	writeSkill(t, dir, deep, "too-deep")

	got, err := Walk(dir)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Walk found %v at depth beyond MaxDepth=%d, want none", rels(got), MaxDepth)
	}
}

func TestWalkNoSkillsIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := Walk(dir)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Walk = %v, want none", rels(got))
	}
}

func TestWalkMissingDirErrors(t *testing.T) {
	if _, err := Walk(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("Walk accepted a missing directory; want an error")
	}
}

func TestWalkMalformedFrontmatterErrorsNamingTheFile(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "skills/ok", "ok")
	bad := filepath.Join(dir, "skills", "broken")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, FileName), []byte("---\nname: [unclosed\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Walk(dir)
	if err == nil {
		t.Fatal("Walk accepted malformed frontmatter; want an error")
	}
	if !strings.Contains(err.Error(), filepath.Join("skills", "broken")) {
		t.Errorf("error = %v, want it to name the offending file", err)
	}
}

func TestWalkSkillWithoutFrontmatterIsStillASkill(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "skills", "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plain, FileName), []byte("# Just a heading\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Walk(dir)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(got) != 1 || got[0].Name != "" {
		t.Fatalf("got %+v, want one skill with an empty Name so the caller can fall back", got)
	}
}

func TestFrontmatterToleratesTrailingSpaceOnFences(t *testing.T) {
	body := []byte("---   \nname: spaced\n---\t\n\nBody.\n")

	got, err := Frontmatter(body)
	if err != nil {
		t.Fatalf("Frontmatter: %v", err)
	}
	if got.Name != "spaced" {
		t.Errorf("Name = %q, want spaced: a fence line with trailing whitespace must still be a fence", got.Name)
	}
}

func TestFrontmatterStopsAtTheFirstFenceNotBodyRules(t *testing.T) {
	body := []byte("---\nname: real\ndescription: still frontmatter\n---\n\nIntro.\n\n---\n\nMore.\n\n----\n")

	got, err := Frontmatter(body)
	if err != nil {
		t.Fatalf("Frontmatter: %v", err)
	}
	if got.Name != "real" {
		t.Errorf("Name = %q, want real", got.Name)
	}
	if got.Description != "still frontmatter" {
		t.Errorf("Description = %q, want \"still frontmatter\": horizontal rules in the body must not affect the block", got.Description)
	}
}

func TestFrontmatterEmptyBlock(t *testing.T) {
	got, err := Frontmatter([]byte("---\n---\n\nBody.\n"))
	if err != nil {
		t.Fatalf("Frontmatter: %v", err)
	}
	if got.Name != "" || got.Description != "" {
		t.Errorf("got %+v, want a zero Meta", got)
	}
}

func TestFrontmatterParsesAgents(t *testing.T) {
	body := []byte("---\nname: demo\ndescription: A demo\nagents:\n  - claude\n  - codex\n---\n")

	got, err := Frontmatter(body)
	if err != nil {
		t.Fatalf("Frontmatter: %v", err)
	}
	want := []string{"claude", "codex"}
	if len(got.Agents) != len(want) {
		t.Fatalf("Agents = %v, want %v", got.Agents, want)
	}
	for i, a := range want {
		if got.Agents[i] != a {
			t.Errorf("Agents[%d] = %q, want %q", i, got.Agents[i], a)
		}
	}
}

func TestFrontmatterWithNoAgentsIsUnrestricted(t *testing.T) {
	got, err := Frontmatter([]byte("---\nname: demo\ndescription: A demo\n---\n"))
	if err != nil {
		t.Fatalf("Frontmatter: %v", err)
	}
	if len(got.Agents) != 0 {
		t.Errorf("Agents = %v, want none: a skill with no agents field is unrestricted", got.Agents)
	}
}
