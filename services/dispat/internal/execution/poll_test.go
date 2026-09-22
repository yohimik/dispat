// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// The three ways a serving node used to stop seeing its work: one ref nobody
// can fetch, a transport branch read as an assignment, and an idle clock that
// counted the wrong thing.
//
// Each of them is a loop-level property rather than a message-level one, so
// each is asserted against the loop: a mailbox that fails one fetch, a branch
// whose name says it carries no work, and a node with a task in flight.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// unfetchableTransport answers the poll with the heads it was given and
// refuses to fetch the branches named in `broken`, in a batch or alone. It is
// the only fault that matters here: a ref that lists and does not fetch.
type unfetchableTransport struct {
	transportx
	heads   []gitx.RemoteHead
	broken  map[string]bool
	batches [][]string
}

func (t *unfetchableTransport) ListRemoteHeads(context.Context, string, string) ([]gitx.RemoteHead, error) {
	return t.heads, nil
}

func (t *unfetchableTransport) FetchRefs(_ context.Context, _ string, branches []string) error {
	t.batches = append(t.batches, branches)
	for _, branch := range branches {
		if t.broken[branch] {
			return errors.New("fatal: couldn't find remote ref " + branch)
		}
	}
	return nil
}

// TestMailboxPollSurvivesOneUnfetchableBranch: the whole poll used to fail on
// the batched fetch, so a namespace holding one bad ref never reported any of
// the work beside it. The batch is retried per branch, the good ones are
// answered, and the bad one is named once.
func TestMailboxPollSurvivesOneUnfetchableBranch(t *testing.T) {
	transport := &unfetchableTransport{
		heads: []gitx.RemoteHead{
			{Name: "dispat-worker-build-a-20260922-build-aa", OID: "oid-a"},
			{Name: "dispat-worker-build-a-20260922-relay-bb", OID: "oid-b"},
			{Name: "dispat-worker-build-a-20260922-build-cc", OID: "oid-c"},
		},
		broken: map[string]bool{"dispat-worker-build-a-20260922-relay-bb": true},
	}
	mailbox := NewGitMailbox("file:///srv/mailbox.git", nil, nil, zerolog.Nop())
	mailbox.remote = transport

	moved, err := mailbox.Observe(t.Context(), FormatBranchPattern("build-a"))

	require.NoError(t, err)
	require.Len(t, moved, 2, "the readable branches are still reported")
	assert.Equal(t, "dispat-worker-build-a-20260922-build-aa", moved[0].Name)
	assert.Equal(t, "dispat-worker-build-a-20260922-build-cc", moved[1].Name)
	require.Len(t, transport.batches, 4, "one batch, then one call per branch")
	assert.True(t, mailbox.quarantined["dispat-worker-build-a-20260922-relay-bb"])

	// A second poll of unmoved branches reports nothing and fetches nothing:
	// the quarantined tip is remembered, so the node does not pay for it again.
	transport.heads = []gitx.RemoteHead{
		{Name: "dispat-worker-build-a-20260922-build-aa", OID: "oid-a"},
		{Name: "dispat-worker-build-a-20260922-relay-bb", OID: "oid-b"},
		{Name: "dispat-worker-build-a-20260922-build-cc", OID: "oid-c"},
	}
	transport.batches = nil
	moved, err = mailbox.Observe(t.Context(), FormatBranchPattern("build-a"))
	require.NoError(t, err)
	assert.Empty(t, moved)
	assert.Empty(t, transport.batches)
}

// TestMailboxPollFailsWhenTheOnlyBranchIsUnreadable: a mailbox that is
// actually unreachable must still be reported as such, so the one-branch case
// answers the failure rather than an empty poll.
func TestMailboxPollFailsWhenTheOnlyBranchIsUnreadable(t *testing.T) {
	transport := &unfetchableTransport{
		heads:  []gitx.RemoteHead{{Name: "dispat-worker-build-a-20260922-build-aa", OID: "oid-a"}},
		broken: map[string]bool{"dispat-worker-build-a-20260922-build-aa": true},
	}
	mailbox := NewGitMailbox("file:///srv/mailbox.git", nil, nil, zerolog.Nop())
	mailbox.remote = transport

	_, err := mailbox.Observe(t.Context(), FormatBranchPattern("build-a"))

	require.Error(t, err)
}

// TestBranchKindHintTellsTransportBranchesApart: the name is a hint and is
// used for exactly one decision, which is to skip reading a branch that could
// not be work. A node name holding hyphens must not confuse it.
func TestBranchKindHintTellsTransportBranchesApart(t *testing.T) {
	for name, tc := range map[string]struct {
		branch    string
		kind      string
		isCarried bool
	}{
		"a build": {
			branch: "dispat-worker-build-a-20260922-build-0f1e", kind: KindBuild, isCarried: true},
		"a publication": {
			branch: "dispat-worker-b-20260922-publish-0f1e", kind: KindPublish, isCarried: true},
		"a probe": {
			branch: "dispat-worker-b-20260922-probe-0f1e", kind: KindProbe, isCarried: true},
		"a prepared input state": {
			branch: "dispat-worker-build-a-20260922-snapshot-0f1e", kind: KindSnapshot},
		"a relayed result": {
			branch: "dispat-worker-build-a-20260922-relay-0f1e", kind: KindRelay},
		"a name that is not one of ours": {
			branch: "main", kind: "", isCarried: true},
		"a kind this build does not know": {
			branch: "dispat-worker-b-20260922-bundle-0f1e", kind: "bundle", isCarried: true},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.kind, ResolveBranchKindHint(tc.branch))
			assert.Equal(t, tc.isCarried, IsBranchCarryingWork(tc.branch))
		})
	}
}

// TestWorkerSkipsTransportBranchesWithoutReadingThem: a prepared input state
// pushed into the node's own namespace used to be inspected and reported as an
// assignment nobody could read, once per snapshot and at warning level.
func TestWorkerSkipsTransportBranchesWithoutReadingThem(t *testing.T) {
	branch := "dispat-worker-build-a-20260922-snapshot-abc"
	tip := ChainTip{Branch: branch, OID: "snapshot-oid", isProtocol: true}
	worker, mailbox := newWorkerFixture(t, branch, tip, validProbe(branch))

	isProgress := worker.tick(t.Context())

	assert.False(t, isProgress)
	assert.Empty(t, mailbox.written, "nothing is claimed and nothing is answered")
	assert.Equal(t, 1, mailbox.polls)
}

// TestWorkerIdleClockCountsFromTheLastThingItDid: the three states that are
// not idleness. A node that has just started, a node with a task in flight,
// and a node whose task has only just ended each answer a positive remainder,
// which is what stops the loop from reporting `idle`.
func TestWorkerIdleClockCountsFromTheLastThingItDid(t *testing.T) {
	worker := &Worker{IdleTimeout: time.Minute, Log: zerolog.Nop()}

	worker.markActive()
	assert.Positive(t, worker.resolveIdleRemainder(), "a node that has just started is not idle")

	worker.lastActive = time.Now().Add(-2 * time.Minute)
	assert.LessOrEqual(t, worker.resolveIdleRemainder(), time.Duration(0),
		"a node that has had nothing to do for longer than the timeout may stop")

	worker.beginTask()
	assert.Equal(t, time.Minute, worker.resolveIdleRemainder(),
		"a node with a task in flight is never idle, however long the task runs")

	worker.lastActive = time.Now().Add(-2 * time.Minute)
	assert.Equal(t, time.Minute, worker.resolveIdleRemainder())

	worker.endTask()
	assert.Positive(t, worker.resolveIdleRemainder(),
		"the clock restarts when the task ends, so a long build is not followed by an immediate stop")
}
