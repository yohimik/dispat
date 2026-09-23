// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// The serving loop over a mailbox that answers whatever a scenario wants it
// to, which is how the acceptance rules are asserted one at a time: every row
// below is a message that reaches a real node and is either answered or
// refused for exactly one reason.

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// fakeMailbox is a mailbox holding a fixed set of branches, recording what
// the worker wrote back.
type fakeMailbox struct {
	heads     []gitx.RemoteHead
	tips      map[string]ChainTip
	documents map[string][]byte
	rejection map[string]RejectReason
	// unreadable makes one branch fail the way a local git failure does: not
	// a message this protocol refuses, but objects this process could not
	// read at all.
	unreadable map[string]error

	written      []MessageKind
	carried      []gitx.TreeEntry
	fetched      []string
	rereads      []string
	reconsidered []string
	forgotten    int
	polls        int
}

func (m *fakeMailbox) Observe(context.Context, string) ([]gitx.RemoteHead, error) {
	m.polls++
	heads := m.heads
	// A poll answers what moved, and nothing moves twice on its own.
	m.heads = nil
	return heads, nil
}

func (m *fakeMailbox) Inspect(_ context.Context, head gitx.RemoteHead) (ChainTip, error) {
	if err, isBroken := m.unreadable["inspect:"+head.Name]; isBroken {
		return ChainTip{}, err
	}
	return m.tips[head.Name], nil
}

func (m *fakeMailbox) Read(_ context.Context, tip ChainTip, _ int64) ([]byte, error) {
	if err, isBroken := m.unreadable["read:"+tip.Branch]; isBroken {
		return nil, err
	}
	if reason, isRejected := m.rejection[tip.Branch]; isRejected {
		return nil, &Rejection{Reason: reason}
	}
	return m.documents[tip.Branch], nil
}

func (m *fakeMailbox) Advance(_ context.Context, branch, _ string, kind MessageKind,
	document []byte, carried []gitx.TreeEntry) (string, error) {
	m.written = append(m.written, kind)
	m.carried = append(m.carried, carried...)
	m.documents[branch+"/"+string(kind)] = document
	return string(kind) + "-oid", nil
}

func (m *fakeMailbox) Fetch(_ context.Context, branches []string) error {
	m.fetched = append(m.fetched, branches...)
	return nil
}

// Reread answers where one branch sits now, which for this fake is the tip it
// was seeded with: the branch the worker is waiting on is the branch the
// scenario put there, and a scenario that wants it to move replaces the tip.
func (m *fakeMailbox) Reread(_ context.Context, branch string) (gitx.RemoteHead, error) {
	m.rereads = append(m.rereads, branch)
	tip, isKnown := m.tips[branch]
	if !isKnown {
		return gitx.RemoteHead{}, nil
	}
	return gitx.RemoteHead{Name: branch, OID: tip.OID}, nil
}

func (m *fakeMailbox) Reconsider(branch string) { m.reconsidered = append(m.reconsidered, branch) }

func (m *fakeMailbox) Forget() { m.forgotten++ }

// newWorkerFixture is one node with one branch in its mailbox, ready to be
// ticked once.
func newWorkerFixture(t *testing.T, branch string, tip ChainTip, message any) (*Worker, *fakeMailbox) {
	t.Helper()
	document, err := json.Marshal(message)
	require.NoError(t, err)
	mailbox := &fakeMailbox{
		heads:      []gitx.RemoteHead{{Name: branch, OID: tip.OID}},
		tips:       map[string]ChainTip{branch: tip},
		documents:  map[string][]byte{branch: document},
		rejection:  map[string]RejectReason{},
		unreadable: map[string]error{},
	}
	seen, err := LoadSeenSet(filepath.Join(t.TempDir(), "seen.json"), time.Now())
	require.NoError(t, err)
	return &Worker{
		Node: "build-a", Endpoint: "file:///srv/mailbox.git", StateDir: t.TempDir(),
		Report:       FormatNodeReport("1.11.0", "git version 2.43.0", 2, TransferLimits{MaxManifestBytes: 1 << 20}),
		Mailbox:      mailbox,
		Seen:         seen,
		Log:          zerolog.Nop(),
		PrepareStore: func(context.Context) error { return nil },
	}, mailbox
}

// validProbe is the assignment every row below varies from.
func validProbe(branch string) Assignment {
	return Assignment{Header: Header{
		Protocol: ProtocolVersion, Kind: KindProbe, Run: "run-1", PlanDigest: "digest",
		Task: PreflightTask, Attempt: 1, Generation: "generation", Node: "build-a",
		Branch: branch, IssuedAt: time.Now().UTC().Format(time.RFC3339),
	}}
}

// TestWorkerAnswersAValidProbe: the whole exchange one probe produces, and
// the report it carries.
func TestWorkerAnswersAValidProbe(t *testing.T) {
	branch := "dispat-worker-build-a-20260921-probe-abc"
	tip := ChainTip{Branch: branch, OID: "assignment-oid", Kind: MessageAssignment}
	worker, mailbox := newWorkerFixture(t, branch, tip, validProbe(branch))

	isProgress := worker.tick(t.Context())

	assert.True(t, isProgress)
	assert.Equal(t, []MessageKind{MessageClaim, MessageResult}, mailbox.written)
	assert.True(t, worker.Seen.IsSeen("run-1", PreflightTask, 1),
		"the node remembers what it took on before it reports it")

	var result Result
	require.NoError(t, json.Unmarshal(mailbox.documents[branch+"/result"], &result))
	assert.Equal(t, StatusSucceeded, result.Status)
	assert.Equal(t, "assignment-oid", result.Assignment)
	assert.Equal(t, branch, result.Branch)
	assert.Equal(t, "build-a", result.Node)
	require.NotNil(t, result.Report)
	assert.Equal(t, ProtocolVersion, result.Report.Protocol)
	assert.Equal(t, 2, result.Report.Capacity)
	assert.Equal(t, result.Report.OS, result.Platform.OS)
	assert.Equal(t, "1.11.0", result.Platform.Dispat)
}

// The run may cancel after the task's last progress read but before its Result
// push. Real Git rejects the stale Result lease; only this exact, signed Cancel
// may replace it with an Ack after the commands have stopped.
func TestWorkerResultLeaseLostToCancellation(t *testing.T) {
	for _, scenario := range []struct {
		name  string
		isOwn bool
	}{
		{name: "own cancellation is acknowledged", isOwn: true},
		{name: "foreign cancellation is left alone"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			orchestrator := newMailboxFixture(t)
			node := orchestrator.second(t)
			branch := FormatBranch("build-a", KindBuild, time.Now())
			assignment := probeAssignment("build-a", branch)
			assignment.Kind, assignment.Task = KindBuild, "app:build"
			assignment.Limits.MaxManifestBytes = 1 << 20
			offered, err := orchestrator.mailbox.Assign(t.Context(), assignment)
			require.NoError(t, err)
			_, err = node.mailbox.Reread(t.Context(), branch)
			require.NoError(t, err)
			claimed, err := node.mailbox.Advance(t.Context(), branch, offered, MessageClaim,
				mustMarshalValue(Claim{Header: replyHeader(*assignment), Assignment: offered}), nil)
			require.NoError(t, err)

			tip := ChainTip{Branch: branch, OID: offered, Kind: MessageAssignment}
			task := &claimedTask{assignment: *assignment, tip: tip, claimed: claimed,
				expectedTip: claimed}
			task.reportPhase("build", true)
			worker := &Worker{Node: "build-a", Mailbox: node.mailbox, Log: zerolog.Nop()}
			seenCancel, _, _ := task.readProgress() // the last read in answerTask, before Result
			assert.Empty(t, seenCancel)

			_, err = orchestrator.mailbox.Reread(t.Context(), branch)
			require.NoError(t, err)
			writer := orchestrator.mailbox
			if !scenario.isOwn {
				otherSigner, signerErr := NewSigner("another-secret")
				require.NoError(t, signerErr)
				writer = NewGitMailbox(orchestrator.endpoint, orchestrator.git, otherSigner, zerolog.Nop())
			}
			cancel, err := writer.Advance(t.Context(), branch, claimed, MessageCancel,
				mustMarshalValue(Withdrawal{Header: assignment.Header,
					Assignment: offered, Tip: claimed}), nil)
			require.NoError(t, err)
			_, err = worker.advance(t.Context(), tip, claimed, MessageResult,
				Result{Header: replyHeader(*assignment), Assignment: offered,
					Status: StatusSucceeded}, nil)
			require.ErrorIs(t, err, gitx.ErrLeaseRejected)

			settled := worker.acknowledgeResultWithdrawal(t.Context(), task, claimed, zerolog.Nop())
			assert.Equal(t, scenario.isOwn, settled)
			head, err := orchestrator.mailbox.Reread(t.Context(), branch)
			require.NoError(t, err)
			if !scenario.isOwn {
				assert.Equal(t, cancel, head.OID, "the forged tip is not acknowledged")
				return
			}
			answer, err := orchestrator.mailbox.Inspect(t.Context(), head)
			require.NoError(t, err)
			assert.Equal(t, MessageAck, answer.Kind)
			assert.Equal(t, cancel, answer.PreviousOID)
			document, err := orchestrator.mailbox.Read(t.Context(), answer, 1<<20)
			require.NoError(t, err)
			var ack Ack
			require.NoError(t, json.Unmarshal(document, &ack))
			assert.Equal(t, offered, ack.Assignment)
			assert.Equal(t, cancel, ack.Cancel)
			assert.Equal(t, "build", ack.Phase)
			assert.True(t, ack.CommandStarted, "the acknowledgement preserves what actually ran")
		})
	}
}

// TestWorkerRefusesEveryUnacceptableAssignment: one row per acceptance rule.
// None of them is claimed, and the loop reports no progress, which is what
// keeps a mailbox full of rubbish from holding a node awake.
func TestWorkerRefusesEveryUnacceptableAssignment(t *testing.T) {
	branch := "dispat-worker-build-a-20260921-probe-abc"
	assignment := ChainTip{Branch: branch, OID: "assignment-oid", Kind: MessageAssignment}

	for name, tc := range map[string]struct {
		tip       ChainTip
		message   Assignment
		rejection RejectReason
	}{
		"another node's work": {
			tip:     assignment,
			message: func() Assignment { m := validProbe(branch); m.Node = "build-b"; return m }()},
		"another branch's message": {
			tip:     assignment,
			message: func() Assignment { m := validProbe(branch); m.Branch = "elsewhere"; return m }()},
		"another protocol version": {
			tip:     assignment,
			message: func() Assignment { m := validProbe(branch); m.Protocol = 9; return m }()},
		"a message issued last week": {
			tip: assignment,
			message: func() Assignment {
				m := validProbe(branch)
				m.IssuedAt = time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
				return m
			}()},
		"an assignment on an illegal chain": {
			tip:     ChainTip{Branch: branch, OID: "assignment-oid", Kind: MessageAssignment, Previous: MessageAssignment},
			message: validProbe(branch)},
		"a tip carrying no message": {
			tip:     ChainTip{Branch: branch, OID: "assignment-oid"},
			message: validProbe(branch)},
		"a document nobody signed": {
			tip: assignment, message: validProbe(branch), rejection: ReasonSignature},
		"a document larger than the ceiling": {
			tip: assignment, message: validProbe(branch), rejection: ReasonOversize},
	} {
		t.Run(name, func(t *testing.T) {
			worker, mailbox := newWorkerFixture(t, branch, tc.tip, tc.message)
			if tc.rejection != "" {
				mailbox.rejection[branch] = tc.rejection
			}

			isProgress := worker.tick(t.Context())

			assert.False(t, isProgress)
			assert.Empty(t, mailbox.written, "nothing is claimed and nothing is answered")
		})
	}

	t.Run("a document that is not the JSON it claims to be", func(t *testing.T) {
		worker, mailbox := newWorkerFixture(t, branch, assignment, validProbe(branch))
		mailbox.documents[branch] = []byte("{not json")

		assert.False(t, worker.tick(t.Context()))
		assert.Empty(t, mailbox.written)
	})

	t.Run("work this node has already answered", func(t *testing.T) {
		worker, mailbox := newWorkerFixture(t, branch, assignment, validProbe(branch))
		require.NoError(t, worker.Seen.Record("run-1", PreflightTask, 1, time.Now()))

		assert.False(t, worker.tick(t.Context()))
		assert.Empty(t, mailbox.written, "a replayed triple is refused however it arrives")
	})

	t.Run("a kind this build does not execute", func(t *testing.T) {
		publish := validProbe(branch)
		publish.Kind = KindPublish
		worker, mailbox := newWorkerFixture(t, branch, assignment, publish)

		assert.False(t, worker.tick(t.Context()))
		assert.Empty(t, mailbox.written, "work this node cannot run stays queued for one that can")
		assert.False(t, worker.Seen.IsSeen("run-1", PreflightTask, 1),
			"and is not remembered, so a node that learns to run it still can")
	})
}

// TestWorkerReadsABranchAgainWhenReadingItFailed: the poll memo is what keeps
// an unchanged branch from being read twice, and it is recorded as soon as a
// branch's objects are fetched. A read that then failed has consumed nothing,
// so a tip that never moves again would never be offered to this node a second
// time: the assignment would sit in the mailbox until the run gave up on it.
//
// A message this protocol refuses is the opposite case and is not forgotten:
// reading it again would reach the same decision every tick.
func TestWorkerReadsABranchAgainWhenReadingItFailed(t *testing.T) {
	branch := "dispat-worker-build-a-20260921-probe-abc"
	tip := ChainTip{Branch: branch, OID: "assignment-oid", Kind: MessageAssignment}
	for name, breakage := range map[string]struct {
		key            string
		err            error
		isReconsidered bool
	}{
		"the chain cannot be resolved": {key: "inspect:" + branch,
			err: errors.New("git failed"), isReconsidered: true},
		"the message cannot be read": {key: "read:" + branch,
			err: errors.New("git failed"), isReconsidered: true},
		"the message is refused": {key: "read:" + branch,
			err: &Rejection{Reason: ReasonOversize}},
	} {
		t.Run(name, func(t *testing.T) {
			worker, mailbox := newWorkerFixture(t, branch, tip, validProbe(branch))
			mailbox.unreadable[breakage.key] = breakage.err

			worker.tick(t.Context())

			assert.Empty(t, mailbox.written, "nothing was answered")
			if !breakage.isReconsidered {
				assert.Empty(t, mailbox.reconsidered)
				return
			}
			assert.Equal(t, []string{branch}, mailbox.reconsidered,
				"the next poll has to offer this branch again")
		})
	}
}

// TestWorkerLeavesQueuedWorkVisible: a node with no free slot leaves the
// assignment exactly where it is and forgets what it saw of the branch, so the
// next poll offers it again.
//
// Without the second half the work would wait for ever: nothing else is going
// to move that branch, so a memo saying "unchanged since the last poll" would
// mean "already dealt with" for the life of the process.
func TestWorkerLeavesQueuedWorkVisible(t *testing.T) {
	branch := "dispat-worker-build-a-20260921-build-abc"
	build := validProbe(branch)
	build.Kind, build.Task = KindBuild, "core:build"
	worker, mailbox := newWorkerFixture(t, branch,
		ChainTip{Branch: branch, OID: "assignment-oid", Kind: MessageAssignment}, build)
	worker.slots = make(chan struct{}, 1)
	worker.slots <- struct{}{}

	isProgress := worker.tick(t.Context())

	assert.False(t, isProgress)
	assert.Empty(t, mailbox.written, "a full node claims nothing")
	assert.Equal(t, []string{branch}, mailbox.reconsidered, "and the next poll offers it again")
	assert.False(t, worker.Seen.IsSeen("run-1", "core:build", 1))
}

// TestWorkerRecoversFromAStoreThatWentAway: the object cache is dispensable,
// so a failure opens it again and forgets what the memo said about objects
// the previous one held.
func TestWorkerRecoversFromAStoreThatWentAway(t *testing.T) {
	branch := "dispat-worker-build-a-20260921-probe-abc"
	tip := ChainTip{Branch: branch, OID: "assignment-oid", Kind: MessageAssignment}
	worker, mailbox := newWorkerFixture(t, branch, tip, validProbe(branch))
	failures := 0
	worker.PrepareStore = func(context.Context) error {
		failures++
		if failures == 1 {
			return assert.AnError
		}
		return nil
	}

	assert.False(t, worker.tick(t.Context()), "the first tick cannot open its store")
	assert.Equal(t, 0, mailbox.polls, "and polls nothing")

	assert.True(t, worker.tick(t.Context()), "the next one opens it again and serves")
	assert.Equal(t, 1, mailbox.forgotten, "the memo is dropped with the store it described")
	assert.Equal(t, []MessageKind{MessageClaim, MessageResult}, mailbox.written)
}

// TestWorkerStopsWhenItHasNothingToDo: --idle-timeout is what makes a node
// started for one release end by itself, and cancellation is what stops a
// long-running one.
func TestWorkerStopsWhenItHasNothingToDo(t *testing.T) {
	t.Run("idle", func(t *testing.T) {
		worker, _ := newWorkerFixture(t, "unused", ChainTip{}, validProbe("unused"))
		worker.Mailbox = &fakeMailbox{tips: map[string]ChainTip{}, documents: map[string][]byte{}}
		worker.IdleTimeout = 300 * time.Millisecond

		assert.Equal(t, StopIdle, worker.Serve(t.Context()))
	})

	t.Run("signalled", func(t *testing.T) {
		worker, _ := newWorkerFixture(t, "unused", ChainTip{}, validProbe("unused"))
		worker.Mailbox = &fakeMailbox{tips: map[string]ChainTip{}, documents: map[string][]byte{}}
		ctx, stop := context.WithCancel(t.Context())
		go func() {
			time.Sleep(300 * time.Millisecond)
			stop()
		}()

		assert.Equal(t, StopSignal, worker.Serve(ctx))
	})
}

// TestPollIntervalBacksOff: a node with nothing to do asks less and less
// often, up to a stated ceiling, and goes back to asking at once the moment
// anything arrives.
func TestPollIntervalBacksOff(t *testing.T) {
	interval := minimumPollInterval
	for range 10 {
		interval = resolvePollInterval(interval, false)
	}
	assert.Equal(t, maximumPollInterval, interval)
	assert.Equal(t, minimumPollInterval, resolvePollInterval(interval, true))
}

// TestAResultCarryingOutputsReportsUnderTheTransferWindow: a build's outputs
// travel inside the result push, so that push is bounded by the transfer
// timeout the operator set for outputs and never by the short report bound
// that a plain result gets; a 1.7 GB install tree once died of the latter.
func TestAResultCarryingOutputsReportsUnderTheTransferWindow(t *testing.T) {
	worker := &Worker{TransferTimeout: 3600 * time.Second}
	assert.Equal(t, 3600*time.Second, worker.resolveReportTimeout(true))
	assert.Equal(t, taskReportTimeout, worker.resolveReportTimeout(false),
		"a result carrying nothing keeps the short bound")
	assert.Equal(t, taskReportTimeout, (&Worker{TransferTimeout: time.Second}).resolveReportTimeout(true),
		"a transfer window shorter than the report bound never shortens a report")
}
