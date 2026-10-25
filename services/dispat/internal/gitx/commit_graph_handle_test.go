// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A rev-list listing carries no trace of a shallow boundary or of a replaced
// parent, so a DAG another handle loaded before either changed cannot be told
// apart from a current one. These tests keep the loading handle alive while
// the history git reports changes under the same HEAD, and require a fresh
// handle to answer from the history as it is now.

func commitHistoryFile(t *testing.T, root, name string) string {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(name), 0o644))
	runGit(t, root, "add", name)
	runGit(t, root, "commit", "-qm", "feat(core): "+name)
	return strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD"))
}

func TestFreshHandleSeesHistoryUnshallowedAfterAnotherHandleLoaded(t *testing.T) {
	origin, _ := initRepo(t)
	first := strings.TrimSpace(runGit(t, origin, "rev-parse", "HEAD"))
	commitHistoryFile(t, origin, "second.txt")
	clone := filepath.Join(t.TempDir(), "shallow")
	runGit(t, filepath.Dir(clone), "clone", "-q", "--depth", "1", "file://"+origin, clone)
	head := strings.TrimSpace(runGit(t, clone, "rev-parse", "HEAD"))
	ctx := t.Context()

	loader := &LocalGitx{Dir: clone}
	isReachable, err := loader.IsReachableFromHead(ctx, head)
	require.NoError(t, err)
	require.True(t, isReachable)

	runGit(t, clone, "fetch", "-q", "--unshallow")
	require.Equal(t, head, strings.TrimSpace(runGit(t, clone, "rev-parse", "HEAD")))

	isReachable, err = (&LocalGitx{Dir: clone}).IsReachableFromHead(ctx, first)
	require.NoError(t, err)
	assert.True(t, isReachable, "the unshallowed history reaches the first commit")
	runtime.KeepAlive(loader)
}

func TestFreshHandleSeesParentsReplacedAfterAnotherHandleLoaded(t *testing.T) {
	root, _ := initRepo(t)
	first := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD"))
	second := commitHistoryFile(t, root, "second.txt")
	ctx := t.Context()

	loader := &LocalGitx{Dir: root}
	isReachable, err := loader.IsReachableFromHead(ctx, first)
	require.NoError(t, err)
	require.True(t, isReachable)

	runGit(t, root, "replace", "--graft", second)
	require.Equal(t, second, strings.TrimSpace(runGit(t, root, "rev-list", "--parents", "HEAD")),
		"git itself now lists HEAD without a parent")

	isReachable, err = (&LocalGitx{Dir: root}).IsReachableFromHead(ctx, first)
	require.NoError(t, err)
	assert.False(t, isReachable, "the graft cuts the first commit off HEAD")
	runtime.KeepAlive(loader)
}
