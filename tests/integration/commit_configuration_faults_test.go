// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A file replacing the original hooks directory must not silently discard
// commit-msg policy while delegating to Git. The staged work survives that
// refusal and commits once the hooks location is repaired.
func TestCommitRefusesAFileAtTheHooksPathAndRetries(t *testing.T) {
	r := authoringRepo(t)
	r.WriteFile("tracked.txt", "changed\n")
	r.Git("add", "tracked.txt")
	before := r.Git("rev-parse", "HEAD")
	staged := r.Git("diff", "--cached")
	hooks := r.Path(".git", "hooks")
	require.NoError(t, os.RemoveAll(hooks))
	require.NoError(t, os.WriteFile(hooks, []byte("not a hook directory\n"), 0o600))

	failed := r.Command("commit", "-m", "fix(core): retain staged work")
	require.NotZero(t, failed.Code, "stdout:\n%s\nstderr:\n%s", failed.Stdout, failed.Stderr)
	assert.Contains(t, failed.Stdout+failed.Stderr, "not a directory")
	assert.Equal(t, before, r.Git("rev-parse", "HEAD"))
	assert.Equal(t, staged, r.Git("diff", "--cached"))

	require.NoError(t, os.Remove(hooks))
	require.NoError(t, os.Mkdir(hooks, 0o700))
	retried := r.Command("commit", "-m", "fix(core): retain staged work")
	require.Zero(t, retried.Code, "stdout:\n%s\nstderr:\n%s", retried.Stdout, retried.Stderr)
	assert.NotEqual(t, before, r.Git("rev-parse", "HEAD"))
}
