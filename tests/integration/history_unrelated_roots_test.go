// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// Moving independent projects into one repository preserves their separate
// release boundaries even though their old tags have no common ancestor.
func TestMergedIndependentHistoriesKeepTheirReleaseBoundaries(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigModel(libsConfig(echoBuild, 1))
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): original library")
	r.Git("tag", "core@1.0.0")
	r.CommitEmpty("fix(core): library repair")

	app := harness.New(t)
	app.SeedPackage("packages", "app")
	app.Commit("feat(app): original application")
	app.Git("tag", "app@2.0.0")
	app.CommitEmpty("feat(app): application feature")
	r.Git("fetch", app.Root, "refs/heads/main:refs/heads/imported-app", "refs/tags/app@2.0.0:refs/tags/app@2.0.0")
	r.Git("merge", "--allow-unrelated-histories", "--no-edit", "-m", "chore: combine independent projects", "imported-app")

	before := r.TagList()
	fault := harness.NewGitFault(t, harness.GitFault{Pattern: "*merge-base --octopus --all*", Code: 128})
	failed := r.CommandEnv(fault.Env(), "release")
	require.NotZero(t, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
	assert.Positive(t, fault.Matches())
	assert.Equal(t, before, r.TagList(), "a failed history query cannot invent a release")
	assert.NotContains(t, failed.Stdout, "publish started")

	status := r.StatusOK()
	assert.Equal(t, "1.0.0 -> 1.0.1", harness.GraphLine(status.Events, "core").Str("version"))
	assert.Equal(t, "2.0.0 -> 2.1.0", harness.GraphLine(status.Events, "app").Str("version"))
	r.ReleaseOK()
	assert.True(t, r.IsTagged("core@1.0.1"))
	assert.True(t, r.IsTagged("app@2.1.0"))
	assert.True(t, r.IsTagged("core@1.0.0"))
	assert.True(t, r.IsTagged("app@2.0.0"))
}
