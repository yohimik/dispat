// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChoreographyLocalEffectBindsSignedTaskAndNativeReceipt(t *testing.T) {
	receipt := strings.Repeat("a", 40)
	commit := strings.Repeat("b", 40)
	evidence := ChoreographyLocalEffectEvidence{Operation: "publish", Package: "api",
		Version: "1.2.3", Tag: "api-v1.2.3", Attempt: 1, PlanDigest: "plan",
		RecordingMode: "commit-push", Observation: "succeeded",
		ReceiptObject: receipt, ReceiptCommit: commit}
	digest, err := DigestChoreographyLocalEffect(evidence)
	require.NoError(t, err)
	outcome := ChoreographyLocalOutcome{ChoreographyRunBinding: ChoreographyRunBinding{PlanDigest: "plan"},
		Task: "api:publish", EffectDigest: digest, EffectEvidence: evidence, Succeeded: true}
	spec := ChoreographyTaskSpec{Task: outcome.Task, Package: "api", Kind: "publish", LocalAuthority: true}
	require.NoError(t, validateChoreographyLocalEffect(outcome, spec))

	tampered := outcome
	tampered.EffectEvidence.ReceiptObject = strings.Repeat("c", 40)
	require.Error(t, validateChoreographyLocalEffect(tampered, spec))
	tampered = outcome
	tampered.EffectEvidence.ReceiptObject, tampered.EffectEvidence.ReceiptCommit = "", ""
	tampered.EffectDigest, err = DigestChoreographyLocalEffect(tampered.EffectEvidence)
	require.NoError(t, err)
	require.ErrorContains(t, validateChoreographyLocalEffect(tampered, spec), "no exact native record")
	tampered.PackageOutcome = "record-blocked"
	tampered.Succeeded = false
	tampered.EffectEvidence.Observation = "failed"
	tampered.EffectDigest, err = DigestChoreographyLocalEffect(tampered.EffectEvidence)
	require.NoError(t, err)
	require.NoError(t, validateChoreographyLocalEffect(tampered, spec),
		"a reported required-record failure does not invent a missing receipt")
	tampered.PackageOutcome = ""
	tampered.Succeeded = true
	tampered.EffectEvidence.Observation = "succeeded"
	tampered.EffectEvidence.RecordingMode = "command-only"
	tampered.EffectDigest, err = DigestChoreographyLocalEffect(tampered.EffectEvidence)
	require.NoError(t, err)
	require.NoError(t, validateChoreographyLocalEffect(tampered, spec),
		"the publish command can finish before a separate record vertex")
	record := spec
	record.Kind = "record"
	tampered.EffectEvidence.Operation = "record"
	tampered.EffectDigest, err = DigestChoreographyLocalEffect(tampered.EffectEvidence)
	require.NoError(t, err)
	require.Error(t, validateChoreographyLocalEffect(tampered, record),
		"a record vertex cannot use command-only evidence")
	tampered.EffectEvidence.RecordingMode = choreographyDeferredFinalizeRecordingMode
	tampered.EffectDigest, err = DigestChoreographyLocalEffect(tampered.EffectEvidence)
	require.NoError(t, err)
	require.NoError(t, validateChoreographyLocalEffect(tampered, record),
		"a record task can finish before the run's final tag is written")
	tampered.EffectEvidence.ReceiptObject, tampered.EffectEvidence.ReceiptCommit = receipt, commit
	tampered.EffectDigest, err = DigestChoreographyLocalEffect(tampered.EffectEvidence)
	require.NoError(t, err)
	require.NoError(t, validateChoreographyLocalEffect(tampered, record),
		"a deferred pushed record retains its exact publication receipt")
	tampered.EffectEvidence.Operation = "build"
	tampered.EffectDigest, err = DigestChoreographyLocalEffect(tampered.EffectEvidence)
	require.NoError(t, err)
	build := record
	build.Kind = "build"
	require.ErrorContains(t, validateChoreographyLocalEffect(tampered, build),
		"neither a publish nor a record task")
}

func TestRetainedLocalEffectReplaysItsExactSignedEvidence(t *testing.T) {
	_, original, binding, spec := newChoreographyLedgerFixture(t,
		ChoreographyTaskSpec{Task: "api:publish", Package: "api", Kind: "publish",
			LocalAuthority: true, ExecutorOwned: true, Budget: "build", Weight: 1,
			EligibleNodes: []string{"node-a"}},
		ChoreographyTaskSpec{Task: "consumer", Predecessors: []string{"api:publish"},
			ExecutorOwned: true})
	opts := original.opts
	persist, err := NewGitChoreographyPersist(opts)
	require.NoError(t, err)
	brokerOptions := choreographyFixtureBrokerOptions(persist)
	brokerOptions.OutcomePolicy = preparedBrokerPolicy{}
	broker, err := RecoverFirstNodeBroker(t.Context(), brokerOptions)
	require.NoError(t, err)
	inputsDigest, _, err := broker.ExpectedInputIdentity(spec.Task)
	require.NoError(t, err)
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: 1, Node: "node-a", Branch: spec.Branch,
		ExpectedOID: spec.ReservationOID, InputsDigest: inputsDigest,
		ProposalID: "native-effect-authority"}
	completeChoreographyFixtureClaim(t, persist, &claim)
	accepted, err := broker.AcceptClaim(t.Context(), claim)
	require.NoError(t, err)
	evidence := ChoreographyLocalEffectEvidence{Operation: "publish", Package: "api",
		Version: "1.2.3", Tag: "api-v1.2.3", Attempt: 1, PlanDigest: binding.PlanDigest,
		RecordingMode: "command-only", Observation: "succeeded"}
	digest, err := DigestChoreographyLocalEffect(evidence)
	require.NoError(t, err)
	outcome := ChoreographyLocalOutcome{ChoreographyRunBinding: binding, Task: spec.Task,
		Node: "node-a", ClaimOID: accepted.OID,
		EffectDigest: digest, EffectEvidence: evidence, Succeeded: true}
	oid, err := persist.PersistLocalOutcome(t.Context(), outcome)
	require.NoError(t, err)
	replayed, err := NewGitChoreographyPersist(opts)
	require.NoError(t, err)
	page, err := replayed.ReadEvents(t.Context(), choreographyFixtureStartCursor(t, replayed), 4)
	require.NoError(t, err)
	require.Len(t, page.Events, 2)
	require.Equal(t, oid, page.Events[1].TaskOID)
	require.Equal(t, ChoreographyTerminal, page.Events[1].Kind)
	read, err := opts.Mailbox.ReadChoreographyLocalOutcome(t.Context(), binding,
		page.Events[1], spec)
	require.NoError(t, err)
	require.Equal(t, outcome, read)
	locator := NodeSlotNativeRecoveryLocator{Binding: binding,
		CanonicalEndpoint: original.opts.Mailbox.endpoint,
		BootstrapOID:      original.opts.BootstrapOID,
		LedgerRootOID:     original.opts.Ledger.OID,
		TaskBranch:        spec.Branch,
		ReservationOID:    spec.ReservationOID,
		Kind:              spec.Kind, Node: "node-a"}
	// A cold reader has no compiled task map; its locator and the signed
	// terminal must still suffice to verify the branchless effect object.
	replayed.localTasks = nil
	feed := &RetainedChoreographyFeed{reader: replayed, maxDocumentBytes: 1 << 20}
	require.NoError(t, feed.VerifyNativeTaskFinality(t.Context(), spec.Task,
		locator, page.Events[1]))
	consumer := opts.Tasks[1]
	coldOptions := choreographyFixtureBrokerOptions(replayed)
	coldOptions.OutcomePolicy = preparedBrokerPolicy{}
	cold, err := RecoverFirstNodeBroker(t.Context(), coldOptions)
	require.NoError(t, err)
	readyDigest, predecessorOIDs, err := cold.ExpectedInputIdentity(consumer.Task)
	require.NoError(t, err)
	require.Equal(t, []string{oid}, predecessorOIDs,
		"a native terminal without Result uses its signed terminal task OID")
	claimConsumer := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: consumer.Task, Attempt: 1, Node: "node-a", Branch: consumer.Branch,
		ExpectedOID: consumer.ReservationOID, InputsDigest: readyDigest,
		ProposalID: "consumer-after-native"}
	completeChoreographyFixtureClaim(t, replayed, &claimConsumer)
	claimConsumer.Bindings.PredecessorResultOIDs = predecessorOIDs
	claimConsumer.BindingsDigest, err = DigestChoreographyTaskBindings(claimConsumer.Bindings)
	require.NoError(t, err)
	cold.resolveBindings = func(context.Context, string, string) (ChoreographyTaskBindings, error) {
		return ChoreographyTaskBindings{PredecessorResultOIDs: predecessorOIDs}, nil
	}
	claimedConsumer, err := cold.AcceptClaim(t.Context(), claimConsumer)
	require.NoError(t, err)
	require.True(t, claimedConsumer.Accepted)
}
