// Package testregistry starts an in-process OCI registry for tests, so no
// test that packages or installs from OCI touches the network.
package testregistry

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"
)

// New starts an in-process registry and returns its host:port. t.Cleanup
// closes it, so no caller needs to.
func New(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(registry.New())
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}
