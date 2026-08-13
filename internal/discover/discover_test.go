package discover

import (
	"errors"
	"os"
	"path/filepath"
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
