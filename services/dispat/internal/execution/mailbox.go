// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// One endpoint, read and written as a mailbox.
//
// Everything a party does to a coordination branch happens here: create it,
// advance it, see what has moved, read what a tip carries, and close it
// again. The Git operations themselves are gitx's; what this file owns is the
// protocol's use of them, which is what neither party should be writing twice.
//
// Four properties are worth stating because they are the reason for the
// shape of the code rather than an accident of it. Every write is one
// compare-and-swap push, so a lost race is answered by re-reading rather than
// by retrying. A push whose outcome git could not report is settled by reading
// the remote, never by pushing again: the chain of one attempt is strictly
// linear, so the object this party wrote is either the tip, an ancestor of the
// tip, or not on the branch at all. Every read resolves an exact object id and
// then reads from that object, never from a ref whose tip can still move
// (§28.4). And every document is read with a stated ceiling, because a mailbox
// is written to by other machines.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// transportx is what a mailbox needs of a Git remote: the compare-and-swap
// pushes, the poll, the fetch into a private namespace and the batched close.
//
// It is declared here, at the consumer, rather than taken from gitx, because
// this is the list of operations the protocol actually depends on: a fake
// standing in for it in a test has to answer these and nothing else, and a
// method added to gitx for somebody else cannot silently become part of what
// a mailbox may do.
type transportx interface {
	PushCreate(ctx context.Context, remote, oid, branch string) error
	PushAdvance(ctx context.Context, remote, oid, branch, expectedOld string) error
	ListRemoteHeads(ctx context.Context, remote, pattern string) ([]gitx.RemoteHead, error)
	FetchRefs(ctx context.Context, remote string, branches []string) error
	DeleteRemoteBranchesLease(ctx context.Context, remote string, leases []gitx.BranchLease) ([]gitx.BranchOutcome, error)
	DeleteLocalTransportRefs(ctx context.Context, branches []string) error
	ResolveFetchedCommit(ctx context.Context, localRef, wantOID string, maxDepth int) error
	ResolveCommit(ctx context.Context, rev string) (string, error)
}

// GitMailbox is one endpoint together with the local object store the
// messages of that endpoint are written and read in.
//
// The two are separate on purpose. An orchestrator's store is the repository
// it is releasing, where transport objects are unreachable garbage the moment
// their refs are deleted; a serving node's store is a bare cache of its own,
// which is dispensable and can be thrown away between two messages. Neither
// ever grows a local branch: the only refs a mailbox writes locally are the
// fetched ones under refs/dispat-transport/, and they are deleted on close.
type GitMailbox struct {
	endpoint string
	remote   transportx
	plumbing *gitx.LocalGitx
	signer   *Signer
	log      zerolog.Logger
	maxDepth int
	// memo makes one mailbox serve several goroutines: a run dispatches its
	// tasks concurrently while one poller watches for their replies, and a
	// serving node answers a task while its own poll goes on. It guards the
	// three maps below and nothing else, and it is never held across a git
	// invocation: a push of a task's outputs can take as long as the outputs
	// are large, and a poll, a withdrawal or the read of an authorization
	// that expires in two minutes must not wait behind it. Pushes, listings,
	// object writes and reads are safe to run at once; what is not is two
	// writers of this store's own transport refs, which refs orders.
	memo     sync.Mutex
	observed map[string]string
	// quarantined are the branches whose objects this party could not fetch,
	// remembered so that the warning they produce is written once rather than
	// on every tick. A quarantined branch is still polled: what is refused is
	// the attempt to read it, and a branch that moves again is tried again.
	quarantined map[string]bool
	// fetched are the branches this process fetched into the transport
	// namespace, which are the local refs it removes again: on close, and as
	// soon as a poll finds the branch gone from the remote.
	fetched map[string]bool
	// refs serializes the writes this process makes to its own transport
	// refs, a fetch into them and their deletion. It is a channel rather than
	// a mutex so that a caller waiting for it leaves when its context ends.
	refs      chan struct{}
	fetchSize int
}

// maxChainDepth bounds how far below a fetched ref's tip an object may sit
// and still be read as part of that attempt. One attempt is at most
// assignment, claim, ready, go and result, on top of one source commit, so
// anything further away is not this protocol's chain.
const maxChainDepth = 8

// NewGitMailbox opens a mailbox on one endpoint, with git as both the
// transport and the local object store.
//
// The two roles are one argument because they are one repository: the objects
// a push sends are the objects the local store holds, and handing the
// protocol two different repositories would be handing it a store whose
// contents nothing guarantees.
func NewGitMailbox(endpoint string, git *gitx.LocalGitx, signer *Signer, log zerolog.Logger) *GitMailbox {
	return &GitMailbox{
		endpoint:    endpoint,
		remote:      git,
		plumbing:    git,
		signer:      signer,
		log:         log,
		maxDepth:    maxChainDepth,
		observed:    map[string]string{},
		quarantined: map[string]bool{},
		fetched:     map[string]bool{},
		refs:        make(chan struct{}, 1),
		fetchSize:   gitx.MaxTransportBatch,
	}
}

// PrepareAssignment writes one assignment into the local object store and
// answers the commit it will travel as. Nothing leaves this machine.
//
// Preparing and offering are two steps so that the caller can make the
// commit its own before anybody else can see it: the branch is registered with
// the poller, bound to this object and recorded for cleanup before the push,
// so a node quick enough to claim between the push and the next line is still
// heard, and a push whose outcome is unknown still leaves a ref this run
// closes.
func (m *GitMailbox) PrepareAssignment(ctx context.Context, message *Assignment) (string, error) {
	document, err := json.Marshal(message)
	if err != nil {
		return "", fmt.Errorf("execution: writing the assignment document: %w", err)
	}
	return m.commitMessage(ctx, MessageAssignment, document, nil, nil)
}

// Offer creates a branch at a prepared commit, create-only, and answers what
// became of it once the remote has been asked.
//
// Create-only is what makes two orchestrators offering the same name produce
// exactly one winner, and it is also why nothing here retries: a name that is
// taken is a name this run did not choose, and 128 bits of randomness say
// that did not happen. A push that reported no outcome is read back rather
// than pushed again. The error is the push's own, and nil only for an offer
// that landed on the first answer.
func (m *GitMailbox) Offer(ctx context.Context, branch, oid string) (pushResolution, error) {
	err := m.remote.PushCreate(ctx, m.endpoint, oid, branch)
	if err == nil {
		// Remembered as observed at the value this run put there, so the next
		// poll reports the branch only once the other side has moved it.
		m.rememberObserved(branch, oid)
		m.log.Debug().Str("branch", branch).Str("commit", oid).Msg("assignment created")
		return pushLanded, nil
	}
	resolution := m.resolveOwnPush(ctx, branch, oid, err)
	m.log.Debug().Err(err).Str("branch", branch).Str("commit", oid).
		Str("resolution", resolution.String()).Msg("the assignment push did not answer cleanly")
	return resolution, fmt.Errorf("execution: offering %s: %w", branch, err)
}

// Advance writes the next message of a branch this party may write, under a
// lease over the value it believes the branch holds.
//
// A push that did not report success is settled by reading the remote and
// never by pushing again: a message this party wrote may already be the tip,
// or sit under whatever the other party wrote on top of it, and either way it
// landed. A message that provably did not land, and one whose fate the reads
// could not establish, come back as a messagePushError saying which.
func (m *GitMailbox) Advance(ctx context.Context, branch, expectedOld string, kind MessageKind,
	document []byte, carried []gitx.TreeEntry) (string, error) {
	oid, err := m.commitMessage(ctx, kind, document, []string{expectedOld}, carried)
	if err != nil {
		return "", err
	}
	if err := m.remote.PushAdvance(ctx, m.endpoint, oid, branch, expectedOld); err != nil {
		resolution := m.resolveOwnPush(ctx, branch, oid, err)
		if resolution != pushLanded {
			return "", &messagePushError{
				cause:      fmt.Errorf("execution: advancing %s to %s: %w", branch, kind, err),
				resolution: resolution, oid: oid,
			}
		}
		m.log.Debug().Err(err).Str("branch", branch).Str("commit", oid).
			Msg("the update landed although its push did not say so")
	}
	// The value this party wrote, and never a tip the settling read found: a
	// message the other party put on top of it is still to be delivered.
	m.rememberObserved(branch, oid)
	m.log.Debug().Str("branch", branch).Str("commit", oid).Str("message", string(kind)).
		Msg("coordination branch advanced")
	return oid, nil
}

// pushResolution is what a push this party made turned out to be once the
// remote had been asked: on the branch, provably not on it, or unknown.
type pushResolution int

const (
	pushLanded pushResolution = iota + 1
	pushNotLanded
	pushUnknown
)

// String names a resolution in a log line.
func (r pushResolution) String() string {
	switch r {
	case pushLanded:
		return "landed"
	case pushNotLanded:
		return "not-landed"
	default:
		return "unknown"
	}
}

// messagePushError is a message this party wrote whose push did not land, or
// whose landing could not be established. A local failure preparing the
// message is a plain error and never this type: no push happened.
//
// The resolution is the whole point. A message that provably never became the
// branch's value can be answered as if it was never written; one whose fate
// is unknown may already be what the other party is acting on, and even a
// branch later removed or reset does not say otherwise. The object id is kept
// so that a caller can recognise the message if it surfaces later.
type messagePushError struct {
	cause      error
	resolution pushResolution
	oid        string
}

func (e *messagePushError) Error() string { return e.cause.Error() }
func (e *messagePushError) Unwrap() error { return e.cause }

// resolvePushError is the resolution a failed advance reports: the push's own
// for a messagePushError, and not landed for a message that never left this
// machine.
func resolvePushError(err error) pushResolution {
	var pushed *messagePushError
	if !errors.As(err, &pushed) {
		return pushNotLanded
	}
	return pushed.resolution
}

// ownPushReadPauses are the waits between the reads that settle a push whose
// outcome git could not report. A refusal gets one read; an unknown outcome
// gets three, because a push the network delayed can land after its client
// gave up, and the second and third reads are what see it.
var ownPushReadPauses = []time.Duration{time.Second, 2 * time.Second}

// resolveOwnPush settles one push this party made by reading the remote, and
// never pushes anything.
//
// A read finds the branch at the object (landed), with the object on the
// tip's first-parent chain (landed, and the other party already answered
// it), or elsewhere. A coordination chain is strictly linear, an assignment a
// root and every later message a single parent over the lease it was written
// under, so ancestry is the whole of the question. A definitive refusal whose
// tip does not descend from the object did not land; everything else, an
// unreadable remote included, is unknown.
//
// It never writes the memo. A tip found here is not a tip this party has
// handled: the poll has to deliver it, and remembering it here would make the
// poll skip it.
func (m *GitMailbox) resolveOwnPush(ctx context.Context, branch, oid string, pushErr error) pushResolution {
	isRefused := errors.Is(pushErr, gitx.ErrLeaseRejected) || errors.Is(pushErr, gitx.ErrRemoteRefused)
	reads := 1 + len(ownPushReadPauses)
	if isRefused {
		reads = 1
	}
	for read := 0; read < reads; read++ {
		if read > 0 && !pauseContext(ctx, ownPushReadPauses[read-1]) {
			break
		}
		isLanded, err := m.readOwnPush(ctx, branch, oid)
		if err != nil {
			m.log.Debug().Err(err).Str("branch", branch).Str("commit", oid).
				Msg("the remote could not be read to settle a push")
			continue
		}
		if isLanded {
			return pushLanded
		}
		if isRefused {
			return pushNotLanded
		}
	}
	return pushUnknown
}

// readOwnPush is one read of the remote on behalf of resolveOwnPush: whether
// the object is the branch's tip or on its first-parent chain.
func (m *GitMailbox) readOwnPush(ctx context.Context, branch, oid string) (bool, error) {
	tip, err := m.readRemoteTip(ctx, branch)
	if err != nil || tip == "" {
		return false, err
	}
	if tip == oid {
		return true, nil
	}
	if err := m.fetchRefs(ctx, []string{branch}); err != nil {
		return false, err
	}
	err = m.remote.ResolveFetchedCommit(ctx, gitx.TransportRefPrefix+branch, oid, m.maxDepth)
	if errors.Is(err, gitx.ErrCommitNotOnRef) {
		return false, nil
	}
	return err == nil, err
}

// readRemoteTip is where one branch sits on the remote, and the empty string
// for a branch the remote does not advertise.
func (m *GitMailbox) readRemoteTip(ctx context.Context, branch string) (string, error) {
	heads, err := m.remote.ListRemoteHeads(ctx, m.endpoint, "refs/heads/"+branch)
	if err != nil {
		return "", fmt.Errorf("execution: reading %s: %w", branch, err)
	}
	for _, head := range heads {
		if head.Name == branch {
			return head.OID, nil
		}
	}
	return "", nil
}

// pauseContext waits for one pause and reports whether the context let it
// finish.
func pauseContext(ctx context.Context, pause time.Duration) bool {
	timer := time.NewTimer(pause)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// Observe polls the mailbox once and answers the branches whose tip is new or
// has moved since the last poll, their objects already fetched.
//
// One ls-remote per poll, whatever the mailbox holds, and a memo of what each
// branch was last seen at: an idle node with a hundred stale branches in its
// mailbox costs one invocation per tick and fetches nothing. Fetching is
// batched because a fetch names its refs on a command line, and a mailbox
// with more branches than one batch is fetched over several polls rather than
// in one unbounded invocation.
//
// isWanted, when it is given, decides before anything is fetched which of the
// moved branches this caller has any use for. An orchestrator watching a
// shared mailbox is interested in its own attempts and nothing else: another
// run's output trees are not fetched into its store, and a branch it does not
// want is not remembered either, so that it is reported the moment it becomes
// wanted. A branch the remote no longer advertises is forgotten, and its
// fetched ref is removed.
func (m *GitMailbox) Observe(ctx context.Context, pattern string, isWanted func(string) bool) ([]gitx.RemoteHead, error) {
	heads, err := m.remote.ListRemoteHeads(ctx, m.endpoint, pattern)
	if err != nil {
		return nil, fmt.Errorf("execution: polling %s: %w", gitx.RedactURL(m.endpoint), err)
	}
	moved, vanished := m.resolveMoved(pattern, heads, isWanted)
	m.dropVanished(ctx, vanished)
	if len(moved) > m.fetchSize {
		moved = moved[:m.fetchSize]
	}
	if len(moved) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(moved))
	for _, head := range moved {
		names = append(names, head.Name)
	}
	if err := m.fetchRefs(ctx, names); err != nil {
		return m.fetchSeparately(ctx, moved, err)
	}
	m.memo.Lock()
	defer m.memo.Unlock()
	for _, head := range moved {
		m.observed[head.Name] = head.OID
		delete(m.quarantined, head.Name)
	}
	return moved, nil
}

// resolveMoved compares one listing with the memo: the wanted branches whose
// tip is not the one last seen, and the fetched branches the pattern covers
// that the remote no longer advertises.
func (m *GitMailbox) resolveMoved(pattern string, heads []gitx.RemoteHead,
	isWanted func(string) bool) ([]gitx.RemoteHead, []string) {
	m.memo.Lock()
	defer m.memo.Unlock()
	present := make(map[string]bool, len(heads))
	moved := make([]gitx.RemoteHead, 0, len(heads))
	for _, head := range heads {
		present[head.Name] = true
		if m.observed[head.Name] == head.OID {
			m.log.Trace().Str("branch", head.Name).Msg("coordination branch unchanged")
			continue
		}
		if isWanted != nil && !isWanted(head.Name) {
			continue
		}
		moved = append(moved, head)
	}
	// A branch the mailbox no longer advertises is forgotten, so that a name
	// closed and later reused is not mistaken for one this node already saw.
	// Only the branches this listing could have named are asked about: a poll
	// of one branch says nothing about any other.
	for branch := range m.observed {
		if !present[branch] && isBranchListed(pattern, branch) {
			delete(m.observed, branch)
		}
	}
	var vanished []string
	for branch := range m.fetched {
		if !present[branch] && isBranchListed(pattern, branch) {
			vanished = append(vanished, branch)
		}
	}
	return moved, vanished
}

// isBranchListed reports whether a listing made with pattern would have named
// branch: the pattern is a ref, or a ref prefix ending in one wildcard.
func isBranchListed(pattern, branch string) bool {
	ref := "refs/heads/" + branch
	if prefix, isPrefix := strings.CutSuffix(pattern, "*"); isPrefix {
		return strings.HasPrefix(ref, prefix)
	}
	return ref == pattern
}

// dropVanished removes the fetched refs of branches the remote no longer
// holds. A failure is a leftover in a private namespace, retried by the next
// poll that finds the branch still gone, and never a failed poll.
func (m *GitMailbox) dropVanished(ctx context.Context, vanished []string) {
	if len(vanished) == 0 {
		return
	}
	if err := m.deleteLocalRefs(ctx, vanished); err != nil {
		m.log.Debug().Err(err).Int("branches", len(vanished)).
			Msg("the fetched refs of closed coordination branches were not removed")
	}
}

// fetchSeparately fetches one branch at a time after the batch failed, and
// answers the ones whose objects are here.
//
// The batch is the whole reason this exists. A fetch names every moved branch
// in one invocation, so a single ref nobody can fetch (a branch deleted
// between the poll and the fetch, an object a sender never pushed, a relay
// copied from an endpoint this party cannot read) fails the invocation and
// with it every other branch of the same tick. A node whose namespace holds
// one such ref would then never see any of its work again, which is a stall
// rather than a refusal. So the second attempt asks per branch: the ones that
// answer are handed on, and the ones that do not are left where they are, with
// one warning each rather than one per tick.
func (m *GitMailbox) fetchSeparately(ctx context.Context, moved []gitx.RemoteHead,
	batch error) ([]gitx.RemoteHead, error) {
	if err := ctx.Err(); err != nil {
		// The batch was cut short because the poll is stopping: that says
		// nothing about any branch, so none is quarantined or reported.
		return nil, err
	}
	m.log.Debug().Err(batch).Int("branches", len(moved)).
		Msg("the batched fetch failed, so the branches are fetched one at a time")
	fetched := make([]gitx.RemoteHead, 0, len(moved))
	for _, head := range moved {
		if err := m.fetchRefs(ctx, []string{head.Name}); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				// Likewise for one branch: a fetch the stopping poll
				// interrupted leaves the branch to be read next time.
				return fetched, ctxErr
			}
			m.quarantineBranch(head, err)
			continue
		}
		m.memo.Lock()
		m.observed[head.Name] = head.OID
		delete(m.quarantined, head.Name)
		m.memo.Unlock()
		fetched = append(fetched, head)
	}
	if len(fetched) == 0 && ctx.Err() == nil && len(moved) == 1 {
		// One branch moved and it is the one that cannot be read: the caller
		// gets the failure rather than an empty poll, so a mailbox that is
		// actually unreachable is still reported as such.
		return nil, fmt.Errorf("execution: fetching %s: %w", moved[0].Name, batch)
	}
	return fetched, nil
}

// quarantineBranch remembers a branch whose objects could not be fetched or
// read, and says so once.
//
// The tip is remembered as observed so that the poll stops offering the same
// unreadable object every tick; the branch itself is not forgotten, so a
// sender that pushes the missing objects, or moves the branch on, is noticed
// the next time it does.
func (m *GitMailbox) quarantineBranch(head gitx.RemoteHead, err error) {
	m.memo.Lock()
	defer m.memo.Unlock()
	m.observed[head.Name] = head.OID
	if m.quarantined[head.Name] {
		m.log.Trace().Err(err).Str("branch", head.Name).Str("commit", head.OID).
			Msg("the coordination branch is still unreadable")
		return
	}
	m.quarantined[head.Name] = true
	m.log.Warn().Err(err).Str("branch", head.Name).Str("commit", head.OID).
		Str("code", CodeTransportRetained).Str("category", CategoryTransportCleanup).
		Msg("a coordination branch could not be read and is left alone for this run")
}

// Reconsider forgets what one branch was last seen at, so that the next poll
// reports it again although nothing on it has moved.
//
// It exists for the one thing a node does with work it cannot take right now:
// a full node leaves the assignment exactly where the orchestrator put it, and
// without this the memo would make "unchanged since the last poll" mean
// "already dealt with" for the rest of the process's life, so queued work
// would wait for a push nobody is going to make.
func (m *GitMailbox) Reconsider(branch string) {
	m.memo.Lock()
	defer m.memo.Unlock()
	delete(m.observed, branch)
}

// rememberObserved records the value this party wrote to a branch, so that
// the poll reports the branch only once somebody else moves it.
func (m *GitMailbox) rememberObserved(branch, oid string) {
	m.memo.Lock()
	defer m.memo.Unlock()
	m.observed[branch] = oid
}

// Fetch brings named coordination branches into this node's store without
// going through the poll, which is what a task does with the extra input
// states its assignment named: they are branches nobody advertises to this
// node's pattern and they are needed before a single command runs.
func (m *GitMailbox) Fetch(ctx context.Context, branches []string) error {
	if len(branches) == 0 {
		return nil
	}
	if err := m.fetchRefs(ctx, branches); err != nil {
		return fmt.Errorf("execution: fetching %d input states: %w", len(branches), err)
	}
	return nil
}

// fetchRefs fetches branches into the transport namespace, one writer of it at
// a time, and remembers them as this process's to remove. A fetch that failed
// may still have written some of them, so they are remembered either way.
func (m *GitMailbox) fetchRefs(ctx context.Context, branches []string) error {
	if err := m.holdRefs(ctx); err != nil {
		return err
	}
	defer m.releaseRefs()
	err := m.remote.FetchRefs(ctx, m.endpoint, branches)
	m.memo.Lock()
	for _, branch := range branches {
		m.fetched[branch] = true
	}
	m.memo.Unlock()
	return err
}

// deleteLocalRefs removes fetched branches from the transport namespace, one
// writer of it at a time, and forgets them once they are gone.
func (m *GitMailbox) deleteLocalRefs(ctx context.Context, branches []string) error {
	if err := m.holdRefs(ctx); err != nil {
		return err
	}
	defer m.releaseRefs()
	if err := m.remote.DeleteLocalTransportRefs(ctx, branches); err != nil {
		return err
	}
	m.memo.Lock()
	for _, branch := range branches {
		delete(m.fetched, branch)
	}
	m.memo.Unlock()
	return nil
}

// holdRefs waits for the transport namespace, or for the context to end.
func (m *GitMailbox) holdRefs(ctx context.Context) error {
	select {
	case m.refs <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("execution: waiting for the transport refs: %w", ctx.Err())
	}
}

func (m *GitMailbox) releaseRefs() { <-m.refs }

// Forget drops what the memo remembers, which is what a node does when the
// object store behind it has been rebuilt: the tips are still where they
// were, but the objects they name are no longer here to be read.
func (m *GitMailbox) Forget() {
	m.memo.Lock()
	defer m.memo.Unlock()
	clear(m.observed)
	// A branch that could not be fetched into the store that has just been
	// thrown away is a branch nobody has tried to fetch into this one, and a
	// ref that store held is gone with it.
	clear(m.quarantined)
	clear(m.fetched)
}

// Inspect resolves an observed tip to the message it carries and to the step
// that produced it.
//
// The object is proven to be on the fetched ref's first-parent chain before
// anything is read from it, so a tip that moved between the poll and the
// fetch cannot make this node read somebody else's object. What comes back
// describes the chain and holds the blobs of the message, which is everything
// the state machine and the acceptance rules need.
func (m *GitMailbox) Inspect(ctx context.Context, head gitx.RemoteHead) (ChainTip, error) {
	localRef := gitx.TransportRefPrefix + head.Name
	if err := m.remote.ResolveFetchedCommit(ctx, localRef, head.OID, m.maxDepth); err != nil {
		return ChainTip{}, fmt.Errorf("execution: resolving %s on %s: %w", head.OID, head.Name, err)
	}
	return m.readChainTip(ctx, head.Name, head.OID)
}

// readChainTip is one commit read as a step of a chain: what it carries, and
// what its first parent carried.
func (m *GitMailbox) readChainTip(ctx context.Context, branch, oid string) (ChainTip, error) {
	tip := ChainTip{Branch: branch, OID: oid}
	carried, err := m.readTree(ctx, oid)
	if err != nil {
		return ChainTip{}, err
	}
	tip.Kind, tip.document, tip.signature = carried.kind, carried.document, carried.signature
	tip.isProtocol = carried.isProtocol
	parent, err := m.remote.ResolveCommit(ctx, oid+"^")
	if err != nil {
		// No first parent: the commit is a root, which is what a probe
		// assignment is. A parent that cannot be read for any other reason
		// would have failed the chain resolution above.
		return tip, nil
	}
	previous, err := m.readTree(ctx, parent)
	if err != nil {
		return ChainTip{}, err
	}
	tip.Previous, tip.PreviousOID = previous.kind, parent
	return tip, nil
}

// InspectChain answers the first-parent ancestors of an observed tip, oldest
// first, as far below it as one attempt's chain can reach.
//
// It exists because the tip of a coordination branch is not the only place an
// attempt's answer can be. A mailbox is writable by whoever can push to it, so
// anybody may put a commit of their own on top of an authentic result: the
// party waiting for that result would see a tip carrying nothing it can act on
// and wait out the whole task deadline, which turns one push into a lost
// attempt and a node taken out of the pool. Reading the chain instead makes
// the answer findable wherever on it the writer left it, while changing
// nothing about what is believed: every ancestor is held to the same
// signature, header and chain rules as a tip.
//
// The tip itself is not in the answer, because the caller has already tried
// it, and the walk is bounded by the same depth a fetched object is resolved
// within: an attempt is at most assignment, claim, ready, go and result.
func (m *GitMailbox) InspectChain(ctx context.Context, head gitx.RemoteHead) ([]ChainTip, error) {
	oids := make([]string, 0, m.maxDepth)
	for oid := head.OID; len(oids) < m.maxDepth; {
		parent, err := m.remote.ResolveCommit(ctx, oid+"^")
		if err != nil {
			// A root commit, or a history that ends before the bound: either
			// way there is nothing further down to read.
			break
		}
		oids = append(oids, parent)
		oid = parent
	}
	ancestors := make([]ChainTip, 0, len(oids))
	for index := len(oids) - 1; index >= 0; index-- {
		tip, err := m.readChainTip(ctx, head.Name, oids[index])
		if err != nil {
			return nil, err
		}
		ancestors = append(ancestors, tip)
	}
	return ancestors, nil
}

// Reread answers where one branch sits on the remote right now, with its
// objects fetched, whatever the memo remembers.
//
// It is the poll's opposite and both are needed. Observe answers "what has
// moved since I last looked", which is the right question for a loop watching
// a whole namespace and the wrong one for a party waiting on a branch of its
// own: whichever goroutine polled first would have consumed the movement, and
// the waiter would wait for a push that already happened. This asks the remote
// about one branch and believes the answer, which is also what makes it the
// fence a publisher re-reads its own tip with immediately before the
// irreversible command (§28.6).
func (m *GitMailbox) Reread(ctx context.Context, branch string) (gitx.RemoteHead, error) {
	heads, err := m.remote.ListRemoteHeads(ctx, m.endpoint, "refs/heads/"+branch)
	if err != nil {
		return gitx.RemoteHead{}, fmt.Errorf("execution: re-reading %s: %w", branch, err)
	}
	for _, head := range heads {
		if head.Name != branch {
			continue
		}
		if err := m.fetchRefs(ctx, []string{branch}); err != nil {
			return gitx.RemoteHead{}, fmt.Errorf("execution: fetching %s: %w", branch, err)
		}
		// Remembered at what was just read, so the loop watching the whole
		// namespace does not report a movement this reader has already taken.
		m.rememberObserved(branch, head.OID)
		return head, nil
	}
	// A branch the remote no longer advertises: deleted under this party,
	// which is a state the caller decides about rather than a failure here.
	return gitx.RemoteHead{}, nil
}

// transportTree is what one transport commit's tree holds: which message it
// carries, the blobs of that message, and whether the tree is this protocol's
// at all.
type transportTree struct {
	kind       MessageKind
	document   string
	signature  string
	isProtocol bool
}

// readTree answers which message a transport commit carries, and the blobs of
// the document and its signature. A commit carrying none is the source
// snapshot an assignment sits on, and answers the empty kind.
func (m *GitMailbox) readTree(ctx context.Context, oid string) (transportTree, error) {
	plumbing := gitx.NewPlumbing(m.plumbing)
	signatures := map[MessageKind]string{}
	carried := transportTree{}
	documents := 0
	plumbing.ListTree(ctx, oid, func(entry gitx.TreeEntry) error {
		name, isMessage := strings.CutPrefix(entry.Name, messageDir+"/")
		if !isMessage {
			return nil
		}
		carried.isProtocol = true
		if found, isDocument := strings.CutSuffix(name, ".json"); isDocument {
			carried.kind, carried.document, documents = MessageKind(found), entry.OID, documents+1
			return nil
		}
		if found, isSignature := strings.CutSuffix(name, ".sig"); isSignature {
			signatures[MessageKind(found)] = entry.OID
		}
		return nil
	})
	if err := plumbing.Err(); err != nil {
		return transportTree{}, fmt.Errorf("execution: listing the transport tree of %s: %w", oid, err)
	}
	// Exactly one message, with its signature beside it. Anything else is not
	// a commit this protocol wrote, and guessing which of two documents was
	// meant is precisely the guess an attacker would be making it take.
	if documents != 1 || signatures[carried.kind] == "" {
		return transportTree{isProtocol: carried.isProtocol}, nil
	}
	carried.signature = signatures[carried.kind]
	return carried, nil
}

// Read answers the verified document of an inspected tip, refusing anything
// larger than maxBytes before a byte of it is read.
//
// Verification happens here rather than in the caller so that no path exists
// on which a document is parsed before it is known to be authentic: what
// comes back is bytes this node's own secret signed, or a Rejection naming
// why they are not.
func (m *GitMailbox) Read(ctx context.Context, tip ChainTip, maxBytes int64) ([]byte, error) {
	if tip.Kind == "" {
		return nil, &Rejection{Reason: ReasonUnreadable}
	}
	plumbing := gitx.NewPlumbing(m.plumbing)
	var document, signature bytes.Buffer
	plumbing.ReadBlob(ctx, tip.document, &document, maxBytes)
	plumbing.ReadBlob(ctx, tip.signature, &signature, maxSignatureBytes)
	if err := plumbing.Err(); err != nil {
		if errors.Is(err, gitx.ErrTransportLimit) {
			return nil, &Rejection{Reason: ReasonOversize}
		}
		return nil, fmt.Errorf("execution: reading the %s of %s: %w", tip.Kind, tip.Branch, err)
	}
	if !m.signer.IsSignatureValid(tip.Kind, document.Bytes(), strings.TrimSpace(signature.String())) {
		return nil, &Rejection{Reason: ReasonSignature}
	}
	return document.Bytes(), nil
}

// maxSignatureBytes bounds the signature blob. A signature is 64 hex
// characters and a newline at most, so the ceiling is small enough that a
// blob claiming to be one and being something else is refused at the read.
const maxSignatureBytes = 128

// Close removes the coordination branches this party owns, in one push per
// batch, and takes the fetched refs out of the local store.
//
// It answers what became of each branch rather than one error. Every batch is
// attempted whatever became of the one before it, and the fetched refs are
// removed whatever became of the pushes. A lease the remote refused because
// the branch is already gone is a branch that is closed; one refused because
// the branch moved, or refused by a server rule, is reported as retained
// rather than retried; and a push that reported nothing is settled by reading
// the remote.
func (m *GitMailbox) Close(ctx context.Context, leases []gitx.BranchLease) ([]gitx.BranchOutcome, error) {
	outcomes := make([]gitx.BranchOutcome, 0, len(leases))
	var failures []error
	for start := 0; start < len(leases); start += gitx.MaxTransportBatch {
		end := min(start+gitx.MaxTransportBatch, len(leases))
		batch, err := m.remote.DeleteRemoteBranchesLease(ctx, m.endpoint, leases[start:end])
		if err != nil {
			failures = append(failures, fmt.Errorf("execution: closing %d coordination branches: %w", end-start, err))
			batch = formatUnknownOutcomes(leases[start:end])
		}
		outcomes = append(outcomes, batch...)
	}
	outcomes = m.settleDeletes(ctx, outcomes)
	if err := m.forgetFetched(ctx, leases); err != nil {
		failures = append(failures, err)
	}
	return outcomes, errors.Join(failures...)
}

// localCleanupTimeout bounds the removal of this process's fetched refs on
// close. It is a local ref transaction, so the bound only has to outlast a
// busy disk.
const localCleanupTimeout = 30 * time.Second

// forgetFetched removes every ref this process fetched, the closed branches'
// and any other, and forgets what the memo held about the closed ones.
//
// It runs on a context of its own, detached from the caller's and bounded,
// because it is the one part of a close that needs nothing from the network:
// a close whose pushes ran out of time still leaves no transport ref behind in
// the repository being released.
func (m *GitMailbox) forgetFetched(ctx context.Context, leases []gitx.BranchLease) error {
	local, done := context.WithTimeout(context.WithoutCancel(ctx), localCleanupTimeout)
	defer done()
	m.memo.Lock()
	branches := make([]string, 0, len(leases)+len(m.fetched))
	named := make(map[string]bool, len(leases)+len(m.fetched))
	for _, lease := range leases {
		delete(m.observed, lease.Branch)
		named[lease.Branch] = true
		branches = append(branches, lease.Branch)
	}
	for branch := range m.fetched {
		if !named[branch] {
			branches = append(branches, branch)
		}
	}
	m.memo.Unlock()
	if len(branches) == 0 {
		return nil
	}
	sort.Strings(branches)
	if err := m.deleteLocalRefs(local, branches); err != nil {
		return fmt.Errorf("execution: forgetting %d fetched coordination branches: %w", len(branches), err)
	}
	return nil
}

// formatUnknownOutcomes is a batch the transport refused to attempt at all,
// answered branch by branch as nothing known.
func formatUnknownOutcomes(leases []gitx.BranchLease) []gitx.BranchOutcome {
	outcomes := make([]gitx.BranchOutcome, 0, len(leases))
	for _, lease := range leases {
		outcomes = append(outcomes, gitx.BranchOutcome{Branch: lease.Branch, Result: gitx.BranchUnknown})
	}
	return outcomes
}

// settleDeletes reads the remote for every delete that did not answer
// cleanly and turns the ones whose branch is gone into deletions.
//
// A stale lease gets one read, because git answers a lease over a ref that is
// already gone with the same refusal as a lease over a ref that moved. An
// unknown outcome gets three, as an unknown push does. A branch still there
// keeps the answer it had, which the caller reports as retained, and a
// refusal by a server rule is not read at all: the ref is still there by
// definition.
func (m *GitMailbox) settleDeletes(ctx context.Context, outcomes []gitx.BranchOutcome) []gitx.BranchOutcome {
	pending := func(outcome gitx.BranchOutcome, read int) bool {
		return outcome.Result == gitx.BranchUnknown || (outcome.Result == gitx.BranchStale && read == 0)
	}
	for read := 0; read <= len(ownPushReadPauses); read++ {
		var waiting []string
		for _, outcome := range outcomes {
			if pending(outcome, read) {
				waiting = append(waiting, outcome.Branch)
			}
		}
		if len(waiting) == 0 {
			break
		}
		if read > 0 && !pauseContext(ctx, ownPushReadPauses[read-1]) {
			break
		}
		present, err := m.readPresentBranches(ctx, waiting)
		if err != nil {
			m.log.Debug().Err(err).Int("branches", len(waiting)).
				Msg("the remote could not be read to settle a delete")
			continue
		}
		for index, outcome := range outcomes {
			if pending(outcome, read) && !present[outcome.Branch] {
				outcomes[index].Result = gitx.BranchDeleted
			}
		}
	}
	return outcomes
}

// readPresentBranches is which of the named branches the remote advertises,
// in one listing: the branch itself when there is one, and every
// coordination branch of the endpoint otherwise.
func (m *GitMailbox) readPresentBranches(ctx context.Context, branches []string) (map[string]bool, error) {
	pattern := "refs/heads/" + branchPrefix + "*"
	if len(branches) == 1 {
		pattern = "refs/heads/" + branches[0]
	}
	heads, err := m.remote.ListRemoteHeads(ctx, m.endpoint, pattern)
	if err != nil {
		return nil, fmt.Errorf("execution: reading %d coordination branches: %w", len(branches), err)
	}
	present := make(map[string]bool, len(heads))
	for _, head := range heads {
		present[head.Name] = true
	}
	return present, nil
}

// Withdraw deletes one coordination branch under a lease over the object this
// party believes it holds, and reports whether the branch is gone.
//
// It is the fence of §28.6 rather than a tidy-up, which is why it is a lease
// and not a force: a branch still at the object this run last wrote is a
// branch nobody has claimed or answered, so deleting it provably ends the
// attempt; a branch that has moved is one somebody is working on, and the
// delete must fail rather than take the work away from under them. A branch
// that is already gone is withdrawn as surely as one this call removed.
func (m *GitMailbox) Withdraw(ctx context.Context, branch, expectedOld string) (bool, error) {
	outcomes, err := m.remote.DeleteRemoteBranchesLease(ctx, m.endpoint,
		[]gitx.BranchLease{{Branch: branch, ExpectedOld: expectedOld}})
	if err != nil {
		return false, fmt.Errorf("execution: revoking %s: %w", branch, err)
	}
	for _, outcome := range m.settleDeletes(ctx, outcomes) {
		if outcome.Branch != branch || outcome.Result != gitx.BranchDeleted {
			continue
		}
		m.memo.Lock()
		delete(m.observed, branch)
		delete(m.quarantined, branch)
		m.memo.Unlock()
		if err := m.deleteLocalRefs(ctx, []string{branch}); err != nil {
			// The remote ref is gone, which is the whole of the fence; a
			// fetched ref left in this store is unreachable garbage.
			m.log.Debug().Err(err).Str("branch", branch).
				Msg("the fetched ref of a revoked branch was not removed")
		}
		return true, nil
	}
	return false, nil
}

// commitMessage writes one message as a transport commit: the document, its
// signature, the `dispat` folder holding both, whatever else the message
// carries beside them, and the commit over all of it. The tree is built
// innermost first because git's mktree writes one level, which is also what
// keeps a path separator out of every entry name.
//
// What a message carries beside itself is build output, and it travels in the
// same commit rather than in one of its own for one reason: a push sends the
// objects a commit needs, so one compare-and-swap push moves the report and
// the bytes it is about together or moves neither. Nothing is ever trusted for
// being in that tree: what a reader believes is the signed document, which
// names the tree by object id.
func (m *GitMailbox) commitMessage(ctx context.Context, kind MessageKind, document []byte,
	parents []string, carried []gitx.TreeEntry) (string, error) {
	return formatMessageCommit(ctx, m.plumbing, m.signer, kind, document, parents, carried)
}

// formatMessageCommit writes one protocol message into a local object store
// and answers the commit that carries it.
//
// It is a function of a store rather than a method of a mailbox because one
// writer has no mailbox at all: a build the run placed on the orchestrator
// produces a result its consumers read exactly as they read a worker's, and
// that result is written into the repository the outputs were captured in and
// relayed from there. Everything about the shape of the commit is the same,
// which is the point of it being one function.
func formatMessageCommit(ctx context.Context, git *gitx.LocalGitx, signer *Signer,
	kind MessageKind, document []byte, parents []string, carried []gitx.TreeEntry) (string, error) {
	plumbing := gitx.NewPlumbing(git)
	documentOID := plumbing.HashObject(ctx, bytes.NewReader(document))
	signatureOID := plumbing.HashObject(ctx, strings.NewReader(signer.Sign(kind, document)))
	inner := plumbing.MakeTree(ctx, []gitx.TreeEntry{
		{Mode: gitx.TreeModeFile, Type: "blob", OID: documentOID, Name: string(kind) + ".json"},
		{Mode: gitx.TreeModeFile, Type: "blob", OID: signatureOID, Name: string(kind) + ".sig"},
	})
	tree := plumbing.MakeTree(ctx, append([]gitx.TreeEntry{
		{Mode: gitx.TreeModeDir, Type: "tree", OID: inner, Name: messageDir},
	}, carried...))
	oid := plumbing.CommitTree(ctx, tree, parents, string(kind))
	if err := plumbing.Err(); err != nil {
		return "", fmt.Errorf("execution: writing the %s message: %w", kind, err)
	}
	return oid, nil
}
