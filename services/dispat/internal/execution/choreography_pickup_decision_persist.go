// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

const (
	choreographyPickupDecisionNoIdentity   = ""
	choreographyPickupDecisionTipErrorText = "execution: pickup decision has no exact canonical task ref"
)

type choreographyPickupDecisionOptions struct {
	Decision      ChoreographyDecision
	Document      []byte
	Identity      string
	Spec          ChoreographyTaskSpec
	OutputOID     string
	OutputEntries []gitx.TreeEntry
	Proposal      ChoreographyResultProposal
}

// persistPickupDecision advances the canonical task branch from its current
// signed tip, which can be a successor Claim. Its secondary parent names the
// winning or exhausted pickup's verified receipt without making that older
// pickup the first-parent ancestor of newer accepted work.
func (g *GitChoreographyPersist) persistPickupDecision(ctx context.Context,
	options choreographyPickupDecisionOptions) (string, error) {
	d, m := options.Decision, g.opts.Mailbox
	if options.Proposal.ChoreographyRunBinding != d.ChoreographyRunBinding ||
		options.Proposal.Task != d.Task || options.Proposal.Branch != d.Branch ||
		options.Proposal.Attempt != d.Attempt || options.Proposal.Node != d.Node ||
		options.Proposal.PickupID == choreographyPickupDecisionNoIdentity {
		return choreographyPickupDecisionNoIdentity, fmt.Errorf(choreographyGitInvalidTaskDecisionError)
	}
	taskTip, err := m.readRemoteTip(ctx, d.Branch)
	if err != nil {
		return choreographyPickupDecisionNoIdentity, err
	}
	if taskTip == choreographyPickupDecisionNoIdentity {
		return choreographyPickupDecisionNoIdentity, fmt.Errorf(choreographyPickupDecisionTipErrorText)
	}
	oid := g.pendingTaskOID(options.Identity)
	if oid == choreographyPickupDecisionNoIdentity {
		oid, err = m.commitMessage(ctx, choreographyDecisionKind, options.Document,
			[]string{taskTip, d.ResultOID}, options.OutputEntries)
		if err != nil {
			return choreographyPickupDecisionNoIdentity, err
		}
	}
	kind := ChoreographyTerminal
	if d.Retry {
		kind = ChoreographyRetryReady
	}
	if err := g.persistAtomic(ctx, choreographyLedgerMutation{
		Identity: options.Identity, TaskBranch: d.Branch,
		OldTaskOID: taskTip, NewTaskOID: oid,
		Budget: options.Spec.Budget, Node: d.Node, Delta: -options.Spec.Weight,
		Event: ChoreographyEvent{Kind: kind, Task: d.Task, Attempt: d.Attempt,
			Node: d.Node, TaskOID: oid, PriorTaskOID: taskTip,
			ResultReceiptOID: d.ResultOID, OutputOID: options.OutputOID,
			PickupID: options.Proposal.PickupID, ClaimOID: options.Proposal.ClaimOID,
			GraphRootOID: g.opts.GraphRootOID,
			Budget:       options.Spec.Budget, Weight: options.Spec.Weight,
			Succeeded: d.Succeeded, Package: options.Spec.Package,
			PackageOutcome: d.PackageOutcome},
	}); err != nil {
		return choreographyPickupDecisionNoIdentity, err
	}
	return oid, nil
}
