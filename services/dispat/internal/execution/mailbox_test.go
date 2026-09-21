// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// The mailbox against real repositories, and against a transport that
// misbehaves in the one way a real one can.
//
// Everything about a coordination branch is what git does with it, so the
// fixtures are a real bare remote and a real object store: a lease is refused
// by the remote, a fetched object is resolved by rev-list, and a tree is one
// git wrote. The fake transport is used for exactly one claim, which is the
// one a real remote cannot be made to produce on demand: a push that applied
// and whose answer was lost.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// mailboxFixture is one endpoint with a local object store of its own, which
// is exactly what one party of the protocol holds.
type mailboxFixture struct {
	endpoint string
	store    string
	git      *gitx.LocalGitx
	mailbox  *GitMailbox
	signer   *Signer
}

func newMailboxFixture(t *testing.T) *mailboxFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	endpoint := filepath.Join(root, "mailbox.git")
	store := filepath.Join(root, "store.git")
	for _, dir := range []string{endpoint, store} {
		out, err := exec.Command("git", "init", "-q", "--bare", dir).CombinedOutput()
		require.NoError(t, err, "git init --bare: %s", out)
	}
	signer, err := NewSigner("hunter2")
	require.NoError(t, err)
	git := &gitx.LocalGitx{Dir: store, Log: zerolog.Nop()}
	return &mailboxFixture{
		endpoint: endpoint, store: store, git: git, signer: signer,
		mailbox: NewGitMailbox(endpoint, git, signer, zerolog.Nop()),
	}
}

// second opens a second party on the same endpoint, with an object store of
// its own: the orchestrator and the worker never share a checkout.
func (f *mailboxFixture) second(t *testing.T) *mailboxFixture {
	t.Helper()
	store := filepath.Join(t.TempDir(), "other.git")
	out, err := exec.Command("git", "init", "-q", "--bare", store).CombinedOutput()
	require.NoError(t, err, "git init --bare: %s", out)
	git := &gitx.LocalGitx{Dir: store, Log: zerolog.Nop()}
	return &mailboxFixture{
		endpoint: f.endpoint, store: store, git: git, signer: f.signer,
		mailbox: NewGitMailbox(f.endpoint, git, f.signer, zerolog.Nop()),
	}
}

func (f *mailboxFixture) remoteBranches(t *testing.T) []string {
	t.Helper()
	out, err := exec.Command("git", "-C", f.endpoint, "for-each-ref", "--format=%(refname:short)", "refs/heads/").CombinedOutput()
	require.NoError(t, err, "for-each-ref: %s", out)
	listed := strings.TrimSpace(string(out))
	if listed == "" {
		return nil
	}
	return strings.Split(listed, "\n")
}

// probeAssignment is the message every scenario here offers.
func probeAssignment(node, branch string) *Assignment {
	return &Assignment{Header: Header{
		Protocol: ProtocolVersion, Kind: KindProbe, Run: "run-1", PlanDigest: "digest",
		Task: PreflightTask, Attempt: 1, Generation: "generation", Node: node,
		Branch: branch, IssuedAt: time.Now().UTC().Format(time.RFC3339),
	}}
}

// TestMailboxCarriesOneAttemptEndToEnd: an assignment is offered, seen by the
// other party, read back as the message it was, claimed, answered, and closed.
func TestMailboxCarriesOneAttemptEndToEnd(t *testing.T) {
	orchestrator := newMailboxFixture(t)
	worker := orchestrator.second(t)
	branch := FormatBranch("build-a", KindProbe, time.Now())

	offered, err := orchestrator.mailbox.Assign(t.Context(), probeAssignment("build-a", branch))
	require.NoError(t, err)
	assert.Equal(t, []string{branch}, orchestrator.remoteBranches(t))

	heads, err := worker.mailbox.Observe(t.Context(), FormatBranchPattern("build-a"))
	require.NoError(t, err)
	require.Len(t, heads, 1)
	assert.Equal(t, offered, heads[0].OID)

	tip, err := worker.mailbox.Inspect(t.Context(), heads[0])
	require.NoError(t, err)
	assert.Equal(t, MessageAssignment, tip.Kind)
	assert.Empty(t, tip.Previous, "a probe assignment is a root commit")
	assert.Equal(t, ActionClaim, ResolveWorkerAction(tip))

	document, err := worker.mailbox.Read(t.Context(), tip, 1<<20)
	require.NoError(t, err)
	var read Assignment
	require.NoError(t, json.Unmarshal(document, &read))
	assert.Equal(t, "run-1", read.Run)
	assert.Equal(t, branch, read.Branch)

	claimed, err := worker.mailbox.Advance(t.Context(), branch, offered, MessageClaim,
		mustMarshal(t, Claim{Header: read.Header, Assignment: offered}))
	require.NoError(t, err)
	reported, err := worker.mailbox.Advance(t.Context(), branch, claimed, MessageResult,
		mustMarshal(t, Result{Header: read.Header, Assignment: offered, Status: StatusSucceeded,
			Report: &NodeReport{Protocol: ProtocolVersion, Capacity: 2}}))
	require.NoError(t, err)

	// The orchestrator sees the branch move and reads the result off it.
	heads, err = orchestrator.mailbox.Observe(t.Context(), "refs/heads/"+branch)
	require.NoError(t, err)
	require.Len(t, heads, 1)
	assert.Equal(t, reported, heads[0].OID)
	tip, err = orchestrator.mailbox.Inspect(t.Context(), heads[0])
	require.NoError(t, err)
	assert.Equal(t, MessageResult, tip.Kind)
	assert.Equal(t, MessageClaim, tip.Previous)
	document, err = orchestrator.mailbox.Read(t.Context(), tip, 1<<20)
	require.NoError(t, err)
	var result Result
	require.NoError(t, json.Unmarshal(document, &result))
	assert.Equal(t, 2, result.Report.Capacity)

	outcomes, err := orchestrator.mailbox.Close(t.Context(),
		[]gitx.BranchLease{{Branch: branch, ExpectedOld: reported}})
	require.NoError(t, err)
	require.Len(t, outcomes, 1)
	assert.True(t, outcomes[0].IsDeleted)
	assert.Empty(t, orchestrator.remoteBranches(t))
}

// TestMailboxObservesOnlyWhatMoved: the memo is what makes an idle node cost
// one invocation per tick, so a branch that has not moved is not answered
// twice and a branch that was closed is forgotten.
func TestMailboxObservesOnlyWhatMoved(t *testing.T) {
	orchestrator := newMailboxFixture(t)
	worker := orchestrator.second(t)
	branch := FormatBranch("build-a", KindProbe, time.Now())
	offered, err := orchestrator.mailbox.Assign(t.Context(), probeAssignment("build-a", branch))
	require.NoError(t, err)

	first, err := worker.mailbox.Observe(t.Context(), FormatBranchPattern("build-a"))
	require.NoError(t, err)
	require.Len(t, first, 1)

	again, err := worker.mailbox.Observe(t.Context(), FormatBranchPattern("build-a"))
	require.NoError(t, err)
	assert.Empty(t, again, "an unchanged branch costs nothing after the first look")

	_, err = worker.mailbox.Advance(t.Context(), branch, offered, MessageClaim,
		mustMarshal(t, Claim{Assignment: offered}))
	require.NoError(t, err)
	moved, err := orchestrator.mailbox.Observe(t.Context(), FormatBranchPattern("build-a"))
	require.NoError(t, err)
	assert.Len(t, moved, 1, "the other party's push is what a poll is looking for")

	worker.mailbox.Forget()
	rebuilt, err := worker.mailbox.Observe(t.Context(), FormatBranchPattern("build-a"))
	require.NoError(t, err)
	assert.Len(t, rebuilt, 1, "a node whose store was rebuilt looks at everything again")
}

// TestMailboxFetchesInBatches: a mailbox holding more branches than one fetch
// may name is read over several polls rather than in one unbounded
// invocation, and every branch is eventually seen.
func TestMailboxFetchesInBatches(t *testing.T) {
	orchestrator := newMailboxFixture(t)
	worker := orchestrator.second(t)
	offered := map[string]bool{}
	for i := 0; i < gitx.MaxTransportBatch+5; i++ {
		branch := FormatBranch("build-a", KindProbe, time.Now())
		_, err := orchestrator.mailbox.Assign(t.Context(), probeAssignment("build-a", branch))
		require.NoError(t, err)
		offered[branch] = true
	}

	seen := map[string]bool{}
	for polls := 0; polls < 3 && len(seen) < len(offered); polls++ {
		heads, err := worker.mailbox.Observe(t.Context(), FormatBranchPattern("build-a"))
		require.NoError(t, err)
		assert.LessOrEqual(t, len(heads), gitx.MaxTransportBatch, "one fetch names at most one batch")
		for _, head := range heads {
			seen[head.Name] = true
			// Every branch the poll answered was actually fetched, which is
			// what makes reading it afterwards a local operation.
			tip, err := worker.mailbox.Inspect(t.Context(), head)
			require.NoError(t, err)
			assert.Equal(t, MessageAssignment, tip.Kind)
		}
	}
	assert.Len(t, seen, len(offered), "every branch is seen, over as many polls as it takes")
}

// TestMailboxRefusesWhatItCannotAuthenticate: everything a party may find on
// a branch that is not a message of this protocol signed with this secret.
func TestMailboxRefusesWhatItCannotAuthenticate(t *testing.T) {
	orchestrator := newMailboxFixture(t)
	fixture := orchestrator.second(t)
	branch := FormatBranch("build-a", KindProbe, time.Now())
	offered, err := orchestrator.mailbox.Assign(t.Context(), probeAssignment("build-a", branch))
	require.NoError(t, err)
	heads, err := fixture.mailbox.Observe(t.Context(), FormatBranchPattern("build-a"))
	require.NoError(t, err)
	require.Len(t, heads, 1)
	tip, err := fixture.mailbox.Inspect(t.Context(), heads[0])
	require.NoError(t, err)

	t.Run("a document larger than the ceiling is refused before it is read", func(t *testing.T) {
		_, err := fixture.mailbox.Read(t.Context(), tip, 8)
		assert.Equal(t, ReasonOversize, RejectionReason(err))
	})

	t.Run("a document this secret did not sign is refused", func(t *testing.T) {
		other, err := NewSigner("hunter3")
		require.NoError(t, err)
		stranger := NewGitMailbox(fixture.endpoint, fixture.git, other, zerolog.Nop())
		_, err = stranger.Read(t.Context(), tip, 1<<20)
		assert.Equal(t, ReasonSignature, RejectionReason(err))
	})

	t.Run("an authentic document read as another kind of message is refused", func(t *testing.T) {
		// The bytes and the signature are the ones this run wrote; only the
		// name they were found under differs, which is the whole of what a
		// party with push access to the mailbox can change without the secret.
		relabelled := tip
		relabelled.Kind = MessageResult

		_, err := fixture.mailbox.Read(t.Context(), relabelled, 1<<20)

		assert.Equal(t, ReasonSignature, RejectionReason(err),
			"a signature covers what the message is, not only what it says")
	})

	t.Run("a commit carrying no message is not a message", func(t *testing.T) {
		_, err := fixture.mailbox.Read(t.Context(), ChainTip{Branch: branch, OID: offered}, 1<<20)
		assert.Equal(t, ReasonUnreadable, RejectionReason(err))
	})
}

// TestMailboxResolvesALostPushResponse: a push whose answer was lost is
// indistinguishable at the caller from a push that was refused, so the branch
// is re-read once: finding the object this call was about to put there is
// success, and finding anything else is the rejection it was given.
func TestMailboxResolvesALostPushResponse(t *testing.T) {
	fixture := newMailboxFixture(t)
	branch := FormatBranch("build-a", KindProbe, time.Now())
	offered, err := fixture.mailbox.Assign(t.Context(), probeAssignment("build-a", branch))
	require.NoError(t, err)

	t.Run("the update was already applied", func(t *testing.T) {
		fake := &lostResponseTransport{real: fixture.git}
		fixture.mailbox.remote = fake
		defer func() { fixture.mailbox.remote = fixture.git }()

		claimed, err := fixture.mailbox.Advance(t.Context(), branch, offered, MessageClaim,
			mustMarshal(t, Claim{Assignment: offered}))

		require.NoError(t, err, "a push that applied is a push that worked, however its answer was lost")
		assert.Equal(t, claimed, fake.applied)
	})

	t.Run("somebody else moved the branch", func(t *testing.T) {
		// The branch is now at the claim, so a second advance leased against
		// the assignment is refused by the remote itself.
		_, err := fixture.mailbox.Advance(t.Context(), branch, offered, MessageClaim,
			mustMarshal(t, Claim{Assignment: offered, Header: Header{Task: "other"}}))

		require.Error(t, err)
		assert.ErrorIs(t, err, gitx.ErrLeaseRejected)
	})
}

// lostResponseTransport applies a leased push and then reports that it was
// rejected, which is what a network failure after a successful remote write
// looks like from here.
type lostResponseTransport struct {
	*gitx.LocalGitx
	real    *gitx.LocalGitx
	applied string
}

func (t *lostResponseTransport) PushAdvance(ctx context.Context, remote, oid, branch, expectedOld string) error {
	if err := t.real.PushAdvance(ctx, remote, oid, branch, expectedOld); err != nil {
		return err
	}
	t.applied = oid
	return fmt.Errorf("the answer never arrived: %w", gitx.ErrLeaseRejected)
}

func (t *lostResponseTransport) ListRemoteHeads(ctx context.Context, remote, pattern string) ([]gitx.RemoteHead, error) {
	return t.real.ListRemoteHeads(ctx, remote, pattern)
}

// TestMailboxClosesInBatches: a run closing more branches than one push may
// name closes all of them, and a branch somebody else took over is reported
// as retained rather than retried.
func TestMailboxClosesInBatches(t *testing.T) {
	fixture := newMailboxFixture(t)
	var leases []gitx.BranchLease
	for i := 0; i < gitx.MaxTransportBatch+3; i++ {
		branch := FormatBranch("build-a", KindProbe, time.Now())
		offered, err := fixture.mailbox.Assign(t.Context(), probeAssignment("build-a", branch))
		require.NoError(t, err)
		leases = append(leases, gitx.BranchLease{Branch: branch, ExpectedOld: offered})
	}
	// One of them is leased against a value it never held, which is what a
	// branch another party took over looks like.
	leases[0].ExpectedOld = strings.Repeat("0", 40)

	outcomes, err := fixture.mailbox.Close(t.Context(), leases)

	require.NoError(t, err)
	require.Len(t, outcomes, len(leases))
	retained := 0
	for _, outcome := range outcomes {
		if !outcome.IsDeleted {
			retained++
		}
	}
	assert.Equal(t, 1, retained, "the stale lease fails its own ref and no other")
	assert.Len(t, fixture.remoteBranches(t), 1)
}

// TestMailboxReadsOnlyWhatWasFetched: a reader resolves the exact object it
// was promised and refuses one that is not on the fetched ref, so a tip that
// moved between the poll and the fetch cannot make a node read somebody
// else's object.
func TestMailboxReadsOnlyWhatWasFetched(t *testing.T) {
	orchestrator := newMailboxFixture(t)
	fixture := orchestrator.second(t)
	branch := FormatBranch("build-a", KindProbe, time.Now())
	_, err := orchestrator.mailbox.Assign(t.Context(), probeAssignment("build-a", branch))
	require.NoError(t, err)
	heads, err := fixture.mailbox.Observe(t.Context(), FormatBranchPattern("build-a"))
	require.NoError(t, err)
	require.Len(t, heads, 1)

	stranger := heads[0]
	stranger.OID = strings.Repeat("a", 40)
	_, err = fixture.mailbox.Inspect(t.Context(), stranger)

	require.Error(t, err)
	assert.True(t, errors.Is(err, gitx.ErrCommitNotOnRef))
}

func mustMarshal(t *testing.T, message any) []byte {
	t.Helper()
	document, err := json.Marshal(message)
	require.NoError(t, err)
	return document
}
