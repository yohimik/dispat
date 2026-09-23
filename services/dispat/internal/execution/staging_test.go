// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// Choosing where an admitted set is assembled: the private Git directory
// first, and a hidden folder beside the outermost checkout when the final
// rename out of the private Git directory fails.

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// recordedInstall stands in for InstallOutputs: it answers one scripted
// result per attempt and remembers where each attempt was staged.
type recordedInstall struct {
	results []error
	staged  []string
}

func (r *recordedInstall) install(_ context.Context, request InstallRequest) error {
	r.staged = append(r.staged, request.Staging)
	return r.results[len(r.staged)-1]
}

// nestedStagingSpec is a checkout inside another checkout, which is what an
// orchestrated source inside its control repository looks like: the second
// staging folder has to leave both.
func nestedStagingSpec(t *testing.T) (outputStagingSpec, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	outer := filepath.Join(base, "control")
	owner := filepath.Join(outer, "sources", "lib")
	require.NoError(t, os.MkdirAll(owner, 0o755))
	for _, dir := range []string{outer, owner} {
		out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput()
		require.NoError(t, err, "%s", out)
	}
	return outputStagingSpec{
		indexPath: filepath.Join(owner, ".git", "index"), ownerDir: owner,
		run: "run-1", packageName: "lib",
	}, base
}

// TestStagedInstallAssemblesBesideTheCheckoutWhenTheRenameFails: a rename out
// of the private Git directory that fails is not the end of the install. The
// set is assembled again in a hidden folder beside the outermost checkout,
// outside every repository that could record it, and that attempt's answer is
// the install's.
func TestStagedInstallAssemblesBesideTheCheckoutWhenTheRenameFails(t *testing.T) {
	spec, base := nestedStagingSpec(t)
	attempts := &recordedInstall{results: []error{
		&stagedRenameError{err: errors.New("rename: invalid cross-device link")}, nil,
	}}

	err := installStaged(t.Context(), spec, InstallRequest{Log: zerolog.Nop()}, attempts.install)

	require.NoError(t, err)
	require.Len(t, attempts.staged, 2)
	assert.Equal(t, filepath.Dir(spec.indexPath), filepath.Dir(attempts.staged[0]),
		"the first attempt is staged in the private Git directory")
	assert.Equal(t, base, filepath.Dir(attempts.staged[1]),
		"the second is staged beside the outermost checkout, not inside either repository")
	assert.True(t, strings.HasPrefix(filepath.Base(attempts.staged[1]), "."+formatIdentityHash(spec.ownerDir)+"-dispat-outputs-"),
		"hidden, and named after the owning checkout and the set: %s", attempts.staged[1])
}

// TestStagedInstallReportsEveryOtherFailureAsItIs: only the final rename is
// retried somewhere else. A set that fails verification, or a second rename
// that fails too, is the install's answer.
func TestStagedInstallReportsEveryOtherFailureAsItIs(t *testing.T) {
	spec, _ := nestedStagingSpec(t)

	t.Run("a failure before the rename is not retried", func(t *testing.T) {
		refused := refuseOutputs(ReasonContentDigest)
		attempts := &recordedInstall{results: []error{refused}}

		err := installStaged(t.Context(), spec, InstallRequest{Log: zerolog.Nop()}, attempts.install)

		require.ErrorIs(t, err, refused)
		assert.Len(t, attempts.staged, 1)
	})

	t.Run("a second rename that fails is reported", func(t *testing.T) {
		second := &stagedRenameError{err: errors.New("rename: the sibling cannot reach it either")}
		attempts := &recordedInstall{results: []error{
			&stagedRenameError{err: errors.New("rename: invalid cross-device link")}, second,
		}}

		err := installStaged(t.Context(), spec, InstallRequest{Log: zerolog.Nop()}, attempts.install)

		require.ErrorIs(t, err, second)
		assert.Len(t, attempts.staged, 2)
	})
}

// TestStagedInstallCrossesAFilesystemBoundary: the case the second folder
// exists for, on a real mount boundary. A linked worktree on another
// filesystem keeps its private Git directory with the checkout it was added
// to, so the rename out of it fails; the set is assembled again beside the
// worktree, installed, and nothing of either staging folder is left behind.
func TestStagedInstallCrossesAFilesystemBoundary(t *testing.T) {
	fixture := newOutputFixture(t, "packages/core")
	fixture.write(t, "dist/value.txt", "kept\n", 0o644)
	manifest, err := fixture.capture(t, []string{"dist"}, testLimits)
	require.NoError(t, err)
	parent, err := os.MkdirTemp("/dev/shm", "dispat-unit-worktree-")
	if err != nil {
		t.Skipf("a second writable filesystem is unavailable: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(parent) })
	probe := filepath.Join(fixture.dir, ".dispat-device-probe")
	require.NoError(t, os.WriteFile(probe, []byte("probe"), 0o600))
	if os.Rename(probe, filepath.Join(parent, ".dispat-device-probe")) == nil {
		t.Skip("the second folder shares the checkout's filesystem")
	}
	require.NoError(t, os.Remove(probe))
	linked := filepath.Join(parent, "linked")
	fixture.run(t, "worktree", "add", "--detach", linked, "HEAD")
	t.Cleanup(func() {
		_, _ = exec.Command("git", "-C", fixture.dir, "worktree", "remove", "--force", linked).CombinedOutput()
	})
	index, err := (&gitx.LocalGitx{Dir: linked, Log: zerolog.Nop()}).IndexPath(t.Context())
	require.NoError(t, err)
	destination := filepath.Join(linked, "packages", "core")

	err = installStaged(t.Context(), outputStagingSpec{
		indexPath: index, ownerDir: linked, run: "run-1", packageName: "core",
	}, InstallRequest{Git: fixture.git, Manifest: manifest, Dir: destination, Log: zerolog.Nop()}, InstallOutputs)

	require.NoError(t, err)
	assert.Equal(t, "kept\n", readInstalled(t, destination, "dist/value.txt"))
	entries, err := os.ReadDir(parent)
	require.NoError(t, err)
	require.Len(t, entries, 1, "only the linked checkout remains beside it")
	private, err := os.ReadDir(filepath.Dir(index))
	require.NoError(t, err)
	for _, entry := range private {
		assert.False(t, strings.HasPrefix(entry.Name(), "dispat-outputs-"), "the first staging folder is gone: %s", entry.Name())
	}
}
