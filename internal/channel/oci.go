package channel

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/richardcase/skillsctl/internal/discover"
	"github.com/richardcase/skillsctl/internal/ocix"
	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/store"
)

// OCI installs skills packaged into an OCI artifact: an immutable revision
// directory per digest, and a symlink per agent — the same shape Git gives a
// sha, with a registry standing in for a repository.
type OCI struct {
	linked

	store *store.Store
	oci   ocix.OCI
}

// NewOCI returns the OCI channel backed by st and o.
func NewOCI(st *store.Store, o ocix.OCI) *OCI { return &OCI{store: st, oci: o} }

// Ownership reports that the store holds the files and the links undo them.
func (c *OCI) Ownership() Ownership { return StoreOwned }

// ociSourceOf re-reads the source a receipt was installed from. A receipt
// records the oci:// form precisely so this round trip exists: the bare
// registry/repository:tag a registry client wants is derived from it, never the
// other way about.
func ociSourceOf(r *state.Receipt) (source.Source, error) {
	src, err := source.Parse(r.Source)
	if err != nil {
		return source.Source{}, fmt.Errorf("this receipt records %q, which cannot be parsed as an oci source: %w", r.Source, err)
	}
	return src, nil
}

// Prepare resolves the tag to a digest, extracts the revision, and narrows
// the skills it found to the ones the request asked for.
func (c *OCI) Prepare(ctx context.Context, req Request) ([]Candidate, error) {
	src := req.Source

	ref := src.OCIRef(req.Ref)

	digest, err := c.oci.Resolve(ctx, ref)
	if err != nil {
		return nil, err
	}

	revRoot, err := c.store.EnsureOCI(ctx, c.oci, src.Slug(), ref, digest)
	if err != nil {
		return nil, err
	}
	// An artifact holds a tree of skills exactly as a repository does, so a
	// subpath narrows it the same way — which is also what lets a manifest
	// name one skill out of an artifact that ships several.
	revPath, err := store.Join(revRoot, src.Subpath)
	if err != nil {
		return nil, fmt.Errorf("refusing to install: %w", err)
	}

	found, err := discover.Walk(revPath)
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("%s: %w", revPath, discover.ErrNoSkill)
	}

	available, err := resolveNames(found, src.DefaultName())
	if err != nil {
		return nil, err
	}

	chosen, err := narrow(available, req)
	if err != nil {
		var amb *Ambiguous
		if errors.As(err, &amb) {
			amb.Header = fmt.Sprintf("skills in %s:", src.OCISource(req.Ref))
			amb.Meta = discover.PluginMeta(revPath)
			amb.Available = brief(available)
			amb.Resolved = digest
		}
		return nil, err
	}

	return c.candidates(chosen, revRoot, digest)
}

func (c *OCI) candidates(sels []selection, revRoot, digest string) ([]Candidate, error) {
	out := make([]Candidate, 0, len(sels))
	for _, s := range sels {
		hash, err := store.HashDir(s.skill.Dir)
		if err != nil {
			return nil, err
		}
		subpath, err := filepath.Rel(revRoot, s.skill.Dir)
		if err != nil {
			return nil, err
		}
		if subpath = filepath.ToSlash(subpath); subpath == "." {
			subpath = ""
		}
		out = append(out, Candidate{
			Name:    s.name,
			Desc:    s.skill.Description,
			Path:    s.skill.Dir,
			Subpath: subpath,
			Version: digest,
			Hash:    hash,
		})
	}
	return out, nil
}

// Install links each candidate into each target and records a receipt.
func (c *OCI) Install(req Request, chosen []Candidate) (plan.Plan, []state.Receipt, []string, error) {
	var p plan.Plan
	receipts := make([]state.Receipt, 0, len(chosen))
	now := time.Now().UTC()

	tag := req.Ref
	if tag == "" {
		tag = req.Source.Tag
	}
	// The receipt records the oci:// form, not the bare ref a registry client
	// takes: it is the string source.Parse round-trips, which is what lets
	// bundle write it into a manifest and sync install from it.
	src := req.Source.OCISource(tag)

	for _, s := range chosen {
		receipt := state.Receipt{
			Name:        s.Name,
			Channel:     string(source.ChannelOCI),
			Source:      src,
			Slug:        req.Source.Slug(),
			Subpath:     s.Subpath,
			Resolved:    s.Version,
			Pinned:      req.Pin,
			RevPath:     s.Path,
			ContentHash: s.Hash,
			InstalledAt: now,
			UpdatedAt:   now,
		}
		if !req.Pin {
			receipt.Ref = tag
		}

		for _, t := range req.Targets {
			linkPath, err := linkPathFor(t, s.Name)
			if err != nil {
				return p, nil, nil, err
			}
			p.Add(plan.Link{Target: t.Name, LinkPath: linkPath, RevPath: s.Path})
			receipt.Links = append(receipt.Links, state.Link{Target: t.Name, Path: linkPath})
		}
		p.Add(plan.Record{Receipt: receipt})
		receipts = append(receipts, receipt)
	}
	return p, receipts, nil, nil
}

// Update re-points each receipt at the current digest of the tag it tracks.
func (c *OCI) Update(ctx context.Context, rs []*state.Receipt, o UpdateOptions) ([]Verdict, plan.Plan, error) {
	seen := map[string]resolution{}
	verdicts := make([]Verdict, 0, len(rs))

	var p plan.Plan
	now := time.Now().UTC()

	for _, r := range rs {
		v := Verdict{Name: r.Name, Channel: r.Channel, Ref: r.Ref, Current: r.Resolved, Pinned: r.Pinned}

		if r.Pinned && !o.Named {
			v.Status = StatusPinned
			verdicts = append(verdicts, v)
			continue
		}

		// The tag to resolve against is the one the receipt tracks, which unpin
		// --ref can have moved away from the tag its source was installed at.
		src, err := ociSourceOf(r)
		if err != nil {
			verdicts = append(verdicts, fail(v, err))
			continue
		}
		ref := src.OCIRef(r.Ref)

		got, ok := seen[ref]
		if !ok {
			got.sha, got.err = c.oci.Resolve(ctx, ref)
			seen[ref] = got
		}
		if got.err != nil {
			verdicts = append(verdicts, fail(v, got.err))
			continue
		}

		v.Latest = got.sha
		if got.sha == r.Resolved {
			v.Status = StatusCurrent
			verdicts = append(verdicts, v)
			continue
		}

		dirty, note, err := inspect(r)
		if err != nil {
			verdicts = append(verdicts, fail(v, err))
			continue
		}
		if dirty && !o.Force {
			v.Status = StatusDirty
			verdicts = append(verdicts, v)
			continue
		}
		v.Note = note

		ops, receipt, err := c.relink(ctx, r, src, ref, got.sha, now)
		if err != nil {
			verdicts = append(verdicts, fail(v, err))
			continue
		}

		p.Add(ops...)
		p.Add(plan.Record{Receipt: receipt})
		v.Status = StatusUpdated
		verdicts = append(verdicts, v)
	}

	return verdicts, p, nil
}

// Settle has nothing to complete: the digest is known before the plan is
// built, and so is every path derived from it.
func (c *OCI) Settle(context.Context, []state.Receipt) ([]state.Receipt, error) {
	return nil, nil
}

func (c *OCI) relink(ctx context.Context, r *state.Receipt, src source.Source, ref, digest string, now time.Time) ([]plan.Op, state.Receipt, error) {
	slug := r.Slug
	if slug == "" {
		slug = src.Slug()
	}

	revRoot, err := c.store.EnsureOCI(ctx, c.oci, slug, ref, digest)
	if err != nil {
		return nil, state.Receipt{}, err
	}
	revPath, err := store.Join(revRoot, r.Subpath)
	if err != nil {
		return nil, state.Receipt{}, err
	}

	if _, err := discover.Root(revPath); err != nil {
		return nil, state.Receipt{}, fmt.Errorf("%s no longer holds a skill at %s: %w", ociShortDigest(digest), subpathOrRoot(r.Subpath), err)
	}

	hash, err := store.HashDir(revPath)
	if err != nil {
		return nil, state.Receipt{}, err
	}

	ops := make([]plan.Op, 0, len(r.Links))
	for _, l := range r.Links {
		ops = append(ops, plan.Relink{Target: l.Target, LinkPath: l.Path, RevPath: revPath})
	}

	receipt := *r
	receipt.Resolved = digest
	receipt.RevPath = revPath
	receipt.ContentHash = hash
	receipt.UpdatedAt = now
	return ops, receipt, nil
}

func ociShortDigest(digest string) string {
	if len(digest) > 17 {
		return digest[:17]
	}
	return digest
}
