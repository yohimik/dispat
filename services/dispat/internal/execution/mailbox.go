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
// Three properties are worth stating because they are the reason for the
// shape of the code rather than an accident of it. Every write is one
// compare-and-swap push, so a lost race is answered by re-reading rather than
// by retrying. Every read resolves an exact object id and then reads from
// that object, never from a ref whose tip can still move (§28.4). And every
// document is read with a stated ceiling, because a mailbox is written to by
// other machines.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

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
	// mu makes one mailbox serve several goroutines: a run dispatches its
	// tasks concurrently while one poller watches for their replies, and a
	// serving node answers a task while its own poll goes on. What it guards
	// is the memo below and the ordering of the git invocations; both are the
	// mailbox's own state, so the lock is the mailbox's own rather than every
	// caller's problem.
	mu        sync.Mutex
	observed  map[string]string
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
		endpoint:  endpoint,
		remote:    git,
		plumbing:  git,
		signer:    signer,
		log:       log,
		maxDepth:  maxChainDepth,
		observed:  map[string]string{},
		fetchSize: gitx.MaxTransportBatch,
	}
}

// Assign writes one create-only assignment and answers the commit it created.
//
// Create-only is what makes two orchestrators offering the same name produce
// exactly one winner, and it is also why nothing here retries: a name that is
// taken is a name this run did not choose, and 128 bits of randomness say
// that did not happen.
func (m *GitMailbox) Assign(ctx context.Context, message *Assignment) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	document, err := json.Marshal(message)
	if err != nil {
		return "", fmt.Errorf("execution: writing the assignment document: %w", err)
	}
	oid, err := m.commitMessage(ctx, MessageAssignment, document, nil)
	if err != nil {
		return "", err
	}
	if err := m.remote.PushCreate(ctx, m.endpoint, oid, message.Branch); err != nil {
		return "", fmt.Errorf("execution: offering %s: %w", message.Branch, err)
	}
	// Remembered as observed at the value this run put there, so the next poll
	// reports the branch only once the other side has moved it.
	m.observed[message.Branch] = oid
	m.log.Debug().Str("worker", message.Node).Str("branch", message.Branch).
		Str("commit", oid).Str("kind", message.Kind).Msg("assignment created")
	return oid, nil
}

// Advance writes the next message of a branch this party may write, under a
// lease over the value it believes the branch holds.
//
// A rejected lease is read once and never retried: either the branch is
// already at the object this call was about to put there, which is this
// caller's own earlier push whose response was lost (the release lock reads
// its attempt back off the remote for exactly this reason), or somebody else
// moved it and the caller has to look at where the branch now is before it
// decides anything.
func (m *GitMailbox) Advance(ctx context.Context, branch, expectedOld string, kind MessageKind, document []byte) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	oid, err := m.commitMessage(ctx, kind, document, []string{expectedOld})
	if err != nil {
		return "", err
	}
	err = m.remote.PushAdvance(ctx, m.endpoint, oid, branch, expectedOld)
	if errors.Is(err, gitx.ErrLeaseRejected) {
		return m.resolveLostPush(ctx, branch, oid, err)
	}
	if err != nil {
		return "", fmt.Errorf("execution: advancing %s to %s: %w", branch, kind, err)
	}
	m.observed[branch] = oid
	m.log.Debug().Str("branch", branch).Str("commit", oid).Str("message", string(kind)).
		Msg("coordination branch advanced")
	return oid, nil
}

// resolveLostPush asks the remote, once, whether the rejected push had in
// fact already been applied. Finding the intended object on the branch is
// success; finding anything else is the rejection the caller was given, with
// what is actually there named in it.
func (m *GitMailbox) resolveLostPush(ctx context.Context, branch, oid string, rejected error) (string, error) {
	heads, err := m.remote.ListRemoteHeads(ctx, m.endpoint, "refs/heads/"+branch)
	if err != nil {
		return "", fmt.Errorf("execution: re-reading %s after a rejected update: %w", branch, err)
	}
	for _, head := range heads {
		if head.Name != branch || head.OID != oid {
			continue
		}
		m.observed[branch] = oid
		m.log.Debug().Str("branch", branch).Str("commit", oid).
			Msg("the rejected update was already on the branch")
		return oid, nil
	}
	return "", fmt.Errorf("execution: advancing %s: %w", branch, rejected)
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
func (m *GitMailbox) Observe(ctx context.Context, pattern string) ([]gitx.RemoteHead, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	heads, err := m.remote.ListRemoteHeads(ctx, m.endpoint, pattern)
	if err != nil {
		return nil, fmt.Errorf("execution: polling %s: %w", gitx.RedactURL(m.endpoint), err)
	}
	present := make(map[string]bool, len(heads))
	moved := make([]gitx.RemoteHead, 0, len(heads))
	for _, head := range heads {
		present[head.Name] = true
		if m.observed[head.Name] == head.OID {
			m.log.Trace().Str("branch", head.Name).Msg("coordination branch unchanged")
			continue
		}
		moved = append(moved, head)
	}
	// A branch the mailbox no longer advertises is forgotten, so that a name
	// closed and later reused is not mistaken for one this node already saw.
	for branch := range m.observed {
		if !present[branch] {
			delete(m.observed, branch)
		}
	}
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
	if err := m.remote.FetchRefs(ctx, m.endpoint, names); err != nil {
		return nil, fmt.Errorf("execution: fetching %d coordination branches: %w", len(names), err)
	}
	for _, head := range moved {
		m.observed[head.Name] = head.OID
	}
	return moved, nil
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
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.observed, branch)
}

// Fetch brings named coordination branches into this node's store without
// going through the poll, which is what a task does with the extra input
// states its assignment named: they are branches nobody advertises to this
// node's pattern and they are needed before a single command runs.
func (m *GitMailbox) Fetch(ctx context.Context, branches []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(branches) == 0 {
		return nil
	}
	if err := m.remote.FetchRefs(ctx, m.endpoint, branches); err != nil {
		return fmt.Errorf("execution: fetching %d input states: %w", len(branches), err)
	}
	return nil
}

// Forget drops what the memo remembers, which is what a node does when the
// object store behind it has been rebuilt: the tips are still where they
// were, but the objects they name are no longer here to be read.
func (m *GitMailbox) Forget() {
	m.mu.Lock()
	defer m.mu.Unlock()
	clear(m.observed)
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
	m.mu.Lock()
	defer m.mu.Unlock()
	localRef := gitx.TransportRefPrefix + head.Name
	if err := m.remote.ResolveFetchedCommit(ctx, localRef, head.OID, m.maxDepth); err != nil {
		return ChainTip{}, fmt.Errorf("execution: resolving %s on %s: %w", head.OID, head.Name, err)
	}
	tip := ChainTip{Branch: head.Name, OID: head.OID}
	carried, err := m.readTree(ctx, head.OID)
	if err != nil {
		return ChainTip{}, err
	}
	tip.Kind, tip.document, tip.signature = carried.kind, carried.document, carried.signature
	tip.isProtocol = carried.isProtocol
	parent, err := m.remote.ResolveCommit(ctx, head.OID+"^")
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
	tip.Previous = previous.kind
	return tip, nil
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
	m.mu.Lock()
	defer m.mu.Unlock()
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
// It answers what became of each branch rather than one error: a lease that
// no longer matches is a branch somebody else took over, which is reported as
// retained rather than retried, and the branches this run still owns are
// closed either way.
func (m *GitMailbox) Close(ctx context.Context, leases []gitx.BranchLease) ([]gitx.BranchOutcome, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(leases) == 0 {
		return nil, nil
	}
	outcomes := make([]gitx.BranchOutcome, 0, len(leases))
	branches := make([]string, 0, len(leases))
	for start := 0; start < len(leases); start += gitx.MaxTransportBatch {
		end := min(start+gitx.MaxTransportBatch, len(leases))
		batch, err := m.remote.DeleteRemoteBranchesLease(ctx, m.endpoint, leases[start:end])
		if err != nil {
			return outcomes, fmt.Errorf("execution: closing %d coordination branches: %w", end-start, err)
		}
		outcomes = append(outcomes, batch...)
	}
	for _, lease := range leases {
		delete(m.observed, lease.Branch)
		branches = append(branches, lease.Branch)
	}
	if err := m.remote.DeleteLocalTransportRefs(ctx, branches); err != nil {
		return outcomes, fmt.Errorf("execution: forgetting %d fetched coordination branches: %w", len(branches), err)
	}
	return outcomes, nil
}

// commitMessage writes one message as a transport commit: the document, its
// signature, the `dispat` folder holding both, and the commit that carries
// them. The tree is built innermost first because git's mktree writes one
// level, which is also what keeps a path separator out of every entry name.
func (m *GitMailbox) commitMessage(ctx context.Context, kind MessageKind, document []byte, parents []string) (string, error) {
	plumbing := gitx.NewPlumbing(m.plumbing)
	documentOID := plumbing.HashObject(ctx, bytes.NewReader(document))
	signatureOID := plumbing.HashObject(ctx, strings.NewReader(m.signer.Sign(kind, document)))
	inner := plumbing.MakeTree(ctx, []gitx.TreeEntry{
		{Mode: gitx.TreeModeFile, Type: "blob", OID: documentOID, Name: string(kind) + ".json"},
		{Mode: gitx.TreeModeFile, Type: "blob", OID: signatureOID, Name: string(kind) + ".sig"},
	})
	tree := plumbing.MakeTree(ctx, []gitx.TreeEntry{
		{Mode: gitx.TreeModeDir, Type: "tree", OID: inner, Name: messageDir},
	})
	oid := plumbing.CommitTree(ctx, tree, parents, string(kind))
	if err := plumbing.Err(); err != nil {
		return "", fmt.Errorf("execution: writing the %s message: %w", kind, err)
	}
	return oid, nil
}
