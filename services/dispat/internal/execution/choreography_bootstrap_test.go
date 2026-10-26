// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

func TestChoreographyBootstrapCarriesHistoricalTagTargetAndExactTagObject(t *testing.T) {
	source := newMailboxFixture(t)
	canonical := source.second(t)
	plumbing := gitx.NewPlumbing(source.git)
	tree := plumbing.MakeTree(t.Context(), nil)
	blob := plumbing.HashObject(t.Context(), strings.NewReader("unrelated blob tag"))
	treeWithBlob := plumbing.MakeTree(t.Context(), []gitx.TreeEntry{{Mode: gitx.TreeModeFile,
		Type: "blob", OID: blob, Name: "payload"}})
	planned := plumbing.CommitTree(t.Context(), tree, nil, "planned")
	snapshot := plumbing.CommitTree(t.Context(), tree, []string{planned}, "snapshot")
	historical := plumbing.CommitTree(t.Context(), tree, nil, "historical")
	since := plumbing.CommitTree(t.Context(), tree, nil, "since-anchor")
	require.NoError(t, plumbing.Err())
	// The annotated tag's target is not in the snapshot's ancestry. A
	// lightweight tag exercises the separate direct commit carrier too.
	runGitIn(t, source.store, "-c", "user.name=Dispat", "-c", "user.email=dispat@example.test",
		"tag", "-a", "v1.0.0", "-m", "historical", historical)
	runGitIn(t, source.store, "tag", "v2.0.0", snapshot)
	runGitIn(t, source.store, "-c", "user.name=Dispat", "-c", "user.email=dispat@example.test",
		"tag", "-a", "nested", "-m", "nested", "refs/tags/v1.0.0")
	runGitIn(t, source.store, "tag", "tree-input", treeWithBlob)
	runGitIn(t, source.store, "tag", "blob-input", blob)
	tagOID := strings.TrimSpace(runGitIn(t, source.store, "rev-parse", "refs/tags/v1.0.0"))
	nestedOID := strings.TrimSpace(runGitIn(t, source.store, "rev-parse", "refs/tags/nested"))
	require.NoError(t, canonical.git.ImportCommitFrom(t.Context(), source.store, snapshot))
	binding := ChoreographyRunBinding{Run: "run", Generation: "generation",
		PlanDigest: "plan"}
	graphTasks := []ChoreographyTaskSpec{{Task: "bootstrap-virtual", Virtual: true}}
	digest, err := DigestChoreographyLogicalGraph(ChoreographyLogicalGraphInventory{
		Binding: binding, AlgorithmVersion: ChoreographyGraphAlgorithmVersion,
		SemanticGraphDigest: "tag-bootstrap-semantic", Tasks: graphTasks})
	require.NoError(t, err)
	binding.GraphDigest = digest
	bootstrap := &RunBootstrap{Protocol: ChoreographyProtocolVersion, Mode: "compilation",
		AlgorithmVersion: ChoreographyGraphAlgorithmVersion,
		JoinPolicy: ChoreographyJoinPolicy{AdmissionDurationSeconds: 60,
			Authorized: []ChoreographyAuthorizedNode{{Name: "first", CoordinationEndpoint: canonical.endpoint}}},
		CoordinationEndpoint:   canonical.endpoint,
		ChoreographyRunBinding: binding, ReturnEndpoint: "https://localhost:8383",
		ReturnCertSHA256: hex.EncodeToString(make([]byte, 32)),
		PinnedInputs: PinnedRunInputs{Repositories: []AssignmentRepository{{
			Snapshot: snapshot, PlannedHead: planned}},
			NativeRefs: []PinnedNativeRef{{Name: "v1.0.0", Object: tagOID, Peeled: historical},
				{Name: "v2.0.0", Object: snapshot},
				{Name: "nested", Object: nestedOID, Peeled: historical},
				{Name: "tree-input", Object: treeWithBlob},
				{Name: "blob-input", Object: blob}},
			SinceAnchor: &PinnedSinceAnchor{OID: since},
			Selection:   []byte(`{"command":"release"}`), Options: []byte(`{"strict":false}`)}}
	oid, err := canonical.mailbox.PrepareChoreographyBootstrap(t.Context(), ChoreographyBootstrapOptions{
		Bootstrap: bootstrap, MaxDocumentBytes: 1 << 20, MaxTagBytes: 1 << 20,
		SinceSourceDir: source.store,
		TagSources: []ChoreographyTagSource{{Name: "v1.0.0", Object: tagOID,
			Peeled: historical, SourceDir: source.store},
			{Name: "v2.0.0", Object: snapshot, SourceDir: source.store},
			{Name: "nested", Object: nestedOID, Peeled: historical, SourceDir: source.store},
			{Name: "tree-input", Object: treeWithBlob, SourceDir: source.store},
			{Name: "blob-input", Object: blob, SourceDir: source.store}},
	})
	require.NoError(t, err)
	parents, err := canonical.git.CommitParents(t.Context(), oid)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{snapshot, historical, since}, parents)
	require.NoError(t, canonical.git.PushCreateAtomic(t.Context(), source.endpoint,
		[]gitx.CreateRef{{Branch: "dispat-bootstrap-test", OID: oid}}))
	peer := source.second(t)
	runGitIn(t, peer.store, "fetch", "--no-tags", source.endpoint,
		"refs/heads/dispat-bootstrap-test:refs/heads/dispat-bootstrap-test")
	read, err := peer.mailbox.ReadChoreographyBootstrap(t.Context(), oid, 1<<20, 1<<20)
	require.NoError(t, err)
	require.Equal(t, binding, read.ChoreographyRunBinding)
	require.Len(t, read.TagClosurePacks, 1)
	require.Equal(t, tagOID, strings.TrimSpace(runGitIn(t, peer.store, "rev-parse", tagOID)))
	require.Equal(t, historical, strings.TrimSpace(runGitIn(t, peer.store, "rev-parse", tagOID+"^{}")))
	require.Equal(t, historical, strings.TrimSpace(runGitIn(t, peer.store, "rev-parse", nestedOID+"^{}")))
	require.Equal(t, "tree\n", runGitIn(t, peer.store, "cat-file", "-t", treeWithBlob))
	require.Equal(t, "unrelated blob tag", runGitIn(t, peer.store, "cat-file", "blob", blob))
	ledger, err := PrepareChoreographyLedgerRoot(t.Context(), ChoreographyLedgerRootOptions{
		Mailbox: canonical.mailbox, Binding: binding, BootstrapOID: oid,
		Roster: ChoreographyRuntimeRoster{
			Nodes: []ChoreographyRuntimeNode{{Name: "first", Platform: "linux/amd64",
				Capacity: 1, First: true, AuthorityID: "0123456789abcdef0123456789abcdef"}},
			Budgets: []ChoreographyRuntimeBudget{{Name: "build", Capacity: 1}},
		}})
	require.NoError(t, err)
	require.NoError(t, canonical.git.PushCreateAtomic(t.Context(), source.endpoint,
		[]gitx.CreateRef{{Branch: ledger.Branch, OID: ledger.OID}}))
	carrier := ChoreographyGraphCarrier{ChoreographyRunBinding: binding,
		BootstrapOID: oid, AlgorithmVersion: ChoreographyGraphAlgorithmVersion,
		SemanticGraphDigest: "tag-bootstrap-semantic", Tasks: graphTasks}
	carrier.LogicalDescriptorDigest, carrier.ManifestDigest, err = digestChoreographyGraphCarrier(carrier)
	require.NoError(t, err)
	carrierBytes, err := json.Marshal(carrier)
	require.NoError(t, err)
	graphOID, err := canonical.mailbox.commitMessage(t.Context(), choreographyGraphRootKind,
		carrierBytes, []string{oid}, nil)
	require.NoError(t, err)
	require.NoError(t, canonical.git.PushCreateAtomic(t.Context(), source.endpoint,
		[]gitx.CreateRef{{Branch: FormatChoreographyGraphRootBranch(binding.Run), OID: graphOID}}))
	init := ChoreographyEvent{Sequence: 1, Kind: ChoreographyInitializationComplete,
		Task: choreographyRunTaskID, TaskOID: graphOID, GraphRootOID: graphOID}
	initBytes, err := json.Marshal(choreographyLedgerRecord{
		ChoreographyRunBinding: binding, Sequence: 1, Event: &init})
	require.NoError(t, err)
	initOID, err := canonical.mailbox.commitMessage(t.Context(), choreographyLedgerKind,
		initBytes, []string{ledger.OID, graphOID}, nil)
	require.NoError(t, err)
	require.NoError(t, canonical.git.PushAtomicAdvances(t.Context(), source.endpoint,
		[]gitx.BranchAdvance{{Branch: ledger.Branch, ExpectedOld: ledger.OID, OID: initOID}}))
	feed, err := OpenRetainedChoreographyFeed(t.Context(), RetainedChoreographyFeedOptions{
		Mailbox: peer.mailbox, Binding: binding, BootstrapOID: oid,
		MaxDocumentBytes: 1 << 20, MaxTagBytes: 1 << 20})
	require.NoError(t, err)
	_, err = OpenRetainedChoreographyFeed(t.Context(), RetainedChoreographyFeedOptions{
		Mailbox: peer.mailbox, Binding: binding, BootstrapOID: oid,
		MaxDocumentBytes: 1 << 20, MaxTagBytes: 1})
	require.Error(t, err, "a ledger root cannot make unreadable bootstrap bytes trustworthy")
	page, err := feed.ReadEvents(t.Context(), ChoreographyEventCursor{}, 32)
	require.NoError(t, err)
	require.Equal(t, []ChoreographyEventKind{ChoreographyInitializationComplete},
		[]ChoreographyEventKind{page.Events[0].Kind})
	require.Equal(t, initOID, page.Head.OID)
}
