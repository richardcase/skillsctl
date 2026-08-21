package manifest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/store"
)

// FetchRemote resolves raw as a git repository at ref (empty meaning its
// HEAD) and returns the skills.toml decoded from its root.
//
// It composes source.Parse, gitx.Resolve and store.Ensure — the same three
// calls an install of a skill from raw would make — because fetching a file
// out of a resolved git tree is what those already do; a manifest fetch is a
// smaller case of the same operation, not a different one. store.Ensure's own
// cache means a second FetchRemote against an unchanged repository touches
// neither the network nor the disk beyond one ls-remote.
func FetchRemote(ctx context.Context, raw, ref string, g gitx.Git, st *store.Store) (File, error) {
	src, err := source.Parse(raw)
	if err != nil {
		return File{}, fmt.Errorf("%s: %w", raw, err)
	}
	if src.Channel != source.ChannelGit {
		return File{}, fmt.Errorf("%s: a profile source must be a git repository, not %s", raw, src.Channel)
	}
	if src.Subpath != "" {
		return File{}, fmt.Errorf("%s: names subpath %q, but skills.toml is always read from the repository root", raw, src.Subpath)
	}

	sha, err := g.Resolve(ctx, src.RepoURL, ref)
	if err != nil {
		return File{}, fmt.Errorf("resolve %s: %w", raw, err)
	}
	rev, err := st.Ensure(ctx, g, src.Slug(), src.RepoURL, sha)
	if err != nil {
		return File{}, err
	}

	path := filepath.Join(rev, "skills.toml")
	blob, err := os.ReadFile(path)
	if err != nil {
		return File{}, fmt.Errorf("read %s: %w", path, err)
	}
	f, err := Decode(blob)
	if err != nil {
		return File{}, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}
