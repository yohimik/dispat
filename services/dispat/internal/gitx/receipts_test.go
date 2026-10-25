// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReceiptMessageRoundTrip(t *testing.T) {
	r := Receipt{Tag: "core@1.2.0", Package: "core", Version: "1.2.0", PlannedHead: "abc123", Integrity: "sha512-abc"}
	message, err := formatReceiptMessage(r)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(message, receiptHeader+"\n\n"))
	parsed, err := ParseReceiptMessage(message)
	require.NoError(t, err)
	assert.Equal(t, r, parsed)

	_, err = formatReceiptMessage(Receipt{Tag: "core@1.2.0", Package: "core", Version: "1.2.0", Integrity: "a\nb"})
	assert.ErrorContains(t, err, "line break")
	_, err = ParseReceiptMessage("release core@1.2.0\n")
	assert.ErrorContains(t, err, "not a dispat receipt")
	_, err = ParseReceiptMessage(receiptHeader + "\n\npackage: core\n")
	assert.ErrorContains(t, err, "names no package, version, tag or planned head")
}

// TestReceiptLifecycle walks a receipt through every state a run puts it in:
// written once and answered as written on a retry, pushed create-only and
// answered as already there on a second push, read back after a fetch into a
// fresh clone, and removed from both sides under a lease.
func TestReceiptLifecycle(t *testing.T) {
	root, cli := initRepo(t)
	ctx := context.Background()
	bare := addBareRemote(t, root)
	head, err := cli.HeadSHA(ctx)
	require.NoError(t, err)

	r, err := cli.WriteReceipt(ctx, Receipt{Tag: "core@0.1.0", Package: "core", Version: "0.1.0", Commit: head})
	require.NoError(t, err)
	require.NotEmpty(t, r.Object)
	again, err := cli.WriteReceipt(ctx, Receipt{Tag: "core@0.1.0", Package: "core", Version: "0.1.0", Commit: head})
	require.NoError(t, err)
	assert.Equal(t, r.Object, again.Object, "a retry writes nothing new")

	outcome, err := cli.PushReceipt(ctx, "origin", r)
	require.NoError(t, err)
	assert.Equal(t, RefCreated, outcome.Result)
	outcome, err = cli.PushReceipt(ctx, "origin", r)
	require.NoError(t, err)
	assert.Equal(t, RefExisting, outcome.Result)

	clone := filepath.Join(t.TempDir(), "clone")
	out, err := exec.Command("git", "clone", "-q", bare, clone).CombinedOutput()
	require.NoError(t, err, "%s", out)
	fresh := &LocalGitx{Dir: clone}
	none, err := fresh.Receipts(ctx)
	require.NoError(t, err)
	assert.Empty(t, none, "a clone does not carry receipts until it fetches them")
	require.NoError(t, fresh.FetchReceipts(ctx, "origin"))
	read, err := fresh.Receipts(ctx)
	require.NoError(t, err)
	require.Len(t, read, 1)
	assert.Equal(t, r, read[0])

	require.NoError(t, fresh.DeleteReceipt(ctx, "origin", read[0]))
	stored, err := cli.remotePeeledRef(ctx, "origin", r.Ref())
	require.NoError(t, err)
	assert.Empty(t, stored)
	left, err := fresh.Receipts(ctx)
	require.NoError(t, err)
	assert.Empty(t, left)
	require.NoError(t, cli.DeleteReceipt(ctx, "origin", r), "a remote that no longer holds it is a delete already done")
}

func TestReceiptRefusesAnotherCommit(t *testing.T) {
	root, cli := initRepo(t)
	ctx := context.Background()
	addBareRemote(t, root)
	first, err := cli.HeadSHA(ctx)
	require.NoError(t, err)
	r, err := cli.WriteReceipt(ctx, Receipt{Tag: "core@0.1.0", Package: "core", Version: "0.1.0", Commit: first})
	require.NoError(t, err)
	_, err = cli.PushReceipt(ctx, "origin", r)
	require.NoError(t, err)

	out, err := exec.Command("git", "-C", root, "commit", "-q", "--allow-empty", "-m", "chore: second").CombinedOutput()
	require.NoError(t, err, "%s", out)
	second, err := cli.HeadSHA(ctx)
	require.NoError(t, err)
	_, err = cli.WriteReceipt(ctx, Receipt{Tag: "core@0.1.0", Package: "core", Version: "0.1.0", Commit: second})
	assert.ErrorContains(t, err, "different publication identity")
	_, err = cli.WriteReceipt(ctx, Receipt{Tag: "core@0.1.0", Package: "core", Version: "0.1.0",
		Commit: first, Integrity: "sha512-different"})
	assert.ErrorContains(t, err, "different publication identity",
		"the same commit does not make two different artefacts one publication")

	// Another clone's receipt for the same tag at another commit.
	other := t.TempDir()
	out, err = exec.Command("git", "clone", "-q", root, other).CombinedOutput()
	require.NoError(t, err, "%s", out)
	theirs := &LocalGitx{Dir: other, Name: "Test", Email: "test@example.com"}
	out, err = exec.Command("git", "-C", other, "-c", "user.name=x", "-c", "user.email=x@x", "commit", "-q", "--allow-empty", "-m", "chore: theirs").CombinedOutput()
	require.NoError(t, err, "%s", out)
	theirHead, err := theirs.HeadSHA(ctx)
	require.NoError(t, err)
	foreign, err := theirs.WriteReceipt(ctx, Receipt{Tag: "core@0.1.0", Package: "core", Version: "0.1.0", Commit: theirHead})
	require.NoError(t, err)
	remote, err := cli.RemoteURL(ctx, "origin")
	require.NoError(t, err)
	outcome, err := theirs.PushReceipt(ctx, remote, foreign)
	require.NoError(t, err)
	assert.Equal(t, RefAtOtherCommit, outcome.Result)
	assert.Equal(t, first, outcome.Commit)

	assert.ErrorIs(t, theirs.DeleteReceipt(ctx, remote, foreign), ErrLeaseRejected,
		"a remote receipt this checkout did not write is left where it is")
}

func TestFetchReceiptsPrunesDeletedRemoteReceipts(t *testing.T) {
	root, cli := initRepo(t)
	ctx := context.Background()
	addBareRemote(t, root)
	head, err := cli.HeadSHA(ctx)
	require.NoError(t, err)
	receipt, err := cli.WriteReceipt(ctx, Receipt{Tag: "core@0.1.0", Package: "core", Version: "0.1.0", Commit: head})
	require.NoError(t, err)
	_, err = cli.PushReceipt(ctx, "origin", receipt)
	require.NoError(t, err)

	// Another run removed the remote ref. A checkout reused for the next run
	// must not keep treating its stale local copy as a publication.
	out, err := exec.Command("git", "-C", root, "push", "-q", "origin", ":"+receipt.Ref()).CombinedOutput()
	require.NoError(t, err, "%s", out)
	require.NoError(t, cli.FetchReceipts(ctx, "origin"))
	remaining, err := cli.Receipts(ctx)
	require.NoError(t, err)
	assert.Empty(t, remaining)
}

func TestPushReceiptRefusesAnotherIdentityAtTheSameCommit(t *testing.T) {
	root, cli := initRepo(t)
	ctx := context.Background()
	bare := addBareRemote(t, root)
	head, err := cli.HeadSHA(ctx)
	require.NoError(t, err)
	first, err := cli.WriteReceipt(ctx, Receipt{Tag: "core@0.1.0", Package: "core", Version: "0.1.0",
		Commit: head, Integrity: "sha512-first"})
	require.NoError(t, err)
	_, err = cli.PushReceipt(ctx, "origin", first)
	require.NoError(t, err)

	otherDir := filepath.Join(t.TempDir(), "other")
	out, err := exec.Command("git", "clone", "-q", bare, otherDir).CombinedOutput()
	require.NoError(t, err, "%s", out)
	other := &LocalGitx{Dir: otherDir, Name: "Test", Email: "test@example.com"}
	second, err := other.WriteReceipt(ctx, Receipt{Tag: "core@0.1.0", Package: "core", Version: "0.1.0",
		Commit: head, Integrity: "sha512-second"})
	require.NoError(t, err)
	_, err = other.PushReceipt(ctx, bare, second)
	assert.ErrorIs(t, err, ErrRemoteRefused)
	assert.ErrorContains(t, err, "different receipt object")
	stored, err := cli.remoteRefObject(ctx, "origin", first.Ref())
	require.NoError(t, err)
	assert.Equal(t, first.Object, stored)
}

func TestReceiptsSkipWhatDispatDidNotWrite(t *testing.T) {
	root, cli := initRepo(t)
	ctx := context.Background()
	head, err := cli.HeadSHA(ctx)
	require.NoError(t, err)
	for _, args := range [][]string{
		{"update-ref", ReceiptRefPrefix + "plain@1.0.0", head},
		{"tag", "-a", "-m", "release other", "scratch"},
	} {
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		require.NoError(t, err, "%s", out)
	}
	out, err := exec.Command("git", "-C", root, "update-ref", ReceiptRefPrefix+"other@1.0.0", "refs/tags/scratch").CombinedOutput()
	require.NoError(t, err, "%s", out)
	read, err := cli.Receipts(ctx)
	require.NoError(t, err)
	assert.Empty(t, read, "a commit ref and a foreign annotation are not receipts")
}

func TestPushReceiptReportsADeclinedWrite(t *testing.T) {
	root, cli := initRepo(t)
	ctx := context.Background()
	bare := addBareRemote(t, root)
	hook := "#!/bin/sh\ncase \"$1\" in refs/dispat/*) echo \"no receipts here\" >&2; exit 1 ;; esac\n"
	require.NoError(t, os.WriteFile(filepath.Join(bare, "hooks", "update"), []byte(hook), 0o755))
	head, err := cli.HeadSHA(ctx)
	require.NoError(t, err)
	r, err := cli.WriteReceipt(ctx, Receipt{Tag: "core@0.1.0", Package: "core", Version: "0.1.0", Commit: head})
	require.NoError(t, err)
	_, err = cli.PushReceipt(ctx, "origin", r)
	assert.ErrorIs(t, err, ErrRemoteRefused)
	assert.ErrorContains(t, cli.FetchReceipts(ctx, "nowhere"), "fetching the receipts")
	_, err = cli.WriteReceipt(ctx, Receipt{Tag: "bad..name", Package: "core", Version: "0.1.0", Commit: head})
	assert.Error(t, err)
}
