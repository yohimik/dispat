// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// A provider's publish task cannot unlock a consumer build while native
// recording is deferred until after the graph. Without commit.push, the
// single-history release has no interim durable receipt to stand for its tag.
func TestExecutionDeferredTagCannotUnlockPublishReadiness(t *testing.T) {
	for _, distributed := range []bool{false, true} {
		name := "local"
		if distributed {
			name = "distributed"
		}
		t.Run(name, func(t *testing.T) {
			rig := newExecutionPrepareWorkspace(t, func(_ *harness.Repo, cfg *models.File) {
				cfg.IsBuildWaitingPublish = models.StageRelationOf(true)
				cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Branch: harness.DefaultBranch}
				if distributed {
					cfg.Execution.Workers = cfg.Execution.Workers[:1]
					cfg.Execution.Timeouts.Preflight = 3
				} else {
					cfg.Execution = nil
				}
			})
			rig.commitTo("provider and consumer changed", "assets", "ui")
			beforeTags := rig.repo.TagList()

			res := rig.release()
			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Contains(t, res.Stdout, "consumer ui waits for provider assets")
			assert.Contains(t, res.Stdout, "writes no durable publication receipt")
			assert.Empty(t, rig.runs(), "no command starts before the missing record is detected")
			assert.Equal(t, beforeTags, rig.repo.TagList(), "no new native tag was created")
		})
	}
}

func TestExecutionImmediateTagStillUnlocksPublishReadiness(t *testing.T) {
	rig := newExecutionPrepareWorkspace(t, func(_ *harness.Repo, cfg *models.File) {
		cfg.IsBuildWaitingPublish = models.StageRelationOf(true)
		cfg.Execution = nil
	})
	rig.commitTo("provider and consumer changed", "assets", "ui")
	res := rig.release()
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, rig.repo.TagList(), "assets@1.1.0")
	assert.Contains(t, rig.repo.TagList(), "ui@1.1.0")
}

func TestExecutionPushedReceiptStillUnlocksPublishReadiness(t *testing.T) {
	rig := newExecutionPrepareWorkspace(t, func(_ *harness.Repo, cfg *models.File) {
		cfg.IsBuildWaitingPublish = models.StageRelationOf(true)
		cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true), Push: true,
			Branch: harness.DefaultBranch}
		cfg.Execution = nil
	})
	rig.commitTo("provider and consumer changed", "assets", "ui")
	rig.repo.Git("push", "-q", "origin", harness.DefaultBranch)
	res := rig.release()
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, rig.repo.TagList(), "assets@1.1.0")
	assert.Contains(t, rig.repo.TagList(), "ui@1.1.0")
}
