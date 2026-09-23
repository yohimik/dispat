// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// Matching text in another source does not grant a source commit authority
// over that package, including when a repeated wildcard uses the scope cache.
func TestFleetWildcardScopesKeepEachSourceOwnIntent(t *testing.T) {
	libraries := harness.New(t)
	libraries.SeedPackage("packages", "lib-core")
	libraries.SeedPackage("packages", "lib-util")
	libraries.Commit("chore: seed library packages")
	libraries.CommitEmpty("feat(*core): library feature")
	libraries.CommitEmpty("fix(*core): library correction")

	applications := harness.New(t)
	applications.SeedPackage("packages", "app-core")
	applications.Commit("chore: seed application package")
	applications.CommitEmpty("fix(*core): application correction")

	control := harness.New(t)
	addPolyrepoSource(t, control, "libraries", "sources/lib", libraries)
	addPolyrepoSource(t, control, "applications", "sources/app", applications)
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{
		"libs": "sources/lib/packages", "apps": "sources/app/packages",
	})
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: assemble fleet")

	status := control.StatusOK()
	assert.Equal(t, "0.0.0 -> 0.1.0", harness.GraphLine(status.Events, "lib-core").Str("version"))
	assert.Equal(t, "0.0.0 -> 0.0.1", harness.GraphLine(status.Events, "app-core").Str("version"),
		"the library's feature must not raise the application's bump")
	assert.Equal(t, "unchanged", harness.GraphLine(status.Events, "lib-util").Str("message"))
	control.ReleaseOK()
	assert.Equal(t, []string{"lib-core@0.1.0"}, polyrepoTags(control, "sources/lib"))
	assert.Equal(t, []string{"app-core@0.0.1"}, polyrepoTags(control, "sources/app"))
	assert.Empty(t, control.TagList())
}
