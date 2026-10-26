// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// What a distributed run does when its own code fails unexpectedly, or when a
// mailbox stops answering while it closes: the failure stays with the branch,
// the node or the task it happened to, and the run's cleanup ends within its
// bound.

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// TestWatcherContainsAPanicToItsBranch: a branch whose reading panics is
// quarantined and reported as unanswered, and the poll goes on.
func TestWatcherContainsAPanicToItsBranch(t *testing.T) {
	fixture := newCoordinatorFixture(t, TransferLimits{MaxManifestBytes: 1 << 20}, answeredPreflight)
	branch := FormatBranch("build-a", KindBuild, time.Now())
	assignment := probeAssignment("build-a", branch)
	offered, err := assign(t.Context(), fixture.orchestrator.mailbox, assignment)
	require.NoError(t, err)
	_, err = fixture.node.mailbox.Reread(t.Context(), branch)
	require.NoError(t, err)
	claimed, err := fixture.node.mailbox.Advance(t.Context(), branch, offered, MessageClaim,
		mustMarshalValue(Claim{Header: replyHeader(*assignment), Assignment: offered}), nil)
	require.NoError(t, err)
	_, err = fixture.orchestrator.mailbox.Reread(t.Context(), branch)
	require.NoError(t, err)
	observer := &watcher{coordinator: fixture.coordinator, link: Link{Name: "build-a"},
		mailbox: fixture.orchestrator.mailbox, attempts: map[string]*attemptState{}}
	observer.watch(branch)
	// A registration with no assignment is what makes reading the claim
	// dereference nothing, which stands in for any bug in the reading.
	observer.bind(branch, offered, nil)

	var isAnswered bool
	require.NotPanics(t, func() {
		isAnswered = observer.inspect(t.Context(), gitx.RemoteHead{Name: branch, OID: claimed})
	})

	assert.False(t, isAnswered)
	assert.True(t, fixture.orchestrator.mailbox.quarantined[branch], "the branch is left alone until it moves")
	assert.Equal(t, claimed, fixture.orchestrator.mailbox.observed[branch])
}

// TestPreflightContainsAPanicToItsNode: a probe that panics fails its node's
// preflight, and the run is refused rather than the process ended.
func TestPreflightContainsAPanicToItsNode(t *testing.T) {
	fixture := newCoordinatorFixture(t, TransferLimits{MaxManifestBytes: 1 << 20}, silentPreflight)
	// A link with no mailbox behind it panics the moment the probe writes.
	fixture.coordinator.Links = append(fixture.coordinator.Links, Link{Name: "ghost"})

	var err error
	require.NotPanics(t, func() { err = fixture.coordinator.Preflight(t.Context(), nil) })

	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not pass preflight")
	assert.Equal(t, CodeConfiguration, config.DiagnosticCode(err))
}

// TestWorkerContainsAPanicToItsTask: a task that panics before its command
// started returns a failure to the first node, and one that panics after
// starting reports nothing so the first node settles the unknown attempt.
func TestWorkerContainsAPanicToItsTask(t *testing.T) {
	for name, isStarted := range map[string]bool{"before its command": false, "after its command": true} {
		t.Run(name, func(t *testing.T) {
			branch := "dispat-task-run-1-build-abc"
			tip := ChainTip{Branch: branch, OID: "assignment-oid", Kind: MessageAssignment}
			message := validProbe(branch)
			message.Kind, message.Task = KindBuild, "pkg:build"
			message.Limits.MaxPackBytes = 1 << 20
			worker, mailbox := newWorkerFixture(t, branch, tip, message)
			fixture := newMailboxFixture(t)
			worker.Cache, worker.ReturnSigner = fixture.git, fixture.signer
			worker.Report.Limits.MaxPackBytes = 1 << 20
			binding := ChoreographyRunBinding{Run: message.Run, Generation: message.Generation,
				PlanDigest: message.PlanDigest, GraphDigest: "graph"}
			returned := 0
			fake := &fakeWorkerReturn{result: func(p ChoreographyResultProposal,
				_ io.ReadSeeker) (ChoreographyReceipt, error) {
				returned++
				query, err := FormatResultReceiptQuery(p)
				require.NoError(t, err)
				return testReturnReceipt(choreographyResultKind, binding, p.Task, p.Attempt,
					p.Node, p.Branch, p.ProposalID, query.ProposalDigest, "first-result-oid"), nil
			}}
			worker.returnChannels = map[string]*workerReturnChannel{message.Run: {binding: binding, client: fake}}
			task := &claimedTask{assignment: message, tip: tip, claimed: "claim-oid", expectedTip: "go-oid"}
			task.reportPhase(release.PartCommands, isStarted)

			require.NotPanics(t, func() {
				func() {
					defer worker.recoverTask(t.Context(), task)
					panic("a bug")
				}()
			})

			if isStarted {
				assert.Empty(t, mailbox.written, "a started command's outcome is not this node's to state")
				assert.Zero(t, returned)
				return
			}
			assert.Empty(t, mailbox.written, "the peer does not push its failed result")
			assert.Equal(t, 1, returned)
			assert.Empty(t, fixture.remoteBranches(t))
		})
	}
}

// TestCloseEndsWithinItsBound: a mailbox that hangs on every delete still lets
// the run's close return within its bound, reporting what it could not close,
// and the fetched refs go regardless.
func TestCloseEndsWithinItsBound(t *testing.T) {
	previous := minimumCloseBound
	minimumCloseBound = 300 * time.Millisecond
	t.Cleanup(func() { minimumCloseBound = previous })
	ownPushReadPauses = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { ownPushReadPauses = []time.Duration{time.Second, 2 * time.Second} })
	fixture := newCoordinatorFixture(t, TransferLimits{MaxManifestBytes: 1 << 20}, answeredPreflight)
	branch := FormatBranch("build-a", KindBuild, time.Now())
	offered, err := assign(t.Context(), fixture.orchestrator.mailbox, probeAssignment("build-a", branch))
	require.NoError(t, err)
	fixture.coordinator.recordOwnedRef(t.Context(), ownedRefStep{node: "build-a", branch: branch, oid: offered})
	fixture.orchestrator.mailbox.remote = &hangingDeleteTransport{LocalGitx: fixture.orchestrator.git}

	started := time.Now()
	err = fixture.coordinator.Close(context.Background())

	require.Error(t, err, "a branch the hanging mailbox kept is reported")
	assert.Less(t, time.Since(started), 5*time.Second, "and the close ended within its bound")
}

// hangingDeleteTransport never answers a delete or a listing until its
// context ends, which is a mailbox that hung after preflight.
type hangingDeleteTransport struct {
	*gitx.LocalGitx
}

func (t *hangingDeleteTransport) DeleteRemoteBranchesLease(ctx context.Context, _ string,
	leases []gitx.BranchLease) ([]gitx.BranchOutcome, error) {
	<-ctx.Done()
	return formatUnknownOutcomes(leases), nil
}

func (t *hangingDeleteTransport) ListRemoteHeads(ctx context.Context, _, _ string) ([]gitx.RemoteHead, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
