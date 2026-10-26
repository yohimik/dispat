// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

func TestChoreographyCompletedTaskBranchDirectlyCarriesVerifiedOutput(t *testing.T) {
	for _, scenario := range []struct {
		name         string
		tampered     bool
		absentOutput bool
		emptyOutput  bool
		failedResult bool
		wantError    bool
	}{
		{name: "exact signed output tree"},
		{name: "failed result retains verified partial output", failedResult: true},
		{name: "signed empty output tree", emptyOutput: true},
		{name: "different carried output tree", tampered: true, wantError: true},
		{name: "undeclared empty output tree", absentOutput: true, wantError: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			fixture, persist, binding, spec := newChoreographyLedgerFixtureWithSettings(t,
				choreographyLedgerFixtureSettings{AuthorityID: "0123456789abcdef0123456789abcdef",
					AdmissionSeconds: choreographyTestAdmissionSeconds,
					Tasks:            []ChoreographyTaskSpec{{Task: "build"}}, OutputRoots: []string{"dist"}})
			persist.opts.VerifyResult = func(ctx context.Context, proposal ChoreographyResultProposal,
				result Result, store *gitx.LocalGitx) error {
				if result.Outputs == nil {
					return nil
				}
				_, err := ValidateOutputs(ctx, store, result.Outputs, OutputExpectation{
					Run: binding.Run, PlanDigest: binding.PlanDigest, Generation: binding.Generation,
					Task: spec.Task, Attempt: proposal.Attempt, Roots: []string{"dist"},
					Limits: TransferLimits{MaxFiles: 8, MaxBytes: 1024},
				})
				return err
			}
			broker, err := RecoverFirstNodeBroker(t.Context(), choreographyFixtureBrokerOptions(persist))
			require.NoError(t, err)
			claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
				Task: spec.Task, Attempt: 1, Node: "node-a", Branch: spec.Branch,
				ExpectedOID: spec.ReservationOID, InputsDigest: spec.InputsDigest,
				ProposalID: "branch-output-claim"}
			completeChoreographyFixtureClaim(t, persist, &claim)
			acceptedClaim, err := broker.AcceptClaim(t.Context(), claim)
			require.NoError(t, err)

			plumbing := gitx.NewPlumbing(fixture.git)
			body := "verified build output\n"
			blobOID := plumbing.HashObject(t.Context(), strings.NewReader(body))
			distOID := plumbing.MakeTree(t.Context(), []gitx.TreeEntry{{Mode: gitx.TreeModeFile,
				Type: "blob", OID: blobOID, Name: "artifact.txt"}})
			outputTreeOID := plumbing.MakeTree(t.Context(), []gitx.TreeEntry{{Mode: gitx.TreeModeDir,
				Type: "tree", OID: distOID, Name: "dist"}})
			require.NoError(t, plumbing.Err())
			bodyDigest := sha256.Sum256([]byte(body))
			manifest := &OutputManifest{Protocol: ProtocolVersion, Run: binding.Run,
				PlanDigest: binding.PlanDigest, Generation: binding.Generation,
				Task: spec.Task, Attempt: 1, Node: "node-a", Package: "pkg",
				Platform: Platform{OS: "linux", Arch: "amd64", Dispat: "test"},
				Roots:    []string{"dist"}, Entries: []ManifestEntry{{Path: "dist/artifact.txt",
					Type: EntryFile, Mode: EntryModeFile, Size: int64(len(body)),
					SHA256: hex.EncodeToString(bodyDigest[:])}},
				Files: 1, Bytes: int64(len(body)), OutputTree: outputTreeOID}
			if scenario.emptyOutput {
				manifest.OutputTree = plumbing.MakeTree(t.Context(), nil)
				manifest.Entries, manifest.Files, manifest.Bytes = nil, 0, 0
				require.NoError(t, plumbing.Err())
			}
			manifest.Digest = CalculateManifestDigest(manifest)
			result := Result{Header: Header{Protocol: ChoreographyProtocolVersion,
				Kind: KindBuild, Run: binding.Run, Generation: binding.Generation,
				PlanDigest: binding.PlanDigest, Task: spec.Task, Attempt: 1,
				Node: "node-a", Branch: spec.Branch}, Assignment: acceptedClaim.OID,
				Status: StatusSucceeded, Outputs: manifest}
			if scenario.failedResult {
				result.Status = StatusFailed
			}
			if scenario.absentOutput {
				result.Outputs = nil
			}
			document, err := json.Marshal(result)
			require.NoError(t, err)
			carriedTreeOID := outputTreeOID
			if scenario.tampered {
				otherOID := plumbing.HashObject(t.Context(), strings.NewReader("undeclared bytes"))
				carriedTreeOID = plumbing.MakeTree(t.Context(), []gitx.TreeEntry{{Mode: gitx.TreeModeFile,
					Type: "blob", OID: otherOID, Name: "unexpected.txt"}})
				require.NoError(t, plumbing.Err())
			}
			if scenario.absentOutput {
				carriedTreeOID = plumbing.MakeTree(t.Context(), nil)
				require.NoError(t, plumbing.Err())
			}
			if scenario.emptyOutput {
				carriedTreeOID = manifest.OutputTree
			}
			rawResultOID, err := fixture.mailbox.commitMessage(t.Context(), MessageResult,
				document, nil, []gitx.TreeEntry{{Mode: gitx.TreeModeDir, Type: "tree",
					OID: carriedTreeOID, Name: outputsDir}})
			require.NoError(t, err)
			pack, err := os.CreateTemp(t.TempDir(), "branch-output-*.pack")
			require.NoError(t, err)
			t.Cleanup(func() { _ = pack.Close() })
			require.NoError(t, fixture.git.ExportCommitPack(t.Context(), rawResultOID, pack))
			proposal := ChoreographyResultProposal{ChoreographyRunBinding: binding,
				Task: spec.Task, Attempt: 1, Node: "node-a", Branch: spec.Branch,
				ClaimOID: acceptedClaim.OID, InputsDigest: claim.InputsDigest,
				ProposalID: "branch-output-result", ResultDigest: rawResultOID}
			proposal, err = PrepareChoreographyResult(t.Context(), proposal, pack, 1<<20)
			require.NoError(t, err)
			acceptedResult, err := broker.AcceptResult(t.Context(), proposal, pack)
			if scenario.wantError {
				require.Error(t, err)
				require.Equal(t, acceptedClaim.OID, remoteChoreographyOID(t, fixture, spec.Branch))
				return
			}
			require.NoError(t, err)
			checkOutput := func() {
				if scenario.emptyOutput {
					command := exec.Command("git", "--git-dir", fixture.endpoint, "rev-parse",
						spec.Branch+":outputs")
					seen, readErr := command.CombinedOutput()
					require.NoError(t, readErr, "%s", seen)
					require.Equal(t, manifest.OutputTree, strings.TrimSpace(string(seen)))
					return
				}
				command := exec.Command("git", "--git-dir", fixture.endpoint, "show",
					spec.Branch+":outputs/dist/artifact.txt")
				seen, readErr := command.CombinedOutput()
				require.NoError(t, readErr, "%s", seen)
				require.Equal(t, body, string(seen))
			}
			checkOutput() // The accepted Result receipt is already directly inspectable.
			require.NoError(t, broker.DecideAttempt(t.Context(), ChoreographyDecision{
				ChoreographyRunBinding: binding, Task: spec.Task, Branch: spec.Branch,
				Attempt: 1, Node: "node-a", ResultOID: acceptedResult.OID,
				Succeeded: !scenario.failedResult}))
			checkOutput() // Terminal keeps the same verified output tree.
			coldPersist, err := NewGitChoreographyPersist(persist.opts)
			require.NoError(t, err)
			coldBroker, err := RecoverFirstNodeBroker(t.Context(), choreographyFixtureBrokerOptions(coldPersist))
			require.NoError(t, err)
			require.True(t, coldBroker.tasks[spec.Task].terminal)
			page, err := coldPersist.ReadEvents(t.Context(), choreographyFixtureStartCursor(t, coldPersist), 8)
			require.NoError(t, err)
			var terminal ChoreographyEvent
			for _, event := range page.Events {
				if event.Kind == ChoreographyTerminal && event.Task == spec.Task {
					terminal = event
				}
			}
			require.NotEmpty(t, terminal.TaskOID)
			completed, err := fixture.mailbox.ReadChoreographyCompletedResult(t.Context(),
				ChoreographyCompletedResultQuery{Binding: binding, Task: spec.Task, Event: terminal}, 1<<20)
			require.NoError(t, err)
			require.Equal(t, manifest.OutputTree, completed.Outputs.OutputTree)
		})
	}
}

func TestChoreographyNoOutputTaskRetainsSignedFinishedBranch(t *testing.T) {
	for _, status := range []string{StatusSucceeded, StatusFailed} {
		t.Run(status, func(t *testing.T) {
			assertNoOutputResultBranch(t, status)
		})
	}
}

func assertNoOutputResultBranch(t *testing.T, status string) {
	t.Helper()
	fixture, persist, binding, spec := newChoreographyLedgerFixture(t)
	broker, err := RecoverFirstNodeBroker(t.Context(), choreographyFixtureBrokerOptions(persist))
	require.NoError(t, err)
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: 1, Node: "node-a", Branch: spec.Branch,
		ExpectedOID: spec.ReservationOID, InputsDigest: spec.InputsDigest,
		ProposalID: "no-output-claim"}
	completeChoreographyFixtureClaim(t, persist, &claim)
	acceptedClaim, err := broker.AcceptClaim(t.Context(), claim)
	require.NoError(t, err)
	result := Result{Header: Header{Protocol: ChoreographyProtocolVersion,
		Kind: KindBuild, Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Task: spec.Task, Attempt: 1,
		Node: "node-a", Branch: spec.Branch}, Assignment: acceptedClaim.OID,
		Status: status}
	document, err := json.Marshal(result)
	require.NoError(t, err)
	rawResultOID, err := fixture.mailbox.commitMessage(t.Context(), MessageResult, document, nil, nil)
	require.NoError(t, err)
	pack, err := os.CreateTemp(t.TempDir(), "no-output-*.pack")
	require.NoError(t, err)
	t.Cleanup(func() { _ = pack.Close() })
	require.NoError(t, fixture.git.ExportCommitPack(t.Context(), rawResultOID, pack))
	proposal := ChoreographyResultProposal{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: 1, Node: "node-a", Branch: spec.Branch,
		ClaimOID: acceptedClaim.OID, InputsDigest: claim.InputsDigest,
		ProposalID: "no-output-result", ResultDigest: rawResultOID}
	proposal, err = PrepareChoreographyResult(t.Context(), proposal, pack, 1<<20)
	require.NoError(t, err)
	acceptedResult, err := broker.AcceptResult(t.Context(), proposal, pack)
	require.NoError(t, err)
	succeeded := status == StatusSucceeded
	require.NoError(t, broker.DecideAttempt(t.Context(), ChoreographyDecision{
		ChoreographyRunBinding: binding, Task: spec.Task, Branch: spec.Branch,
		Attempt: 1, Node: "node-a", ResultOID: acceptedResult.OID, Succeeded: succeeded}))
	terminalOID := remoteChoreographyOID(t, fixture, spec.Branch)
	command := exec.Command("git", "--git-dir", fixture.endpoint, "show",
		spec.Branch+":"+messageDir+"/"+string(choreographyDecisionKind)+".json")
	marked, err := command.CombinedOutput()
	require.NoError(t, err, "%s", marked)
	var decision ChoreographyDecision
	require.NoError(t, strictJSON(marked, &decision))
	require.Equal(t, succeeded, decision.Succeeded)
	require.Equal(t, acceptedResult.OID, decision.ResultOID)
	_, err = broker.StopClaims(t.Context())
	require.NoError(t, err)
	_, err = persist.SealRun(t.Context(), true)
	require.NoError(t, err)
	require.Equal(t, terminalOID, remoteChoreographyOID(t, fixture, spec.Branch),
		"sealing retains the completed no-output saga branch")
}

func TestChoreographyCancelledNoOutputTaskRetainsSignedAckBranch(t *testing.T) {
	fixture, persist, binding, spec := newChoreographyLedgerFixture(t)
	broker, err := RecoverFirstNodeBroker(t.Context(), choreographyFixtureBrokerOptions(persist))
	require.NoError(t, err)
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding,
		Task: spec.Task, Attempt: 1, Node: "node-a", Branch: spec.Branch,
		ExpectedOID: spec.ReservationOID, InputsDigest: spec.InputsDigest,
		ProposalID: "no-output-cancel-claim"}
	completeChoreographyFixtureClaim(t, persist, &claim)
	acceptedClaim, err := broker.AcceptClaim(t.Context(), claim)
	require.NoError(t, err)
	cancelOID, err := broker.CancelClaim(t.Context(), ChoreographyCancellation{
		ChoreographyRunBinding: binding, Task: spec.Task, Branch: spec.Branch,
		Attempt: 1, Node: "node-a", AssignmentOID: acceptedClaim.AssignmentOID,
		ClaimOID: acceptedClaim.OID})
	require.NoError(t, err)
	acked, err := broker.AcceptAck(t.Context(), ChoreographyAckProposal{
		ChoreographyRunBinding: binding, Task: spec.Task, Branch: spec.Branch,
		Attempt: 1, Node: "node-a", AssignmentOID: acceptedClaim.AssignmentOID,
		CancelOID: cancelOID, ProposalID: "no-output-cancel-ack",
		Phase: PhaseAuthorizationWait})
	require.NoError(t, err)
	require.NoError(t, broker.DecideAttempt(t.Context(), ChoreographyDecision{
		ChoreographyRunBinding: binding, Task: spec.Task, Branch: spec.Branch,
		Attempt: 1, Node: "node-a", ResultOID: acked.OID}))
	terminalOID := remoteChoreographyOID(t, fixture, spec.Branch)
	command := exec.Command("git", "--git-dir", fixture.endpoint, "show",
		spec.Branch+":"+messageDir+"/"+string(choreographyDecisionKind)+".json")
	marked, err := command.CombinedOutput()
	require.NoError(t, err, "%s", marked)
	var decision ChoreographyDecision
	require.NoError(t, strictJSON(marked, &decision))
	require.False(t, decision.Succeeded)
	require.Equal(t, acked.OID, decision.ResultOID,
		"cancelled task marks an accepted Ack instead of fabricating a Result")
	_, err = broker.StopClaims(t.Context())
	require.NoError(t, err)
	_, err = persist.SealRun(t.Context(), true)
	require.NoError(t, err)
	require.Equal(t, terminalOID, remoteChoreographyOID(t, fixture, spec.Branch))
}
