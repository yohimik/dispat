// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A generated import list may itself be assembled through several files.
// Each item must keep the directory of the fragment that wrote it, and a
// missing fragment must refuse the whole workspace rather than quietly use
// the source whose path was already resolved.
func TestConfigNestedImportFragmentsKeepOriginsAndFailClosed(t *testing.T) {
	control := covPolyrepoImportFleet(t)
	control.WriteFile("cfg/one.json", `["../sources/one/dispat.json"]`+"\n")
	control.WriteFile("cfg/two.json", `["../sources/two/dispat.json"]`+"\n")
	control.WriteFile("cfg/list.json", `{"$ref":["one.json","missing.json"]}`+"\n")
	control.WriteConfigRaw(map[string]any{
		"polyrepo": true, "logFormat": "json", "updateCheck": false,
		"github": map[string]any{"enabled": false},
		"configs": map[string]any{"$ref": "cfg/list.json"},
	})
	control.Commit("chore: assemble source imports from nested fragments")

	bad := control.Status()
	require.NotZero(t, bad.Code)
	assert.Contains(t, diagnosticText(bad), "missing.json")
	assert.Empty(t, plannedPackages(bad), "one resolved source cannot hide a broken second import")
	assert.Empty(t, control.TagList())

	control.WriteFile("cfg/list.json", `{"$ref":["one.json","two.json"]}`+"\n")
	good := control.StatusOK()
	found := covPolyrepoImported(good)
	assert.True(t, found["one"], "the first import resolves from cfg/one.json")
	assert.True(t, found["two"], "the second import resolves from cfg/two.json")
}
