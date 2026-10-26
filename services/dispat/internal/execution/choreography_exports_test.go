// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
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
	require.NoError(t, rehomeChoreographyExports(&result, "source", "ui",
		"/authoritative/ui", []string{"dist"}))
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
		require.Error(t, rehomeChoreographyExports(&result, "source", "ui",
			"/authoritative/ui", []string{"dist"}))
	}
}
