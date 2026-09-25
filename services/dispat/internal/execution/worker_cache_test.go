// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// A worker's object cache over many tasks, against real repositories.
//
// The cache is dispensable, and what these tests hold it to is that it also
// stays small: a node that serves work for days must not keep the refs and the
// objects of every attempt it ever answered. The fixture is a real bare
// mailbox, a real orchestrator store that offers probes and input states, and
// a real serving node whose cache is a bare repository of its own.

import (
	"bytes"
	"crypto/rand"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// cacheFixture is one orchestrator and one serving node on one mailbox.
type cacheFixture struct {
	orchestrator *mailboxFixture
	node         *mailboxFixture
	worker       *Worker
}

func newCacheFixture(t *testing.T) *cacheFixture {
	t.Helper()
	orchestrator := newMailboxFixture(t)
	node := orchestrator.second(t)
	return &cacheFixture{orchestrator: orchestrator, node: node, worker: newCacheWorker(t, node)}
}

// newCacheWorker is a serving node over a cache, as `dispat worker` assembles
// one: the mailbox fetches into the cache, and the cache is opened by the
// same call that repairs it.
func newCacheWorker(t *testing.T, node *mailboxFixture) *Worker {
	t.Helper()
	return &Worker{
		Node: "build-a", Endpoint: node.endpoint, StateDir: t.TempDir(),
		Report:       FormatNodeReport("1.11.0", "git version 2.43.0", 2, TransferLimits{MaxManifestBytes: 1 << 20}),
		Mailbox:      NewGitMailbox(node.endpoint, node.git, node.signer, zerolog.Nop()),
		Cache:        node.git,
		Seen:         newSeenSetForTest(t),
		Log:          zerolog.Nop(),
		PrepareStore: node.git.InitBareStore,
	}
}

// offerTask puts one probe and one input state in the node's mailbox, as a
// dispatch does, and answers the leases the run closes them under. The input
// state carries 64 KiB nobody has seen before, so every task brings objects
// of its own into the cache.
func (f *cacheFixture) offerTask(t *testing.T, attempt int) (string, gitx.BranchLease) {
	t.Helper()
	ctx := t.Context()
	branch := FormatBranch("build-a", KindProbe, time.Now())
	probe := probeAssignment("build-a", branch)
	probe.Attempt = attempt
	_, err := assign(ctx, f.orchestrator.mailbox, probe)
	require.NoError(t, err)

	content := make([]byte, 64<<10)
	_, _ = rand.Read(content)
	plumbing := gitx.NewPlumbing(f.orchestrator.git)
	blob := plumbing.HashObject(ctx, bytes.NewReader(content))
	tree := plumbing.MakeTree(ctx, []gitx.TreeEntry{{Mode: gitx.TreeModeFile, Type: "blob", OID: blob, Name: "source.bin"}})
	commit := plumbing.CommitTree(ctx, tree, nil, string(KindSnapshot))
	require.NoError(t, plumbing.Err())
	snapshot := FormatBranch("build-a", KindSnapshot, time.Now())
	require.NoError(t, f.orchestrator.git.PushCreate(ctx, f.orchestrator.endpoint, commit, snapshot))
	return branch, gitx.BranchLease{Branch: snapshot, ExpectedOld: commit}
}

// closeTask ends the run of one task the way an orchestrator does: its
// branches are deleted from the mailbox under leases over what they hold.
func (f *cacheFixture) closeTask(t *testing.T, branch string, snapshot gitx.BranchLease) {
	t.Helper()
	head, err := f.orchestrator.mailbox.Reread(t.Context(), branch)
	require.NoError(t, err)
	require.NotEmpty(t, head.OID)
	outcomes, err := f.orchestrator.mailbox.Close(t.Context(),
		[]gitx.BranchLease{{Branch: branch, ExpectedOld: head.OID}, snapshot})
	require.NoError(t, err)
	for _, outcome := range outcomes {
		require.Equal(t, gitx.BranchDeleted, outcome.Result, outcome.Branch)
	}
}

// cacheSize is what one cache holds: its fetched coordination refs, and its
// objects loose and packed with the disk they take.
type cacheSize struct {
	refs    int
	objects int
	kib     int
}

func measureCache(t *testing.T, store string) cacheSize {
	t.Helper()
	refs := strings.Fields(runGitIn(t, store, "for-each-ref", "--format=%(refname)", gitx.TransportRefPrefix))
	size := cacheSize{refs: len(refs)}
	for line := range strings.Lines(runGitIn(t, store, "count-objects", "-v")) {
		name, value, _ := strings.Cut(strings.TrimSpace(line), ": ")
		number, err := strconv.Atoi(value)
		if err != nil {
			continue
		}
		switch name {
		case "count", "in-pack":
			size.objects += number
		case "size", "size-pack":
			size.kib += number
		}
	}
	return size
}

// TestAWorkerStartsWithAnEmptyCoordinationCache: a node that was killed with
// coordination branches fetched leaves their refs in its cache, and the
// process that only remembers what it fetched itself would keep them, and the
// objects they reach, for ever. A worker restarted over that cache drops them
// before its first poll, and serves as usual afterwards.
func TestAWorkerStartsWithAnEmptyCoordinationCache(t *testing.T) {
	fixture := newCacheFixture(t)
	var branches []string
	var snapshots []gitx.BranchLease
	for attempt := 1; attempt <= 3; attempt++ {
		branch, snapshot := fixture.offerTask(t, attempt)
		require.True(t, fixture.worker.tick(t.Context()))
		branches, snapshots = append(branches, branch), append(snapshots, snapshot)
	}
	killed := measureCache(t, fixture.node.store)
	require.Equal(t, 6, killed.refs, "the killed node held a probe and an input state per task")
	// The run ends while the node is down, so no poll of the old process
	// ever sees the branches go.
	for index := range branches {
		fixture.closeTask(t, branches[index], snapshots[index])
	}

	restarted := newCacheWorker(t, fixture.node)
	restarted.Seen = fixture.worker.Seen
	require.False(t, restarted.tick(t.Context()))

	started := measureCache(t, fixture.node.store)
	t.Logf("killed with %+v, restarted with %+v", killed, started)
	require.Zero(t, started.refs, "the restarted node starts with no coordination ref")

	branch, snapshot := fixture.offerTask(t, 4)
	require.True(t, restarted.tick(t.Context()), "and serves the next task as usual")
	require.Equal(t, 2, measureCache(t, fixture.node.store).refs)
	fixture.closeTask(t, branch, snapshot)
	restarted.tick(t.Context())
	require.Zero(t, measureCache(t, fixture.node.store).refs)
}
