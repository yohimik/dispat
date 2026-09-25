// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// shrinkRecordingGrace makes the grace after an interrupt short enough to
// test, and puts the real value back afterwards.
func shrinkRecordingGrace(t *testing.T, grace time.Duration) {
	t.Helper()
	previous := recordingGrace
	recordingGrace = grace
	t.Cleanup(func() { recordingGrace = previous })
}

// TestDetachRecordingIsUnboundedWhileTheRunIsLive: an uninterrupted run is not
// bounded by the recording's grace, because a large release commit or a slow
// push is still the work the run exists to finish. Ending the recording
// cancels it without the grace's cause.
func TestDetachRecordingIsUnboundedWhileTheRunIsLive(t *testing.T) {
	shrinkRecordingGrace(t, 10*time.Millisecond)
	recording, stop := detachRecording(t.Context())

	_, isBounded := recording.Deadline()
	assert.False(t, isBounded, "a live run gives its records no deadline")
	select {
	case <-recording.Done():
		t.Fatal("the recording ended while the run was live")
	case <-time.After(50 * time.Millisecond):
	}

	stop()
	require.ErrorIs(t, recording.Err(), context.Canceled)
	assert.NotErrorIs(t, context.Cause(recording), errRecordingGraceElapsed)
}

// TestDetachRecordingGivesAGraceAfterCancellation: the run's cancellation does
// not reach the recording, which goes on for the grace and only then ends,
// with a cause that says so. A recording that ends before its grace runs out
// never reads as one that timed out.
func TestDetachRecordingGivesAGraceAfterCancellation(t *testing.T) {
	t.Run("the grace runs out", func(t *testing.T) {
		shrinkRecordingGrace(t, 50*time.Millisecond)
		run, cancel := context.WithCancel(t.Context())
		recording, stop := detachRecording(run)
		defer stop()

		cancel()
		assert.NoError(t, recording.Err(), "the interrupt itself does not end the recording")
		require.Eventually(t, func() bool { return recording.Err() != nil }, 5*time.Second, 5*time.Millisecond)
		assert.ErrorIs(t, context.Cause(recording), errRecordingGraceElapsed)
	})

	t.Run("the records finish first", func(t *testing.T) {
		shrinkRecordingGrace(t, 50*time.Millisecond)
		run, cancel := context.WithCancel(t.Context())
		recording, stop := detachRecording(run)

		cancel()
		stop()
		time.Sleep(100 * time.Millisecond)
		require.ErrorIs(t, recording.Err(), context.Canceled)
		assert.NotErrorIs(t, context.Cause(recording), errRecordingGraceElapsed,
			"a stopped recording keeps the cause it ended with")
	})
}

// TestFinalizeRecordsWhileItsObservedRunIsCancelled: an interrupt that arrives
// while a bracket hook runs stops the operator's scripts and nothing else. The
// release commit, the tag and the push go on under the live recording
// context, and no later hook starts.
func TestFinalizeRecordsWhileItsObservedRunIsCancelled(t *testing.T) {
	enabled := true
	cfg := &config.File{Commit: &config.CommitConfig{Enabled: &enabled, Push: true}, Run: &config.RunConfig{}}
	root, a := guardRepo(t, cfg)
	remote := addRecordBareRemote(t, root, "origin")
	pkgDir := filepath.Join(root, "pkg")
	require.NoError(t, os.MkdirAll(pkgDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "CHANGELOG.md"), []byte("# core\n"), 0o644))
	cfg.Scripts = map[string]config.Script{}
	for _, hook := range []string{"before-commit", "after-commit", "post-commit", "before-push", "after-push"} {
		cfg.Scripts[hook] = config.Script{hook}
	}
	cfg.Run.BeforeCommit, cfg.Run.AfterCommit = []string{"before-commit"}, []string{"after-commit"}
	cfg.Run.PostCommit = []string{"post-commit"}
	cfg.Run.BeforePush, cfg.Run.AfterPush = []string{"before-push"}, []string{"after-push"}

	run, cancel := context.WithCancel(t.Context())
	runner := &interruptionHookRunner{cancel: cancel, cancelOn: "before-commit"}
	hooks := &runHooks{cfg: cfg, runner: runner, root: root, log: zerolog.Nop()}
	recordCtx, stopRecording := detachRecording(run)
	defer stopRecording()
	observed, stopObserving := newRecordHooks(recordCtx, run)
	defer stopObserving()
	rel := &plan.Release{Pkg: &model.Package{Name: "core", Dir: pkgDir, Space: &model.Space{Name: "libs"}},
		Channel: "stable", Next: ccme.Version{Minor: 1}, Bump: ccme.BumpMinor, NewWork: true}
	pl := &plan.Plan{Order: []string{"core"}, Releases: map[string]*plan.Release{"core": rel}}
	results := map[string]*release.Result{"core": {Name: "core", Status: release.StatusPublished}}
	crit := &criticals{}

	a.finalize(recordCtx, finalizer{remote: "origin", hooks: hooks, crit: crit, observed: observed}, pl, results)

	require.Zero(t, crit.len(), "%v", crit.err())
	require.ErrorIs(t, run.Err(), context.Canceled)
	assert.NoError(t, recordCtx.Err(), "the records outlive the interrupt")
	assert.Equal(t, []string{"before-commit"}, runner.commands(),
		"the hook the interrupt arrived in is the last one that starts")
	head := recordGit(t, root, "rev-parse", "HEAD")
	assert.Equal(t, "chore(release): "+rel.TagName(), recordGit(t, root, "log", "-1", "--format=%s"))
	assert.Equal(t, head, recordGit(t, root, "rev-parse", rel.TagName()+"^{commit}"))
	assert.Equal(t, head, recordGit(t, remote, "rev-parse", rel.TagName()+"^{commit}"), "the tag was pushed")
	assert.Equal(t, head, recordGit(t, remote, "rev-parse", "refs/heads/main"), "and so was the release commit")
}
