// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

const choreographyTestAdmissionSeconds = 60

func newExecutionStartFixture(t *testing.T) (graphCarrierFixture, *GitChoreographyPersist, FirstNodeBrokerOptions) {
	t.Helper()
	f := newCompleteGraphCarrierFixture(t)
	policy := ChoreographyJoinPolicy{AdmissionDurationSeconds: choreographyTestAdmissionSeconds,
		Authorized: []ChoreographyAuthorizedNode{{Name: "peer", CoordinationEndpoint: f.endpoint.endpoint}}}
	persist, err := NewGitChoreographyPersist(GitChoreographyOptions{
		Binding: f.binding, Mailbox: f.endpoint.mailbox,
		Ledger:       ChoreographyLedgerReservation{Branch: FormatChoreographyLedgerBranch(f.binding.Run), OID: f.ledgerRoot},
		BootstrapOID: f.bootstrap, GraphRootOID: f.root, InitializationCompleteOID: f.complete,
		Tasks: f.carrier.Tasks, Templates: f.carrier.Templates, JoinPolicy: policy,
		AlgorithmVersion: ChoreographyGraphAlgorithmVersion, MaxPackBytes: 1 << 20,
		VerifyFirst:  func(context.Context) error { return nil },
		VerifyResult: func(context.Context, ChoreographyResultProposal, Result, *gitx.LocalGitx) error { return nil },
	})
	require.NoError(t, err)
	opts := FirstNodeBrokerOptions{Binding: f.binding, GraphRootOID: f.root,
		InitialMembershipEpochOID: f.complete, FirstNode: "first", Tasks: f.carrier.Tasks,
		Templates: f.carrier.Templates,
		Members: []ChoreographyRuntimeNode{{Name: "first", First: true, Platform: "linux/amd64",
			Capacity: 1, AuthorityID: "0123456789abcdef0123456789abcdef"},
			{Name: "peer", Platform: "linux/amd64", Capacity: 1,
				AuthorityID: "11111111111111111111111111111111"}},
		NodeCapacity: map[string]int{"first": 1, "peer": 1},
		Budgets:      map[string]int{ChoreographyBudgetBuild: 1}, JoinPolicy: policy, Persist: persist}
	return f, persist, opts
}

func TestChoreographyExecutionStartSetsSignedAdmissionDeadlineAndReplays(t *testing.T) {
	f, persist, opts := newExecutionStartFixture(t)
	broker, err := NewFirstNodeBroker(opts)
	require.NoError(t, err)
	require.False(t, broker.executionStarted)
	before := time.Now().UTC()
	cursor, err := broker.StartExecution(t.Context())
	require.NoError(t, err)
	require.Equal(t, uint64(2), cursor.Sequence)
	require.True(t, broker.executionStarted)
	deadline, err := time.Parse(choreographyAdmissionTimeFormat, broker.admissionDeadlineUTC)
	require.NoError(t, err)
	require.True(t, deadline.After(before.Add(time.Duration(choreographyTestAdmissionSeconds-1)*time.Second)))
	page, err := persist.ReadEvents(t.Context(), ChoreographyEventCursor{}, 3)
	require.NoError(t, err)
	require.Len(t, page.Events, 2)
	require.Equal(t, ChoreographyExecutionStarted, page.Events[1].Kind)
	require.Equal(t, f.root, page.Events[1].GraphRootOID)
	require.Equal(t, broker.admissionDeadlineUTC, page.Events[1].AdmissionDeadlineUTC)
	replayedCursor, err := broker.StartExecution(t.Context())
	require.NoError(t, err)
	require.Equal(t, cursor, replayedCursor)
	restarted, err := NewGitChoreographyPersist(persist.opts)
	require.NoError(t, err)
	opts.Persist = restarted
	recovered, err := RecoverFirstNodeBroker(t.Context(), opts)
	require.NoError(t, err)
	require.True(t, recovered.executionStarted)
	require.Equal(t, broker.admissionDeadlineUTC, recovered.admissionDeadlineUTC)
}

func TestChoreographyInputInvalidationFencesClaimBeforeStopReplay(t *testing.T) {
	f, persist, opts := newExecutionStartFixture(t)
	broker, err := NewFirstNodeBroker(opts)
	require.NoError(t, err)
	_, err = broker.StartExecution(t.Context())
	require.NoError(t, err)
	request := ChoreographyInputInvalidationRequest{ChoreographyRunBinding: f.binding,
		GraphRootOID: f.root, OldPreparedOID: f.taskRoot, NewPreparedOID: f.bootstrap,
		AffectedTasks: []string{f.spec.Task}}
	// Simulate a crash after invalidation CAS but before explicit RunStopping.
	require.NoError(t, persist.PersistInputInvalidation(t.Context(), request))
	page, err := persist.ReadEvents(t.Context(), ChoreographyEventCursor{}, 4)
	require.NoError(t, err)
	require.Len(t, page.Events, 3)
	require.Equal(t, ChoreographyInputInvalidation, page.Events[2].Kind)
	require.True(t, persist.stopping)
	restarted, err := NewGitChoreographyPersist(persist.opts)
	require.NoError(t, err)
	opts.Persist = restarted
	recovered, err := RecoverFirstNodeBroker(t.Context(), opts)
	require.NoError(t, err)
	require.True(t, recovered.invalidated)
	require.True(t, recovered.stopping)
	require.True(t, recovered.signedStop)
	page, err = restarted.ReadEvents(t.Context(), ChoreographyEventCursor{}, 5)
	require.NoError(t, err)
	require.Len(t, page.Events, 4)
	require.Equal(t, ChoreographyRunStopping, page.Events[3].Kind)
}

func TestChoreographyStoppedInertTaskHasCanonicalCancellation(t *testing.T) {
	f, persist, opts := newExecutionStartFixture(t)
	broker, err := NewFirstNodeBroker(opts)
	require.NoError(t, err)
	_, err = broker.StartExecution(t.Context())
	require.NoError(t, err)
	_, err = broker.StopClaims(t.Context())
	require.NoError(t, err)
	require.NoError(t, broker.CancelInertTask(t.Context(), f.spec.Task))
	require.True(t, broker.tasks[f.spec.Task].terminal)
	require.NotEqual(t, f.taskRoot, broker.tasks[f.spec.Task].tip)
	require.Equal(t, broker.tasks[f.spec.Task].tip, remoteChoreographyOID(t, f.endpoint, f.spec.Branch))
	page, err := persist.ReadEvents(t.Context(), ChoreographyEventCursor{}, 5)
	require.NoError(t, err)
	require.Len(t, page.Events, 4)
	require.Equal(t, ChoreographyInertCancelled, page.Events[3].Kind)
	require.Empty(t, page.Events[3].PackageOutcome)
	restarted, err := NewGitChoreographyPersist(persist.opts)
	require.NoError(t, err)
	opts.Persist = restarted
	recovered, err := RecoverFirstNodeBroker(t.Context(), opts)
	require.NoError(t, err)
	require.True(t, recovered.tasks[f.spec.Task].terminal)
	require.Equal(t, broker.tasks[f.spec.Task].tip, recovered.tasks[f.spec.Task].tip)
	peer := f.endpoint.second(t)
	_, root, err := peer.mailbox.ReadChoreographyGraphCarrier(t.Context(), f.binding, f.bootstrap, 1<<20)
	require.NoError(t, err)
	require.Equal(t, f.root, root)
}

func TestChoreographyStopCancelsNonreadyInertTaskBeforePredecessor(t *testing.T) {
	fixture, persist, binding, _ := newChoreographyLedgerFixture(t,
		ChoreographyTaskSpec{Task: "upstream"},
		ChoreographyTaskSpec{Task: "downstream", Predecessors: []string{"upstream"}})
	children := persist.opts.Tasks
	broker, err := NewFirstNodeBroker(FirstNodeBrokerOptions{Binding: binding,
		GraphRootOID:              persist.opts.GraphRootOID,
		InitialMembershipEpochOID: persist.opts.InitializationCompleteOID,
		FirstNode:                 "node-a", Tasks: children, Templates: persist.opts.Templates,
		Members: []ChoreographyRuntimeNode{{Name: "node-a", First: true,
			Platform: "linux/amd64", Capacity: 1,
			AuthorityID: "0123456789abcdef0123456789abcdef"}},
		NodeCapacity: map[string]int{"node-a": 1}, Budgets: map[string]int{"build": 1},
		JoinPolicy: persist.opts.JoinPolicy, Persist: persist})
	require.NoError(t, err)
	require.Equal(t, 1, broker.tasks["downstream"].remaining)
	_, err = broker.StopClaims(t.Context())
	require.NoError(t, err)
	require.NoError(t, broker.CancelInertTask(t.Context(), "downstream"))
	require.Equal(t, broker.tasks["downstream"].tip,
		remoteChoreographyOID(t, fixture, children[1].Branch))
	restarted, err := NewGitChoreographyPersist(persist.opts)
	require.NoError(t, err)
	brokerOpts := FirstNodeBrokerOptions{Binding: binding,
		GraphRootOID:              persist.opts.GraphRootOID,
		InitialMembershipEpochOID: persist.opts.InitializationCompleteOID,
		FirstNode:                 "node-a", Tasks: children, Templates: persist.opts.Templates,
		Members: []ChoreographyRuntimeNode{{Name: "node-a", First: true,
			Platform: "linux/amd64", Capacity: 1,
			AuthorityID: "0123456789abcdef0123456789abcdef"}},
		NodeCapacity: map[string]int{"node-a": 1}, Budgets: map[string]int{"build": 1},
		JoinPolicy: persist.opts.JoinPolicy, Persist: restarted}
	recovered, err := RecoverFirstNodeBroker(t.Context(), brokerOpts)
	require.NoError(t, err)
	require.True(t, recovered.tasks["downstream"].terminal)
	require.Equal(t, 1, recovered.tasks["downstream"].remaining)
	frontier, err := NewCompiledChoreographyFrontier(CompiledChoreographyFrontierOptions{
		Binding:      binding,
		GraphRootOID: persist.opts.GraphRootOID,
		Node: ChoreographyRuntimeNode{Name: "node-a", First: true, Platform: "linux/amd64",
			Capacity: 1, AuthorityID: "0123456789abcdef0123456789abcdef"},
		Tasks: children,
	})
	require.NoError(t, err)
	page, err := restarted.ReadEvents(t.Context(), ChoreographyEventCursor{}, 8)
	require.NoError(t, err)
	require.NoError(t, frontier.ApplyEventPage(page))
	require.Equal(t, frontierTerminal, frontier.tasks["downstream"].state)
}

func TestChoreographyStoppedInertFailureSettlesAfterPackageBarrierAndReplays(t *testing.T) {
	fixture, persist, _, spec := newChoreographyLedgerFixture(t,
		ChoreographyTaskSpec{Task: "core:run", Package: "core"})
	opts := choreographyFixtureBrokerOptions(persist)
	opts.OutcomePolicy = preparedBrokerPolicy{}
	broker, err := RecoverFirstNodeBroker(t.Context(), opts)
	require.NoError(t, err)
	_, err = broker.StopClaims(t.Context())
	require.NoError(t, err)
	require.NoError(t, broker.CancelInertTask(t.Context(), spec.Task))
	cancelOID := broker.tasks[spec.Task].tip
	require.Equal(t, cancelOID, remoteChoreographyOID(t, fixture, spec.Branch))
	require.Empty(t, broker.packageState[spec.Package])
	require.True(t, broker.IsInertCancelled(spec.Task))
	require.Error(t, broker.SettleInertCancellation(t.Context(), spec.Task, ""),
		"a noncausal settlement requires an earlier package outcome")
	require.NoError(t, broker.SettleInertCancellation(t.Context(), spec.Task, "failed"))
	require.Equal(t, cancelOID, remoteChoreographyOID(t, fixture, spec.Branch),
		"package settlement must retain the signed task cancellation")
	require.Equal(t, "failed", broker.packageState[spec.Package])
	require.False(t, broker.IsInertCancelled(spec.Task))
	require.Error(t, broker.SettleInertCancellation(t.Context(), spec.Task, "failed"),
		"the package outcome must be applied once")
	restarted, err := NewGitChoreographyPersist(persist.opts)
	require.NoError(t, err)
	opts.Persist = restarted
	recovered, err := RecoverFirstNodeBroker(t.Context(), opts)
	require.NoError(t, err)
	require.Equal(t, "failed", recovered.packageState[spec.Package])
	require.False(t, recovered.IsInertCancelled(spec.Task))
	require.True(t, recovered.tasks[spec.Task].terminal)
	page, err := restarted.ReadEvents(t.Context(), choreographyFixtureStartCursor(t, restarted), 4)
	require.NoError(t, err)
	require.Len(t, page.Events, 3)
	require.Equal(t, ChoreographyInertCancelled, page.Events[1].Kind)
	require.Empty(t, page.Events[1].PackageOutcome)
	require.Equal(t, ChoreographyInertCancelSettled, page.Events[2].Kind)
	require.Equal(t, "failed", page.Events[2].PackageOutcome)
	require.Equal(t, cancelOID, page.Events[2].TaskOID)
}
