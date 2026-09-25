// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// The three ways a serving node used to stop seeing its work: one ref nobody
// can fetch, a transport branch read as an assignment, and an idle clock that
// counted the wrong thing.
//
// Each of them is a loop-level property rather than a message-level one, so
// each is asserted against the loop: a mailbox that fails one fetch, a branch
// whose name says it carries no work, and a node with a task in flight.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// unfetchableTransport answers the poll with the heads it was given and
// refuses to fetch the branches named in `broken`, in a batch or alone. It is
// the only fault that matters here: a ref that lists and does not fetch.
type unfetchableTransport struct {
	transportx
	heads   []gitx.RemoteHead
	broken  map[string]bool
	batches [][]string
}

func (t *unfetchableTransport) ListRemoteHeads(context.Context, string, string) ([]gitx.RemoteHead, error) {
	return t.heads, nil
}

func (t *unfetchableTransport) FetchRefs(_ context.Context, _ string, branches []string) error {
	t.batches = append(t.batches, branches)
	for _, branch := range branches {
		if t.broken[branch] {
			return errors.New("fatal: couldn't find remote ref " + branch)
		}
	}
	return nil
}

// TestMailboxPollSurvivesOneUnfetchableBranch: the whole poll used to fail on
// the batched fetch, so a namespace holding one bad ref never reported any of
// the work beside it. The batch is retried per branch, the good ones are
// answered, and the bad one is named once.
func TestMailboxPollSurvivesOneUnfetchableBranch(t *testing.T) {
	transport := &unfetchableTransport{
		heads: []gitx.RemoteHead{
			{Name: "dispat-worker-build-a-20260922-build-aa", OID: "oid-a"},
			{Name: "dispat-worker-build-a-20260922-relay-bb", OID: "oid-b"},
			{Name: "dispat-worker-build-a-20260922-build-cc", OID: "oid-c"},
		},
		broken: map[string]bool{"dispat-worker-build-a-20260922-relay-bb": true},
	}
	mailbox := NewGitMailbox("file:///srv/mailbox.git", nil, nil, zerolog.Nop())
	mailbox.remote = transport

	moved, err := mailbox.Observe(t.Context(), FormatBranchPattern("build-a"), nil)

	require.NoError(t, err)
	require.Len(t, moved, 2, "the readable branches are still reported")
	assert.Equal(t, "dispat-worker-build-a-20260922-build-aa", moved[0].Name)
	assert.Equal(t, "dispat-worker-build-a-20260922-build-cc", moved[1].Name)
	require.Len(t, transport.batches, 4, "one batch, then one call per branch")
	assert.True(t, mailbox.quarantined["dispat-worker-build-a-20260922-relay-bb"])

	// A second poll of unmoved branches reports nothing and fetches nothing:
	// the quarantined tip is remembered, so the node does not pay for it again.
	transport.heads = []gitx.RemoteHead{
		{Name: "dispat-worker-build-a-20260922-build-aa", OID: "oid-a"},
		{Name: "dispat-worker-build-a-20260922-relay-bb", OID: "oid-b"},
		{Name: "dispat-worker-build-a-20260922-build-cc", OID: "oid-c"},
	}
	transport.batches = nil
	moved, err = mailbox.Observe(t.Context(), FormatBranchPattern("build-a"), nil)
	require.NoError(t, err)
	assert.Empty(t, moved)
	assert.Empty(t, transport.batches)
}

// TestMailboxPollFailsWhenTheOnlyBranchIsUnreadable: a mailbox that is
// actually unreachable must still be reported as such, so the one-branch case
// answers the failure rather than an empty poll.
func TestMailboxPollFailsWhenTheOnlyBranchIsUnreadable(t *testing.T) {
	transport := &unfetchableTransport{
		heads:  []gitx.RemoteHead{{Name: "dispat-worker-build-a-20260922-build-aa", OID: "oid-a"}},
		broken: map[string]bool{"dispat-worker-build-a-20260922-build-aa": true},
	}
	mailbox := NewGitMailbox("file:///srv/mailbox.git", nil, nil, zerolog.Nop())
	mailbox.remote = transport

	_, err := mailbox.Observe(t.Context(), FormatBranchPattern("build-a"), nil)

	require.Error(t, err)
}

// cancellingTransport lists its heads and then cancels the poll during the
// fetch, the way a signal stops a node while a fetch is in flight.
type cancellingTransport struct {
	transportx
	heads  []gitx.RemoteHead
	cancel context.CancelFunc
}

func (t *cancellingTransport) ListRemoteHeads(context.Context, string, string) ([]gitx.RemoteHead, error) {
	return t.heads, nil
}

func (t *cancellingTransport) FetchRefs(context.Context, string, []string) error {
	t.cancel()
	return errors.New("signal: interrupt")
}

// TestMailboxPollInterruptedByAStopQuarantinesNothing: a fetch cut short
// because the node is stopping says nothing about the branches it was
// fetching. They are neither quarantined nor reported as unreadable (W244),
// and the next poll reads them again.
func TestMailboxPollInterruptedByAStopQuarantinesNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	transport := &cancellingTransport{
		heads: []gitx.RemoteHead{
			{Name: "dispat-worker-build-a-20260922-build-aa", OID: "oid-a"},
			{Name: "dispat-worker-build-a-20260922-build-bb", OID: "oid-b"},
		},
		cancel: cancel,
	}
	mailbox := NewGitMailbox("file:///srv/mailbox.git", nil, nil, zerolog.Nop())
	mailbox.remote = transport

	_, err := mailbox.Observe(ctx, FormatBranchPattern("build-a"), nil)

	require.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, mailbox.quarantined, "a stopping poll quarantines nothing")
	assert.Empty(t, mailbox.observed, "nothing was read, so nothing counts as seen")
}

// TestBranchKindHintTellsTransportBranchesApart: the name is a hint and is
// used for exactly one decision, which is to skip reading a branch that could
// not be work. A node name holding hyphens must not confuse it.
func TestBranchKindHintTellsTransportBranchesApart(t *testing.T) {
	for name, tc := range map[string]struct {
		branch    string
		kind      string
		isCarried bool
	}{
		"a build": {
			branch: "dispat-worker-build-a-20260922-build-0f1e", kind: KindBuild, isCarried: true},
		"a publication": {
			branch: "dispat-worker-b-20260922-publish-0f1e", kind: KindPublish, isCarried: true},
		"a probe": {
			branch: "dispat-worker-b-20260922-probe-0f1e", kind: KindProbe, isCarried: true},
		"a sweep task": {
			branch: "dispat-worker-build-a-20260922-run-0f1e", kind: KindRun, isCarried: true},
		"a prepared input state": {
			branch: "dispat-worker-build-a-20260922-snapshot-0f1e", kind: KindSnapshot},
		"a relayed result": {
			branch: "dispat-worker-build-a-20260922-relay-0f1e", kind: KindRelay},
		"a name that is not one of ours": {
			branch: "main", kind: "", isCarried: true},
		"a kind this build does not know": {
			branch: "dispat-worker-b-20260922-bundle-0f1e", kind: "bundle", isCarried: true},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.kind, ResolveBranchKindHint(tc.branch))
			assert.Equal(t, tc.isCarried, IsBranchCarryingWork(tc.branch))
		})
	}
}

// TestWorkerSkipsTransportBranchesWithoutReadingThem: a prepared input state
// pushed into the node's own namespace used to be inspected and reported as an
// assignment nobody could read, once per snapshot and at warning level.
func TestWorkerSkipsTransportBranchesWithoutReadingThem(t *testing.T) {
	branch := "dispat-worker-build-a-20260922-snapshot-abc"
	tip := ChainTip{Branch: branch, OID: "snapshot-oid", isProtocol: true}
	worker, mailbox := newWorkerFixture(t, branch, tip, validProbe(branch))

	isProgress := worker.tick(t.Context())

	assert.False(t, isProgress)
	assert.Empty(t, mailbox.written, "nothing is claimed and nothing is answered")
	assert.Equal(t, 1, mailbox.polls)
}

// TestWorkerIdleClockCountsFromTheLastThingItDid: the three states that are
// not idleness. A node that has just started, a node with a task in flight,
// and a node whose task has only just ended each answer a positive remainder,
// which is what stops the loop from reporting `idle`.
func TestWorkerIdleClockCountsFromTheLastThingItDid(t *testing.T) {
	worker := &Worker{IdleTimeout: time.Minute, Log: zerolog.Nop()}

	worker.markActive()
	assert.Positive(t, worker.resolveIdleRemainder(), "a node that has just started is not idle")

	worker.lastActive = time.Now().Add(-2 * time.Minute)
	assert.LessOrEqual(t, worker.resolveIdleRemainder(), time.Duration(0),
		"a node that has had nothing to do for longer than the timeout may stop")

	worker.beginTask()
	assert.Equal(t, time.Minute, worker.resolveIdleRemainder(),
		"a node with a task in flight is never idle, however long the task runs")

	worker.lastActive = time.Now().Add(-2 * time.Minute)
	assert.Equal(t, time.Minute, worker.resolveIdleRemainder())

	worker.endTask()
	assert.Positive(t, worker.resolveIdleRemainder(),
		"the clock restarts when the task ends, so a long build is not followed by an immediate stop")
}

// TestMailboxInspectsTheChainUnderAForeignTip: an attempt's answer used to be
// findable only at the branch tip, so anybody with write access to the mailbox
// could bury a finished result under one commit and make the run wait out the
// whole task deadline. The chain under the tip is read instead, oldest first,
// and every step of it carries what it carried.
func TestMailboxInspectsTheChainUnderAForeignTip(t *testing.T) {
	fixture := newMailboxFixture(t)
	branch := FormatBranch("build-a", KindBuild, time.Now())
	offered, err := assign(t.Context(), fixture.mailbox, probeAssignment("build-a", branch))
	require.NoError(t, err)
	claimed, err := fixture.mailbox.Advance(t.Context(), branch, offered, MessageClaim,
		[]byte(`{"assignment":"`+offered+`"}`), nil)
	require.NoError(t, err)
	reported, err := fixture.mailbox.Advance(t.Context(), branch, claimed, MessageResult,
		[]byte(`{"status":"succeeded"}`), nil)
	require.NoError(t, err)
	// One commit of this protocol's shape, of a kind no party acts on, pushed
	// on top of the finished attempt by whoever can write to the mailbox.
	foreign, err := formatMessageCommit(t.Context(), fixture.git, fixture.signer,
		"noise", []byte(`{}`), []string{reported}, nil)
	require.NoError(t, err)
	require.NoError(t, fixture.git.PushAdvance(t.Context(), fixture.endpoint, foreign, branch, reported))

	heads, err := fixture.mailbox.Observe(t.Context(), FormatBranchPattern("build-a"), nil)
	require.NoError(t, err)
	require.Len(t, heads, 1)
	tip, err := fixture.mailbox.Inspect(t.Context(), heads[0])
	require.NoError(t, err)
	assert.Equal(t, MessageKind("noise"), tip.Kind, "the tip answers nothing an attempt waits for")

	ancestors, err := fixture.mailbox.InspectChain(t.Context(), heads[0])

	require.NoError(t, err)
	require.Len(t, ancestors, 3)
	assert.Equal(t, MessageAssignment, ancestors[0].Kind)
	assert.Equal(t, MessageClaim, ancestors[1].Kind)
	assert.Equal(t, MessageResult, ancestors[2].Kind, "the result is where the writer left it")
	assert.Equal(t, MessageClaim, ancestors[2].Previous)
	assert.Equal(t, claimed, ancestors[2].PreviousOID)
	document, err := fixture.mailbox.Read(t.Context(), ancestors[2], 1<<20)
	require.NoError(t, err)
	assert.JSONEq(t, `{"status":"succeeded"}`, string(document))
}

// TestAWithdrawalNobodySignedStopsNothing: the worker used to acknowledge a
// `cancel` tip for carrying the word, so anybody who could write into a
// mailbox could refuse every publication of every run. The message is held to
// the rules an authorization is held to, and a withdrawal of an earlier state
// of the branch is not a withdrawal of the one the node waits at.
func TestAWithdrawalNobodySignedStopsNothing(t *testing.T) {
	branch := "dispat-worker-build-a-20260922-publish-abc"
	assignment := Assignment{Header: Header{
		Protocol: ProtocolVersion, Kind: KindPublish, Run: "run-1", PlanDigest: "digest",
		Task: "app:publish", Attempt: 1, Generation: "generation", Node: "build-a",
		Branch: branch, IssuedAt: time.Now().UTC().Format(time.RFC3339),
	}, Limits: TransferLimits{MaxManifestBytes: 1 << 20}}
	tip := ChainTip{Branch: branch, OID: "assignment-oid", Kind: MessageAssignment}
	answer := ChainTip{Branch: branch, OID: "cancel-oid", Kind: MessageCancel,
		Previous: MessageReady, PreviousOID: "ready-oid"}
	valid := Withdrawal{Header: assignment.Header, Assignment: "assignment-oid", Tip: "ready-oid"}

	for name, tc := range map[string]struct {
		message   Withdrawal
		rejection RejectReason
		answer    ChainTip
		reason    RejectReason
	}{
		"the run's own withdrawal": {message: valid},
		"one nobody signed": {
			message: valid, rejection: ReasonSignature, reason: ReasonSignature},
		"one addressed to another node": {
			message: func() Withdrawal { m := valid; m.Node = "build-b"; return m }(),
			reason:  ReasonNode},
		"one of another run": {
			message: func() Withdrawal { m := valid; m.Run = "run-2"; return m }(),
			reason:  ReasonReplay},
		"one for another kind of work": {
			message: func() Withdrawal { m := valid; m.Kind = KindBuild; return m }(),
			reason:  ReasonReplay},
		"one of another attempt": {
			message: func() Withdrawal { m := valid; m.Attempt = 2; return m }(),
			reason:  ReasonReplay},
		"one under another ownership": {
			message: func() Withdrawal { m := valid; m.Generation = "older"; return m }(),
			reason:  ReasonReplay},
		"one withdrawing an earlier state of the branch": {
			message: func() Withdrawal { m := valid; m.Tip = "claim-oid"; return m }(),
			reason:  ReasonReplay},
		// The chain step and the echoes are one refusal rather than one each,
		// exactly as they are for an authorization: which of them a forged
		// message failed tells an attacker more than it tells an operator.
		"one written on a step no party could have written it on": {
			message: valid,
			answer: ChainTip{Branch: branch, OID: "cancel-oid", Kind: MessageCancel,
				Previous: MessageResult, PreviousOID: "ready-oid"},
			reason: ReasonReplay},
	} {
		t.Run(name, func(t *testing.T) {
			document, err := json.Marshal(tc.message)
			require.NoError(t, err)
			mailbox := &fakeMailbox{
				documents: map[string][]byte{branch: document},
				rejection: map[string]RejectReason{},
			}
			if tc.rejection != "" {
				mailbox.rejection[branch] = tc.rejection
			}
			worker := &Worker{Node: "build-a", Mailbox: mailbox, Log: zerolog.Nop()}
			observed := answer
			if tc.answer.OID != "" {
				observed = tc.answer
			}

			reason := worker.checkWithdrawal(t.Context(), observed, tip, "ready-oid", assignment)

			assert.Equal(t, tc.reason, reason)
		})
	}
}
