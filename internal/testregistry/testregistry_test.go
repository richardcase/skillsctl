package testregistry

import (
	"fmt"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

func TestNewServesAWritableRegistry(t *testing.T) {
	host := New(t)

	ref, err := name.ParseReference(fmt.Sprintf("%s/skills:v1", host))
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(ref, empty.Image, remote.WithAuthFromKeychain(authn.DefaultKeychain)); err != nil {
		t.Fatalf("push to test registry: %v", err)
	}
	if _, err := remote.Get(ref, remote.WithAuthFromKeychain(authn.DefaultKeychain)); err != nil {
		t.Fatalf("read back from test registry: %v", err)
	}
}
