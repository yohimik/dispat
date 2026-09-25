// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// The mailbox against real repositories, and against a transport that
// misbehaves in the one way a real one can.
//
// Everything about a coordination branch is what git does with it, so the
// fixtures are a real bare remote and a real object store: a lease is refused
// by the remote, a fetched object is resolved by rev-list, and a tree is one
// git wrote. The fake transport is used for exactly one kind of claim, which
// is the one a real remote cannot be made to produce on demand: a push that
// applied, or did not, and whose answer was lost.

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

	offered, err := assign(t.Context(), orchestrator.mailbox, probeAssignment("build-a", branch))
	require.NoError(t, err)
	assert.Equal(t, []string{branch}, orchestrator.remoteBranches(t))

	heads, err := worker.mailbox.Observe(t.Context(), FormatBranchPattern("build-a"), nil)
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
		mustMarshal(t, Claim{Header: read.Header, Assignment: offered}), nil)
	require.NoError(t, err)
	reported, err := worker.mailbox.Advance(t.Context(), branch, claimed, MessageResult,
		mustMarshal(t, Result{Header: read.Header, Assignment: offered, Status: StatusSucceeded,
			Report: &NodeReport{Protocol: ProtocolVersion, Capacity: 2}}), nil)
	require.NoError(t, err)

	// The orchestrator sees the branch move and reads the result off it.
	heads, err = orchestrator.mailbox.Observe(t.Context(), "refs/heads/"+branch, nil)
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
	assert.Equal(t, gitx.BranchDeleted, outcomes[0].Result)
	assert.Empty(t, orchestrator.remoteBranches(t))
}

// TestMailboxObservesOnlyWhatMoved: the memo is what makes an idle node cost
// one invocation per tick, so a branch that has not moved is not answered
// twice and a branch that was closed is forgotten.
func TestMailboxObservesOnlyWhatMoved(t *testing.T) {
	orchestrator := newMailboxFixture(t)
	worker := orchestrator.second(t)
	branch := FormatBranch("build-a", KindProbe, time.Now())
	offered, err := assign(t.Context(), orchestrator.mailbox, probeAssignment("build-a", branch))
	require.NoError(t, err)

	first, err := worker.mailbox.Observe(t.Context(), FormatBranchPattern("build-a"), nil)
	require.NoError(t, err)
	require.Len(t, first, 1)

	again, err := worker.mailbox.Observe(t.Context(), FormatBranchPattern("build-a"), nil)
	require.NoError(t, err)
	assert.Empty(t, again, "an unchanged branch costs nothing after the first look")

	_, err = worker.mailbox.Advance(t.Context(), branch, offered, MessageClaim,
		mustMarshal(t, Claim{Assignment: offered}), nil)
	require.NoError(t, err)
	moved, err := orchestrator.mailbox.Observe(t.Context(), FormatBranchPattern("build-a"), nil)
	require.NoError(t, err)
	assert.Len(t, moved, 1, "the other party's push is what a poll is looking for")

	worker.mailbox.Forget()
	rebuilt, err := worker.mailbox.Observe(t.Context(), FormatBranchPattern("build-a"), nil)
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
		_, err := assign(t.Context(), orchestrator.mailbox, probeAssignment("build-a", branch))
		require.NoError(t, err)
		offered[branch] = true
	}

	seen := map[string]bool{}
	for polls := 0; polls < 3 && len(seen) < len(offered); polls++ {
		heads, err := worker.mailbox.Observe(t.Context(), FormatBranchPattern("build-a"), nil)
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
	offered, err := assign(t.Context(), orchestrator.mailbox, probeAssignment("build-a", branch))
	require.NoError(t, err)
	heads, err := fixture.mailbox.Observe(t.Context(), FormatBranchPattern("build-a"), nil)
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

// TestMailboxSettlesAPushWithNoAnswer: a push that did not report success is
// settled by reading the remote and never by pushing again. The object this
// party wrote is on the branch (landed), under what the other party wrote on
// top of it (landed and answered), provably not there after a refusal, or not
// found after an unknown outcome, which stays unknown however often it is
// read. A late landing is seen on a later read. And the settling read never
// records a tip as observed, so the poll still delivers what it found.
func TestMailboxSettlesAPushWithNoAnswer(t *testing.T) {
	ownPushReadPauses = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { ownPushReadPauses = []time.Duration{time.Second, 2 * time.Second} })

	t.Run("an update that applied and whose answer was lost landed", func(t *testing.T) {
		fixture, branch, offered := newAssignedFixture(t)
		fake := &faultyTransport{LocalGitx: fixture.git, apply: true, answer: gitx.ErrPushUnknown}
		fixture.mailbox.remote = fake

		claimed, err := fixture.mailbox.Advance(t.Context(), branch, offered, MessageClaim,
			mustMarshal(t, Claim{Assignment: offered}), nil)

		require.NoError(t, err, "a push that applied is a push that worked, however its answer was lost")
		assert.Equal(t, claimed, fake.pushed)
		assert.Equal(t, 1, fake.pushes, "and it is never pushed again")
	})

	t.Run("an update the other party already answered landed", func(t *testing.T) {
		fixture, branch, offered := newAssignedFixture(t)
		worker := fixture.second(t)
		fake := &faultyTransport{LocalGitx: fixture.git, apply: true, answer: gitx.ErrPushUnknown,
			afterPush: func(claimed string) {
				_, err := worker.mailbox.Reread(t.Context(), branch)
				require.NoError(t, err)
				_, err = worker.mailbox.Advance(t.Context(), branch, claimed, MessageResult,
					mustMarshal(t, Result{Assignment: offered}), nil)
				require.NoError(t, err)
			}}
		fixture.mailbox.remote = fake

		_, err := fixture.mailbox.Advance(t.Context(), branch, offered, MessageClaim,
			mustMarshal(t, Claim{Assignment: offered}), nil)

		require.NoError(t, err, "a message the other party built on is on the branch")
		heads, err := fixture.mailbox.Observe(t.Context(), "refs/heads/"+branch, nil)
		require.NoError(t, err)
		require.Len(t, heads, 1, "the settling read recorded nothing, so the poll delivers the answer")
		tip, err := fixture.mailbox.Inspect(t.Context(), heads[0])
		require.NoError(t, err)
		assert.Equal(t, MessageResult, tip.Kind)
	})

	t.Run("a refused update did not land", func(t *testing.T) {
		fixture, branch, offered := newAssignedFixture(t)
		worker := fixture.second(t)
		_, err := worker.mailbox.Reread(t.Context(), branch)
		require.NoError(t, err)
		_, err = worker.mailbox.Advance(t.Context(), branch, offered, MessageClaim,
			mustMarshal(t, Claim{Assignment: offered}), nil)
		require.NoError(t, err)

		_, err = fixture.mailbox.Advance(t.Context(), branch, offered, MessageClaim,
			mustMarshal(t, Claim{Assignment: offered, Header: Header{Task: "other"}}), nil)

		require.ErrorIs(t, err, gitx.ErrLeaseRejected)
		assert.Equal(t, pushNotLanded, resolvePushError(err),
			"a refusal whose branch does not descend from the message proves it never became the branch")
	})

	t.Run("an unknown outcome stays unknown", func(t *testing.T) {
		fixture, branch, offered := newAssignedFixture(t)
		fake := &faultyTransport{LocalGitx: fixture.git, answer: gitx.ErrPushUnknown}
		fixture.mailbox.remote = fake

		_, err := fixture.mailbox.Advance(t.Context(), branch, offered, MessageClaim,
			mustMarshal(t, Claim{Assignment: offered}), nil)

		assert.Equal(t, pushUnknown, resolvePushError(err), "a branch still at the old object proves nothing")
		assert.Equal(t, 3, fake.listings, "an unknown outcome is read three times")
		assert.Equal(t, 1, fake.pushes)
		var pushed *messagePushError
		require.ErrorAs(t, err, &pushed)
		assert.NotEmpty(t, pushed.oid, "the message is named, so it can be recognised if it surfaces")
	})

	t.Run("a late landing is seen on a later read", func(t *testing.T) {
		fixture, branch, offered := newAssignedFixture(t)
		fake := &faultyTransport{LocalGitx: fixture.git, answer: gitx.ErrPushUnknown, applyOnListing: 2}
		fixture.mailbox.remote = fake

		_, err := fixture.mailbox.Advance(t.Context(), branch, offered, MessageClaim,
			mustMarshal(t, Claim{Assignment: offered}), nil)

		require.NoError(t, err)
		assert.Equal(t, 2, fake.listings, "the second read found it")
	})

	t.Run("a refusal the remote cannot be read after is unknown", func(t *testing.T) {
		fixture, branch, offered := newAssignedFixture(t)
		fake := &faultyTransport{LocalGitx: fixture.git, answer: gitx.ErrLeaseRejected, isUnreadable: true}
		fixture.mailbox.remote = fake

		_, err := fixture.mailbox.Advance(t.Context(), branch, offered, MessageClaim,
			mustMarshal(t, Claim{Assignment: offered}), nil)

		assert.Equal(t, pushUnknown, resolvePushError(err))
		assert.Equal(t, 1, fake.listings, "a refusal is read once")
	})

	t.Run("a local failure is no push at all", func(t *testing.T) {
		fixture, branch, _ := newAssignedFixture(t)

		_, err := fixture.mailbox.Advance(t.Context(), branch, strings.Repeat("0", 40), MessageClaim,
			mustMarshal(t, Claim{}), nil)

		require.Error(t, err)
		var pushed *messagePushError
		assert.False(t, errors.As(err, &pushed), "a message that was never written was never pushed")
		assert.Equal(t, pushNotLanded, resolvePushError(err))
	})
}

// TestMailboxSettlesACreateWithNoAnswer: an offer whose push reported nothing
// is read back like an advance, and one the remote took is recorded as
// observed only when the push itself said so.
func TestMailboxSettlesACreateWithNoAnswer(t *testing.T) {
	ownPushReadPauses = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { ownPushReadPauses = []time.Duration{time.Second, 2 * time.Second} })
	fixture := newMailboxFixture(t)
	for name, tc := range map[string]struct {
		transport *faultyTransport
		want      pushResolution
	}{
		"applied and lost":  {&faultyTransport{LocalGitx: fixture.git, apply: true, answer: gitx.ErrPushUnknown}, pushLanded},
		"never applied":     {&faultyTransport{LocalGitx: fixture.git, answer: gitx.ErrPushUnknown}, pushUnknown},
		"refused by a hook": {&faultyTransport{LocalGitx: fixture.git, answer: gitx.ErrRemoteRefused}, pushNotLanded},
	} {
		t.Run(name, func(t *testing.T) {
			branch := FormatBranch("build-a", KindProbe, time.Now())
			oid, err := fixture.mailbox.PrepareAssignment(t.Context(), probeAssignment("build-a", branch))
			require.NoError(t, err)
			fixture.mailbox.remote = tc.transport
			t.Cleanup(func() { fixture.mailbox.remote = fixture.git })

			resolution, err := fixture.mailbox.Offer(t.Context(), branch, oid)

			require.Error(t, err, "the push's own failure is still reported")
			assert.Equal(t, tc.want, resolution)
			assert.Empty(t, fixture.mailbox.observed[branch], "a settled offer is not a handled tip")
		})
	}
}

// newAssignedFixture is a mailbox with one probe assignment on it.
func newAssignedFixture(t *testing.T) (*mailboxFixture, string, string) {
	t.Helper()
	fixture := newMailboxFixture(t)
	branch := FormatBranch("build-a", KindProbe, time.Now())
	offered, err := assign(t.Context(), fixture.mailbox, probeAssignment("build-a", branch))
	require.NoError(t, err)
	return fixture, branch, offered
}

// faultyTransport answers every push with a chosen failure, after applying it
// or not, which is the one thing a real remote cannot be made to do on
// demand: apply a write and lose the answer.
type faultyTransport struct {
	*gitx.LocalGitx
	// apply applies the push before failing it; applyOnListing applies it
	// only when the remote is listed for the nth time, which is a push the
	// network delivered after its client gave up.
	apply          bool
	applyOnListing int
	answer         error
	isUnreadable   bool
	afterPush      func(oid string)

	pushes, listings int
	pushed           string
	late             func() error
}

func (t *faultyTransport) PushCreate(ctx context.Context, remote, oid, branch string) error {
	return t.push(func() error { return t.LocalGitx.PushCreate(ctx, remote, oid, branch) }, oid)
}

func (t *faultyTransport) PushAdvance(ctx context.Context, remote, oid, branch, expectedOld string) error {
	return t.push(func() error { return t.LocalGitx.PushAdvance(ctx, remote, oid, branch, expectedOld) }, oid)
}

func (t *faultyTransport) push(real func() error, oid string) error {
	t.pushes++
	t.pushed = oid
	if t.apply {
		if err := real(); err != nil {
			return err
		}
		if t.afterPush != nil {
			t.afterPush(oid)
		}
	}
	if t.applyOnListing > 0 {
		t.late = real
	}
	return fmt.Errorf("the answer never arrived: %w", t.answer)
}

func (t *faultyTransport) ListRemoteHeads(ctx context.Context, remote, pattern string) ([]gitx.RemoteHead, error) {
	t.listings++
	if t.isUnreadable {
		return nil, errors.New("the remote hung up")
	}
	if t.late != nil && t.listings == t.applyOnListing {
		if err := t.late(); err != nil {
			return nil, err
		}
	}
	return t.LocalGitx.ListRemoteHeads(ctx, remote, pattern)
}

// TestMailboxClosesInBatches: a run closing more branches than one push may
// name closes all of them, and a branch somebody else took over is reported
// as retained rather than retried.
func TestMailboxClosesInBatches(t *testing.T) {
	fixture := newMailboxFixture(t)
	var leases []gitx.BranchLease
	for i := 0; i < gitx.MaxTransportBatch+3; i++ {
		branch := FormatBranch("build-a", KindProbe, time.Now())
		offered, err := assign(t.Context(), fixture.mailbox, probeAssignment("build-a", branch))
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
		if outcome.Result != gitx.BranchDeleted {
			retained++
		}
	}
	assert.Equal(t, 1, retained, "the stale lease fails its own ref and no other")
	assert.Len(t, fixture.remoteBranches(t), 1)
}

// TestMailboxCloseSettlesEveryBatch: a close attempts every batch whatever
// became of the one before it. A lease over a branch that is already gone is
// a closed branch, although git refuses it as stale; a batch the transport
// could not push is settled by reading the remote, so its branches that are
// gone are closed and the ones still there are retained; and the fetched refs
// are removed either way.
func TestMailboxCloseSettlesEveryBatch(t *testing.T) {
	ownPushReadPauses = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { ownPushReadPauses = []time.Duration{time.Second, 2 * time.Second} })
	fixture := newMailboxFixture(t)
	var leases []gitx.BranchLease
	for i := 0; i < gitx.MaxTransportBatch+2; i++ {
		branch := FormatBranch("build-a", KindProbe, time.Now())
		offered, err := assign(t.Context(), fixture.mailbox, probeAssignment("build-a", branch))
		require.NoError(t, err)
		leases = append(leases, gitx.BranchLease{Branch: branch, ExpectedOld: offered})
	}
	gone := gitx.BranchLease{Branch: FormatBranch("build-a", KindProbe, time.Now()), ExpectedOld: leases[0].ExpectedOld}
	leases = append(leases, gone)
	fake := &failingDeleteTransport{LocalGitx: fixture.git, failFirst: true}
	fixture.mailbox.remote = fake

	outcomes, err := fixture.mailbox.Close(t.Context(), leases)

	require.Error(t, err, "the batch that could not be pushed is reported")
	assert.Equal(t, 2, fake.batches, "and the next batch was still attempted")
	results := map[string]gitx.BranchResult{}
	for _, outcome := range outcomes {
		results[outcome.Branch] = outcome.Result
	}
	assert.Equal(t, gitx.BranchDeleted, results[gone.Branch], "a branch already gone is closed")
	assert.Equal(t, gitx.BranchUnknown, results[leases[0].Branch], "a branch still there is not")
	assert.Equal(t, gitx.BranchDeleted, results[leases[gitx.MaxTransportBatch].Branch])
	assert.Len(t, fixture.remoteBranches(t), gitx.MaxTransportBatch, "the failed batch left its branches")
	assert.Equal(t, 1, fake.localDeletes, "the fetched refs are removed whatever the pushes did")
}

// failingDeleteTransport refuses to push the first batched delete at all and
// counts what it is asked.
type failingDeleteTransport struct {
	*gitx.LocalGitx
	failFirst    bool
	batches      int
	localDeletes int
}

func (t *failingDeleteTransport) DeleteRemoteBranchesLease(ctx context.Context, remote string,
	leases []gitx.BranchLease) ([]gitx.BranchOutcome, error) {
	t.batches++
	if t.failFirst && t.batches == 1 {
		return nil, errors.New("the batch could not be written")
	}
	return t.LocalGitx.DeleteRemoteBranchesLease(ctx, remote, leases)
}

func (t *failingDeleteTransport) DeleteLocalTransportRefs(ctx context.Context, branches []string) error {
	t.localDeletes++
	return t.LocalGitx.DeleteLocalTransportRefs(ctx, branches)
}

// TestMailboxWithdrawSettlesAStaleLease: a revocation of a branch that is
// already gone is a revocation, and one of a branch that moved is not.
func TestMailboxWithdrawSettlesAStaleLease(t *testing.T) {
	fixture, branch, offered := newAssignedFixture(t)
	absent := FormatBranch("build-a", KindProbe, time.Now())

	isRevoked, err := fixture.mailbox.Withdraw(t.Context(), absent, offered)
	require.NoError(t, err)
	assert.True(t, isRevoked, "a branch nobody holds is withdrawn")

	worker := fixture.second(t)
	_, err = worker.mailbox.Reread(t.Context(), branch)
	require.NoError(t, err)
	_, err = worker.mailbox.Advance(t.Context(), branch, offered, MessageClaim,
		mustMarshal(t, Claim{Assignment: offered}), nil)
	require.NoError(t, err)
	isRevoked, err = fixture.mailbox.Withdraw(t.Context(), branch, offered)
	require.NoError(t, err)
	assert.False(t, isRevoked, "a branch somebody moved is theirs")
}

// TestMailboxReadsOnlyWhatWasFetched: a reader resolves the exact object it
// was promised and refuses one that is not on the fetched ref, so a tip that
// moved between the poll and the fetch cannot make a node read somebody
// else's object.
func TestMailboxReadsOnlyWhatWasFetched(t *testing.T) {
	orchestrator := newMailboxFixture(t)
	fixture := orchestrator.second(t)
	branch := FormatBranch("build-a", KindProbe, time.Now())
	_, err := assign(t.Context(), orchestrator.mailbox, probeAssignment("build-a", branch))
	require.NoError(t, err)
	heads, err := fixture.mailbox.Observe(t.Context(), FormatBranchPattern("build-a"), nil)
	require.NoError(t, err)
	require.Len(t, heads, 1)

	stranger := heads[0]
	stranger.OID = strings.Repeat("a", 40)
	_, err = fixture.mailbox.Inspect(t.Context(), stranger)

	require.Error(t, err)
	assert.True(t, errors.Is(err, gitx.ErrCommitNotOnRef))
}

// assign prepares and offers one assignment as the coordinator does, and
// answers its commit when the offer landed.
func assign(ctx context.Context, mailbox *GitMailbox, message *Assignment) (string, error) {
	oid, err := mailbox.PrepareAssignment(ctx, message)
	if err != nil {
		return "", err
	}
	if _, err := mailbox.Offer(ctx, message.Branch, oid); err != nil {
		return "", err
	}
	return oid, nil
}

func mustMarshal(t *testing.T, message any) []byte {
	t.Helper()
	document, err := json.Marshal(message)
	require.NoError(t, err)
	return document
}

// TestMailboxFetchesOnlyWantedBranches: a poll that says which branches it
// wants fetches nothing else and remembers nothing else, so another run's
// branch in a shared mailbox is never brought into this store and is reported
// the moment it becomes wanted.
func TestMailboxFetchesOnlyWantedBranches(t *testing.T) {
	orchestrator := newMailboxFixture(t)
	reader := orchestrator.second(t)
	mine := FormatBranch("build-a", KindProbe, time.Now())
	theirs := FormatBranch("build-a", KindProbe, time.Now())
	for _, branch := range []string{mine, theirs} {
		_, err := assign(t.Context(), orchestrator.mailbox, probeAssignment("build-a", branch))
		require.NoError(t, err)
	}
	isMine := func(branch string) bool { return branch == mine }

	heads, err := reader.mailbox.Observe(t.Context(), FormatBranchPattern("build-a"), isMine)

	require.NoError(t, err)
	require.Len(t, heads, 1)
	assert.Equal(t, mine, heads[0].Name)
	refs := strings.Fields(runGitIn(t, reader.store, "for-each-ref", "--format=%(refname)", gitx.TransportRefPrefix))
	assert.Equal(t, []string{gitx.TransportRefPrefix + mine}, refs, "the unwanted branch was never fetched")

	heads, err = reader.mailbox.Observe(t.Context(), FormatBranchPattern("build-a"), nil)
	require.NoError(t, err)
	require.Len(t, heads, 1, "the unwanted branch was not remembered, so it is reported once wanted")
	assert.Equal(t, theirs, heads[0].Name)
}

// TestMailboxDoesNotHoldItselfAcrossATransfer: a push that takes as long as a
// large output set does blocks nothing else of the mailbox. A poll, a reread
// and a withdrawal go on while it is in flight.
func TestMailboxDoesNotHoldItselfAcrossATransfer(t *testing.T) {
	fixture, branch, offered := newAssignedFixture(t)
	other := FormatBranch("build-a", KindProbe, time.Now())
	otherOffered, err := assign(t.Context(), fixture.mailbox, probeAssignment("build-a", other))
	require.NoError(t, err)
	slow := &blockingPushTransport{LocalGitx: fixture.git, entered: make(chan struct{}), release: make(chan struct{})}
	fixture.mailbox.remote = slow
	pushed := make(chan error, 1)
	go func() {
		_, err := fixture.mailbox.Advance(context.Background(), branch, offered, MessageClaim,
			mustMarshal(t, Claim{Assignment: offered}), nil)
		pushed <- err
	}()
	<-slow.entered

	head, err := fixture.mailbox.Reread(t.Context(), branch)
	require.NoError(t, err, "a reread goes on while the push is in flight")
	assert.Equal(t, offered, head.OID)
	_, err = fixture.mailbox.Observe(t.Context(), FormatBranchPattern("build-a"), nil)
	require.NoError(t, err, "and so does a poll")
	isRevoked, err := fixture.mailbox.Withdraw(t.Context(), other, otherOffered)
	require.NoError(t, err)
	assert.True(t, isRevoked, "and a withdrawal")

	close(slow.release)
	require.NoError(t, <-pushed)
}

// TestMailboxCloseRemovesEveryFetchedRef: a close removes the fetched refs of
// the branches it closes and of every other branch this process fetched, and
// a poll removes the fetched ref of a branch the remote no longer holds.
func TestMailboxCloseRemovesEveryFetchedRef(t *testing.T) {
	orchestrator := newMailboxFixture(t)
	reader := orchestrator.second(t)
	var leases []gitx.BranchLease
	for range 3 {
		branch := FormatBranch("build-a", KindProbe, time.Now())
		offered, err := assign(t.Context(), orchestrator.mailbox, probeAssignment("build-a", branch))
		require.NoError(t, err)
		leases = append(leases, gitx.BranchLease{Branch: branch, ExpectedOld: offered})
	}
	_, err := reader.mailbox.Observe(t.Context(), FormatBranchPattern("build-a"), nil)
	require.NoError(t, err)
	listRefs := func() []string {
		return strings.Fields(runGitIn(t, reader.store, "for-each-ref", "--format=%(refname)", gitx.TransportRefPrefix))
	}
	require.Len(t, listRefs(), 3)

	isRevoked, err := orchestrator.mailbox.Withdraw(t.Context(), leases[0].Branch, leases[0].ExpectedOld)
	require.NoError(t, err)
	require.True(t, isRevoked)
	_, err = reader.mailbox.Observe(t.Context(), FormatBranchPattern("build-a"), nil)
	require.NoError(t, err)
	assert.Len(t, listRefs(), 2, "a branch the remote closed is not kept here")

	_, err = reader.mailbox.Close(t.Context(), leases[1:2])
	require.NoError(t, err)
	assert.Empty(t, listRefs(), "a close removes every ref this process fetched")
}

// blockingPushTransport holds every advance until it is released, which is
// what a push of a large output set looks like to everything else.
type blockingPushTransport struct {
	*gitx.LocalGitx
	entered chan struct{}
	release chan struct{}
}

func (t *blockingPushTransport) PushAdvance(ctx context.Context, remote, oid, branch, expectedOld string) error {
	close(t.entered)
	<-t.release
	return t.LocalGitx.PushAdvance(ctx, remote, oid, branch, expectedOld)
}

func runGitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return string(out)
}
