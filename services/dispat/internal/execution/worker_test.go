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

	written      []MessageKind
	fetched      []string
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
	return m.tips[head.Name], nil
}

func (m *fakeMailbox) Read(_ context.Context, tip ChainTip, _ int64) ([]byte, error) {
	if reason, isRejected := m.rejection[tip.Branch]; isRejected {
		return nil, &Rejection{Reason: reason}
	}
	return m.documents[tip.Branch], nil
}

func (m *fakeMailbox) Advance(_ context.Context, branch, _ string, kind MessageKind, document []byte) (string, error) {
	m.written = append(m.written, kind)
	m.documents[branch+"/"+string(kind)] = document
	return string(kind) + "-oid", nil
}

func (m *fakeMailbox) Fetch(_ context.Context, branches []string) error {
	m.fetched = append(m.fetched, branches...)
	return nil
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
		heads:     []gitx.RemoteHead{{Name: branch, OID: tip.OID}},
		tips:      map[string]ChainTip{branch: tip},
		documents: map[string][]byte{branch: document},
		rejection: map[string]RejectReason{},
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
