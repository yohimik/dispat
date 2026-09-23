// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// Preflight against real mailboxes, answered by a node the test plays.
//
// Every claim here is about what a run does before it dispatches anything, so
// the fixtures are the real thing: an assignment really is pushed to a real
// bare repository, and the answer is written by a hand-rolled node that can
// say whatever the scenario needs, including things no dispat would say.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// coordinatorFixture is one run with one configured node, and the second
// party's mailbox to answer it with.
type coordinatorFixture struct {
	orchestrator *mailboxFixture
	node         *mailboxFixture
	coordinator  *Coordinator
}

// The preflight bounds the fixtures give a run. An answered probe returns the
// moment the node's report arrives, so its bound only has to outlast the
// answer: several git subprocesses, which a loaded runner has been seen to
// take past two seconds over, and it matches the fake node's own patience in
// answer. A probe nothing valid answers is waited out in full, so its bound
// stays short.
const (
	answeredPreflight = 20 * time.Second
	silentPreflight   = 2 * time.Second
)

func newCoordinatorFixture(t *testing.T, limits TransferLimits, preflight time.Duration) *coordinatorFixture {
	t.Helper()
	orchestrator := newMailboxFixture(t)
	node := orchestrator.second(t)
	links := []Link{{Name: "build-a", Endpoint: orchestrator.endpoint}}
	coordinator := NewCoordinator("run-1", "digest", "generation",
		LocalNode{Name: "here", Capacity: 1}, links,
		map[string]*GitMailbox{"build-a": orchestrator.mailbox}, orchestrator.signer,
		Timeouts{Preflight: preflight}, limits, zerolog.Nop())
	return &coordinatorFixture{orchestrator: orchestrator, node: node, coordinator: coordinator}
}

// answer plays the node: it waits for the probe, claims it and reports
// whatever the scenario says, signed with the secret the scenario chose.
func (f *coordinatorFixture) answer(t *testing.T, signer *Signer, report func(Assignment, string) any) {
	t.Helper()
	mailbox := NewGitMailbox(f.node.endpoint, f.node.git, signer, zerolog.Nop())
	go func() {
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			heads, err := mailbox.Observe(context.Background(), FormatBranchPattern("build-a"))
			if err != nil || len(heads) == 0 {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			tip, err := mailbox.Inspect(context.Background(), heads[0])
			if err != nil || tip.Kind != MessageAssignment {
				continue
			}
			// The document is read with the run's own secret, because a node
			// answering with another one still has to know what it answers.
			document, err := f.orchestrator.mailbox.Read(context.Background(), tip, 1<<20)
			if err != nil {
				return
			}
			var assignment Assignment
			if err := json.Unmarshal(document, &assignment); err != nil {
				return
			}
			claim, err := mailbox.Advance(context.Background(), tip.Branch, tip.OID, MessageClaim,
				mustMarshalValue(Claim{Header: assignment.Header, Assignment: tip.OID}), nil)
			if err != nil {
				return
			}
			_, _ = mailbox.Advance(context.Background(), tip.Branch, claim, MessageResult,
				mustMarshalValue(report(assignment, tip.OID)), nil)
			return
		}
	}()
}

func mustMarshalValue(message any) []byte {
	document, _ := json.Marshal(message)
	return document
}

// healthyReport is what a node that can take this run's work answers.
func healthyReport(limits TransferLimits) func(Assignment, string) any {
	return func(assignment Assignment, offered string) any {
		return Result{
			Header:     replyHeader(assignment),
			Assignment: offered, Status: StatusSucceeded,
			Report: &NodeReport{Protocol: ProtocolVersion, Dispat: "1.11.0",
				OS: "linux", Arch: "amd64", Capacity: 2, Limits: limits},
		}
	}
}

// replyHeader is the header a node writes back, which is the assignment's own
// with this moment on it.
func replyHeader(assignment Assignment) Header {
	header := assignment.Header
	header.IssuedAt = time.Now().UTC().Format(time.RFC3339)
	return header
}

// TestPreflightAcceptsANodeThatCanTakeTheWork: a healthy node answers, the
// run learns what it is, and the branch the probe travelled on is the run's
// to close.
func TestPreflightAcceptsANodeThatCanTakeTheWork(t *testing.T) {
	limits := TransferLimits{MaxFiles: 10, MaxBytes: 20, MaxManifestBytes: 1 << 20}
	fixture := newCoordinatorFixture(t, limits, answeredPreflight)
	fixture.answer(t, fixture.node.signer, healthyReport(limits))

	err := fixture.coordinator.Preflight(t.Context(),
		[]PackagePlatforms{{Package: "core", Platforms: []string{"linux/amd64"}}})

	require.NoError(t, err)
	assert.Len(t, fixture.orchestrator.remoteBranches(t), 1, "the probe branch is still there to be closed")

	require.NoError(t, fixture.coordinator.Close(t.Context()))
	assert.Empty(t, fixture.orchestrator.remoteBranches(t), "and the run closes what it created")
	require.NoError(t, fixture.coordinator.Close(t.Context()), "closing a run that owns nothing is nothing")
}

// TestClosePreservesUnknownPublicationEvidence: a run whose publisher never
// reported back leaves that attempt's branch for reconciliation, while its
// unrelated temporary branch is cleaned normally.
func TestClosePreservesUnknownPublicationEvidence(t *testing.T) {
	mailbox := newMailboxFixture(t)
	coordinator := &Coordinator{Run: "run-1", Log: zerolog.Nop(),
		mailboxes: map[string]*GitMailbox{"build-a": mailbox.mailbox},
		owned:     map[string][]gitx.BranchLease{}}
	unknownBranch := FormatBranch("build-a", KindPublish, time.Now())
	cleanBranch := FormatBranch("build-a", KindProbe, time.Now())
	for _, branch := range []string{unknownBranch, cleanBranch} {
		oid, err := mailbox.mailbox.Assign(t.Context(), probeAssignment("build-a", branch))
		require.NoError(t, err)
		coordinator.recordOwnedRef(t.Context(), ownedRefStep{node: "build-a", branch: branch, oid: oid})
	}
	coordinator.rememberUnknownPublication(unknownPublication{
		Task: "pkg:publish", Attempt: 1, Node: "build-a", Branch: unknownBranch})

	require.NoError(t, coordinator.Close(t.Context()))
	assert.Equal(t, []string{unknownBranch}, mailbox.remoteBranches(t),
		"the authorization evidence stays reachable while unrelated refs are removed")
}

// TestPreflightRefusesBeforeAnythingIsDispatched: every way a node can fail
// the check, each one a refusal naming the node, carrying the configuration
// code and the class a machine switches on.
func TestPreflightRefusesBeforeAnythingIsDispatched(t *testing.T) {
	limits := TransferLimits{MaxFiles: 10, MaxBytes: 20, MaxManifestBytes: 1 << 20}

	for name, tc := range map[string]struct {
		report   func(Assignment, string) any
		secret   string
		packages []PackagePlatforms
		says     string
	}{
		"a node that never answers": {
			says: "did not pass preflight"},
		"a node signing with another secret": {
			report: healthyReport(limits), secret: "hunter3", says: "did not pass preflight"},
		"a node speaking another protocol version": {
			report: func(assignment Assignment, offered string) any {
				result := healthyReport(limits)(assignment, offered).(Result)
				result.Report.Protocol = ProtocolVersion + 1
				return result
			},
			says: "protocol"},
		"a node with no capacity": {
			report: func(assignment Assignment, offered string) any {
				result := healthyReport(limits)(assignment, offered).(Result)
				result.Report.Capacity = 0
				return result
			},
			says: "capacity"},
		"a node that would move fewer bytes than this run transfers": {
			report: func(assignment Assignment, offered string) any {
				return healthyReport(TransferLimits{MaxFiles: 10, MaxBytes: 5, MaxManifestBytes: 1 << 20})(assignment, offered)
			},
			says: "transfer.maxBytes"},
		"a node that would move fewer files than this run transfers": {
			report: func(assignment Assignment, offered string) any {
				return healthyReport(TransferLimits{MaxFiles: 1, MaxBytes: 20, MaxManifestBytes: 1 << 20})(assignment, offered)
			},
			says: "transfer.maxFiles"},
		"a package no configured node can build": {
			report:   healthyReport(limits),
			packages: []PackagePlatforms{{Package: "core", Platforms: []string{"plan9/mips"}}},
			says:     "buildPlatforms"},
	} {
		t.Run(name, func(t *testing.T) {
			// A report signed with the run's secret is answered; no report,
			// or one the run cannot verify, is waited out.
			preflight := silentPreflight
			if tc.report != nil && tc.secret == "" {
				preflight = answeredPreflight
			}
			fixture := newCoordinatorFixture(t, limits, preflight)
			if tc.report != nil {
				signer := fixture.node.signer
				if tc.secret != "" {
					other, err := NewSigner(tc.secret)
					require.NoError(t, err)
					signer = other
				}
				fixture.answer(t, signer, tc.report)
			}

			err := fixture.coordinator.Preflight(t.Context(), tc.packages)

			require.Error(t, err)
			assert.Equal(t, config.DiagnosticExecution, config.DiagnosticCode(err))
			assert.Equal(t, CategoryConfiguration, DiagnosticCategory(err))
			assert.Contains(t, err.Error(), tc.says)
			// The refusal names the work it is about, so a log line can be
			// read back to the node and the run it happened in.
			assert.Equal(t, "run-1", identityOf(err).Run)
		})
	}
}

// identityOf is the work an error names itself against.
func identityOf(err error) Identity {
	var diagnostic *Diagnostic
	if !errors.As(err, &diagnostic) {
		return Identity{}
	}
	return diagnostic.DiagnosticIdentity()
}

// TestPreflightReportsARetainedBranch: a branch another party took over
// cannot be closed by this run, and that is a warning rather than a failed
// release, because a coordination branch carries no release record.
func TestPreflightReportsARetainedBranch(t *testing.T) {
	fixture := newCoordinatorFixture(t, TransferLimits{MaxManifestBytes: 1 << 20}, silentPreflight)
	branch := FormatBranch("build-a", KindProbe, time.Now())
	_, err := fixture.orchestrator.mailbox.Assign(t.Context(), probeAssignment("build-a", branch))
	require.NoError(t, err)
	// The run believes the branch is somewhere it is not, which is what a
	// branch somebody else advanced looks like from here.
	fixture.coordinator.recordOwnedRef(t.Context(), ownedRefStep{
		node: "build-a", branch: branch, oid: "0000000000000000000000000000000000000000",
	})

	err = fixture.coordinator.Close(t.Context())

	require.Error(t, err)
	assert.Equal(t, CodeTransportRetained, config.DiagnosticCode(err))
	assert.Equal(t, CategoryTransportCleanup, DiagnosticCategory(err))
	assert.Len(t, fixture.orchestrator.remoteBranches(t), 1)
}

// TestPlatformsAreSatisfiedByThePool: a package is placed on whichever
// compatible node is free, so the question is asked of the whole pool.
func TestPlatformsAreSatisfiedByThePool(t *testing.T) {
	linux := &NodeReport{OS: "linux", Arch: "amd64"}
	darwin := &NodeReport{OS: "darwin", Arch: "arm64"}

	assert.True(t, linux.IsPlatformSatisfied(nil), "a package that requires nothing runs anywhere")
	assert.True(t, linux.IsPlatformSatisfied([]string{"darwin/arm64", "linux/amd64"}))
	assert.False(t, linux.IsPlatformSatisfied([]string{"darwin/arm64"}))

	coordinator := &Coordinator{}
	assert.True(t, coordinator.isPlacementPossible([]string{"darwin/arm64"}, []*NodeReport{linux, darwin}))
	assert.False(t, coordinator.isPlacementPossible([]string{"plan9/mips"}, []*NodeReport{linux, darwin}))
	assert.False(t, coordinator.isPlacementPossible(nil, nil),
		"and a pool with no node in it satisfies nothing at all")
}
