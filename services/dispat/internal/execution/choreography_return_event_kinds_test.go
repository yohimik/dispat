// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

type choreographyHTTPSFeedBroker struct {
	*FirstNodeBroker
	events []ChoreographyEvent
}

func (b *choreographyHTTPSFeedBroker) ReadEvents(_ context.Context,
	cursor ChoreographyEventCursor, maxEvents int) (ChoreographyEventPage, error) {
	start := int(cursor.Sequence)
	end := min(start+maxEvents, len(b.events))
	page := ChoreographyEventPage{Events: append([]ChoreographyEvent(nil), b.events[start:end]...),
		Cursor: cursor, Head: ChoreographyEventCursor{OID: b.events[len(b.events)-1].LedgerOID,
			Sequence: uint64(len(b.events))}, More: end < len(b.events)}
	if end > start {
		page.Cursor = ChoreographyEventCursor{OID: b.events[end-1].LedgerOID,
			Sequence: uint64(end)}
	}
	return page, nil
}

func TestChoreographyHTTPSFeedAcceptsStartJoinPreparedClaimSequence(t *testing.T) {
	f, _, opts := newExecutionStartFixture(t)
	broker, err := NewFirstNodeBroker(opts)
	require.NoError(t, err)
	feed := &choreographyHTTPSFeedBroker{FirstNodeBroker: broker, events: []ChoreographyEvent{
		{Sequence: 1, LedgerOID: "ledger-1", Kind: ChoreographyInitializationComplete,
			Task: choreographyRunTaskID, TaskOID: f.root},
		{Sequence: 2, LedgerOID: "ledger-2", Kind: ChoreographyExecutionStarted,
			Task: choreographyRunTaskID, TaskOID: f.root},
		{Sequence: 3, LedgerOID: "ledger-3", Kind: ChoreographyMemberJoined,
			Task: choreographyRunTaskID, TaskOID: f.root},
		{Sequence: 4, LedgerOID: "ledger-4", Kind: ChoreographyFrameReady,
			Task: f.spec.Task, TaskOID: f.taskRoot, Attempt: 1},
		{Sequence: 5, LedgerOID: "ledger-5", Kind: ChoreographyClaimAccepted,
			Task: f.spec.Task, TaskOID: "claim-1", Attempt: 1},
	}}
	handler, err := NewChoreographyReturnHandler(ReturnHandlerOptions{
		Broker: feed, Signer: f.endpoint.signer, Binding: f.binding,
		MaxClaimBytes: 1 << 20, MaxResultBytes: 1 << 20, TempDir: t.TempDir()})
	require.NoError(t, err)
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	pinned := sha256.Sum256(server.Certificate().Raw)
	client, err := NewChoreographyReturnClient(ReturnClientOptions{BaseURL: server.URL,
		PinnedCertSHA256: hex.EncodeToString(pinned[:]), Signer: f.endpoint.signer,
		Binding: f.binding, MaxResultBytes: 1 << 20})
	require.NoError(t, err)
	defer client.Close()
	page, err := client.PollEvents(t.Context(), ChoreographyEventRequest{
		ChoreographyRunBinding: f.binding, Node: "peer", MaxEvents: 5})
	require.NoError(t, err)
	require.Len(t, page.Events, len(feed.events))
	for index, event := range feed.events {
		require.Equal(t, event.Kind, page.Events[index].Kind)
	}
}
