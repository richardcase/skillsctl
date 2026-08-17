package ocix

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/richardcase/skillsctl/internal/pack"
	"github.com/richardcase/skillsctl/internal/testregistry"
)

func packDir(t *testing.T, dir string) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := pack.Tar(&buf, dir); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestPushThenResolveThenPullRoundTrips(t *testing.T) {
	host := testregistry.New(t)
	ref := fmt.Sprintf("%s/skills:v1", host)

	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "alpha", "SKILL.md"), []byte("---\nname: alpha\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	o := New()
	ctx := context.Background()

	if err := o.Push(ctx, ref, bytes.NewReader(packDir(t, src))); err != nil {
		t.Fatalf("push: %v", err)
	}

	digest, err := o.Resolve(ctx, ref)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if digest == "" {
		t.Fatal("resolve returned an empty digest")
	}

	dest := t.TempDir()
	if err := o.Pull(ctx, ref, dest); err != nil {
		t.Fatalf("pull: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dest, "alpha", "SKILL.md"))
	if err != nil {
		t.Fatalf("pulled tree is missing alpha/SKILL.md: %v", err)
	}
	if string(got) != "---\nname: alpha\n---\n" {
		t.Errorf("pulled SKILL.md = %q", got)
	}
}

func TestResolveIsStableForAnUnchangedTag(t *testing.T) {
	host := testregistry.New(t)
	ref := fmt.Sprintf("%s/skills:v1", host)
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	o := New()
	ctx := context.Background()
	if err := o.Push(ctx, ref, bytes.NewReader(packDir(t, src))); err != nil {
		t.Fatal(err)
	}

	first, err := o.Resolve(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	second, err := o.Resolve(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Errorf("resolve of an unchanged tag returned %q then %q", first, second)
	}
}
