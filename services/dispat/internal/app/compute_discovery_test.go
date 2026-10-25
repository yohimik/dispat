package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestComputeReadsTheDiscoveredWorkspace: compute answers from the one
// workspace walk its invocation makes, so a package folder that appears after
// that walk is not seen by a second one.
func TestComputeReadsTheDiscoveredWorkspace(t *testing.T) {
	root, cfg := newComputeRepository(t, 1)
	a := New(root, cfg, zerolog.Nop())
	require.NoError(t, a.discovery().err)
	writeComputePackage(t, root, "late")

	var out strings.Builder
	_, err := a.Compute(t.Context(), filepath.Join(root, "dispat.json"), ComputeOptions{Out: &out})
	require.NoError(t, err)
	assert.Contains(t, out.String(), "+ initial pkg000 1.0.0")
	assert.NotContains(t, out.String(), "late", "compute walked the workspace a second time")
}
