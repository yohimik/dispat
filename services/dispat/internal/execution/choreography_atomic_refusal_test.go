// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

const choreographyRefusalHookFileMode = 0o755

type choreographyAtomicUnknownTransport struct {
	transportx
	apply bool
	calls int
}

func (t *choreographyAtomicUnknownTransport) PushAtomicAdvances(ctx context.Context, endpoint string,
	refs []gitx.BranchAdvance) error {
	t.calls++
	if t.calls == 1 {
		if t.apply {
			if err := t.transportx.PushAtomicAdvances(ctx, endpoint, refs); err != nil {
				return err
			}
		}
		return gitx.ErrPushUnknown
	}
	return t.transportx.PushAtomicAdvances(ctx, endpoint, refs)
}

// Git's pre-receive hook refuses the atomic task/ledger Claim CAS. The first
// node retries only after both exact remote tips remain unchanged.
func TestChoreographyAtomicClaimRefusalRetainsExactAttempt(t *testing.T) {
	for _, tc := range []struct {
		name         string
		refuseCount  int
		firstRefused bool
	}{
		{name: "one refusal then accepted", refuseCount: 1},
		{name: "repeated refusal remains inert", refuseCount: 2, firstRefused: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture, persist, binding, spec := newChoreographyLedgerFixture(t)
			broker, err := RecoverFirstNodeBroker(t.Context(), choreographyFixtureBrokerOptions(persist))
			require.NoError(t, err)
			claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
				Task: spec.Task, Attempt: 1, Node: "node-a", Branch: spec.Branch,
				ExpectedOID: spec.ReservationOID, InputsDigest: spec.InputsDigest,
				ProposalID: "refused-atomic-claim"}
			completeChoreographyFixtureClaim(t, persist, &claim)
			countFile := filepath.Join(fixture.endpoint, "hooks", "refused-count")
			hook := "#!/bin/sh\n" +
				"count=$(cat \"$(dirname \"$0\")/refused-count\" 2>/dev/null || printf 0)\n" +
				"count=$((count + 1))\n" +
				"printf '%s\\n' \"$count\" > \"$(dirname \"$0\")/refused-count\"\n" +
				"if [ \"$count\" -le " + strconv.Itoa(tc.refuseCount) + " ]; then\n" +
				"  printf '%s\\n' 'pre-receive hook declined this atomic claim' >&2\n" +
				"  exit 1\n" +
				"fi\n"
			hookPath := filepath.Join(fixture.endpoint, "hooks", "pre-receive")
			require.NoError(t, os.WriteFile(hookPath, []byte(hook), choreographyRefusalHookFileMode))
			t.Cleanup(func() { _ = os.Remove(hookPath) })

			receipt, err := broker.AcceptClaim(t.Context(), claim)
			count, readErr := os.ReadFile(countFile)
			require.NoError(t, readErr)
			require.Equal(t, "2", strings.TrimSpace(string(count)))
			if tc.firstRefused {
				require.ErrorIs(t, err, errChoreographyAtomicRefused)
				require.False(t, receipt.Accepted)
				require.Nil(t, persist.pending)
				require.Equal(t, spec.ReservationOID, remoteChoreographyOID(t, fixture, spec.Branch))
				require.Equal(t, claim.ExpectedLedgerOID, remoteChoreographyOID(t, fixture, persist.opts.Ledger.Branch))
				require.Empty(t, broker.tasks[spec.Task].claim)
				require.NoError(t, os.Remove(hookPath))
				receipt, err = broker.AcceptClaim(t.Context(), claim)
			}
			require.NoError(t, err)
			require.True(t, receipt.Accepted)
			require.NotEmpty(t, receipt.AssignmentOID)
			require.Equal(t, 1, broker.tasks[spec.Task].attempt)
			require.Equal(t, receipt.OID, remoteChoreographyOID(t, fixture, spec.Branch))
			require.Equal(t, receipt.Cursor.OID, remoteChoreographyOID(t, fixture, persist.opts.Ledger.Branch))
			page, err := persist.ReadEvents(t.Context(), choreographyFixtureStartCursor(t, persist), 2)
			require.NoError(t, err)
			require.Len(t, page.Events, 1)
			require.Equal(t, ChoreographyClaimAccepted, page.Events[0].Kind)
		})
	}
}

// An unknown direct Claim push with unchanged tips remains pending; only the
// identical exact proposal can reconcile its task and ledger CAS.
func TestChoreographyUnknownAtomicClaimStaysFenced(t *testing.T) {
	for _, applied := range []bool{false, true} {
		name := "never applied"
		if applied {
			name = "applied with lost reply"
		}
		t.Run(name, func(t *testing.T) {
			fixture, persist, binding, spec := newChoreographyLedgerFixture(t)
			broker, err := RecoverFirstNodeBroker(t.Context(), choreographyFixtureBrokerOptions(persist))
			require.NoError(t, err)
			claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
				Task: spec.Task, Attempt: 1, Node: "node-a", Branch: spec.Branch,
				ExpectedOID: spec.ReservationOID, InputsDigest: spec.InputsDigest,
				ProposalID: "unknown-atomic-claim"}
			completeChoreographyFixtureClaim(t, persist, &claim)
			transport := &choreographyAtomicUnknownTransport{transportx: fixture.mailbox.remote, apply: applied}
			fixture.mailbox.remote = transport
			first, err := broker.AcceptClaim(t.Context(), claim)
			if applied {
				require.NoError(t, err)
				require.True(t, first.Accepted)
				require.Nil(t, persist.pending)
				require.Equal(t, first.OID, remoteChoreographyOID(t, fixture, spec.Branch))
				return
			}
			require.ErrorIs(t, err, gitx.ErrPushUnknown)
			require.NotNil(t, persist.pending)
			require.Empty(t, broker.tasks[spec.Task].claim)
			require.Equal(t, spec.ReservationOID, remoteChoreographyOID(t, fixture, spec.Branch))
			require.Equal(t, claim.ExpectedLedgerOID, remoteChoreographyOID(t, fixture, persist.opts.Ledger.Branch))
			for name, alter := range map[string]func(*ChoreographyClaimProposal){
				"membership epoch":   func(p *ChoreographyClaimProposal) { p.MembershipEpochOID = spec.ReservationOID },
				"capacity authority": func(p *ChoreographyClaimProposal) { p.CapacityAuthorityID = "abcdef0123456789abcdef0123456789" },
				"physical lease":     func(p *ChoreographyClaimProposal) { p.LeaseIntentID = "different-held-lease" },
				"physical weight":    func(p *ChoreographyClaimProposal) { p.LeaseWeight++ },
				"node":               func(p *ChoreographyClaimProposal) { p.Node = "other-node" },
			} {
				t.Run(name, func(t *testing.T) {
					altered := claim
					alter(&altered)
					_, err := persist.PersistClaim(t.Context(), altered)
					require.ErrorContains(t, err, "unresolved task and capacity CAS blocks another transition")
					require.NotNil(t, persist.pending)
					require.Equal(t, 1, transport.calls)
				})
			}
			receipt, err := broker.AcceptClaim(t.Context(), claim)
			require.NoError(t, err)
			require.True(t, receipt.Accepted)
			require.Equal(t, 2, transport.calls)
			require.Nil(t, persist.pending)
			require.Equal(t, receipt.OID, remoteChoreographyOID(t, fixture, spec.Branch))
			require.Equal(t, receipt.Cursor.OID, remoteChoreographyOID(t, fixture, persist.opts.Ledger.Branch))
		})
	}
}
