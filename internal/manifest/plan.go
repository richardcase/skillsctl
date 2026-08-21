package manifest

import (
	"context"
	"fmt"

	"github.com/richardcase/skillsctl/internal/channel"
	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/target"
)

// Status is what sync did about one entry, or could not do.
type Status string

const (
	// StatusInstalled means the entry was not installed here and now is.
	StatusInstalled Status = "installed"
	// StatusLinked means it was installed, and an agent it names now has it.
	StatusLinked Status = "linked"
	// StatusPresent means the entry was already satisfied.
	StatusPresent Status = "present"
	// StatusDiffers means a skill under this name is installed, but not as the
	// entry describes it. sync only ever adds, so the difference is reported.
	StatusDiffers Status = "differs"
	// StatusError means this entry could not be acted on.
	StatusError Status = "error"
)

// Verdict is the answer for one manifest entry.
type Verdict struct {
	Name string
	// Agents are the agents installed or linked, for the report.
	Agents []string
	// Detail says what differs, or why the entry failed.
	Detail string
	// Version is the resolved revision, when there is one to name.
	Version string
	// Skipped carries the reasons a channel could not do part of what it was
	// asked — a name another skill already holds costs one link, not the entry —
	// so they are reported beside a verdict that otherwise succeeded.
	Skipped []string
	Status  Status
}

// Report is what a sync found: one verdict per entry, in the file's order,
// plus the installed skills the manifest never mentioned.
//
// Extra sits beside the verdicts rather than among them because a skill the
// manifest does not name is not an entry and has no verdict.
type Report struct {
	Verdicts []Verdict
	Extra    []*state.Receipt
}

// Plan says what each entry needs and returns the ops that provide it.
//
// sync only ever adds: an entry that is not installed is installed, an entry
// whose agents are incomplete is linked, and an entry that disagrees with the
// receipt under its name is reported. Nothing here re-points a ref, moves a pin
// or removes anything, which is what makes running it twice change nothing the
// second time.
//
// One entry in, one verdict out, in the file's order. There is no error to
// return: everything that could fail the whole command — an unreadable file, a
// TOML error, a missing name, a version from the future — is Decode's job and
// has already happened. A single unreachable remote is one entry's error, and
// must not hide the rest of the report.
func Plan(ctx context.Context, reg channel.Registry, f File, db *state.DB, cfg target.Config) (Report, plan.Plan) {
	var p plan.Plan
	rep := Report{Verdicts: make([]Verdict, 0, len(f.Skills))}
	named := make(map[string]bool, len(f.Skills))

	for _, e := range f.Skills {
		named[e.Name] = true
		v, ops := planEntry(ctx, reg, e, db, cfg)
		p.Ops = append(p.Ops, ops.Ops...)
		rep.Verdicts = append(rep.Verdicts, v)
	}

	for _, r := range db.List() {
		if !named[r.Name] {
			rep.Extra = append(rep.Extra, r)
		}
	}
	return rep, p
}

// planEntry answers one entry, against the receipt under its name if there is
// one.
func planEntry(ctx context.Context, reg channel.Registry, e Entry, db *state.DB, cfg target.Config) (Verdict, plan.Plan) {
	targets, err := agentsFor(e, cfg)
	if err != nil {
		return errorVerdict(e, err), plan.Plan{}
	}
	if r, ok := db.Receipts[e.Name]; ok {
		return planInstalled(reg, e, r, targets)
	}
	return planMissing(ctx, reg, e, targets)
}

// planMissing installs an entry this machine does not have, through exactly the
// path install takes. Nothing about sync is a second way to install a skill.
func planMissing(ctx context.Context, reg channel.Registry, e Entry, targets []target.Target) (Verdict, plan.Plan) {
	src, err := e.Parse()
	if err != nil {
		return errorVerdict(e, err), plan.Plan{}
	}
	ch, err := reg.For(src.Channel)
	if err != nil {
		return errorVerdict(e, err), plan.Plan{}
	}

	req := channel.Request{Source: src, Targets: targets, Ref: e.Ref, Pin: e.Pinned}
	chosen, _, err := ch.Prepare(ctx, req)
	if err != nil {
		return errorVerdict(e, err), plan.Plan{}
	}
	// An entry names one skill. A source that resolves to several is a manifest
	// that cannot be acted on without guessing, and the remedy is a subpath.
	if len(chosen) != 1 {
		return errorVerdict(e, fmt.Errorf("names %d skills, and an entry installs one: give it a subpath", len(chosen))), plan.Plan{}
	}
	// The entry's name is the receipt key, so it wins over what SKILL.md says —
	// the same override --as applies at install time.
	chosen[0].Name = e.Name

	p, receipts, skips, err := ch.Install(req, chosen)
	if err != nil {
		return errorVerdict(e, err), plan.Plan{}
	}
	if len(e.Tags) > 0 {
		applyTags(&p, e.Tags)
	}

	v := Verdict{Name: e.Name, Status: StatusInstalled, Skipped: skips}
	if len(receipts) == 1 {
		// The pre-narrowing targets are not who received the skill: Prepare may
		// have narrowed further (a plugin to the agents that install plugins) and
		// Install may have linked nothing at all (a plugin links none). The
		// receipt's own channel is what actually knows who has it, exactly as
		// install.go asks per receipt rather than trusting the request.
		v.Agents = ch.Agents(receipts[0])
		v.Version = receipts[0].Resolved
	}
	return v, p
}

// planInstalled compares an entry against the receipt already under its name.
// The only mutation it will plan is a link, because sync only ever adds.
func planInstalled(reg channel.Registry, e Entry, r *state.Receipt, targets []target.Target) (Verdict, plan.Plan) {
	if d := differs(e, r); d != "" {
		return Verdict{Name: e.Name, Status: StatusDiffers, Detail: d, Version: r.Resolved}, plan.Plan{}
	}

	ch, err := reg.ForReceipt(r)
	if err != nil {
		return errorVerdict(e, err), plan.Plan{}
	}
	// Only the agents with no link at all are handed to Link, and that narrowing
	// is what keeps sync additive. Link is reconciliation now — for a plugin it
	// re-points a link claude has moved and unlinks a skill the plugin stopped
	// shipping — but it only ever touches the agents it is given, so an agent
	// that already holds this receipt is never reconsidered here.
	add := missingLinks(r, targets)
	if len(add) == 0 {
		return Verdict{Name: e.Name, Status: StatusPresent, Version: r.Resolved}, plan.Plan{}
	}

	p, skips, err := ch.Link(*r, add)
	if err != nil {
		return errorVerdict(e, err), plan.Plan{}
	}
	// An empty plan with nothing skipped means those agents needed nothing: a
	// plugin's own installing agent sees its skills without a symlink, so it is
	// always "missing" a link it must never be given.
	if p.IsEmpty() && len(skips) == 0 {
		return Verdict{Name: e.Name, Status: StatusPresent, Version: r.Resolved}, plan.Plan{}
	}
	return Verdict{
		Name:    e.Name,
		Status:  StatusLinked,
		Agents:  linkedTargets(p),
		Version: r.Resolved,
		Skipped: skips,
	}, p
}

// linkedTargets names the agents the plan actually links into, in the order it
// links them.
//
// The agents that were asked are not the answer: a plugin's installing agent is
// always missing a link it must never be given, and a plugin fans one link per
// skill it ships, so a target can appear many times. Reading the ops is the only
// account that matches what the user will see on disk.
func linkedTargets(p plan.Plan) []string {
	var out []string
	seen := map[string]bool{}

	for _, op := range p.Ops {
		var name string
		switch o := op.(type) {
		case plan.Link:
			name = o.Target
		case plan.Relink:
			name = o.Target
		default:
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// differs says how the receipt under this name disagrees with the entry, or ""
// when it does not.
//
// The channel and the source come first: a name pointing at different files is
// the sharpest disagreement there is, and saying "the ref differs" about an
// entirely different skill would be misleading. Shas are reported in full,
// because the remedy for a pin difference is editing the manifest and a
// seven-character prefix is not what anybody pastes into one.
func differs(e Entry, r *state.Receipt) string {
	src, err := e.Parse()
	if err != nil {
		return fmt.Sprintf("its source cannot be parsed: %v", err)
	}
	if string(src.Channel) != r.Channel {
		return fmt.Sprintf("the manifest installs it through the %s channel, the receipt through %s", src.Channel, r.Channel)
	}
	if want := canonicalSource(src); want != "" && want != r.Source {
		return fmt.Sprintf("the manifest installs it from %s, the receipt from %s", want, r.Source)
	}
	if src.Subpath != r.Subpath {
		return fmt.Sprintf("the manifest names subpath %q, the receipt %q", src.Subpath, r.Subpath)
	}

	switch {
	case e.Pinned && !r.Pinned:
		return fmt.Sprintf("the manifest pins it at %s, the install tracks %s", e.Ref, describeRef(r.Ref))
	case !e.Pinned && r.Pinned:
		return fmt.Sprintf("the manifest tracks %s, the install is pinned at %s", describeRef(e.Ref), r.Resolved)
	case e.Pinned && r.Pinned && e.Ref != r.Resolved:
		return fmt.Sprintf("the manifest pins it at %s, the install at %s", e.Ref, r.Resolved)
	case !e.Pinned && !r.Pinned && e.Ref != r.Ref:
		return fmt.Sprintf("the manifest tracks %s, the install tracks %s", describeRef(e.Ref), describeRef(r.Ref))
	}
	return ""
}

// canonicalSource renders what a receipt installed from src would record in its
// Source field, so an entry can be compared with one.
//
// Local is the exception: its receipt records an absolute path resolved against
// the working directory, which only the local channel can produce, so a local
// entry is compared no further than its channel.
func canonicalSource(src source.Source) string {
	switch src.Channel {
	case source.ChannelGit:
		return src.RepoURL
	case source.ChannelPlugin:
		return src.Plugin + "@" + src.Marketplace
	case source.ChannelOCI:
		// The tag is part of an OCI source, so the whole oci:// string is the
		// comparison — which is also exactly what Install records.
		return src.OCISource("")
	default:
		return ""
	}
}

// describeRef names what something tracks, in the words pin and update already
// use for an empty ref.
func describeRef(ref string) string {
	if ref == "" {
		return "the repository's default branch"
	}
	return ref
}

// agentsFor resolves the agents an entry names, or the default set when it
// names none, by the same rule install -a resolves by.
func agentsFor(e Entry, cfg target.Config) ([]target.Target, error) {
	return cfg.Resolve(e.Agents)
}

// missingLinks returns the targets an entry names that the receipt does not
// already record. Links is a set keyed by target, so one already there is never
// a second link.
func missingLinks(r *state.Receipt, targets []target.Target) []target.Target {
	held := make(map[string]bool, len(r.Links))
	for _, l := range r.Links {
		held[l.Target] = true
	}

	var add []target.Target
	for _, t := range targets {
		if !held[t.Name] {
			add = append(add, t)
		}
	}
	return add
}

func errorVerdict(e Entry, err error) Verdict {
	return Verdict{Name: e.Name, Status: StatusError, Detail: err.Error()}
}

// applyTags stamps tags onto every Record op an install just planned. Tags
// are manifest metadata with no channel-specific meaning, so this stays
// entirely inside the manifest package rather than teaching every channel
// about a field install itself never sets.
func applyTags(p *plan.Plan, tags []string) {
	for i, op := range p.Ops {
		rec, ok := op.(plan.Record)
		if !ok {
			continue
		}
		rec.Receipt.Tags = tags
		p.Ops[i] = rec
	}
}
