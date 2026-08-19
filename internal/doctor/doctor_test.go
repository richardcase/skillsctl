package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/store"
	"github.com/richardcase/skillsctl/internal/target"
)

const sha = "abcdef0123456789abcdef0123456789abcdef01"

// fixture is a healthy installation: one git skill extracted into the store and
// linked into two agents, with a receipt that matches. Every test damages it in
// one way and asserts on what the scan says.
type fixture struct {
	t   *testing.T
	st  *store.Store
	db  *state.DB
	ts  []target.Target
	rev string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	root := t.TempDir()
	agents := t.TempDir()
	st := store.New(root)

	rev := st.RevPath("example.com/o/repo", sha)
	mkdir(t, rev)
	write(t, filepath.Join(rev, "SKILL.md"), "---\nname: demo\ndescription: A demo\n---\n")

	hash, err := store.HashDir(rev)
	if err != nil {
		t.Fatalf("hash %s: %v", rev, err)
	}

	ts := []target.Target{
		{Name: "claude", Dir: filepath.Join(agents, "claude", "skills")},
		{Name: "codex", Dir: filepath.Join(agents, "codex", "skills")},
	}
	links := make([]state.Link, 0, len(ts))
	for _, tg := range ts {
		mkdir(t, tg.Dir)
		link := filepath.Join(tg.Dir, "demo")
		if err := os.Symlink(rev, link); err != nil {
			t.Fatal(err)
		}
		links = append(links, state.Link{Target: tg.Name, Path: link})
	}

	db := &state.DB{
		Version: state.SchemaVersion,
		Receipts: map[string]*state.Receipt{
			"demo": {
				Name:        "demo",
				Channel:     "git",
				Source:      "https://example.com/o/repo",
				Slug:        "example.com/o/repo",
				Resolved:    sha,
				RevPath:     rev,
				ContentHash: hash,
				Links:       links,
			},
		},
	}
	return &fixture{t: t, st: st, db: db, ts: ts, rev: rev}
}

// live is the root set the CLI computes with (*env).liveRoots, reduced to what
// this package needs: every receipt here is store-owned unless a test says
// otherwise by leaving RevPath empty.
func (f *fixture) live() store.Live {
	var live store.Live
	for _, r := range f.db.List() {
		if r.RevPath == "" {
			continue
		}
		live.RevPaths = append(live.RevPaths, r.RevPath)
		live.Slugs = append(live.Slugs, r.Slug)
	}
	return live
}

func (f *fixture) scan() Report {
	f.t.Helper()
	rep, err := Scan(f.ts, f.db, f.st, f.live())
	if err != nil {
		f.t.Fatalf("scan: %v", err)
	}
	return rep
}

// link is the path of the demo link in the named agent.
func (f *fixture) link(agent string) string {
	f.t.Helper()
	for _, t := range f.ts {
		if t.Name == agent {
			return filepath.Join(t.Dir, "demo")
		}
	}
	f.t.Fatalf("no target named %q", agent)
	return ""
}

func mkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// only asserts the report holds exactly one finding, of the given kind.
func only(t *testing.T, rep Report, kind Kind) Finding {
	t.Helper()
	if len(rep.Findings) != 1 {
		t.Fatalf("got %d findings, want 1 of kind %q: %+v", len(rep.Findings), kind, rep.Findings)
	}
	if rep.Findings[0].Kind != kind {
		t.Fatalf("kind = %q, want %q: %+v", rep.Findings[0].Kind, kind, rep.Findings[0])
	}
	return rep.Findings[0]
}

func kinds(rep Report) []Kind {
	out := make([]Kind, 0, len(rep.Findings))
	for _, f := range rep.Findings {
		out = append(out, f.Kind)
	}
	return out
}

func has(rep Report, kind Kind) bool {
	for _, f := range rep.Findings {
		if f.Kind == kind {
			return true
		}
	}
	return false
}

func TestHealthyInstallationHasNothingToReport(t *testing.T) {
	f := newFixture(t)

	rep := f.scan()
	if !rep.IsEmpty() {
		t.Errorf("a healthy store should be clean, got %v", kinds(rep))
	}
	if len(rep.Unscanned) != 0 {
		t.Errorf("unscanned = %+v, want none", rep.Unscanned)
	}
}

func TestALinkDeletedByHandIsAMissingLink(t *testing.T) {
	f := newFixture(t)
	if err := os.Remove(f.link("codex")); err != nil {
		t.Fatal(err)
	}

	got := only(t, f.scan(), KindMissingLink)
	if got.Name != "demo" || got.Target != "codex" {
		t.Errorf("finding = %+v, want demo/codex", got)
	}
	if !strings.Contains(got.Remedy, "skillsctl link demo -a codex") {
		t.Errorf("remedy = %q, should name the command that puts the link back", got.Remedy)
	}
}

func TestALinkPointingAtNothingIsDangling(t *testing.T) {
	f := newFixture(t)
	if err := os.RemoveAll(f.rev); err != nil {
		t.Fatal(err)
	}

	// Both links dangle, and the revision the receipt names is gone too. The
	// dangling links are reported once each, and the missing revision once.
	rep := f.scan()
	if n := len(rep.Findings); n != 3 {
		t.Fatalf("got %d findings, want 2 dangling links and 1 missing revision: %v", n, kinds(rep))
	}
	if !has(rep, KindDanglingLink) || !has(rep, KindMissingRevision) {
		t.Errorf("kinds = %v", kinds(rep))
	}
	// A revision that is gone takes the files with it, so there is nothing to
	// compare a hash against and drift must not be claimed as well.
	if has(rep, KindContentDrift) {
		t.Errorf("a missing revision is not content drift: %v", kinds(rep))
	}
}

func TestARealDirectoryWhereALinkShouldBeIsReported(t *testing.T) {
	f := newFixture(t)
	link := f.link("claude")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	mkdir(t, link)
	write(t, filepath.Join(link, "SKILL.md"), "---\nname: demo\n---\n")

	rep := f.scan()
	if !has(rep, KindNotASymlink) {
		t.Fatalf("kinds = %v, want a not-a-symlink finding", kinds(rep))
	}
	// The two agents now hold different content under one name.
	if !has(rep, KindNameCollision) {
		t.Errorf("kinds = %v, want the collision reported too", kinds(rep))
	}
}

func TestALinkRepointedElsewhereIsAWrongTarget(t *testing.T) {
	f := newFixture(t)

	other := f.st.RevPath("example.com/o/other", strings.Repeat("bc", 20))
	mkdir(t, other)
	write(t, filepath.Join(other, "SKILL.md"), "---\nname: demo\n---\n")

	link := f.link("codex")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, link); err != nil {
		t.Fatal(err)
	}

	rep := f.scan()
	if !has(rep, KindWrongTarget) {
		t.Fatalf("kinds = %v, want a wrong-target finding", kinds(rep))
	}
	// The revision it was repointed at is not referenced by any receipt.
	if !has(rep, KindOrphanRevision) {
		t.Errorf("kinds = %v, want the unreferenced revision reported", kinds(rep))
	}
}

func TestARevisionMissingFromTheStoreIsReported(t *testing.T) {
	f := newFixture(t)
	f.db.Receipts["demo"].RevPath = filepath.Join(f.st.Root, "rev", "example.com", "o", "repo", "gone")

	rep := f.scan()
	if !has(rep, KindMissingRevision) {
		t.Fatalf("kinds = %v, want a missing-revision finding", kinds(rep))
	}
	// The links still resolve, but no longer to what the receipt records.
	if !has(rep, KindWrongTarget) {
		t.Errorf("kinds = %v, want the links reported as pointing elsewhere", kinds(rep))
	}
}

// A local skill lives in a directory of the user's own. Nothing can fetch that
// back, so it is a different finding from a revision missing from the store.
func TestALocalSkillWhoseDirectoryIsGoneIsAMissingSource(t *testing.T) {
	f := newFixture(t)

	src := filepath.Join(t.TempDir(), "in-progress")
	mkdir(t, src)
	r := f.db.Receipts["demo"]
	r.Channel = "local"
	r.Slug = ""
	r.Resolved = ""
	r.ContentHash = ""
	r.RevPath = src
	for _, l := range r.Links {
		if err := os.Remove(l.Path); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(src, l.Path); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.RemoveAll(src); err != nil {
		t.Fatal(err)
	}

	rep := f.scan()
	if !has(rep, KindMissingSource) {
		t.Fatalf("kinds = %v, want a missing-source finding", kinds(rep))
	}
	if has(rep, KindMissingRevision) {
		t.Errorf("a directory outside the store is not a revision: %v", kinds(rep))
	}
	for _, got := range rep.Findings {
		if got.Kind == KindMissingSource && strings.Contains(got.Remedy, "skillsctl update") {
			t.Errorf("remedy = %q, but update cannot bring back a directory somebody deleted", got.Remedy)
		}
	}
}

func TestASkillEditedThroughTheSymlinkIsDrift(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.rev, "SKILL.md"), "---\nname: demo\n---\n\nEdited in place.\n")

	got := only(t, f.scan(), KindContentDrift)
	if got.Name != "demo" {
		t.Errorf("finding = %+v, want demo", got)
	}
	if !strings.Contains(got.Remedy, "skillsctl gc") {
		t.Errorf("remedy = %q, should collect the edited revision before reinstalling", got.Remedy)
	}
}

func TestAReceiptWithNoContentHashIsNeverDrift(t *testing.T) {
	f := newFixture(t)
	f.db.Receipts["demo"].ContentHash = ""
	write(t, filepath.Join(f.rev, "SKILL.md"), "---\nname: demo\n---\n\nEdited in place.\n")

	// A receipt written before the field existed, or by hand, has nothing to
	// compare against. Claiming drift would leave it permanently unhealthy.
	if rep := f.scan(); !rep.IsEmpty() {
		t.Errorf("got %v, want nothing", kinds(rep))
	}
}

func TestARevisionNoReceiptReferencesIsAnOrphan(t *testing.T) {
	f := newFixture(t)
	stray := f.st.RevPath("example.com/o/stray", strings.Repeat("cd", 20))
	mkdir(t, stray)
	write(t, filepath.Join(stray, "SKILL.md"), "---\nname: stray\n---\n")

	got := only(t, f.scan(), KindOrphanRevision)
	if !strings.Contains(got.Path, "stray") {
		t.Errorf("path = %q, want the stray revision", got.Path)
	}
	if got.Bytes == 0 {
		t.Errorf("finding = %+v, want the size that would be reclaimed", got)
	}
	if !strings.Contains(got.Remedy, "skillsctl gc") {
		t.Errorf("remedy = %q, want gc", got.Remedy)
	}
}

func TestALinkIntoTheStoreNoReceiptClaimsIsAnOrphan(t *testing.T) {
	f := newFixture(t)

	// A second name pointing into the same live revision: the revision stays
	// live, so gc says nothing, and only the link is wrong.
	if err := os.Symlink(f.rev, filepath.Join(f.ts[0].Dir, "hand-made")); err != nil {
		t.Fatal(err)
	}

	got := only(t, f.scan(), KindOrphanLink)
	if got.Name != "hand-made" || got.Target != "claude" {
		t.Errorf("finding = %+v, want hand-made/claude", got)
	}
}

func TestOneNameResolvingTwoWaysIsACollision(t *testing.T) {
	f := newFixture(t)

	other := t.TempDir()
	write(t, filepath.Join(other, "SKILL.md"), "---\nname: demo\n---\n")
	link := f.link("codex")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, link); err != nil {
		t.Fatal(err)
	}

	rep := f.scan()
	if !has(rep, KindNameCollision) {
		t.Fatalf("kinds = %v, want a name-collision finding", kinds(rep))
	}
	for _, got := range rep.Findings {
		if got.Kind != KindNameCollision {
			continue
		}
		if got.Name != "demo" {
			t.Errorf("finding = %+v, want demo", got)
		}
		// Both sides have to be named, or there is no way to tell which is wrong.
		if !strings.Contains(got.Detail, "claude") || !strings.Contains(got.Detail, "codex") {
			t.Errorf("detail = %q, should name both agents", got.Detail)
		}
	}
}

func TestOneNameLinkedTwiceAtTheSameThingIsNotACollision(t *testing.T) {
	f := newFixture(t)

	// The ordinary managed case: one skill, two agents, one destination.
	if rep := f.scan(); has(rep, KindNameCollision) {
		t.Errorf("two links to one revision is not a collision: %v", kinds(rep))
	}
}

func TestAPluginReceiptIsNeverUnhealthy(t *testing.T) {
	f := newFixture(t)

	// A plugin's files belong to the agent: it has no links, nothing in the
	// store and no hash, so every check has to be a no-op by construction
	// rather than by a channel special case.
	f.db.Receipts["a-plugin"] = &state.Receipt{
		Name:     "a-plugin",
		Channel:  "plugin",
		Source:   "pack@marketplace",
		Resolved: "1.2.0",
	}

	if rep := f.scan(); !rep.IsEmpty() {
		t.Errorf("got %v, want nothing", kinds(rep))
	}
}

func TestADotEntryIsIgnored(t *testing.T) {
	f := newFixture(t)
	// codex keeps a .system directory in its skills directory.
	mkdir(t, filepath.Join(f.ts[1].Dir, ".system"))

	if rep := f.scan(); !rep.IsEmpty() {
		t.Errorf("got %v, want the agent's own bookkeeping ignored", kinds(rep))
	}
}

func TestAnUnreadableAgentDirectoryDoesNotStopTheScan(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions, so nothing can be made unreadable")
	}
	f := newFixture(t)

	// Damage claude, then make codex unreadable: the report has to carry both
	// the finding and the fact that it is incomplete.
	if err := os.Remove(f.link("claude")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(f.ts[1].Dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(f.ts[1].Dir, 0o755) })

	rep := f.scan()
	if len(rep.Unscanned) != 1 || rep.Unscanned[0].Target != "codex" {
		t.Fatalf("unscanned = %+v, want codex", rep.Unscanned)
	}
	if !has(rep, KindMissingLink) {
		t.Errorf("kinds = %v, want claude still scanned", kinds(rep))
	}
}

func TestAnAgentThatHasNeverInstalledAnythingIsNotAFinding(t *testing.T) {
	f := newFixture(t)
	f.ts = append(f.ts, target.Target{Name: "gemini", Dir: filepath.Join(t.TempDir(), "never-created")})

	rep := f.scan()
	if !rep.IsEmpty() || len(rep.Unscanned) != 0 {
		t.Errorf("findings = %v, unscanned = %+v, want neither", kinds(rep), rep.Unscanned)
	}
}

func TestGroupsCollectFindingsUnderOneRemedy(t *testing.T) {
	f := newFixture(t)
	for _, agent := range []string{"claude", "codex"} {
		if err := os.Remove(f.link(agent)); err != nil {
			t.Fatal(err)
		}
	}

	groups := f.scan().Groups()
	if len(groups) != 1 {
		t.Fatalf("got %d groups, want 1: %+v", len(groups), groups)
	}
	if groups[0].Kind != KindMissingLink || len(groups[0].Findings) != 2 {
		t.Errorf("group = %+v, want both missing links under one kind", groups[0])
	}
	if groups[0].Title == "" || len(groups[0].Remedies) == 0 {
		t.Errorf("group = %+v, want a title and a remedy", groups[0])
	}
}

// No remedy may name `skillsctl update`. Update moves a skill to the head of
// the ref it tracks and stops at "current" when the ref has not moved, which is
// the usual state of a skill whose link somebody deleted — it repairs nothing
// there, not even with --force. A remedy that named it would send the user
// round a loop that leaves doctor reporting the same thing.
func TestNoRemedyNamesUpdate(t *testing.T) {
	for _, k := range order {
		got := remedy(k, "demo", "owner/repo", []string{"claude"})
		if got == "" {
			t.Errorf("%s has no remedy; every finding must name how to repair it", k)
		}
		if strings.Contains(got, "skillsctl update") {
			t.Errorf("%s remedy = %q, but update does nothing when the ref has not moved", k, got)
		}
	}
}

// A skill missing from two agents is one repair, not two.
func TestOneRemedyPerSkillMergesTheAgents(t *testing.T) {
	f := newFixture(t)
	for _, agent := range []string{"claude", "codex"} {
		if err := os.Remove(f.link(agent)); err != nil {
			t.Fatal(err)
		}
	}

	groups := f.scan().Groups()
	if len(groups) != 1 || len(groups[0].Remedies) != 1 {
		t.Fatalf("groups = %+v, want one group with one command", groups)
	}
	if !strings.Contains(groups[0].Remedies[0], "-a claude,codex") {
		t.Errorf("remedy = %q, want both agents in one command", groups[0].Remedies[0])
	}
}

// A reinstall has to say what to install from, and only the receipt knows.
func TestAReinstallRemedyNamesTheSource(t *testing.T) {
	f := newFixture(t)
	if err := os.RemoveAll(f.rev); err != nil {
		t.Fatal(err)
	}

	for _, got := range f.scan().Findings {
		if got.Kind != KindMissingRevision && got.Kind != KindDanglingLink {
			continue
		}
		if !strings.Contains(got.Remedy, "skillsctl install https://example.com/o/repo") {
			t.Errorf("%s remedy = %q, want the receipt's source named", got.Kind, got.Remedy)
		}
	}
}

func TestSkillsCountsDistinctNames(t *testing.T) {
	f := newFixture(t)
	for _, agent := range []string{"claude", "codex"} {
		if err := os.Remove(f.link(agent)); err != nil {
			t.Fatal(err)
		}
	}

	rep := f.scan()
	if n := rep.Skills(); n != 1 {
		t.Errorf("Skills() = %d, want 1: two links, one skill", n)
	}
}

func TestCosignMissingFromPathIsAWarningNotAFinding(t *testing.T) {
	f := newFixture(t)
	t.Setenv("PATH", t.TempDir())

	rep := f.scan()
	if !rep.IsEmpty() {
		t.Fatalf("a missing cosign is not a finding: %+v", rep.Findings)
	}
	if len(rep.Warnings) != 1 || rep.Warnings[0] != cosignWarning {
		t.Fatalf("warnings = %v, want one warning about cosign", rep.Warnings)
	}
}

func TestCosignOnPathAddsNoWarning(t *testing.T) {
	f := newFixture(t)
	bin := t.TempDir()
	write(t, filepath.Join(bin, "cosign"), "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(filepath.Join(bin, "cosign"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	rep := f.scan()
	if len(rep.Warnings) != 0 {
		t.Errorf("warnings = %v, want none when cosign is on PATH", rep.Warnings)
	}
}

func TestScanChangesNothing(t *testing.T) {
	f := newFixture(t)
	// Damage of every kind the scan can reach, so nothing is left unexercised.
	if err := os.Remove(f.link("codex")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(f.rev, "SKILL.md"), "edited\n")
	stray := f.st.RevPath("example.com/o/stray", strings.Repeat("cd", 20))
	mkdir(t, stray)
	write(t, filepath.Join(stray, "SKILL.md"), "---\nname: stray\n---\n")

	before := tree(t, f.st.Root)
	if rep := f.scan(); rep.IsEmpty() {
		t.Fatal("the fixture is damaged; the scan should have found something")
	}
	if after := tree(t, f.st.Root); after != before {
		t.Errorf("doctor changed the store:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// tree renders every path under root with its size, so a test can assert that
// nothing moved, grew or vanished.
func tree(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		fmt.Fprintf(&b, "%s %s %d\n", rel, info.Mode(), info.Size())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}
