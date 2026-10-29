// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

const (
	resultPackTestMaximumBytes  = 64 << 20
	resultPackTestDocumentBytes = 1 << 20
	resultPackTestLargeBytes    = 17 << 20
	resultPackTestIndexExitCode = 73
	resultPackTestPushExitCode  = 74
	resultPackTestDirectoryMode = 0o700
	resultPackTestFileMode      = 0o600
	resultPackTestNode          = "node-a"
	resultPackTestTask          = "package:build"
	resultPackTestRoot          = "dist"
	resultPackTestFile          = "artifact.bin"
	resultPackTestCommandScript = `#!/bin/sh
printf '%s\n' "$*" >> "$DISPAT_TEST_PACK_COMMANDS"
case " $* " in
  *" index-pack "*) if [ "$DISPAT_TEST_PACK_FAIL" = index ]; then exit 73; fi ;;
  *" push "*) if [ "$DISPAT_TEST_PACK_FAIL" = push ]; then exit 74; fi ;;
esac
exec "$DISPAT_TEST_PACK_GIT" "$@"
`
)

type resultPackTestFixture struct {
	worker        *Worker
	task          *claimedTask
	broker        *FirstNodeBroker
	persist       *GitChoreographyPersist
	canonical     *mailboxFixture
	authorityRoot string
	report        Result
	submissions   int
	beforeSubmit  func(ChoreographyResultProposal, io.ReadSeeker)
}

func newResultPackTestFixture(t *testing.T, payloadBytes int) *resultPackTestFixture {
	t.Helper()
	authorityRoot := t.TempDir()
	require.NoError(t, os.Chmod(authorityRoot, resultPackTestDirectoryMode))
	authority, err := OpenNodeSlotAuthority(authorityRoot, resultPackTestNode, 1)
	require.NoError(t, err)
	limits := TransferLimits{MaxPackBytes: resultPackTestMaximumBytes, MaxManifestBytes: resultPackTestDocumentBytes, MaxBytes: resultPackTestMaximumBytes, MaxFiles: 10}
	canonical, persist, binding, spec := newChoreographyLedgerFixtureWithSettings(t, choreographyLedgerFixtureSettings{
		AuthorityID: authority.AuthorityID(), AdmissionSeconds: choreographyTestAdmissionSeconds,
		Tasks: []ChoreographyTaskSpec{{Task: resultPackTestTask}}, OutputRoots: []string{resultPackTestRoot}, Limits: limits})
	coordinator := &Coordinator{Run: binding.Run, Generation: binding.Generation, PlanDigest: binding.PlanDigest, Limits: limits}
	require.NoError(t, coordinator.RegisterCompiledChoreographyResults(persist.opts.Templates))
	persist.opts.VerifyResult = coordinator.VerifyChoreographyResult
	options := choreographyFixtureBrokerOptions(persist)
	options.Members[0].AuthorityID = authority.AuthorityID()
	broker, err := RecoverFirstNodeBroker(t.Context(), options)
	require.NoError(t, err)
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding, Task: spec.Task, Attempt: 1, Node: resultPackTestNode, Branch: spec.Branch,
		ExpectedOID: spec.ReservationOID, InputsDigest: spec.InputsDigest, PickupID: NewChoreographyPickupID(), ProposalID: "result-pack-claim"}
	completeChoreographyFixtureClaim(t, persist, &claim)
	peer := canonical.second(t)
	fixture := &resultPackTestFixture{broker: broker, persist: persist, canonical: canonical, authorityRoot: authorityRoot}
	worker := &Worker{Node: resultPackTestNode, Endpoint: peer.endpoint, StateDir: t.TempDir(), Mailbox: peer.mailbox, Cache: peer.git, ReturnSigner: peer.signer,
		NodeSlots: authority, Log: zerolog.Nop(), Report: NodeReport{AuthorityID: authority.AuthorityID(), Limits: limits, OS: "linux", Arch: "amd64", Dispat: "test"}}
	fixture.worker = worker
	channel := &workerReturnChannel{binding: binding, bootstrapOID: persist.opts.BootstrapOID,
		bootstrap: &RunBootstrap{Protocol: ChoreographyProtocolVersion, Mode: bootstrapCompilationModeName,
			ChoreographyRunBinding: binding, CoordinationEndpoint: peer.endpoint}}
	channel.client = &fakeWorkerReturn{result: func(proposal ChoreographyResultProposal, payload io.ReadSeeker) (ChoreographyReceipt, error) {
		fixture.submissions++
		if fixture.beforeSubmit != nil {
			fixture.beforeSubmit(proposal, payload)
		}
		receipt, err := broker.AcceptResult(t.Context(), proposal, payload.(*os.File))
		require.NoError(t, err)
		held, err := authority.Held(t.Context())
		require.NoError(t, err)
		require.Len(t, held, 1, "admission alone retains physical capacity")
		err = broker.DecideAttempt(t.Context(), ChoreographyDecision{ChoreographyRunBinding: binding, Task: proposal.Task, Branch: proposal.Branch,
			Attempt: proposal.Attempt, Node: proposal.Node, ResultOID: receipt.OID, Succeeded: true})
		require.NoError(t, err)
		return receipt, nil
	}, pollWithContext: func(ctx context.Context, request ChoreographyEventRequest) (ChoreographyEventPage, error) {
		return broker.ReadEvents(ctx, request.Cursor, request.MaxEvents)
	}}
	worker.returnChannels = map[string]*workerReturnChannel{binding.Run: channel}
	assignment := Assignment{Header: Header{Protocol: ChoreographyProtocolVersion, Kind: spec.Kind, Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Task: spec.Task, Attempt: 1, Node: resultPackTestNode, Branch: spec.Branch}, GraphDigest: binding.GraphDigest,
		GraphRootOID: persist.opts.GraphRootOID, BootstrapOID: persist.opts.BootstrapOID, Weight: spec.Weight}
	lease, available, err := worker.reserveNodeSlot(t.Context(), assignment, channel, claim)
	require.NoError(t, err)
	require.True(t, available)
	receipt, err := broker.AcceptClaim(t.Context(), claim)
	require.NoError(t, err)
	_, err = peer.mailbox.ObserveManyExact(t.Context(), []string{spec.Branch})
	require.NoError(t, err)
	view, err := peer.mailbox.OpenCarriedAssignment(t.Context(), receipt.AssignmentOID, resultPackTestDocumentBytes)
	require.NoError(t, err)
	fixture.task = &claimedTask{assignment: view.Assignment, tip: ChainTip{OID: receipt.AssignmentOID, Branch: spec.Branch}, claimed: receipt.OID, slotLease: lease, finalityCtx: t.Context()}
	_, err = worker.ensurePickupPending(t.Context(), fixture.task)
	require.NoError(t, err)
	payload := make([]byte, payloadBytes)
	_, err = rand.Read(payload)
	require.NoError(t, err)
	plumbing := gitx.NewPlumbing(peer.git)
	blob := plumbing.HashObject(t.Context(), bytes.NewReader(payload))
	folder := plumbing.MakeTree(t.Context(), []gitx.TreeEntry{{Name: resultPackTestFile, Type: "blob", Mode: gitx.TreeModeFile, OID: blob}})
	tree := plumbing.MakeTree(t.Context(), []gitx.TreeEntry{{Name: resultPackTestRoot, Type: "tree", Mode: gitx.TreeModeDir, OID: folder}})
	require.NoError(t, plumbing.Err())
	digest := sha256.Sum256(payload)
	manifest := &OutputManifest{Protocol: ProtocolVersion, Run: binding.Run, Generation: binding.Generation, PlanDigest: binding.PlanDigest, Task: spec.Task,
		Attempt: 1, Node: resultPackTestNode, Platform: Platform{OS: worker.Report.OS, Arch: worker.Report.Arch, Dispat: worker.Report.Dispat}, Roots: []string{resultPackTestRoot}, OutputTree: tree, Files: 1, Bytes: int64(payloadBytes),
		Entries: []ManifestEntry{{Path: resultPackTestRoot + "/" + resultPackTestFile, Type: EntryFile, Mode: EntryModeFile, Size: int64(payloadBytes), SHA256: hex.EncodeToString(digest[:])}}}
	manifest.Digest = CalculateManifestDigest(manifest)
	fixture.report = Result{Header: worker.formatReplyHeader(view.Assignment.Header), Assignment: receipt.OID, Status: StatusSucceeded, Platform: manifest.Platform, Outputs: manifest}
	return fixture
}

func recordResultPackGit(t *testing.T, failure string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Git command observer uses a POSIX executable")
	}
	realGit, err := exec.LookPath("git")
	require.NoError(t, err)
	directory := t.TempDir()
	log := filepath.Join(directory, "commands")
	require.NoError(t, os.WriteFile(filepath.Join(directory, "git"), []byte(resultPackTestCommandScript), 0o755))
	t.Setenv("DISPAT_TEST_PACK_GIT", realGit)
	t.Setenv("DISPAT_TEST_PACK_COMMANDS", log)
	t.Setenv("DISPAT_TEST_PACK_FAIL", failure)
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func TestWorkerResultPackReusesOnlyLargeBoundedExportsBeforePublication(t *testing.T) {
	for _, size := range []int{32, resultPackTestLargeBytes} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			fixture := newResultPackTestFixture(t, size)
			log := recordResultPackGit(t, "")
			fixture.beforeSubmit = func(proposal ChoreographyResultProposal, payload io.ReadSeeker) {
				tip, err := fixture.worker.pickupRemoteTip(t.Context(), proposal.OutcomeBranch)
				require.NoError(t, err)
				require.Equal(t, proposal.ResultDigest, tip, "submission follows exact durable outcome")
				raw, err := os.ReadFile(log)
				require.NoError(t, err)
				index := strings.Index(string(raw), " index-pack ")
				push := strings.Index(string(raw), " push ")
				require.GreaterOrEqual(t, push, 0)
				if size >= resultPackTestLargeBytes {
					require.GreaterOrEqual(t, index, 0)
					require.Less(t, index, push)
				} else {
					require.Equal(t, -1, index, "small results add no local indexing process")
				}
				require.Equal(t, 1, strings.Count(string(raw), " pack-objects "), "one prepared export is retained for submission")
				hash := sha256.New()
				n, err := io.Copy(hash, payload)
				require.NoError(t, err)
				require.Equal(t, proposal.PayloadBytes, n)
				require.Equal(t, proposal.PayloadSHA256, hex.EncodeToString(hash.Sum(nil)))
				_, err = payload.Seek(0, io.SeekStart)
				require.NoError(t, err)
			}
			oid, err := fixture.worker.returnTaskResult(t.Context(), fixture.task, fixture.report, carriedOutputs(fixture.report.Outputs))
			require.NoError(t, err)
			require.NotEmpty(t, oid)
			require.Equal(t, 1, fixture.submissions)
			held, err := fixture.worker.NodeSlots.Held(t.Context())
			require.NoError(t, err)
			require.Empty(t, held)
			files, err := filepath.Glob(filepath.Join(fixture.worker.StateDir, workerReturnResultPackFilePattern))
			require.NoError(t, err)
			require.Empty(t, files)
		})
	}
}

func TestPreparedResultPackGuardRejectsInvalidLargeDataDespiteExistingCommit(t *testing.T) {
	fixture := newWorkerPickupFixture(t)
	_, err := fixture.worker.ensurePickupPending(t.Context(), fixture.task)
	require.NoError(t, err)
	report, oid, err := fixture.worker.preparePickupOutcome(t.Context(), pickupOutcomeRequest{Task: fixture.task, Report: Result{Header: fixture.assignment.Header, Assignment: fixture.task.claimed, Status: StatusSucceeded}})
	require.NoError(t, err)
	for _, size := range []int64{workerResultPackIndexMinimumBytes - 1, workerResultPackIndexMinimumBytes, workerResultPackIndexMinimumBytes + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			payload, err := os.CreateTemp(t.TempDir(), "pack")
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, payload.Close()) })
			require.NoError(t, payload.Truncate(size))
			pack := workerResultPack{Result: report, OID: oid, Payload: payload, MaxBytes: resultPackTestMaximumBytes, ExportedBytes: size}
			err = fixture.worker.indexPreparedResultPack(t.Context(), pack)
			if size < workerResultPackIndexMinimumBytes {
				require.NoError(t, err)
			} else {
				require.Error(t, err, "preexisting ResultOID cannot make malformed pack bytes acceptable")
			}
			pack.ExportedBytes--
			require.ErrorContains(t, fixture.worker.indexPreparedResultPack(t.Context(), pack), workerResultPackSizeError)
			pack.ExportedBytes = size
			pack.MaxBytes = size - 1
			require.ErrorContains(t, fixture.worker.indexPreparedResultPack(t.Context(), pack), workerResultPackSizeError)
			if size >= workerResultPackIndexMinimumBytes {
				pack.MaxBytes = resultPackTestMaximumBytes
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				require.ErrorIs(t, fixture.worker.indexPreparedResultPack(ctx, pack), context.Canceled)
			}
		})
	}
}

func TestWorkerResultPackPreparationFailureKeepsPickupUnresolvedAcrossGC(t *testing.T) {
	for _, failure := range []string{"oversized", "index", "push", "push after index"} {
		t.Run(failure, func(t *testing.T) {
			size := 32
			if failure == "index" || failure == "push after index" {
				size = resultPackTestLargeBytes
			}
			fixture := newResultPackTestFixture(t, size)
			injection := failure
			if failure == "push after index" {
				injection = "push"
			}
			log := recordResultPackGit(t, injection)
			if failure == "oversized" {
				fixture.worker.Report.Limits.MaxPackBytes = 1
			}
			_, returnErr := fixture.worker.returnTaskResult(t.Context(), fixture.task, fixture.report, carriedOutputs(fixture.report.Outputs))
			require.Error(t, returnErr)
			require.Zero(t, fixture.submissions)
			if failure == "oversized" {
				require.ErrorIs(t, returnErr, gitx.ErrTransportLimit)
			} else {
				var exit *exec.ExitError
				require.ErrorAs(t, returnErr, &exit, "the injected process failure must be the returned cause")
				if injection == "index" {
					require.Equal(t, resultPackTestIndexExitCode, exit.ExitCode())
				} else {
					require.Equal(t, resultPackTestPushExitCode, exit.ExitCode())
				}
			}
			state, found, err := fixture.worker.readPickupState(fixture.task.slotLease.ID)
			require.NoError(t, err)
			require.True(t, found)
			require.NotEmpty(t, state.ResultOID)
			raw, err := os.ReadFile(log)
			require.NoError(t, err)
			index := strings.Index(string(raw), " index-pack ")
			push := strings.Index(string(raw), " push ")
			if size >= resultPackTestLargeBytes {
				require.GreaterOrEqual(t, index, 0, "the large result reached local indexing")
			} else {
				require.Equal(t, -1, index, "small and oversized results did not reach local indexing")
			}
			if injection == "push" {
				require.GreaterOrEqual(t, push, 0, "the injected durable publication was attempted")
				if failure == "push after index" {
					require.Less(t, index, push, "local indexing completed before the injected push failure")
				}
			} else {
				require.Equal(t, -1, push, "preparation failures cannot publish an outcome")
			}
			tip, err := fixture.worker.pickupRemoteTip(t.Context(), state.OutcomeBranch)
			require.NoError(t, err)
			require.Empty(t, tip)
			held, err := fixture.worker.NodeSlots.Held(t.Context())
			require.NoError(t, err)
			require.Equal(t, []NodeSlotLease{fixture.task.slotLease}, held)
			files, err := filepath.Glob(filepath.Join(fixture.worker.StateDir, workerReturnResultPackFilePattern))
			require.NoError(t, err)
			require.Empty(t, files)
			t.Setenv("DISPAT_TEST_PACK_FAIL", "")
			require.NoError(t, fixture.worker.Cache.CollectGarbage(t.Context()))
			_, err = fixture.worker.Cache.ObjectType(t.Context(), state.ResultOID)
			require.Error(t, err, "unpublished result can become unavailable after idle GC")
			reopened, err := OpenNodeSlotAuthority(fixture.authorityRoot, resultPackTestNode, 1)
			require.NoError(t, err)
			fixture.worker.NodeSlots = reopened
			require.NoError(t, fixture.worker.recoverHeldNodeSlots(t.Context(), state.Run))
			held, err = reopened.Held(t.Context())
			require.NoError(t, err)
			require.Equal(t, []NodeSlotLease{fixture.task.slotLease}, held, "cold signed history has no terminal that could discharge this pickup")
			retained, found, err := fixture.worker.readPickupState(fixture.task.slotLease.ID)
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, state, retained)
			_, _, err = fixture.worker.publishPickupOutcome(t.Context(), pickupOutcomeRequest{Task: fixture.task, Report: fixture.report, Carried: carriedOutputs(fixture.report.Outputs)})
			require.Error(t, err, "missing retained objects cause conservative refusal, not a replacement task or result")
			held, err = reopened.Held(t.Context())
			require.NoError(t, err)
			require.Len(t, held, 1)
			page, err := fixture.broker.ReadEvents(t.Context(), ChoreographyEventCursor{}, choreographyLedgerPageMaxEvents)
			require.NoError(t, err)
			require.False(t, page.More)
			for _, event := range page.Events {
				require.NotEqual(t, ChoreographyTerminal, event.Kind)
				require.NotEqual(t, ChoreographyResultAccepted, event.Kind)
			}
		})
	}
}

func TestWorkerPreparedResultRetainsExactIdentityAcrossRestartAndCancellation(t *testing.T) {
	for _, cancellation := range []bool{false, true} {
		t.Run(fmt.Sprint(cancellation), func(t *testing.T) {
			fixture := newResultPackTestFixture(t, 32)
			prepared, oid, err := fixture.worker.preparePickupOutcome(t.Context(), pickupOutcomeRequest{
				Task: fixture.task, Report: fixture.report, Carried: carriedOutputs(fixture.report.Outputs)})
			require.NoError(t, err)
			tip, err := fixture.worker.pickupRemoteTip(t.Context(), prepared.OutcomeBranch)
			require.NoError(t, err)
			require.Empty(t, tip, "preparation alone does not publish")
			authority, err := OpenNodeSlotAuthority(fixture.authorityRoot, resultPackTestNode, 1)
			require.NoError(t, err)
			original := fixture.worker
			restarted := &Worker{Node: original.Node, Endpoint: original.Endpoint, StateDir: original.StateDir,
				Mailbox: original.Mailbox, Cache: original.Cache, ReturnSigner: original.ReturnSigner,
				NodeSlots: authority, Log: original.Log, Report: original.Report, returnChannels: original.returnChannels}
			changed := fixture.report
			changed.Reason = "different result after restart"
			_, _, err = restarted.preparePickupOutcome(t.Context(), pickupOutcomeRequest{
				Task: fixture.task, Report: changed, Carried: carriedOutputs(changed.Outputs)})
			require.ErrorContains(t, err, workerPickupResultMismatchError)
			if cancellation {
				require.NoError(t, restarted.recordCancelledPickup(t.Context(), fixture.task))
				require.Zero(t, fixture.submissions)
			} else {
				_, err = restarted.returnTaskResult(t.Context(), fixture.task, fixture.report, carriedOutputs(fixture.report.Outputs))
				require.NoError(t, err)
				require.Equal(t, 1, fixture.submissions)
			}
			tip, err = restarted.pickupRemoteTip(t.Context(), prepared.OutcomeBranch)
			require.NoError(t, err)
			require.Equal(t, oid, tip, "restart and cancellation must publish the originally prepared result")
			state, found, err := restarted.readPickupState(fixture.task.slotLease.ID)
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, oid, state.ResultOID)
			held, err := authority.Held(t.Context())
			require.NoError(t, err)
			if cancellation {
				require.Equal(t, []NodeSlotLease{fixture.task.slotLease}, held, "publication without signed settlement does not discharge capacity")
			} else {
				require.Empty(t, held)
			}
		})
	}
}
