package diff

import (
	"context"
	"errors"
	"io"
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
	st, g, r, _, second := gitFixture(t)

	out, err := Check(context.Background(), g, refusingOCI{}, st, r, Latest)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !strings.Contains(out, "More.") {
		t.Errorf("diff missing the change introduced by the second commit:\n%s", out)
	}
	_ = second
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

// refusingOCI fails loudly if diff reaches for a registry during a
// git-only test.
type refusingOCI struct{}

func (refusingOCI) Resolve(context.Context, string) (string, error) { return "", errNoOCI }
func (refusingOCI) Pull(context.Context, string, string) error      { return errNoOCI }
func (refusingOCI) Push(context.Context, string, io.Reader) error   { return errNoOCI }

var errNoOCI = errors.New("this test did not expect an OCI call")

var _ ocix.OCI = refusingOCI{}
