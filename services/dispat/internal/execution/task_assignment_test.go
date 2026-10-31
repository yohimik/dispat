// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"io"
	"os/exec"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
	"github.com/yohimik/dispat/services/dispat/internal/script"
)

const (
	claimAssignmentTestTimeout        = 30 * time.Second
	claimAssignmentTestProvider       = "provider"
	claimAssignmentTestProviderTask   = "provider:build"
	claimAssignmentTestConsumer       = "consumer"
	claimAssignmentTestInputPath      = "providers/lib"
	claimAssignmentTestOutputRoot     = "dist"
	claimAssignmentTestOutputFile     = "dist/asset.txt"
	claimAssignmentTestOutputBody     = "verified"
	claimAssignmentTestFileMode       = 0o600
	claimAssignmentTestObjectBytes    = 1 << 20
	claimAssignmentTestAttempt        = 1
	claimAssignmentTestNode           = "node-a"
	claimAssignmentTestExport         = "ASSET"
	claimAssignmentTestExportRelative = "asset.txt"
	claimAssignmentTestClaimOID       = "claim-oid"
	claimAssignmentTestCancelOID      = "cancel-oid"
	claimAssignmentTestStageMessage   = "stage started"
	claimAssignmentTestCommand        = `set -eu; printf 'CONSUMED=%s\n' "$(cat "$DISPAT_OUTPUT_ASSET")" >> "$DISPAT_OUTPUT"`
	claimAssignmentTestExportName     = "CONSUMED"
	claimAssignmentTestExportSource   = "consumer:build"
	claimAssignmentTestSuccessCase    = "success"
	claimAssignmentTestCancelledCase  = "cancelled before command"
	claimAssignmentTestContinues      = false
	claimAssignmentTestCancels        = true
	claimAssignmentTestShellIndex     = 0
	claimAssignmentTestShellMissing   = "%s not available"
)

func TestTaskKeepsRegisteredAssignmentImmutableWhileRunningRehomedExports(t *testing.T) {
	shell := script.DefaultShell()[claimAssignmentTestShellIndex]
	if _, err := exec.LookPath(shell); err != nil {
		t.Skipf(claimAssignmentTestShellMissing, shell)
	}
	for name, isCancelled := range map[string]bool{
		claimAssignmentTestSuccessCase:   claimAssignmentTestContinues,
		claimAssignmentTestCancelledCase: claimAssignmentTestCancels,
	} {
		t.Run(name, func(t *testing.T) {
			fixture, persist, binding, spec := newChoreographyLedgerFixture(t)
			outputs := newOutputFixture(t, claimAssignmentTestProvider)
			outputs.write(t, claimAssignmentTestOutputFile, claimAssignmentTestOutputBody, claimAssignmentTestFileMode)
			manifest, err := outputs.capture(t, []string{claimAssignmentTestOutputRoot}, testLimits)
			require.NoError(t, err)
			manifest.Run, manifest.PlanDigest = binding.Run, binding.PlanDigest
			manifest.Generation, manifest.Task = binding.Generation, claimAssignmentTestProviderTask
			manifest.Package = claimAssignmentTestProvider
			manifest.Digest = CalculateManifestDigest(manifest)
			require.NoError(t, fixture.git.ImportObjectFrom(t.Context(), outputs.dir, manifest.OutputTree, claimAssignmentTestObjectBytes))
			result := Result{Header: Header{Protocol: ChoreographyProtocolVersion, Kind: KindBuild,
				Run: binding.Run, Generation: binding.Generation, PlanDigest: binding.PlanDigest,
				Task: claimAssignmentTestProviderTask, Attempt: claimAssignmentTestAttempt, Node: claimAssignmentTestNode},
				Status: StatusSucceeded, Outputs: manifest}
			resultOID, err := fixture.mailbox.commitMessage(t.Context(), MessageResult, mustMarshal(t, result), nil, nil)
			require.NoError(t, err)
			bootstrap, err := fixture.mailbox.ReadChoreographyBootstrap(t.Context(), persist.opts.BootstrapOID,
				claimAssignmentTestObjectBytes, claimAssignmentTestObjectBytes)
			require.NoError(t, err)
			assignment := Assignment{Header: Header{Protocol: ChoreographyProtocolVersion, Kind: KindBuild,
				Run: binding.Run, Generation: binding.Generation, PlanDigest: binding.PlanDigest,
				Task: spec.Task, Attempt: claimAssignmentTestAttempt, Node: claimAssignmentTestNode, Branch: spec.Branch},
				GraphDigest: binding.GraphDigest, BootstrapOID: persist.opts.BootstrapOID,
				Weight: spec.Weight, Limits: testLimits, Repositories: bootstrap.PinnedInputs.Repositories,
				Package: &AssignmentPackage{Name: claimAssignmentTestConsumer},
				Frame:   &AssignmentFrame{Commands: []string{claimAssignmentTestCommand}},
				Inputs: []AssignmentInput{{Task: claimAssignmentTestProviderTask, Package: claimAssignmentTestProvider,
					Path: claimAssignmentTestInputPath, Commit: resultOID, Digest: manifest.Digest}},
				Exports: []ExportedValue{{Name: claimAssignmentTestExport, DeclaredPath: &ExportedDeclaredPath{
					Package: claimAssignmentTestProvider, Root: claimAssignmentTestOutputRoot, Relative: claimAssignmentTestExportRelative}}}}
			assignmentOID, err := fixture.mailbox.PrepareAssignmentOnReservation(t.Context(), &assignment, spec.ReservationOID)
			require.NoError(t, err)
			signedAssignment := mustMarshal(t, assignment)
			worker := &Worker{Node: claimAssignmentTestNode, StateDir: t.TempDir(), Mailbox: fixture.mailbox, Cache: fixture.git}
			ctx, stop := context.WithTimeout(t.Context(), claimAssignmentTestTimeout)
			defer stop()
			task := &claimedTask{assignment: assignment, tip: ChainTip{OID: assignmentOID, Branch: spec.Branch},
				claimed: claimAssignmentTestClaimOID, stop: stop}
			forget := worker.registerClaim(task)
			defer forget()
			frameReady, resumeFrame, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			log := zerolog.New(io.Discard).Hook(zerolog.HookFunc(func(_ *zerolog.Event, _ zerolog.Level, message string) {
				if message != claimAssignmentTestStageMessage {
					return
				}
				close(frameReady)
				select {
				case <-resumeFrame:
				case <-ctx.Done():
				}
			}))
			var outcome taskOutcome
			go func() {
				defer close(done)
				outcome = worker.runTask(ctx, task, log)
			}()
			t.Cleanup(func() { stop(); <-done })
			select {
			case <-frameReady:
			case <-done:
				t.Fatalf("task ended before its frame: %+v", outcome)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			require.JSONEq(t, string(signedAssignment), string(mustMarshal(t, task.assignment)))
			retired, err := worker.retireSealedCompiledRun(ctx, &workerCompiledRun{
				binding: binding, frontier: &ChoreographyFrontier{sealed: true}})
			require.NoError(t, err)
			require.False(t, retired, "the original registered claim still fences run retirement")
			_, phase, isCommandStarted := task.readProgress()
			require.Equal(t, release.PartCommands, phase)
			require.True(t, isCommandStarted, "the frame shares the registered claim's progress")
			if isCancelled {
				require.True(t, task.withdraw(claimAssignmentTestCancelOID))
				task.stop()
			}
			close(resumeFrame)
			<-done
			require.JSONEq(t, string(signedAssignment), string(mustMarshal(t, task.assignment)))
			if isCancelled {
				require.Equal(t, StatusFailed, outcome.status)
				require.Equal(t, release.PartCommands, outcome.failedPart)
				require.Empty(t, outcome.exports)
				return
			}
			require.Equal(t, StatusSucceeded, outcome.status)
			require.Equal(t, []plan.Output{{Name: claimAssignmentTestExportName,
				Value: claimAssignmentTestOutputBody, Source: claimAssignmentTestExportSource}}, outcome.exports)
		})
	}
}
