package store

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHashDirIsStable(t *testing.T) {
	files := map[string]string{"SKILL.md": "hello", "refs/a.md": "world"}

	a := t.TempDir()
	write(t, a, files)
	b := t.TempDir()
	write(t, b, files)

	ha, err := HashDir(a)
	if err != nil {
		t.Fatalf("HashDir(a): %v", err)
	}
	hb, err := HashDir(b)
	if err != nil {
		t.Fatalf("HashDir(b): %v", err)
	}
	if ha != hb {
		t.Errorf("identical trees hashed differently: %s vs %s", ha, hb)
	}
	if ha == "" {
		t.Error("HashDir returned an empty hash")
	}
}

func TestHashDirDetectsContentChange(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{"SKILL.md": "hello"})
	before, _ := HashDir(dir)

	write(t, dir, map[string]string{"SKILL.md": "hello, edited"})
	after, _ := HashDir(dir)

	if before == after {
		t.Error("HashDir did not change after the file was edited")
	}
}

func TestHashDirDetectsNewFile(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{"SKILL.md": "hello"})
	before, _ := HashDir(dir)

	write(t, dir, map[string]string{"extra.md": "new"})
	after, _ := HashDir(dir)

	if before == after {
		t.Error("HashDir did not change after a file was added")
	}
}
