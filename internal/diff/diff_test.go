package diff

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/richardcase/skillsctl/internal/ocix"
	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/store"
	"github.com/richardcase/skillsctl/internal/testrepo"
)

const diffSkillMD = "---\nname: demo\ndescription: A demo\n---\n\nBody.\n"

func gitFixture(t *testing.T) (st *store.Store, g gitx.Git, r *state.Receipt, first, second string) {
	t.Helper()

	url, first := testrepo.New(t, map[string]string{"SKILL.md": diffSkillMD})
	second = testrepo.Commit(t, testrepo.Dir(url), map[string]string{"SKILL.md": diffSkillMD + "\nMore.\n"})

	src, err := source.Parse(url)
	if err != nil {
		t.Fatalf("Parse(%q): %v", url, err)
	}

	st = store.New(t.TempDir())
	g = gitx.New()
	rev, err := st.Ensure(context.Background(), g, src.Slug(), src.RepoURL, first)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	r = &state.Receipt{
		Name:     "demo",
		Channel:  "git",
		Source:   src.RepoURL,
		Slug:     src.Slug(),
		Ref:      "main",
		Resolved: first,
		RevPath:  rev,
	}
	return st, g, r, first, second
}

func TestCheckAgainstLatestReportsWhatUpdateWouldChange(t *testing.T) {
	st, g, r, _, _ := gitFixture(t)

	out, err := Check(context.Background(), g, refusingOCI{}, st, r, Latest)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !strings.Contains(out, "More.") {
		t.Errorf("diff missing the change introduced by the second commit:\n%s", out)
	}
}

func TestCheckAgainstLatestIsEmptyWhenCurrent(t *testing.T) {
	st, g, r, _, second := gitFixture(t)
	r.Resolved = second
	r.Ref = "main"

	out, err := Check(context.Background(), g, refusingOCI{}, st, r, Latest)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if out != "" {
		t.Errorf("Check() = %q, want empty: nothing has moved", out)
	}
}

func TestCheckAgainstPreviousUsesThePreviousRevision(t *testing.T) {
	st, g, r, first, second := gitFixture(t)
	r.Resolved = second
	r.PreviousResolved = first

	out, err := Check(context.Background(), g, refusingOCI{}, st, r, Previous)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !strings.Contains(out, "More.") {
		t.Errorf("diff missing the change between the two revisions:\n%s", out)
	}
}

func TestCheckAgainstPreviousRefusesWhenThereIsNone(t *testing.T) {
	st, g, r, _, _ := gitFixture(t)

	_, err := Check(context.Background(), g, refusingOCI{}, st, r, Previous)
	if err == nil {
		t.Fatal("Check accepted Previous with no PreviousResolved recorded")
	}
	if !strings.Contains(err.Error(), "never been updated") {
		t.Errorf("error = %q, want it to say the skill has never been updated", err)
	}
}

func TestCheckRefusesAnUnsupportedChannel(t *testing.T) {
	st, g, r, _, _ := gitFixture(t)
	r.Channel = "local"

	_, err := Check(context.Background(), g, refusingOCI{}, st, r, Latest)
	if err == nil {
		t.Fatal("Check accepted a local receipt, which has no revision history to diff")
	}
}

func TestCheckScopesAGitDiffToTheReceiptsSubpath(t *testing.T) {
	url, first := testrepo.New(t, map[string]string{
		"alpha/SKILL.md": diffSkillMD,
		"beta/SKILL.md":  diffSkillMD,
	})
	testrepo.Commit(t, testrepo.Dir(url), map[string]string{
		"alpha/SKILL.md": diffSkillMD + "\nMine.\n",
		"beta/SKILL.md":  diffSkillMD + "\nTheirs.\n",
	})

	src, err := source.Parse(url)
	if err != nil {
		t.Fatalf("Parse(%q): %v", url, err)
	}

	st := store.New(t.TempDir())
	g := gitx.New()
	rev, err := st.Ensure(context.Background(), g, src.Slug(), src.RepoURL, first)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	r := &state.Receipt{
		Name:     "alpha",
		Channel:  "git",
		Source:   src.RepoURL,
		Slug:     src.Slug(),
		Subpath:  "alpha",
		Ref:      "main",
		Resolved: first,
		RevPath:  rev,
	}

	out, err := Check(context.Background(), g, refusingOCI{}, st, r, Latest)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !strings.Contains(out, "Mine.") {
		t.Errorf("diff missing the installed skill's own change:\n%s", out)
	}
	if strings.Contains(out, "Theirs.") || strings.Contains(out, "beta/") {
		t.Errorf("diff reported a change outside the installed skill's subpath:\n%s", out)
	}
}

func TestCheckScopesAnOCIDiffToTheReceiptsSubpath(t *testing.T) {
	st := store.New(t.TempDir())
	o := twoSkillOCI{
		"sha256:aaa": {"alpha": diffSkillMD, "beta": diffSkillMD},
		"sha256:bbb": {"alpha": diffSkillMD + "\nMine.\n", "beta": diffSkillMD + "\nTheirs.\n"},
	}

	r := &state.Receipt{
		Name:             "alpha",
		Channel:          "oci",
		Source:           "oci://ghcr.io/owner/skills:v1",
		Slug:             "ghcr.io-owner-skills",
		Subpath:          "alpha",
		Ref:              "v1",
		Resolved:         "sha256:bbb",
		PreviousResolved: "sha256:aaa",
	}

	out, err := Check(context.Background(), gitx.New(), o, st, r, Previous)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !strings.Contains(out, "Mine.") {
		t.Errorf("diff missing the installed skill's own change:\n%s", out)
	}
	if strings.Contains(out, "Theirs.") {
		t.Errorf("diff reported a change outside the installed skill's subpath:\n%s", out)
	}
}

// twoSkillOCI serves two skills per revision, keyed by the digest the
// reference is pinned to, so a diff that failed to narrow to one skill's
// subpath would visibly report the other one's change too.
type twoSkillOCI map[string]map[string]string

func (t twoSkillOCI) Resolve(_ context.Context, ref string) (string, error) {
	if i := strings.Index(ref, "@"); i >= 0 {
		return ref[i+1:], nil
	}
	return "", errNoOCI
}

func (t twoSkillOCI) Pull(_ context.Context, ref, dest string) error {
	digest, err := t.Resolve(context.Background(), ref)
	if err != nil {
		return err
	}
	skills, ok := t[digest]
	if !ok {
		return errNoOCI
	}
	for name, body := range skills {
		dir := filepath.Join(dest, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (t twoSkillOCI) Push(context.Context, string, io.Reader) error { return errNoOCI }

// refusingOCI fails loudly if diff reaches for a registry during a
// git-only test.
type refusingOCI struct{}

func (refusingOCI) Resolve(context.Context, string) (string, error) { return "", errNoOCI }
func (refusingOCI) Pull(context.Context, string, string) error      { return errNoOCI }
func (refusingOCI) Push(context.Context, string, io.Reader) error   { return errNoOCI }

var errNoOCI = errors.New("this test did not expect an OCI call")

var _ ocix.OCI = refusingOCI{}
