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

// compactIdle gives the node the poll it makes once it has had nothing to do
// for the whole idle stretch: everything it remembers the time of is moved
// back by that long, which is that stretch passing.
func compactIdle(t *testing.T, worker *Worker) {
	t.Helper()
	worker.activity.Lock()
	worker.lastActive = worker.lastActive.Add(-cacheMaintenanceIdle)
	worker.activity.Unlock()
	if !worker.lastMaintained.IsZero() {
		worker.lastMaintained = worker.lastMaintained.Add(-cacheMaintenanceIdle)
	}
	worker.maintainIdleCache(t.Context(), time.Now())
}

// TestAWorkerCacheStaysFlatAcrossTasks: twelve tasks through one node, each
// bringing a probe and an input state of its own into the cache. The refs go
// when the run closes the branches, and the objects go when the node next has
// nothing to do, so the cache holds as much after the twelfth task as after
// the first. Without the compaction it grows by every task's objects.
func TestAWorkerCacheStaysFlatAcrossTasks(t *testing.T) {
	fixture := newCacheFixture(t)
	var first cacheSize
	for attempt := 1; attempt <= 12; attempt++ {
		branch, snapshot := fixture.offerTask(t, attempt)
		require.True(t, fixture.worker.tick(t.Context()))
		fixture.closeTask(t, branch, snapshot)
		fixture.worker.tick(t.Context())
		compactIdle(t, fixture.worker)

		size := measureCache(t, fixture.node.store)
		t.Logf("after task %d: %d refs, %d objects, %d KiB", attempt, size.refs, size.objects, size.kib)
		if attempt == 1 {
			first = size
		}
		require.Zero(t, size.refs, "task %d", attempt)
		require.LessOrEqual(t, size.objects, first.objects, "task %d", attempt)
		require.LessOrEqual(t, size.kib, first.kib, "task %d", attempt)
	}
	for _, setting := range [][2]string{{"gc.auto", "0"}, {"maintenance.auto", "false"}} {
		require.Equal(t, setting[1], strings.TrimSpace(runGitIn(t, fixture.node.store, "config", "--get", setting[0])),
			"git's own maintenance never starts inside a poll")
	}
}

// TestAWorkerCompactsItsCacheOnlyWhenIdle: the compaction deletes every object
// nothing reaches, so it runs only when nothing else can be writing the cache:
// never with a task in flight, never before the node has been idle for the
// stated stretch, and once per stretch rather than on every poll.
func TestAWorkerCompactsItsCacheOnlyWhenIdle(t *testing.T) {
	now := time.Now()
	for _, scenario := range []struct {
		name       string
		inFlight   int
		idle       time.Duration
		maintained time.Duration
		isClosed   bool
		isDue      bool
	}{
		{name: "idle for the whole stretch", idle: cacheMaintenanceIdle, isDue: true},
		{name: "a task in flight", inFlight: 1, idle: time.Hour},
		{name: "idle for less than the stretch", idle: cacheMaintenanceIdle - time.Second},
		{name: "already compacted in this stretch", idle: time.Hour, maintained: time.Minute},
		{name: "the store is not open", idle: time.Hour, isClosed: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			worker := &Worker{Cache: &gitx.LocalGitx{}, isStorePrepared: !scenario.isClosed,
				inFlight: scenario.inFlight, lastActive: now.Add(-scenario.idle)}
			if scenario.maintained > 0 {
				worker.lastMaintained = now.Add(-scenario.maintained)
			}
			require.Equal(t, scenario.isDue, worker.isCacheMaintenanceDue(now))
		})
	}

	fixture := newCacheFixture(t)
	branch, snapshot := fixture.offerTask(t, 1)
	require.True(t, fixture.worker.tick(t.Context()))
	fixture.closeTask(t, branch, snapshot)
	fixture.worker.tick(t.Context())
	held := measureCache(t, fixture.node.store)
	fixture.worker.beginTask()
	compactIdle(t, fixture.worker)
	require.Equal(t, held, measureCache(t, fixture.node.store), "nothing is collected under a running task")
	fixture.worker.endTask()
	compactIdle(t, fixture.worker)
	require.Zero(t, measureCache(t, fixture.node.store).objects, "the idle node collects what the task left")
}
