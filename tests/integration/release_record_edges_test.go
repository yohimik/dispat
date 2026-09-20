// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestReleaseRecordSnapshotHeadReadFailureRefusesPublication proves a fleet
// whose source HEAD cannot be re-read at the pre-publish snapshot boundary is
// refused before its publish script or either repository's release record.
// Once Git answers again, the unchanged plan can publish and checkpoint once.
func TestReleaseRecordSnapshotHeadReadFailureRefusesPublication(t *testing.T) {
	fleet := newFinalFaultFleet(t)
	control := fleet.control
	sourceRoot := filepath.Join(canonicalRoot(t, control), "sources", "lib")
	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*-C " + sourceRoot + " *rev-parse HEAD^{commit}*",
		Nth:     4,
	})

	failed := control.CommandEnv(fault.Env())
	require.NotZero(t, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
	combined := failed.Stdout + failed.Stderr
	assert.Contains(t, combined, harness.GitFaultMarker)
	assert.Contains(t, combined, "reading snapshot HEAD")
	assert.True(t, harness.IsCodePresent(failed.Events, "E330"), "stdout:\n%s", failed.Stdout)
	assert.NoFileExists(t, control.Path("sources", "lib", "publish-count"))
	assert.Empty(t, polyrepoTags(control, "sources/lib"))
	assert.Equal(t, fleet.sourceBefore, control.Git("rev-parse", "HEAD:sources/lib"))
	assert.Equal(t, fleet.controlBefore, control.Git("rev-parse", "HEAD"))

	retry := control.Release()
	require.Equal(t, 0, retry.Code, "stdout:\n%s\nstderr:\n%s", retry.Stdout, retry.Stderr)
	assert.Equal(t, 1, finalPublishCount(t, control))
	sourceAfter := control.Git("-C", "sources/lib", "rev-parse", "HEAD")
	assert.Equal(t, sourceAfter, control.Git("-C", fleet.sourceRemote, "rev-parse", "lib@0.1.0^{commit}"))
	assert.Equal(t, sourceAfter, control.Git("rev-parse", "HEAD:sources/lib"))
}

// TestReleaseRecordPostPublishHeadReadFailuresWithholdTheCheckpoint exercises
// the two source-HEAD reads after publication: one captures the commit just
// written, and the next proves that revision before tagging it. Either failure
// leaves the control repository at its old gitlink and reports publication as
// durable but incompletely recorded.
func TestReleaseRecordPostPublishHeadReadFailuresWithholdTheCheckpoint(t *testing.T) {
	for _, tc := range []struct {
		name string
		nth  int
		want string
	}{
		{name: "capturing the source release commit", nth: 5, want: "source release commit failed"},
		{name: "proving the source revision before tagging", nth: 7, want: "repository lib-source tag lib@0.1.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fleet := newFinalFaultFleet(t)
			control := fleet.control
			sourceRoot := filepath.Join(canonicalRoot(t, control), "sources", "lib")
			fault := harness.NewGitFault(t, harness.GitFault{
				Pattern: "*-C " + sourceRoot + " *rev-parse HEAD^{commit}*",
				Nth:     tc.nth,
			})

			failed := control.CommandEnv(fault.Env())
			require.NotZero(t, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
			combined := failed.Stdout + failed.Stderr
			assert.Contains(t, combined, harness.GitFaultMarker)
			assert.Contains(t, combined, tc.want)
			assert.True(t, harness.IsCodePresent(failed.Events, "E335"), "stdout:\n%s", failed.Stdout)
			assert.Contains(t, failed.Stdout, `"status":"published"`)
			assert.Equal(t, 1, finalPublishCount(t, control))
			assert.Empty(t, polyrepoTags(control, "sources/lib"))
			assert.Equal(t, fleet.sourceBefore, control.Git("rev-parse", "HEAD:sources/lib"),
				"control cannot claim a source revision that was not proven and tagged")
			assert.Equal(t, fleet.controlBefore, control.Git("rev-parse", "HEAD"))
		})
	}
}
