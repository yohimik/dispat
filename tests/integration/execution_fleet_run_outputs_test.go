// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: a sweep root is resolved in each participating repository. A
// harmless-looking path in control must not overwrite a source package.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecutionFleetRunOutputsRefuseASourcePackageFolder(t *testing.T) {
	fleet := finalPolyrepo(t)
	control := fleet.control
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{"libs": "sources/lib/packages"})
	cfg["runOutputs"] = map[string]any{"tests": []string{"packages"}}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: configure a sweep root in every participating repository")
	controlBefore := control.Git("rev-parse", "HEAD")
	sourceBefore := control.Git("-C", "sources/lib", "rev-parse", "HEAD")

	refused := control.Status()
	require.NotZero(t, refused.Code, "a sweep must not merge into the source package\nstdout:\n%s", refused.Stdout)
	assert.Contains(t, diagnosticText(refused), `runOutputs["tests"]`)
	assert.Contains(t, diagnosticText(refused), `folder of package "core"`)
	assert.Empty(t, plannedPackages(refused), "composition refuses before planning any release")
	assert.Equal(t, controlBefore, control.Git("rev-parse", "HEAD"))
	assert.Equal(t, sourceBefore, control.Git("-C", "sources/lib", "rev-parse", "HEAD"))
	assert.Empty(t, control.TagList())
	assert.Empty(t, polyrepoTags(control, "sources/lib"))

	cfg["runOutputs"] = map[string]any{"tests": []string{"coverage"}}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: move the sweep root outside source packages")
	good := control.StatusOK()
	assert.Contains(t, importedPackages(good), "core", "a safe root lets the source release plan form")
	assert.Empty(t, control.TagList(), "status remains read-only")
	assert.Empty(t, polyrepoTags(control, "sources/lib"))
}
