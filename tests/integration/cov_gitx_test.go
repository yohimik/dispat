// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Coverage scenarios for the git layer, driven through the flows that reach
// it: a remote URL carrying a credential, a staged rename under the dirty
// guard, a remote that already records the version a release is about to plan,
// and a release whose commit has nothing to stage.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestGitReleaseCommitIsSkippedWhenNothingWasStaged: with the changelog off
// and no manifest to rewrite, the release commit would have nothing in it. An
// empty commit is not a record of anything, so none is made, and the tag then
// names the commit the release was planned on — which is where it would have
// pointed had there been no commit stage at all.
func TestGitReleaseCommitIsSkippedWhenNothingWasStaged(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Changelog = &models.ChangelogConfig{Enabled: models.Bool(false)}
	cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true)}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")
	planned := r.Git("rev-parse", "HEAD")

	res := r.ReleaseOK()
	assert.NotContains(t, res.Stdout, "created release commit")
	assert.Equal(t, planned, r.Git("rev-parse", "HEAD"), "no commit was made")
	require.True(t, r.IsTagged("core@0.1.0"), "tags: %v", r.TagList())
	assert.Equal(t, planned, r.Git("rev-list", "-n", "1", "core@0.1.0"),
		"and the tag names the commit that was planned")

	// A second run converges: the package has nothing pending, and the
	// staging question is asked again on the same clean tree.
	r.ReleaseOK()
	assert.Equal(t, 1, r.TagCount("core@"), "tags: %v", r.TagList())
}
