# skillsctl Phases 0–1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a `skillsctl` binary with working CI and a tag-triggered release pipeline, plus a walking skeleton that can install a git-hosted skill into every configured agent, list what is installed, and remove it cleanly.

**Architecture:** A source string is parsed into a `Source`; the resolved commit sha is fetched into a content-addressed store of immutable revision directories; each agent's skills directory gets a symlink into that revision; a receipt recording exactly which links were created is written to a JSON DB. Commands build a `Plan` of user-visible mutations which an `Executor` applies, so `--dry-run` is exact and a failed apply rolls back.

**Tech Stack:** Go, cobra (CLI), pelletier/go-toml/v2 (config), gopkg.in/yaml.v3 (SKILL.md frontmatter), gofrs/flock (state locking), the `git` binary (shelled out), GoReleaser + GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-08-13-skillsctl-design.md`

## Global Constraints

- Go module path: `github.com/richardcase/skillsctl`. Binary name: `skillsctl`.
- **All tooling comes from mise**, pinned in a checked-in `mise.toml` — locally and in CI, one source of truth. Never `brew install` a build tool (mise itself is the only exception, since it has to bootstrap from somewhere), and never use `actions/setup-go`.
- Each Bash invocation is a fresh shell, so mise activation does not persist between commands. Every direct tool call in this plan is therefore written as `mise exec -- <tool> …`, and the `Makefile` puts mise's shims directory on `PATH` itself so `make test` / `make lint` / `make snapshot` work unprefixed. If you activate mise in your own shell, the `mise exec --` prefixes become redundant but stay harmless.
- `go.mod` floor: `go 1.25`, matching the Go version pinned in `mise.toml`. Keep the two in step.
- `CGO_ENABLED=0` everywhere. Target platforms: `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`. Windows is out of scope — never add it.
- Dependencies are limited to: `spf13/cobra`, `pelletier/go-toml/v2`, `gopkg.in/yaml.v3`, `gofrs/flock`. Adding any other module requires stopping and asking.
- All git operations shell out to the `git` binary through the `gitx.Git` interface. Never add a Go git library.
- No test may touch the network or the real `~/.claude`, `~/.codex`, `~/.gemini`, or the real store. Tests use `t.TempDir()` and `file://` fixture repositories, with `SKILLSCTL_HOME` and `SKILLSCTL_CONFIG` pointed at temp paths.
- Every symlink removal must verify the path is a symlink first. `skillsctl` never deletes a real directory in an agent's skills dir.
- Commit messages use conventional prefixes (`feat:`, `fix:`, `docs:`, `test:`, `chore:`, `ci:`) — the release changelog groups on them.

## Spec amendments made by this plan

These three refinements are applied to the spec in Task 1, Step 1, so the spec and plan agree:

1. **Plan ops are user-visible mutations only** — `Link`, `Unlink`, `Record`, `Forget`, `Exec`. Fetching and extracting are store *cache population* (`store.Ensure`), performed before planning rather than as plan ops. This is what makes `--dry-run` exact: the skill's name comes from the extracted `SKILL.md`, so it cannot be known until after extraction. Populating a content-addressed cache is idempotent and not a user-visible mutation.
2. **A revision directory holds the whole repository at a sha.** Subpath selection happens at link time, so two skills from different subpaths of one commit share a single revision directory.
3. **Cobra commands live in `internal/cli`**, and `cmd/skillsctl/main.go` is a thin `main`. This makes commands testable end to end without spawning a binary.

---

### Task 1: Toolchain, module scaffold, and version reporting

**Files:**
- Create: `mise.toml`, `go.mod`, `.gitignore`, `cmd/skillsctl/main.go`, `internal/buildinfo/buildinfo.go`
- Test: `internal/buildinfo/buildinfo_test.go`
- Modify: `docs/superpowers/specs/2026-08-13-skillsctl-design.md`

**Interfaces:**
- Consumes: nothing.
- Produces: `buildinfo.Get() Info` where `type Info struct { Version, Commit, Date string }` and `func (i Info) String() string`.

- [ ] **Step 1: Amend the spec with the three refinements**

In `docs/superpowers/specs/2026-08-13-skillsctl-design.md`:

Replace the line ``Ops: `Fetch`, `Extract`, `Link`, `Unlink`, `Exec`, `Record`, `GC`.`` with:

```markdown
Ops: `Link`, `Unlink`, `Record`, `Forget`, `Exec`. A plan contains only
user-visible mutations. Fetching and extracting are store cache population
(`store.Ensure`) performed *before* planning — a skill's name comes from its
extracted `SKILL.md`, so it cannot be known until the revision is on disk.
Populating a content-addressed cache is idempotent, so this keeps `--dry-run`
exact rather than speculative.
```

Replace the `Resolve(source) → …` pipeline block with:

```
Resolve(sha) → Ensure(rev in store) → Discover(SKILL.md) → Plan{Link,Record} → Apply
```

In the "Store layout" section, after the `rev/…` line, add:

```markdown
A revision directory holds the *whole repository* at that sha. Subpath selection
happens at link time, so two skills taken from different subpaths of one commit
share a single revision directory.
```

In "Package layout", change the `cmd/skillsctl/` line to:

```
cmd/skillsctl/            thin main(); calls cli.Execute()
internal/cli/             cobra commands + output rendering
```

and delete the standalone `internal/cli/` line further down.

- [ ] **Step 2: Bootstrap mise**

Neither mise nor Go is installed on this machine. mise is the one tool that has to come from elsewhere:

```bash
brew install mise
mise --version
```

Expected: a version string. If mise is already installed by other means, skip this and carry on.

- [ ] **Step 3: Pin the toolchain in mise.toml**

Create `mise.toml` at the repository root:

```toml
[tools]
go = "1.25"
goreleaser = "2"
golangci-lint = "2"

[env]
# Keep module and build caches inside the repo-independent default locations,
# so CI can cache them by path.
GOTOOLCHAIN = "local"
```

`GOTOOLCHAIN=local` stops the Go toolchain silently downloading a different version than the one mise pinned — without it, a `go` directive newer than the installed toolchain triggers an invisible upgrade and mise's pin stops meaning anything.

Then install and verify:

```bash
mise trust
mise install
mise exec -- go version
mise exec -- goreleaser --version
mise exec -- golangci-lint --version
```

Expected: `go version` reports 1.25.x, goreleaser reports 2.x, golangci-lint reports 2.x.

If you want bare `go` to work in your interactive shell, add mise activation to your profile once — this is optional and the plan does not depend on it:

```bash
echo 'eval "$(mise activate zsh)"' >> ~/.zshrc
```

- [ ] **Step 4: Initialise the module**

```bash
cd /Users/richard/orca/workspaces/skillsctl/firstversion
mise exec -- go mod init github.com/richardcase/skillsctl
```

Create `.gitignore`:

```gitignore
/dist/
/skillsctl
*.test
.DS_Store

# mise.toml is committed; per-machine overrides are not.
mise.local.toml
.mise.local.toml
```

- [ ] **Step 5: Write the failing test**

Create `internal/buildinfo/buildinfo_test.go`:

```go
package buildinfo

import (
	"runtime/debug"
	"testing"
)

func readerFor(mainVersion string, settings map[string]string) func() (*debug.BuildInfo, bool) {
	return func() (*debug.BuildInfo, bool) {
		bi := &debug.BuildInfo{}
		bi.Main.Version = mainVersion
		for k, v := range settings {
			bi.Settings = append(bi.Settings, debug.BuildSetting{Key: k, Value: v})
		}
		return bi, true
	}
}

func TestGetPrefersLdflags(t *testing.T) {
	got := get("v1.2.3", "abcdef1", "2026-08-13T00:00:00Z", readerFor("v9.9.9", nil))
	if got.Version != "v1.2.3" || got.Commit != "abcdef1" {
		t.Fatalf("ldflags must win, got %+v", got)
	}
}

func TestGetFallsBackToModuleVersion(t *testing.T) {
	got := get("", "", "", readerFor("v0.4.0", map[string]string{
		"vcs.revision": "deadbeef",
		"vcs.time":     "2026-01-02T03:04:05Z",
	}))
	if got.Version != "v0.4.0" {
		t.Errorf("Version = %q, want v0.4.0", got.Version)
	}
	if got.Commit != "deadbeef" {
		t.Errorf("Commit = %q, want deadbeef", got.Commit)
	}
	if got.Date != "2026-01-02T03:04:05Z" {
		t.Errorf("Date = %q, want the vcs.time value", got.Date)
	}
}

func TestGetReportsDevelForUnstampedBuild(t *testing.T) {
	got := get("", "", "", readerFor("(devel)", nil))
	if got.Version != "devel" {
		t.Errorf("Version = %q, want devel", got.Version)
	}
}

func TestGetHandlesMissingBuildInfo(t *testing.T) {
	got := get("", "", "", func() (*debug.BuildInfo, bool) { return nil, false })
	if got.Version != "devel" {
		t.Errorf("Version = %q, want devel", got.Version)
	}
}

func TestStringIsSingleLine(t *testing.T) {
	s := Info{Version: "v1.0.0", Commit: "abc1234", Date: "2026-08-13T00:00:00Z"}.String()
	want := "skillsctl v1.0.0 (abc1234, 2026-08-13T00:00:00Z)"
	if s != want {
		t.Errorf("String() = %q, want %q", s, want)
	}
}
```

- [ ] **Step 6: Run the test to verify it fails**

Run: `mise exec -- go test ./internal/buildinfo/ -v`
Expected: FAIL — `undefined: get`, `undefined: Info`.

- [ ] **Step 7: Write the implementation**

Create `internal/buildinfo/buildinfo.go`:

```go
// Package buildinfo reports the version of the running binary, whether it was
// stamped by GoReleaser or built directly with `go install`.
package buildinfo

import (
	"fmt"
	"runtime/debug"
)

// Set via -ldflags at release time.
var (
	version string
	commit  string
	date    string
)

// Info describes the provenance of the running binary.
type Info struct {
	Version string
	Commit  string
	Date    string
}

func (i Info) String() string {
	return fmt.Sprintf("skillsctl %s (%s, %s)", i.Version, i.Commit, i.Date)
}

// Get returns the build provenance, preferring ldflags-injected values and
// falling back to the module's own build info.
func Get() Info { return get(version, commit, date, debug.ReadBuildInfo) }

func get(version, commit, date string, read func() (*debug.BuildInfo, bool)) Info {
	info := Info{Version: version, Commit: commit, Date: date}

	bi, ok := read()
	if ok {
		if info.Version == "" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			info.Version = bi.Main.Version
		}
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				if info.Commit == "" {
					info.Commit = s.Value
				}
			case "vcs.time":
				if info.Date == "" {
					info.Date = s.Value
				}
			}
		}
	}

	if info.Version == "" {
		info.Version = "devel"
	}
	if info.Commit == "" {
		info.Commit = "unknown"
	}
	if info.Date == "" {
		info.Date = "unknown"
	}
	return info
}
```

- [ ] **Step 8: Run the test to verify it passes**

Run: `mise exec -- go test ./internal/buildinfo/ -v`
Expected: PASS — all five tests.

- [ ] **Step 9: Add the cobra root and version commands**

```bash
mise exec -- go get github.com/spf13/cobra@latest
```

Create `internal/cli/root.go`:

```go
// Package cli builds the skillsctl command tree.
package cli

import (
	"github.com/spf13/cobra"
)

// NewRootCmd builds the command tree. Tests construct a fresh tree per case.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "skillsctl",
		Short:         "Install, update and remove agent skills",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newVersionCmd())
	return root
}

// Execute runs the command tree and returns the process exit code.
func Execute() int {
	root := NewRootCmd()
	if err := root.Execute(); err != nil {
		root.PrintErrf("error: %v\n", err)
		return 1
	}
	return 0
}
```

Create `internal/cli/version.go`:

```go
package cli

import (
	"github.com/richardcase/skillsctl/internal/buildinfo"
	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the skillsctl version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Println(buildinfo.Get())
			return nil
		},
	}
}
```

Create `cmd/skillsctl/main.go`:

```go
package main

import (
	"os"

	"github.com/richardcase/skillsctl/internal/cli"
)

func main() { os.Exit(cli.Execute()) }
```

- [ ] **Step 10: Verify the binary works**

```bash
mise exec -- go mod tidy
mise exec -- go build ./... && mise exec -- go run ./cmd/skillsctl version
```

Expected: prints `skillsctl devel (<sha>, <time>)` — a real sha, because the working tree is a git repo.

- [ ] **Step 11: Commit**

```bash
git add mise.toml go.mod go.sum .gitignore cmd internal docs
git commit -m "feat: scaffold module with version reporting"
```

---

### Task 2: Lint config and CI workflow

**Files:**
- Create: `.golangci.yml`, `.github/workflows/ci.yml`, `Makefile`

**Interfaces:**
- Consumes: the module from Task 1.
- Produces: `make test`, `make lint`, `make build` targets used by later tasks.

- [ ] **Step 1: Write the lint config**

Create `.golangci.yml`:

```yaml
version: "2"

linters:
  enable:
    - errcheck
    - govet
    - ineffassign
    - misspell
    - revive
    - staticcheck
    - unused

formatters:
  enable:
    - gofumpt
    - goimports

issues:
  max-issues-per-linter: 0
  max-same-issues: 0
```

- [ ] **Step 2: Write the Makefile**

Create `Makefile`:

```makefile
# mise's shims put the pinned go, goreleaser and golangci-lint on PATH without
# needing mise activation in the calling shell.
export PATH := $(HOME)/.local/share/mise/shims:$(PATH)

.PHONY: tools test lint fmt build snapshot tidy-check

tools:
	mise install

test:
	go test -race -cover ./...

lint:
	golangci-lint run

fmt:
	golangci-lint fmt

build:
	go build -o skillsctl ./cmd/skillsctl

snapshot:
	goreleaser release --snapshot --clean

tidy-check:
	go mod tidy
	git diff --exit-code -- go.mod go.sum
```

If `make test` reports `go: command not found`, mise's shims are somewhere else on this machine — run `mise where go` and correct the `PATH` line.

- [ ] **Step 3: Run lint locally and fix what it reports**

Run: `make lint`
Expected: PASS with no findings. If `gofumpt` reports formatting, run `golangci-lint fmt` and re-run.

- [ ] **Step 4: Write the CI workflow**

Create `.github/workflows/ci.yml`:

```yaml
name: CI

on:
  pull_request:
  push:
    branches: [main]

permissions:
  contents: read

concurrency:
  group: ci-${{ github.ref }}
  cancel-in-progress: true

jobs:
  test:
    strategy:
      fail-fast: false
      matrix:
        os: [ubuntu-latest, macos-latest]
    runs-on: ${{ matrix.os }}
    steps:
      - uses: actions/checkout@v4
      - uses: jdx/mise-action@v2
      - uses: actions/cache@v4
        with:
          path: |
            ~/.cache/go-build
            ~/Library/Caches/go-build
            ~/go/pkg/mod
          key: ${{ runner.os }}-go-${{ hashFiles('**/go.sum') }}
          restore-keys: ${{ runner.os }}-go-
      - name: Configure git for fixture repositories
        run: |
          git config --global user.email ci@example.com
          git config --global user.name "CI"
          git config --global init.defaultBranch main
      - run: go test -race -cover ./...

  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: jdx/mise-action@v2
      - uses: actions/cache@v4
        with:
          path: |
            ~/.cache/go-build
            ~/go/pkg/mod
          key: ${{ runner.os }}-go-${{ hashFiles('**/go.sum') }}
          restore-keys: ${{ runner.os }}-go-
      - run: golangci-lint run
      - name: go mod tidy is clean
        run: |
          go mod tidy
          git diff --exit-code -- go.mod go.sum

  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - uses: jdx/mise-action@v2
      - run: goreleaser build --snapshot --clean
```

`jdx/mise-action` installs exactly what `mise.toml` pins, so CI and this machine run identical versions of Go, golangci-lint and GoReleaser — that is the whole reason for using mise here rather than `actions/setup-go` plus two tool-specific actions. It puts the tools on `PATH`, so the `run:` steps call them unprefixed. mise-action caches the tools themselves; the separate `actions/cache` step covers the Go module and build caches, which mise knows nothing about.

The `git config --global` step matters: the integration tests in later tasks create fixture repositories with `git commit`, which fails on a runner with no configured identity.

The `build` job will fail until Task 3 adds `.goreleaser.yaml` — that is expected and is fixed by the next task.

- [ ] **Step 5: Add Dependabot**

Create `.github/dependabot.yml`:

```yaml
version: 2
updates:
  - package-ecosystem: gomod
    directory: /
    schedule:
      interval: weekly
  - package-ecosystem: github-actions
    directory: /
    schedule:
      interval: weekly
```

- [ ] **Step 6: Verify locally**

Run: `make test && make lint && make tidy-check`
Expected: all three succeed.

- [ ] **Step 7: Commit**

```bash
git add .golangci.yml Makefile .github/workflows/ci.yml .github/dependabot.yml
git commit -m "ci: add lint config and mise-based test/lint/build workflow"
```

---

### Task 3: GoReleaser config and release workflow

**Files:**
- Create: `.goreleaser.yaml`, `.github/workflows/release.yml`

**Interfaces:**
- Consumes: `internal/buildinfo` package-level `version`, `commit`, `date` vars from Task 1 (the ldflags targets).
- Produces: a release pipeline; nothing consumed by later tasks.

- [ ] **Step 1: Write the GoReleaser config**

Create `.goreleaser.yaml`:

```yaml
version: 2

project_name: skillsctl

before:
  hooks:
    - go mod tidy

builds:
  - id: skillsctl
    main: ./cmd/skillsctl
    binary: skillsctl
    env:
      - CGO_ENABLED=0
    goos: [linux, darwin]
    goarch: [amd64, arm64]
    ldflags:
      - -s -w
      - -X github.com/richardcase/skillsctl/internal/buildinfo.version={{ .Version }}
      - -X github.com/richardcase/skillsctl/internal/buildinfo.commit={{ .Commit }}
      - -X github.com/richardcase/skillsctl/internal/buildinfo.date={{ .Date }}

universal_binaries:
  - id: skillsctl
    replace: true

archives:
  - formats: [tar.gz]
    name_template: >-
      {{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}
    files:
      - README.md
      - LICENSE

nfpms:
  - id: packages
    package_name: skillsctl
    formats: [deb, rpm]
    maintainer: Richard Case <richmcase@gmail.com>
    description: Install, update and remove agent skills.
    homepage: https://github.com/richardcase/skillsctl
    license: AGPL-3.0
    bindir: /usr/bin

brews:
  - repository:
      owner: richardcase
      name: homebrew-tap
      token: "{{ .Env.HOMEBREW_TAP_TOKEN }}"
    homepage: https://github.com/richardcase/skillsctl
    description: Install, update and remove agent skills.
    license: AGPL-3.0
    test: |
      system "#{bin}/skillsctl", "version"

checksum:
  name_template: checksums.txt

changelog:
  use: github
  sort: asc
  groups:
    - title: Features
      regexp: '^feat'
      order: 0
    - title: Fixes
      regexp: '^fix'
      order: 1
    - title: Other
      order: 99
  filters:
    exclude:
      - '^docs:'
      - '^test:'
      - '^chore:'
      - '^ci:'
```

The ldflags paths must match the unexported variable names in `internal/buildinfo/buildinfo.go` exactly — `-X` on a name that does not exist fails silently and the binary reports `devel`. Step 4 checks for this.

The `license:` value is `AGPL-3.0` because that is what the repository's `LICENSE` file actually contains. Do not change it to MIT.

- [ ] **Step 2: Validate the config**

Run: `mise exec -- goreleaser check`
Expected: `1 configuration file(s) validated`.

- [ ] **Step 3: Build a snapshot**

Run: `make snapshot`
Expected: succeeds, and `ls dist/` shows a `darwin_all` archive, two linux archives, and four package files:

```bash
ls dist/*.tar.gz dist/*.deb dist/*.rpm
```

Expected: `skillsctl_<v>_darwin_all.tar.gz`, `skillsctl_<v>_linux_amd64.tar.gz`, `skillsctl_<v>_linux_arm64.tar.gz`, plus `.deb` and `.rpm` for both linux architectures.

- [ ] **Step 4: Verify version injection actually worked**

```bash
./dist/skillsctl_darwin_all/skillsctl version
```

Expected: prints a real snapshot version and commit sha — NOT `skillsctl devel (unknown, unknown)`. If it prints `devel`, the `-X` paths in `ldflags` do not match the variable names in `internal/buildinfo/buildinfo.go`; fix them and repeat from Step 2.

- [ ] **Step 5: Write the release workflow**

Create `.github/workflows/release.yml`:

```yaml
name: Release

on:
  push:
    tags: ['v*.*.*']

permissions:
  contents: write

jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - name: Validate semver tag
        run: |
          tag="${GITHUB_REF_NAME}"
          if ! printf '%s' "$tag" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$'; then
            echo "::error::tag '$tag' is not valid semver"
            exit 1
          fi
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - uses: jdx/mise-action@v2
      - run: goreleaser release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          HOMEBREW_TAP_TOKEN: ${{ secrets.HOMEBREW_TAP_TOKEN }}
```

`fetch-depth: 0` is required — GoReleaser builds the changelog from history and fails on a shallow clone. The GoReleaser version comes from `mise.toml`, so a release is built by the same binary that validated the config on the pull request.

- [ ] **Step 6: Commit**

```bash
rm -rf dist
git add .goreleaser.yaml .github/workflows/release.yml
git commit -m "ci: add goreleaser config and tag-triggered release workflow"
```

- [ ] **Step 7: Record the two manual prerequisites**

These cannot be done from here and must be done before the first tag is pushed. Report them to the user rather than attempting them:

1. Create the `richardcase/homebrew-tap` repository (public, with a README).
2. Add a `HOMEBREW_TAP_TOKEN` repository secret — a PAT with `contents: write` on `richardcase/homebrew-tap`.

Cutting the first tag (`v0.1.0`, in Task 11 Step 12) happens after Task 11, once the binary does something worth releasing. The spec's phase 0 suggested a throwaway `v0.0.1` immediately; the snapshot build in Step 3 already proves the config, so a real tag on an empty binary would add nothing.

---

### Task 4: Source string parsing

**Files:**
- Create: `internal/source/source.go`
- Test: `internal/source/source_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Channel string` with `ChannelGit`, `ChannelPlugin`, `ChannelLocal`
  - `type Source struct { Channel Channel; RepoURL, Subpath, Plugin, Marketplace, Path, Raw string; host, owner, repo string }`
  - `func Parse(raw string) (Source, error)`
  - `func (s Source) Slug() string` — filesystem-safe repo identifier, e.g. `github.com/owner/repo`
  - `func (s Source) DefaultName() string` — fallback skill name

- [ ] **Step 1: Write the failing test**

Create `internal/source/source_test.go`:

```go
package source

import "testing"

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    Source
		wantErr bool
	}{
		{
			name: "github shorthand",
			raw:  "conorbronsdon/avoid-ai-writing",
			want: Source{
				Channel: ChannelGit,
				RepoURL: "https://github.com/conorbronsdon/avoid-ai-writing.git",
			},
		},
		{
			name: "github shorthand with subpath",
			raw:  "vercel-labs/agent-skills/skills/web-research",
			want: Source{
				Channel: ChannelGit,
				RepoURL: "https://github.com/vercel-labs/agent-skills.git",
				Subpath: "skills/web-research",
			},
		},
		{
			name: "https url",
			raw:  "https://github.com/foo/bar",
			want: Source{Channel: ChannelGit, RepoURL: "https://github.com/foo/bar.git"},
		},
		{
			name: "https url with .git suffix",
			raw:  "https://github.com/foo/bar.git",
			want: Source{Channel: ChannelGit, RepoURL: "https://github.com/foo/bar.git"},
		},
		{
			name: "gitlab url",
			raw:  "https://gitlab.com/foo/bar",
			want: Source{Channel: ChannelGit, RepoURL: "https://gitlab.com/foo/bar.git"},
		},
		{
			name: "ssh url keeps its scp form",
			raw:  "git@github.com:foo/bar.git",
			want: Source{Channel: ChannelGit, RepoURL: "git@github.com:foo/bar.git"},
		},
		{
			name: "file url for fixtures",
			raw:  "file:///tmp/fixture/my-skill",
			want: Source{Channel: ChannelGit, RepoURL: "file:///tmp/fixture/my-skill"},
		},
		{
			name: "plugin",
			raw:  "superpowers@claude-plugins-official",
			want: Source{Channel: ChannelPlugin, Plugin: "superpowers", Marketplace: "claude-plugins-official"},
		},
		{
			name: "relative local path",
			raw:  "./my-skill",
			want: Source{Channel: ChannelLocal, Path: "./my-skill"},
		},
		{
			name: "absolute local path",
			raw:  "/srv/skills/my-skill",
			want: Source{Channel: ChannelLocal, Path: "/srv/skills/my-skill"},
		},
		{name: "empty", raw: "", wantErr: true},
		{name: "bare word", raw: "notaskill", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Parse(%q) = %+v, want error", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tc.raw, err)
			}
			if got.Channel != tc.want.Channel {
				t.Errorf("Channel = %q, want %q", got.Channel, tc.want.Channel)
			}
			if got.RepoURL != tc.want.RepoURL {
				t.Errorf("RepoURL = %q, want %q", got.RepoURL, tc.want.RepoURL)
			}
			if got.Subpath != tc.want.Subpath {
				t.Errorf("Subpath = %q, want %q", got.Subpath, tc.want.Subpath)
			}
			if got.Plugin != tc.want.Plugin {
				t.Errorf("Plugin = %q, want %q", got.Plugin, tc.want.Plugin)
			}
			if got.Marketplace != tc.want.Marketplace {
				t.Errorf("Marketplace = %q, want %q", got.Marketplace, tc.want.Marketplace)
			}
			if got.Path != tc.want.Path {
				t.Errorf("Path = %q, want %q", got.Path, tc.want.Path)
			}
		})
	}
}

func TestSlug(t *testing.T) {
	tests := []struct{ raw, want string }{
		{"conorbronsdon/avoid-ai-writing", "github.com/conorbronsdon/avoid-ai-writing"},
		{"vercel-labs/agent-skills/skills/web-research", "github.com/vercel-labs/agent-skills"},
		{"https://gitlab.com/foo/bar", "gitlab.com/foo/bar"},
		{"git@github.com:foo/bar.git", "github.com/foo/bar"},
		{"file:///tmp/fixture/my-skill", "file/tmp/fixture/my-skill"},
	}
	for _, tc := range tests {
		s, err := Parse(tc.raw)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.raw, err)
		}
		if got := s.Slug(); got != tc.want {
			t.Errorf("Parse(%q).Slug() = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestDefaultName(t *testing.T) {
	tests := []struct{ raw, want string }{
		{"conorbronsdon/avoid-ai-writing", "avoid-ai-writing"},
		{"vercel-labs/agent-skills/skills/web-research", "web-research"},
		{"git@github.com:foo/bar.git", "bar"},
		{"./my-skill", "my-skill"},
		{"superpowers@claude-plugins-official", "superpowers"},
	}
	for _, tc := range tests {
		s, err := Parse(tc.raw)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.raw, err)
		}
		if got := s.DefaultName(); got != tc.want {
			t.Errorf("Parse(%q).DefaultName() = %q, want %q", tc.raw, got, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mise exec -- go test ./internal/source/ -v`
Expected: FAIL — `undefined: Parse`.

- [ ] **Step 3: Write the implementation**

Create `internal/source/source.go`:

```go
// Package source turns a user-supplied source string into a structured Source.
package source

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// Channel is the mechanism used to install a skill.
type Channel string

const (
	ChannelGit    Channel = "git"
	ChannelPlugin Channel = "plugin"
	ChannelLocal  Channel = "local"
)

// Source is a parsed, canonicalised install source.
type Source struct {
	Channel Channel

	// Git channel.
	RepoURL string // clone URL, passed to git verbatim
	Subpath string // "" when the repository itself is the skill

	// Plugin channel.
	Plugin      string
	Marketplace string

	// Local channel.
	Path string

	Raw string

	host  string
	owner string
	repo  string
}

var (
	shorthandRe = regexp.MustCompile(`^([A-Za-z0-9._-]+)/([A-Za-z0-9._-]+)(?:/(.+))?$`)
	scpRe       = regexp.MustCompile(`^[A-Za-z0-9._-]+@([A-Za-z0-9._-]+):(.+?)(?:\.git)?$`)
	pluginRe    = regexp.MustCompile(`^([A-Za-z0-9._-]+)@([A-Za-z0-9._-]+)$`)
)

// Parse canonicalises raw into a Source, inferring the channel from its shape.
func Parse(raw string) (Source, error) {
	s := Source{Raw: raw}

	switch {
	case raw == "":
		return s, fmt.Errorf("empty source")

	case raw == ".", strings.HasPrefix(raw, "./"), strings.HasPrefix(raw, "../"), strings.HasPrefix(raw, "/"), strings.HasPrefix(raw, "~/"):
		s.Channel = ChannelLocal
		s.Path = raw
		return s, nil

	case strings.Contains(raw, "://"):
		return parseURL(raw)

	case scpRe.MatchString(raw):
		m := scpRe.FindStringSubmatch(raw)
		s.Channel = ChannelGit
		s.RepoURL = raw
		s.host = m[1]
		s.owner, s.repo = splitOwnerRepo(m[2])
		return s, nil

	case pluginRe.MatchString(raw):
		m := pluginRe.FindStringSubmatch(raw)
		s.Channel = ChannelPlugin
		s.Plugin, s.Marketplace = m[1], m[2]
		return s, nil

	case shorthandRe.MatchString(raw):
		m := shorthandRe.FindStringSubmatch(raw)
		s.Channel = ChannelGit
		s.host, s.owner, s.repo = "github.com", m[1], strings.TrimSuffix(m[2], ".git")
		s.RepoURL = fmt.Sprintf("https://github.com/%s/%s.git", s.owner, s.repo)
		s.Subpath = m[3]
		return s, nil
	}

	return s, fmt.Errorf("unrecognised source %q: expected owner/repo, a git URL, plugin@marketplace, or a path", raw)
}

func parseURL(raw string) (Source, error) {
	s := Source{Raw: raw, Channel: ChannelGit}

	u, err := url.Parse(raw)
	if err != nil {
		return s, fmt.Errorf("parse %q: %w", raw, err)
	}

	if u.Scheme == "file" {
		// Fixture and vendored repositories: the whole path identifies the repo.
		s.RepoURL = raw
		s.host = "file"
		s.repo = path.Base(strings.TrimSuffix(u.Path, "/"))
		return s, nil
	}

	trimmed := strings.Trim(strings.TrimSuffix(u.Path, ".git"), "/")
	parts := strings.Split(trimmed, "/")
	if len(parts) < 2 {
		return s, fmt.Errorf("git URL %q has no owner/repo path", raw)
	}

	s.host = u.Host
	s.owner, s.repo = parts[0], parts[1]
	s.Subpath = strings.Join(parts[2:], "/")
	s.RepoURL = fmt.Sprintf("%s://%s/%s/%s.git", u.Scheme, u.Host, s.owner, s.repo)
	return s, nil
}

func splitOwnerRepo(p string) (owner, repo string) {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) == 1 {
		return "", parts[0]
	}
	return parts[0], parts[len(parts)-1]
}

// Slug is a stable, filesystem-safe identifier for the repository, used to lay
// out the store. It deliberately excludes the subpath: every skill taken from
// one repository shares one mirror and one revision directory.
func (s Source) Slug() string {
	switch s.Channel {
	case ChannelGit:
		if s.host == "file" {
			u, err := url.Parse(s.RepoURL)
			if err != nil {
				return path.Join("file", sanitise(s.RepoURL))
			}
			return path.Join("file", strings.Trim(u.Path, "/"))
		}
		return path.Join(s.host, s.owner, s.repo)
	case ChannelPlugin:
		return path.Join("plugin", s.Marketplace, s.Plugin)
	default:
		return path.Join("local", sanitise(s.Path))
	}
}

var unsafeRe = regexp.MustCompile(`[^A-Za-z0-9._/-]`)

func sanitise(s string) string {
	return strings.Trim(unsafeRe.ReplaceAllString(s, "-"), "/")
}

// DefaultName is the skill name to use when SKILL.md declares none.
func (s Source) DefaultName() string {
	switch s.Channel {
	case ChannelPlugin:
		return s.Plugin
	case ChannelLocal:
		return path.Base(strings.TrimSuffix(s.Path, "/"))
	default:
		if s.Subpath != "" {
			return path.Base(s.Subpath)
		}
		return s.repo
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mise exec -- go test ./internal/source/ -v`
Expected: PASS — every subtest.

- [ ] **Step 5: Commit**

```bash
git add internal/source
git commit -m "feat: parse install source strings into channels"
```

---

### Task 5: Receipts database

**Files:**
- Create: `internal/state/state.go`
- Test: `internal/state/state_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Link struct { Target, Path string }`
  - `type Receipt struct { Name, Channel, Source, Slug, Subpath, Ref, Resolved string; Pinned bool; RevPath, ContentHash string; Links []Link; InstalledAt, UpdatedAt time.Time }`
  - `type DB struct { Version int; Receipts map[string]*Receipt }` with `func (d *DB) List() []*Receipt` (sorted by name)
  - `func Open(path string) (*Handle, error)`, `func (h *Handle) Commit() error`, `func (h *Handle) Close() error`, and field `h.DB *DB`
  - `const SchemaVersion = 1`

- [ ] **Step 1: Write the failing test**

Create `internal/state/state_test.go`:

```go
package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func statePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "nested", "state.json")
}

func TestOpenMissingFileGivesEmptyDB(t *testing.T) {
	h, err := Open(statePath(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer h.Close()

	if h.DB.Version != SchemaVersion {
		t.Errorf("Version = %d, want %d", h.DB.Version, SchemaVersion)
	}
	if len(h.DB.Receipts) != 0 {
		t.Errorf("Receipts = %v, want empty", h.DB.Receipts)
	}
}

func TestCommitThenReopenRoundTrips(t *testing.T) {
	p := statePath(t)
	now := time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC)

	h, err := Open(p)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	h.DB.Receipts["avoid-ai-writing"] = &Receipt{
		Name:        "avoid-ai-writing",
		Channel:     "git",
		Source:      "https://github.com/conorbronsdon/avoid-ai-writing.git",
		Slug:        "github.com/conorbronsdon/avoid-ai-writing",
		Ref:         "main",
		Resolved:    "a1b2c3d",
		RevPath:     "/store/rev/x/a1b2c3d",
		ContentHash: "deadbeef",
		Links:       []Link{{Target: "claude", Path: "/home/x/.claude/skills/avoid-ai-writing"}},
		InstalledAt: now,
		UpdatedAt:   now,
	}
	if err := h.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	h2, err := Open(p)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer h2.Close()

	got, ok := h2.DB.Receipts["avoid-ai-writing"]
	if !ok {
		t.Fatal("receipt did not survive the round trip")
	}
	if got.Resolved != "a1b2c3d" {
		t.Errorf("Resolved = %q, want a1b2c3d", got.Resolved)
	}
	if len(got.Links) != 1 || got.Links[0].Target != "claude" {
		t.Errorf("Links = %+v, want one claude link", got.Links)
	}
	if !got.InstalledAt.Equal(now) {
		t.Errorf("InstalledAt = %v, want %v", got.InstalledAt, now)
	}
}

func TestCloseWithoutCommitDiscardsChanges(t *testing.T) {
	p := statePath(t)

	h, _ := Open(p)
	h.DB.Receipts["ghost"] = &Receipt{Name: "ghost"}
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	h2, err := Open(p)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer h2.Close()
	if _, ok := h2.DB.Receipts["ghost"]; ok {
		t.Error("uncommitted change was persisted")
	}
}

func TestOpenRejectsNewerSchema(t *testing.T) {
	p := statePath(t)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	blob, _ := json.Marshal(map[string]any{"version": SchemaVersion + 1, "receipts": map[string]any{}})
	if err := os.WriteFile(p, blob, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Open(p); err == nil {
		t.Fatal("Open accepted a newer schema version; want an error telling the user to upgrade")
	}
}

func TestListIsSortedByName(t *testing.T) {
	db := &DB{Version: SchemaVersion, Receipts: map[string]*Receipt{
		"zulu":  {Name: "zulu"},
		"alpha": {Name: "alpha"},
		"mike":  {Name: "mike"},
	}}
	got := db.List()
	want := []string{"alpha", "mike", "zulu"}
	for i, r := range got {
		if r.Name != want[i] {
			t.Fatalf("List()[%d] = %q, want %q", i, r.Name, want[i])
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mise exec -- go test ./internal/state/ -v`
Expected: FAIL — `undefined: Open`, `undefined: DB`.

- [ ] **Step 3: Write the implementation**

```bash
mise exec -- go get github.com/gofrs/flock@latest
```

Create `internal/state/state.go`:

```go
// Package state persists one receipt per installed skill. A receipt records
// exactly what an install created, so removal never has to infer anything.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/gofrs/flock"
)

// SchemaVersion is the on-disk format version. Bump it only for a breaking
// change, and add a migration when you do.
const SchemaVersion = 1

// Link is a symlink an install created in one agent's skills directory.
type Link struct {
	Target string `json:"target"`
	Path   string `json:"path"`
}

// Receipt records how a skill was installed.
type Receipt struct {
	Name        string    `json:"name"`
	Channel     string    `json:"channel"`
	Source      string    `json:"source"`
	Slug        string    `json:"slug,omitempty"`
	Subpath     string    `json:"subpath,omitempty"`
	Ref         string    `json:"ref,omitempty"`
	Resolved    string    `json:"resolved"`
	Pinned      bool      `json:"pinned,omitempty"`
	RevPath     string    `json:"revPath"`
	ContentHash string    `json:"contentHash,omitempty"`
	Links       []Link    `json:"links"`
	InstalledAt time.Time `json:"installedAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// DB is the in-memory receipt set.
type DB struct {
	Version  int                 `json:"version"`
	Receipts map[string]*Receipt `json:"receipts"`
}

// List returns every receipt, sorted by name.
func (d *DB) List() []*Receipt {
	out := make([]*Receipt, 0, len(d.Receipts))
	for _, r := range d.Receipts {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Handle is an exclusive session over the receipt DB. The lock is held from
// Open until Close so that read-modify-write is atomic across processes.
type Handle struct {
	DB *DB

	path string
	lock *flock.Flock
}

// Open acquires the lock and loads the DB, treating a missing file as empty.
func Open(path string) (*Handle, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}

	lock := flock.New(path + ".lock")
	if err := lock.Lock(); err != nil {
		return nil, fmt.Errorf("lock state: %w", err)
	}

	h := &Handle{path: path, lock: lock}

	blob, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		h.DB = &DB{Version: SchemaVersion, Receipts: map[string]*Receipt{}}
		return h, nil
	case err != nil:
		_ = lock.Unlock()
		return nil, fmt.Errorf("read state: %w", err)
	}

	var db DB
	if err := json.Unmarshal(blob, &db); err != nil {
		_ = lock.Unlock()
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if db.Version > SchemaVersion {
		_ = lock.Unlock()
		return nil, fmt.Errorf("%s was written by a newer skillsctl (schema %d, this build understands %d): upgrade skillsctl", path, db.Version, SchemaVersion)
	}
	if db.Receipts == nil {
		db.Receipts = map[string]*Receipt{}
	}
	db.Version = SchemaVersion

	h.DB = &db
	return h, nil
}

// Commit writes the DB atomically. Changes are lost unless Commit is called.
func (h *Handle) Commit() error {
	blob, err := json.MarshalIndent(h.DB, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	blob = append(blob, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(h.path), ".state-*.json")
	if err != nil {
		return fmt.Errorf("create temp state: %w", err)
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(blob); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp state: %w", err)
	}
	if err := os.Rename(tmp.Name(), h.path); err != nil {
		return fmt.Errorf("replace state: %w", err)
	}
	return nil
}

// Close releases the lock. Safe to call more than once.
func (h *Handle) Close() error {
	if h.lock == nil {
		return nil
	}
	err := h.lock.Unlock()
	h.lock = nil
	return err
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mise exec -- go test ./internal/state/ -v`
Expected: PASS — all five tests.

- [ ] **Step 5: Commit**

```bash
git add internal/state go.mod go.sum
git commit -m "feat: add locked, atomically-written receipts database"
```

---

### Task 6: Target configuration and symlinking

**Files:**
- Create: `internal/target/target.go`, `internal/target/link.go`
- Test: `internal/target/target_test.go`, `internal/target/link_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Target struct { Name, Dir, ProjectDir string; Plugins bool }`
  - `type Config struct { Targets []Target }` with `func (c Config) Present() []Target` and `func (c Config) Select(names []string) ([]Target, error)`
  - `func Default() Config`, `func Load(path string) (Config, error)`, `func ConfigPath() (string, error)`
  - `func Link(linkPath, revPath string) error`, `func Unlink(linkPath string) error`

- [ ] **Step 1: Write the failing config test**

Create `internal/target/target_test.go`:

```go
package target

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "absent.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Targets) != len(Default().Targets) {
		t.Fatalf("got %d targets, want the %d defaults", len(got.Targets), len(Default().Targets))
	}
	if got.Targets[0].Name != "claude" {
		t.Errorf("first default target = %q, want claude", got.Targets[0].Name)
	}
	if !got.Targets[0].Plugins {
		t.Error("claude target must have Plugins enabled")
	}
}

func TestLoadExpandsTilde(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	body := `
[[target]]
name = "claude"
dir = "~/.claude/skills"
plugins = true
`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".claude", "skills")
	if got.Targets[0].Dir != want {
		t.Errorf("Dir = %q, want %q", got.Targets[0].Dir, want)
	}
}

func TestPresentUsesParentDirectory(t *testing.T) {
	root := t.TempDir()
	// ~/.claude exists but ~/.claude/skills does not: still present, because
	// the skills directory is created on demand at first install.
	if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := Config{Targets: []Target{
		{Name: "claude", Dir: filepath.Join(root, ".claude", "skills")},
		{Name: "codex", Dir: filepath.Join(root, ".codex", "skills")},
	}}

	got := cfg.Present()
	if len(got) != 1 || got[0].Name != "claude" {
		t.Fatalf("Present() = %+v, want only claude", got)
	}
}

func TestSelect(t *testing.T) {
	cfg := Config{Targets: []Target{{Name: "claude"}, {Name: "codex"}, {Name: "gemini"}}}

	got, err := cfg.Select([]string{"codex", "claude"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Select returned %d targets, want 2", len(got))
	}

	if _, err := cfg.Select([]string{"emacs"}); err == nil {
		t.Error("Select accepted an unknown agent name; want an error")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mise exec -- go test ./internal/target/ -v`
Expected: FAIL — `undefined: Load`.

- [ ] **Step 3: Write the config implementation**

```bash
mise exec -- go get github.com/pelletier/go-toml/v2@latest
```

Create `internal/target/target.go`:

```go
// Package target describes the agents skillsctl installs into, and manages the
// symlinks in their skills directories.
package target

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Target is one agent's skills directory.
type Target struct {
	Name       string `toml:"name"`
	Dir        string `toml:"dir"`
	ProjectDir string `toml:"project_dir"`
	Plugins    bool   `toml:"plugins"`
}

// Config is the set of agents skillsctl knows about.
type Config struct {
	Targets []Target `toml:"target"`
}

// Default is the built-in agent table, used when no config file exists.
func Default() Config {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	return Config{Targets: []Target{
		{Name: "claude", Dir: filepath.Join(home, ".claude", "skills"), ProjectDir: ".claude/skills", Plugins: true},
		{Name: "codex", Dir: filepath.Join(home, ".codex", "skills"), ProjectDir: ".codex/skills"},
		{Name: "gemini", Dir: filepath.Join(home, ".gemini", "skills"), ProjectDir: ".gemini/skills"},
	}}
}

// ConfigPath is where the agent table lives, honouring SKILLSCTL_CONFIG and
// XDG_CONFIG_HOME before falling back to ~/.config.
func ConfigPath() (string, error) {
	if p := os.Getenv("SKILLSCTL_CONFIG"); p != "" {
		return p, nil
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "skillsctl", "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, ".config", "skillsctl", "config.toml"), nil
}

// Load reads the agent table, returning Default when the file does not exist.
func Load(path string) (Config, error) {
	blob, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}

	var cfg Config
	if err := toml.Unmarshal(blob, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(cfg.Targets) == 0 {
		return Config{}, fmt.Errorf("%s defines no [[target]] entries", path)
	}
	for i := range cfg.Targets {
		cfg.Targets[i].Dir = expand(cfg.Targets[i].Dir)
	}
	return cfg, nil
}

func expand(p string) string {
	if !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, p[2:])
}

// Present returns the targets whose agent directory exists. The skills
// subdirectory itself may be absent; it is created at first install.
func (c Config) Present() []Target {
	var out []Target
	for _, t := range c.Targets {
		if fi, err := os.Stat(filepath.Dir(t.Dir)); err == nil && fi.IsDir() {
			out = append(out, t)
		}
	}
	return out
}

// Select returns the named targets, in the order given.
func (c Config) Select(names []string) ([]Target, error) {
	byName := make(map[string]Target, len(c.Targets))
	known := make([]string, 0, len(c.Targets))
	for _, t := range c.Targets {
		byName[t.Name] = t
		known = append(known, t.Name)
	}

	out := make([]Target, 0, len(names))
	for _, n := range names {
		t, ok := byName[n]
		if !ok {
			return nil, fmt.Errorf("unknown agent %q (known: %s)", n, strings.Join(known, ", "))
		}
		out = append(out, t)
	}
	return out, nil
}
```

- [ ] **Step 4: Run the config tests to verify they pass**

Run: `mise exec -- go test ./internal/target/ -run 'TestLoad|TestPresent|TestSelect' -v`
Expected: PASS.

- [ ] **Step 5: Write the failing link test**

Create `internal/target/link_test.go`:

```go
package target

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinkCreatesParentDirectories(t *testing.T) {
	root := t.TempDir()
	rev := filepath.Join(root, "rev")
	if err := os.MkdirAll(rev, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "agent", "skills", "my-skill")

	if err := Link(link, rev); err != nil {
		t.Fatalf("Link: %v", err)
	}
	got, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("Readlink: %v", err)
	}
	if got != rev {
		t.Errorf("symlink points at %q, want %q", got, rev)
	}
}

func TestLinkIsIdempotent(t *testing.T) {
	root := t.TempDir()
	rev := filepath.Join(root, "rev")
	os.MkdirAll(rev, 0o755)
	link := filepath.Join(root, "skills", "my-skill")

	if err := Link(link, rev); err != nil {
		t.Fatalf("first Link: %v", err)
	}
	if err := Link(link, rev); err != nil {
		t.Fatalf("second Link should be a no-op, got: %v", err)
	}
}

func TestLinkRefusesToClobber(t *testing.T) {
	root := t.TempDir()
	rev := filepath.Join(root, "rev")
	os.MkdirAll(rev, 0o755)
	link := filepath.Join(root, "skills", "my-skill")
	os.MkdirAll(link, 0o755)

	if err := Link(link, rev); err == nil {
		t.Fatal("Link overwrote an existing real directory; want an error")
	}
	if fi, err := os.Lstat(link); err != nil || !fi.IsDir() {
		t.Error("the existing directory must be left untouched")
	}
}

func TestUnlinkRemovesOnlySymlinks(t *testing.T) {
	root := t.TempDir()
	rev := filepath.Join(root, "rev")
	os.MkdirAll(rev, 0o755)

	link := filepath.Join(root, "skills", "linked")
	if err := Link(link, rev); err != nil {
		t.Fatal(err)
	}
	if err := Unlink(link); err != nil {
		t.Fatalf("Unlink: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Error("symlink was not removed")
	}

	real := filepath.Join(root, "skills", "real")
	os.MkdirAll(real, 0o755)
	if err := Unlink(real); err == nil {
		t.Fatal("Unlink removed a real directory; want an error")
	}
	if _, err := os.Stat(real); err != nil {
		t.Error("the real directory must survive")
	}
}

func TestUnlinkMissingPathSucceeds(t *testing.T) {
	if err := Unlink(filepath.Join(t.TempDir(), "nothing-here")); err != nil {
		t.Errorf("Unlink of a missing path should be a no-op, got: %v", err)
	}
}
```

- [ ] **Step 6: Run the link test to verify it fails**

Run: `mise exec -- go test ./internal/target/ -run TestLink -v`
Expected: FAIL — `undefined: Link`.

- [ ] **Step 7: Write the link implementation**

Create `internal/target/link.go`:

```go
package target

import (
	"fmt"
	"os"
	"path/filepath"
)

// Link points linkPath at revPath, creating parent directories as needed.
// It is a no-op when the link already points where it should, and it refuses
// to replace anything that is not such a symlink.
func Link(linkPath, revPath string) error {
	if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
		return fmt.Errorf("create skills directory: %w", err)
	}

	fi, err := os.Lstat(linkPath)
	switch {
	case err == nil && fi.Mode()&os.ModeSymlink != 0:
		existing, rerr := os.Readlink(linkPath)
		if rerr == nil && existing == revPath {
			return nil
		}
		return fmt.Errorf("%s is already a symlink to %s: remove it first", linkPath, existing)
	case err == nil:
		return fmt.Errorf("%s already exists and is not a skillsctl symlink: remove it first", linkPath)
	case !os.IsNotExist(err):
		return fmt.Errorf("inspect %s: %w", linkPath, err)
	}

	if err := os.Symlink(revPath, linkPath); err != nil {
		return fmt.Errorf("link %s: %w", linkPath, err)
	}
	return nil
}

// Unlink removes linkPath when it is a symlink. A missing path succeeds; a
// real file or directory is an error, never a deletion.
func Unlink(linkPath string) error {
	fi, err := os.Lstat(linkPath)
	switch {
	case os.IsNotExist(err):
		return nil
	case err != nil:
		return fmt.Errorf("inspect %s: %w", linkPath, err)
	case fi.Mode()&os.ModeSymlink == 0:
		return fmt.Errorf("refusing to remove %s: it is not a symlink", linkPath)
	}

	if err := os.Remove(linkPath); err != nil {
		return fmt.Errorf("remove %s: %w", linkPath, err)
	}
	return nil
}
```

- [ ] **Step 8: Run all target tests**

Run: `mise exec -- go test ./internal/target/ -v`
Expected: PASS — all nine tests.

- [ ] **Step 9: Commit**

```bash
git add internal/target go.mod go.sum
git commit -m "feat: add agent target config and safe symlinking"
```

---

### Task 7: Plan and executor

**Files:**
- Create: `internal/plan/plan.go`, `internal/plan/executor.go`
- Test: `internal/plan/plan_test.go`, `internal/plan/executor_test.go`

**Interfaces:**
- Consumes: `state.DB`, `state.Receipt`, `target.Link`, `target.Unlink` (Tasks 5 and 6).
- Produces:
  - `type Op interface { Describe() string }`
  - ops `Link{Target, LinkPath, RevPath string}`, `Unlink{Target, LinkPath string}`, `Record{Receipt state.Receipt}`, `Forget{Name string}`, `Exec{Argv []string}`
  - `type Plan struct { Ops []Op }` with `Add(...Op)`, `Describe() []string`, `IsEmpty() bool`
  - `type Executor struct { DB *state.DB; Out io.Writer; Run func(context.Context, []string) error }` with `Apply(context.Context, Plan) error`

- [ ] **Step 1: Write the failing plan test**

Create `internal/plan/plan_test.go`:

```go
package plan

import (
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/state"
)

func TestDescribeRendersEveryOp(t *testing.T) {
	var p Plan
	p.Add(
		Link{Target: "claude", LinkPath: "/h/.claude/skills/foo", RevPath: "/s/rev/x/abc"},
		Unlink{Target: "codex", LinkPath: "/h/.codex/skills/foo"},
		Record{Receipt: state.Receipt{Name: "foo", Resolved: "abc1234"}},
		Forget{Name: "bar"},
		Exec{Argv: []string{"claude", "plugin", "install", "x@y"}},
	)

	got := p.Describe()
	if len(got) != 5 {
		t.Fatalf("Describe() produced %d lines, want 5", len(got))
	}

	wantFragments := []string{
		"/h/.claude/skills/foo",
		"/h/.codex/skills/foo",
		"foo",
		"bar",
		"claude plugin install x@y",
	}
	for i, frag := range wantFragments {
		if !strings.Contains(got[i], frag) {
			t.Errorf("Describe()[%d] = %q, want it to mention %q", i, got[i], frag)
		}
	}
}

func TestIsEmpty(t *testing.T) {
	var p Plan
	if !p.IsEmpty() {
		t.Error("a plan with no ops must be empty")
	}
	p.Add(Forget{Name: "x"})
	if p.IsEmpty() {
		t.Error("a plan with an op must not be empty")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mise exec -- go test ./internal/plan/ -v`
Expected: FAIL — `undefined: Plan`.

- [ ] **Step 3: Write the plan implementation**

Create `internal/plan/plan.go`:

```go
// Package plan models a command's user-visible mutations as inspectable data,
// so that --dry-run is exact and tests can assert over op sequences.
package plan

import (
	"fmt"
	"strings"

	"github.com/richardcase/skillsctl/internal/state"
)

// Op is a single user-visible mutation.
type Op interface {
	Describe() string
}

// Link creates a symlink in an agent's skills directory.
type Link struct {
	Target   string
	LinkPath string
	RevPath  string
}

func (o Link) Describe() string {
	return fmt.Sprintf("link    %s -> %s [%s]", o.LinkPath, o.RevPath, o.Target)
}

// Unlink removes a symlink skillsctl created.
type Unlink struct {
	Target   string
	LinkPath string
}

func (o Unlink) Describe() string {
	return fmt.Sprintf("unlink  %s [%s]", o.LinkPath, o.Target)
}

// Record writes a receipt.
type Record struct {
	Receipt state.Receipt
}

func (o Record) Describe() string {
	return fmt.Sprintf("record  %s @ %s", o.Receipt.Name, short(o.Receipt.Resolved))
}

// Forget deletes a receipt.
type Forget struct {
	Name string
}

func (o Forget) Describe() string { return fmt.Sprintf("forget  %s", o.Name) }

// Exec shells out, used by the plugin channel.
type Exec struct {
	Argv []string
}

func (o Exec) Describe() string { return "exec    " + strings.Join(o.Argv, " ") }

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// Plan is an ordered list of mutations.
type Plan struct {
	Ops []Op
}

// Add appends ops to the plan.
func (p *Plan) Add(ops ...Op) { p.Ops = append(p.Ops, ops...) }

// IsEmpty reports whether the plan would change nothing.
func (p Plan) IsEmpty() bool { return len(p.Ops) == 0 }

// Describe renders one line per op, for --dry-run.
func (p Plan) Describe() []string {
	out := make([]string, 0, len(p.Ops))
	for _, op := range p.Ops {
		out = append(out, op.Describe())
	}
	return out
}
```

- [ ] **Step 4: Run the plan tests to verify they pass**

Run: `mise exec -- go test ./internal/plan/ -v`
Expected: PASS — both tests.

- [ ] **Step 5: Write the failing executor test**

Create `internal/plan/executor_test.go`:

```go
package plan

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/richardcase/skillsctl/internal/state"
)

func newExecutor() *Executor {
	return &Executor{
		DB:  &state.DB{Version: state.SchemaVersion, Receipts: map[string]*state.Receipt{}},
		Out: io.Discard,
	}
}

func TestApplyLinksAndRecords(t *testing.T) {
	root := t.TempDir()
	rev := filepath.Join(root, "rev")
	if err := os.MkdirAll(rev, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "agent", "skills", "foo")

	e := newExecutor()
	var p Plan
	p.Add(
		Link{Target: "claude", LinkPath: link, RevPath: rev},
		Record{Receipt: state.Receipt{Name: "foo", Resolved: "abc"}},
	)

	if err := e.Apply(context.Background(), p); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Readlink(link); err != nil {
		t.Errorf("symlink was not created: %v", err)
	}
	if _, ok := e.DB.Receipts["foo"]; !ok {
		t.Error("receipt was not recorded")
	}
}

func TestApplyRollsBackLinksOnFailure(t *testing.T) {
	root := t.TempDir()
	rev := filepath.Join(root, "rev")
	os.MkdirAll(rev, 0o755)

	good := filepath.Join(root, "a", "skills", "foo")
	// A real directory at the second link path makes that Link op fail.
	bad := filepath.Join(root, "b", "skills", "foo")
	os.MkdirAll(bad, 0o755)

	e := newExecutor()
	var p Plan
	p.Add(
		Link{Target: "claude", LinkPath: good, RevPath: rev},
		Link{Target: "codex", LinkPath: bad, RevPath: rev},
		Record{Receipt: state.Receipt{Name: "foo"}},
	)

	if err := e.Apply(context.Background(), p); err == nil {
		t.Fatal("Apply succeeded despite a failing Link op")
	}
	if _, err := os.Lstat(good); !os.IsNotExist(err) {
		t.Error("the first symlink must be rolled back when a later op fails")
	}
	if _, ok := e.DB.Receipts["foo"]; ok {
		t.Error("no receipt should be recorded when apply fails")
	}
}

func TestApplyForgetAndUnlink(t *testing.T) {
	root := t.TempDir()
	rev := filepath.Join(root, "rev")
	os.MkdirAll(rev, 0o755)
	link := filepath.Join(root, "skills", "foo")

	e := newExecutor()
	e.DB.Receipts["foo"] = &state.Receipt{Name: "foo"}

	var setup Plan
	setup.Add(Link{Target: "claude", LinkPath: link, RevPath: rev})
	if err := e.Apply(context.Background(), setup); err != nil {
		t.Fatal(err)
	}

	var p Plan
	p.Add(Unlink{Target: "claude", LinkPath: link}, Forget{Name: "foo"})
	if err := e.Apply(context.Background(), p); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Error("symlink was not removed")
	}
	if _, ok := e.DB.Receipts["foo"]; ok {
		t.Error("receipt was not forgotten")
	}
}

func TestApplyExecUsesInjectedRunner(t *testing.T) {
	var got []string
	e := newExecutor()
	e.Run = func(_ context.Context, argv []string) error {
		got = argv
		return nil
	}

	var p Plan
	p.Add(Exec{Argv: []string{"claude", "plugin", "install", "x@y"}})
	if err := e.Apply(context.Background(), p); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(got) != 4 || got[0] != "claude" {
		t.Errorf("runner received %v, want the exec argv", got)
	}
}
```

- [ ] **Step 6: Run the executor test to verify it fails**

Run: `mise exec -- go test ./internal/plan/ -run TestApply -v`
Expected: FAIL — `undefined: Executor`.

- [ ] **Step 7: Write the executor implementation**

Create `internal/plan/executor.go`:

```go
package plan

import (
	"context"
	"fmt"
	"io"
	"os/exec"

	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/target"
)

// Executor applies a plan. Receipt changes land in DB but are not persisted:
// the caller commits the state handle only after Apply returns nil, so a
// failed apply leaves the on-disk receipts untouched.
type Executor struct {
	DB  *state.DB
	Out io.Writer

	// Run executes an Exec op. Defaults to os/exec; tests inject a fake.
	Run func(ctx context.Context, argv []string) error
}

// Apply runs every op in order. If one fails, symlinks created by this apply
// are removed before the error is returned.
func (e *Executor) Apply(ctx context.Context, p Plan) error {
	var linked []string

	rollback := func() {
		for i := len(linked) - 1; i >= 0; i-- {
			if err := target.Unlink(linked[i]); err != nil {
				fmt.Fprintf(e.Out, "warning: could not roll back %s: %v\n", linked[i], err)
			}
		}
	}

	for _, op := range p.Ops {
		var err error
		switch o := op.(type) {
		case Link:
			if err = target.Link(o.LinkPath, o.RevPath); err == nil {
				linked = append(linked, o.LinkPath)
			}
		case Unlink:
			err = target.Unlink(o.LinkPath)
		case Record:
			r := o.Receipt
			e.DB.Receipts[r.Name] = &r
		case Forget:
			delete(e.DB.Receipts, o.Name)
		case Exec:
			err = e.run(ctx, o.Argv)
		default:
			err = fmt.Errorf("unknown op %T", op)
		}

		if err != nil {
			rollback()
			return fmt.Errorf("%s: %w", op.Describe(), err)
		}
	}
	return nil
}

func (e *Executor) run(ctx context.Context, argv []string) error {
	if e.Run != nil {
		return e.Run(ctx, argv)
	}
	if len(argv) == 0 {
		return fmt.Errorf("empty command")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout = e.Out
	cmd.Stderr = e.Out
	return cmd.Run()
}
```

Note the rollback undoes only the links this apply created — it deliberately does not restore links removed by `Unlink` ops, because a remove that fails partway leaves the uncommitted receipt intact and `skillsctl doctor` (phase 4) is the repair path.

- [ ] **Step 8: Run all plan tests**

Run: `mise exec -- go test ./internal/plan/ -v`
Expected: PASS — all six tests.

- [ ] **Step 9: Commit**

```bash
git add internal/plan
git commit -m "feat: add plan/apply executor with link rollback"
```

---

### Task 8: Git operations

**Files:**
- Create: `internal/gitx/gitx.go`, `internal/gitx/untar.go`, `internal/testrepo/testrepo.go`
- Test: `internal/gitx/gitx_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Git interface { Resolve(ctx context.Context, repoURL, ref string) (string, error); Mirror(ctx context.Context, repoURL, mirrorPath string) error; Extract(ctx context.Context, mirrorPath, sha, dest string) error }`
  - `type CLI struct { Bin string }`, `func New() *CLI`
  - `func testrepo.New(t *testing.T, files map[string]string) (url, sha string)`
  - `func testrepo.Commit(t *testing.T, dir string, files map[string]string) string`

- [ ] **Step 1: Write the fixture repository helper**

Create `internal/testrepo/testrepo.go`:

```go
// Package testrepo builds throwaway git repositories for tests, so no test
// ever needs the network.
package testrepo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// Write creates files (paths relative to dir, "/" separated) under dir.
func Write(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// New creates a repository containing files and returns its file:// URL and
// the sha of the initial commit.
func New(t *testing.T, files map[string]string) (url, sha string) {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "init", "-q", "-b", "main")
	sha = Commit(t, dir, files)
	return "file://" + dir, sha
}

// Commit writes files into an existing repository and commits them, returning
// the new sha. dir is the working tree path, not the file:// URL.
func Commit(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	Write(t, dir, files)
	run(t, dir, "add", "-A")
	run(t, dir, "commit", "-q", "-m", "update")
	return run(t, dir, "rev-parse", "HEAD")
}

// Dir converts a file:// URL returned by New back into a filesystem path.
func Dir(url string) string { return strings.TrimPrefix(url, "file://") }
```

- [ ] **Step 2: Write the failing test**

Create `internal/gitx/gitx_test.go`:

```go
package gitx

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/richardcase/skillsctl/internal/testrepo"
)

func TestResolveHEAD(t *testing.T) {
	url, sha := testrepo.New(t, map[string]string{"SKILL.md": "---\nname: demo\n---\n"})

	got, err := New().Resolve(context.Background(), url, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != sha {
		t.Errorf("Resolve() = %q, want %q", got, sha)
	}
}

func TestResolveBranch(t *testing.T) {
	url, sha := testrepo.New(t, map[string]string{"SKILL.md": "x"})

	got, err := New().Resolve(context.Background(), url, "main")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != sha {
		t.Errorf("Resolve(main) = %q, want %q", got, sha)
	}
}

func TestResolvePassesThroughFullSha(t *testing.T) {
	sha := "0123456789abcdef0123456789abcdef01234567"

	got, err := New().Resolve(context.Background(), "file:///nonexistent", sha)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != sha {
		t.Errorf("Resolve(sha) = %q, want the sha unchanged", got)
	}
}

func TestResolveUnknownRefErrors(t *testing.T) {
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": "x"})

	if _, err := New().Resolve(context.Background(), url, "no-such-branch"); err == nil {
		t.Fatal("Resolve accepted an unknown ref; want an error")
	}
}

func TestMirrorThenExtract(t *testing.T) {
	url, sha := testrepo.New(t, map[string]string{
		"SKILL.md":               "---\nname: demo\n---\nbody\n",
		"skills/nested/SKILL.md": "---\nname: nested\n---\n",
	})

	ctx := context.Background()
	root := t.TempDir()
	mirror := filepath.Join(root, "demo.git")
	g := New()

	if err := g.Mirror(ctx, url, mirror); err != nil {
		t.Fatalf("Mirror: %v", err)
	}
	// A second Mirror must fetch into the existing mirror, not fail.
	if err := g.Mirror(ctx, url, mirror); err != nil {
		t.Fatalf("second Mirror: %v", err)
	}

	dest := filepath.Join(root, "rev")
	if err := g.Extract(ctx, mirror, sha, dest); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(dest, "SKILL.md"))
	if err != nil {
		t.Fatalf("root SKILL.md missing: %v", err)
	}
	if string(body) != "---\nname: demo\n---\nbody\n" {
		t.Errorf("extracted content = %q", body)
	}
	if _, err := os.Stat(filepath.Join(dest, "skills", "nested", "SKILL.md")); err != nil {
		t.Errorf("nested file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, ".git")); !os.IsNotExist(err) {
		t.Error("extracted revision must not contain a .git directory")
	}
}

func TestMirrorPicksUpNewCommits(t *testing.T) {
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": "v1"})
	dir := testrepo.Dir(url)

	ctx := context.Background()
	mirror := filepath.Join(t.TempDir(), "m.git")
	g := New()
	if err := g.Mirror(ctx, url, mirror); err != nil {
		t.Fatal(err)
	}

	second := testrepo.Commit(t, dir, map[string]string{"SKILL.md": "v2"})
	if err := g.Mirror(ctx, url, mirror); err != nil {
		t.Fatalf("re-Mirror: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "rev")
	if err := g.Extract(ctx, mirror, second, dest); err != nil {
		t.Fatalf("Extract of the new sha: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(dest, "SKILL.md"))
	if string(body) != "v2" {
		t.Errorf("extracted %q, want v2", body)
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `mise exec -- go test ./internal/gitx/ -v`
Expected: FAIL — `undefined: New`.

- [ ] **Step 4: Write the git implementation**

Create `internal/gitx/gitx.go`:

```go
// Package gitx wraps the git binary. Shelling out rather than using a Go git
// library is deliberate: SSH keys, credential helpers and proxies then work
// with no code on our side.
package gitx

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Git is the set of git operations skillsctl needs.
type Git interface {
	// Resolve returns the commit sha for ref (empty ref means HEAD).
	Resolve(ctx context.Context, repoURL, ref string) (string, error)
	// Mirror creates or updates a bare mirror of repoURL at mirrorPath.
	Mirror(ctx context.Context, repoURL, mirrorPath string) error
	// Extract writes the tree at sha into dest, without a .git directory.
	Extract(ctx context.Context, mirrorPath, sha, dest string) error
}

// CLI implements Git using the git binary.
type CLI struct{ Bin string }

// New returns a CLI backed by git on PATH.
func New() *CLI { return &CLI{Bin: "git"} }

var shaRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

func (c *CLI) Resolve(ctx context.Context, repoURL, ref string) (string, error) {
	if shaRe.MatchString(ref) {
		return ref, nil
	}
	query := ref
	if query == "" {
		query = "HEAD"
	}

	out, err := c.output(ctx, "", "ls-remote", repoURL, query)
	if err != nil {
		return "", err
	}

	// Prefer the dereferenced line for annotated tags: refs/tags/v1^{}.
	var fallback string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if strings.HasSuffix(fields[1], "^{}") {
			return fields[0], nil
		}
		if fallback == "" {
			fallback = fields[0]
		}
	}
	if fallback == "" {
		return "", fmt.Errorf("ref %q not found in %s", query, repoURL)
	}
	return fallback, nil
}

func (c *CLI) Mirror(ctx context.Context, repoURL, mirrorPath string) error {
	if _, err := os.Stat(filepath.Join(mirrorPath, "HEAD")); err == nil {
		_, err := c.output(ctx, mirrorPath, "fetch", "--prune", "--tags", "origin")
		return err
	}

	if err := os.MkdirAll(filepath.Dir(mirrorPath), 0o755); err != nil {
		return fmt.Errorf("create mirror directory: %w", err)
	}
	_, err := c.output(ctx, "", "clone", "--mirror", "--quiet", repoURL, mirrorPath)
	return err
}

func (c *CLI) Extract(ctx context.Context, mirrorPath, sha, dest string) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("create destination: %w", err)
	}

	cmd := exec.CommandContext(ctx, c.Bin, "-C", mirrorPath, "archive", "--format=tar", sha)
	cmd.Env = env()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("pipe git archive: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start git archive: %w", err)
	}

	untarErr := untar(stdout, dest)
	// Drain so git never blocks on a full pipe, then reap.
	if waitErr := cmd.Wait(); waitErr != nil {
		return fmt.Errorf("git archive %s: %w: %s", sha, waitErr, strings.TrimSpace(stderr.String()))
	}
	if untarErr != nil {
		return fmt.Errorf("extract %s: %w", sha, untarErr)
	}
	return nil
}

func (c *CLI) output(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, c.Bin, args...)
	cmd.Dir = dir
	cmd.Env = env()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// env keeps git non-interactive so a missing credential never hangs the CLI.
func env() []string {
	return append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ADVICE=0",
	)
}
```

- [ ] **Step 5: Write the tar extractor**

Create `internal/gitx/untar.go`:

```go
package gitx

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// untar writes a tar stream into dest, rejecting any entry that would escape it.
func untar(r io.Reader, dest string) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}

		target, err := safeJoin(dest, hdr.Name)
		if err != nil {
			return err
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("create %s: %w", target, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("create %s: %w", filepath.Dir(target), err)
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return fmt.Errorf("create %s: %w", target, err)
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return fmt.Errorf("write %s: %w", target, err)
			}
			if err := f.Close(); err != nil {
				return fmt.Errorf("close %s: %w", target, err)
			}
		case tar.TypeSymlink:
			if filepath.IsAbs(hdr.Linkname) {
				return fmt.Errorf("symlink %s points outside the revision directory", hdr.Name)
			}
			if _, err := safeJoin(dest, filepath.Join(filepath.Dir(hdr.Name), hdr.Linkname)); err != nil {
				return fmt.Errorf("symlink %s escapes the revision directory", hdr.Name)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return fmt.Errorf("symlink %s: %w", target, err)
			}
		default:
			// Skip anything else: skills are files, directories and symlinks.
		}
	}
}

func safeJoin(dest, name string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("archive entry %q escapes the destination", name)
	}
	return filepath.Join(dest, clean), nil
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `mise exec -- go test ./internal/gitx/ -v`
Expected: PASS — all six tests.

- [ ] **Step 7: Commit**

```bash
git add internal/gitx internal/testrepo
git commit -m "feat: add git mirror, resolve and archive extraction"
```

---

### Task 9: Content-addressed store

**Files:**
- Create: `internal/store/store.go`, `internal/store/hash.go`
- Test: `internal/store/store_test.go`, `internal/store/hash_test.go`

**Interfaces:**
- Consumes: `gitx.Git` (Task 8), `source.Source` (Task 4).
- Produces:
  - `func Home() (string, error)`, `type Store struct { Root string }`, `func New(root string) *Store`
  - `func (s *Store) MirrorPath(slug string) string`, `func (s *Store) RevPath(slug, sha string) string`, `func (s *Store) StatePath() string`
  - `func (s *Store) Ensure(ctx context.Context, g gitx.Git, src source.Source, sha string) (string, error)`
  - `func HashDir(root string) (string, error)`

- [ ] **Step 1: Write the failing hash test**

Create `internal/store/hash_test.go`:

```go
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
```

- [ ] **Step 2: Run the hash test to verify it fails**

Run: `mise exec -- go test ./internal/store/ -v`
Expected: FAIL — `undefined: HashDir`.

- [ ] **Step 3: Write the hash implementation**

Create `internal/store/hash.go`:

```go
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// HashDir fingerprints a directory tree by path, mode and content. It is how
// skillsctl notices that someone edited a skill through its symlink, since
// revision directories carry no .git of their own.
func HashDir(root string) (string, error) {
	type entry struct {
		rel  string
		mode fs.FileMode
		sum  string
	}
	var entries []entry

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}

		sum := ""
		if info.Mode()&os.ModeSymlink != 0 {
			dest, err := os.Readlink(p)
			if err != nil {
				return err
			}
			h := sha256.Sum256([]byte(dest))
			sum = hex.EncodeToString(h[:])
		} else {
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			h := sha256.New()
			_, cerr := io.Copy(h, f)
			f.Close()
			if cerr != nil {
				return cerr
			}
			sum = hex.EncodeToString(h.Sum(nil))
		}

		entries = append(entries, entry{rel: filepath.ToSlash(rel), mode: info.Mode(), sum: sum})
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("hash %s: %w", root, err)
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].rel < entries[j].rel })

	h := sha256.New()
	for _, e := range entries {
		fmt.Fprintf(h, "%s\x00%o\x00%s\n", e.rel, e.mode.Perm(), e.sum)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
```

- [ ] **Step 4: Run the hash tests to verify they pass**

Run: `mise exec -- go test ./internal/store/ -v`
Expected: PASS — all three hash tests.

- [ ] **Step 5: Write the failing store test**

Create `internal/store/store_test.go`:

```go
package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/testrepo"
)

func TestHomePrefersSkillsctlHome(t *testing.T) {
	t.Setenv("SKILLSCTL_HOME", "/custom/root")
	got, err := Home()
	if err != nil {
		t.Fatalf("Home: %v", err)
	}
	if got != "/custom/root" {
		t.Errorf("Home() = %q, want /custom/root", got)
	}
}

func TestHomeUsesXDGDataHome(t *testing.T) {
	t.Setenv("SKILLSCTL_HOME", "")
	t.Setenv("XDG_DATA_HOME", "/xdg/data")
	got, err := Home()
	if err != nil {
		t.Fatalf("Home: %v", err)
	}
	want := filepath.Join("/xdg/data", "skillsctl")
	if got != want {
		t.Errorf("Home() = %q, want %q", got, want)
	}
}

func TestPathsAreDerivedFromSlug(t *testing.T) {
	s := New("/root")
	if got, want := s.MirrorPath("github.com/o/r"), filepath.Join("/root", "cache", "github.com", "o", "r.git"); got != want {
		t.Errorf("MirrorPath = %q, want %q", got, want)
	}
	if got, want := s.RevPath("github.com/o/r", "abc"), filepath.Join("/root", "rev", "github.com", "o", "r", "abc"); got != want {
		t.Errorf("RevPath = %q, want %q", got, want)
	}
	if got, want := s.StatePath(), filepath.Join("/root", "state.json"); got != want {
		t.Errorf("StatePath = %q, want %q", got, want)
	}
}

func TestEnsureExtractsAndIsIdempotent(t *testing.T) {
	url, sha := testrepo.New(t, map[string]string{"SKILL.md": "---\nname: demo\n---\n"})
	src, err := source.Parse(url)
	if err != nil {
		t.Fatalf("Parse(%q): %v", url, err)
	}

	s := New(t.TempDir())
	ctx := context.Background()

	rev, err := s.Ensure(ctx, gitx.New(), src, sha)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if _, err := os.Stat(filepath.Join(rev, "SKILL.md")); err != nil {
		t.Fatalf("SKILL.md not extracted: %v", err)
	}
	if !strings.HasSuffix(rev, sha) {
		t.Errorf("revision path %q should end in the sha", rev)
	}

	// Second call must be a no-op that returns the same path.
	marker := filepath.Join(rev, "MARKER")
	if err := os.WriteFile(marker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	again, err := s.Ensure(ctx, gitx.New(), src, sha)
	if err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	if again != rev {
		t.Errorf("second Ensure returned %q, want %q", again, rev)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Error("second Ensure re-extracted instead of reusing the cached revision")
	}
}

func TestEnsureLeavesNoTempDirOnFailure(t *testing.T) {
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": "x"})
	src, _ := source.Parse(url)

	s := New(t.TempDir())
	_, err := s.Ensure(context.Background(), gitx.New(), src, "0123456789abcdef0123456789abcdef01234567")
	if err == nil {
		t.Fatal("Ensure succeeded for a sha that does not exist")
	}

	revParent := filepath.Dir(s.RevPath(src.Slug(), "irrelevant"))
	entries, rerr := os.ReadDir(revParent)
	if rerr != nil {
		return // nothing was created at all, which is fine
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("temporary extraction directory %q was left behind", e.Name())
		}
	}
}
```

- [ ] **Step 6: Run the store test to verify it fails**

Run: `mise exec -- go test ./internal/store/ -run 'TestHome|TestPaths|TestEnsure' -v`
Expected: FAIL — `undefined: New`, `undefined: Home`.

- [ ] **Step 7: Write the store implementation**

Create `internal/store/store.go`:

```go
// Package store manages the content-addressed cache of repository mirrors and
// immutable revision directories.
package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/richardcase/skillsctl/internal/source"
)

// Store is a skillsctl data root.
type Store struct{ Root string }

// New returns a Store rooted at root.
func New(root string) *Store { return &Store{Root: root} }

// Home locates the data root, honouring SKILLSCTL_HOME and XDG_DATA_HOME
// before falling back to ~/.local/share. Go's os.UserConfigDir is deliberately
// not used: on macOS it resolves to ~/Library/Application Support.
func Home() (string, error) {
	if p := os.Getenv("SKILLSCTL_HOME"); p != "" {
		return p, nil
	}
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "skillsctl"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, ".local", "share", "skillsctl"), nil
}

// MirrorPath is where the bare mirror for slug lives.
func (s *Store) MirrorPath(slug string) string {
	return filepath.Join(s.Root, "cache", filepath.FromSlash(slug)+".git")
}

// RevPath is where the extracted tree for slug at sha lives. A revision holds
// the whole repository; subpath selection happens at link time.
func (s *Store) RevPath(slug, sha string) string {
	return filepath.Join(s.Root, "rev", filepath.FromSlash(slug), sha)
}

// StatePath is the receipts database.
func (s *Store) StatePath() string { return filepath.Join(s.Root, "state.json") }

// Ensure guarantees the revision is extracted, returning its path. It is a
// no-op when the revision is already present, so it is safe to call on every
// install including a --dry-run.
func (s *Store) Ensure(ctx context.Context, g gitx.Git, src source.Source, sha string) (string, error) {
	slug := src.Slug()
	rev := s.RevPath(slug, sha)

	if fi, err := os.Stat(rev); err == nil && fi.IsDir() {
		return rev, nil
	}

	mirror := s.MirrorPath(slug)
	if err := g.Mirror(ctx, src.RepoURL, mirror); err != nil {
		return "", fmt.Errorf("mirror %s: %w", src.RepoURL, err)
	}

	if err := os.MkdirAll(filepath.Dir(rev), 0o755); err != nil {
		return "", fmt.Errorf("create revision directory: %w", err)
	}

	// Extract into a sibling temp directory, then rename, so a revision
	// directory is never observed half-written.
	tmp, err := os.MkdirTemp(filepath.Dir(rev), ".tmp-")
	if err != nil {
		return "", fmt.Errorf("create temp revision directory: %w", err)
	}
	defer os.RemoveAll(tmp)

	if err := g.Extract(ctx, mirror, sha, tmp); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, rev); err != nil {
		// Another process may have won the race; accept its result.
		if fi, serr := os.Stat(rev); serr == nil && fi.IsDir() {
			return rev, nil
		}
		return "", fmt.Errorf("publish revision: %w", err)
	}
	return rev, nil
}
```

- [ ] **Step 8: Run all store tests**

Run: `mise exec -- go test ./internal/store/ -v`
Expected: PASS — all eight tests.

- [ ] **Step 9: Commit**

```bash
git add internal/store
git commit -m "feat: add content-addressed revision store"
```

---

### Task 10: SKILL.md discovery

**Files:**
- Create: `internal/discover/discover.go`
- Test: `internal/discover/discover_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Meta struct { Name, Description string }`
  - `type Skill struct { Meta; Dir string }`
  - `func Frontmatter(data []byte) (Meta, error)`
  - `func Root(dir string) (Skill, error)` and `var ErrNoSkill = errors.New(...)`

- [ ] **Step 1: Write the failing test**

Create `internal/discover/discover_test.go`:

```go
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
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mise exec -- go test ./internal/discover/ -v`
Expected: FAIL — `undefined: Frontmatter`.

- [ ] **Step 3: Write the implementation**

```bash
mise exec -- go get gopkg.in/yaml.v3@latest
```

Create `internal/discover/discover.go`:

```go
// Package discover reads SKILL.md files and their YAML frontmatter.
package discover

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ErrNoSkill reports a directory with no SKILL.md.
var ErrNoSkill = errors.New("no SKILL.md")

// FileName is the file that marks a directory as a skill.
const FileName = "SKILL.md"

// Meta is the frontmatter skillsctl cares about.
type Meta struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// Skill is a discovered skill directory.
type Skill struct {
	Meta
	Dir string
}

// Frontmatter parses a leading `---` YAML block. A file with no block is not
// an error: the caller falls back to a name derived from the source.
func Frontmatter(data []byte) (Meta, error) {
	body := bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))

	const fence = "---\n"
	if !bytes.HasPrefix(body, []byte(fence)) {
		return Meta{}, nil
	}
	rest := body[len(fence):]

	end := bytes.Index(rest, []byte("\n---"))
	if end < 0 {
		return Meta{}, fmt.Errorf("frontmatter block is not terminated by ---")
	}

	var m Meta
	if err := yaml.Unmarshal(rest[:end+1], &m); err != nil {
		return Meta{}, fmt.Errorf("parse frontmatter: %w", err)
	}
	return m, nil
}

// Root reads the SKILL.md directly inside dir.
func Root(dir string) (Skill, error) {
	p := filepath.Join(dir, FileName)

	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return Skill{}, fmt.Errorf("%s: %w", dir, ErrNoSkill)
	}
	if err != nil {
		return Skill{}, fmt.Errorf("read %s: %w", p, err)
	}

	m, err := Frontmatter(data)
	if err != nil {
		return Skill{}, fmt.Errorf("%s: %w", p, err)
	}
	return Skill{Meta: m, Dir: dir}, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mise exec -- go test ./internal/discover/ -v`
Expected: PASS — all seven tests.

- [ ] **Step 5: Commit**

```bash
git add internal/discover go.mod go.sum
git commit -m "feat: read SKILL.md frontmatter"
```

---

### Task 11: install, list and remove commands

**Files:**
- Create: `internal/cli/context.go`, `internal/cli/install.go`, `internal/cli/list.go`, `internal/cli/remove.go`
- Modify: `internal/cli/root.go`
- Test: `internal/cli/cli_test.go`

**Interfaces:**
- Consumes: every package from Tasks 4–10.
- Produces: the `skillsctl install`, `skillsctl list` and `skillsctl remove` commands.

- [ ] **Step 1: Write the failing end-to-end test**

Create `internal/cli/cli_test.go`:

```go
package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/testrepo"
)

// harness points skillsctl at a temp store and two temp agent directories.
type harness struct {
	root   string
	agents string
	claude string
	codex  string
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	root := t.TempDir()
	agents := t.TempDir()
	h := &harness{
		root:   filepath.Join(root, "store"),
		agents: agents,
		claude: filepath.Join(agents, ".claude", "skills"),
		codex:  filepath.Join(agents, ".codex", "skills"),
	}

	// Both agent parent directories exist, so both are "present".
	for _, d := range []string{filepath.Join(agents, ".claude"), filepath.Join(agents, ".codex")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	cfg := filepath.Join(root, "config.toml")
	body := "[[target]]\nname = \"claude\"\ndir = \"" + h.claude + "\"\nplugins = true\n\n" +
		"[[target]]\nname = \"codex\"\ndir = \"" + h.codex + "\"\n"
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SKILLSCTL_HOME", h.root)
	t.Setenv("SKILLSCTL_CONFIG", cfg)
	return h
}

// run executes the command tree and returns combined output.
func (h *harness) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	root := NewRootCmd()
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

const skillMD = "---\nname: demo-skill\ndescription: A demo\n---\n\nBody.\n"

func TestInstallListRemoveRoundTrip(t *testing.T) {
	h := newHarness(t)
	url, sha := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	out, err := h.run(t, "install", url)
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	for name, dir := range map[string]string{"claude": h.claude, "codex": h.codex} {
		link := filepath.Join(dir, "demo-skill")
		dest, rerr := os.Readlink(link)
		if rerr != nil {
			t.Fatalf("%s link missing: %v", name, rerr)
		}
		if _, serr := os.Stat(filepath.Join(dest, "SKILL.md")); serr != nil {
			t.Errorf("%s link does not resolve to a skill: %v", name, serr)
		}
		if !strings.Contains(dest, sha) {
			t.Errorf("%s link target %q should contain the sha", name, dest)
		}
	}

	out, err = h.run(t, "list")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, out)
	}
	if !strings.Contains(out, "demo-skill") {
		t.Errorf("list output missing the skill:\n%s", out)
	}

	out, err = h.run(t, "remove", "demo-skill")
	if err != nil {
		t.Fatalf("remove: %v\n%s", err, out)
	}
	for name, dir := range map[string]string{"claude": h.claude, "codex": h.codex} {
		if _, serr := os.Lstat(filepath.Join(dir, "demo-skill")); !os.IsNotExist(serr) {
			t.Errorf("%s link survived removal", name)
		}
	}

	out, _ = h.run(t, "list")
	if strings.Contains(out, "demo-skill") {
		t.Errorf("removed skill still listed:\n%s", out)
	}
}

func TestInstallFallsBackToRepoNameWithoutFrontmatter(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": "# No frontmatter\n"})

	if out, err := h.run(t, "install", url); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	want := filepath.Base(testrepo.Dir(url))
	if _, err := os.Lstat(filepath.Join(h.claude, want)); err != nil {
		t.Errorf("expected a link named %q from the repo name: %v", want, err)
	}
}

func TestInstallSingleAgent(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	if out, err := h.run(t, "install", url, "-a", "claude"); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if _, err := os.Lstat(filepath.Join(h.claude, "demo-skill")); err != nil {
		t.Errorf("claude link missing: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(h.codex, "demo-skill")); !os.IsNotExist(err) {
		t.Error("codex should not have been linked")
	}
}

func TestInstallDryRunChangesNothing(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	out, err := h.run(t, "install", url, "--dry-run")
	if err != nil {
		t.Fatalf("install --dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "link") {
		t.Errorf("dry run should describe the link ops:\n%s", out)
	}
	if _, err := os.Lstat(filepath.Join(h.claude, "demo-skill")); !os.IsNotExist(err) {
		t.Error("dry run created a symlink")
	}
	if _, err := os.Stat(filepath.Join(h.root, "state.json")); !os.IsNotExist(err) {
		t.Error("dry run wrote the receipts database")
	}
}

func TestInstallRejectsDuplicateName(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	if _, err := h.run(t, "install", url); err != nil {
		t.Fatalf("first install: %v", err)
	}
	out, err := h.run(t, "install", url)
	if err == nil {
		t.Fatalf("second install succeeded; want a name-collision error\n%s", out)
	}
	if !strings.Contains(err.Error(), "--as") {
		t.Errorf("error should suggest --as, got: %v", err)
	}
}

func TestInstallAsOverridesName(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	if out, err := h.run(t, "install", url, "--as", "renamed"); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if _, err := os.Lstat(filepath.Join(h.claude, "renamed")); err != nil {
		t.Errorf("link named 'renamed' missing: %v", err)
	}
}

func TestInstallRejectsUnsupportedChannel(t *testing.T) {
	h := newHarness(t)

	if _, err := h.run(t, "install", "superpowers@claude-plugins-official"); err == nil {
		t.Fatal("plugin install succeeded; the plugin channel arrives in phase 3")
	}
}

func TestRemoveSingleAgentKeepsReceipt(t *testing.T) {
	h := newHarness(t)
	url, _ := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	if _, err := h.run(t, "install", url); err != nil {
		t.Fatal(err)
	}
	if out, err := h.run(t, "remove", "demo-skill", "-a", "codex"); err != nil {
		t.Fatalf("remove: %v\n%s", err, out)
	}

	if _, err := os.Lstat(filepath.Join(h.codex, "demo-skill")); !os.IsNotExist(err) {
		t.Error("codex link should be gone")
	}
	if _, err := os.Lstat(filepath.Join(h.claude, "demo-skill")); err != nil {
		t.Error("claude link should survive a codex-only removal")
	}

	out, _ := h.run(t, "list")
	if !strings.Contains(out, "demo-skill") {
		t.Errorf("skill should still be listed while one link remains:\n%s", out)
	}
}

func TestRemoveUnknownSkillErrors(t *testing.T) {
	h := newHarness(t)
	if _, err := h.run(t, "remove", "never-installed"); err == nil {
		t.Fatal("remove of an unknown skill succeeded; want an error")
	}
}

func TestListJSON(t *testing.T) {
	h := newHarness(t)
	url, sha := testrepo.New(t, map[string]string{"SKILL.md": skillMD})

	if _, err := h.run(t, "install", url); err != nil {
		t.Fatal(err)
	}
	out, err := h.run(t, "list", "--json")
	if err != nil {
		t.Fatalf("list --json: %v\n%s", err, out)
	}

	var got []struct {
		Name     string `json:"name"`
		Channel  string `json:"channel"`
		Resolved string `json:"resolved"`
		Links    []struct {
			Target string `json:"target"`
		} `json:"links"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("list --json emitted invalid JSON: %v\n%s", err, out)
	}
	if len(got) != 1 {
		t.Fatalf("got %d receipts, want 1", len(got))
	}
	if got[0].Name != "demo-skill" || got[0].Resolved != sha {
		t.Errorf("receipt = %+v, want demo-skill @ %s", got[0], sha)
	}
	if len(got[0].Links) != 2 {
		t.Errorf("got %d links, want 2", len(got[0].Links))
	}
}

func TestListEmpty(t *testing.T) {
	h := newHarness(t)
	out, err := h.run(t, "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "No skills installed") {
		t.Errorf("empty list should say so, got:\n%s", out)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mise exec -- go test ./internal/cli/ -v`
Expected: FAIL — `unknown command "install"`.

- [ ] **Step 3: Write the shared command context**

Create `internal/cli/context.go`:

```go
package cli

import (
	"fmt"

	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/store"
	"github.com/richardcase/skillsctl/internal/target"
)

// env is the resolved environment a command runs against.
type env struct {
	store *store.Store
	cfg   target.Config
}

func newEnv() (*env, error) {
	root, err := store.Home()
	if err != nil {
		return nil, err
	}
	cfgPath, err := target.ConfigPath()
	if err != nil {
		return nil, err
	}
	cfg, err := target.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	return &env{store: store.New(root), cfg: cfg}, nil
}

// targets resolves the -a flag, defaulting to every present agent.
func (e *env) targets(names []string) ([]target.Target, error) {
	if len(names) > 0 {
		return e.cfg.Select(names)
	}
	present := e.cfg.Present()
	if len(present) == 0 {
		return nil, fmt.Errorf("no agent directories found: create one (for example ~/.claude) or configure targets")
	}
	return present, nil
}

// openState acquires the receipts database.
func (e *env) openState() (*state.Handle, error) {
	return state.Open(e.store.StatePath())
}
```

- [ ] **Step 4: Write the install command**

Create `internal/cli/install.go`:

```go
package cli

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/richardcase/skillsctl/internal/discover"
	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/store"
	"github.com/spf13/cobra"
)

func newInstallCmd() *cobra.Command {
	var (
		agents []string
		ref    string
		as     string
		pin    bool
		dryRun bool
	)

	cmd := &cobra.Command{
		Use:   "install <source>",
		Short: "Install a skill",
		Long: "Install a skill from a git repository.\n\n" +
			"Sources may be owner/repo, owner/repo/path/to/skill, any git URL, or a local path.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInstall(cmd, args[0], agents, ref, as, pin, dryRun)
		},
	}

	cmd.Flags().StringSliceVarP(&agents, "agent", "a", nil, "agents to link into (default: every agent found)")
	cmd.Flags().StringVar(&ref, "ref", "", "branch, tag or sha to install (default: the repository's HEAD)")
	cmd.Flags().StringVar(&as, "as", "", "install under this name instead of the one in SKILL.md")
	cmd.Flags().BoolVar(&pin, "pin", false, "freeze at the resolved sha, so update skips it")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change without changing it")
	return cmd
}

func runInstall(cmd *cobra.Command, raw string, agents []string, ref, as string, pin, dryRun bool) error {
	ctx := cmd.Context()

	src, err := source.Parse(raw)
	if err != nil {
		return err
	}
	if src.Channel != source.ChannelGit {
		return fmt.Errorf("the %s channel is not supported yet", src.Channel)
	}

	e, err := newEnv()
	if err != nil {
		return err
	}
	targets, err := e.targets(agents)
	if err != nil {
		return err
	}

	g := gitx.New()
	sha, err := g.Resolve(ctx, src.RepoURL, ref)
	if err != nil {
		return err
	}

	// Populating the content-addressed cache is idempotent and not a
	// user-visible mutation, so it runs even for --dry-run. It is what lets
	// the plan below name the skill exactly rather than guess.
	revRoot, err := e.store.Ensure(ctx, g, src, sha)
	if err != nil {
		return err
	}
	revPath := filepath.Join(revRoot, filepath.FromSlash(src.Subpath))

	skill, err := discover.Root(revPath)
	if err != nil {
		return err
	}

	name := as
	if name == "" {
		name = skill.Name
	}
	if name == "" {
		name = src.DefaultName()
	}
	if name == "" {
		return fmt.Errorf("could not determine a name for this skill: pass --as")
	}

	hash, err := store.HashDir(revPath)
	if err != nil {
		return err
	}

	h, err := e.openState()
	if err != nil {
		return err
	}
	defer h.Close()

	if existing, ok := h.DB.Receipts[name]; ok {
		return fmt.Errorf("%q is already installed from %s: remove it first, or install this one with --as <name>", name, existing.Source)
	}

	now := time.Now().UTC()
	receipt := state.Receipt{
		Name:        name,
		Channel:     string(src.Channel),
		Source:      src.RepoURL,
		Slug:        src.Slug(),
		Subpath:     src.Subpath,
		Resolved:    sha,
		Pinned:      pin,
		RevPath:     revPath,
		ContentHash: hash,
		InstalledAt: now,
		UpdatedAt:   now,
	}
	if !pin {
		receipt.Ref = ref
	}

	var p plan.Plan
	for _, t := range targets {
		linkPath := filepath.Join(t.Dir, name)
		p.Add(plan.Link{Target: t.Name, LinkPath: linkPath, RevPath: revPath})
		receipt.Links = append(receipt.Links, state.Link{Target: t.Name, Path: linkPath})
	}
	p.Add(plan.Record{Receipt: receipt})

	if dryRun {
		for _, line := range p.Describe() {
			cmd.Println(line)
		}
		return nil
	}

	ex := &plan.Executor{DB: h.DB, Out: cmd.OutOrStdout()}
	if err := ex.Apply(ctx, p); err != nil {
		return err
	}
	if err := h.Commit(); err != nil {
		return err
	}

	cmd.Printf("installed %s @ %s into %s\n", name, shortSha(sha), targetNames(targets))
	return nil
}
```

- [ ] **Step 5: Write the list and remove commands**

Create `internal/cli/list.go`:

```go
package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/target"
	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List installed skills",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := newEnv()
			if err != nil {
				return err
			}
			h, err := e.openState()
			if err != nil {
				return err
			}
			defer h.Close()

			receipts := h.DB.List()

			if asJSON {
				if receipts == nil {
					receipts = []*state.Receipt{}
				}
				blob, err := json.MarshalIndent(receipts, "", "  ")
				if err != nil {
					return err
				}
				cmd.Println(string(blob))
				return nil
			}

			if len(receipts) == 0 {
				cmd.Println("No skills installed.")
				return nil
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tCHANNEL\tVERSION\tAGENTS")
			for _, r := range receipts {
				var agents []string
				for _, l := range r.Links {
					agents = append(agents, l.Target)
				}
				version := shortSha(r.Resolved)
				if r.Pinned {
					version += " (pinned)"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", r.Name, r.Channel, version, strings.Join(agents, ","))
			}
			return w.Flush()
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the receipts as JSON")
	return cmd
}

func shortSha(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func targetNames(ts []target.Target) string {
	names := make([]string, 0, len(ts))
	for _, t := range ts {
		names = append(names, t.Name)
	}
	return strings.Join(names, ", ")
}
```

Create `internal/cli/remove.go`:

```go
package cli

import (
	"fmt"
	"time"

	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/spf13/cobra"
)

func newRemoveCmd() *cobra.Command {
	var (
		agents []string
		dryRun bool
	)

	cmd := &cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"uninstall", "rm"},
		Short:   "Remove an installed skill",
		Long: "Remove a skill from every agent, or from just the agents named with -a.\n\n" +
			"Removing from some agents keeps the receipt; removing the last link forgets it.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			e, err := newEnv()
			if err != nil {
				return err
			}
			h, err := e.openState()
			if err != nil {
				return err
			}
			defer h.Close()

			receipt, ok := h.DB.Receipts[name]
			if !ok {
				return fmt.Errorf("%q is not installed", name)
			}

			drop := map[string]bool{}
			if len(agents) > 0 {
				selected, err := e.cfg.Select(agents)
				if err != nil {
					return err
				}
				for _, t := range selected {
					drop[t.Name] = true
				}
			}

			var p plan.Plan
			var keep []state.Link
			for _, l := range receipt.Links {
				if len(drop) > 0 && !drop[l.Target] {
					keep = append(keep, l)
					continue
				}
				p.Add(plan.Unlink{Target: l.Target, LinkPath: l.Path})
			}

			if p.IsEmpty() {
				return fmt.Errorf("%q is not linked into %v", name, agents)
			}

			if len(keep) == 0 {
				p.Add(plan.Forget{Name: name})
			} else {
				updated := *receipt
				updated.Links = keep
				updated.UpdatedAt = time.Now().UTC()
				p.Add(plan.Record{Receipt: updated})
			}

			if dryRun {
				for _, line := range p.Describe() {
					cmd.Println(line)
				}
				return nil
			}

			ex := &plan.Executor{DB: h.DB, Out: cmd.OutOrStdout()}
			if err := ex.Apply(cmd.Context(), p); err != nil {
				return err
			}
			if err := h.Commit(); err != nil {
				return err
			}

			cmd.Printf("removed %s\n", name)
			return nil
		},
	}

	cmd.Flags().StringSliceVarP(&agents, "agent", "a", nil, "remove from these agents only")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change without changing it")
	return cmd
}
```

- [ ] **Step 6: Register the commands**

In `internal/cli/root.go`, replace the `root.AddCommand(newVersionCmd())` line with:

```go
	root.AddCommand(
		newInstallCmd(),
		newListCmd(),
		newRemoveCmd(),
		newVersionCmd(),
	)
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `mise exec -- go test ./internal/cli/ -v`
Expected: PASS — all eleven tests.

- [ ] **Step 8: Run the whole suite and lint**

Run: `make test && make lint && make tidy-check`
Expected: all pass, no lint findings.

- [ ] **Step 9: Commit**

```bash
git add internal/cli
git commit -m "feat: add install, list and remove commands"
```

- [ ] **Step 10: Manual smoke test against a real repository**

Use a throwaway store and throwaway agent directories so nothing real is touched:

```bash
SMOKE=$(mktemp -d)
mkdir -p "$SMOKE/agents/.claude" "$SMOKE/agents/.codex"
cat > "$SMOKE/config.toml" <<EOF
[[target]]
name = "claude"
dir = "$SMOKE/agents/.claude/skills"
plugins = true

[[target]]
name = "codex"
dir = "$SMOKE/agents/.codex/skills"
EOF
export SKILLSCTL_HOME="$SMOKE/store" SKILLSCTL_CONFIG="$SMOKE/config.toml"

mise exec -- go build -o "$SMOKE/skillsctl" ./cmd/skillsctl
"$SMOKE/skillsctl" install conorbronsdon/avoid-ai-writing --dry-run
"$SMOKE/skillsctl" install conorbronsdon/avoid-ai-writing
"$SMOKE/skillsctl" list
ls -l "$SMOKE/agents/.claude/skills" "$SMOKE/agents/.codex/skills"
"$SMOKE/skillsctl" remove avoid-ai-writing
"$SMOKE/skillsctl" list
```

Expected: the dry run prints two `link` lines and one `record` line and creates no symlinks; the real install creates a symlink in both agent directories resolving to a directory containing `SKILL.md`; `list` shows one row; `remove` deletes both symlinks and `list` then reports `No skills installed.`

Confirm the real `~/.claude/skills` was untouched:

```bash
ls ~/.claude/skills | wc -l   # unchanged from before the smoke test
rm -rf "$SMOKE"
```

- [ ] **Step 11: Update the README**

Replace `README.md` with:

````markdown
# skillsctl

Homebrew for agent skills: install, update and remove agent skills from git
repositories, with a receipt for every install so update and removal are
deterministic. One store, symlinked into every agent you use.

## Install

```bash
brew install richardcase/tap/skillsctl
```

Or grab a binary or `.deb`/`.rpm` from the [releases page](https://github.com/richardcase/skillsctl/releases),
or build from source with `go install github.com/richardcase/skillsctl/cmd/skillsctl@latest`.

## Use

```bash
skillsctl install conorbronsdon/avoid-ai-writing   # link into every agent found
skillsctl install owner/repo -a claude             # just one agent
skillsctl install owner/repo --ref v1.2.0 --pin    # pin a version
skillsctl install owner/repo --dry-run             # show what would change
skillsctl list                                     # what's installed
skillsctl remove avoid-ai-writing                  # unlink everywhere
```

Skills are fetched once into `~/.local/share/skillsctl` and symlinked into each
agent's skills directory, so one copy serves Claude Code, Codex and Gemini.

## Configuration

Agents are configured in `~/.config/skillsctl/config.toml`. Without one,
skillsctl uses built-in defaults for Claude Code, Codex and Gemini, and installs
into whichever of them exist.

```toml
[[target]]
name = "claude"
dir = "~/.claude/skills"
plugins = true

[[target]]
name = "codex"
dir = "~/.codex/skills"
```

## Development

Tooling is pinned in `mise.toml` and installed with [mise](https://mise.jdx.dev):

```bash
mise install     # go, golangci-lint, goreleaser at the pinned versions
make test
make lint
make snapshot    # build release artifacts locally, into dist/
```

CI installs the same `mise.toml`, so local and CI tool versions never drift.

## Design

See [the design spec](docs/superpowers/specs/2026-08-13-skillsctl-design.md).
````

- [ ] **Step 12: Commit and cut the first release**

```bash
git add README.md
git commit -m "docs: describe installation and usage"
git push -u origin richardcase/firstversion
```

Then, once CI is green on `main` and the two manual prerequisites from Task 3 Step 7 are done, tag the first release:

```bash
git tag v0.1.0
git push origin v0.1.0
```

Verify: the Release workflow succeeds, the GitHub Release has the `darwin_all` archive, two linux archives, four packages and `checksums.txt`, and `brew install richardcase/tap/skillsctl && skillsctl version` prints `v0.1.0`.

---

## What this plan deliberately leaves out

These are spec features belonging to later phases. Do not implement them here:

- `update`, `outdated`, `--pin` *honouring* (the flag is recorded but nothing reads it yet), `gc`
- `--skill` / `--all` and multi-skill repository discovery — only a repository whose root is a skill, or an explicit subpath, installs today
- The `plugin` and `local` channels — `install` rejects them with a clear message
- `adopt`, `doctor`, `link`, `bundle`, `sync`, `--project`
