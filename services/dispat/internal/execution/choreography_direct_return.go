// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"
	"io"
	"os"
)

const choreographyDirectReturnBrokerUnavailableError = "execution: local return broker is unavailable"
const choreographyDirectReturnPeerProbeError = "execution: first-local task does not answer a peer probe"
const choreographyDirectReturnResultFileError = "execution: local result pack must be a bounded temporary file"
const choreographyDirectReturnEventRunError = "execution: local event request belongs to another run"

// DirectChoreographyReturn gives the first node's own Worker the same
// receipt-gated broker path as a remote choreographer without an HTTPS
// round trip to its public return endpoint. No Git push capability crosses
// this boundary: only the broker's verified persistence owns refs.
func DirectChoreographyReturn(broker *FirstNodeBroker) (workerReturnx, error) {
	if broker == nil {
		return nil, fmt.Errorf(choreographyDirectReturnBrokerUnavailableError)
	}
	return &directChoreographyReturn{broker: broker}, nil
}

type directChoreographyReturn struct{ broker *FirstNodeBroker }

func (d *directChoreographyReturn) SubmitProbe(context.Context, ChoreographyProbeReply) (ChoreographyReceipt, error) {
	return ChoreographyReceipt{}, fmt.Errorf(choreographyDirectReturnPeerProbeError)
}
func (d *directChoreographyReturn) SubmitClaim(ctx context.Context, p ChoreographyClaimProposal) (ChoreographyReceipt, error) {
	return d.broker.AcceptClaim(ctx, p)
}
func (d *directChoreographyReturn) SubmitResult(ctx context.Context, p ChoreographyResultProposal,
	payload io.ReadSeeker) (ChoreographyReceipt, error) {
	file, ok := payload.(*os.File)
	if !ok {
		return ChoreographyReceipt{}, fmt.Errorf(choreographyDirectReturnResultFileError)
	}
	return d.broker.AcceptResult(ctx, p, file)
}
func (d *directChoreographyReturn) SubmitAck(ctx context.Context, p ChoreographyAckProposal) (ChoreographyReceipt, error) {
	return d.broker.AcceptAck(ctx, p)
}
func (d *directChoreographyReturn) LookupReceipt(ctx context.Context, q ChoreographyReceiptQuery) (ChoreographyReceipt, bool, error) {
	return d.broker.LookupReceipt(ctx, q)
}
func (d *directChoreographyReturn) PollEvents(ctx context.Context, request ChoreographyEventRequest) (ChoreographyEventPage, error) {
	if request.ChoreographyRunBinding != d.broker.binding {
		return ChoreographyEventPage{}, fmt.Errorf(choreographyDirectReturnEventRunError)
	}
	return d.broker.ReadEvents(ctx, request.Cursor, request.MaxEvents)
}
func (d *directChoreographyReturn) WaitTaskEvent(ctx context.Context, task string,
	after ChoreographyEventCursor) (ChoreographyEvent, ChoreographyEventCursor, error) {
	return d.broker.WaitTaskEvent(ctx, task, after)
}
func (*directChoreographyReturn) Close() {}
