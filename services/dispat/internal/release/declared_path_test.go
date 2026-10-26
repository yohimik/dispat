// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTypedDeclaredOutputPathIsPortableAndBounded(t *testing.T) {
	for _, tc := range []struct {
		name, source, value, rehome, expected string
	}{
		{"unix", "/private/pkg", "/private/pkg/dist/lib/main.so", "/authoritative/pkg",
			"/authoritative/pkg/dist/lib/main.so"},
		{"windows", `C:\private\pkg`, `c:\PRIVATE\pkg\dist\lib.dll`, `D:\source\pkg`,
			`D:\source\pkg\dist\lib.dll`},
		{"unc", `\\server\share\pkg`, `\\server\share\pkg\dist\lib.dll`,
			`\\other\share\pkg`, `\\other\share\pkg\dist\lib.dll`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, ok := IdentifyDeclaredOutputPath(tc.value, tc.source, []string{"dist", "dist/lib"})
			require.True(t, ok)
			if tc.name == "unix" {
				require.Equal(t, DeclaredOutputPath{Root: "dist/lib", Relative: "main.so"}, path)
			} else {
				require.Equal(t, DeclaredOutputPath{Root: "dist", Relative: "lib.dll"}, path)
			}
			value, err := RehomeDeclaredOutputPath(path, tc.rehome, []string{"dist", "dist/lib"})
			require.NoError(t, err)
			require.Equal(t, tc.expected, value)
		})
	}
}

func TestTypedDeclaredOutputPathLeavesLiteralsAndRejectsTraversal(t *testing.T) {
	root, err := RehomeDeclaredOutputPath(DeclaredOutputPath{Root: "dist"},
		"/authoritative/pkg", []string{"dist"})
	require.NoError(t, err)
	require.Equal(t, "/authoritative/pkg/dist", root)
	for _, value := range []string{
		"dispat-output://v1/literal", "prefix:/private/pkg/dist/lib.so",
		"/private/pkg/dist-old/lib.so", "/private/pkg/dist/../dist/lib.so",
		`C:\private\pkg\dist\..\secret`,
	} {
		_, ok := IdentifyDeclaredOutputPath(value, "/private/pkg", []string{"dist"})
		require.False(t, ok, value)
	}
	for _, ref := range []DeclaredOutputPath{
		{Root: "dist", Relative: "../secret"},
		{Root: "dist", Relative: "/absolute"},
		{Root: "dist", Relative: `C:\escape`},
		{Root: "dist", Relative: `\\server\share`},
		{Root: "dist-old", Relative: "file"},
		{Root: "dist/../secret", Relative: "file"},
	} {
		_, err := RehomeDeclaredOutputPath(ref, "/authoritative/pkg", []string{"dist"})
		require.Error(t, err, "%+v", ref)
	}
}
