package manifest

import (
	"context"
	"testing"

	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/richardcase/skillsctl/internal/store"
	"github.com/richardcase/skillsctl/internal/testrepo"
)

func TestFetchRemoteReadsSkillsTomlAtHEAD(t *testing.T) {
	url, _ := testrepo.New(t, map[string]string{
		"skills.toml": "version = 1\n\n[[skill]]\nname = 'alpha'\nsource = 'https://github.com/owner/repo.git'\n",
	})
	st := store.New(t.TempDir())

	f, err := FetchRemote(context.Background(), url, "", gitx.New(), st)
	if err != nil {
		t.Fatalf("FetchRemote: %v", err)
	}
	if len(f.Skills) != 1 || f.Skills[0].Name != "alpha" {
		t.Fatalf("got %+v, want one skill named alpha", f.Skills)
	}
}

func TestFetchRemoteAtAPinnedSha(t *testing.T) {
	url, sha1 := testrepo.New(t, map[string]string{"skills.toml": "version = 1\n"})
	dir := testrepo.Dir(url)
	testrepo.Commit(t, dir, map[string]string{
		"skills.toml": "version = 1\n\n[[skill]]\nname = 'alpha'\nsource = 'https://github.com/owner/repo.git'\n",
	})
	st := store.New(t.TempDir())

	old, err := FetchRemote(context.Background(), url, sha1, gitx.New(), st)
	if err != nil {
		t.Fatalf("FetchRemote at %s: %v", sha1, err)
	}
	if len(old.Skills) != 0 {
		t.Errorf("got %d skills at the old sha, want 0", len(old.Skills))
	}

	head, err := FetchRemote(context.Background(), url, "", gitx.New(), st)
	if err != nil {
		t.Fatalf("FetchRemote at HEAD: %v", err)
	}
	if len(head.Skills) != 1 {
		t.Errorf("got %d skills at HEAD, want 1", len(head.Skills))
	}
}

func TestFetchRemoteRefusesANonGitSource(t *testing.T) {
	st := store.New(t.TempDir())
	if _, err := FetchRemote(context.Background(), "some-plugin@marketplace", "", gitx.New(), st); err == nil {
		t.Fatal("want an error for a non-git profile source")
	}
}

func TestFetchRemoteRefusesASubpath(t *testing.T) {
	st := store.New(t.TempDir())
	if _, err := FetchRemote(context.Background(), "owner/repo/some/subpath", "", gitx.New(), st); err == nil {
		t.Fatal("want an error for a profile source naming a subpath: skills.toml is always at the root")
	}
}

func TestFetchRemoteErrorsWhenTheRepoHasNoManifest(t *testing.T) {
	url, _ := testrepo.New(t, map[string]string{"README.md": "hi\n"})
	st := store.New(t.TempDir())

	if _, err := FetchRemote(context.Background(), url, "", gitx.New(), st); err == nil {
		t.Fatal("want an error when the repository has no skills.toml at its root")
	}
}
