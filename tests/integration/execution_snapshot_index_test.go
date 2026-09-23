// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A committed checkout with a lost index cannot offer a delegated source
// snapshot. The CLI must refuse before any build or tag and leave that index
// untouched for the operator to repair.
func TestExecutionMissingCommittedIndexCannotPublish(t *testing.T) {
	rig := newExecutionRig(t)
	index := strings.TrimSpace(rig.repo.Git("rev-parse", "--path-format=absolute", "--git-path", "index"))
	require.NoError(t, os.Remove(index))
	worker := startWorker(t, rig.repo, executionWorkerConfig(rig.mailbox), 0)

	res := rig.release()
	reply := stopAll(t, []*executionWorker{worker})[0]
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, 0, reply.Code)
	assert.Contains(t, res.Stdout+res.Stderr, "missing index")
	assert.Empty(t, buildRuns(rig.repo))
	assert.Empty(t, rig.repo.TagList())
	assert.NoFileExists(t, index, "release cannot rebuild the user's index")
}
