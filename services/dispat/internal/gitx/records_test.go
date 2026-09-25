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

// TestParseRemoteTagInventoryReadsWhatEachNameRecords: an annotated tag is
// advertised twice and only the peeled line names the commit, which is what a
// record is compared by. Anything that is not a tag advertisement is not one.
func TestParseRemoteTagInventoryReadsWhatEachNameRecords(t *testing.T) {
	const (
		annotated = "1111111111111111111111111111111111111111"
		commit    = "2222222222222222222222222222222222222222"
		light     = "3333333333333333333333333333333333333333"
		head      = "4444444444444444444444444444444444444444"
	)
	out := annotated + "\trefs/tags/core@0.1.0\n" +
		commit + "\trefs/tags/core@0.1.0^{}\n" +
		light + "\trefs/tags/v1\n" +
		head + "\trefs/heads/main\n" +
		"not a line\n" +
		"zzzz\trefs/tags/broken\n"

	entries := parseRemoteTagInventory(out)

	require.Len(t, entries, 2, "one entry per tag name; branches and malformed lines are not records")
	assert.Equal(t, "core@0.1.0", entries[0].name, "entries come in name order")
	assert.Equal(t, commit, entries[0].commit, "the peeled object is what the record names")
	assert.Equal(t, "v1", entries[1].name)
	assert.Equal(t, light, entries[1].commit, "a lightweight tag names its object directly")
}

// TestRemoteReleaseTagsReadsOnlyTheWorkspacesOwnFormats: a remote holds
// everybody's tags. Only the ones a configured package's format could have
// written are records of this workspace, and the coordination refs never are.
func TestRemoteReleaseTagsReadsOnlyTheWorkspacesOwnFormats(t *testing.T) {
	root, cli := initRepo(t)
	ctx := context.Background()
	addBareRemote(t, root)
	head, err := cli.HeadSHA(ctx)
	require.NoError(t, err)

	for _, name := range []string{"core@0.1.0", "other@2.0.0", "v1", LockTagName, LockAttemptTagPrefix + "abc"} {
		require.NoError(t, cli.CreateTag(ctx, name, "release "+name, ""))
	}
	_, err = cli.PushReleaseRefs(ctx, "origin", []ReleaseRef{
		{Name: "core@0.1.0"}, {Name: "other@2.0.0"}, {Name: "v1"},
		{Name: LockTagName}, {Name: LockAttemptTagPrefix + "abc"},
	})
	require.NoError(t, err)

	stored, err := cli.RemoteReleaseTags(ctx, "origin", map[string]TagFormat{"core": DefaultTagFormat})
	require.NoError(t, err)

	require.Len(t, stored["core"], 1, "one package, one record: %v", stored)
	assert.Equal(t, "core@0.1.0", stored["core"][0].Name)
	assert.Equal(t, head, stored["core"][0].Commit, "peeled to the commit it records")
	assert.True(t, stored["core"][0].Parsed)
}

// TestPushReleaseRefsAnswersARewrittenAnnotationAsAlreadyRecorded: two runs
// annotating one commit write two different tag objects, and the remote
// refuses the second. That is the retry of a write whose answer was lost
// (§19.4), not a conflict: the record already names this release's commit.
func TestPushReleaseRefsAnswersARewrittenAnnotationAsAlreadyRecorded(t *testing.T) {
	root, cli := initRepo(t)
	ctx := context.Background()
	bare := addBareRemote(t, root)

	require.NoError(t, cli.CreateTag(ctx, "core@0.1.0", "release core@0.1.0", ""))
	outcomes, err := cli.PushReleaseRefs(ctx, "origin", []ReleaseRef{{Name: "core@0.1.0"}})
	require.NoError(t, err)
	require.Equal(t, []RefOutcome{{Name: "core@0.1.0", Result: RefCreated}}, outcomes)
	stored := remoteTagObject(t, bare, "core@0.1.0")

	require.NoError(t, cli.CreateTagForce(ctx, "core@0.1.0", "written again", ""))
	outcomes, err = cli.PushReleaseRefs(ctx, "origin", []ReleaseRef{{Name: "core@0.1.0"}})
	require.NoError(t, err)
	require.Len(t, outcomes, 1)
	assert.Equal(t, RefExisting, outcomes[0].Result, "the same commit is the same record")
	assert.Equal(t, stored, remoteTagObject(t, bare, "core@0.1.0"),
		"and the annotation the remote published is not replaced by the retry")
}

// TestPushReleaseRefsReportsARecordTheRemoteDeclinedAsAFailedPush: an update
// hook that declines one release tag leaves the remote without it, which is
// no record at another commit but the push failing: the error wraps
// ErrRemoteRefused and names the remote's reason, while the alias the same
// push carried is still answered as created.
func TestPushReleaseRefsReportsARecordTheRemoteDeclinedAsAFailedPush(t *testing.T) {
	root, cli := initRepo(t)
	ctx := context.Background()
	bare := addBareRemote(t, root)
	hook := "#!/bin/sh\ncase \"$1\" in refs/tags/core@*) echo \"no release tags here\" >&2; exit 1 ;; esac\n"
	require.NoError(t, os.WriteFile(filepath.Join(bare, "hooks", "update"), []byte(hook), 0o755))

	require.NoError(t, cli.CreateTag(ctx, "core@0.1.0", "release core@0.1.0", ""))
	require.NoError(t, cli.CreateTag(ctx, "v0", "moving alias", ""))
	outcomes, err := cli.PushReleaseRefs(ctx, "origin", []ReleaseRef{{Name: "core@0.1.0"}, {Name: "v0", IsMoving: true}})
	require.ErrorIs(t, err, ErrRemoteRefused)
	assert.ErrorContains(t, err, "refs/tags/core@0.1.0")
	assert.ErrorContains(t, err, "[remote rejected] (hook declined)", "the remote's reason is named")
	assert.Equal(t, []RefOutcome{{Name: "v0", Result: RefCreated}}, outcomes,
		"the alias landed and is answered, and the declined record is no outcome at all")
	listed, err := exec.Command("git", "-C", bare, "tag", "--list", "core@*").Output()
	require.NoError(t, err)
	assert.Empty(t, strings.TrimSpace(string(listed)), "the remote holds no record")
}

// TestPushReleaseRefsRefusesANameGitWouldNotTake: a ref name is built from
// configured text, so it is checked before it reaches a command line.
func TestPushReleaseRefsRefusesANameGitWouldNotTake(t *testing.T) {
	root, cli := initRepo(t)
	addBareRemote(t, root)

	_, err := cli.PushReleaseRefs(context.Background(), "origin", []ReleaseRef{{Name: "core@0.1.0 "}})
	require.Error(t, err)
}

// TestIsReachableFromHeadAnswersFromTheHeadHistory: the question the record
// comparison asks of every record the checkout lacks. A commit off the head,
// and one the checkout does not hold at all, both answer no, and neither
// costs a git process of its own.
func TestIsReachableFromHeadAnswersFromTheHeadHistory(t *testing.T) {
	root, cli := initRepo(t)
	ctx := context.Background()
	head, err := cli.HeadSHA(ctx)
	require.NoError(t, err)

	reachable, err := cli.IsReachableFromHead(ctx, head)
	require.NoError(t, err)
	assert.True(t, reachable, "the head reaches itself")

	reachable, err = cli.IsReachableFromHead(ctx, "")
	require.NoError(t, err)
	assert.False(t, reachable, "an empty commit is no commit")

	reachable, err = cli.IsReachableFromHead(ctx, "5555555555555555555555555555555555555555")
	require.NoError(t, err)
	assert.False(t, reachable, "a commit the checkout does not hold is not in its history")

	// A commit this checkout holds on a branch of its own is still off the
	// head the plan is computed from.
	runGit(t, root, "checkout", "-q", "-b", "elsewhere")
	require.NoError(t, os.WriteFile(filepath.Join(root, "other.txt"), []byte("x"), 0o644))
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-qm", "feat(core): elsewhere")
	elsewhere, err := cli.HeadSHA(ctx)
	require.NoError(t, err)
	runGit(t, root, "checkout", "-q", "-")

	// A fresh handle: the graph is loaded once per handle, and the branch
	// moved after the one above had already read it.
	fresh := &LocalGitx{Dir: root}
	reachable, err = fresh.IsReachableFromHead(ctx, elsewhere)
	require.NoError(t, err)
	assert.False(t, reachable, "another branch's commit cannot be reachable from this head")
}
