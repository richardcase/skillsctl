# OCI Skill Packaging & Install Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `skillsctl package` command that bundles a directory of skills
into an OCI artifact and pushes it to a registry, and an `oci` install
channel so `skillsctl install oci://registry/repo:tag` works exactly like
installing from git — including `outdated`/`update` detecting when the tag
has moved.

**Architecture:** A new `internal/ocix` package wraps
`google/go-containerregistry` behind a 3-method interface, the same seam
`gitx` gives the `git` binary. A new `internal/pack` package turns a
directory into a `.gitignore`-aware, `.git`-excluding tar stream. A new
`internal/channel.OCI` type is the fourth `channel.Channel` implementation,
`StoreOwned` like git, reusing `linked` for its removal/link/agents contract.
`internal/source` gains an explicit `oci://` scheme. Everything downstream —
`cli`, `plan`, `state`, `list`/`remove`/`gc` — needs no new branches, because
they already dispatch through `channel.Registry` and `Ownership()`.

**Tech Stack:** Go 1.25, `github.com/google/go-containerregistry` (OCI
push/pull/auth), `github.com/sabhiram/go-gitignore` (`.gitignore` matching).

**Spec:** `docs/superpowers/specs/2026-08-17-oci-skill-packaging-design.md`

## Global Constraints

- No test may touch a real registry or the network — `internal/ocix`'s real
  implementation sits behind the `OCI` interface, exactly as `gitx.Git` and
  `claudex.Plugins` already do, and package/channel/outdated/cli tests use a
  fake or the in-process `testregistry` fixture.
- `.git` directories are excluded from a packaged image unconditionally, at
  any depth, regardless of `.gitignore` contents.
- `.gitignore` files are respected, scoped to their own directory and below —
  a `.gitignore` in a subdirectory must not affect its siblings.
- No new receipt fields: an OCI receipt reuses `Ref` (tag) and `Resolved`
  (digest) exactly as a git receipt reuses them for a branch and a sha.
- Source syntax is the explicit `oci://registry/repo:tag` scheme only — no
  bare-ref detection.
- `skillsctl package` builds and pushes in one step; there is no local-only
  output mode in this plan.
- Every new direct dependency is `go get`-added and reflected in `go.mod`;
  none are vendored or hand-copied.
- `make test && make lint && make tidy-check` must pass before any task is
  considered done, and `README.md` / `AGENTS.md` are updated in the same
  pull request as the user-visible surface they describe (final task).

---

### Task 1: Export `gitx.Untar` for reuse by the OCI puller

The OCI channel needs the same path-safety-checked tar extraction git
already has (`safeJoin`, symlink target checks). Rather than duplicate it,
export the existing unexported `untar` so `internal/ocix` can call it.

**Files:**
- Modify: `internal/gitx/untar.go`
- Modify: `internal/gitx/gitx.go:138` (call site)

**Interfaces:**
- Produces: `gitx.Untar(r io.Reader, dest string) error` — writes the tar
  stream in `r` into `dest`, rejecting any entry that would escape `dest`.

- [ ] **Step 1: Rename `untar` to `Untar` and add a doc comment**

In `internal/gitx/untar.go`, rename the function and its doc comment:

```go
// Untar writes the tar stream in r into dest, rejecting any entry — a
// symlink target included — that would resolve outside dest.
func Untar(r io.Reader, dest string) error {
```

Update every other reference to `untar(` inside `untar.go` (there are none;
`safeJoin` is a separate helper) to the new name.

- [ ] **Step 2: Update the call site in `gitx.go`**

In `internal/gitx/gitx.go:138`, change:

```go
untarErr := untar(stdout, dest)
```

to:

```go
untarErr := Untar(stdout, dest)
```

- [ ] **Step 3: Run the existing gitx tests to confirm nothing broke**

Run: `make test`
Expected: PASS (this is a pure rename, `internal/gitx` tests already cover
`Extract`, which is `Untar`'s only caller today).

- [ ] **Step 4: Commit**

```bash
git add internal/gitx/untar.go internal/gitx/gitx.go
git commit -m "refactor(gitx): export Untar for reuse by the OCI channel"
```

---

### Task 2: `internal/pack` — a `.gitignore`-aware, `.git`-excluding tar builder

**Files:**
- Create: `internal/pack/pack.go`
- Test: `internal/pack/pack_test.go`

**Interfaces:**
- Produces: `pack.Tar(w io.Writer, dir string) error` — writes `dir`'s tree
  into `w` as an uncompressed tar stream, skipping any `.git` directory and
  anything a `.gitignore` in the tree marks ignored, scoped like git scopes
  nested `.gitignore` files.

- [ ] **Step 1: Add the dependency**

Run: `go get github.com/sabhiram/go-gitignore`

- [ ] **Step 2: Write the failing tests**

```go
package pack

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func tarNames(t *testing.T, data []byte) []string {
	t.Helper()
	var names []string
	tr := tar.NewReader(bytes.NewReader(data))
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		names = append(names, hdr.Name)
	}
	return names
}

func TestTarIncludesEveryFileAndSubdirectory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "alpha", "SKILL.md"), "---\nname: alpha\n---\n")
	writeFile(t, filepath.Join(dir, "alpha", "scripts", "run.sh"), "#!/bin/sh\n")

	var buf bytes.Buffer
	if err := Tar(&buf, dir); err != nil {
		t.Fatal(err)
	}

	names := tarNames(t, buf.Bytes())
	want := []string{"alpha/", "alpha/SKILL.md", "alpha/scripts/", "alpha/scripts/run.sh"}
	for _, w := range want {
		found := false
		for _, n := range names {
			if n == w {
				found = true
			}
		}
		if !found {
			t.Errorf("tar is missing %q, got %v", w, names)
		}
	}
}

func TestTarExcludesGitDirectoryUnconditionally(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "alpha", "SKILL.md"), "---\nname: alpha\n---\n")
	writeFile(t, filepath.Join(dir, ".git", "HEAD"), "ref: refs/heads/main\n")

	var buf bytes.Buffer
	if err := Tar(&buf, dir); err != nil {
		t.Fatal(err)
	}

	for _, n := range tarNames(t, buf.Bytes()) {
		if n == ".git/" || n == ".git/HEAD" {
			t.Errorf("tar included %q, .git must never be packaged", n)
		}
	}
}

func TestTarHonoursRootGitignore(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "alpha", "SKILL.md"), "---\nname: alpha\n---\n")
	writeFile(t, filepath.Join(dir, "alpha", "notes.local"), "secret\n")
	writeFile(t, filepath.Join(dir, ".gitignore"), "*.local\n")

	var buf bytes.Buffer
	if err := Tar(&buf, dir); err != nil {
		t.Fatal(err)
	}

	for _, n := range tarNames(t, buf.Bytes()) {
		if n == "alpha/notes.local" {
			t.Errorf("tar included %q, root .gitignore should have excluded it", n)
		}
	}
}

func TestTarScopesNestedGitignoreToItsOwnSubtree(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "alpha", "SKILL.md"), "---\nname: alpha\n---\n")
	writeFile(t, filepath.Join(dir, "alpha", "build.tmp"), "junk\n")
	writeFile(t, filepath.Join(dir, "alpha", ".gitignore"), "*.tmp\n")
	writeFile(t, filepath.Join(dir, "beta", "SKILL.md"), "---\nname: beta\n---\n")
	writeFile(t, filepath.Join(dir, "beta", "build.tmp"), "kept\n")

	var buf bytes.Buffer
	if err := Tar(&buf, dir); err != nil {
		t.Fatal(err)
	}

	names := tarNames(t, buf.Bytes())
	for _, n := range names {
		if n == "alpha/build.tmp" {
			t.Errorf("alpha/.gitignore should have excluded %q", n)
		}
	}
	found := false
	for _, n := range names {
		if n == "beta/build.tmp" {
			found = true
		}
	}
	if !found {
		t.Errorf("beta/build.tmp should survive: alpha's .gitignore must not leak into beta, got %v", names)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/pack/... -v`
Expected: FAIL — `pack.Tar` is undefined.

- [ ] **Step 4: Implement `pack.Tar`**

```go
// Package pack builds an uncompressed tar stream from a directory tree,
// excluding .git and anything .gitignore marks ignored — the input to an OCI
// skills layer.
package pack

import (
	"archive/tar"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	ignore "github.com/sabhiram/go-gitignore"
)

// scope is one .gitignore's reach: it applies to root and everything under
// it, never to a sibling — the same reach git itself gives a nested
// .gitignore.
type scope struct {
	root string
	m    *ignore.GitIgnore
}

// Tar writes dir's tree into w as an uncompressed tar stream. A .git
// directory is skipped unconditionally, at any depth. A .gitignore file is
// honoured for its own directory and below.
func Tar(w io.Writer, dir string) error {
	tw := tar.NewWriter(w)
	var scopes []scope

	if m, err := loadGitignore(dir); err != nil {
		return err
	} else if m != nil {
		scopes = append(scopes, scope{root: dir, m: m})
	}

	walk := func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == dir {
			return nil
		}
		if d.IsDir() && d.Name() == ".git" {
			return fs.SkipDir
		}
		if ignored(scopes, p) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		if d.IsDir() {
			if m, err := loadGitignore(p); err != nil {
				return err
			} else if m != nil {
				scopes = append(scopes, scope{root: p, m: m})
			}
			return writeHeader(tw, rel+"/", d)
		}
		return writeEntry(tw, p, rel, d)
	}

	if err := filepath.WalkDir(dir, walk); err != nil {
		return err
	}
	return tw.Close()
}

// ignored reports whether p falls under any scope whose .gitignore matches
// it, checked with each scope's patterns applied relative to that scope's
// own root rather than the tar root — which is what keeps a nested
// .gitignore from reaching into a sibling directory.
func ignored(scopes []scope, p string) bool {
	for _, sc := range scopes {
		rel, err := filepath.Rel(sc.root, p)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if sc.m.MatchesPath(filepath.ToSlash(rel)) {
			return true
		}
	}
	return false
}

func loadGitignore(dir string) (*ignore.GitIgnore, error) {
	p := filepath.Join(dir, ".gitignore")
	if fi, err := os.Stat(p); err != nil || fi.IsDir() {
		return nil, nil
	}
	m, err := ignore.CompileIgnoreFile(p)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	return m, nil
}

func writeHeader(tw *tar.Writer, name string, d fs.DirEntry) error {
	info, err := d.Info()
	if err != nil {
		return err
	}
	hdr, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	hdr.Name = name
	return tw.WriteHeader(hdr)
}

func writeEntry(tw *tar.Writer, p, rel string, d fs.DirEntry) error {
	info, err := d.Info()
	if err != nil {
		return err
	}

	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(p)
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(info, target)
		if err != nil {
			return err
		}
		hdr.Name = rel
		return tw.WriteHeader(hdr)
	}
	if !info.Mode().IsRegular() {
		return nil
	}

	hdr, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	hdr.Name = rel
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}

	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(tw, f)
	return err
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/pack/... -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/pack
git commit -m "feat(pack): add a gitignore-aware tar builder for OCI packaging"
```

---

### Task 3: `internal/testregistry` — an in-process OCI registry for tests

Mirrors `internal/testrepo`'s role for git: every later test that pushes or
pulls an image uses this instead of the network.

**Files:**
- Create: `internal/testregistry/testregistry.go`
- Test: `internal/testregistry/testregistry_test.go`

**Interfaces:**
- Produces: `testregistry.New(t *testing.T) string` — starts an in-process
  registry on `httptest.NewServer`, registers `t.Cleanup` to close it, and
  returns its `host:port` (no scheme), so a caller builds a ref as
  `fmt.Sprintf("%s/skills:v1", host)`.

- [ ] **Step 1: Add the dependency**

`go-containerregistry` is added in Task 4; add it now since this task needs
`pkg/registry` too:

Run: `go get github.com/google/go-containerregistry`

- [ ] **Step 2: Write the failing test**

```go
package testregistry

import (
	"fmt"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

func TestNewServesAWritableRegistry(t *testing.T) {
	host := New(t)

	ref, err := name.ParseReference(fmt.Sprintf("%s/skills:v1", host))
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(ref, empty.Image, remote.WithAuthFromKeychain(authn.DefaultKeychain)); err != nil {
		t.Fatalf("push to test registry: %v", err)
	}
	if _, err := remote.Get(ref, remote.WithAuthFromKeychain(authn.DefaultKeychain)); err != nil {
		t.Fatalf("read back from test registry: %v", err)
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/testregistry/... -v`
Expected: FAIL — `New` is undefined.

- [ ] **Step 4: Implement `testregistry.New`**

```go
// Package testregistry starts an in-process OCI registry for tests, so no
// test that packages or installs from OCI touches the network.
package testregistry

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"
)

// New starts an in-process registry and returns its host:port. t.Cleanup
// closes it, so no caller needs to.
func New(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(registry.New())
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/testregistry/... -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/testregistry
git commit -m "test: add an in-process OCI registry fixture"
```

---

### Task 4: `internal/ocix` — the OCI registry seam

**Files:**
- Create: `internal/ocix/ocix.go`
- Test: `internal/ocix/ocix_test.go`

**Interfaces:**
- Consumes: `gitx.Untar(r io.Reader, dest string) error` (Task 1);
  `testregistry.New(t *testing.T) string` (Task 3).
- Produces:
  ```go
  type OCI interface {
      Resolve(ctx context.Context, ref string) (string, error)
      Pull(ctx context.Context, ref, dest string) error
      Push(ctx context.Context, ref string, r io.Reader) error
  }
  type Registry struct{}
  func New() Registry
  ```
  `Resolve` returns the digest as `"sha256:...."`. `Pull` extracts the
  image's single layer into `dest` using `gitx.Untar`. `Push` builds a
  one-layer image from the uncompressed tar in `r` and writes it to `ref`.

- [ ] **Step 1: Write the failing tests**

```go
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/ocix/... -v`
Expected: FAIL — `New` is undefined.

- [ ] **Step 3: Implement `internal/ocix/ocix.go`**

```go
// Package ocix wraps an OCI registry client. Using go-containerregistry
// rather than shelling out to docker is deliberate: it still reads Docker's
// own config and credential helpers for auth, so nothing here reimplements
// login, but the calls are typed and need no binary on PATH.
package ocix

import (
	"context"
	"fmt"
	"io"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/google/go-containerregistry/pkg/v1/types"

	"github.com/richardcase/skillsctl/internal/gitx"
)

// LayerMediaType identifies a skillsctl skills layer, so a registry that
// distinguishes artifact types does not mistake it for a runnable container
// layer.
const LayerMediaType types.MediaType = "application/vnd.skillsctl.skills.layer.v1.tar"

// OCI is the set of registry operations skillsctl needs to package and
// install skills as an OCI artifact.
type OCI interface {
	// Resolve returns the digest ref currently points at. It fetches only
	// the manifest, never a layer — the same "no fetch" cost as git's
	// ls-remote.
	Resolve(ctx context.Context, ref string) (string, error)
	// Pull extracts the single skills layer at ref into dest.
	Pull(ctx context.Context, ref, dest string) error
	// Push builds a one-layer artifact from the uncompressed tar stream r
	// and writes it to ref.
	Push(ctx context.Context, ref string, r io.Reader) error
}

// Registry implements OCI against a real registry.
type Registry struct{}

// New returns a Registry authenticated through Docker's default keychain.
func New() Registry { return Registry{} }

func (Registry) Resolve(ctx context.Context, ref string) (string, error) {
	r, err := name.ParseReference(ref)
	if err != nil {
		return "", fmt.Errorf("parse %q: %w", ref, err)
	}
	desc, err := remote.Get(r, remote.WithContext(ctx), remote.WithAuthFromKeychain(authn.DefaultKeychain))
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", ref, err)
	}
	return desc.Digest.String(), nil
}

func (Registry) Pull(ctx context.Context, ref, dest string) error {
	r, err := name.ParseReference(ref)
	if err != nil {
		return fmt.Errorf("parse %q: %w", ref, err)
	}
	desc, err := remote.Get(r, remote.WithContext(ctx), remote.WithAuthFromKeychain(authn.DefaultKeychain))
	if err != nil {
		return fmt.Errorf("resolve %s: %w", ref, err)
	}
	img, err := desc.Image()
	if err != nil {
		return fmt.Errorf("read image %s: %w", ref, err)
	}
	layers, err := img.Layers()
	if err != nil {
		return fmt.Errorf("read layers of %s: %w", ref, err)
	}
	if len(layers) != 1 {
		return fmt.Errorf("%s holds %d layers, expected exactly one skills layer", ref, len(layers))
	}
	rc, err := layers[0].Uncompressed()
	if err != nil {
		return fmt.Errorf("read skills layer of %s: %w", ref, err)
	}
	defer func() { _ = rc.Close() }()
	if err := gitx.Untar(rc, dest); err != nil {
		return fmt.Errorf("extract %s: %w", ref, err)
	}
	return nil
}

func (Registry) Push(ctx context.Context, ref string, r io.Reader) error {
	dst, err := name.ParseReference(ref)
	if err != nil {
		return fmt.Errorf("parse %q: %w", ref, err)
	}
	layer, err := tarball.LayerFromReader(r, tarball.WithMediaType(LayerMediaType))
	if err != nil {
		return fmt.Errorf("build skills layer: %w", err)
	}
	img, err := mutate.AppendLayers(empty.Image, layer)
	if err != nil {
		return fmt.Errorf("build image: %w", err)
	}
	if err := remote.Write(dst, img, remote.WithContext(ctx), remote.WithAuthFromKeychain(authn.DefaultKeychain)); err != nil {
		return fmt.Errorf("push %s: %w", ref, err)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/ocix/... -v`
Expected: PASS. If `tarball.WithMediaType`, `mutate.AppendLayers`, or
`remote.Get`/`remote.Write` option names differ from this draft in the
`go-containerregistry` version `go get` resolved, run `go doc` against the
installed module (e.g. `go doc github.com/google/go-containerregistry/pkg/v1/tarball`)
and adjust the call to match — the shape (parse ref, build one layer, append
to `empty.Image`, write) is what matters, not the exact option name.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/ocix
git commit -m "feat(ocix): add an OCI registry seam for packaging and install"
```

---

### Task 5: `internal/source` — the `oci://` channel

**Files:**
- Modify: `internal/source/source.go`
- Test: `internal/source/source_test.go` (add cases; file already exists)

**Interfaces:**
- Produces: `source.ChannelOCI`; `Source.Registry`, `Source.Repository`,
  `Source.Tag` fields; `Source.Slug()` and `Source.DefaultName()` handle
  `ChannelOCI`.

- [ ] **Step 1: Write the failing tests**

Add to `internal/source/source_test.go`:

```go
func TestParseOCIReference(t *testing.T) {
	s, err := Parse("oci://ghcr.io/richardcase/skills:v1")
	if err != nil {
		t.Fatal(err)
	}
	if s.Channel != ChannelOCI {
		t.Errorf("Channel = %q, want %q", s.Channel, ChannelOCI)
	}
	if s.Registry != "ghcr.io" {
		t.Errorf("Registry = %q, want ghcr.io", s.Registry)
	}
	if s.Repository != "richardcase/skills" {
		t.Errorf("Repository = %q, want richardcase/skills", s.Repository)
	}
	if s.Tag != "v1" {
		t.Errorf("Tag = %q, want v1", s.Tag)
	}
}

func TestParseOCIReferenceRequiresATag(t *testing.T) {
	if _, err := Parse("oci://ghcr.io/richardcase/skills"); err == nil {
		t.Fatal("expected an error for an OCI reference with no tag")
	}
}

func TestOCISlugIsStableAndFilesystemSafe(t *testing.T) {
	s, err := Parse("oci://ghcr.io/richardcase/skills:v1")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := s.Slug(), "oci/ghcr.io/richardcase/skills"; got != want {
		t.Errorf("Slug() = %q, want %q", got, want)
	}
}

func TestOCIDefaultNameIsTheLastRepositorySegment(t *testing.T) {
	s, err := Parse("oci://ghcr.io/richardcase/skills:v1")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := s.DefaultName(), "skills"; got != want {
		t.Errorf("DefaultName() = %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/source/... -v -run OCI`
Expected: FAIL — `ChannelOCI` is undefined.

- [ ] **Step 3: Add the channel constant and fields**

In `internal/source/source.go`, extend the `Channel` block and `Source`
struct:

```go
const (
	ChannelGit    Channel = "git"
	ChannelPlugin Channel = "plugin"
	ChannelLocal  Channel = "local"
	// ChannelOCI represents installation from an OCI registry.
	ChannelOCI Channel = "oci"
)
```

```go
type Source struct {
	Channel Channel

	// Git channel.
	RepoURL string
	Subpath string

	// Plugin channel.
	Plugin      string
	Marketplace string

	// Local channel.
	Path string

	// OCI channel.
	Registry   string // registry host[:port]
	Repository string // path within the registry, e.g. "owner/skills"
	Tag        string

	Raw string

	host  string
	owner string
	repo  string
}
```

- [ ] **Step 4: Recognise `oci://` in `parseChannel`**

`oci://` already matches the `strings.Contains(raw, "://")` branch and would
otherwise fall into `parseURL`'s git assumptions, so it needs its own case
ahead of that branch:

```go
case strings.HasPrefix(raw, "oci://"):
	return parseOCI(raw)

case strings.Contains(raw, "://"):
	return parseURL(raw)
```

Add `parseOCI` beside `parseURL`:

```go
// parseOCI reads an explicit oci://registry/repository:tag reference. The
// scheme is required rather than inferred from shape, so an OCI source never
// collides with the owner/repo git shorthand.
func parseOCI(raw string) (Source, error) {
	s := Source{Raw: raw, Channel: ChannelOCI}

	rest := strings.TrimPrefix(raw, "oci://")
	repoPart, tag, ok := strings.Cut(rest, ":")
	if !ok || tag == "" {
		return s, fmt.Errorf("oci source %q has no :tag", raw)
	}

	parts := strings.SplitN(repoPart, "/", 2)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return s, fmt.Errorf("oci source %q has no repository path after the registry host", raw)
	}

	s.Registry = parts[0]
	s.Repository = parts[1]
	s.Tag = tag
	return s, nil
}
```

- [ ] **Step 5: Add the `Slug()` and `DefaultName()` cases**

In `Slug()`:

```go
func (s Source) Slug() string {
	switch s.Channel {
	case ChannelGit:
		// ...unchanged...
	case ChannelPlugin:
		return slugPath("plugin", s.Marketplace, s.Plugin)
	case ChannelOCI:
		return slugPath("oci", s.Registry, s.Repository)
	default:
		return slugPath("local", s.Path)
	}
}
```

In `DefaultName()`:

```go
func (s Source) DefaultName() string {
	switch s.Channel {
	case ChannelPlugin:
		return s.Plugin
	case ChannelLocal:
		return path.Base(strings.TrimSuffix(s.Path, "/"))
	case ChannelOCI:
		return path.Base(s.Repository)
	default:
		if s.Subpath != "" {
			return path.Base(s.Subpath)
		}
		return s.repo
	}
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/source/... -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/source
git commit -m "feat(source): recognise oci:// references"
```

---

### Task 6: `internal/store` — `EnsureOCI`

Mirrors `Store.Ensure`'s shape, but an OCI pull has no separate mirror step —
each pull is already content-addressed by the digest it lands at, so there is
nothing to cache beyond the revision directory itself.

**Files:**
- Modify: `internal/store/store.go`
- Test: `internal/store/store_test.go` (add case; file already exists)

**Interfaces:**
- Consumes: `ocix.OCI.Pull(ctx, ref, dest string) error` (Task 4).
- Produces: `func (s *Store) EnsureOCI(ctx context.Context, o ocix.OCI, slug, ref, digest string) (string, error)`.

- [ ] **Step 1: Write the failing test**

Add to `internal/store/store_test.go`:

```go
type fakeOCI struct {
	pullCount int
}

func (f *fakeOCI) Resolve(context.Context, string) (string, error) { return "", nil }

func (f *fakeOCI) Pull(_ context.Context, _, dest string) error {
	f.pullCount++
	return os.WriteFile(filepath.Join(dest, "SKILL.md"), []byte("---\nname: x\n---\n"), 0o644)
}

func (f *fakeOCI) Push(context.Context, string, io.Reader) error { return nil }

func TestEnsureOCIExtractsOnceAndIsIdempotent(t *testing.T) {
	s := New(t.TempDir())
	o := &fakeOCI{}

	rev, err := s.EnsureOCI(context.Background(), o, "oci/ghcr.io/owner/skills", "ghcr.io/owner/skills:v1", "sha256:abc")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(rev, "SKILL.md")); err != nil {
		t.Fatalf("revision directory missing SKILL.md: %v", err)
	}

	if _, err := s.EnsureOCI(context.Background(), o, "oci/ghcr.io/owner/skills", "ghcr.io/owner/skills:v1", "sha256:abc"); err != nil {
		t.Fatal(err)
	}
	if o.pullCount != 1 {
		t.Errorf("Pull was called %d times, want 1 (EnsureOCI must be a no-op once the digest is present)", o.pullCount)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/store/... -v -run EnsureOCI`
Expected: FAIL — `EnsureOCI` is undefined.

- [ ] **Step 3: Implement `EnsureOCI`**

Add to `internal/store/store.go`, importing `"github.com/richardcase/skillsctl/internal/ocix"`:

```go
// EnsureOCI guarantees the revision at digest is extracted, returning its
// path. It is a no-op when the revision is already present, so it is safe to
// call on every install including a --dry-run.
//
// Unlike Ensure there is no separate mirror step: an OCI pull already lands
// at a specific digest, so there is nothing worth caching beyond the
// revision directory itself.
func (s *Store) EnsureOCI(ctx context.Context, o ocix.OCI, slug, ref, digest string) (string, error) {
	rev := s.RevPath(slug, digest)
	if err := s.within(rev); err != nil {
		return "", err
	}

	if fi, err := os.Stat(rev); err == nil && fi.IsDir() {
		return rev, nil
	}

	if err := os.MkdirAll(filepath.Dir(rev), 0o755); err != nil {
		return "", fmt.Errorf("create revision directory: %w", err)
	}

	tmp, err := os.MkdirTemp(filepath.Dir(rev), ".tmp-")
	if err != nil {
		return "", fmt.Errorf("create temp revision directory: %w", err)
	}
	defer func() {
		if rerr := os.RemoveAll(tmp); rerr != nil {
			fmt.Fprintf(os.Stderr, "skillsctl: could not remove temporary extraction directory %s: %v\n", tmp, rerr)
		}
	}()

	if err := o.Pull(ctx, ref, tmp); err != nil {
		return "", fmt.Errorf("pull %s: %w", ref, err)
	}
	if err := os.Rename(tmp, rev); err != nil {
		if fi, serr := os.Stat(rev); serr == nil && fi.IsDir() {
			return rev, nil
		}
		return "", fmt.Errorf("publish revision: %w", err)
	}
	return rev, nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/store/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store
git commit -m "feat(store): add EnsureOCI to extract an OCI-pulled revision"
```

---

### Task 7: `internal/channel.OCI` — the channel itself

Mirrors `Git` in `internal/channel/git.go` closely: same `Candidate`
narrowing, same receipt shape, `linked` supplies `Remove`/`Link`/`Agents`.

**Files:**
- Create: `internal/channel/oci.go`
- Test: `internal/channel/oci_test.go`
- Modify: `internal/channel/channel.go` (`Registry` struct + `For`)
- Modify: `internal/channel/pin.go` (add `OCI.Pin`)

**Interfaces:**
- Consumes: `store.EnsureOCI` (Task 6); `ocix.OCI` (Task 4);
  `discover.Walk`/`discover.PluginMeta` (existing); `channel.linked`
  (existing, `internal/channel/linked.go`).
- Produces: `channel.NewOCI(st *store.Store, o ocix.OCI) *OCI`; `OCI`
  satisfies `channel.Channel`; `channel.Registry.OCI` field.

- [ ] **Step 1: Write the failing tests**

```go
package channel

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/store"
	"github.com/richardcase/skillsctl/internal/target"
)

// fakeOCI is a fixed single-skill image at one digest, with a call counter
// so a test can assert Resolve is cheap.
type fakeOCI struct {
	digest      string
	resolveHits int
}

func (f *fakeOCI) Resolve(context.Context, string) (string, error) {
	f.resolveHits++
	return f.digest, nil
}

func (f *fakeOCI) Pull(_ context.Context, _, dest string) error {
	dir := filepath.Join(dest, "alpha")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: alpha\ndescription: a skill\n---\n"), 0o644)
}

func (f *fakeOCI) Push(context.Context, string, io.Reader) error { return nil }

func TestOCIPrepareFindsTheSkillAtTheResolvedDigest(t *testing.T) {
	st := store.New(t.TempDir())
	o := &fakeOCI{digest: "sha256:aaa"}
	c := NewOCI(st, o)

	src, err := source.Parse("oci://ghcr.io/owner/skills:v1")
	if err != nil {
		t.Fatal(err)
	}

	cands, err := c.Prepare(context.Background(), Request{Source: src, All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 || cands[0].Name != "alpha" {
		t.Fatalf("Prepare() = %+v, want one candidate named alpha", cands)
	}
	if cands[0].Version != "sha256:aaa" {
		t.Errorf("Version = %q, want the resolved digest", cands[0].Version)
	}
}

func TestOCIInstallRecordsAReceiptTrackingTheTag(t *testing.T) {
	st := store.New(t.TempDir())
	o := &fakeOCI{digest: "sha256:aaa"}
	c := NewOCI(st, o)

	src, _ := source.Parse("oci://ghcr.io/owner/skills:v1")
	cands, err := c.Prepare(context.Background(), Request{Source: src, All: true})
	if err != nil {
		t.Fatal(err)
	}

	tgt := target.Target{Name: "claude", Dir: t.TempDir()}
	req := Request{Source: src, Targets: []target.Target{tgt}, All: true}
	_, receipts, _, err := c.Install(req, cands)
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 1 {
		t.Fatalf("got %d receipts, want 1", len(receipts))
	}
	r := receipts[0]
	if r.Channel != "oci" {
		t.Errorf("Channel = %q, want oci", r.Channel)
	}
	if r.Ref != "v1" {
		t.Errorf("Ref = %q, want the tag v1", r.Ref)
	}
	if r.Resolved != "sha256:aaa" {
		t.Errorf("Resolved = %q, want the digest", r.Resolved)
	}
}

func TestOCIUpdateRelinksWhenTheDigestMoved(t *testing.T) {
	st := store.New(t.TempDir())
	o := &fakeOCI{digest: "sha256:aaa"}
	c := NewOCI(st, o)

	r := &state.Receipt{
		Name: "alpha", Channel: "oci", Source: "ghcr.io/owner/skills:v1",
		Slug: "oci/ghcr.io/owner/skills", Ref: "v1", Resolved: "sha256:old",
		RevPath: filepath.Join(t.TempDir(), "gone"),
	}

	verdicts, p, err := c.Update(context.Background(), []*state.Receipt{r}, UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(verdicts) != 1 || verdicts[0].Status != StatusUpdated {
		t.Fatalf("verdicts = %+v, want one StatusUpdated", verdicts)
	}
	if p.IsEmpty() {
		t.Error("expected a non-empty plan for a moved digest")
	}
}

func TestOCIOwnershipIsStoreOwned(t *testing.T) {
	c := NewOCI(store.New(t.TempDir()), &fakeOCI{})
	if c.Ownership() != StoreOwned {
		t.Errorf("Ownership() = %v, want StoreOwned", c.Ownership())
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/channel/... -v -run OCI`
Expected: FAIL — `NewOCI` is undefined.

- [ ] **Step 3: Implement `internal/channel/oci.go`**

```go
package channel

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"time"

	"github.com/richardcase/skillsctl/internal/discover"
	"github.com/richardcase/skillsctl/internal/ocix"
	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/store"
)

// OCI installs skills packaged into an OCI artifact: an immutable revision
// directory per digest, and a symlink per agent — the same shape Git gives a
// sha, with a registry standing in for a repository.
type OCI struct {
	linked

	store *store.Store
	oci   ocix.OCI
}

// NewOCI returns the OCI channel backed by st and o.
func NewOCI(st *store.Store, o ocix.OCI) *OCI { return &OCI{store: st, oci: o} }

// Ownership reports that the store holds the files and the links undo them.
func (c *OCI) Ownership() Ownership { return StoreOwned }

func ociRef(src source.Source, tag string) string {
	return fmt.Sprintf("%s/%s:%s", src.Registry, src.Repository, tag)
}

// Prepare resolves the tag to a digest, extracts the revision, and narrows
// the skills it found to the ones the request asked for.
func (c *OCI) Prepare(ctx context.Context, req Request) ([]Candidate, error) {
	src := req.Source

	tag := req.Ref
	if tag == "" {
		tag = src.Tag
	}
	ref := ociRef(src, tag)

	digest, err := c.oci.Resolve(ctx, ref)
	if err != nil {
		return nil, err
	}

	revRoot, err := c.store.EnsureOCI(ctx, c.oci, src.Slug(), ref, digest)
	if err != nil {
		return nil, err
	}

	found, err := discover.Walk(revRoot)
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("%s: %w", revRoot, discover.ErrNoSkill)
	}

	available, err := resolveNames(found, src.DefaultName())
	if err != nil {
		return nil, err
	}

	chosen, err := narrow(available, req)
	if err != nil {
		var amb *Ambiguous
		if errors.As(err, &amb) {
			amb.Header = fmt.Sprintf("skills in %s:", ref)
			amb.Meta = discover.PluginMeta(revRoot)
			amb.Available = brief(available)
			amb.Resolved = digest
		}
		return nil, err
	}

	return c.candidates(chosen, revRoot, digest)
}

func (c *OCI) candidates(sels []selection, revRoot, digest string) ([]Candidate, error) {
	out := make([]Candidate, 0, len(sels))
	for _, s := range sels {
		hash, err := store.HashDir(s.skill.Dir)
		if err != nil {
			return nil, err
		}
		subpath, err := filepath.Rel(revRoot, s.skill.Dir)
		if err != nil {
			return nil, err
		}
		if subpath = filepath.ToSlash(subpath); subpath == "." {
			subpath = ""
		}
		out = append(out, Candidate{
			Name:    s.name,
			Desc:    s.skill.Description,
			Path:    s.skill.Dir,
			Subpath: subpath,
			Version: digest,
			Hash:    hash,
		})
	}
	return out, nil
}

// Install links each candidate into each target and records a receipt.
func (c *OCI) Install(req Request, chosen []Candidate) (plan.Plan, []state.Receipt, []string, error) {
	var p plan.Plan
	receipts := make([]state.Receipt, 0, len(chosen))
	now := time.Now().UTC()

	tag := req.Ref
	if tag == "" {
		tag = req.Source.Tag
	}
	ref := ociRef(req.Source, tag)

	for _, s := range chosen {
		receipt := state.Receipt{
			Name:        s.Name,
			Channel:     string(source.ChannelOCI),
			Source:      ref,
			Slug:        req.Source.Slug(),
			Subpath:     s.Subpath,
			Resolved:    s.Version,
			Pinned:      req.Pin,
			RevPath:     s.Path,
			ContentHash: s.Hash,
			InstalledAt: now,
			UpdatedAt:   now,
		}
		if !req.Pin {
			receipt.Ref = tag
		}

		for _, t := range req.Targets {
			linkPath, err := linkPathFor(t, s.Name)
			if err != nil {
				return p, nil, nil, err
			}
			p.Add(plan.Link{Target: t.Name, LinkPath: linkPath, RevPath: s.Path})
			receipt.Links = append(receipt.Links, state.Link{Target: t.Name, Path: linkPath})
		}
		p.Add(plan.Record{Receipt: receipt})
		receipts = append(receipts, receipt)
	}
	return p, receipts, nil, nil
}

// Update re-points each receipt at the current digest of the tag it tracks.
func (c *OCI) Update(ctx context.Context, rs []*state.Receipt, o UpdateOptions) ([]Verdict, plan.Plan, error) {
	seen := map[string]resolution{}
	verdicts := make([]Verdict, 0, len(rs))

	var p plan.Plan
	now := time.Now().UTC()

	for _, r := range rs {
		v := Verdict{Name: r.Name, Channel: r.Channel, Ref: r.Ref, Current: r.Resolved, Pinned: r.Pinned}

		if r.Pinned && !o.Named {
			v.Status = StatusPinned
			verdicts = append(verdicts, v)
			continue
		}

		got, ok := seen[r.Source]
		if !ok {
			got.sha, got.err = c.oci.Resolve(ctx, r.Source)
			seen[r.Source] = got
		}
		if got.err != nil {
			verdicts = append(verdicts, fail(v, got.err))
			continue
		}

		v.Latest = got.sha
		if got.sha == r.Resolved {
			v.Status = StatusCurrent
			verdicts = append(verdicts, v)
			continue
		}

		dirty, note, err := inspect(r)
		if err != nil {
			verdicts = append(verdicts, fail(v, err))
			continue
		}
		if dirty && !o.Force {
			v.Status = StatusDirty
			verdicts = append(verdicts, v)
			continue
		}
		v.Note = note

		ops, receipt, err := c.relink(ctx, r, got.sha, now)
		if err != nil {
			verdicts = append(verdicts, fail(v, err))
			continue
		}

		p.Add(ops...)
		p.Add(plan.Record{Receipt: receipt})
		v.Status = StatusUpdated
		verdicts = append(verdicts, v)
	}

	return verdicts, p, nil
}

// Settle has nothing to complete: the digest is known before the plan is
// built, and so is every path derived from it.
func (c *OCI) Settle(context.Context, []state.Receipt) ([]state.Receipt, error) {
	return nil, nil
}

func (c *OCI) relink(ctx context.Context, r *state.Receipt, digest string, now time.Time) ([]plan.Op, state.Receipt, error) {
	slug := r.Slug
	if slug == "" {
		src, err := source.Parse(r.Source)
		if err != nil {
			return nil, state.Receipt{}, fmt.Errorf("this receipt records no store location and %q cannot be parsed: %w", r.Source, err)
		}
		slug = src.Slug()
	}

	revRoot, err := c.store.EnsureOCI(ctx, c.oci, slug, r.Source, digest)
	if err != nil {
		return nil, state.Receipt{}, err
	}
	revPath, err := store.Join(revRoot, r.Subpath)
	if err != nil {
		return nil, state.Receipt{}, err
	}

	if _, err := discover.Root(revPath); err != nil {
		return nil, state.Receipt{}, fmt.Errorf("%s no longer holds a skill at %s: %w", ociShortDigest(digest), subpathOrRoot(r.Subpath), err)
	}

	hash, err := store.HashDir(revPath)
	if err != nil {
		return nil, state.Receipt{}, err
	}

	ops := make([]plan.Op, 0, len(r.Links))
	for _, l := range r.Links {
		ops = append(ops, plan.Relink{Target: l.Target, LinkPath: l.Path, RevPath: revPath})
	}

	receipt := *r
	receipt.Resolved = digest
	receipt.RevPath = revPath
	receipt.ContentHash = hash
	receipt.UpdatedAt = now
	return ops, receipt, nil
}

func ociShortDigest(digest string) string {
	if len(digest) > 17 {
		return digest[:17]
	}
	return digest
}
```

`inspect`, `resolveNames`, `narrow`, `brief`, `selection`, `fail`,
`subpathOrRoot`, `resolution` and `linkPathFor` are unexported helpers
already defined in `internal/channel/git.go` and `internal/channel/linked.go`
— they are package-level, so `oci.go` reuses them directly with no import
needed beyond what is already listed.

- [ ] **Step 4: Add `OCI.Pin` in `internal/channel/pin.go`**

Append, keeping the existing "all implementations live here" convention:

```go
// Pin freezes this receipt at the digest it is already on, or releases it to
// track a tag again. Identical in shape to Git.Pin — a tag stands in for a
// branch, a digest for a sha.
func (c *OCI) Pin(r state.Receipt, o PinOptions) (plan.Plan, PinResult, error) {
	var p plan.Plan

	if r.Pinned == o.On {
		return p, PinResult{Receipt: r}, nil
	}

	res := PinResult{Receipt: r, Changed: true}
	res.Receipt.Pinned = o.On
	res.Receipt.UpdatedAt = time.Now().UTC()

	if o.On {
		res.Receipt.Ref = ""
	} else {
		res.Receipt.Ref = o.Ref
		if !c.store.Contains(r.RevPath) {
			res.Note = fmt.Sprintf("its files are at %s, and the next update will re-point the symlinks into skillsctl's store", r.RevPath)
		}
	}

	p.Add(plan.Record{Receipt: res.Receipt})
	return p, res, nil
}
```

- [ ] **Step 5: Wire `OCI` into `channel.Registry`**

In `internal/channel/channel.go`:

```go
type Registry struct {
	Git    Channel
	Plugin Channel
	Local  Channel
	OCI    Channel
}
```

```go
func (r Registry) For(c source.Channel) (Channel, error) {
	switch c {
	case source.ChannelGit:
		if r.Git != nil {
			return r.Git, nil
		}
	case source.ChannelPlugin:
		if r.Plugin != nil {
			return r.Plugin, nil
		}
	case source.ChannelLocal:
		if r.Local != nil {
			return r.Local, nil
		}
	case source.ChannelOCI:
		if r.OCI != nil {
			return r.OCI, nil
		}
	}
	return nil, fmt.Errorf("the %s channel is %w", c, ErrUnsupported)
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/channel/... -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/channel
git commit -m "feat(channel): add the OCI channel"
```

---

### Task 8: `internal/outdated` — the OCI branch

**Files:**
- Modify: `internal/outdated/outdated.go`
- Test: `internal/outdated/outdated_test.go` (add case; file already exists)

**Interfaces:**
- Consumes: `ocix.OCI` (Task 4).
- Produces: `outdated.Check` gains a fifth parameter,
  `o ocix.OCI`, placed after `g gitx.Git` to match the existing
  git-then-plugin-then-everything-else reading order.

- [ ] **Step 1: Write the failing test**

Add to `internal/outdated/outdated_test.go`, reusing the `fakePlugins` type
already defined there (`internal/outdated/outdated_test.go:171-183`, a
pointer receiver, so it is constructed as `&fakePlugins{}`):

```go
type fakeOCI struct{ digest string }

func (f fakeOCI) Resolve(context.Context, string) (string, error) { return f.digest, nil }
func (f fakeOCI) Pull(context.Context, string, string) error      { return nil }
func (f fakeOCI) Push(context.Context, string, io.Reader) error   { return nil }

func TestCheckReportsAnOCIReceiptOutdatedWhenTheDigestMoved(t *testing.T) {
	r := &state.Receipt{Name: "alpha", Channel: "oci", Source: "ghcr.io/owner/skills:v1", Ref: "v1", Resolved: "sha256:old"}

	entries := Check(context.Background(), gitx.New(), &fakePlugins{}, fakeOCI{digest: "sha256:new"}, []*state.Receipt{r})

	if len(entries) != 1 || entries[0].Status != StatusOutdated {
		t.Fatalf("entries = %+v, want one StatusOutdated", entries)
	}
	if entries[0].Latest != "sha256:new" {
		t.Errorf("Latest = %q, want sha256:new", entries[0].Latest)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/outdated/... -v -run OCI`
Expected: FAIL — `Check`'s signature does not accept a fourth argument yet.

- [ ] **Step 3: Extend `Check`**

In `internal/outdated/outdated.go`:

```go
import (
	// ...existing imports...
	"github.com/richardcase/skillsctl/internal/ocix"
)

func Check(ctx context.Context, g gitx.Git, p claudex.Plugins, o ocix.OCI, receipts []*state.Receipt) []Entry {
	seen := map[string]resolution{}
	entries := make([]Entry, 0, len(receipts))

	var installed []claudex.Installed
	var listErr error
	var listed bool
	plugins := func() ([]claudex.Installed, error) {
		if !listed {
			listed = true
			installed, listErr = p.List(ctx)
		}
		return installed, listErr
	}

	for _, r := range receipts {
		e := Entry{
			Name:    r.Name,
			Channel: r.Channel,
			Source:  r.Source,
			Current: r.Resolved,
			Pinned:  r.Pinned,
		}

		if r.Channel == string(source.ChannelPlugin) {
			entries = append(entries, checkPlugin(e, r, plugins))
			continue
		}

		if r.Channel == string(source.ChannelOCI) {
			e.Ref = r.Ref
			got, ok := seen[r.Source]
			if !ok {
				got.sha, got.err = o.Resolve(ctx, r.Source)
				seen[r.Source] = got
			}
			if got.err != nil {
				e.Status = StatusError
				e.Error = got.err.Error()
				entries = append(entries, e)
				continue
			}
			e.Latest = got.sha
			e.Status = StatusCurrent
			if got.sha != r.Resolved {
				e.Status = StatusOutdated
			}
			entries = append(entries, e)
			continue
		}

		ref := r.Ref
		if ref == "" {
			ref = "HEAD"
		}
		e.Ref = ref

		if r.Channel != string(source.ChannelGit) {
			e.Status = StatusSkipped
			entries = append(entries, e)
			continue
		}

		key := r.Source + "\x00" + ref
		got, ok := seen[key]
		if !ok {
			got.sha, got.err = g.Resolve(ctx, r.Source, ref)
			seen[key] = got
		}

		latest, err := got.sha, got.err
		if err != nil {
			e.Status = StatusError
			e.Error = err.Error()
			entries = append(entries, e)
			continue
		}

		e.Latest = latest
		e.Status = StatusCurrent
		if latest != r.Resolved {
			e.Status = StatusOutdated
		}
		entries = append(entries, e)
	}
	return entries
}
```

The `seen` map is shared across git and OCI keys; a git key is always
`source + "\x00" + ref` while an OCI key is the bare `r.Source` (already the
full `registry/repo:tag` ref, so it needs no ref suffix) — the two key shapes
never collide because a git `r.Source` is a clone URL, never `host/path:tag`.

- [ ] **Step 4: Update the call site**

In `internal/cli/outdated.go:46`, add a `newOCI` seam next to the existing
`newRunner`/`newPlugins`/`newPicker` vars in `internal/cli/context.go`:

```go
// newOCI builds the wrapper around the OCI registry client. Tests replace
// it, so that no test reaches a real registry.
var newOCI = func() ocix.OCI { return ocix.New() }
```

Then in `internal/cli/outdated.go`:

```go
entries := outdated.Check(cmd.Context(), gitx.New(), newPlugins(), newOCI(), receipts)
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/outdated/... ./internal/cli/... -v -run Outdated`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/outdated internal/cli/context.go internal/cli/outdated.go
git commit -m "feat(outdated): detect a moved OCI tag"
```

---

### Task 9: `internal/cli` — wire the OCI channel into every command and add `package`

**Files:**
- Modify: `internal/cli/context.go` (`channels()`)
- Modify: `internal/cli/cli_test.go` (`harness` gains an `oci` seam)
- Create: `internal/cli/package.go`
- Test: `internal/cli/package_test.go`

**Interfaces:**
- Consumes: `channel.NewOCI` (Task 7); `pack.Tar` (Task 2); `ocix.OCI`
  (Task 4); `discover.Walk` (existing); `newOCI` (Task 8); the existing
  `harness`/`newHarness(t)` test fixture in `internal/cli/cli_test.go:60-107`,
  which already swaps `newPlugins`/`newPicker`/`newRunner` for the duration
  of a test and restores them in `t.Cleanup`.
- Produces: `skillsctl package <source-dir> <oci-ref>` CLI command.

- [ ] **Step 1: Wire `OCI` into `env.channels()`**

In `internal/cli/context.go`:

```go
func (e *env) channels() channel.Registry {
	return channel.Registry{
		Git:    channel.NewGit(e.store, gitx.New()),
		Plugin: channel.NewPlugin(newPlugins(), e.cfg),
		Local:  channel.NewLocal(e.store),
		OCI:    channel.NewOCI(e.store, newOCI()),
	}
}
```

Add the `"github.com/richardcase/skillsctl/internal/ocix"` import.

- [ ] **Step 2: Give `harness` an OCI seam**

`newHarness` (`internal/cli/cli_test.go:60-107`) already swaps
`newPlugins`/`newPicker`/`newRunner` to fakes for the whole test and restores
them on cleanup, so no test reaches a real binary. Extend it the same way
for `newOCI`, defaulting to a fake that fails loudly if a test forgets to set
it — that keeps the "no test touches the network" constraint honest instead
of silently attempting a real push.

Add to the `harness` struct:

```go
	// oci answers every OCI call for the duration of the test. It defaults to
	// a fake that refuses, so a test that means to exercise the OCI channel
	// must say so by setting h.oci, the same bargain h.plugins already makes.
	oci ocix.OCI
```

Add a `refusingOCI` type near `fakePicker`:

```go
// refusingOCI is newHarness's default: any call fails loudly, so a test that
// forgets to set h.oci finds out immediately rather than silently reaching
// for a real registry.
type refusingOCI struct{}

func (refusingOCI) Resolve(context.Context, string) (string, error) {
	return "", errors.New("this test has not configured an OCI registry (set h.oci)")
}
func (refusingOCI) Pull(context.Context, string, string) error {
	return errors.New("this test has not configured an OCI registry (set h.oci)")
}
func (refusingOCI) Push(context.Context, string, io.Reader) error {
	return errors.New("this test has not configured an OCI registry (set h.oci)")
}
```

In `newHarness`, initialise and swap it alongside the existing three:

```go
	h := &harness{
		root:    filepath.Join(root, "store"),
		agents:  agents,
		claude:  filepath.Join(agents, ".claude", "skills"),
		codex:   filepath.Join(agents, ".codex", "skills"),
		plugins: &fakePlugins{root: filepath.Join(root, "plugins")},
		picker:  &fakePicker{},
		oci:     refusingOCI{},
	}

	realPlugins, realRunner, realPicker, realOCI := newPlugins, newRunner, newPicker, newOCI
	newPlugins = func() claudex.Plugins { return h.plugins }
	newPicker = func() picker { return h.picker }
	newOCI = func() ocix.OCI { return h.oci }
	newRunner = func() func(context.Context, []string) error {
		return func(_ context.Context, argv []string) error {
			h.ran = append(h.ran, argv)
			return h.plugins.exec(argv)
		}
	}
	t.Cleanup(func() { newPlugins, newRunner, newPicker, newOCI = realPlugins, realRunner, realPicker, realOCI })
```

Add `"errors"`, `"io"` and
`"github.com/richardcase/skillsctl/internal/ocix"` to `cli_test.go`'s
imports.

- [ ] **Step 3: Write the failing CLI test**

```go
package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type recordingOCI struct {
	pushedRef string
	pushedTar []byte
}

func (r *recordingOCI) Resolve(context.Context, string) (string, error) { return "sha256:dryrun", nil }
func (r *recordingOCI) Pull(context.Context, string, string) error      { return nil }
func (r *recordingOCI) Push(_ context.Context, ref string, body io.Reader) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	r.pushedRef = ref
	r.pushedTar = data
	return nil
}

func TestPackageDryRunListsSkillsWithoutPushing(t *testing.T) {
	h := newHarness(t)
	rec := &recordingOCI{}
	h.oci = rec

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "alpha", "SKILL.md"), []byte("---\nname: alpha\ndescription: a\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := h.run(t, "package", dir, "ghcr.io/owner/skills:v1", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if rec.pushedRef != "" {
		t.Errorf("--dry-run pushed to %q, want no push", rec.pushedRef)
	}
	if !strings.Contains(out, "alpha") {
		t.Errorf("dry-run output %q does not mention the alpha skill", out)
	}
}

func TestPackagePushesTheTarredTree(t *testing.T) {
	h := newHarness(t)
	rec := &recordingOCI{}
	h.oci = rec

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "alpha", "SKILL.md"), []byte("---\nname: alpha\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := h.run(t, "package", dir, "ghcr.io/owner/skills:v1"); err != nil {
		t.Fatal(err)
	}
	if rec.pushedRef != "ghcr.io/owner/skills:v1" {
		t.Errorf("pushed to %q, want ghcr.io/owner/skills:v1", rec.pushedRef)
	}
	if len(rec.pushedTar) == 0 {
		t.Error("pushed an empty tar")
	}
}
```

- [ ] **Step 4: Run the tests to verify they fail**

Run: `go test ./internal/cli/... -v -run Package`
Expected: FAIL — `newPackageCmd` is undefined.

- [ ] **Step 5: Implement `internal/cli/package.go`**

```go
package cli

import (
	"bytes"
	"fmt"

	"github.com/richardcase/skillsctl/internal/discover"
	"github.com/richardcase/skillsctl/internal/pack"
	"github.com/spf13/cobra"
)

type packageOpts struct {
	dryRun bool
}

func newPackageCmd() *cobra.Command {
	var o packageOpts

	cmd := &cobra.Command{
		Use:   "package <source-dir> <oci-ref>",
		Short: "Package a directory of skills into an OCI artifact and push it",
		Long: "Package walks <source-dir>, packages every skill it finds (each SKILL.md\n" +
			"and everything beside and below it) into one OCI artifact, and pushes it to\n" +
			"<oci-ref> (registry/repository:tag). It excludes any .git directory and\n" +
			"anything a .gitignore in the tree marks ignored. Authentication reuses\n" +
			"Docker's own config and credential helpers.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPackage(cmd, args[0], args[1], o)
		},
	}

	cmd.Flags().BoolVar(&o.dryRun, "dry-run", false, "show what would be packaged without pushing")
	return cmd
}

func runPackage(cmd *cobra.Command, dir, ref string, o packageOpts) error {
	found, err := discover.Walk(dir)
	if err != nil {
		return err
	}
	if len(found) == 0 {
		return fmt.Errorf("%s: %w", dir, discover.ErrNoSkill)
	}

	for _, s := range found {
		cmd.Printf("package %s\n", s.Rel)
	}

	if o.dryRun {
		cmd.Printf("would push %s\n", ref)
		return nil
	}

	var buf bytes.Buffer
	if err := pack.Tar(&buf, dir); err != nil {
		return fmt.Errorf("build skills layer: %w", err)
	}

	if err := newOCI().Push(cmd.Context(), ref, &buf); err != nil {
		return err
	}
	cmd.Printf("pushed %s\n", ref)
	return nil
}
```

- [ ] **Step 5: Register the command**

In `internal/cli/root.go`, add `newPackageCmd()` to the `root.AddCommand(...)`
list (alphabetically, between `newOutdatedCmd()` and `newPinCmd()`):

```go
	root.AddCommand(
		newAdoptCmd(),
		newBundleCmd(),
		newDoctorCmd(),
		newGCCmd(),
		newInfoCmd(),
		newInstallCmd(),
		newLinkCmd(),
		newListCmd(),
		newOutdatedCmd(),
		newPackageCmd(),
		newPinCmd(),
		newRemoveCmd(),
		newSyncCmd(),
		newUnpinCmd(),
		newUpdateCmd(),
		newVersionCmd(),
	)
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/cli/... -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/cli
git commit -m "feat(cli): add the package command and wire up the OCI channel"
```

---

### Task 10: End-to-end round trip

Proves `package` → `install` → `outdated` → `update` → `remove` works
together against the in-process registry, the way a real user's session
would.

**Files:**
- Create: `internal/cli/oci_e2e_test.go`

**Interfaces:**
- Consumes: everything above, plus the `harness`/`newHarness(t)` fixture
  extended in Task 9 Step 2 (`internal/cli/cli_test.go`), whose `h.run(t,
  args...)` (`cli_test.go:110-119`) runs `NewRootCmd()` (`root.go:11`) with
  the given args and returns combined stdout+stderr.

- [ ] **Step 1: Write the end-to-end test**

```go
package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/ocix"
	"github.com/richardcase/skillsctl/internal/testregistry"
)

func TestPackageInstallOutdatedUpdateRemoveRoundTrip(t *testing.T) {
	h := newHarness(t)
	// Use the real registry client end to end; only the transport (an
	// in-process httptest server) is fake, so this exercises the exact code
	// path skillsctl runs against a real registry.
	h.oci = ocix.New()

	host := testregistry.New(t)
	ref := host + "/skills:v1"

	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "alpha", "SKILL.md"), []byte("---\nname: alpha\ndescription: a\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if out, err := h.run(t, "package", src, ref); err != nil {
		t.Fatalf("package: %v\n%s", err, out)
	}
	if out, err := h.run(t, "install", "oci://"+ref); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	out, _ := h.run(t, "outdated")
	if !strings.Contains(out, "current") {
		t.Errorf("outdated output %q, want it to report current", out)
	}

	// Repackage the same tag with different content, then confirm outdated
	// notices and update follows it.
	if err := os.WriteFile(filepath.Join(src, "alpha", "SKILL.md"), []byte("---\nname: alpha\ndescription: a\n---\nchanged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := h.run(t, "package", src, ref); err != nil {
		t.Fatalf("re-package: %v\n%s", err, out)
	}

	// outdated exits non-zero once a finding is present (ExitOutdated), so
	// its error is expected here rather than a failure — only the report
	// content is asserted, matching TestOutdatedReportsAMovedRef's pattern
	// in cli_test.go.
	out, _ = h.run(t, "outdated")
	if !strings.Contains(out, "outdated") {
		t.Errorf("outdated output %q, want it to report outdated after repackaging", out)
	}

	if out, err := h.run(t, "update"); err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	if out, err := h.run(t, "remove", "alpha"); err != nil {
		t.Fatalf("remove: %v\n%s", err, out)
	}
}
```

- [ ] **Step 2: Run the test**

Run: `go test ./internal/cli/... -v -run RoundTrip`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/cli/oci_e2e_test.go
git commit -m "test: add an end-to-end OCI package/install/update round trip"
```

---

### Task 11: Documentation and final verification

**Files:**
- Modify: `README.md`
- Modify: `AGENTS.md` (package table)

**Interfaces:** none — this task only documents the surface built above.

- [ ] **Step 1: Update `AGENTS.md`'s package table**

Add rows for the three new packages, in the same one-line style as the
existing entries:

```markdown
| `ocix` | The OCI registry client behind an `OCI` interface: `Resolve`, `Pull`, `Push` |
| `pack` | Builds a `.gitignore`-aware, `.git`-excluding tar stream for packaging |
| `testregistry` | Test-only in-process OCI registry fixture |
```

- [ ] **Step 2: Update `README.md`**

- Add `package` to the commands table, with its `Use` line
  (`skillsctl package <source-dir> <oci-ref>`) and a one-line description.
- Add an `oci://registry/repo:tag` example to the `install` usage section,
  beside the existing owner/repo and git-URL examples.
- Add a bullet to **Features** describing OCI packaging and install, phrased
  as what the user gets ("package skills into an OCI image and install them
  from one, with `outdated`/`update` detecting a moved tag") rather than how
  it is implemented.
- If a **Status** section lists OCI/registry install as a planned-but-not-built
  channel, remove that line now that it is built.

- [ ] **Step 3: Run full verification**

Run: `make test && make lint && make tidy-check`
Expected: all three pass.

- [ ] **Step 4: Commit**

```bash
git add README.md AGENTS.md
git commit -m "docs: describe OCI packaging and install"
```
