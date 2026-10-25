// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

func TestDeclaredOutputPathsFollowTaskCheckoutAcrossSourcePathStyles(t *testing.T) {
	for _, source := range []struct {
		name string
		root string
		path string
		file string
		want string
	}{
		{"Unix source", "/opt/source/pkg", "/opt/source/pkg/dist/lib.so", "lib.so", "/opt/source/pkg/dist/lib.so"},
		{"Windows source", `C:\src\pkg`, `c:\SRC\pkg\dist\lib.dll`, "lib.dll", `C:\src\pkg\dist\lib.dll`},
	} {
		t.Run(source.name, func(t *testing.T) {
			mapping := []OutputPathMapping{{SourceRoot: source.root, TaskRoot: "/tmp/task/pkg",
				Roots: []string{"dist"}}}
			env := RemapOutputEnvironment([]string{
				"DISPAT_OUTPUT_DIST_DIR=" + source.path,
				"DISPAT_OUTPUT_LITERAL=prefix:" + source.path,
				"OTHER=" + source.path,
			}, mapping)
			assert.Equal(t, "DISPAT_OUTPUT_DIST_DIR=/tmp/task/pkg/dist/"+source.file, env[0])
			assert.Equal(t, "DISPAT_OUTPUT_LITERAL=prefix:"+source.path, env[1])
			assert.Equal(t, "OTHER="+source.path, env[2])
			outputs := []plan.Output{{Name: "DIST_DIR", Value: "/tmp/task/pkg/dist/" + source.file}}
			RemapCapturedOutputs(outputs, mapping)
			assert.Equal(t, source.want, outputs[0].Value)
		})
	}
}

func TestDeclaredOutputPathMappingRejectsSiblingAndTraversal(t *testing.T) {
	mapping := []OutputPathMapping{{SourceRoot: `C:\src\pkg`, TaskRoot: "/tmp/task/pkg",
		Roots: []string{"dist"}}}
	for _, value := range []string{`C:\src\pkg\dist-old\lib.dll`,
		`C:\src\pkg\dist\..\secret.dll`, `dist/lib.dll`} {
		assert.Equal(t, value, remapDeclaredPath(value, mapping, false))
	}
	unc := []OutputPathMapping{{SourceRoot: `\\server\share\pkg`, TaskRoot: "/tmp/task/pkg",
		Roots: []string{"dist"}}}
	assert.Equal(t, "/server/share/pkg/dist/lib.dll",
		remapDeclaredPath("/server/share/pkg/dist/lib.dll", unc, false))
}
