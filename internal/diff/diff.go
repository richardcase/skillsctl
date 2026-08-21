// Package diff compares an installed skill's revision against another one —
// what update would move to, or what rollback would move back to — and
// returns the unified diff between them. Like outdated, it performs no
// mutation an install or update would: no receipt changes, no symlink
// changes, so this package produces no plan.Plan. Comparing against Latest is
// not purely read-only, though — it fetches into the local mirror cache to
// see the tracked ref's true upstream head, the same cache install and
// update populate.
package diff

import (
	"context"
	"fmt"

	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/richardcase/skillsctl/internal/ocix"
	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/store"
)

// Against selects which revision an installed skill is compared to.
type Against string

const (
	// Latest compares against what update would move to.
	Latest Against = "latest"
	// Previous compares against the revision Previous* recorded — what
	// rollback would move back to.
	Previous Against = "previous"
)

// Check diffs r's installed revision against Latest or Previous, and
// returns the unified diff. The result is "" when the two revisions are
// identical, which is what lets a caller print "no changes" without
// inspecting the text.
//
// Only the git and OCI channels have a revision history to diff; any other
// channel is refused by name, the same way outdated skips a local skill's
// ref column.
func Check(ctx context.Context, g gitx.Git, o ocix.OCI, st *store.Store, r *state.Receipt, against Against) (string, error) {
	toSha := r.PreviousResolved
	if against != Previous {
		latest, err := resolveLatest(ctx, g, o, r)
		if err != nil {
			return "", err
		}
		toSha = latest
	} else if toSha == "" {
		return "", fmt.Errorf("%s has never been updated: nothing to diff against its previous revision", r.Name)
	}

	if toSha == r.Resolved {
		return "", nil
	}

	switch r.Channel {
	case string(source.ChannelGit):
		slug, err := gitSlug(r)
		if err != nil {
			return "", err
		}
		mirror := st.MirrorPath(slug)
		// Previous's sha was already fetched when it was installed or
		// updated onto, so the mirror already holds it. Latest's sha was
		// only just read from the remote's refs and may not be: bring the
		// mirror's objects up to date before asking it for a diff.
		if against != Previous {
			if err := g.Mirror(ctx, r.Source, mirror); err != nil {
				return "", fmt.Errorf("update mirror for %s: %w", r.Name, err)
			}
		}
		// A skill installed from a subdirectory of a multi-skill repository
		// is only that subdirectory: diffing the whole revision would report
		// changes to skills the user never installed.
		if r.Subpath != "" {
			return g.Diff(ctx, mirror, r.Resolved, toSha, r.Subpath)
		}
		return g.Diff(ctx, mirror, r.Resolved, toSha)

	case string(source.ChannelOCI):
		src, err := ociSourceOf(r)
		if err != nil {
			return "", err
		}
		slug := r.Slug
		if slug == "" {
			slug = src.Slug()
		}
		fromRoot, err := st.EnsureOCI(ctx, o, slug, ociDigestRef(src, r.Resolved), r.Resolved)
		if err != nil {
			return "", err
		}
		toRoot, err := st.EnsureOCI(ctx, o, slug, ociDigestRef(src, toSha), toSha)
		if err != nil {
			return "", err
		}
		// The same narrowing the git branch does with a pathspec, and the
		// same containment check every other resolution of a subpath makes.
		fromDir, err := store.Join(fromRoot, r.Subpath)
		if err != nil {
			return "", err
		}
		toDir, err := store.Join(toRoot, r.Subpath)
		if err != nil {
			return "", err
		}
		return g.DiffDirs(ctx, fromDir, toDir)

	default:
		return "", fmt.Errorf("diff is not supported for the %s channel", r.Channel)
	}
}

// resolveLatest reads only, the same promise outdated makes: a git ref is
// read with ls-remote, an OCI tag's manifest is read without pulling a
// layer, and nothing is mirrored or extracted.
func resolveLatest(ctx context.Context, g gitx.Git, o ocix.OCI, r *state.Receipt) (string, error) {
	switch r.Channel {
	case string(source.ChannelGit):
		ref := r.Ref
		if ref == "" {
			ref = "HEAD"
		}
		return g.Resolve(ctx, r.Source, ref)

	case string(source.ChannelOCI):
		src, err := ociSourceOf(r)
		if err != nil {
			return "", err
		}
		return o.Resolve(ctx, src.OCIRef(r.Ref))

	default:
		return "", fmt.Errorf("diff is not supported for the %s channel", r.Channel)
	}
}

// gitSlug is where in the store this receipt's mirror lives. Receipts
// written before the slug was recorded fall back to deriving it from the
// source, the same fallback the git channel's relink uses.
func gitSlug(r *state.Receipt) (string, error) {
	if r.Slug != "" {
		return r.Slug, nil
	}
	src, err := source.Parse(r.Source)
	if err != nil {
		return "", fmt.Errorf("this receipt records no store location and %q cannot be parsed: %w", r.Source, err)
	}
	return src.Slug(), nil
}

// ociSourceOf re-reads the source a receipt was installed from. A receipt
// records the oci:// form precisely so this round trip exists: the bare
// registry/repository:tag a registry client wants is derived from it, never
// the other way about.
func ociSourceOf(r *state.Receipt) (source.Source, error) {
	src, err := source.Parse(r.Source)
	if err != nil {
		return source.Source{}, fmt.Errorf("this receipt records %q, which cannot be parsed as an oci source: %w", r.Source, err)
	}
	return src, nil
}

// ociDigestRef pins a reference to an exact digest rather than a tag: a tag
// can have moved since the receipt was written, and a diff that read
// through it could silently compare against neither revision it was asked
// for.
func ociDigestRef(src source.Source, digest string) string {
	return fmt.Sprintf("%s/%s@%s", src.Registry, src.Repository, digest)
}
