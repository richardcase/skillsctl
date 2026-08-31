package channel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/cosignx"
	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/store"
	"github.com/richardcase/skillsctl/internal/target"
)

// fakeOCI is a fixed single-skill image at one digest, with a call counter
// so a test can assert Resolve is cheap. byRef answers per reference, for the
// tests about which reference was asked for; asked and pulled record every
// reference Resolve and Pull were called with, respectively.
//
// movedDigest and content simulate a tag moving between Resolve and Pull, the
// way a real registry can: if movedDigest is set, digest advances to it the
// instant Resolve returns, as though someone repointed the tag. Pull then
// answers like a real registry would — a digest-pinned ref (name@sha256:...)
// always serves the content recorded for that exact digest, while a
// tag-shaped ref serves whatever digest currently holds, moved or not. content
// maps a digest to the skill name Pull writes for it; a digest with no entry
// falls back to "alpha", so tests that don't care about the moved-tag
// scenario are unaffected by these fields' zero values.
type fakeOCI struct {
	digest      string
	movedDigest string
	content     map[string]string
	byRef       map[string]string
	asked       []string
	pulled      []string
	resolveHits int
}

func (f *fakeOCI) Resolve(_ context.Context, ref string) (string, error) {
	f.resolveHits++
	f.asked = append(f.asked, ref)
	d := f.digest
	if got, ok := f.byRef[ref]; ok {
		d = got
	}
	if f.movedDigest != "" {
		f.digest = f.movedDigest
	}
	return d, nil
}

func (f *fakeOCI) Pull(_ context.Context, ref, dest string) error {
	f.pulled = append(f.pulled, ref)

	digest := f.digest
	if i := strings.LastIndex(ref, "@"); i != -1 {
		digest = ref[i+1:]
	}

	name := "alpha"
	if n, ok := f.content[digest]; ok {
		name = n
	}

	dir := filepath.Join(dest, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body := fmt.Sprintf("---\nname: %s\ndescription: a skill\n---\n", name)
	return os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644)
}

func (f *fakeOCI) Push(context.Context, string, io.Reader) error { return nil }

func TestOCIPrepareFindsTheSkillAtTheResolvedDigest(t *testing.T) {
	st := store.New(t.TempDir())
	o := &fakeOCI{digest: "sha256:aaa"}
	c := NewOCI(st, o, &fakeCosign{})

	src, err := source.Parse("oci://ghcr.io/owner/skills:v1")
	if err != nil {
		t.Fatal(err)
	}

	cands, _, err := c.Prepare(context.Background(), Request{Source: src, All: true})
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

func TestOCIPreparePullsTheVerifiedDigestEvenIfTheTagMovesAfterResolve(t *testing.T) {
	st := store.New(t.TempDir())
	o := &fakeOCI{
		digest:      "sha256:aaa",
		movedDigest: "sha256:bbb",
		content:     map[string]string{"sha256:aaa": "alpha", "sha256:bbb": "mallory"},
	}
	c := NewOCI(st, o, &fakeCosign{})

	src, err := source.Parse("oci://ghcr.io/owner/skills:v1")
	if err != nil {
		t.Fatal(err)
	}

	cands, _, err := c.Prepare(context.Background(), Request{Source: src, All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 || cands[0].Name != "alpha" {
		t.Fatalf("Prepare() = %+v, want the verified digest's skill (alpha), not the moved tag's (mallory)", cands)
	}
	for _, ref := range o.pulled {
		if !strings.HasSuffix(ref, "@sha256:aaa") {
			t.Errorf("Pull was called with %q, want a reference pinned to the verified digest sha256:aaa", ref)
		}
	}
}

func TestOCIInstallRecordsAReceiptTrackingTheTag(t *testing.T) {
	st := store.New(t.TempDir())
	o := &fakeOCI{digest: "sha256:aaa"}
	c := NewOCI(st, o, &fakeCosign{})

	src, _ := source.Parse("oci://ghcr.io/owner/skills:v1")
	cands, _, err := c.Prepare(context.Background(), Request{Source: src, All: true})
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
	c := NewOCI(st, o, &fakeCosign{})

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

	var rec plan.Record
	for _, op := range p.Ops {
		if r, ok := op.(plan.Record); ok {
			rec = r
			break
		}
	}
	if rec.Receipt.Resolved != "sha256:aaa" {
		t.Errorf("recorded Resolved = %q, want the new digest", rec.Receipt.Resolved)
	}
	if rec.Receipt.PreviousResolved != "sha256:old" {
		t.Errorf("recorded PreviousResolved = %q, want the digest it moved from", rec.Receipt.PreviousResolved)
	}
}

func TestOCIUpdateRelinksToTheVerifiedDigestEvenIfTheTagMovesAfterResolve(t *testing.T) {
	st := store.New(t.TempDir())
	o := &fakeOCI{
		digest:      "sha256:aaa",
		movedDigest: "sha256:bbb",
		content:     map[string]string{"sha256:aaa": "alpha", "sha256:bbb": "mallory"},
	}
	c := NewOCI(st, o, &fakeCosign{})

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
	if len(verdicts) != 1 || verdicts[0].Status != StatusUpdated || verdicts[0].Latest != "sha256:aaa" {
		t.Fatalf("verdicts = %+v, want one StatusUpdated at the verified digest sha256:aaa", verdicts)
	}

	var rec plan.Record
	for _, op := range p.Ops {
		if r, ok := op.(plan.Record); ok {
			rec = r
			break
		}
	}
	if rec.Receipt.Resolved != "sha256:aaa" {
		t.Errorf("recorded Resolved = %q, want the verified digest sha256:aaa, not the moved tag's sha256:bbb", rec.Receipt.Resolved)
	}
	for _, ref := range o.pulled {
		if !strings.HasSuffix(ref, "@sha256:aaa") {
			t.Errorf("Pull was called with %q, want a reference pinned to the verified digest sha256:aaa", ref)
		}
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
	c := NewOCI(st, o, &fakeCosign{})

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
	c := NewOCI(store.New(t.TempDir()), &fakeOCI{}, &fakeCosign{})
	if c.Ownership() != StoreOwned {
		t.Errorf("Ownership() = %v, want StoreOwned", c.Ownership())
	}
}

// fakeCosign answers Verify/Signed/Sign for a test. A zero-value fakeCosign
// verifies successfully and reports every ref as unsigned, so tests that
// don't care about signing are unaffected by its presence.
type fakeCosign struct {
	verifyErr          error
	signed             bool
	signedErr          error
	verifyKeylessErr   error
	verified           []string
	verifiedKeyless    []string
	asked              []string
	lastVerifyIdentity string
	lastVerifyIssuer   string
}

func (f *fakeCosign) Verify(_ context.Context, ref, _ string) error {
	f.verified = append(f.verified, ref)
	return f.verifyErr
}

func (f *fakeCosign) Signed(_ context.Context, ref string) (bool, error) {
	f.asked = append(f.asked, ref)
	return f.signed, f.signedErr
}

func (f *fakeCosign) Sign(context.Context, string, string) error { return nil }

func (f *fakeCosign) SignKeyless(context.Context, string) error { return nil }

func (f *fakeCosign) VerifyKeyless(_ context.Context, ref, identity, issuer string) error {
	f.verifiedKeyless = append(f.verifiedKeyless, ref)
	f.lastVerifyIdentity = identity
	f.lastVerifyIssuer = issuer
	return f.verifyKeylessErr
}

func TestOCIPrepareVerifiesAgainstTheResolvedDigest(t *testing.T) {
	st := store.New(t.TempDir())
	o := &fakeOCI{digest: "sha256:aaa"}
	cs := &fakeCosign{}
	c := NewOCI(st, o, cs)

	src, _ := source.Parse("oci://ghcr.io/owner/skills:v1")
	_, warnings, err := c.Prepare(context.Background(), Request{Source: src, All: true, VerifyKey: "cosign.pub"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none for a successful verify", warnings)
	}
	if len(cs.verified) != 1 || cs.verified[0] != "ghcr.io/owner/skills@sha256:aaa" {
		t.Errorf("verified %v, want one call against the digest ref", cs.verified)
	}
}

func TestOCIPrepareFailsClosedOnABadSignature(t *testing.T) {
	st := store.New(t.TempDir())
	o := &fakeOCI{digest: "sha256:aaa"}
	cs := &fakeCosign{verifyErr: errors.New("no matching signatures")}
	c := NewOCI(st, o, cs)

	src, _ := source.Parse("oci://ghcr.io/owner/skills:v1")
	_, _, err := c.Prepare(context.Background(), Request{Source: src, All: true, VerifyKey: "cosign.pub"})
	if err == nil {
		t.Fatal("Prepare accepted a failing verification")
	}
	// store.Root is a plain exported field: st.Root is the same t.TempDir()
	// passed into store.New above.
	if _, statErr := os.Stat(filepath.Join(st.Root, "rev")); statErr == nil {
		t.Error("a failed verification must not extract the revision")
	}
}

func TestOCIPrepareVerifiesKeylessAgainstTheResolvedDigest(t *testing.T) {
	st := store.New(t.TempDir())
	o := &fakeOCI{digest: "sha256:aaa"}
	cs := &fakeCosign{}
	c := NewOCI(st, o, cs)

	src, _ := source.Parse("oci://ghcr.io/owner/skills:v1")
	_, warnings, err := c.Prepare(context.Background(), Request{
		Source: src, All: true,
		VerifyIdentity: "signer@example.com",
		VerifyIssuer:   "https://accounts.google.com",
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none for a successful keyless verify", warnings)
	}
	if len(cs.verifiedKeyless) != 1 || cs.verifiedKeyless[0] != "ghcr.io/owner/skills@sha256:aaa" {
		t.Errorf("verifiedKeyless = %v, want one call against the digest ref", cs.verifiedKeyless)
	}
	if cs.lastVerifyIdentity != "signer@example.com" {
		t.Errorf("VerifyKeyless identity = %q, want the requested identity", cs.lastVerifyIdentity)
	}
	if cs.lastVerifyIssuer != "https://accounts.google.com" {
		t.Errorf("VerifyKeyless issuer = %q, want the requested issuer", cs.lastVerifyIssuer)
	}
}

func TestOCIPrepareFailsClosedOnABadKeylessSignature(t *testing.T) {
	st := store.New(t.TempDir())
	o := &fakeOCI{digest: "sha256:aaa"}
	cs := &fakeCosign{verifyKeylessErr: errors.New("no matching signatures")}
	c := NewOCI(st, o, cs)

	src, _ := source.Parse("oci://ghcr.io/owner/skills:v1")
	_, _, err := c.Prepare(context.Background(), Request{
		Source: src, All: true,
		VerifyIdentity: "signer@example.com",
		VerifyIssuer:   "https://accounts.google.com",
	})
	if err == nil {
		t.Fatal("Prepare accepted a failing keyless verification")
	}
	if _, statErr := os.Stat(filepath.Join(st.Root, "rev")); statErr == nil {
		t.Error("a failed keyless verification must not extract the revision")
	}
}

func TestOCIPrepareWarnsWhenSignedButNotVerified(t *testing.T) {
	st := store.New(t.TempDir())
	o := &fakeOCI{digest: "sha256:aaa"}
	cs := &fakeCosign{signed: true}
	c := NewOCI(st, o, cs)

	src, _ := source.Parse("oci://ghcr.io/owner/skills:v1")
	_, warnings, err := c.Prepare(context.Background(), Request{Source: src, All: true})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "ghcr.io/owner/skills@sha256:aaa") {
		t.Errorf("warnings = %v, want one warning naming the digest ref", warnings)
	}
	if len(cs.asked) != 1 {
		t.Errorf("Signed asked %d times, want 1", len(cs.asked))
	}
}

func TestOCIPrepareStaysSilentWhenUnsigned(t *testing.T) {
	st := store.New(t.TempDir())
	o := &fakeOCI{digest: "sha256:aaa"}
	cs := &fakeCosign{signed: false}
	c := NewOCI(st, o, cs)

	src, _ := source.Parse("oci://ghcr.io/owner/skills:v1")
	_, warnings, err := c.Prepare(context.Background(), Request{Source: src, All: true})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none for an unsigned image", warnings)
	}
}

func TestOCIPrepareStaysSilentWhenSignedIsUnknown(t *testing.T) {
	st := store.New(t.TempDir())
	o := &fakeOCI{digest: "sha256:aaa"}
	cs := &fakeCosign{signedErr: cosignx.ErrNotFound}
	c := NewOCI(st, o, cs)

	src, _ := source.Parse("oci://ghcr.io/owner/skills:v1")
	_, warnings, err := c.Prepare(context.Background(), Request{Source: src, All: true})
	if err != nil {
		t.Fatalf("Prepare: %v (cosign missing must not fail an unrelated install)", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none when whether it is signed is unknown", warnings)
	}
}

func TestOCIRollbackSwapsBackToThePreviousDigest(t *testing.T) {
	st := store.New(t.TempDir())
	o := &fakeOCI{digest: "sha256:aaa"}
	c := NewOCI(st, o, &fakeCosign{})

	src, _ := source.Parse("oci://ghcr.io/owner/skills:v1")
	cands, _, err := c.Prepare(context.Background(), Request{Source: src, All: true})
	if err != nil {
		t.Fatal(err)
	}
	tgt := target.Target{Name: "claude", Dir: t.TempDir()}
	req := Request{Source: src, Targets: []target.Target{tgt}, All: true}
	_, receipts, _, err := c.Install(req, cands)
	if err != nil {
		t.Fatal(err)
	}
	r := receipts[0]

	// Move to a second digest, exactly as TestOCIUpdateRelinksWhenTheDigestMoved does.
	o.digest = "sha256:bbb"
	verdicts, p, err := c.Update(context.Background(), []*state.Receipt{&r}, UpdateOptions{})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(verdicts) != 1 || verdicts[0].Status != StatusUpdated {
		t.Fatalf("fixture setup: Update verdicts = %+v, want one StatusUpdated", verdicts)
	}
	for _, op := range p.Ops {
		if rec, ok := op.(plan.Record); ok {
			r = rec.Receipt
		}
	}
	if r.Resolved != "sha256:bbb" || r.PreviousResolved != "sha256:aaa" {
		t.Fatalf("fixture: receipt = %+v, want Resolved sha256:bbb and PreviousResolved sha256:aaa", r)
	}

	rp, v, err := c.Rollback(context.Background(), r, false)
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if v.Status != StatusUpdated {
		t.Errorf("Status = %q, want %q", v.Status, StatusUpdated)
	}
	if v.Latest != "sha256:aaa" {
		t.Errorf("Latest = %q, want the digest it rolled back to", v.Latest)
	}
	var rec plan.Record
	for _, op := range rp.Ops {
		if r, ok := op.(plan.Record); ok {
			rec = r
		}
	}
	if rec.Receipt.Resolved != "sha256:aaa" {
		t.Errorf("recorded Resolved = %q, want sha256:aaa", rec.Receipt.Resolved)
	}
	if rec.Receipt.PreviousResolved != "sha256:bbb" {
		t.Errorf("recorded PreviousResolved = %q, want the toggle to remember sha256:bbb", rec.Receipt.PreviousResolved)
	}
}

func TestOCIRollbackRefusesWithNothingToRollBackTo(t *testing.T) {
	st := store.New(t.TempDir())
	o := &fakeOCI{digest: "sha256:aaa"}
	c := NewOCI(st, o, &fakeCosign{})

	r := state.Receipt{Name: "demo", Channel: "oci", Source: "oci://ghcr.io/owner/skills:v1", Resolved: "sha256:aaa"}
	_, _, err := c.Rollback(context.Background(), r, false)
	if !errors.Is(err, ErrNothingToRollBackTo) {
		t.Errorf("Rollback error = %v, want ErrNothingToRollBackTo", err)
	}
}
