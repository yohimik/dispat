// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// The decisions a delegated publication is made of, asked of the pure
// functions that make them. What travels, how long an authorization means
// anything, which object the terminal message is written on top of, and which
// node a publisher would rather run on.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

// TestOnlyAPublicationBoundsItself: the node enforces the run's own wait for
// the one kind of work that must not outlive it. Gate 12 extends this to every
// kind; until then a build is bounded by the orchestrator alone.
func TestOnlyAPublicationBoundsItself(t *testing.T) {
	assert.Equal(t, 900, resolveTaskDeadlineSeconds(KindPublish, 15*time.Minute))
	assert.Equal(t, 0, resolveTaskDeadlineSeconds(KindBuild, 15*time.Minute))
	assert.Equal(t, 0, resolveTaskDeadlineSeconds(KindPrepare, 15*time.Minute))
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

// TestTheAuthorizationWaitIsTheRunsOwn: the node waits as long as the run
// says it will, and falls back to a bound of its own for an assignment that
// states none, because a node holding a checkout for ever is a node nobody
// can reuse.
func TestTheAuthorizationWaitIsTheRunsOwn(t *testing.T) {
	assert.Equal(t, 90*time.Second, resolveAuthorizationWait(90))
	assert.Equal(t, defaultAuthorizationWait, resolveAuthorizationWait(0))
	assert.Equal(t, defaultAuthorizationWait, resolveAuthorizationWait(-1))
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
