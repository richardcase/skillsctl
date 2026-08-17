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
// so a test can assert Resolve is cheap. byRef answers per reference, for the
// tests about which reference was asked for; asked records them all.
type fakeOCI struct {
	digest      string
	byRef       map[string]string
	asked       []string
	resolveHits int
}

func (f *fakeOCI) Resolve(_ context.Context, ref string) (string, error) {
	f.resolveHits++
	f.asked = append(f.asked, ref)
	if d, ok := f.byRef[ref]; ok {
		return d, nil
	}
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
	// The scheme is the whole point: a bare registry/repo:tag parses back as
	// the owner/repo git shorthand, so bundle would write a manifest sync
	// installs from github.
	if r.Source != "oci://ghcr.io/owner/skills:v1" {
		t.Errorf("Source = %q, want the oci:// form the user typed", r.Source)
	}
	back, err := source.Parse(r.Source)
	if err != nil {
		t.Fatalf("a receipt's Source must round-trip through Parse: %v", err)
	}
	if back.Channel != source.ChannelOCI {
		t.Errorf("Source %q parses back as the %s channel", r.Source, back.Channel)
	}
}

func TestOCIUpdateRelinksWhenTheDigestMoved(t *testing.T) {
	st := store.New(t.TempDir())
	o := &fakeOCI{digest: "sha256:aaa"}
	c := NewOCI(st, o)

	r := &state.Receipt{
		Name: "alpha", Channel: "oci", Source: "oci://ghcr.io/owner/skills:v1",
		Slug: "oci/ghcr.io/owner/skills", Ref: "v1", Resolved: "sha256:old",
		Subpath: "alpha",
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

func TestOCIUpdateResolvesTheTagTheReceiptTracks(t *testing.T) {
	// unpin --ref moves the tag a receipt follows, and the tag baked into its
	// source is not that tag. Reading the source's tag instead would silently
	// ignore the ref the user chose.
	st := store.New(t.TempDir())
	o := &fakeOCI{
		digest: "sha256:wrong",
		byRef:  map[string]string{"ghcr.io/owner/skills:v2": "sha256:right"},
	}
	c := NewOCI(st, o)

	r := &state.Receipt{
		Name: "alpha", Channel: "oci", Source: "oci://ghcr.io/owner/skills:v1",
		Slug: "oci/ghcr.io/owner/skills", Ref: "v2", Resolved: "sha256:old",
		Subpath: "alpha",
		RevPath: filepath.Join(t.TempDir(), "gone"),
	}

	verdicts, _, err := c.Update(context.Background(), []*state.Receipt{r}, UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(o.asked) != 1 || o.asked[0] != "ghcr.io/owner/skills:v2" {
		t.Fatalf("resolved %v, want the bare ref at the tracked tag v2", o.asked)
	}
	if len(verdicts) != 1 || verdicts[0].Latest != "sha256:right" {
		t.Errorf("verdicts = %+v, want the digest of v2", verdicts)
	}
}

func TestOCIOwnershipIsStoreOwned(t *testing.T) {
	c := NewOCI(store.New(t.TempDir()), &fakeOCI{})
	if c.Ownership() != StoreOwned {
		t.Errorf("Ownership() = %v, want StoreOwned", c.Ownership())
	}
}
