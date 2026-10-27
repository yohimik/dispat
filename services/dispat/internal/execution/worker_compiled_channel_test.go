// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

const workerCompiledChannelTestNode = "node-a"
const workerCompiledChannelTestCapacity = 2
const workerCompiledChannelCloseError = "private interpreted plan close failed"

func newWorkerCompiledChannelTestNotice(t *testing.T, mode string) (*Worker, gitx.RemoteHead, SlotNotice) {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.Chmod(root, 0o700))
	slots, err := OpenNodeSlotAuthority(root, workerCompiledChannelTestNode, workerCompiledChannelTestCapacity)
	require.NoError(t, err)
	settings := choreographyLedgerFixtureSettings{AuthorityID: slots.AuthorityID(),
		AdmissionSeconds: choreographyTestAdmissionSeconds, Mode: mode}
	if mode == bootstrapInterpretationModeName {
		settings.MaxAttempts = choreographyAttemptPolicyFirstAttempt
	}
	fixture, persist, binding, _ := newChoreographyLedgerFixtureWithSettings(t,
		settings)
	branch := FormatSlotBranch(workerCompiledChannelTestNode, binding.Run, 0)
	notice := SlotNotice{Header: Header{Protocol: ChoreographyProtocolVersion,
		Kind: KindSlot, Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Node: workerCompiledChannelTestNode, Branch: branch},
		BootstrapOID: persist.opts.BootstrapOID, GraphRootOID: persist.opts.GraphRootOID,
		Sequence: choreographyActivatedSlotSequence}
	worker := &Worker{Node: workerCompiledChannelTestNode, Endpoint: fixture.endpoint, Mailbox: fixture.mailbox,
		NodeSlots: slots, ReturnSigner: fixture.signer, Report: NodeReport{AuthorityID: slots.AuthorityID(),
			Capacity: workerCompiledChannelTestCapacity, OS: "linux", Arch: "amd64",
			Limits: TransferLimits{MaxManifestBytes: 1 << 20, MaxPackBytes: 1 << 20}}}
	return worker, gitx.RemoteHead{Name: branch}, notice
}

func TestWorkerDoesNotRememberReturnChannelBeforeSignedRosterValidation(t *testing.T) {
	worker, head, notice := newWorkerCompiledChannelTestNotice(t, bootstrapCompilationModeName)
	_, err := worker.handleCompiledNotice(t.Context(), head, notice)
	require.ErrorContains(t, err, compiledNoticeIdentityError)
	require.Empty(t, worker.compiledRuns)
	require.Empty(t, worker.returnChannels,
		"a roster mismatch must not retain an unauthenticated return channel")
}

func TestWorkerReturnsPrivatePlannerCloseFailureBeforeInstallingRun(t *testing.T) {
	worker, head, notice := newWorkerCompiledChannelTestNotice(t, bootstrapInterpretationModeName)
	closeErr := errors.New(workerCompiledChannelCloseError)
	worker.PrepareInterpretedRun = func(context.Context, InterpretedRunOptions) (*InterpretedRun, error) {
		return &InterpretedRun{Close: func() error { return closeErr }}, nil
	}
	_, err := worker.handleCompiledNotice(t.Context(), head, notice)
	require.ErrorContains(t, err, interpretedRunMismatchError)
	require.ErrorIs(t, err, closeErr)
	require.Empty(t, worker.compiledRuns)
	require.Empty(t, worker.returnChannels)
}
