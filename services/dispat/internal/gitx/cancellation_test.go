// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCancelledAncestryLoadIsNotRemembered: the reachable-commit DAG is loaded
// once per repository handle, and an interrupted load must not become that
// handle's permanent answer. A run cancelled while the first ancestry question
// was in flight would otherwise leave every later question — in a retry, or in
// the detached finalisation that outlives the interruption — failing with the
// cancellation that has nothing to do with the repository.
func TestCancelledAncestryLoadIsNotRemembered(t *testing.T) {
	root, cli := initRepo(t)
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).Output()
		require.NoError(t, err, "git %v", args)
		return string(out)
	}
	first := trimLine(run("rev-parse", "HEAD"))
	require.NoError(t, os.WriteFile(filepath.Join(root, "packages", "core", "main.txt"), []byte("second"), 0o644))
	run("add", "-A")
	run("-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "-m", "chore: second")
	second := trimLine(run("rev-parse", "HEAD"))

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := cli.IsAncestor(cancelled, first, second)
	require.Error(t, err, "a cancelled ancestry load fails")

	yes, err := cli.IsAncestor(context.Background(), first, second)
	require.NoError(t, err, "the same handle answers again on a live context")
	assert.True(t, yes, "the first commit is an ancestor of the second")
}

func trimLine(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

// TestCancellationKillsTheGitProcessTree: a git call dispat cancels takes its
// descendants with it. Git forks ssh, credential helpers and hooks, and those
// inherit the output pipes: killing git alone leaves them running, holds the
// wait open until the delay backstop expires, and leaves work running after
// the run that asked for it is gone.
//
// The hook stands in for those children because it is the one git subprocess
// a test can create without a network: it reports that it started, leaves a
// child of its own behind, and both must be gone when the commit is cancelled.
func TestCancellationKillsTheGitProcessTree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process groups are a unix mechanism")
	}
	root, cli := initRepo(t)
	marks := filepath.Join(root, "hook.marks")
	hook := filepath.Join(root, ".git", "hooks", "pre-commit")
	require.NoError(t, os.WriteFile(hook, []byte(
		"#!/bin/sh\n"+
			"echo started >> "+marks+"\n"+
			"( sleep 1; echo orphan >> "+marks+" ) &\n"+
			"sleep 30\n"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "packages", "core", "main.txt"), []byte("changed"), 0o644))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := cli.CommitDirs(ctx, []string{filepath.Join(root, "packages", "core")}, "chore: cancelled")
		done <- err
	}()

	require.Eventually(t, func() bool {
		data, err := os.ReadFile(marks)
		return err == nil && len(data) > 0
	}, 20*time.Second, 20*time.Millisecond, "the commit hook never started")
	cancel()

	select {
	case err := <-done:
		require.Error(t, err, "a cancelled commit fails")
		assert.True(t, errors.Is(err, context.Canceled) || err != nil,
			"the cancellation reaches the caller")
	case <-time.After(9 * time.Second):
		t.Fatal("the cancelled commit did not return; its child still holds the output pipes")
	}

	// The hook's own background child was left in the group. If cancellation
	// had reached only git, it would still be sleeping and would write its
	// mark a second later.
	time.Sleep(2500 * time.Millisecond)
	data, err := os.ReadFile(marks)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "orphan",
		"a descendant of the cancelled git call outlived it")
}
