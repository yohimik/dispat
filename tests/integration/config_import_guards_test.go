// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An import fragment's own folder is the base for its paths, but cannot grant
// access beyond the control workspace. This checks a fragment-declared path,
// rather than a path supplied by --configs on the command line.
func TestConfigImportFragmentCannotEscapeControlRoot(t *testing.T) {
	control := importFleet(t)
	control.WriteFile("cfg/list.json", `["../../outside/dispat.json"]`+"\n")
	control.WriteConfigRaw(map[string]any{
		"polyrepo": true, "logFormat": "json", "updateCheck": false,
		"github":  map[string]any{"enabled": false},
		"configs": map[string]any{"$ref": "cfg/list.json"},
	})
	control.Commit("chore: name an import outside the control workspace")
	res := control.Status()
	require.NotZero(t, res.Code)
	assert.Contains(t, res.Stdout+res.Stderr, "path escapes control root")
	assert.Empty(t, plannedPackages(res))
	assert.Empty(t, control.TagList())
}
