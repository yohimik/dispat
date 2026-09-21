// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parseTestTime is one RFC3339 instant, for the fixtures that state a clock
// rather than reading one.
func parseTestTime(t *testing.T, value string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, value)
	require.NoError(t, err)
	return at
}

// TestEveryTransitionHasOneWriter: the protocol as a table, asserted from
// both sides. Each legal step is legal for exactly one party, and the steps
// that would let a node authorize itself or an orchestrator report its own
// work are not steps at all.
func TestEveryTransitionHasOneWriter(t *testing.T) {
	legal := []Transition{
		{From: "", To: MessageAssignment, By: PartyOrchestrator},
		{From: MessageAssignment, To: MessageClaim, By: PartyWorker},
		{From: MessageClaim, To: MessageReady, By: PartyWorker},
		{From: MessageReady, To: MessageGo, By: PartyOrchestrator},
		{From: MessageGo, To: MessageResult, By: PartyWorker},
		{From: MessageClaim, To: MessageResult, By: PartyWorker},
		{From: MessageAssignment, To: MessageCancel, By: PartyOrchestrator},
		{From: MessageClaim, To: MessageCancel, By: PartyOrchestrator},
		{From: MessageReady, To: MessageCancel, By: PartyOrchestrator},
		{From: MessageCancel, To: MessageAck, By: PartyWorker},
	}
	for _, transition := range legal {
		assert.True(t, IsTransitionLegal(transition.From, transition.To, transition.By),
			"%s -> %s by %s is the protocol", transition.From, transition.To, transition.By)
		other := PartyWorker
		if transition.By == PartyWorker {
			other = PartyOrchestrator
		}
		assert.False(t, IsTransitionLegal(transition.From, transition.To, other),
			"%s -> %s is not %s's to write", transition.From, transition.To, other)
	}

	for name, tc := range map[string]Transition{
		"a node authorizing its own publication": {From: MessageReady, To: MessageGo, By: PartyWorker},
		"a second assignment on one branch":      {From: MessageAssignment, To: MessageAssignment, By: PartyOrchestrator},
		"a result with nothing claimed":          {From: MessageAssignment, To: MessageResult, By: PartyWorker},
		"a claim of a closed attempt":            {From: MessageResult, To: MessageClaim, By: PartyWorker},
		"an authorization with nothing ready":    {From: MessageClaim, To: MessageGo, By: PartyOrchestrator},
		"work after an acknowledged cancel":      {From: MessageAck, To: MessageResult, By: PartyWorker},
		"a cancel of a finished attempt":         {From: MessageResult, To: MessageCancel, By: PartyOrchestrator},
		"an orphan claim":                        {From: "", To: MessageClaim, By: PartyWorker},
		"an acknowledgement of nothing":          {From: MessageAssignment, To: MessageAck, By: PartyWorker},
	} {
		t.Run(name, func(t *testing.T) {
			assert.False(t, IsTransitionLegal(tc.From, tc.To, tc.By))
		})
	}
}

// TestWorkerClaimsOnlyAnAssignmentOnAProperChain: what a serving node does
// with a branch is decided by the chain alone, and only one shape is work it
// may take.
func TestWorkerClaimsOnlyAnAssignmentOnAProperChain(t *testing.T) {
	for name, tc := range map[string]struct {
		tip  ChainTip
		want Action
	}{
		"a probe assignment on a root commit": {
			tip: ChainTip{Kind: MessageAssignment}, want: ActionClaim},
		"an assignment on a source snapshot": {
			tip: ChainTip{Kind: MessageAssignment, Previous: ""}, want: ActionClaim},
		"an assignment on another assignment": {
			tip: ChainTip{Kind: MessageAssignment, Previous: MessageAssignment}, want: ActionNone},
		"an assignment on a claim": {
			tip: ChainTip{Kind: MessageAssignment, Previous: MessageClaim}, want: ActionNone},
		"a branch this node already claimed": {
			tip: ChainTip{Kind: MessageClaim, Previous: MessageAssignment}, want: ActionNone},
		"a branch this node already answered": {
			tip: ChainTip{Kind: MessageResult, Previous: MessageClaim}, want: ActionNone},
		"a tip carrying no message at all": {
			tip: ChainTip{}, want: ActionNone},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, ResolveWorkerAction(tc.tip))
		})
	}
}

// TestHeaderAcceptanceRules: every rule a message is held to whichever side
// reads it, each failing one on its own and naming a reason a log can be
// filtered on.
func TestHeaderAcceptanceRules(t *testing.T) {
	now := parseTestTime(t, "2026-09-21T12:00:00Z")
	binding := Binding{Node: "build-a", Branch: "dispat-worker-build-a-20260921-probe-abc"}
	valid := Header{
		Protocol: ProtocolVersion, Kind: KindProbe, Run: "run-1", Task: "preflight",
		Attempt: 1, Node: "build-a", Branch: binding.Branch,
		IssuedAt: "2026-09-21T11:59:00Z",
	}

	assert.Empty(t, CheckHeader(valid, binding, now))

	for name, tc := range map[string]struct {
		change func(*Header)
		want   RejectReason
	}{
		"another protocol version": {
			change: func(h *Header) { h.Protocol = ProtocolVersion + 1 }, want: ReasonProtocol},
		"no protocol version at all": {
			change: func(h *Header) { h.Protocol = 0 }, want: ReasonProtocol},
		"another node": {
			change: func(h *Header) { h.Node = "build-b" }, want: ReasonNode},
		"a node whose name differs by case": {
			change: func(h *Header) { h.Node = "Build-A" }, want: ReasonNode},
		"another attempt's branch": {
			change: func(h *Header) { h.Branch = "dispat-worker-build-a-20260921-probe-def" }, want: ReasonBranch},
		"a timestamp from last week": {
			change: func(h *Header) { h.IssuedAt = "2026-09-14T12:00:00Z" }, want: ReasonIssuedAt},
		"a timestamp from next week": {
			change: func(h *Header) { h.IssuedAt = "2026-09-28T12:00:00Z" }, want: ReasonIssuedAt},
		"a timestamp that is not one": {
			change: func(h *Header) { h.IssuedAt = "yesterday" }, want: ReasonIssuedAt},
		"no timestamp at all": {
			change: func(h *Header) { h.IssuedAt = "" }, want: ReasonIssuedAt},
	} {
		t.Run(name, func(t *testing.T) {
			header := valid
			tc.change(&header)
			assert.Equal(t, tc.want, CheckHeader(header, binding, now))
		})
	}

	t.Run("a clock that differs by an hour is still accepted", func(t *testing.T) {
		header := valid
		header.IssuedAt = "2026-09-21T13:00:00Z"
		assert.Empty(t, CheckHeader(header, binding, now))
	})
}

// TestRejectionCarriesItsReason: the reason is read off the error, so the
// caller logs an enum rather than parsing a sentence, and an error that is
// not a rejection answers nothing.
func TestRejectionCarriesItsReason(t *testing.T) {
	assert.Equal(t, ReasonSignature, RejectionReason(&Rejection{Reason: ReasonSignature}))
	assert.Contains(t, (&Rejection{Reason: ReasonReplay}).Error(), "replay")
	assert.Empty(t, RejectionReason(nil))
	assert.Empty(t, RejectionReason(ErrNoSecret))
}
