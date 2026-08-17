package adopt

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/richardcase/skillsctl/internal/discover"
	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/richardcase/skillsctl/internal/source"
)

// lockFileName is the manifest npx skills (github.com/vercel-labs/skills)
// writes at the root of whatever directory it was run from. It never
// git-clones — it extracts a GitHub tarball straight to disk — so a skill it
// installed has no .git for Describe to find, but this file names the
// upstream repo it came from.
const lockFileName = "skills-lock.json"

// npxSkillsLock is the subset of skills-lock.json adopt needs.
type npxSkillsLock struct {
	Skills map[string]npxSkillsLockEntry `json:"skills"`
}

type npxSkillsLockEntry struct {
	Source     string `json:"source"`
	SourceType string `json:"sourceType"`
	SkillPath  string `json:"skillPath"`
}

// findLockFile walks upward from dir looking for skills-lock.json, stopping
// at the filesystem root. npx skills always writes it above the directories
// it extracts skills into, and how many levels up depends on the project's
// own layout, so the walk is unbounded rather than a fixed depth.
//
// A match found this way still has to name the entry by its exact directory
// basename, parse as a git source, and resolve against a real repository
// before it becomes provenance, so an unrelated lockfile higher up the tree
// producing a wrong-but-plausible receipt is accepted as a narrow risk.
func findLockFile(dir string) (string, bool) {
	d := filepath.Clean(dir)
	for {
		candidate := filepath.Join(d, lockFileName)
		if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
			return candidate, true
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", false
		}
		d = parent
	}
}

// npxSkillsProvenance recovers git provenance for a skill npx skills
// extracted, by reading its skills-lock.json sidecar and resolving the
// upstream repo it names live. Any failure returns ok=false: a lockfile
// that is missing is the ordinary "nothing to promote" case and carries no
// reason, but everything past that point (present yet silent about this
// skill, or naming something adopt cannot install from) gets one, since
// there the user has a concrete question to ask npx skills' own manifest.
func npxSkillsProvenance(ctx context.Context, g gitx.Git, dest string) (Provenance, string, bool) {
	lockPath, found := findLockFile(filepath.Dir(dest))
	if !found {
		return Provenance{}, "", false
	}

	data, err := os.ReadFile(lockPath)
	if err != nil {
		return Provenance{}, "", false
	}
	var lock npxSkillsLock
	if err := json.Unmarshal(data, &lock); err != nil {
		return Provenance{}, "", false
	}

	entry, ok := lock.Skills[filepath.Base(dest)]
	if !ok {
		return Provenance{}, fmt.Sprintf("no entry for %q in %s", filepath.Base(dest), lockPath), false
	}
	if entry.SourceType != "github" {
		return Provenance{}, fmt.Sprintf("named in %s with sourceType %q, which adopt does not resolve", lockPath, entry.SourceType), false
	}

	repo, err := source.Parse(entry.Source)
	if err != nil || repo.Channel != source.ChannelGit {
		return Provenance{}, fmt.Sprintf("named in %s with a source that is not a git source: %s", lockPath, entry.Source), false
	}

	sha, err := g.Resolve(ctx, repo.RepoURL, "")
	if err != nil {
		return Provenance{}, fmt.Sprintf("named in %s but resolving %s failed: %v", lockPath, repo.RepoURL, err), false
	}

	subpath := strings.TrimSuffix(entry.SkillPath, "/"+discover.FileName)
	return Provenance{Repo: repo, SHA: sha, Subpath: subpath}, "", true
}
