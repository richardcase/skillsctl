// Package ocix wraps an OCI registry client. Using go-containerregistry
// rather than shelling out to docker is deliberate: it still reads Docker's
// own config and credential helpers for auth, so nothing here reimplements
// login, but the calls are typed and need no binary on PATH.
package ocix

import (
	"context"
	"fmt"
	"io"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/stream"
	"github.com/google/go-containerregistry/pkg/v1/types"

	"github.com/richardcase/skillsctl/internal/gitx"
)

// LayerMediaType identifies a skillsctl skills layer, so a registry that
// distinguishes artifact types does not mistake it for a runnable container
// layer.
const LayerMediaType types.MediaType = "application/vnd.skillsctl.skills.layer.v1.tar"

// OCI is the set of registry operations skillsctl needs to package and
// install skills as an OCI artifact.
type OCI interface {
	// Resolve returns the digest ref currently points at. It fetches only
	// the manifest, never a layer — the same "no fetch" cost as git's
	// ls-remote.
	Resolve(ctx context.Context, ref string) (string, error)
	// Pull extracts the single skills layer at ref into dest.
	Pull(ctx context.Context, ref, dest string) error
	// Push builds a one-layer artifact from the uncompressed tar stream r
	// and writes it to ref.
	Push(ctx context.Context, ref string, r io.Reader) error
}

// Registry implements OCI against a real registry.
type Registry struct{}

// New returns a Registry authenticated through Docker's default keychain.
func New() Registry { return Registry{} }

// Resolve returns the digest ref currently points at.
func (Registry) Resolve(ctx context.Context, ref string) (string, error) {
	r, err := name.ParseReference(ref)
	if err != nil {
		return "", fmt.Errorf("parse %q: %w", ref, err)
	}
	desc, err := remote.Get(r, remote.WithContext(ctx), remote.WithAuthFromKeychain(authn.DefaultKeychain))
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", ref, err)
	}
	return desc.Digest.String(), nil
}

// Pull extracts the single skills layer at ref into dest.
func (Registry) Pull(ctx context.Context, ref, dest string) error {
	r, err := name.ParseReference(ref)
	if err != nil {
		return fmt.Errorf("parse %q: %w", ref, err)
	}
	desc, err := remote.Get(r, remote.WithContext(ctx), remote.WithAuthFromKeychain(authn.DefaultKeychain))
	if err != nil {
		return fmt.Errorf("resolve %s: %w", ref, err)
	}
	img, err := desc.Image()
	if err != nil {
		return fmt.Errorf("read image %s: %w", ref, err)
	}
	layers, err := img.Layers()
	if err != nil {
		return fmt.Errorf("read layers of %s: %w", ref, err)
	}
	if len(layers) != 1 {
		return fmt.Errorf("%s holds %d layers, expected exactly one skills layer", ref, len(layers))
	}
	rc, err := layers[0].Uncompressed()
	if err != nil {
		return fmt.Errorf("read skills layer of %s: %w", ref, err)
	}
	defer func() { _ = rc.Close() }()
	if err := gitx.Untar(rc, dest); err != nil {
		return fmt.Errorf("extract %s: %w", ref, err)
	}
	return nil
}

// Push builds a one-layer artifact from the uncompressed tar stream r and
// writes it to ref.
func (Registry) Push(ctx context.Context, ref string, r io.Reader) error {
	dst, err := name.ParseReference(ref)
	if err != nil {
		return fmt.Errorf("parse %q: %w", ref, err)
	}
	layer := stream.NewLayer(io.NopCloser(r), stream.WithMediaType(LayerMediaType))
	img, err := mutate.AppendLayers(empty.Image, layer)
	if err != nil {
		return fmt.Errorf("build image: %w", err)
	}
	if err := remote.Write(dst, img, remote.WithContext(ctx), remote.WithAuthFromKeychain(authn.DefaultKeychain)); err != nil {
		return fmt.Errorf("push %s: %w", ref, err)
	}
	return nil
}
