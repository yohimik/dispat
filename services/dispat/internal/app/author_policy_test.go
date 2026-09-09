// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/config"
)

func TestAuthorArgumentBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		boundary int
		cleanup  string
		dry      bool
	}{
		{"literal terminator message", []string{"-m", "--", "--cleanup", "strip", "--", "--no-verify"}, 4, "strip", false},
		{"clustered message", []string{"-amfeat(core): add", "--cleanup=verbatim"}, 2, "verbatim", false},
		{"separate values", []string{"--author", "--no-verify", "-m", "feat(core): add"}, 4, "", false},
		{"reuse value", []string{"-C", "-n", "-Skey", "--edit"}, 4, "", false},
		{"porcelain implies dry run", []string{"-m", "feat(core): add", "--porcelain=v1"}, 3, "", true},
		{"dry run", []string{"--dry-run", "--message=feat(core): add", "-uall"}, 3, "", true},
		{"path operands", []string{"-m", "feat(core): add", "file", "-"}, 4, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := inspectAuthorArgs(tc.args)
			require.NoError(t, err)
			assert.Equal(t, tc.boundary, got.boundary)
			assert.Equal(t, tc.cleanup, got.cleanup)
			assert.Equal(t, tc.dry, got.dryRun)
		})
	}
	for _, args := range [][]string{{"-anmfeat(core): bypass"}, {"--no-verify"}, {"--no-ver"}, {"--no-no-verify"}, {"--future-option"}, {"--message"}, {"-am"}, {"-Q"}} {
		_, err := inspectAuthorArgs(args)
		require.Error(t, err, "%q", args)
	}
}

func TestAuthorConfigurationPolicy(t *testing.T) {
	root := t.TempDir()
	cmd := exec.Command("git", "init", "--quiet", root)
	require.NoError(t, cmd.Run())
	a := New(root, &config.File{}, zerolog.Nop())
	gitConfig := func(key, value string) {
		t.Helper()
		require.NoError(t, exec.Command("git", "-C", root, "config", key, value).Run())
	}
	for _, mode := range []string{"default", "strip", "whitespace", "verbatim", "scissors"} {
		gitConfig("commit.cleanup", mode)
		got, err := a.commitCleanupMode(context.Background(), nil)
		require.NoError(t, err)
		assert.Equal(t, mode, got)
	}
	gitConfig("commit.cleanup", "bad")
	_, err := a.commitCleanupMode(context.Background(), nil)
	require.ErrorContains(t, err, "unsupported git cleanup")
	got, err := a.commitCleanupMode(context.Background(), []string{"--cleanup=strip"})
	require.NoError(t, err)
	assert.Equal(t, "strip", got)
	gitConfig("core.commentChar", "auto")
	_, err = a.commitCleanupMode(context.Background(), []string{"--cleanup=strip"})
	require.ErrorContains(t, err, "explicit core.commentChar")
	_, err = a.commitCleanupMode(context.Background(), []string{"--cleanup=verbatim"})
	require.NoError(t, err)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = a.commitCleanupMode(cancelled, nil)
	require.Error(t, err)
	_, err = a.commitCleanupMode(context.Background(), []string{"--no-ver"})
	require.Error(t, err)
}

func TestAuthorHookProxiesPreserveOriginalLocation(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "original hooks")
	target := filepath.Join(root, "proxies")
	require.NoError(t, os.Mkdir(source, 0700))
	require.NoError(t, os.Mkdir(filepath.Join(source, "hook-support"), 0700))
	hook := filepath.Join(source, "pre-commit")
	require.NoError(t, os.WriteFile(hook, []byte("#!/bin/sh\ncat \"$(dirname \"$0\")/payload\"\n"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(source, "payload"), []byte("original support file"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(source, "commit-msg"), []byte("separately composed"), 0700))
	require.NoError(t, copyHooks(source, target))
	out, err := exec.Command(filepath.Join(target, "pre-commit")).Output()
	require.NoError(t, err)
	assert.Equal(t, "original support file", string(out))
	_, err = os.Stat(filepath.Join(target, "commit-msg"))
	require.True(t, os.IsNotExist(err))
	require.NoError(t, copyHooks(filepath.Join(root, "absent"), filepath.Join(root, "empty")))
	require.Error(t, copyHooks(source, hook))
	require.Error(t, copyHooks(hook, filepath.Join(root, "bad-source")))
	assert.False(t, executable(root))
	assert.False(t, executable(filepath.Join(root, "absent")))
	// A colliding directory must fail proxy creation instead of silently losing a hook.
	collision := filepath.Join(root, "collision")
	require.NoError(t, os.MkdirAll(filepath.Join(collision, "pre-commit"), 0700))
	require.Error(t, copyHooks(source, collision))
	original, err := os.ReadFile(filepath.Join(source, "payload"))
	require.NoError(t, err)
	assert.Equal(t, "original support file", string(original))
}

func TestWindowsHookPolicyPreservesExtensionFallback(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "hooks")
	target := filepath.Join(root, "proxy")
	require.NoError(t, os.Mkdir(source, 0o700))
	plain := filepath.Join(source, "pre-commit")
	fallback := filepath.Join(source, "pre-commit.exe")
	commitFallback := filepath.Join(source, "commit-msg.exe")
	require.NoError(t, os.WriteFile(plain, []byte("plain"), 0o600))
	require.NoError(t, os.WriteFile(fallback, []byte("fallback"), 0o600))
	require.NoError(t, os.WriteFile(commitFallback, []byte("commit fallback"), 0o600))

	assert.False(t, hookExecutable(plain, false), "Unix still requires execute bits")
	assert.True(t, hookExecutable(plain, true), "Git for Windows accepts regular hook files")
	assert.Equal(t, plain, findHook(source, "pre-commit", true), "extensionless hook has precedence")
	require.NoError(t, copyHooksForPlatform(source, target, true))
	preferred, err := os.ReadFile(filepath.Join(target, "pre-commit"))
	require.NoError(t, err)
	assert.Contains(t, string(preferred), shellQuote(filepath.ToSlash(plain)))
	assert.NotContains(t, string(preferred), filepath.ToSlash(fallback))
	require.NoError(t, os.Remove(plain))
	assert.Equal(t, fallback, findHook(source, "pre-commit", true), ".exe is the Windows fallback")
	assert.Empty(t, findHook(source, "pre-commit", false))
	assert.Equal(t, commitFallback, findHook(source, "commit-msg", true))

	require.NoError(t, copyHooksForPlatform(source, target, true))
	proxy, err := os.ReadFile(filepath.Join(target, "pre-commit"))
	require.NoError(t, err)
	assert.Contains(t, string(proxy), filepath.ToSlash(fallback))
	_, err = os.Stat(filepath.Join(target, "pre-commit.exe"))
	assert.True(t, os.IsNotExist(err), "the fallback is normalized to Git's hook name")
	_, err = os.Stat(filepath.Join(target, "commit-msg"))
	assert.True(t, os.IsNotExist(err), "commit-msg is composed with validation separately")
}

func TestAuthorOutputBoundAndQuoting(t *testing.T) {
	var out limitedOutput
	n, err := out.Write([]byte("start"))
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	n, err = out.Write([]byte(strings.Repeat("x", 65536)))
	require.NoError(t, err)
	assert.Equal(t, 65536, n)
	assert.Len(t, out.String(), 65536)
	assert.True(t, out.over)
	_, err = out.Write([]byte("tail"))
	require.NoError(t, err)
	assert.Len(t, out.String(), 65536)
	value := "path 'with spaces' $no_expand `no_execute`"
	result, err := exec.Command("sh", "-c", "printf '%s' "+shellQuote(value)).Output()
	require.NoError(t, err)
	assert.Equal(t, value, string(result))
}

func TestAuthorSetupFailuresDoNotCreateACommitOrLeakWrappers(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, exec.Command("git", "init", "--quiet", root).Run())
	a := New(root, &config.File{}, zerolog.Nop())
	ctx := context.Background()
	require.NoError(t, exec.Command("git", "-C", root, "config", "commit.cleanup", "unknown").Run())
	require.ErrorContains(t, a.AuthorCommit(ctx, []string{"-m", "feat(core): add"}, io.Discard, io.Discard), "unsupported git cleanup")
	require.NoError(t, exec.Command("git", "-C", root, "config", "commit.cleanup", "default").Run())
	scratch := t.TempDir()
	t.Setenv("TMPDIR", scratch)
	// A configured hook path that names a file is invalid, not an empty hook directory.
	hooks := filepath.Join(root, "hooks-file")
	require.NoError(t, os.WriteFile(hooks, nil, 0600))
	require.NoError(t, exec.Command("git", "-C", root, "config", "core.hooksPath", hooks).Run())
	require.Error(t, a.AuthorCommit(ctx, []string{"-m", "feat(core): add"}, io.Discard, io.Discard))
	entries, err := os.ReadDir(scratch)
	require.NoError(t, err)
	assert.Empty(t, entries)
	require.NoError(t, exec.Command("git", "-C", root, "config", "--unset", "core.hooksPath").Run())
	t.Setenv("TMPDIR", filepath.Join(scratch, "missing"))
	require.Error(t, a.AuthorCommit(ctx, []string{"-m", "feat(core): add"}, io.Discard, io.Discard))
	_, err = exec.Command("git", "-C", root, "rev-parse", "--verify", "HEAD").Output()
	require.Error(t, err)
}

func TestAuthorRejectsUnboundedGitMetadata(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "git")
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\nhead -c 70000 /dev/zero\n"), 0700))
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	a := New(root, &config.File{}, zerolog.Nop())
	text, err := a.gitOutput(context.Background(), "config", "--get", "commit.cleanup")
	require.ErrorContains(t, err, "exceeds 64 KiB")
	assert.Empty(t, text)
	assert.Equal(t, "invalid commit message (2 parser errors)", (&CommitMessageError{Errors: 2}).Error())
}

func TestAuthorRefusesNonRepositoryAndBypassBeforeCreatingHooks(t *testing.T) {
	root := t.TempDir()
	scratch := t.TempDir()
	t.Setenv("TMPDIR", scratch)
	// Isolate Git configuration so an unrelated user setting cannot change
	// which preflight operation detects the missing repository.
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	a := New(root, &config.File{}, zerolog.Nop())
	err := a.AuthorCommit(context.Background(), []string{"-m", "feat(core): add"}, io.Discard, io.Discard)
	require.ErrorContains(t, err, "resolve git hooks")
	err = a.AuthorCommit(context.Background(), []string{"-nm", "feat(core): add"}, io.Discard, io.Discard)
	require.ErrorContains(t, err, "bypasses commit-msg validation")
	entries, err := os.ReadDir(scratch)
	require.NoError(t, err)
	assert.Empty(t, entries)
}
