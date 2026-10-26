// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChoreographyTypedExportsRehomeWithoutChangingLiterals(t *testing.T) {
	result := Result{Header: Header{Protocol: ChoreographyProtocolVersion},
		Exports: []ExportedValue{
			{Name: "artifact", DeclaredPath: &ExportedDeclaredPath{
				Repository: "source", Package: "ui", Root: "dist", Relative: "lib/a.js"}},
			{Name: "literal", Value: "dispat-output://source/ui/dist/lib/a.js"},
		}}
	require.NoError(t, rehomeChoreographyExports(choreographyExportRehomeOptions{
		Result: &result, Repository: "source", Package: "ui",
		SourceRoot: "/authoritative/ui", Roots: []string{"dist"}}))
	require.Equal(t, "/authoritative/ui/dist/lib/a.js", result.Exports[0].Value)
	require.Nil(t, result.Exports[0].DeclaredPath)
	require.Equal(t, "dispat-output://source/ui/dist/lib/a.js", result.Exports[1].Value)
}

func TestChoreographyTypedExportsRejectForeignOrEscapingPaths(t *testing.T) {
	for _, ref := range []ExportedDeclaredPath{
		{Repository: "foreign", Package: "ui", Root: "dist", Relative: "a.js"},
		{Repository: "source", Package: "ui", Root: "dist", Relative: "../secret"},
	} {
		result := Result{Header: Header{Protocol: ChoreographyProtocolVersion},
			Exports: []ExportedValue{{Name: "artifact", DeclaredPath: &ref}}}
		require.Error(t, rehomeChoreographyExports(choreographyExportRehomeOptions{
			Result: &result, Repository: "source", Package: "ui",
			SourceRoot: "/authoritative/ui", Roots: []string{"dist"}}))
	}
}

func TestChoreographyTypedExportUsesResolvedLocalSourcePath(t *testing.T) {
	root := t.TempDir()
	actual := filepath.Join(root, "actual", "ui")
	require.NoError(t, os.MkdirAll(actual, 0o700))
	linked := filepath.Join(root, "linked-ui")
	if err := os.Symlink(actual, linked); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	result := Result{Header: Header{Protocol: ChoreographyProtocolVersion},
		Exports: []ExportedValue{{Name: "artifact", DeclaredPath: &ExportedDeclaredPath{
			Repository: "source", Package: "ui", Root: "dist", Relative: "base"}}}}
	require.NoError(t, rehomeChoreographyExports(choreographyExportRehomeOptions{
		Result: &result, Repository: "source", Package: "ui",
		SourceRoot: linked, Roots: []string{"dist"}}))
	resolved, err := filepath.EvalSymlinks(actual)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(resolved, "dist", "base"), result.Exports[0].Value)
}
