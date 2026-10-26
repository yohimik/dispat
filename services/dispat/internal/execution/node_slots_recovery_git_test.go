//go:build linux || darwin

// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"errors"
	"os"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func TestColdNodeSlotReleasesOnlyAfterSignedPreclaimEpochFence(t *testing.T) {
	for _, mode := range []string{"preclaim-fence", "accepted-result-finality", "https-loss-fallback"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			root := t.TempDir()
			require.NoError(t, os.Chmod(root, 0o700))
			authority, err := OpenNodeSlotAuthority(root, "node-a", 1)
			require.NoError(t, err)
			task := "pkg:build"
			canonical, persist, binding, spec := newChoreographyLedgerFixtureWithAuthority(t,
				authority.AuthorityID(), ChoreographyTaskSpec{Task: task, Kind: KindBuild,
					Placement: PlacementOrchestrator, EligibleNodes: []string{"node-a"}})
			peer := canonical.second(t)
			brokerOptions := choreographyFixtureBrokerOptions(persist)
			brokerOptions.Members[0].AuthorityID = authority.AuthorityID()
			broker, err := RecoverFirstNodeBroker(ctx, brokerOptions)
			require.NoError(t, err)
			bootstrapOID := persist.opts.BootstrapOID
			head, err := persist.LedgerHead(ctx)
			require.NoError(t, err)
			proposal := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
				Task: task, Attempt: 1, Node: "node-a", Branch: spec.Branch,
				ExpectedOID: spec.ReservationOID, ExpectedLedgerOID: head.OID,
				InputsDigest: spec.InputsDigest, PickupID: NewChoreographyPickupID(), ProposalID: "lost-preclaim"}
			completeChoreographyFixtureClaim(t, persist, &proposal)
			query, err := FormatClaimReceiptQuery(proposal)
			require.NoError(t, err)
			lease, acquired, err := authority.TryReserve(ctx, NodeSlotIntent{Run: binding.Run,
				Task: task, Attempt: 1, PickupID: proposal.PickupID, Weight: 1, Recovery: &NodeSlotRecoveryLocator{
					Binding: binding, CanonicalEndpoint: canonical.endpoint, BootstrapOID: bootstrapOID,
					TaskBranch: spec.Branch, Kind: KindBuild,
					ExpectedTaskOID: spec.ReservationOID, GraphRootOID: persist.opts.GraphRootOID,
					TemplateDigest:    spec.TemplateDigest,
					ExpectedLedgerOID: head.OID, ProposalDigest: query.ProposalDigest, Node: "node-a"}})
			require.NoError(t, err)
			require.True(t, acquired)
			w := &Worker{Node: "node-a", Endpoint: canonical.endpoint, Mailbox: peer.mailbox,
				NodeSlots: authority, Report: NodeReport{Limits: TransferLimits{
					MaxManifestBytes: 1 << 20, MaxPackBytes: 1 << 20}}, Log: zerolog.Nop()}
			require.NoError(t, w.RecoverHeldNodeSlots(ctx))
			held, err := authority.Held(ctx)
			require.NoError(t, err)
			require.Len(t, held, 1, "the unchanged signed epoch cannot settle a lost claim")
			if mode == "preclaim-fence" {
				_, err = broker.StopClaims(ctx)
				require.NoError(t, err)
			} else {
				claim, err := broker.AcceptClaim(ctx, proposal)
				require.NoError(t, err)
				require.True(t, claim.Accepted)
				view, err := canonical.mailbox.OpenCarriedAssignment(ctx, claim.AssignmentOID, 1<<20)
				require.NoError(t, err)
				assignment := &view.Assignment
				require.NoError(t, w.RecoverHeldNodeSlots(ctx))
				held, err = authority.Held(ctx)
				require.NoError(t, err)
				require.Len(t, held, 1, "an accepted claim still owns physical capacity")
				result := Result{Header: assignment.Header, Assignment: claim.OID, Status: StatusSucceeded}
				_, err = peer.mailbox.Reread(ctx, spec.Branch)
				require.NoError(t, err)
				prepared, pack := prepareChoreographyFixturePickupResult(t, peer,
					choreographyFixturePickupResult{Claim: proposal, Accepted: claim,
						Result: result, ProposalID: "lost-result"})
				resultReceipt, err := broker.AcceptResult(ctx, prepared, pack)
				require.NoError(t, err)
				require.True(t, resultReceipt.Accepted)
				require.NoError(t, w.RecoverHeldNodeSlots(ctx))
				held, err = authority.Held(ctx)
				require.NoError(t, err)
				require.Len(t, held, 1, "a result receipt without a signed decision holds capacity")
				require.NoError(t, broker.DecideAttempt(ctx, ChoreographyDecision{
					ChoreographyRunBinding: binding, Task: task, Branch: spec.Branch, Attempt: 1,
					Node: "node-a", ResultOID: resultReceipt.OID, Succeeded: true}))
				if mode == "https-loss-fallback" {
					polls := 0
					channel := &workerReturnChannel{binding: binding, client: &fakeWorkerReturn{
						poll: func(ChoreographyEventRequest) (ChoreographyEventPage, error) {
							polls++
							return ChoreographyEventPage{}, errors.New("first-node HTTPS listener closed")
						},
					}}
					claimed := &claimedTask{assignment: *assignment,
						tip:     ChainTip{OID: claim.AssignmentOID, Branch: spec.Branch},
						claimed: claim.OID, slotLease: lease}
					require.NoError(t, w.awaitNodeSlotDecision(ctx, claimed, channel, resultReceipt))
					require.Equal(t, 1, polls, "retained Git replay settles after one failed HTTPS read")
				}
			}
			reopened, err := OpenNodeSlotAuthority(root, "node-a", 1)
			require.NoError(t, err)
			w.NodeSlots = reopened
			require.NoError(t, w.RecoverHeldNodeSlots(ctx))
			held, err = reopened.Held(ctx)
			require.NoError(t, err)
			require.Empty(t, held, "a newer signed epoch and no accepted exact proposal fence the preclaim")
			_, acquired, err = reopened.TryReserve(ctx, NodeSlotIntent{Run: "later", Task: "other", Attempt: 1, Weight: 1})
			require.NoError(t, err)
			require.True(t, acquired)
		})
	}
}
