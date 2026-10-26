// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"
	"slices"
)

const (
	compiledGraphReadFailureLimit        = 3
	compiledGraphUnavailableErrorFormat  = "execution: admitted node cannot read the complete signed graph after %d attempts: %w"
	compiledGraphDrainUnknownErrorFormat = "execution: exact graph-unavailability drain has no signed receipt: %w"
	compiledGraphDrainRefusedErrorFormat = "execution: graph-unavailability drain for node %q was refused: %s"
	compiledGraphDrainIdentityError      = "execution: graph-unavailability drain differs from the signed run or admitted node"
	compiledGraphDrainPageError          = "execution: graph-unavailability drain cannot verify the initialization event"
	compiledGraphDrainedLogMessage       = "the admitted node drained after repeated failure to read the signed task graph"
	compiledGraphDrainLogRunField        = "run"
	compiledGraphDrainNoProgress         = false
	compiledGraphDrainProgress           = true
)

// workerCompiledUnavailable belongs to the poll goroutine. Once a drain is
// proposed, an uncertain reply keeps that exact proposal until lookup proves
// its result. No task lease or Result is synthesized for a failed graph read.
type workerCompiledUnavailable struct {
	bootstrapOID string
	rootOID      string
	failures     int
	channel      *workerReturnChannel
	proposal     *ChoreographyDrainProposal
	drained      bool
}

type compiledGraphReadFailure struct {
	Notice    SlotNotice
	Bootstrap RunBootstrap
	ReadErr   error
}

type compiledUnavailableDrainPreparation struct {
	Notice    SlotNotice
	Bootstrap RunBootstrap
	State     *workerCompiledUnavailable
}

func (w *Worker) compiledUnavailableState(notice SlotNotice) (*workerCompiledUnavailable, error) {
	if w.compiledUnavailable == nil {
		w.compiledUnavailable = make(map[string]*workerCompiledUnavailable)
	}
	state := w.compiledUnavailable[notice.Run]
	if state == nil {
		state = &workerCompiledUnavailable{bootstrapOID: notice.BootstrapOID,
			rootOID: notice.GraphRootOID}
		w.compiledUnavailable[notice.Run] = state
	}
	if state.bootstrapOID != notice.BootstrapOID || state.rootOID != notice.GraphRootOID {
		return nil, fmt.Errorf(compiledGraphDrainIdentityError)
	}
	return state, nil
}

func (w *Worker) recordCompiledGraphReadFailure(ctx context.Context,
	failure compiledGraphReadFailure) (bool, error) {
	notice, bootstrap, readErr := failure.Notice, failure.Bootstrap, failure.ReadErr
	state, err := w.compiledUnavailableState(notice)
	if err != nil {
		return compiledGraphDrainNoProgress, err
	}
	if state.drained {
		return compiledGraphDrainNoProgress, nil
	}
	state.failures++
	if state.failures < compiledGraphReadFailureLimit {
		return compiledGraphDrainNoProgress, readErr
	}
	if state.proposal == nil {
		if err := w.prepareCompiledUnavailableDrain(ctx, compiledUnavailableDrainPreparation{
			Notice: notice, Bootstrap: bootstrap, State: state}); err != nil {
			return compiledGraphDrainNoProgress, err
		}
	}
	settled, err := w.settleCompiledUnavailableDrain(ctx, state)
	if err != nil {
		return compiledGraphDrainNoProgress,
			fmt.Errorf(compiledGraphUnavailableErrorFormat, state.failures, err)
	}
	if settled {
		w.Log.Error().Err(readErr).Str(compiledGraphDrainLogRunField, notice.Run).Msg(compiledGraphDrainedLogMessage)
	}
	return settled, nil
}

func (w *Worker) prepareCompiledUnavailableDrain(ctx context.Context,
	preparation compiledUnavailableDrainPreparation) error {
	notice, bootstrap, state := preparation.Notice, preparation.Bootstrap, preparation.State
	mailbox, ok := w.Mailbox.(*GitMailbox)
	if !ok || w.NodeSlots == nil || w.Report.AuthorityID != w.NodeSlots.AuthorityID() {
		return fmt.Errorf(compiledGraphDrainIdentityError)
	}
	_, roster, err := ReadChoreographyLedgerRoot(ctx, mailbox,
		bootstrap.ChoreographyRunBinding, notice.BootstrapOID)
	if err != nil {
		return err
	}
	index := slices.IndexFunc(roster.Nodes, func(node ChoreographyRuntimeNode) bool {
		return node.Name == w.Node
	})
	if index < compiledFirstRosterIndex || roster.Nodes[index].First ||
		roster.Nodes[index].AuthorityID != w.Report.AuthorityID ||
		roster.Nodes[index].Capacity != w.Report.Capacity ||
		roster.Nodes[index].Platform != w.Report.OS+nodePlatformSeparator+w.Report.Arch {
		return fmt.Errorf(compiledGraphDrainIdentityError)
	}
	if state.channel == nil {
		state.channel, err = w.prepareReturnChannel(bootstrap.ChoreographyRunBinding,
			bootstrap.ReturnEndpoint, bootstrap.ReturnCertSHA256)
		if err != nil {
			return err
		}
	}
	page, err := state.channel.client.PollEvents(ctx, ChoreographyEventRequest{
		ChoreographyRunBinding: bootstrap.ChoreographyRunBinding,
		Node:                   w.Node, MaxEvents: compiledSingleHeadEvent})
	if err != nil {
		return err
	}
	if len(page.Events) != compiledSingleHeadEvent ||
		page.Events[compiledFirstSelectionIndex].Kind != ChoreographyInitializationComplete ||
		page.Events[compiledFirstSelectionIndex].GraphRootOID != notice.GraphRootOID ||
		page.Events[compiledFirstSelectionIndex].LedgerOID == compiledMissingIdentity ||
		page.Head.OID == compiledMissingIdentity {
		return fmt.Errorf(compiledGraphDrainPageError)
	}
	epoch := page.Events[compiledFirstSelectionIndex].LedgerOID
	proposal := ChoreographyDrainProposal{ChoreographyRunBinding: bootstrap.ChoreographyRunBinding,
		BootstrapOID: notice.BootstrapOID, GraphRootOID: notice.GraphRootOID,
		Node: w.Node, CapacityAuthorityID: w.Report.AuthorityID,
		MembershipEpochOID: epoch, ExpectedLedgerOID: page.Head.OID,
		RequestID: returnProposalID(choreographyDrainKind, bootstrap.Run,
			w.Node, minimumCompiledAttempt, epoch+page.Head.OID)}
	if len(w.JoinIdentityKey) != compiledNoJoinKeyBytes {
		if err := SignChoreographyDrainProposal(&proposal, w.JoinIdentityKey); err != nil {
			return err
		}
	}
	state.proposal = &proposal
	return nil
}

func (w *Worker) settleCompiledUnavailableDrain(ctx context.Context,
	state *workerCompiledUnavailable) (bool, error) {
	if state == nil || state.channel == nil || state.proposal == nil {
		return compiledGraphDrainNoProgress, fmt.Errorf(compiledGraphDrainIdentityError)
	}
	client, ok := state.channel.client.(workerDrainReturnx)
	if !ok {
		return compiledGraphDrainNoProgress, fmt.Errorf(compiledGraphDrainIdentityError)
	}
	query, err := FormatChoreographyDrainReceiptQuery(*state.proposal)
	if err != nil {
		return compiledGraphDrainNoProgress, err
	}
	receipt, submitErr := client.SubmitDrain(ctx, *state.proposal)
	if submitErr != nil {
		var found bool
		receipt, found, err = client.LookupDrain(ctx, query)
		if err != nil {
			return compiledGraphDrainNoProgress, err
		}
		if !found {
			return compiledGraphDrainNoProgress,
				fmt.Errorf(compiledGraphDrainUnknownErrorFormat, submitErr)
		}
	}
	if receipt.ChoreographyRunBinding != state.proposal.ChoreographyRunBinding ||
		receipt.BootstrapOID != state.bootstrapOID || receipt.GraphRootOID != state.rootOID ||
		receipt.Node != state.proposal.Node || receipt.RequestID != state.proposal.RequestID ||
		receipt.RequestDigest != query.RequestDigest || receipt.Cursor.OID == compiledMissingIdentity {
		return compiledGraphDrainNoProgress, fmt.Errorf(compiledGraphDrainIdentityError)
	}
	if receipt.Refused && receipt.Reason == choreographyDrainRefusalStale &&
		receipt.Cursor.OID != state.proposal.ExpectedLedgerOID {
		state.proposal = nil
		return compiledGraphDrainNoProgress,
			fmt.Errorf(compiledGraphDrainRefusedErrorFormat, receipt.Node, receipt.Reason)
	}
	if !receipt.Accepted {
		return compiledGraphDrainNoProgress,
			fmt.Errorf(compiledGraphDrainRefusedErrorFormat, receipt.Node, receipt.Reason)
	}
	state.drained = compiledDrainAccepted
	state.channel.client.Close()
	state.channel = nil
	state.proposal = nil
	return compiledGraphDrainProgress, nil
}
