// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"
	"reflect"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// recoveredNodeSlot tracks one persisted attempt while a retained signed run
// ledger is replayed once. Missing or ambiguous evidence leaves its lease held.
type recoveredNodeSlot struct {
	lease         NodeSlotLease
	claimOID      string
	assignmentOID string
	effectOID     string
	refused       bool
	terminalOID   string
	terminal      *ChoreographyEvent
}

func (s *recoveredNodeSlot) settlementEvidence(priorSequence uint64,
	head ChoreographyEventCursor) string {
	if s.terminalOID != nodeSlotAbsentIdentityText {
		return s.terminalOID
	}
	if s.claimOID == nodeSlotAbsentIdentityText && head.Sequence > priorSequence {
		// The old ledger epoch cannot win a later CAS. The complete signed
		// replay found no matching accepted claim, even if HTTP was ambiguous.
		return head.OID
	}
	return nodeSlotAbsentIdentityText
}

type recoveredNodeSlotKey struct {
	task    string
	attempt int
	node    string
}

func (r NodeSlotRecoveryLocator) expectedTaskOID() string {
	if r.ExpectedTaskOID != nodeSlotAbsentIdentityText {
		return r.ExpectedTaskOID
	}
	return r.AssignmentOID
}

type recoveredNativeNodeSlot struct {
	lease    NodeSlotLease
	finality *ChoreographyEvent
}

func (s *recoveredNativeNodeSlot) apply(event ChoreographyEvent) error {
	if event.Task != s.lease.Intent.Task || event.Attempt != s.lease.Intent.Attempt ||
		(event.Kind != ChoreographyTerminal && event.Kind != ChoreographyBlocked) {
		return nil
	}
	r := s.lease.Intent.NativeRecovery
	if r == nil || event.Kind == ChoreographyTerminal && event.Node != r.Node ||
		event.Kind == ChoreographyBlocked && event.Node != nodeSlotAbsentIdentityText {
		return fmt.Errorf(nodeSlotNativeFinalityNodeMismatchErrorText)
	}
	if s.finality != nil && !reflect.DeepEqual(*s.finality, event) {
		return fmt.Errorf(nodeSlotNativeFinalityConflictErrorText)
	}
	copy := event
	s.finality = &copy
	return nil
}

type nodeSlotRecoveryGroup struct {
	peer   []*recoveredNodeSlot
	native []*recoveredNativeNodeSlot
}

func (s *recoveredNodeSlot) apply(event ChoreographyEvent) error {
	r := s.lease.Intent.Recovery
	if r == nil || event.Task != s.lease.Intent.Task ||
		event.Attempt != s.lease.Intent.Attempt || event.Node != r.Node {
		return nil
	}
	switch event.Kind {
	case ChoreographyClaimAccepted:
		if event.ProposalDigest != r.ProposalDigest {
			return nil
		}
		if event.PriorTaskOID != r.expectedTaskOID() || event.TaskOID == nodeSlotAbsentIdentityText || s.refused ||
			(r.GraphRootOID != nodeSlotAbsentIdentityText &&
				(event.GraphRootOID != r.GraphRootOID || event.AssignmentOID == nodeSlotAbsentIdentityText)) ||
			(s.claimOID != nodeSlotAbsentIdentityText && s.claimOID != event.TaskOID) {
			return fmt.Errorf(nodeSlotClaimProposalMismatchErrorText)
		}
		s.claimOID = event.TaskOID
		s.assignmentOID = event.AssignmentOID
	case ChoreographyClaimRefused:
		if event.ProposalDigest != r.ProposalDigest {
			return nil
		}
		if s.claimOID != nodeSlotAbsentIdentityText || event.TaskOID != r.expectedTaskOID() ||
			(r.GraphRootOID != nodeSlotAbsentIdentityText && event.GraphRootOID != r.GraphRootOID) {
			return fmt.Errorf(nodeSlotRefusalClaimConflictErrorText)
		}
		s.refused = nodeSlotProposalRefusedState
	case ChoreographyResultAccepted, ChoreographyAckAccepted:
		if s.claimOID == nodeSlotAbsentIdentityText || event.TaskOID == nodeSlotAbsentIdentityText {
			return nil
		}
		if event.Kind == ChoreographyResultAccepted && event.PriorTaskOID != s.claimOID {
			return fmt.Errorf(nodeSlotResultClaimMismatchErrorText)
		}
		if s.effectOID != nodeSlotAbsentIdentityText && s.effectOID != event.TaskOID {
			return fmt.Errorf(nodeSlotAcceptedEffectsConflictErrorText)
		}
		s.effectOID = event.TaskOID
	case ChoreographyTerminal, ChoreographyRetryReady:
		if s.claimOID == nodeSlotAbsentIdentityText || s.effectOID == nodeSlotAbsentIdentityText || event.TaskOID == nodeSlotAbsentIdentityText {
			return nil
		}
		if event.ResultReceiptOID != s.effectOID {
			return fmt.Errorf(nodeSlotFinalityEffectMismatchErrorText)
		}
		s.terminalOID = event.TaskOID
		copy := event
		s.terminal = &copy
	}
	return nil
}

// RecoverHeldNodeSlots examines retained v3 leases before a restarted node
// accepts more work. It never infers settlement from a missing process, an
// absent task ref, a timeout, or one failed HTTP/Git read.
func (w *Worker) RecoverHeldNodeSlots(ctx context.Context) error {
	return w.recoverHeldNodeSlots(ctx, nodeSlotAbsentIdentityText)
}

// RecoverHeldNodeSlotsForRun reconciles only one protected prior run. A first
// node retiring its locator must not need another checkout's signing key just
// because the same physical node authority also retains that checkout's work.
func (w *Worker) RecoverHeldNodeSlotsForRun(ctx context.Context, run string) error {
	if run == nodeSlotAbsentIdentityText {
		return fmt.Errorf(nodeSlotRecoveryRunMissingErrorText)
	}
	return w.recoverHeldNodeSlots(ctx, run)
}

func (w *Worker) recoverHeldNodeSlots(ctx context.Context, onlyRun string) error {
	if w == nil || w.NodeSlots == nil {
		return nil
	}
	held, err := w.NodeSlots.Held(ctx)
	if err != nil {
		return err
	}
	groups := make(map[nodeSlotRunKey]*nodeSlotRecoveryGroup)
	for _, lease := range held {
		if onlyRun != nodeSlotAbsentIdentityText && lease.Intent.Run != onlyRun {
			continue
		}
		r := lease.Intent.Recovery
		n := lease.Intent.NativeRecovery
		if r == nil && n == nil {
			continue // no-links local completion has its own durable journal
		}
		var key nodeSlotRunKey
		if r != nil {
			if !validNodeSlotRecovery(lease.Intent) || r.Node != w.Node {
				return fmt.Errorf(nodeSlotRunEndpointMismatchErrorText)
			}
			key = nodeSlotRunKey{Binding: r.Binding, BootstrapOID: r.BootstrapOID,
				Endpoint: r.CanonicalEndpoint}
		} else {
			if !validNodeSlotNativeRecovery(lease.Intent) || n.Node != w.Node {
				return fmt.Errorf(nodeSlotNativeRunEndpointMismatchErrorText)
			}
			key = nodeSlotRunKey{Binding: n.Binding, BootstrapOID: n.BootstrapOID,
				Endpoint: n.CanonicalEndpoint}
		}
		if groups[key] == nil {
			groups[key] = &nodeSlotRecoveryGroup{}
		}
		if r != nil {
			groups[key].peer = append(groups[key].peer, &recoveredNodeSlot{lease: lease})
		} else {
			groups[key].native = append(groups[key].native, &recoveredNativeNodeSlot{lease: lease})
		}
	}
	if len(groups) == nodeSlotNoEntriesCount {
		return nil
	}
	mailbox, ok := w.Mailbox.(*GitMailbox)
	if !ok || w.Report.Limits.MaxManifestBytes <= nodeSlotNoObjectBytes || w.Report.Limits.MaxPackBytes <= nodeSlotNoObjectBytes {
		return fmt.Errorf(nodeSlotRetainedReaderRequiredErrorText)
	}
	if w.PrepareStore != nil {
		if err := w.PrepareStore(ctx); err != nil {
			return err
		}
	}
	for key, group := range groups {
		if err := gitx.RequireTransportEndpoint(key.Endpoint); err != nil {
			return fmt.Errorf(nodeSlotInvalidRetainedEndpointErrorFormat, err)
		}
		runMailbox := mailbox
		if mailbox.endpoint != key.Endpoint {
			// The same physical authority can retain work from distinct signed
			// runs and canonical mailboxes. Never substitute today's ambient
			// endpoint for the one bound by the old bootstrap.
			runMailbox = NewGitMailbox(key.Endpoint, mailbox.plumbing, mailbox.signer, w.Log)
		}
		feed, err := w.openRetainedNodeSlotFeed(ctx, runMailbox, key)
		if err != nil {
			return err
		}
		states := group.peer
		byTask := make(map[recoveredNodeSlotKey]*recoveredNodeSlot, len(states))
		for _, state := range states {
			r := state.lease.Intent.Recovery
			identity := recoveredNodeSlotKey{task: state.lease.Intent.Task,
				attempt: state.lease.Intent.Attempt, node: r.Node}
			if byTask[identity] != nil {
				return fmt.Errorf(nodeSlotDuplicateRetainedTaskErrorText)
			}
			byTask[identity] = state
			if _, err := runMailbox.ReadChoreographyTaskRoot(ctx, ChoreographyTaskRootQuery{
				Binding: r.Binding, Task: state.lease.Intent.Task,
				Kind: r.Kind, BootstrapOID: r.BootstrapOID,
			}); err != nil {
				return err
			}
			if r.ExpectedTaskOID != nodeSlotAbsentIdentityText {
				carrier, rootOID, err := runMailbox.ReadChoreographyGraphCarrier(ctx,
					r.Binding, r.BootstrapOID, w.Report.Limits.MaxManifestBytes)
				if err != nil {
					return fmt.Errorf(nodeSlotGraphRootUnavailableErrorFormat, err)
				}
				if rootOID != r.GraphRootOID {
					return fmt.Errorf(nodeSlotGraphRootMismatchErrorFormat, rootOID, r.GraphRootOID)
				}
				foundTemplate := nodeSlotTemplateNotFoundState
				for _, task := range carrier.Tasks {
					if task.Task == state.lease.Intent.Task && task.TemplateDigest == r.TemplateDigest {
						foundTemplate = nodeSlotTemplateFoundState
						break
					}
				}
				if !foundTemplate {
					return fmt.Errorf(nodeSlotTemplateGraphMismatchErrorText)
				}
				continue
			}
			view, err := runMailbox.OpenCarriedAssignment(ctx, r.AssignmentOID,
				w.Report.Limits.MaxManifestBytes)
			if err != nil {
				return fmt.Errorf(nodeSlotAssignmentUnavailableErrorFormat, err)
			}
			if view == nil {
				return fmt.Errorf(nodeSlotAssignmentMissingErrorText)
			}
			a := view.Assignment
			if a.Protocol != ChoreographyProtocolVersion || a.Kind != r.Kind ||
				a.Run != r.Binding.Run || a.Generation != r.Binding.Generation ||
				a.PlanDigest != r.Binding.PlanDigest || a.GraphDigest != r.Binding.GraphDigest ||
				a.Task != state.lease.Intent.Task || a.Attempt != state.lease.Intent.Attempt ||
				a.Node != r.Node || a.Branch != r.TaskBranch || a.BootstrapOID != r.BootstrapOID ||
				a.Weight != state.lease.Intent.Weight {
				return fmt.Errorf(nodeSlotAssignmentLocatorMismatchErrorText)
			}
		}
		nativeByTask := make(map[string]*recoveredNativeNodeSlot, len(group.native))
		for _, state := range group.native {
			n := state.lease.Intent.NativeRecovery
			if n.LedgerRootOID != feed.reader.opts.Ledger.OID ||
				nativeByTask[state.lease.Intent.Task] != nil {
				return fmt.Errorf(nodeSlotNativeLedgerTaskMismatchErrorText)
			}
			nativeByTask[state.lease.Intent.Task] = state
			if n.TaskBranch == nodeSlotAbsentIdentityText {
				continue
			}
			rootOID, err := runMailbox.ReadChoreographyTaskRoot(ctx, ChoreographyTaskRootQuery{
				Binding: n.Binding, Task: state.lease.Intent.Task,
				Kind: n.Kind, BootstrapOID: n.BootstrapOID})
			if err != nil {
				return fmt.Errorf(nodeSlotNativeTaskRootUnavailableErrorFormat, err)
			}
			if rootOID != n.ReservationOID {
				return fmt.Errorf(nodeSlotNativeTaskRootMismatchErrorFormat, rootOID, n.ReservationOID)
			}
		}
		cursor := ChoreographyEventCursor{}
		for {
			page, err := feed.ReadEvents(ctx, cursor, nodeSlotRecoveryPageEvents)
			if err != nil {
				return err
			}
			if page.Cursor.OID == nodeSlotAbsentIdentityText || page.Cursor.Sequence != cursor.Sequence+uint64(len(page.Events)) ||
				(len(page.Events) == nodeSlotNoEntriesCount && cursor.OID != nodeSlotAbsentIdentityText && page.Cursor.OID != cursor.OID) {
				return fmt.Errorf(nodeSlotLedgerCursorGapErrorText)
			}
			for _, event := range page.Events {
				if state := byTask[recoveredNodeSlotKey{task: event.Task,
					attempt: event.Attempt, node: event.Node}]; state != nil {
					if err := state.apply(event); err != nil {
						return err
					}
				}
				if state := nativeByTask[event.Task]; state != nil {
					if err := state.apply(event); err != nil {
						return err
					}
				}
			}
			cursor = page.Cursor
			if !page.More {
				break
			}
		}
		for _, state := range states {
			r := state.lease.Intent.Recovery
			if r.ExpectedTaskOID != nodeSlotAbsentIdentityText && state.claimOID != nodeSlotAbsentIdentityText {
				view, err := runMailbox.OpenCarriedAssignment(ctx, state.assignmentOID,
					w.Report.Limits.MaxManifestBytes)
				if err != nil {
					return fmt.Errorf(nodeSlotCompiledAssignmentUnavailableErrorFormat, err)
				}
				if view == nil || view.Assignment.TemplateDigest != r.TemplateDigest ||
					view.Assignment.Task != state.lease.Intent.Task || view.Assignment.Node != r.Node ||
					view.Assignment.Branch != r.TaskBranch || view.Assignment.BootstrapOID != r.BootstrapOID {
					return fmt.Errorf(nodeSlotCompiledAssignmentMismatchErrorText)
				}
			}
			priorSequence, found, err := feed.SequenceForOID(ctx, r.ExpectedLedgerOID)
			if err != nil {
				return fmt.Errorf(nodeSlotSignedEpochUnavailableErrorFormat, err)
			}
			if !found {
				return fmt.Errorf(nodeSlotEpochOutsideRunErrorText)
			}
			evidence := state.settlementEvidence(priorSequence, cursor)
			if evidence == nodeSlotAbsentIdentityText {
				continue
			}
			if state.terminal != nil {
				assignmentOID := r.AssignmentOID
				if r.ExpectedTaskOID != nodeSlotAbsentIdentityText {
					assignmentOID = state.assignmentOID
				}
				if err := feed.VerifyAttemptFinality(ctx, ChoreographyAttemptFinalityProof{
					Event: *state.terminal, ReceiptOID: state.effectOID,
					ClaimOID: state.claimOID, AssignmentOID: assignmentOID}); err != nil {
					return fmt.Errorf(nodeSlotExactDecisionMissingErrorFormat, err)
				}
			}
			if err := w.NodeSlots.ReleaseSettled(ctx, state.lease.ID, evidence); err != nil {
				return err
			}
		}
		for _, state := range group.native {
			if state.finality == nil {
				continue // an unproved preclaim hook or child process may survive
			}
			if err := feed.VerifyNativeTaskFinality(ctx, state.lease.Intent.Task,
				*state.lease.Intent.NativeRecovery, *state.finality); err != nil {
				return fmt.Errorf(nodeSlotNativeFinalityMissingErrorFormat, err)
			}
			if err := w.NodeSlots.ReleaseSettled(ctx, state.lease.ID,
				state.finality.TaskOID); err != nil {
				return err
			}
		}
	}
	return nil
}

type nodeSlotRunKey struct {
	Binding      ChoreographyRunBinding
	BootstrapOID string
	Endpoint     string
}

func (w *Worker) openRetainedNodeSlotFeed(ctx context.Context, mailbox *GitMailbox,
	key nodeSlotRunKey) (*RetainedChoreographyFeed, error) {
	if mailbox == nil || mailbox.endpoint != key.Endpoint {
		return nil, fmt.Errorf(nodeSlotRetainedEndpointMismatchErrorText)
	}
	return OpenRetainedChoreographyFeed(ctx, RetainedChoreographyFeedOptions{
		Mailbox: mailbox, Binding: key.Binding, BootstrapOID: key.BootstrapOID,
		MaxDocumentBytes: w.Report.Limits.MaxManifestBytes,
		MaxTagBytes:      w.Report.Limits.MaxPackBytes,
	})
}
