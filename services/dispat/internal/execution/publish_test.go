// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// The decisions a delegated publication is made of, asked of the pure
// functions that make them. What travels, how long an authorization means
// anything, which object the terminal message is written on top of, and which
// node a publisher would rather run on.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// TestPublicationCarriesNoDeclaredOutputs: a publication consumes the bytes a
// build already produced and describes none of its own, so the assignment asks
// for nothing back. A build of the same package asks for exactly what the
// configuration declared.
func TestPublicationCarriesNoDeclaredOutputs(t *testing.T) {
	request := release.StageRequest{Release: &plan.Release{
		Pkg: &model.Package{Name: "core", Space: &model.Space{BuildOutputs: []string{"dist"}}},
	}}

	assert.Equal(t, []string{"dist"}, resolveDeclaredOutputs(KindBuild, request))
	assert.Equal(t, []string{"dist"}, resolveDeclaredOutputs(KindPrepare, request))
	assert.Nil(t, resolveDeclaredOutputs(KindPublish, request),
		"a publisher describes no output set of its own")
}

// TestEveryAssignmentBoundsItself: the node enforces the run's own wait,
// whatever the work is. A publication that outlived it would be acting on an
// authorization the run has written off, and a build that outlived it would be
// holding a machine for a run that is no longer listening: an orchestrator
// whose network went away can end neither, so the bound travels and the node
// applies it to every kind (§28.6).
func TestEveryAssignmentBoundsItself(t *testing.T) {
	assert.Equal(t, 900, resolveTaskDeadlineSeconds(15*time.Minute))
	assert.Equal(t, 0, resolveTaskDeadlineSeconds(0))
}

// TestAnAuthorizationExpires: an authorization is a statement about an
// instant, so a publisher acts on it only while it is current, and never at
// all on one that states no expiry or states nonsense.
func TestAnAuthorizationExpires(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	for name, row := range map[string]struct {
		notAfter string
		want     bool
	}{
		"still current":      {notAfter: now.Add(time.Minute).Format(time.RFC3339), want: true},
		"already passed":     {notAfter: now.Add(-time.Second).Format(time.RFC3339), want: false},
		"exactly at the end": {notAfter: now.Format(time.RFC3339), want: false},
		"stated as nothing":  {notAfter: "", want: false},
		"not a timestamp":    {notAfter: "soon", want: false},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, row.want, isAuthorizationUnexpired(row.notAfter, now))
		})
	}
}

// TestTheAuthorizationWaitIsTheRunsOwn: a publication shares the deadline
// that started when its assignment was claimed, including time spent in the
// hook. Only an assignment with no deadline needs a publication-gate fallback.
func TestTheAuthorizationWaitIsTheRunsOwn(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	waiting, stop := resolveAuthorizationContext(parent, 90)
	defer stop()
	assert.True(t, waiting == parent, "a stated wait must not start a second timer")
	for _, seconds := range []int{0, -1} {
		waiting, stop := resolveAuthorizationContext(context.Background(), seconds)
		deadline, bounded := waiting.Deadline()
		require.True(t, bounded, "an unbounded assignment needs a fallback")
		assert.InDelta(t, defaultAuthorizationWait.Seconds(), time.Until(deadline).Seconds(), 2)
		stop()
	}
}

// TestTheResultLeaseFollowsTheHandshake: a task that moved its branch no
// further writes its result over the claim, and a publication writes it over
// whatever the handshake left there. A lease over the wrong object would be a
// finished task the run never hears about.
func TestTheResultLeaseFollowsTheHandshake(t *testing.T) {
	assert.Equal(t, "claim-oid", resolveResultLease("claim-oid", taskOutcome{}))
	assert.Equal(t, "go-oid", resolveResultLease("claim-oid", taskOutcome{expectedTip: "go-oid"}))
}

// TestAPublisherWithoutThePermitIsRefused: the permits are the assignment's
// own statement of what a node may do beyond running commands, so a publish
// task that does not authorize publication is refused before it is claimed.
func TestAPublisherWithoutThePermitIsRefused(t *testing.T) {
	worker := &Worker{Node: "build-a", Seen: newSeenSetForTest(t)}
	branch := "dispat-worker-build-a-20260922-publish-abc"
	header := Header{Protocol: ProtocolVersion, Kind: KindPublish, Run: "run-1",
		Node: "build-a", Branch: branch, IssuedAt: time.Now().UTC().Format(time.RFC3339)}

	assert.Equal(t, ReasonPermit,
		worker.checkAssignment(Assignment{Header: header}, ChainTip{Branch: branch}))
	assert.Empty(t, worker.checkAssignment(
		Assignment{Header: header, Permits: AssignmentPermits{Publish: true}},
		ChainTip{Branch: branch}), "the same assignment with the permit is accepted")
	header.Kind = KindBuild
	assert.Empty(t, worker.checkAssignment(Assignment{Header: header}, ChainTip{Branch: branch}),
		"a build needs no permit: running its commands is the whole of what it does")
}

// newSeenSetForTest opens a seen-set in a folder of the test's own.
func newSeenSetForTest(t *testing.T) *SeenSet {
	t.Helper()
	seen, err := LoadSeenSet(t.TempDir()+"/seen.json", time.Now())
	require.NoError(t, err)
	return seen
}

// TestAPublisherPrefersTheNodeThatBuilt: the preference is a preference. The
// named node wins while it has room, and a busy one is passed over rather than
// waited for, because waiting trades a certain delay for a possible copy.
func TestAPublisherPrefersTheNodeThatBuilt(t *testing.T) {
	pool := newTestPool(linuxNode(1), linuxNode(1))

	first, err := pool.AcquireNear(t.Context(), nil, PlacementWorker, "b-node")
	require.NoError(t, err)
	assert.Equal(t, "b-node", first.Node, "the node the package was built on took it")

	second, err := pool.AcquireNear(t.Context(), nil, PlacementWorker, "b-node")
	require.NoError(t, err)
	assert.Equal(t, "a-node", second.Node, "and a busy preference is passed over, not waited for")
}

// TestARefusedAuthorizationPushIsNoUnknownPublication: an authorization push
// the remote refused provably never became the branch's value, so nobody can
// have read it and nothing can have started under it. The run withdraws the
// waiting publisher as it does after any refused authorization, reports no
// unknown publication and retains no lock. A push whose outcome the reads
// could not establish is the case that stays unknown: the authorization may
// already have been read, and a node that never acknowledges the withdrawal
// leaves its repository's lock for an operator.
func TestARefusedAuthorizationPushIsNoUnknownPublication(t *testing.T) {
	for name, tc := range map[string]struct {
		failure  error
		retained []string
	}{
		"a push the remote refused": {
			failure: &messagePushError{cause: errors.New("the lease was rejected"), resolution: pushNotLanded}},
		"a failure preparing the message": {
			failure: errors.New("writing the go document")},
		"a push with no answer": {
			failure:  &messagePushError{cause: errors.New("the connection was reset"), resolution: pushUnknown},
			retained: []string{"web"}},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newCoordinatorFixture(t, TransferLimits{MaxManifestBytes: 1 << 20}, silentPreflight)
			fixture.coordinator.Timeouts.Cancel = 200 * time.Millisecond
			pool := NewPool([]Link{{Name: "build-a", Endpoint: fixture.orchestrator.endpoint}},
				[]*NodeReport{linuxNode(1)}, LocalNode{Name: "here", Capacity: 1}, zerolog.Nop())
			lease, err := pool.AcquireNear(t.Context(), nil, PlacementWorker, "")
			require.NoError(t, err)
			branch := FormatBranch("build-a", KindPublish, time.Now())

			_, err = fixture.coordinator.reportLostAuthorization(t.Context(), publicationAttempt{
				lease: lease, task: "core:publish", attempt: 1, repository: "web",
				offer: taskOffer{branch: branch, kind: KindPublish, offered: "assignment-oid"},
			}, release.StageOutcome{}, taskReply{kind: MessageReady, commit: "ready-oid"}, tc.failure)

			require.Error(t, err)
			if tc.retained == nil {
				require.ErrorIs(t, err, tc.failure, "the package fails with what refused the authorization")
				assert.Empty(t, fixture.coordinator.RetainedRepositories(), "no lock is retained")
				assert.Empty(t, fixture.coordinator.UnknownPublications(), "and nothing is unknown")
				assert.NotEqual(t, CodePublicationUnknown, config.DiagnosticCode(err))
				return
			}
			assert.Equal(t, tc.retained, fixture.coordinator.RetainedRepositories())
			assert.Equal(t, CodePublicationUnknown, config.DiagnosticCode(err))
		})
	}
}
