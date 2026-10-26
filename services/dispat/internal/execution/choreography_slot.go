// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

const choreographyParticipationSlotIndex = 0
const choreographyActivatedSlotSequence = 1

type ChoreographySlotRootOptions struct {
	Mailbox      *GitMailbox
	Binding      ChoreographyRunBinding
	BootstrapOID string
	Node         string
	Index        int
}

// PrepareChoreographySlotRoot writes one inert, bootstrap-bound dispatch
// index. The first node includes this ref in its initial create batches;
// later offers reuse it, so task count never creates per-task notice refs.
func PrepareChoreographySlotRoot(ctx context.Context,
	opts ChoreographySlotRootOptions) (dispatchSlot, error) {
	if opts.Mailbox == nil || !opts.Binding.valid() || opts.BootstrapOID == "" ||
		opts.Node == "" || opts.Index < 0 {
		return dispatchSlot{}, fmt.Errorf("execution: invalid choreography slot root")
	}
	branch := FormatSlotBranch(opts.Node, opts.Binding.Run, opts.Index)
	notice := SlotNotice{Header: Header{
		Protocol: ChoreographyProtocolVersion, Kind: KindSlot,
		Run: opts.Binding.Run, Generation: opts.Binding.Generation,
		PlanDigest: opts.Binding.PlanDigest, Node: opts.Node, Branch: branch,
		IssuedAt: time.Now().UTC().Format(time.RFC3339),
	}, BootstrapOID: opts.BootstrapOID}
	document, err := json.Marshal(notice)
	if err != nil {
		return dispatchSlot{}, err
	}
	if len(document) > returnReplyLimit {
		return dispatchSlot{}, fmt.Errorf("execution: signed slot root exceeds transfer limit")
	}
	oid, err := opts.Mailbox.commitMessage(ctx, MessageSlot, document, []string{opts.BootstrapOID}, nil)
	if err != nil {
		return dispatchSlot{}, err
	}
	return dispatchSlot{branch: branch, oid: oid}, nil
}

// ActivateChoreographyGraphNotices makes the complete graph discoverable to
// preauthorized nodes only after every task root and the signed graph root
// have settled at exact remote OIDs. No task is claimable until this succeeds.
func (c *Coordinator) ActivateChoreographyGraphNotices(ctx context.Context, rootOID string) error {
	if c == nil || c.canonicalMailbox == nil || rootOID == "" ||
		c.GraphRootOID != rootOID || c.choreographySlots == nil {
		return fmt.Errorf("execution: participation notices lack a complete graph root")
	}
	state := c.choreographySlots
	var advances []gitx.BranchAdvance
	byBranch := make(map[string]*dispatchSlot)
	for _, authorized := range c.JoinPolicy.Authorized {
		slots := state.free[authorized.Name]
		if len(slots) != 1 || slots[0].oid == "" {
			return fmt.Errorf("execution: participation notice for %q has no inert root", authorized.Name)
		}
		slot := slots[0]
		notice := &SlotNotice{Header: Header{
			Protocol: ChoreographyProtocolVersion, Kind: KindSlot,
			Run: c.Run, Generation: c.Generation, PlanDigest: c.PlanDigest,
			Node: authorized.Name, Branch: slot.branch,
			IssuedAt: time.Now().UTC().Format(time.RFC3339),
		}, BootstrapOID: c.BootstrapOID, GraphRootOID: rootOID,
			Sequence: choreographyActivatedSlotSequence}
		newOID, err := c.canonicalMailbox.PrepareSlotNotice(ctx, notice, slot.oid)
		if err != nil {
			return err
		}
		advances = append(advances, gitx.BranchAdvance{
			Branch: slot.branch, ExpectedOld: slot.oid, OID: newOID})
		byBranch[slot.branch] = slot
	}
	slices.SortFunc(advances, func(a, b gitx.BranchAdvance) int { return cmp.Compare(a.Branch, b.Branch) })
	for start := 0; start < len(advances); start += gitx.MaxTransportBatch {
		stop := min(start+gitx.MaxTransportBatch, len(advances))
		batch := advances[start:stop]
		pushErr := c.canonicalMailbox.remote.PushAtomicAdvances(ctx, c.CanonicalEndpoint, batch)
		for _, advance := range batch {
			actual, err := c.canonicalMailbox.readRemoteTip(ctx, advance.Branch)
			if err != nil {
				return err
			}
			if actual != advance.OID {
				if pushErr != nil {
					return fmt.Errorf("execution: participation notice %q is not settled at its complete graph root: %w", advance.Branch, pushErr)
				}
				return fmt.Errorf("execution: participation notice %q is not settled at its complete graph root", advance.Branch)
			}
			slot := byBranch[advance.Branch]
			slot.oid, slot.sequence = advance.OID, choreographyActivatedSlotSequence
			c.ChoreographyRefAdvanced(ctx, ChoreographyRefAdvance{
				Branch: advance.Branch, OldOID: advance.ExpectedOld, NewOID: advance.OID})
		}
	}
	return nil
}
