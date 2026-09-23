// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// This is a Linux mount-boundary regression. A linked worktree's private Git
// index belongs to the original checkout, which need not share a filesystem
// with the linked working tree whose output roots are installed.

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
)

func TestExecutionLinkedWorktreeInstallsOutputsAcrossFilesystemBoundary(t *testing.T) {
	rig := newExecutionRig(t, func(cfg *models.File) {
		cfg.BuildOutputs = []string{"dist"}
		cfg.RunOnly = placedOn(models.RunOnlyWorker, models.RunOnlyOrchestrator)
		cfg.Scripts["build"] = models.Script{
			executionRecordingScript + " && mkdir -p dist && printf fresh > dist/from-worker.txt"}
		cfg.Scripts["publish"] = models.Script{
			"test \"$(cat dist/from-worker.txt)\" = fresh && " + executionRecordingScript}
	})
	rig.repo.WriteFile(".gitignore", "dist/\n")
	rig.repo.Commit("chore(core): ignore the transferred build output")
	parent, err := os.MkdirTemp("/dev/shm", "dispat-it-worktree-")
	if err != nil {
		t.Skipf("a second writable Linux filesystem is unavailable: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(parent) })
	worktree := filepath.Join(parent, "linked")
	out, err := exec.Command("git", "-C", rig.repo.Root, "worktree", "add", "--detach", worktree, "HEAD").CombinedOutput()
	require.NoError(t, err, "creating a linked worktree on another filesystem: %s", out)
	t.Cleanup(func() {
		out, err := exec.Command("git", "-C", rig.repo.Root, "worktree", "remove", "--force", worktree).CombinedOutput()
		assert.NoError(t, err, "removing linked worktree: %s", out)
	})
	probe := filepath.Join(rig.repo.Root, ".dispat-device-probe")
	require.NoError(t, os.WriteFile(probe, []byte("probe"), 0o600))
	err = os.Rename(probe, filepath.Join(worktree, ".dispat-device-probe"))
	if err == nil {
		t.Fatal("the fixture did not separate the Git index and linked worktree filesystems")
	}
	require.True(t, errors.Is(err, syscall.EXDEV), "the fixture must cross a filesystem: %v", err)
	require.NoError(t, os.Remove(probe))
	worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0)

	res := rig.repo.CommandRootEnv(worktree, rig.env(), "release")

	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	content, err := os.ReadFile(filepath.Join(worktree, "packages", "core", "dist", "from-worker.txt"))
	require.NoError(t, err)
	assert.Equal(t, "fresh", string(content), "the locally published package reads the worker's output")
	assert.Contains(t, rig.repo.TagList(), "core@0.1.0")
	status := strings.TrimSpace(rig.repo.Git("-C", worktree, "status", "--porcelain"))
	assert.NotContains(t, status, "dispat-outputs-", "staging never enters the working tree or Git index")
	assert.NotContains(t, status, ".dispat-old-", "rollback names never enter Git status")
	files := rig.repo.Git("-C", worktree, "ls-tree", "-r", "--name-only", "core@0.1.0")
	assert.NotContains(t, files, "dispat-outputs-", "staging never reaches a release record")
	entries, err := os.ReadDir(parent)
	require.NoError(t, err)
	require.Len(t, entries, 1, "only the linked checkout remains after installation")
	assert.Equal(t, "linked", entries[0].Name())
	stopAll(t, []*executionWorker{worker})
}
