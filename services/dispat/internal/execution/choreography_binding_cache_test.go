// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

type bindingCacheEventReader struct{ events []ChoreographyEvent }

func (r *bindingCacheEventReader) ReadEvents(_ context.Context,
	cursor ChoreographyEventCursor, limit int) (ChoreographyEventPage, error) {
	start := int(cursor.Sequence)
	end := min(start+limit, len(r.events))
	page := ChoreographyEventPage{Events: append([]ChoreographyEvent(nil), r.events[start:end]...),
		Cursor: cursor, Head: cursor, More: end < len(r.events)}
	if end > start {
		last := r.events[end-1]
		page.Cursor = ChoreographyEventCursor{OID: last.LedgerOID, Sequence: last.Sequence}
	}
	if len(r.events) > 0 {
		last := r.events[len(r.events)-1]
		page.Head = ChoreographyEventCursor{OID: last.LedgerOID, Sequence: last.Sequence}
	}
	return page, nil
}

func TestAcceptedBuildSnapshotCacheCannotReplaceFirstAcceptedBaseline(t *testing.T) {
	coordinator := &Coordinator{snapshots: newSnapshots(), Log: zerolog.Nop()}
	spec := ChoreographyTaskSpec{Task: "core:build", Package: "core", Kind: string(KindBuild)}
	accepted := ChoreographyTaskBindings{Repositories: []AssignmentRepository{
		{Name: "", Path: ".", Snapshot: "accepted-oid"},
	}}
	coordinator.CacheAcceptedChoreographyBuildSnapshot(spec, accepted, "")
	require.Equal(t, "accepted-oid", coordinator.PreparedSnapshot("core"))
	changed := ChoreographyTaskBindings{Repositories: []AssignmentRepository{
		{Name: "", Path: ".", Snapshot: "changed-oid"},
	}}
	coordinator.CacheAcceptedChoreographyBuildSnapshot(spec, changed, "")
	require.Equal(t, "accepted-oid", coordinator.PreparedSnapshot("core"))
	coordinator.CacheAcceptedChoreographyBuildSnapshot(
		ChoreographyTaskSpec{Task: "core:test", Package: "core", Kind: string(KindStage)}, changed, "")
	require.Equal(t, "accepted-oid", coordinator.PreparedSnapshot("core"))
}

func TestVerifiedResultInstallsAtTwoSignedDestinations(t *testing.T) {
	bindings := ChoreographyTaskBindings{}
	imported := make(map[string]bool)
	seen := make(map[choreographyInputDestination]bool)
	imports := 0
	importCommit := func(context.Context, string, string) error {
		imports++
		return nil
	}
	for _, path := range []string{"packages/a", "packages/b", "packages/a"} {
		err := appendVerifiedChoreographyInput(context.Background(), &bindings,
			AssignmentInput{Task: "assets:build", Package: "assets", Commit: "signed-result",
				Digest: "verified-manifest", Path: path},
			"/canonical/mailbox", imported, seen, importCommit)
		require.NoError(t, err)
	}
	require.Equal(t, 1, imports, "one immutable Result object is imported once")
	require.Equal(t, []AssignmentInput{
		{Task: "assets:build", Package: "assets", Commit: "signed-result",
			Digest: "verified-manifest", Path: "packages/a"},
		{Task: "assets:build", Package: "assets", Commit: "signed-result",
			Digest: "verified-manifest", Path: "packages/b"},
	}, bindings.Inputs, "each declared installation path remains in the Assignment")
}

func TestReleaseFrameRestoresSignedDeclaredPathForLaterPeerStage(t *testing.T) {
	rehomed := plan.Output{Name: "CORE_ASSET", Value: "/first/coverage/core.asset", Source: "core:build"}
	literal := plan.Output{Name: "LITERAL", Value: "dispat-output://source/core/coverage/core.asset"}
	signed := ExportedValue{Name: rehomed.Name, Source: rehomed.Source,
		DeclaredPath: &ExportedDeclaredPath{Repository: "source", Package: "core",
			Root: "coverage", Relative: "core.asset"}}
	bindings := ChoreographyTaskBindings{Exports: formatExports([]plan.Output{rehomed, literal})}
	imports := 0
	inputs := choreographyCarriedExportInputs{
		Outputs: []plan.Output{rehomed, literal},
		Verified: map[plan.Output]ChoreographyVerifiedCarriedExport{
			rehomed: {Signed: signed, Output: ChoreographyVerifiedOutput{
				Task: "core:build", Package: "core", ResultOID: "signed-result",
				Manifest: &OutputManifest{Digest: "signed-manifest"},
			}},
		},
		Sources: []Source{{Name: "source", Path: "src"}}, SourceDir: "/canonical/mailbox",
		Imported: make(map[string]bool), Seen: make(map[choreographyInputDestination]bool),
		ImportCommit: func(context.Context, string, string) error { imports++; return nil },
	}
	require.NoError(t, applyVerifiedChoreographyExports(t.Context(), &bindings, inputs))
	require.Equal(t, signed, bindings.Exports[0])
	require.Equal(t, literal.Value, bindings.Exports[1].Value)
	require.Nil(t, bindings.Exports[1].DeclaredPath)
	require.Equal(t, []AssignmentInput{{Task: "core:build", Package: "core",
		Commit: "signed-result", Digest: "signed-manifest", Path: "src"}}, bindings.Inputs)
	require.Equal(t, 1, imports)
	inputs.Sources = []Source{{Name: "unrelated", Path: "other"}}
	require.ErrorContains(t, applyVerifiedChoreographyExports(t.Context(), &bindings, inputs),
		"outside its input closure")
}

func TestSignedBindingCacheHoldsNoClaimUntilPackageSettlement(t *testing.T) {
	feed := &bindingCacheEventReader{events: []ChoreographyEvent{
		{Sequence: 1, LedgerOID: "ledger-a", Kind: ChoreographyTerminal,
			Task: "alpha", Package: "alpha", TaskOID: "terminal-alpha", Succeeded: true},
		{Sequence: 2, LedgerOID: "ledger-b", Kind: ChoreographyNoClaimExpired,
			Task: "beta", Package: "beta", TaskOID: "typed-beta"},
	}}
	cache, err := NewChoreographyBindingCache(ChoreographyBindingCacheOptions{
		Binding: ChoreographyRunBinding{Run: "run", Generation: "generation",
			PlanDigest: "plan", GraphDigest: "graph"},
		LedgerRootOID: "root", Reader: feed, Mailbox: &GitMailbox{}, MaxDocumentBytes: 1,
		Tasks: []ChoreographyTaskSpec{
			{Task: "alpha", Package: "alpha"}, {Task: "beta", Package: "beta"},
			{Task: "consumer", Package: "consumer", Predecessors: []string{"alpha", "beta"}},
		},
		OutcomePackagesByTask: map[string][]string{"consumer": {"beta"}},
		Accumulate: func(_ string, _ map[string][]plan.Output, own []plan.Output) ([]plan.Output, error) {
			return own, nil
		},
	})
	require.NoError(t, err)
	_, err = cache.State(context.Background(), "consumer")
	require.ErrorContains(t, err, "lacks signed predecessor state")
	feed.events = append(feed.events, ChoreographyEvent{
		Sequence: 3, LedgerOID: "ledger-settled", Kind: ChoreographyNoClaimPackageSettled,
		Task: "beta", Package: "beta", TaskOID: "typed-beta", PackageOutcome: "failed",
	})
	state, err := cache.State(context.Background(), "consumer")
	require.NoError(t, err)
	require.Equal(t, []string{"terminal-alpha", "typed-beta"}, state.PredecessorTaskOIDs)
	require.Equal(t, "failed", string(state.ProviderOutcomes["beta"]))
}

func TestSignedBindingCacheUsesAcceptedOutputForPredecessorClaimIdentity(t *testing.T) {
	feed := &bindingCacheEventReader{events: []ChoreographyEvent{{
		Sequence: 1, LedgerOID: "ledger-terminal", Kind: ChoreographyTerminal,
		Task: "provider", Package: "provider", TaskOID: "terminal-decision",
		ResultReceiptOID: "accepted-result-receipt", OutputOID: "raw-result-object",
		Attempt: 1, Succeeded: true,
	}, {
		Sequence: 2, LedgerOID: "ledger-settled", Kind: ChoreographyTaskPackageSettled,
		Task: "provider", Package: "provider", TaskOID: "terminal-decision",
		PriorTaskOID: "terminal-decision", Attempt: 1,
	}}}
	cache, err := NewChoreographyBindingCache(ChoreographyBindingCacheOptions{
		Binding: ChoreographyRunBinding{Run: "run", Generation: "generation",
			PlanDigest: "plan", GraphDigest: "graph"},
		LedgerRootOID: "root", Reader: feed, Mailbox: &GitMailbox{}, MaxDocumentBytes: 1,
		Tasks: []ChoreographyTaskSpec{{Task: "provider", Package: "provider"},
			{Task: "consumer", Package: "consumer", Predecessors: []string{"provider"}}},
		OutputContexts: map[string]ChoreographyBindingOutputContext{
			"provider": {Package: "provider"},
		},
		Accumulate: func(_ string, _ map[string][]plan.Output, own []plan.Output) ([]plan.Output, error) {
			return own, nil
		},
	})
	require.NoError(t, err)
	cache.accepted["accepted-result-receipt"] = choreographyAcceptedBindingResult{oid: "raw-result-object"}
	state, err := cache.State(t.Context(), "consumer")
	require.NoError(t, err)
	require.Equal(t, []string{"raw-result-object"}, state.PredecessorTaskOIDs,
		"a Claim names the accepted output carrier verified through the signed Result receipt")
}

func TestSignedBindingCacheRefusesTerminalNamingAnotherResultReceipt(t *testing.T) {
	feed := &bindingCacheEventReader{events: []ChoreographyEvent{{
		Sequence: 1, LedgerOID: "ledger-foreign", Kind: ChoreographyTerminal,
		Task: "consumer", Package: "consumer", TaskOID: "terminal-consumer",
		ResultReceiptOID: "receipt-from-another-claim", OutputOID: "unverified-result-object",
		Succeeded: true,
	}}}
	cache, err := NewChoreographyBindingCache(ChoreographyBindingCacheOptions{
		Binding: ChoreographyRunBinding{Run: "run", Generation: "generation",
			PlanDigest: "plan", GraphDigest: "graph"},
		LedgerRootOID: "root", Reader: feed, Mailbox: &GitMailbox{}, MaxDocumentBytes: 1,
		Tasks: []ChoreographyTaskSpec{{Task: "consumer", Package: "consumer"}},
		Accumulate: func(_ string, _ map[string][]plan.Output, own []plan.Output) ([]plan.Output, error) {
			return own, nil
		},
	})
	require.NoError(t, err)
	_, err = cache.State(context.Background(), "consumer")
	require.ErrorContains(t, err, "no verified signed Result")
}

func TestSignedBindingCacheTreatsCancellationAckAsTerminalWithoutResultOutput(t *testing.T) {
	feed := &bindingCacheEventReader{events: []ChoreographyEvent{{
		Sequence: 1, LedgerOID: "ledger-ack-terminal", Kind: ChoreographyTerminal,
		Task: "provider", Package: "provider", TaskOID: "terminal-after-ack",
		ResultReceiptOID: "signed-ack-receipt", Attempt: 1, Succeeded: false,
	}, {
		Sequence: 2, LedgerOID: "ledger-ack-settled", Kind: ChoreographyTaskPackageSettled,
		Task: "provider", Package: "provider", TaskOID: "terminal-after-ack",
		PriorTaskOID: "terminal-after-ack", Attempt: 1, PackageOutcome: "failed",
	}}}
	cache, err := NewChoreographyBindingCache(ChoreographyBindingCacheOptions{
		Binding: ChoreographyRunBinding{Run: "run", Generation: "generation",
			PlanDigest: "plan", GraphDigest: "graph"},
		LedgerRootOID: "root", Reader: feed, Mailbox: &GitMailbox{}, MaxDocumentBytes: 1,
		Tasks: []ChoreographyTaskSpec{{Task: "provider", Package: "provider"},
			{Task: "consumer", Package: "consumer", Predecessors: []string{"provider"}}},
		Accumulate: func(_ string, _ map[string][]plan.Output, own []plan.Output) ([]plan.Output, error) {
			return own, nil
		},
	})
	require.NoError(t, err)
	state, err := cache.State(t.Context(), "consumer")
	require.NoError(t, err)
	require.Equal(t, []string{"terminal-after-ack"}, state.PredecessorTaskOIDs)
	require.Empty(t, state.VerifiedOutputByPackage)
}

func TestSignedBindingCacheDoesNotRenderVirtualPassThroughAsExecutableTask(t *testing.T) {
	feed := &bindingCacheEventReader{events: []ChoreographyEvent{{
		Sequence: 1, LedgerOID: "ledger-provider-terminal", Kind: ChoreographyTerminal,
		Task: "provider:prepare", Package: "provider", TaskOID: "provider-decision", Succeeded: true,
	}}}
	cache, err := NewChoreographyBindingCache(ChoreographyBindingCacheOptions{
		Binding: ChoreographyRunBinding{Run: "run", Generation: "generation",
			PlanDigest: "plan", GraphDigest: "graph"},
		LedgerRootOID: "root", Reader: feed, Mailbox: &GitMailbox{}, MaxDocumentBytes: 1,
		Tasks: []ChoreographyTaskSpec{{Task: "provider:prepare", Package: "provider"},
			{Task: "provider:10:", Package: "provider", Virtual: true,
				Predecessors: []string{"provider:prepare"}},
			{Task: "consumer:build", Package: "consumer",
				Predecessors: []string{"provider:10:"}}},
		Accumulate: func(taskID string, _ map[string][]plan.Output, own []plan.Output) ([]plan.Output, error) {
			require.NotEqual(t, "provider:10:", taskID, "virtual precedence has no executable frame")
			return own, nil
		},
	})
	require.NoError(t, err)
	state, err := cache.State(t.Context(), "consumer:build")
	require.NoError(t, err)
	require.Equal(t, []string{""}, state.PredecessorTaskOIDs)
}

func TestSignedBindingCacheHoldsTaskOutputUntilExactPackageSettlementAndColdReplay(t *testing.T) {
	feed := &bindingCacheEventReader{events: []ChoreographyEvent{{
		Sequence: 1, LedgerOID: "ledger-terminal", Kind: ChoreographyTerminal,
		Task: "provider:build", Package: "provider", TaskOID: "terminal-tip",
		Attempt: 1, ResultReceiptOID: "accepted-receipt", OutputOID: "raw-result",
		Succeeded: true,
	}}}
	options := ChoreographyBindingCacheOptions{
		Binding: ChoreographyRunBinding{Run: "run", Generation: "generation",
			PlanDigest: "plan", GraphDigest: "graph"},
		LedgerRootOID: "root", Reader: feed, Mailbox: &GitMailbox{}, MaxDocumentBytes: 1,
		Tasks: []ChoreographyTaskSpec{{Task: "provider:build", Package: "provider",
			Kind: string(KindBuild)}, {Task: "consumer:build", Package: "consumer",
			Predecessors: []string{"provider:build"}}},
		OutputContexts:      map[string]ChoreographyBindingOutputContext{"provider:build": {}},
		InputPackagesByTask: map[string][]string{"consumer:build": {"provider"}},
		Accumulate: func(_ string, _ map[string][]plan.Output, own []plan.Output) ([]plan.Output, error) {
			return own, nil
		},
	}
	newCache := func() *ChoreographyBindingCache {
		cache, err := NewChoreographyBindingCache(options)
		require.NoError(t, err)
		cache.accepted["accepted-receipt"] = choreographyAcceptedBindingResult{
			oid: "raw-result", result: Result{Outputs: &OutputManifest{Digest: "manifest"},
				Exports: []ExportedValue{{Name: "TOKEN", Value: "token"}}}}
		return cache
	}
	cache := newCache()
	_, err := cache.State(t.Context(), "consumer:build")
	require.ErrorContains(t, err, "no signed logical finality")
	require.Empty(t, cache.providers, "a signed task terminal alone does not release package outputs")
	feed.events = append(feed.events, ChoreographyEvent{
		Sequence: 2, LedgerOID: "ledger-settled", Kind: ChoreographyTaskPackageSettled,
		Task: "provider:build", Package: "provider", TaskOID: "terminal-tip",
		PriorTaskOID: "terminal-tip", Attempt: 1, PackageOutcome: "succeeded",
	})
	state, err := cache.State(t.Context(), "consumer:build")
	require.NoError(t, err)
	require.Equal(t, []string{"raw-result"}, state.PredecessorTaskOIDs)
	require.Equal(t, "raw-result", state.VerifiedOutputByPackage["provider"].ResultOID)
	require.Equal(t, []plan.Output{{Name: "TOKEN", Value: "token"}},
		state.PredecessorOutputs["provider:build"])
	cold := newCache()
	replayed, err := cold.State(t.Context(), "consumer:build")
	require.NoError(t, err)
	require.Equal(t, state, replayed, "cold event feed rebuilds only settled output state")
}
