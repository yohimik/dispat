// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

func TestWorkerSignsOnlyVerifiedDeclaredRootExportsAsTypedPaths(t *testing.T) {
	privateRoot := filepath.Join(t.TempDir(), "task", "ui")
	assignment := Assignment{Header: Header{Protocol: ChoreographyProtocolVersion},
		Package: &AssignmentPackage{Name: "ui", Repository: "source"},
		Outputs: []string{"dist"}}
	values := []plan.Output{
		{Name: "artifact", Value: filepath.Join(privateRoot, "dist", "lib", "a.js")},
		{Name: "literal", Value: "dispat-output://source/ui/dist/lib/a.js"},
		{Name: "outside", Value: filepath.Join(privateRoot, "dist-extra", "a.js")},
	}
	require.NoError(t, os.MkdirAll(filepath.Join(privateRoot, "dist", "lib"), 0o700))
	require.NoError(t, os.WriteFile(values[0].Value, []byte("verified"), 0o600))
	exports := formatDeclaredTaskExports(values, assignment, privateRoot)
	require.Equal(t, "", exports[0].Value)
	require.Equal(t, &ExportedDeclaredPath{Repository: "source", Package: "ui",
		Root: "dist", Relative: "lib/a.js"}, exports[0].DeclaredPath)
	require.Nil(t, exports[1].DeclaredPath)
	require.Equal(t, values[1].Value, exports[1].Value)
	require.Nil(t, exports[2].DeclaredPath)
	require.Equal(t, values[2].Value, exports[2].Value)

	result := Result{Header: Header{Protocol: ChoreographyProtocolVersion}, Exports: exports}
	require.NoError(t, rehomeChoreographyExports(&result, "source", "ui",
		filepath.Join(t.TempDir(), "authoritative", "ui"), assignment.Outputs))
	require.NotContains(t, result.Exports[0].Value, privateRoot)
	require.Nil(t, result.Exports[0].DeclaredPath)
}

func TestFailedSweepCarriesOnlyManifestProvenPrivatePaths(t *testing.T) {
	checkout := filepath.Join(t.TempDir(), "private-checkout")
	source := filepath.Join(checkout, "repo")
	file := filepath.Join(source, "coverage", "core.txt")
	require.NoError(t, os.MkdirAll(filepath.Dir(file), 0o700))
	require.NoError(t, os.WriteFile(file, []byte("partial"), 0o600))
	assignment := Assignment{Header: Header{Protocol: ChoreographyProtocolVersion, Kind: KindRun},
		Package: &AssignmentPackage{Name: "core"}, Outputs: []string{"coverage"}}
	values := []plan.Output{
		{Name: "SCALAR", Value: "before-failure"},
		{Name: "FILE", Value: file},
		{Name: "LITERAL", Value: "dispat-output://core/coverage/core.txt"},
	}
	manifest := &OutputManifest{Roots: []string{"coverage"},
		Entries: []ManifestEntry{{Path: "coverage/core.txt", Type: "file"}}}
	carried := formatFailedSweepExports(values, assignment, source, checkout, manifest, zerolog.Nop())
	require.Len(t, carried, 3)
	require.Equal(t, "before-failure", carried[0].Value)
	require.Equal(t, &ExportedDeclaredPath{Package: "core", Root: "coverage", Relative: "core.txt"},
		carried[1].DeclaredPath)
	require.Empty(t, carried[1].Value)
	require.Equal(t, values[2].Value, carried[2].Value,
		"an ordinary token-shaped string must remain literal")

	uncaptured := formatFailedSweepExports(values, assignment, source, checkout, nil, zerolog.Nop())
	require.Len(t, uncaptured, 2)
	require.Equal(t, "SCALAR", uncaptured[0].Name)
	require.Equal(t, "LITERAL", uncaptured[1].Name)
	require.True(t, privateTaskPath(file, checkout))
	require.False(t, privateTaskPath(filepath.Join(t.TempDir(), "external"), checkout))
	alias := filepath.Join(t.TempDir(), "checkout-alias")
	if err := os.Symlink(checkout, alias); err == nil {
		require.True(t, privateTaskPath(file, alias),
			"a canonical shell path into a symlinked checkout is still ephemeral")
	}
}

func TestWorkerClassifiesCanonicalShellPathBelowSymlinkedTaskRoot(t *testing.T) {
	base := t.TempDir()
	realRoot := filepath.Join(base, "real", "ui")
	file := filepath.Join(realRoot, "dist", "a.js")
	require.NoError(t, os.MkdirAll(filepath.Dir(file), 0o700))
	require.NoError(t, os.WriteFile(file, []byte("asset"), 0o600))
	if err := os.Symlink(filepath.Join(base, "real"), filepath.Join(base, "alias")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	assignment := Assignment{Package: &AssignmentPackage{Name: "ui"}, Outputs: []string{"dist"}}
	exports := formatDeclaredTaskExports([]plan.Output{{Name: "ASSET", Value: file}},
		assignment, filepath.Join(base, "alias", "ui"))
	require.Equal(t, "", exports[0].Value)
	require.Equal(t, &ExportedDeclaredPath{Package: "ui", Root: "dist", Relative: "a.js"},
		exports[0].DeclaredPath)
}
