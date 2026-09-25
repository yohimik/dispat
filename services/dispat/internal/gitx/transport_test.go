// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

// The transport against real repositories.
//
// Every claim here is about what Git does rather than about what this package
// believes Git does, so the fixtures are real bare remotes and real object
// stores: a lease that is refused is refused by the remote, and a first-parent
// chain is the one `git rev-list` walks.

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type shortRequestPipe struct{}

func (shortRequestPipe) Write(data []byte) (int, error) { return len(data) - 1, nil }
func (shortRequestPipe) Close() error                   { return nil }

// transportFixture is a repository with two commits and a bare remote to
// coordinate through, plus the two commit ids the scenarios push around.
type transportFixture struct {
	root   string
	git    *LocalGitx
	bare   string
	first  string
	second string
}

func newTransportFixture(t *testing.T) *transportFixture {
	t.Helper()
	root, git := initRepo(t)
	runGit(t, root, "commit", "-q", "--allow-empty", "-m", "second")
	bare := filepath.Join(t.TempDir(), "mailbox.git")
	out, err := exec.Command("git", "init", "-q", "--bare", bare).CombinedOutput()
	require.NoError(t, err, "git init --bare: %s", out)
	return &transportFixture{
		root: root, git: git, bare: bare,
		first:  strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD~1")),
		second: strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD")),
	}
}

func (f *transportFixture) remoteOID(t *testing.T, ref string) string {
	t.Helper()
	return strings.TrimSpace(runGit(t, f.bare, "rev-parse", "--verify", "-q", ref))
}

// TestFailedGitInvocationReturnsNoOutput: the streaming runner hands back what
// git printed even when git failed, because a leased push reports its answer
// that way, but every other caller was written against "no output beside an
// error". `rev-parse` on an unknown ref is the case that tells the two apart:
// git echoes the argument on standard output on its way to failing, and a
// caller that ignores the error must not mistake that echo for an object id.
func TestFailedGitInvocationReturnsNoOutput(t *testing.T) {
	_, git := initRepo(t)

	streamed, err := git.runStream(t.Context(), gitStream{}, "rev-parse", "refs/tags/absent")
	require.Error(t, err)
	require.NotEmpty(t, streamed, "the fixture no longer proves anything: git printed nothing before failing")

	out, err := git.run(t.Context(), "rev-parse", "refs/tags/absent")
	require.Error(t, err)
	assert.Empty(t, out, "run returns nothing beside an error")

	object, err := git.TagObject(t.Context(), "absent")
	require.Error(t, err)
	assert.Empty(t, object, "a tag that does not exist has no object id, whatever git echoed")
}

// TestTransportCreateOnlyAdmitsOneWriter: a create-only push succeeds once,
// a second party offering a different object under the same name is rejected
// rather than winning, and the same party re-offering the object it already
// put there succeeds, because a lost response must not look like a loss.
func TestTransportCreateOnlyAdmitsOneWriter(t *testing.T) {
	f := newTransportFixture(t)
	ctx := t.Context()

	require.NoError(t, f.git.PushCreate(ctx, f.bare, f.first, "dispat-worker-a-build"))
	assert.Equal(t, f.first, f.remoteOID(t, "refs/heads/dispat-worker-a-build"))

	err := f.git.PushCreate(ctx, f.bare, f.second, "dispat-worker-a-build")
	require.ErrorIs(t, err, ErrLeaseRejected, "a taken name is not this caller's to write")
	assert.Equal(t, f.first, f.remoteOID(t, "refs/heads/dispat-worker-a-build"),
		"a rejected create leaves the holder's branch alone")

	assert.NoError(t, f.git.PushCreate(ctx, f.bare, f.first, "dispat-worker-a-build"),
		"re-offering the object already there is this caller's own lost response")
}

// TestTransportAdvanceFollowsTheLease: a transition lands only from the value
// its writer read, which is what makes each step of the protocol a
// compare-and-swap rather than a hope.
func TestTransportAdvanceFollowsTheLease(t *testing.T) {
	f := newTransportFixture(t)
	ctx := t.Context()
	require.NoError(t, f.git.PushCreate(ctx, f.bare, f.first, "dispat-worker-a-publish"))

	err := f.git.PushAdvance(ctx, f.bare, f.second, "dispat-worker-a-publish", f.second)
	require.ErrorIs(t, err, ErrLeaseRejected)
	assert.Equal(t, f.first, f.remoteOID(t, "refs/heads/dispat-worker-a-publish"))

	require.NoError(t, f.git.PushAdvance(ctx, f.bare, f.second, "dispat-worker-a-publish", f.first))
	assert.Equal(t, f.second, f.remoteOID(t, "refs/heads/dispat-worker-a-publish"))
}

// TestTransportBatchedDeleteReportsEachRef: one push closes every branch a run
// owns, and a branch somebody else moved in the meantime is reported as
// retained instead of taking the whole batch down with it.
func TestTransportBatchedDeleteReportsEachRef(t *testing.T) {
	f := newTransportFixture(t)
	ctx := t.Context()
	for _, branch := range []string{"dispat-worker-a-1", "dispat-worker-a-2", "dispat-worker-a-3"} {
		require.NoError(t, f.git.PushCreate(ctx, f.bare, f.first, branch))
	}
	// The second branch moves under the run's feet, so the lease it carries is
	// no longer the value the remote holds.
	require.NoError(t, f.git.PushAdvance(ctx, f.bare, f.second, "dispat-worker-a-2", f.first))

	outcomes, err := f.git.DeleteRemoteBranchesLease(ctx, f.bare, []BranchLease{
		{Branch: "dispat-worker-a-1", ExpectedOld: f.first},
		{Branch: "dispat-worker-a-2", ExpectedOld: f.first},
		{Branch: "dispat-worker-a-3", ExpectedOld: f.first},
	})
	require.NoError(t, err)
	assert.Equal(t, []BranchOutcome{
		{Branch: "dispat-worker-a-1", Result: BranchDeleted},
		{Branch: "dispat-worker-a-2", Result: BranchStale},
		{Branch: "dispat-worker-a-3", Result: BranchDeleted},
	}, outcomes)

	heads, err := f.git.ListRemoteHeads(ctx, f.bare, "refs/heads/dispat-worker-a-*")
	require.NoError(t, err)
	require.Len(t, heads, 1)
	assert.Equal(t, RemoteHead{Name: "dispat-worker-a-2", OID: f.second}, heads[0])
}

// TestPushStatusTellsTheThreeOutcomesApart: a `!` line is three different
// answers, and the protocol acts on each differently. A lost lease and a
// server's refusal never applied the update; a remote failure, a refusal git
// does not name and a missing line may have. The reason a server gives is
// redacted, because a hook chooses its text.
func TestPushStatusTellsTheThreeOutcomesApart(t *testing.T) {
	const ref = "refs/heads/dispat-worker-a-1"
	for name, row := range map[string]struct {
		out        string
		isReported bool
		want       pushOutcome
		delete     BranchResult
	}{
		"a new ref":          {"*\tabc:" + ref + "\t[new branch]\n", true, pushApplied, BranchDeleted},
		"already there":      {"=\tabc:" + ref + "\t[up to date]\n", true, pushApplied, BranchDeleted},
		"a delete":           {"-\t:" + ref + "\t[deleted]\n", true, pushApplied, BranchDeleted},
		"a stale lease":      {"!\tabc:" + ref + "\t[rejected] (stale info)\n", true, pushStale, BranchStale},
		"fetch first":        {"!\tabc:" + ref + "\t[rejected] (fetch first)\n", true, pushStale, BranchStale},
		"an absent delete":   {"!\t(delete):" + ref + "\t[rejected] (stale info)\n", true, pushStale, BranchStale},
		"a hook":             {"!\tabc:" + ref + "\t[remote rejected] (hook declined)\n", true, pushRefused, BranchRefused},
		"a remote failure":   {"!\tabc:" + ref + "\t[remote failure] (remote failed to report status)\n", true, pushUnknown, BranchUnknown},
		"an unnamed refusal": {"!\tabc:" + ref + "\t[no match]\n", true, pushUnknown, BranchUnknown},
		"an unknown flag":    {"X\tabc:" + ref + "\t[strange]\n", true, pushUnknown, BranchUnknown},
		"another ref only":   {"*\tabc:refs/heads/other\t[new branch]\n", false, 0, 0},
		"no output at all":   {"", false, 0, 0},
	} {
		t.Run(name, func(t *testing.T) {
			status, isReported := findPushStatus(row.out, ref)
			require.Equal(t, row.isReported, isReported)
			if !isReported {
				return
			}
			assert.Equal(t, row.want, status.resolve())
			assert.Equal(t, row.delete, status.resolveDelete())
		})
	}

	status, isReported := findPushStatus("!\tabc:"+ref+
		"\t[remote rejected] (denied for https://bot:hunter2@git.example/acme.git)\n", ref)
	require.True(t, isReported)
	assert.NotContains(t, status.formatReason(), "hunter2", "a server's reason is redacted")
	assert.Contains(t, status.formatReason(), "[remote rejected] (denied for https://")
}

// TestTransportPushOutcomesAgainstARealRemote: what git itself answers for the
// shapes the protocol relies on. A lease over a ref that is already gone is a
// stale lease and nothing is sent; re-pushing the object a ref already holds
// is a success; a pre-receive hook is a refusal distinct from a lost lease; and
// a push that never reported the ref is an unknown outcome, whether or not git
// failed on its way out.
func TestTransportPushOutcomesAgainstARealRemote(t *testing.T) {
	f := newTransportFixture(t)
	ctx := t.Context()

	outcomes, err := f.git.DeleteRemoteBranchesLease(ctx, f.bare, []BranchLease{
		{Branch: "dispat-worker-a-gone", ExpectedOld: f.first},
	})
	require.NoError(t, err)
	assert.Equal(t, []BranchOutcome{{Branch: "dispat-worker-a-gone", Result: BranchStale}}, outcomes,
		"a lease over an absent ref is refused as stale")

	require.NoError(t, f.git.PushCreate(ctx, f.bare, f.first, "dispat-worker-a-same"))
	require.NoError(t, f.git.PushAdvance(ctx, f.bare, f.first, "dispat-worker-a-same", f.second),
		"the object the ref already holds is this caller's own earlier push")

	hook := filepath.Join(f.bare, "hooks", "pre-receive")
	require.NoError(t, os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755))
	err = f.git.PushCreate(ctx, f.bare, f.second, "dispat-worker-a-hooked")
	require.ErrorIs(t, err, ErrRemoteRefused)
	assert.NotErrorIs(t, err, ErrLeaseRejected, "a server's refusal is not a lost lease")
	assert.Contains(t, err.Error(), "pre-receive hook declined")
	outcomes, err = f.git.DeleteRemoteBranchesLease(ctx, f.bare, []BranchLease{
		{Branch: "dispat-worker-a-same", ExpectedOld: f.first},
	})
	require.NoError(t, err)
	assert.Equal(t, BranchRefused, outcomes[0].Result)
	require.NoError(t, os.Remove(hook))

	missing := filepath.Join(t.TempDir(), "absent.git")
	err = f.git.PushCreate(ctx, missing, f.first, "dispat-worker-a-lost")
	require.ErrorIs(t, err, ErrPushUnknown, "a remote that never answered says nothing about the ref")
	outcomes, err = f.git.DeleteRemoteBranchesLease(ctx, missing, []BranchLease{
		{Branch: "dispat-worker-a-lost", ExpectedOld: f.first},
	})
	require.NoError(t, err)
	assert.Equal(t, BranchUnknown, outcomes[0].Result)
}

// TestTransportListsAndBatchesWithinItsCeilings: the mailbox reads are
// bounded. A pattern matching nothing is an empty answer rather than a
// failure, and a batch larger than one command line may name is refused
// before git is asked at all.
func TestTransportListsAndBatchesWithinItsCeilings(t *testing.T) {
	f := newTransportFixture(t)
	ctx := t.Context()
	require.NoError(t, f.git.PushCreate(ctx, f.bare, f.first, "dispat-worker-b-1"))

	heads, err := f.git.ListRemoteHeads(ctx, f.bare, "refs/heads/dispat-worker-zz-*")
	require.NoError(t, err)
	assert.Empty(t, heads, "a mailbox with nothing for this node is not an error")

	oversized := make([]string, MaxTransportBatch+1)
	leases := make([]BranchLease, MaxTransportBatch+1)
	for i := range oversized {
		oversized[i] = "dispat-worker-b-" + strconv.Itoa(i)
		leases[i] = BranchLease{Branch: oversized[i], ExpectedOld: f.first}
	}
	assert.ErrorIs(t, f.git.FetchRefs(ctx, f.bare, oversized), ErrTransportLimit)
	_, err = f.git.DeleteRemoteBranchesLease(ctx, f.bare, leases)
	assert.ErrorIs(t, err, ErrTransportLimit)
}

// TestTransportRefusesARefNameGitWouldNot: a coordination branch name is a
// routing hint that came from a mailbox, so it is checked before it reaches a
// command line where a leading dash or a "..' would mean something else.
func TestTransportRefusesARefNameGitWouldNot(t *testing.T) {
	f := newTransportFixture(t)
	ctx := t.Context()
	for name, push := range map[string]func(string) error{
		"create":  func(branch string) error { return f.git.PushCreate(ctx, f.bare, f.first, branch) },
		"advance": func(branch string) error { return f.git.PushAdvance(ctx, f.bare, f.first, branch, "") },
		"fetch":   func(branch string) error { return f.git.FetchRefs(ctx, f.bare, []string{branch}) },
		"delete": func(branch string) error {
			_, err := f.git.DeleteRemoteBranchesLease(ctx, f.bare, []BranchLease{{Branch: branch}})
			return err
		},
		"local delete": func(branch string) error {
			return f.git.DeleteLocalTransportRefs(ctx, []string{branch})
		},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Error(t, push("dispat-worker-a-..build"))
		})
	}
}

// TestTransportResolvesOnlyWhatTheRefCarries: a fetched branch is read by
// resolving the exact object the protocol named, and the object has to be on
// that branch's first-parent chain within the depth the caller allows.
func TestTransportResolvesOnlyWhatTheRefCarries(t *testing.T) {
	f := newTransportFixture(t)
	ctx := t.Context()
	// A side line merged into the mailbox branch: its tip is reachable from
	// the branch but only as a second parent, which is not where a
	// coordination step ever writes.
	branch := strings.TrimSpace(runGit(t, f.root, "rev-parse", "--abbrev-ref", "HEAD"))
	runGit(t, f.root, "checkout", "-q", "-b", "side", f.first)
	runGit(t, f.root, "commit", "-q", "--allow-empty", "-m", "side work")
	side := strings.TrimSpace(runGit(t, f.root, "rev-parse", "HEAD"))
	runGit(t, f.root, "checkout", "-q", branch)
	runGit(t, f.root, "merge", "-q", "--no-ff", "-m", "merge side", side)
	merged := strings.TrimSpace(runGit(t, f.root, "rev-parse", "HEAD"))
	require.NoError(t, f.git.PushCreate(ctx, f.bare, merged, "dispat-worker-c-relay"))

	require.NoError(t, f.git.FetchRefs(ctx, f.bare, []string{"dispat-worker-c-relay"}))
	local := TransportRefPrefix + "dispat-worker-c-relay"

	assert.NoError(t, f.git.ResolveFetchedCommit(ctx, local, merged, 1), "the tip itself")
	assert.NoError(t, f.git.ResolveFetchedCommit(ctx, local, f.second, 8), "a first-parent ancestor")
	assert.ErrorIs(t, f.git.ResolveFetchedCommit(ctx, local, side, 8), ErrCommitNotOnRef,
		"a second parent is reachable and is still not a step of this chain")
	assert.ErrorIs(t, f.git.ResolveFetchedCommit(ctx, local, f.first, 1), ErrCommitNotOnRef,
		"an ancestor beyond the allowed depth")
	assert.ErrorIs(t, f.git.ResolveFetchedCommit(ctx, local, strings.Repeat("a", 40), 8), ErrCommitNotOnRef,
		"an object that was never fetched")

	require.NoError(t, f.git.DeleteLocalTransportRefs(ctx, []string{"dispat-worker-c-relay"}))
	assert.Empty(t, strings.TrimSpace(runGit(t, f.root, "for-each-ref", "--format=%(refname)", TransportRefPrefix)))
	assert.NoError(t, f.git.DeleteLocalTransportRefs(ctx, []string{"dispat-worker-c-relay"}),
		"removing a ref that is already gone converges")
}

// TestClearTransportRefsEmptiesOnlyABareCache: a serving node starts by
// dropping every coordination ref an earlier process fetched into its cache,
// and nothing else; the same call in a checkout, or in a cache folder nobody
// initialized inside one, is refused and removes nothing.
func TestClearTransportRefsEmptiesOnlyABareCache(t *testing.T) {
	f := newTransportFixture(t)
	ctx := t.Context()
	branches := []string{"dispat-worker-c-20260925-build-a", "dispat-worker-c-20260925-snapshot-b"}
	require.NoError(t, f.git.PushCreate(ctx, f.bare, f.first, branches[0]))
	require.NoError(t, f.git.PushCreate(ctx, f.bare, f.second, branches[1]))

	cache := &LocalGitx{Dir: filepath.Join(t.TempDir(), "cache.git"), Log: zerolog.Nop()}
	require.NoError(t, os.MkdirAll(cache.Dir, 0o755))
	require.NoError(t, cache.InitBareStore(ctx))
	require.NoError(t, cache.FetchRefs(ctx, f.bare, branches))
	runGit(t, cache.Dir, "update-ref", "refs/heads/kept", f.second)

	cleared, err := cache.ClearTransportRefs(ctx)

	require.NoError(t, err)
	assert.Equal(t, 2, cleared)
	assert.Empty(t, strings.TrimSpace(runGit(t, cache.Dir, "for-each-ref", "--format=%(refname)", TransportRefPrefix)))
	assert.Equal(t, "refs/heads/kept", strings.TrimSpace(runGit(t, cache.Dir, "for-each-ref", "--format=%(refname)")),
		"a ref outside the transport namespace is not the cache's to drop")
	cleared, err = cache.ClearTransportRefs(ctx)
	require.NoError(t, err)
	assert.Zero(t, cleared, "an empty namespace clears nothing")

	require.NoError(t, f.git.FetchRefs(ctx, f.bare, branches[:1]))
	nested := filepath.Join(f.root, "never-initialized")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	for _, dir := range []string{f.root, nested} {
		_, err := (&LocalGitx{Dir: dir, Log: zerolog.Nop()}).ClearTransportRefs(ctx)
		require.Error(t, err, dir)
	}
	assert.Equal(t, TransportRefPrefix+branches[0],
		strings.TrimSpace(runGit(t, f.root, "for-each-ref", "--format=%(refname)", TransportRefPrefix)),
		"the checkout's own fetched ref is left for the process that fetched it")
}

// TestBareStoreLeavesMaintenanceToItsNode: a serving node's cache never runs
// git's automatic maintenance, which a fetch would start in the foreground, and
// the node's own collection deletes every object nothing reaches at once. The
// collection is refused in a checkout and in a folder nobody initialized
// inside one, where it would delete the checkout's own unreachable objects.
func TestBareStoreLeavesMaintenanceToItsNode(t *testing.T) {
	f := newTransportFixture(t)
	ctx := t.Context()
	cache := &LocalGitx{Dir: filepath.Join(t.TempDir(), "cache.git"), Log: zerolog.Nop()}
	require.NoError(t, os.MkdirAll(cache.Dir, 0o755))
	require.NoError(t, cache.InitBareStore(ctx))
	require.NoError(t, cache.InitBareStore(ctx), "opening the store again changes nothing")
	for setting, value := range map[string]string{"gc.auto": "0", "maintenance.auto": "false"} {
		assert.Equal(t, value, strings.TrimSpace(runGit(t, cache.Dir, "config", "--get", setting)), setting)
	}

	for _, dir := range []string{cache.Dir, f.root} {
		cmd := exec.Command("git", "-C", dir, "hash-object", "-w", "--stdin")
		cmd.Stdin = strings.NewReader("an object nothing reaches\n")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "hash-object: %s", out)
	}
	require.NoError(t, cache.CollectGarbage(ctx))
	assert.Contains(t, runGit(t, cache.Dir, "count-objects", "-v"), "count: 0\n")
	assert.Contains(t, runGit(t, cache.Dir, "count-objects", "-v"), "in-pack: 0\n")

	nested := filepath.Join(f.root, "never-initialized")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	held := runGit(t, f.root, "count-objects", "-v")
	for _, dir := range []string{f.root, nested} {
		require.Error(t, (&LocalGitx{Dir: dir, Log: zerolog.Nop()}).CollectGarbage(ctx), dir)
	}
	assert.Equal(t, held, runGit(t, f.root, "count-objects", "-v"), "the checkout keeps every object it had")
}

// TestTransportReadsTheTagObjectTheRemoteAdvertises: the lock is an annotated
// tag whose object id identifies one acquisition, so the tag object is what
// comes back and never the commit it peels to.
func TestTransportReadsTheTagObjectTheRemoteAdvertises(t *testing.T) {
	f := newTransportFixture(t)
	ctx := t.Context()

	absent, err := f.git.RemoteTagObject(ctx, f.bare, LockTagName)
	require.NoError(t, err)
	assert.Empty(t, absent, "a remote with no lock answers nothing")

	require.NoError(t, f.git.CreateTag(ctx, LockTagName, "dispat release lock\n\nattempt 1\n", "HEAD"))
	object, err := f.git.TagObject(ctx, LockTagName)
	require.NoError(t, err)
	require.NoError(t, f.git.PushObjectToTag(ctx, f.bare, object, LockTagName))

	held, err := f.git.RemoteTagObject(ctx, f.bare, LockTagName)
	require.NoError(t, err)
	assert.Equal(t, object, held)
	assert.NotEqual(t, f.second, held, "the tag object, not the commit under it")
}

// TestTransportPlumbingBuildsAnInertCommit: the object tree a mailbox message
// is made of, built by a session that needs no identity of its own and whose
// message can never parse as a release unit.
func TestTransportPlumbingBuildsAnInertCommit(t *testing.T) {
	f := newTransportFixture(t)
	ctx := t.Context()
	// No identity anywhere: a worker's cache clone is exactly this, and a
	// commit that fell back to git's configuration would fail here.
	bare := &LocalGitx{Dir: f.root, Log: zerolog.Nop()}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	runGit(t, f.root, "config", "--unset", "user.name")
	runGit(t, f.root, "config", "--unset", "user.email")

	plumbing := NewPlumbing(bare)
	blob := plumbing.HashObject(ctx, strings.NewReader(`{"protocol":1}`))
	inner := plumbing.MakeTree(ctx, []TreeEntry{{Mode: TreeModeFile, Type: "blob", OID: blob, Name: "assign.json"}})
	tree := plumbing.MakeTree(ctx, []TreeEntry{{Mode: TreeModeDir, Type: "tree", OID: inner, Name: "dispat"}})
	commit := plumbing.CommitTree(ctx, tree, []string{f.second}, "assign")
	require.NoError(t, plumbing.Err())

	body := runGit(t, f.root, "cat-file", "-p", commit)
	assert.Contains(t, body, "author "+TransportIdentityName+" <"+TransportIdentityEmail+">")
	assert.Contains(t, body, "committer "+TransportIdentityName+" <"+TransportIdentityEmail+">")
	assert.Contains(t, body, "dispat transport assign")
	assert.NotContains(t, body, ":", "a transport message carries nothing a unit header needs")
	assert.Equal(t, f.second, strings.TrimSpace(runGit(t, f.root, "rev-parse", commit+"^1")))
}

// TestTransportPlumbingStopsAtTheFirstFailure: the session is sticky, so a
// caller building four objects in a row has one error arm rather than four,
// and nothing after the failure runs.
func TestTransportPlumbingStopsAtTheFirstFailure(t *testing.T) {
	f := newTransportFixture(t)
	ctx := t.Context()
	plumbing := NewPlumbing(f.git)

	// A tree entry naming two levels is refused by this package before git is
	// asked, which is the failure the session then carries.
	before := GitInvocations()
	assert.Empty(t, plumbing.MakeTree(ctx, []TreeEntry{
		{Mode: TreeModeFile, Type: "blob", OID: strings.Repeat("0", 40), Name: "dispat/assign.json"}}))
	require.Error(t, plumbing.Err())
	first := plumbing.Err()

	assert.Empty(t, plumbing.HashObject(ctx, strings.NewReader("later")))
	assert.Empty(t, plumbing.CommitTree(ctx, strings.Repeat("0", 40), nil, "assign"))
	plumbing.ReadBlob(ctx, strings.Repeat("0", 40), &bytes.Buffer{}, 1)
	plumbing.ListTree(ctx, strings.Repeat("0", 40), func(TreeEntry) error { return nil })
	plumbing.WorktreeAdd(ctx, t.TempDir(), f.second)
	plumbing.WorktreeRemove(ctx, t.TempDir())
	plumbing.WorktreePrune(ctx)
	assert.Equal(t, before, GitInvocations(), "a failed session starts no further git process")
	assert.Same(t, first, plumbing.Err(), "the first failure is the one that is kept")
}

// TestTransportPlumbingCapturesAndReadsBackAWorkingFolder: the capture and
// install halves of an output transfer, including the names a build output
// may really have and the ceiling a blob is refused by.
func TestTransportPlumbingCapturesAndReadsBackAWorkingFolder(t *testing.T) {
	f := newTransportFixture(t)
	ctx := t.Context()
	// Ignored, which is what a build output is, and named the way files on a
	// real machine are named.
	dist := filepath.Join(f.root, "packages", "core", "dist")
	require.NoError(t, os.MkdirAll(filepath.Join(dist, "nested"), 0o755))
	names := []string{"a file with spaces.js", "ünïcode.js"}
	if runtime.GOOS == "windows" {
		// Windows file names cannot contain a newline. Keep a punctuation case
		// here while POSIX exercises the newline-delimited hash-list fallback.
		names = append(names, "semi;colon.js")
	} else {
		names = append(names, "new\nline.js")
	}
	for _, name := range names {
		require.NoError(t, os.WriteFile(filepath.Join(dist, name), []byte("payload of "+name), 0o644))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dist, "nested", "deep.txt"), []byte("deep"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(f.root, ".gitignore"), []byte("dist/\n"), 0o644))

	plumbing := NewPlumbing(f.git)
	index := filepath.Join(t.TempDir(), "index")
	tree := plumbing.WriteTreeFromPaths(ctx, filepath.Join(f.root, "packages", "core"), index, []string{"dist"}, true)
	require.NoError(t, plumbing.Err())

	listed := map[string]int64{}
	plumbing.ListTree(ctx, tree, func(entry TreeEntry) error {
		listed[entry.Name] = entry.Size
		return nil
	})
	require.NoError(t, plumbing.Err())
	require.Len(t, listed, 4, "every declared file travels, whatever it is called")
	for _, name := range names {
		// The tree a temporary index writes is rooted at the repository, so a
		// captured path is the one the manifest of the release names too.
		size, isListed := listed["packages/core/dist/"+name]
		require.True(t, isListed, name)
		assert.Equal(t, int64(len("payload of "+name)), size)
	}

	oid := strings.TrimSpace(runGit(t, f.root, "rev-parse", tree+":packages/core/dist/nested/deep.txt"))
	var content bytes.Buffer
	plumbing.ReadBlob(ctx, oid, &content, 64)
	require.NoError(t, plumbing.Err())
	assert.Equal(t, "deep", content.String())

	refused := NewPlumbing(f.git)
	refused.ReadBlob(ctx, oid, &bytes.Buffer{}, 3)
	assert.ErrorIs(t, refused.Err(), ErrTransportLimit, "the size is refused before a byte is written")
}

// TestTransportPlumbingStopsAWalkOnTheVisitorsError: a consumer of a listing
// that refuses an entry stops the walk with its own error rather than reading
// the rest of a listing it has already rejected.
func TestTransportPlumbingStopsAWalkOnTheVisitorsError(t *testing.T) {
	f := newTransportFixture(t)
	ctx := t.Context()
	refusal := errors.New("this entry escapes its declared root")
	plumbing := NewPlumbing(f.git)
	plumbing.ListTree(ctx, f.second+"^{tree}", func(TreeEntry) error { return refusal })
	assert.ErrorIs(t, plumbing.Err(), refusal)
}

// TestTransportNeverPrintsACredential: every message and every trace line a
// transport call writes goes through the same redaction as the rest of this
// package, because a mailbox address reaches a log on every poll.
func TestTransportNeverPrintsACredential(t *testing.T) {
	f := newTransportFixture(t)
	ctx := t.Context()
	var trace bytes.Buffer
	noisy := &LocalGitx{Dir: f.root, Log: zerolog.New(&trace).Level(zerolog.TraceLevel)}
	remote := "https://ci-bot:s3cr3t-token@127.0.0.1:1/acme/mailbox.git"

	err := noisy.PushCreate(ctx, remote, f.first, "dispat-worker-a-build")
	require.Error(t, err)
	_, listErr := noisy.ListRemoteHeads(ctx, remote, "refs/heads/dispat-worker-a-*")
	require.Error(t, listErr)
	_, tagErr := noisy.RemoteTagObject(ctx, remote, LockTagName)
	require.Error(t, tagErr)

	for _, text := range []string{err.Error(), listErr.Error(), tagErr.Error(), trace.String()} {
		assert.NotContains(t, text, "s3cr3t-token")
		assert.Contains(t, text, "REDACTED")
	}
}

// TestObjectReaderStreamsManyBlobsThroughOneProcess: the reader an output
// transfer reads every file through. One process answers every request in
// order, an object the store does not hold is a sentinel rather than a
// failure, and a ceiling refuses a blob before a byte of it is copied.
func TestObjectReaderStreamsManyBlobsThroughOneProcess(t *testing.T) {
	f := newTransportFixture(t)
	ctx := t.Context()
	plumbing := NewPlumbing(f.git)
	bodies := map[string]string{"one": "first", "two": strings.Repeat("x", 4096), "three": ""}
	oids := map[string]string{}
	for name, body := range bodies {
		oids[name] = plumbing.HashObject(ctx, strings.NewReader(body))
	}
	require.NoError(t, plumbing.Err())

	before := GitInvocations()
	reader, err := OpenObjectReader(ctx, f.git)
	require.NoError(t, err)
	for name, body := range bodies {
		var out bytes.Buffer
		size, err := reader.ReadBlob(oids[name], &out, 1<<20)
		require.NoError(t, err, name)
		assert.Equal(t, int64(len(body)), size, name)
		assert.Equal(t, body, out.String(), name)
	}
	_, missingErr := reader.ReadBlob(strings.Repeat("0", 40), &bytes.Buffer{}, 1<<20)
	assert.ErrorIs(t, missingErr, ErrObjectMissing,
		"an object nobody fetched is named as one rather than reported as a broken process")
	// The stream survives a missing object, because nothing of it was read.
	var again bytes.Buffer
	_, err = reader.ReadBlob(oids["one"], &again, 1<<20)
	require.NoError(t, err)
	assert.Equal(t, "first", again.String())
	require.NoError(t, reader.Close())
	assert.Equal(t, before+1, GitInvocations(), "one process answered every request")
}

// TestObjectReaderRefusesAnOversizedBlobAndStops: the ceiling is applied to
// the length the header promised, so an oversized object is never copied
// anywhere; the reader is then spent, because the bytes of it are still in the
// pipe and nothing is going to read them.
func TestObjectReaderRefusesAnOversizedBlobAndStops(t *testing.T) {
	f := newTransportFixture(t)
	ctx := t.Context()
	plumbing := NewPlumbing(f.git)
	oid := plumbing.HashObject(ctx, strings.NewReader(strings.Repeat("y", 1000)))
	require.NoError(t, plumbing.Err())

	reader, err := OpenObjectReader(ctx, f.git)
	require.NoError(t, err)
	var out bytes.Buffer
	_, err = reader.ReadBlob(oid, &out, 10)

	assert.ErrorIs(t, err, ErrTransportLimit)
	assert.Empty(t, out.String(), "nothing was written before the refusal")
	_, err = reader.ReadBlob(oid, &bytes.Buffer{}, 1<<20)
	assert.ErrorIs(t, err, ErrTransportLimit, "a reader that stopped mid-stream answers nothing more")
	assert.NoError(t, reader.Close())
}

// TestObjectReaderRefusesAShortRequest: a pipe that accepted only a prefix of
// the object id cannot be trusted to answer the request, even if its writer
// returned no error with the short count.
func TestObjectReaderRefusesAShortRequest(t *testing.T) {
	reader := &ObjectReader{
		stdin: shortRequestPipe{}, stdout: bufio.NewReader(strings.NewReader("unexpected\n")),
	}
	size, err := reader.request(strings.Repeat("a", 40))
	assert.Zero(t, size)
	assert.ErrorIs(t, err, io.ErrShortWrite)
	assert.True(t, reader.isBroken, "a partial request cannot share the next response")
}

// TestResolveSubtreeNarrowsARepositoryTreeToAFolder: a tree written from an
// index is rooted at the repository, and a manifest names its files by the
// package folder, so the one is narrowed to the other.
func TestResolveSubtreeNarrowsARepositoryTreeToAFolder(t *testing.T) {
	f := newTransportFixture(t)
	ctx := t.Context()
	dist := filepath.Join(f.root, "packages", "core", "dist")
	require.NoError(t, os.MkdirAll(dist, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dist, "app.js"), []byte("payload"), 0o644))

	plumbing := NewPlumbing(f.git)
	index := filepath.Join(t.TempDir(), "index")
	tree := plumbing.WriteTreeFromPaths(ctx, filepath.Join(f.root, "packages", "core"), index, []string{"dist"}, true)
	narrowed := plumbing.ResolveSubtree(ctx, tree, "packages/core")
	require.NoError(t, plumbing.Err())
	assert.Equal(t, tree, plumbing.ResolveSubtree(ctx, tree, "."), "a package at the root is the tree itself")

	listed := map[string]int64{}
	plumbing.ListTree(ctx, narrowed, func(entry TreeEntry) error {
		listed[entry.Name] = entry.Size
		return nil
	})
	require.NoError(t, plumbing.Err())
	assert.Equal(t, map[string]int64{"dist/app.js": int64(len("payload"))}, listed)

	absent := NewPlumbing(f.git)
	absent.ResolveSubtree(ctx, tree, "packages/absent")
	assert.Error(t, absent.Err(), "a folder the tree does not hold is a failure rather than an empty answer")
}

// TestCountChangedPathsIgnoresTheFoldersACallerDeclared: a checkout is asked
// what a task changed in the source it was given, so a build's own products
// are not counted, and neither are tracked files inside a folder the caller
// declared as a build output: those the run carries on purpose.
func TestCountChangedPathsIgnoresTheFoldersACallerDeclared(t *testing.T) {
	f := newTransportFixture(t)
	ctx := t.Context()
	for _, name := range []string{"src/main.go", "packages/core/dist/kept.js", "dist-old/other.js"} {
		path := filepath.Join(f.root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("one"), 0o644))
	}
	runGit(t, f.root, "add", "-A")
	runGit(t, f.root, "commit", "-q", "-m", "tracked")
	for _, name := range []string{"src/main.go", "packages/core/dist/kept.js", "dist-old/other.js"} {
		require.NoError(t, os.WriteFile(filepath.Join(f.root, filepath.FromSlash(name)), []byte("two"), 0o644))
	}
	require.NoError(t, os.WriteFile(filepath.Join(f.root, "untracked.txt"), []byte("three"), 0o644))

	counted, err := f.git.CountChangedPaths(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, 3, counted, "every tracked change counts and the untracked file does not")

	declared, err := f.git.CountChangedPaths(ctx, []string{"packages/core/dist"})
	require.NoError(t, err)
	assert.Equal(t, 2, declared,
		"the declared folder is not a stray write, and a folder whose name only begins like it still is")
}

// TestTransportPlumbingCapturesBytesUntouchedByAttributes: a checkout's
// attributes describe its sources and must not touch a build output. A tree
// marked `text` would have every CRLF pair inside a library rewritten by
// `git add`; the forced capture records the bytes as the build wrote them,
// with the modes git records for an executable and a link.
func TestTransportPlumbingCapturesBytesUntouchedByAttributes(t *testing.T) {
	f := newTransportFixture(t)
	ctx := t.Context()
	core := filepath.Join(f.root, "packages", "core")
	dist := filepath.Join(core, "dist", "lib")
	require.NoError(t, os.MkdirAll(dist, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(core, ".gitattributes"), []byte("* text eol=lf\n"), 0o644))
	library := []byte("ELF\x00header\r\nbody\r\n\x00tail\r\n")
	require.NoError(t, os.WriteFile(filepath.Join(dist, "libcore.a"), library, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dist, "tool"), []byte("#!/bin/sh\r\necho\r\n"), 0o755))
	require.NoError(t, os.Symlink("tool", filepath.Join(dist, "alias")))
	require.NoError(t, os.WriteFile(filepath.Join(f.root, ".gitignore"), []byte("dist/\n"), 0o644))

	plumbing := NewPlumbing(f.git)
	index := filepath.Join(t.TempDir(), "index")
	tree := plumbing.WriteTreeFromPaths(ctx, core, index, []string{"dist"}, true)
	require.NoError(t, plumbing.Err())

	modes := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(runGit(t, f.root, "ls-tree", "-r", tree)), "\n") {
		fields := strings.Fields(line)
		modes[fields[3]] = fields[0]
	}
	assert.Equal(t, "100644", modes["packages/core/dist/lib/libcore.a"])
	executableMode := "100755"
	if runtime.GOOS == "windows" {
		// Windows does not expose the POSIX executable bit through os.FileMode.
		// A new regular output therefore has Git's ordinary file mode.
		executableMode = "100644"
	}
	assert.Equal(t, executableMode, modes["packages/core/dist/lib/tool"], "the native file mode is recorded")
	assert.Equal(t, "120000", modes["packages/core/dist/lib/alias"], "a link is recorded as a link")

	var captured bytes.Buffer
	plumbing.ReadBlob(ctx, strings.TrimSpace(runGit(t, f.root, "rev-parse", tree+":packages/core/dist/lib/libcore.a")),
		&captured, 1024)
	require.NoError(t, plumbing.Err())
	assert.Equal(t, library, captured.Bytes(), "every byte the build wrote, CRLF pairs included")
	assert.Equal(t, "tool", strings.TrimSpace(runGit(t, f.root, "cat-file", "blob", tree+":packages/core/dist/lib/alias")))
}
