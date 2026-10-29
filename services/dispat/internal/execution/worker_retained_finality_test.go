// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

const (
	workerRetainedFinalityNode          = "node-a"
	workerRetainedFinalityTask          = "web:run"
	workerRetainedFinalityClaim         = "retained-finality-claim"
	workerRetainedFinalityCapacity      = 1
	workerRetainedFinalityFirstAttempt  = 1
	workerRetainedFinalityDocumentLimit = 1 << 20
	workerRetainedFinalityDirectoryMode = 0o700
)

var errWorkerRetainedFinalityHTTP = errors.New("finality return endpoint is unavailable")
var errWorkerRetainedFinalityGit = errors.New("retained finality Git read is unavailable")

type workerRetainedFinalityFixture struct {
	worker  *Worker
	task    *claimedTask
	channel *workerReturnChannel
	receipt ChoreographyReceipt
	mailbox *GitMailbox
	polls   int
}

func newWorkerRetainedFinalityFixture(t *testing.T, settled bool) *workerRetainedFinalityFixture {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.Chmod(root, workerRetainedFinalityDirectoryMode))
	authority, err := OpenNodeSlotAuthority(root, workerRetainedFinalityNode, workerRetainedFinalityCapacity)
	require.NoError(t, err)
	canonical, persist, binding, spec := newChoreographyLedgerFixtureWithAuthority(t,
		authority.AuthorityID(), ChoreographyTaskSpec{Task: workerRetainedFinalityTask, Kind: KindRun})
	options := choreographyFixtureBrokerOptions(persist)
	options.Members[0].AuthorityID = authority.AuthorityID()
	broker, err := RecoverFirstNodeBroker(t.Context(), options)
	require.NoError(t, err)
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: workerRetainedFinalityFirstAttempt, Node: workerRetainedFinalityNode,
		Branch: spec.Branch, ExpectedOID: spec.ReservationOID, InputsDigest: spec.InputsDigest,
		PickupID: NewChoreographyPickupID(), ProposalID: workerRetainedFinalityClaim}
	completeChoreographyFixtureClaim(t, persist, &claim)
	peer := canonical.second(t)
	fixture := &workerRetainedFinalityFixture{mailbox: peer.mailbox}
	fixture.worker = &Worker{Node: workerRetainedFinalityNode, Endpoint: peer.endpoint,
		Mailbox: peer.mailbox, Cache: peer.git, NodeSlots: authority, Log: zerolog.Nop(),
		Report: NodeReport{AuthorityID: authority.AuthorityID(), Limits: TransferLimits{
			MaxManifestBytes: workerRetainedFinalityDocumentLimit, MaxPackBytes: workerRetainedFinalityDocumentLimit}}}
	fixture.channel = &workerReturnChannel{binding: binding, client: &fakeWorkerReturn{
		pollWithContext: func(ctx context.Context, request ChoreographyEventRequest) (ChoreographyEventPage, error) {
			fixture.polls++
			require.Equal(t, fixture.receipt.Cursor, request.Cursor)
			require.Equal(t, binding, request.ChoreographyRunBinding)
			if fixture.polls == workerRetainedFinalityFirstAttempt {
				require.ErrorIs(t, ctx.Err(), context.Canceled)
			} else {
				require.NoError(t, ctx.Err(), "the finality probe detaches from serving cancellation")
				_, bounded := ctx.Deadline()
				require.True(t, bounded, "the independent finality probe must remain bounded")
			}
			return ChoreographyEventPage{}, errWorkerRetainedFinalityHTTP
		}}}
	assignment := Assignment{Header: Header{Protocol: ChoreographyProtocolVersion,
		Run: binding.Run, Generation: binding.Generation, PlanDigest: binding.PlanDigest,
		Task: spec.Task, Attempt: claim.Attempt, Node: claim.Node, Branch: spec.Branch, Kind: spec.Kind},
		GraphDigest: binding.GraphDigest, GraphRootOID: persist.opts.GraphRootOID,
		BootstrapOID: persist.opts.BootstrapOID, Weight: spec.Weight}
	lease, available, err := fixture.worker.reserveNodeSlot(t.Context(), assignment, fixture.channel, claim)
	require.NoError(t, err)
	require.True(t, available)
	accepted, err := broker.AcceptClaim(t.Context(), claim)
	require.NoError(t, err)
	require.True(t, accepted.Accepted)
	view, err := canonical.mailbox.OpenCarriedAssignment(t.Context(), accepted.AssignmentOID,
		workerRetainedFinalityDocumentLimit)
	require.NoError(t, err)
	fixture.task = &claimedTask{assignment: view.Assignment,
		tip:     ChainTip{Branch: spec.Branch, OID: accepted.AssignmentOID},
		claimed: accepted.OID, slotLease: lease}
	fixture.receipt = choreographyContactAcceptPickupResult(t, canonical, broker, binding, spec, claim, accepted)
	if settled {
		require.NoError(t, broker.DecideAttempt(t.Context(), ChoreographyDecision{
			ChoreographyRunBinding: binding, Task: spec.Task, Branch: spec.Branch,
			Attempt: claim.Attempt, Node: claim.Node, ResultOID: fixture.receipt.OID, Succeeded: true}))
	}
	_, err = broker.StopClaims(t.Context())
	require.NoError(t, err)
	if settled {
		_, err = persist.SealRun(t.Context(), choreographyCloseNormalSeal)
		require.NoError(t, err)
	}
	return fixture
}

type workerRetainedFinalityReadFailure struct {
	transportx
	err error
}

func (f *workerRetainedFinalityReadFailure) ListRemoteHeads(context.Context,
	string, string) ([]gitx.RemoteHead, error) {
	return nil, f.err
}

func TestWorkerStopUsesRetainedGitFinalityWhenReturnEndpointIsUnavailable(t *testing.T) {
	for _, scenario := range []string{"exact finality", "missing finality", "wrong receipt", "wrong claim",
		"wrong cursor", "wrong bootstrap", "retained read fails", "retained read cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newWorkerRetainedFinalityFixture(t, scenario != "missing finality")
			finalityCtx, stop := context.WithCancel(t.Context())
			stop()
			fixture.task.finalityCtx = finalityCtx
			switch scenario {
			case "wrong receipt":
				fixture.receipt.OID = fixture.task.claimed
			case "wrong claim":
				fixture.task.claimed = fixture.task.tip.OID
			case "wrong cursor":
				fixture.receipt.Cursor.OID = fixture.task.claimed
			case "wrong bootstrap":
				fixture.task.slotLease.Intent.Recovery.BootstrapOID = fixture.task.claimed
			case "retained read fails":
				fixture.mailbox.remote = &workerRetainedFinalityReadFailure{transportx: fixture.mailbox.remote,
					err: errWorkerRetainedFinalityGit}
			case "retained read cancelled":
				fixture.mailbox.remote = &workerRetainedFinalityReadFailure{transportx: fixture.mailbox.remote,
					err: errors.Join(errWorkerRetainedFinalityGit, context.Canceled)}
			}
			before, readErr := fixture.worker.NodeSlots.Held(t.Context())
			require.NoError(t, readErr)
			err := fixture.worker.awaitNodeSlotDecision(t.Context(), fixture.task, fixture.channel, fixture.receipt)
			require.Equal(t, 2, fixture.polls, "the detached stop probe must attempt the unavailable endpoint before authenticated Git recovery")
			held, readErr := fixture.worker.NodeSlots.Held(t.Context())
			require.NoError(t, readErr)
			if scenario == "exact finality" {
				require.NoError(t, err)
				require.Empty(t, held)
				require.True(t, fixture.task.slotSettled)
				return
			}
			require.Error(t, err)
			require.Equal(t, before, held)
			require.False(t, fixture.task.slotSettled)
			reopened, reopenErr := OpenNodeSlotAuthority(filepath.Dir(fixture.worker.NodeSlots.dir),
				workerRetainedFinalityNode, workerRetainedFinalityCapacity)
			require.NoError(t, reopenErr)
			reopenedHeld, readErr := reopened.Held(t.Context())
			require.NoError(t, readErr)
			require.Equal(t, before, reopenedHeld, "failed recovery retains the exact durable lease across reopen")
			if scenario == "retained read fails" || scenario == "retained read cancelled" {
				require.ErrorIs(t, err, errWorkerRetainedFinalityHTTP)
				require.ErrorIs(t, err, errWorkerRetainedFinalityGit)
				require.ErrorContains(t, err, "opening retained capacity finality")
				if scenario == "retained read cancelled" {
					require.ErrorIs(t, err, context.Canceled)
				}
			}
		})
	}
}
