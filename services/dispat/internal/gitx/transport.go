// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

// The Git operations a distributed run coordinates through (CCME §28.4).
//
// Everything here is compare and swap. A coordination branch moves by one
// push carrying a lease over the value the pusher believed the ref held, so
// the remote decides who won and no reader has to trust a check it made a
// moment earlier. PushBranchAt is deliberately not used for any of it: it asks
// ls-remote whether a name is free and then pushes, which is two operations
// with a race between them, and the whole point of a mailbox is that two
// parties write to it.
//
// A rejection is told from a failure by machine-readable output rather than
// by git's wording. `git push --porcelain` prints one status line per ref with
// a flag in the first column — `!` rejected, `*` new, `=` already there, a
// space for a fast-forward, `-` for a delete — and it prints them even when it
// exits non-zero, which is what makes "somebody else holds this name" and "the
// remote is unreachable" two different answers instead of two spellings of one
// error.
//
// Three rules hold throughout. Every remote argument follows `--`, so a
// mailbox address can never be read as an option. Every remote that reaches a
// message goes through RedactURL. And every invocation goes through this
// package's own runner, so the trace line, the redaction and the cancellation
// of a transport call are the ones every other git call in dispat gets.

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/script"
)

// Transportx is the mailbox surface of a Git remote: create a branch nobody
// else holds, advance one this node still owns, read what is there, bring the
// objects home and close the branch again.
//
// It is declared here, beside its only implementation, for the same reason
// CommitProbex and UnionHistoryx are: this package owns what a Git remote can
// be asked, and the coordinator and the worker that consume it in later gates
// are two callers of one capability rather than two opinions about it.
//
// The local implementation is asserted to satisfy it here rather than where
// it is consumed, so that a method that drifts is a compilation failure in
// the file that owns both halves.
type Transportx interface {
	PushCreate(ctx context.Context, remote, oid, branch string) error
	PushAdvance(ctx context.Context, remote, oid, branch, expectedOld string) error
	DeleteRemoteBranchesLease(ctx context.Context, remote string, leases []BranchLease) ([]BranchOutcome, error)
	ListRemoteHeads(ctx context.Context, remote, pattern string) ([]RemoteHead, error)
	FetchRefs(ctx context.Context, remote string, branches []string) error
	DeleteLocalTransportRefs(ctx context.Context, branches []string) error
	ResolveFetchedCommit(ctx context.Context, localRef, wantOID string, maxDepth int) error
	RemoteTagObject(ctx context.Context, remote, tag string) (string, error)
}

var _ Transportx = (*LocalGitx)(nil)

// ErrLeaseRejected is the remote refusing a compare-and-swap push: the ref was
// not what the pusher leased it against, because somebody else created it,
// moved it, or removed it.
//
// It is a sentinel rather than a message because it is the one Git answer a
// coordination protocol acts on: the caller re-reads the branch and follows
// the state machine from wherever it now is, while any other failure is a
// transport problem and stops the operation.
var ErrLeaseRejected = errors.New("gitx: the remote rejected a leased ref update")

// ErrTransportLimit is a documented ceiling refusing what it bounds: too many
// remote refs, too many refs in one batch, or an object larger than the caller
// is willing to read. A mailbox is written to by other machines, so every read
// of it has a bound that is stated rather than discovered as a full disk.
var ErrTransportLimit = errors.New("gitx: a transport limit was exceeded")

// ErrCommitNotOnRef is a fetched commit that is not the one the caller was
// promised: absent from the object store, or present but not on the fetched
// ref's first-parent chain. A mailbox branch is an append-only first-parent
// chain by construction, so a commit reachable only some other way is one
// nobody in the protocol wrote there.
var ErrCommitNotOnRef = errors.New("gitx: the commit is not on the fetched ref")

// TransportRefPrefix is the private local namespace fetched coordination
// branches land in. It is neither refs/heads/ nor refs/tags/, so a fetched
// mailbox branch can never be mistaken for a branch of this checkout, can
// never be read back as a release tag, and is never pushed anywhere by the
// ordinary release paths.
const TransportRefPrefix = "refs/dispat-transport/"

// The ceilings ErrTransportLimit reports. They bound what a hostile or broken
// mailbox can make this process allocate, and they are constants rather than
// settings because they bound dispat's own memory rather than a user's
// policy: the configurable ceilings are the ones on build outputs.
const (
	// MaxRemoteHeads is the largest number of refs one ls-remote answer may
	// carry. A run's own mailbox holds a handful of branches per node.
	MaxRemoteHeads = 10000
	// MaxTransportBatch is the largest number of refs one fetch or one
	// batched delete may name, which keeps a command line bounded whatever
	// the mailbox holds. Callers batch.
	MaxTransportBatch = 32
	// MaxTreeEntryBytes bounds one entry of a streamed tree listing, so a
	// reply with no separator in it cannot be accumulated without end.
	MaxTreeEntryBytes = 1 << 16
)

// The identity every transport object is written under. A worker's cache
// clone is bare and has no user.name of its own, and a transport commit is
// dispat's own plumbing rather than anybody's work, so the identity is fixed
// here and passed in the child environment rather than read from a
// configuration that may not exist.
const (
	TransportIdentityName  = "dispat transport"
	TransportIdentityEmail = "transport@dispat.invalid"
)

// transportMessagePrefix begins every transport commit message. The text is
// inert on purpose: it carries no ":" and no separator line, so it cannot
// parse as a CCME unit and a coordination commit can never contribute a bump
// to anything that reads the history it sits in.
const transportMessagePrefix = "dispat transport "

// BranchLease is one ref of a batched delete with the value its owner
// believes it holds. Deleting without the lease would remove a branch another
// party recreated between the read and the delete.
type BranchLease struct {
	Branch      string
	ExpectedOld string
}

// BranchOutcome is what one ref of a batched delete ended as.
type BranchOutcome struct {
	Branch string
	// IsDeleted is false for a ref the remote refused because the lease was
	// stale. Such a ref is not this run's to remove, and the caller reports
	// it as retained rather than retrying.
	IsDeleted bool
}

// RemoteHead is one branch a remote advertises.
type RemoteHead struct {
	// Name is the branch name with refs/heads/ taken off, which is the form
	// a coordination branch is routed by.
	Name string
	OID  string
}

// PushCreate puts oid on the remote under branch only while that branch does
// not exist: the lease is written with an empty expected value, which is git's
// spelling of "the ref must not be there".
//
// It is the create half of the protocol, and it is one operation rather than a
// read and a write, so two orchestrators offering the same name at the same
// instant produce exactly one winner. A ref that already carries this exact
// object is this caller's own earlier push whose response was lost, and it
// answers success, for the same reason the release lock reads its own attempt
// id back off the remote rather than concluding it lost.
func (c *LocalGitx) PushCreate(ctx context.Context, remote, oid, branch string) error {
	return c.pushLeased(ctx, remote, oid, branch, "")
}

// PushAdvance moves branch from expectedOld to oid, and is refused when the
// branch is anywhere else. It is how every later transition of a coordination
// branch is written: each party pushes the state it read plus its own step,
// and a lost race is an ErrLeaseRejected the caller resolves by re-reading.
func (c *LocalGitx) PushAdvance(ctx context.Context, remote, oid, branch, expectedOld string) error {
	return c.pushLeased(ctx, remote, oid, branch, expectedOld)
}

func (c *LocalGitx) pushLeased(ctx context.Context, remote, oid, branch, expectedOld string) error {
	ref, err := transportBranchRef(branch)
	if err != nil {
		return err
	}
	out, runErr := c.runStream(ctx, gitStream{}, "push", "--porcelain",
		"--force-with-lease="+ref+":"+expectedOld, "--", remote, oid+":"+ref)
	status, isReported := findPushStatus(out, ref)
	if !isReported {
		return transportError(runErr, remote, "pushing %s", ref)
	}
	if status == pushRejected {
		return fmt.Errorf("gitx: %s on %s: %w", ref, RedactURL(remote), ErrLeaseRejected)
	}
	return nil
}

// DeleteRemoteBranchesLease closes several coordination branches in one push,
// each under its own lease, and reports what became of each.
//
// One push rather than one per ref: a run closes every branch of an endpoint
// at the end, and a fork per branch would make cleanup cost grow with the
// number of tasks. A stale lease fails its own ref and no other, which is the
// behaviour the batch exists for: the branches this run still owns are closed
// even when one of them was taken over.
func (c *LocalGitx) DeleteRemoteBranchesLease(ctx context.Context, remote string, leases []BranchLease) ([]BranchOutcome, error) {
	if len(leases) == 0 {
		return nil, nil
	}
	if len(leases) > MaxTransportBatch {
		return nil, fmt.Errorf("gitx: %d refs in one delete, at most %d: %w",
			len(leases), MaxTransportBatch, ErrTransportLimit)
	}
	args := []string{"push", "--porcelain"}
	refspecs := make([]string, 0, len(leases))
	refs := make([]string, 0, len(leases))
	for _, lease := range leases {
		ref, err := transportBranchRef(lease.Branch)
		if err != nil {
			return nil, err
		}
		args = append(args, "--force-with-lease="+ref+":"+lease.ExpectedOld)
		refspecs = append(refspecs, ":"+ref)
		refs = append(refs, ref)
	}
	args = append(args, "--", remote)
	out, runErr := c.runStream(ctx, gitStream{}, append(args, refspecs...)...)
	outcomes := make([]BranchOutcome, 0, len(leases))
	for i, ref := range refs {
		status, isReported := findPushStatus(out, ref)
		if !isReported {
			return nil, transportError(runErr, remote, "deleting %s", ref)
		}
		outcomes = append(outcomes, BranchOutcome{Branch: leases[i].Branch, IsDeleted: status != pushRejected})
	}
	sort.Slice(outcomes, func(i, j int) bool { return outcomes[i].Branch < outcomes[j].Branch })
	return outcomes, nil
}

// ListRemoteHeads is one poll of a mailbox: the branches the remote
// advertises under pattern, in name order so two polls of one unchanged
// mailbox read identically.
//
// The answer is bounded. A mailbox is a repository other machines write to,
// and a reply naming a million refs would otherwise be read into memory
// before anything had a chance to refuse it.
func (c *LocalGitx) ListRemoteHeads(ctx context.Context, remote, pattern string) ([]RemoteHead, error) {
	out, err := c.run(ctx, "ls-remote", "--heads", "--", remote, pattern)
	if err != nil {
		return nil, transportError(err, remote, "listing %s", pattern)
	}
	heads := make([]RemoteHead, 0, 8)
	for line := range strings.Lines(out) {
		oid, ref, isEntry := strings.Cut(strings.TrimSpace(line), "\t")
		if !isEntry {
			continue
		}
		if len(heads) == MaxRemoteHeads {
			return nil, fmt.Errorf("gitx: %s advertises more than %d refs matching %s: %w",
				RedactURL(remote), MaxRemoteHeads, pattern, ErrTransportLimit)
		}
		heads = append(heads, RemoteHead{Name: strings.TrimPrefix(ref, "refs/heads/"), OID: oid})
	}
	sort.Slice(heads, func(i, j int) bool { return heads[i].Name < heads[j].Name })
	return heads, nil
}

// FetchRefs brings coordination branches into this checkout's private
// transport namespace in one fetch.
//
// Nothing about the fetch touches anything else: no tags travel with it, and
// FETCH_HEAD is not written, because FETCH_HEAD is a moving name and every
// read in the protocol resolves an exact object id instead (see
// ResolveFetchedCommit).
func (c *LocalGitx) FetchRefs(ctx context.Context, remote string, branches []string) error {
	if len(branches) == 0 {
		return nil
	}
	if len(branches) > MaxTransportBatch {
		return fmt.Errorf("gitx: %d refs in one fetch, at most %d: %w",
			len(branches), MaxTransportBatch, ErrTransportLimit)
	}
	args := []string{"fetch", "--no-tags", "--no-write-fetch-head", "--", remote}
	for _, branch := range branches {
		ref, err := transportBranchRef(branch)
		if err != nil {
			return err
		}
		args = append(args, "+"+ref+":"+TransportRefPrefix+branch)
	}
	if _, err := c.run(ctx, args...); err != nil {
		return transportError(err, remote, "fetching %d refs", len(branches))
	}
	return nil
}

// DeleteLocalTransportRefs removes fetched coordination branches from this
// checkout in one update-ref batch. Deleting a ref that is not there succeeds,
// so cleanup converges however far a previous run got.
func (c *LocalGitx) DeleteLocalTransportRefs(ctx context.Context, branches []string) error {
	if len(branches) == 0 {
		return nil
	}
	var commands bytes.Buffer
	for _, branch := range branches {
		ref := TransportRefPrefix + branch
		if err := validRefName(ref); err != nil {
			return fmt.Errorf("gitx: coordination branch %q: %w", branch, err)
		}
		commands.WriteString("delete " + ref + "\n")
	}
	_, err := c.runStream(ctx, gitStream{stdin: &commands}, "update-ref", "--stdin")
	if err != nil {
		return fmt.Errorf("gitx: removing %d fetched transport refs: %w", len(branches), err)
	}
	return nil
}

// ResolveFetchedCommit proves that wantOID is a commit this checkout holds and
// that it sits on localRef's first-parent chain within maxDepth steps of its
// tip. It returns nothing but an error: callers read from wantOID afterwards,
// never from the ref, so what they read cannot move under them.
//
// Both halves matter. The object check refuses a reference to something that
// was never fetched; the chain check refuses an object that exists locally for
// some unrelated reason, which on a shared checkout is every object of every
// other branch. A depth below one admits nothing at all, not even the tip.
func (c *LocalGitx) ResolveFetchedCommit(ctx context.Context, localRef, wantOID string, maxDepth int) error {
	if _, err := c.run(ctx, "cat-file", "-e", wantOID+"^{commit}"); err != nil {
		return fmt.Errorf("gitx: %s was not fetched as a commit: %w", wantOID, ErrCommitNotOnRef)
	}
	out, err := c.run(ctx, "rev-list", "--first-parent", "--max-count="+strconv.Itoa(maxDepth), localRef)
	if err != nil {
		return fmt.Errorf("gitx: reading the first-parent chain of %s: %w", localRef, err)
	}
	for line := range strings.Lines(out) {
		if strings.TrimSpace(line) == wantOID {
			return nil
		}
	}
	return fmt.Errorf("gitx: %s is not within %d first-parent steps of %s: %w",
		wantOID, maxDepth, localRef, ErrCommitNotOnRef)
}

// InitBareStore makes this handle's folder a bare repository, and leaves one
// that is already there as it is.
//
// It is what a serving node opens its object cache with. The cache holds
// nothing but fetched coordination objects, so it is dispensable by design: a
// node whose cache was deleted, or never existed, creates it again and pays
// one full fetch, which is why this is safe to call on every recovery rather
// than only once. git init is idempotent, so no caller has to ask first
// whether the folder is already a repository.
func (c *LocalGitx) InitBareStore(ctx context.Context) error {
	if _, err := c.run(ctx, "init", "--bare", "--quiet"); err != nil {
		return fmt.Errorf("gitx: opening the bare object store at %s: %w", c.Dir, err)
	}
	return nil
}

// IndexPath is where this repository keeps the index, as an absolute path.
//
// It exists for the one operation that has to start from the real index and
// must not touch it: capturing a working tree as a tree object through a copy
// (see the execution package's snapshots). The path is asked of git rather
// than assembled, because a repository whose git directory is elsewhere — a
// worktree, a separate GIT_DIR, a configured index file — keeps its index
// where git says it does and nowhere a caller could guess.
func (c *LocalGitx) IndexPath(ctx context.Context) (string, error) {
	out, err := c.run(ctx, "rev-parse", "--git-path", "index")
	if err != nil {
		return "", fmt.Errorf("gitx: locating the index of %s: %w", c.Dir, err)
	}
	path := strings.TrimSpace(out)
	if path == "" {
		return "", fmt.Errorf("gitx: %s reported no index path", c.Dir)
	}
	if filepath.IsAbs(path) {
		return path, nil
	}
	// git answers relative to the repository it was asked in, which is this
	// handle's folder because every invocation carries -C.
	return filepath.Join(c.Dir, path), nil
}

// CountChangedPaths is how many tracked paths of this checkout differ from
// what it was materialized at, ignoring the folders the caller declared.
//
// Untracked paths are deliberately not counted. A build's own products are
// untracked by construction, so counting them would report every successful
// build as a build that wrote where it should not have; what this asks about
// is the source a task was given, and whether it came back changed. The
// declared folders are excluded for the same reason one step further on: a
// tracked file inside a declared build output root is a file the run carries
// deliberately, so counting it would report a transfer as a stray write.
//
// The listing is NUL separated, so a path holding a space, a quote or a
// newline arrives as itself rather than as git's quoted rendering of it.
func (c *LocalGitx) CountChangedPaths(ctx context.Context, declared []string) (int, error) {
	out, err := c.run(ctx, "status", "--porcelain", "-z")
	if err != nil {
		return 0, fmt.Errorf("gitx: reading the status of %s: %w", c.Dir, err)
	}
	changed := 0
	for _, record := range strings.Split(out, "\x00") {
		if len(record) < 4 || strings.HasPrefix(record, "??") {
			continue
		}
		if isPathDeclared(record[3:], declared) {
			continue
		}
		changed++
	}
	return changed, nil
}

// isPathDeclared reports whether one changed path lies in a folder the caller
// declared. The comparison is by component, so `dist-old/x` is not a file
// inside `dist`.
func isPathDeclared(path string, declared []string) bool {
	for _, folder := range declared {
		if path == folder || strings.HasPrefix(path, folder+"/") {
			return true
		}
	}
	return false
}

// GitVersion is what the git behind this repository calls itself, and the
// empty string when it cannot be asked.
//
// The failure is deliberately not returned. The version travels in a node's
// description of itself, where it is a diagnostic a person reads and never a
// decision anything is made from, so a node whose git could not be asked
// reports nothing rather than refusing to serve over it.
func (c *LocalGitx) GitVersion(ctx context.Context) string {
	out, _ := c.run(ctx, "--version")
	return strings.TrimSpace(out)
}

// ChangedPathsBetween is the tracked paths that differ between two commits,
// restricted to the pathspecs the caller names.
//
// It is a tree-to-tree comparison and touches neither the working tree nor the
// index, which is what makes it safe to ask while a release is running: a run
// that had to hold a folder still to ask whether that folder changed would be
// a run serialising itself on its own question.
//
// The pathspecs are git's own, magic included, because the one caller needs to
// say "these folders except these files" and git already has a spelling for
// it. The listing is NUL separated, so a path holding a space or a newline
// arrives as itself rather than as git's quoted rendering of it.
func (c *LocalGitx) ChangedPathsBetween(ctx context.Context, from, to string, pathspecs []string) ([]string, error) {
	out, err := c.run(ctx, append([]string{"diff", "--name-only", "-z", from, to, "--"}, pathspecs...)...)
	if err != nil {
		return nil, fmt.Errorf("gitx: comparing %s with %s in %s: %w", from, to, c.Dir, err)
	}
	var changed []string
	for _, path := range strings.Split(out, "\x00") {
		if path == "" {
			continue
		}
		changed = append(changed, path)
	}
	return changed, nil
}

// RemoteTagObject is the object id the remote advertises for a tag, empty when
// the remote does not carry it.
//
// The tag OBJECT is what comes back, not the commit it peels to: the release
// lock is an annotated tag whose object id is the identity of one acquisition
// (see release.Lock), so peeling it would answer about the commit every
// acquisition of that checkout shares.
func (c *LocalGitx) RemoteTagObject(ctx context.Context, remote, tag string) (string, error) {
	ref := "refs/tags/" + tag
	out, err := c.run(ctx, "ls-remote", "--tags", "--", remote, ref)
	if err != nil {
		return "", transportError(err, remote, "reading %s", ref)
	}
	for line := range strings.Lines(out) {
		oid, name, isEntry := strings.Cut(strings.TrimSpace(line), "\t")
		// The peeled "^{}" line names the commit under an annotated tag and is
		// exactly the answer this must not give.
		if isEntry && name == ref {
			return oid, nil
		}
	}
	return "", nil
}

// The porcelain status flags this package reads. Anything else git may print
// is a successful update of some shape. The coordination protocol only has to
// recognise the rejection; a release record additionally tells a name that was
// already there from one this push created (see records.go).
const (
	pushRejected = '!'
	pushUpToDate = '='
	pushForced   = '+'
)

// findPushStatus reads the flag of one ref out of `git push --porcelain`
// output, and reports whether the ref was mentioned at all.
//
// A line is "<flag>\t<from>:<to>\t<summary>"; a deletion writes an empty or
// "(delete)" source half. A ref with no line of its own means git never got as
// far as deciding about it, which is a transport failure rather than a
// rejection.
func findPushStatus(out, ref string) (rune, bool) {
	for line := range strings.Lines(out) {
		line = strings.TrimRight(line, "\n")
		flag, rest, isStatus := strings.Cut(line, "\t")
		if !isStatus || len([]rune(flag)) != 1 {
			continue
		}
		refspec, _, _ := strings.Cut(rest, "\t")
		if _, destination, isPair := strings.Cut(refspec, ":"); isPair && destination == ref {
			return []rune(flag)[0], true
		}
	}
	return 0, false
}

// transportError names the operation and the remote, redacted, around
// whatever git reported. A nil run error means git succeeded and still said
// nothing about the ref, which is a broken reply rather than a failure, and
// is reported as one.
func transportError(err error, remote, format string, args ...any) error {
	operation := fmt.Sprintf(format, args...)
	if err == nil {
		return fmt.Errorf("gitx: %s on %s: the remote reported no result for it",
			operation, RedactURL(remote))
	}
	return fmt.Errorf("gitx: %s on %s: %w", operation, RedactURL(remote), err)
}

// transportBranchRef turns a coordination branch name into the ref it is, and
// refuses a name git would not accept, before it reaches a command line where
// it would be read as something else.
func transportBranchRef(branch string) (string, error) {
	ref := "refs/heads/" + branch
	if err := validRefName(ref); err != nil {
		return "", fmt.Errorf("gitx: coordination branch %q: %w", branch, err)
	}
	return ref, nil
}

// formatTransportIdentity is the child environment every transport object is
// written under: both identities and both dates, stated rather than inherited.
//
// The dates are the current time rather than a fixed one on purpose. A git
// object is identified by its content, its identity and its date to the
// second, so two nodes writing identical trees under a fixed date would
// produce one object, and a create-only push of an object the remote already
// has succeeds instead of reporting that somebody got there first.
func formatTransportIdentity(now time.Time) []string {
	stamp := strconv.FormatInt(now.Unix(), 10) + " +0000"
	return []string{
		"GIT_AUTHOR_NAME=" + TransportIdentityName,
		"GIT_AUTHOR_EMAIL=" + TransportIdentityEmail,
		"GIT_AUTHOR_DATE=" + stamp,
		"GIT_COMMITTER_NAME=" + TransportIdentityName,
		"GIT_COMMITTER_EMAIL=" + TransportIdentityEmail,
		"GIT_COMMITTER_DATE=" + stamp,
	}
}

// TreeEntry is one entry of a Git tree: what MakeTree is given and what
// ListTree hands back. Size is the blob's size in bytes and is -1 for an
// entry that has none, which is every tree.
type TreeEntry struct {
	Mode string
	Type string
	OID  string
	Size int64
	Name string
}

// The two tree modes the transport writes. A coordination tree holds data
// files and the sub-trees they sit in, and nothing else: no symlink, no
// executable bit and no gitlink, because none of them would mean anything to
// a reader that only ever reads blobs back out.
const (
	TreeModeFile = "100644"
	TreeModeDir  = "040000"
)

// Plumbing is one multi-step object construction, with the first failure
// remembered and every later step skipped.
//
// A transport message is a blob, a tree, a parent tree and a commit, built in
// that order, and checking four errors in a row would put four arms in the
// caller that mean the same thing. Here the caller builds the object and asks
// Err once. Every step is still one git invocation with a subcommand of its
// own, so a test that has to make the third step fail can name it.
//
// One Plumbing belongs to one operation and to one goroutine: the remembered
// error is its whole state, and sharing it would make two operations decide
// each other's outcome.
type Plumbing struct {
	git *LocalGitx
	err error
}

// NewPlumbing opens a plumbing session on a repository.
func NewPlumbing(git *LocalGitx) *Plumbing { return &Plumbing{git: git} }

// Err is the first failure of the session, or nil when every step ran.
func (p *Plumbing) Err() error { return p.err }

// fail remembers the first error of the session; a later one is dropped,
// because the first is the one that explains the rest.
func (p *Plumbing) fail(err error) {
	if p.err == nil {
		p.err = err
	}
}

// HashObject writes the reader's bytes into the object store and answers the
// blob's id. The content is streamed into git rather than read into memory:
// what travels through a mailbox is build output, and a build output is
// exactly the thing that does not fit.
func (p *Plumbing) HashObject(ctx context.Context, content io.Reader) string {
	if p.err != nil {
		return ""
	}
	out, err := p.git.runStream(ctx, gitStream{stdin: content}, "hash-object", "-w", "--stdin")
	if err != nil {
		p.fail(fmt.Errorf("gitx: hashing a transport blob: %w", err))
		return ""
	}
	return strings.TrimSpace(out)
}

// MakeTree writes one tree level and answers its id. Git's mktree builds a
// single level, so a nested path is built innermost first and the result
// named as a sub-tree entry of its parent, which is what keeps the entry
// names free of any separator a caller could smuggle a path through.
func (p *Plumbing) MakeTree(ctx context.Context, entries []TreeEntry) string {
	if p.err != nil {
		return ""
	}
	var listing bytes.Buffer
	for _, entry := range entries {
		if strings.ContainsAny(entry.Name, "/\x00") {
			p.fail(fmt.Errorf("gitx: tree entry %q names more than one level", entry.Name))
			return ""
		}
		fmt.Fprintf(&listing, "%s %s %s\t%s\x00", entry.Mode, entry.Type, entry.OID, entry.Name)
	}
	out, err := p.git.runStream(ctx, gitStream{stdin: &listing}, "mktree", "-z")
	if err != nil {
		p.fail(fmt.Errorf("gitx: writing a transport tree: %w", err))
		return ""
	}
	return strings.TrimSpace(out)
}

// CommitTree wraps a tree in a commit under the fixed transport identity and
// answers its id. The kind is the only part of the message a caller chooses,
// and the message it lands in carries no colon and no separator, so a
// coordination commit can never parse as a release unit.
func (p *Plumbing) CommitTree(ctx context.Context, tree string, parents []string, kind string) string {
	if p.err != nil {
		return ""
	}
	args := []string{"commit-tree", tree}
	for _, parent := range parents {
		args = append(args, "-p", parent)
	}
	args = append(args, "-m", transportMessagePrefix+kind)
	out, err := p.git.runStream(ctx, gitStream{env: formatTransportIdentity(time.Now().UTC())}, args...)
	if err != nil {
		p.fail(fmt.Errorf("gitx: writing the transport commit for %s: %w", kind, err))
		return ""
	}
	return strings.TrimSpace(out)
}

// WriteTreeFromPaths captures paths of a working folder as a tree, through a
// temporary index of its own so that neither the repository's index nor its
// worktree is touched.
//
// Forcing is what a build output needs: the paths a package declares are
// usually ignored by Git, which is the whole reason they have to be declared
// instead of discovered. The pathspecs are literal, so a folder whose name
// holds a glob character is the folder it is named, and they are read
// relative to dir while the tree itself comes out rooted at the repository,
// because that is what an index holds and what the manifest of a release
// names its files by.
func (p *Plumbing) WriteTreeFromPaths(ctx context.Context, dir, indexFile string, paths []string, isForced bool) string {
	if p.err != nil {
		return ""
	}
	env := []string{"GIT_INDEX_FILE=" + indexFile}
	if isForced {
		if err := p.stageWithoutConversion(ctx, dir, env, paths); err != nil {
			p.fail(fmt.Errorf("gitx: staging %d transport paths: %w", len(paths), err))
			return ""
		}
	} else {
		add := []string{"-C", dir, "--literal-pathspecs", "add", "--"}
		if _, err := p.git.runStream(ctx, gitStream{env: env}, append(add, paths...)...); err != nil {
			p.fail(fmt.Errorf("gitx: staging %d transport paths: %w", len(paths), err))
			return ""
		}
	}
	out, err := p.git.runStream(ctx, gitStream{env: env}, "write-tree")
	if err != nil {
		p.fail(fmt.Errorf("gitx: writing the staged transport tree: %w", err))
		return ""
	}
	return strings.TrimSpace(out)
}

// stagedPath is one entry of a forced capture: the mode git records for it,
// its path from the repository root, and where its bytes are.
type stagedPath struct {
	mode string
	path string
	oid  string
	file string
}

// stageWithoutConversion fills the index with the paths exactly as the working
// folder holds them.
//
// A build output is not source. A checkout's `.gitattributes` describe how its
// sources are stored, and `git add` applies them to whatever it stages: a
// `text` attribute rewrites every CRLF pair inside a file, a clean filter
// replaces its content, `ident` expands a keyword. Applied to a library a
// build wrote, that is corruption, and it happened: a project that marks its
// whole tree as text shipped three libraries whose bytes the consumer could not
// verify. `git add` has no way to leave attributes out, so the entries are
// hashed with `--no-filters` and written into the index by hand, with the modes
// git itself would record: 100755 for an executable, 120000 for a link, 160000
// for a nested repository, which the manifest rules refuse afterwards exactly
// as they refused what `add` produced.
func (p *Plumbing) stageWithoutConversion(ctx context.Context, dir string, env []string, paths []string) error {
	// The folder's place in the repository is asked of git rather than computed
	// from the repository root, which git answers with symlinks resolved while
	// the caller's path may carry them.
	prefix, err := p.git.runStream(ctx, gitStream{}, "-C", dir, "rev-parse", "--show-prefix")
	if err != nil {
		return fmt.Errorf("locating %s in its repository: %w", dir, err)
	}
	prefix = strings.TrimSpace(prefix)
	var staged []stagedPath
	for _, root := range paths {
		if err := p.collectStagedPaths(ctx, dir, prefix, filepath.Join(dir, root), &staged); err != nil {
			return err
		}
	}
	if err := p.hashStagedFiles(ctx, dir, staged); err != nil {
		return err
	}
	var records bytes.Buffer
	for _, entry := range staged {
		fmt.Fprintf(&records, "%s %s\t%s\x00", entry.mode, entry.oid, entry.path)
	}
	if _, err := p.git.runStream(ctx, gitStream{stdin: &records, env: env},
		"-C", dir, "update-index", "-z", "--add", "--index-info"); err != nil {
		return fmt.Errorf("writing %d entries into the transport index: %w", len(staged), err)
	}
	return nil
}

// collectStagedPaths walks one declared root lexically, in name order, and
// records what git would have recorded for each entry, named from the
// repository root.
func (p *Plumbing) collectStagedPaths(ctx context.Context, dir, prefix, abs string, staged *[]stagedPath) error {
	info, err := os.Lstat(abs)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(dir, abs)
	if err != nil {
		return fmt.Errorf("placing %s under %s: %w", abs, dir, err)
	}
	path := prefix + filepath.ToSlash(rel)
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(abs)
		if err != nil {
			return err
		}
		oid, err := p.hashBytes(ctx, dir, []byte(target))
		if err != nil {
			return err
		}
		*staged = append(*staged, stagedPath{mode: "120000", path: path, oid: oid})
	case info.Mode().IsRegular():
		mode := "100644"
		if info.Mode()&0o111 != 0 {
			mode = "100755"
		}
		*staged = append(*staged, stagedPath{mode: mode, path: path, file: abs})
	case info.IsDir():
		if _, err := os.Lstat(filepath.Join(abs, ".git")); err == nil {
			*staged = append(*staged, stagedPath{mode: "160000", path: path,
				oid: p.resolveNestedHead(ctx, abs)})
			return nil
		}
		entries, err := os.ReadDir(abs)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := p.collectStagedPaths(ctx, dir, prefix, filepath.Join(abs, entry.Name()), staged); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("%s is neither a file, a link nor a folder", path)
	}
	return nil
}

// resolveNestedHead is the object a nested repository is recorded as, or the
// null object when it has no commit: either way the manifest rules refuse it.
func (p *Plumbing) resolveNestedHead(ctx context.Context, dir string) string {
	head, err := p.git.runStream(ctx, gitStream{}, "-C", dir, "rev-parse", "HEAD")
	if err != nil {
		return strings.Repeat("0", 40)
	}
	return strings.TrimSpace(head)
}

// hashStagedFiles writes every regular file into the object store with no
// filter or conversion, and fills in their object ids: one git invocation for
// every path git can read from a list, and one of its own for a path holding
// a newline, which no list can carry.
func (p *Plumbing) hashStagedFiles(ctx context.Context, dir string, staged []stagedPath) error {
	var listed []int
	var names bytes.Buffer
	for index, entry := range staged {
		if entry.file == "" {
			continue
		}
		if strings.Contains(entry.file, "\n") {
			oid, err := p.hashFile(ctx, dir, entry.file)
			if err != nil {
				return err
			}
			staged[index].oid = oid
			continue
		}
		listed = append(listed, index)
		names.WriteString(entry.file)
		names.WriteByte('\n')
	}
	if len(listed) == 0 {
		return nil
	}
	out, err := p.git.runStream(ctx, gitStream{stdin: &names},
		"-C", dir, "hash-object", "-w", "--no-filters", "--stdin-paths")
	if err != nil {
		return fmt.Errorf("hashing %d transport files: %w", len(listed), err)
	}
	oids := strings.Fields(out)
	if len(oids) != len(listed) {
		return fmt.Errorf("hashing %d transport files answered %d objects", len(listed), len(oids))
	}
	for position, index := range listed {
		staged[index].oid = oids[position]
	}
	return nil
}

// hashFile writes one file's bytes as a blob through standard input, which no
// attribute applies to, and answers its id.
func (p *Plumbing) hashFile(ctx context.Context, dir, file string) (string, error) {
	handle, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer handle.Close()
	out, err := p.git.runStream(ctx, gitStream{stdin: handle},
		"-C", dir, "hash-object", "-w", "--no-filters", "--stdin")
	if err != nil {
		return "", fmt.Errorf("hashing %s: %w", file, err)
	}
	return strings.TrimSpace(out), nil
}

// hashBytes writes one blob of the given bytes and answers its id.
func (p *Plumbing) hashBytes(ctx context.Context, dir string, content []byte) (string, error) {
	out, err := p.git.runStream(ctx, gitStream{stdin: bytes.NewReader(content)},
		"-C", dir, "hash-object", "-w", "--no-filters", "--stdin")
	if err != nil {
		return "", fmt.Errorf("hashing a link target: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// ReadBlob streams one blob into the caller's writer, refusing anything larger
// than maxBytes before a byte of it is read.
//
// The size is asked for first and separately, because the point of the ceiling
// is that an oversized object is never written anywhere: a check made while
// copying would have already filled a disk by the time it fired.
func (p *Plumbing) ReadBlob(ctx context.Context, oid string, to io.Writer, maxBytes int64) {
	if p.err != nil {
		return
	}
	out, err := p.git.run(ctx, "cat-file", "-s", oid)
	if err != nil {
		p.fail(fmt.Errorf("gitx: sizing the transport blob %s: %w", oid, err))
		return
	}
	size, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		p.fail(fmt.Errorf("gitx: the size of the transport blob %s is not a number: %w", oid, err))
		return
	}
	if size > maxBytes {
		p.fail(fmt.Errorf("gitx: the transport blob %s is %d bytes, at most %d: %w",
			oid, size, maxBytes, ErrTransportLimit))
		return
	}
	if _, err := p.git.runStream(ctx, gitStream{stdout: to}, "cat-file", "blob", oid); err != nil {
		p.fail(fmt.Errorf("gitx: reading the transport blob %s: %w", oid, err))
	}
}

// ObjectReader is one long-running `git cat-file --batch`, asked for objects
// one at a time and streaming each answer straight into the caller's writer.
//
// It exists because the alternative is two forks per file. A build output set
// is tens of thousands of files, and both the node that captures it and the
// node that installs it have to read every blob once: doing that through
// Plumbing.ReadBlob would start eighty thousand git processes where one is
// enough. The process is also the boundary the bytes never cross as a whole,
// since every answer is copied out with io.CopyN.
//
// One reader belongs to one operation and to one goroutine: the request and
// the answer share a pipe, so two callers interleaving on it would each read
// the other's object.
type ObjectReader struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stop   context.CancelFunc
	// isBroken is set by a read that left the pipe mid-object, which is every
	// refusal: the protocol is a stream, so a reader that stopped early can
	// answer nothing more and is killed rather than asked again.
	isBroken bool
}

// ErrObjectMissing is the object store answering that it does not have what it
// was asked for. It is a sentinel because it is the one answer a caller acts
// on: a digest, a file name or a branch reference whose bytes cannot be
// retrieved is a transfer that did not happen (§28.5), and that fails the
// prerequisite rather than the process.
var ErrObjectMissing = errors.New("gitx: the object store does not hold the object")

// OpenObjectReader starts one batch reader on a repository.
//
// The caller's context is wrapped rather than used directly so that Close can
// kill a reader that was abandoned mid-object without cancelling anything else
// the caller is doing.
func OpenObjectReader(ctx context.Context, git *LocalGitx) (*ObjectReader, error) {
	gitInvocations.Add(1)
	running, stop := context.WithCancel(ctx)
	cmd := exec.CommandContext(running, "git", "-C", git.Dir, "cat-file", "--batch")
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	cmd.WaitDelay = 10 * time.Second
	script.SetProcessGroup(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		stop()
		return nil, fmt.Errorf("gitx: opening the object reader of %s: %w", git.Dir, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stop()
		return nil, fmt.Errorf("gitx: opening the object reader of %s: %w", git.Dir, err)
	}
	if err := cmd.Start(); err != nil {
		stop()
		return nil, fmt.Errorf("gitx: starting the object reader of %s: %w", git.Dir, err)
	}
	git.Log.Trace().Str("dir", git.Dir).Msg("object reader opened")
	return &ObjectReader{cmd: cmd, stdin: stdin, stdout: bufio.NewReaderSize(stdout, 64<<10), stop: stop}, nil
}

// ReadBlob streams one blob into to and answers how many bytes it was,
// refusing anything the caller is not willing to read before a byte of it is
// copied.
//
// The size comes from the object's own header, which is the first thing the
// batch answers, so the ceiling is applied to the real length rather than to
// however much arrived.
func (r *ObjectReader) ReadBlob(oid string, to io.Writer, maxBytes int64) (int64, error) {
	if r.isBroken {
		return 0, fmt.Errorf("gitx: the object reader stopped at an earlier object: %w", ErrTransportLimit)
	}
	size, err := r.request(oid)
	if err != nil {
		return 0, err
	}
	if size > maxBytes {
		r.isBroken = true
		return 0, fmt.Errorf("gitx: the object %s is %d bytes, at most %d: %w",
			oid, size, maxBytes, ErrTransportLimit)
	}
	if _, err := io.CopyN(to, r.stdout, size); err != nil {
		r.isBroken = true
		return 0, fmt.Errorf("gitx: reading the %d bytes of %s: %w", size, oid, err)
	}
	// Every answer ends with one newline the caller never sees; leaving it in
	// the pipe would make the next header unreadable.
	if _, err := r.stdout.ReadByte(); err != nil {
		r.isBroken = true
		return 0, fmt.Errorf("gitx: reading the end of %s: %w", oid, err)
	}
	return size, nil
}

// request asks for one object and answers the length the header promised.
func (r *ObjectReader) request(oid string) (int64, error) {
	if _, err := io.WriteString(r.stdin, oid+"\n"); err != nil {
		r.isBroken = true
		return 0, fmt.Errorf("gitx: asking the object reader for %s: %w", oid, err)
	}
	header, err := r.stdout.ReadString('\n')
	if err != nil {
		r.isBroken = true
		return 0, fmt.Errorf("gitx: the object reader did not answer about %s: %w", oid, err)
	}
	fields := strings.Fields(header)
	if len(fields) == 2 && fields[1] == "missing" {
		return 0, fmt.Errorf("gitx: %s: %w", oid, ErrObjectMissing)
	}
	if len(fields) != 3 || fields[1] != "blob" {
		r.isBroken = true
		return 0, fmt.Errorf("gitx: the object reader answered %q about %s, which is not a blob header",
			strings.TrimSpace(header), oid)
	}
	size, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil {
		r.isBroken = true
		return 0, fmt.Errorf("gitx: the length of %s is not a number: %w", oid, err)
	}
	return size, nil
}

// Close ends the reader and reports what git made of the session.
//
// A reader that answered everything it was asked is closed by closing its
// input, which is how the batch protocol ends; one abandoned mid-object is
// killed instead, because the bytes still in the pipe are bytes nobody is
// going to read and git would block writing them.
func (r *ObjectReader) Close() error {
	if r.isBroken {
		r.stop()
		_ = r.stdin.Close()
		_ = r.cmd.Wait()
		return nil
	}
	if err := r.stdin.Close(); err != nil {
		r.stop()
		_ = r.cmd.Wait()
		return fmt.Errorf("gitx: ending the object reader: %w", err)
	}
	err := r.cmd.Wait()
	r.stop()
	if err != nil {
		return fmt.Errorf("gitx: the object reader failed: %w", err)
	}
	return nil
}

// ResolveSubtree is the object id of one path inside a tree, which is how a
// tree written from a repository's index is narrowed to the folder a package
// owns.
//
// The path is read as git reads `<tree>:<path>`, and the caller passes a path
// it composed rather than one a message carried: what comes back is an object
// id, so a path naming something else would answer something else rather than
// reach anywhere it should not.
func (p *Plumbing) ResolveSubtree(ctx context.Context, tree, path string) string {
	if p.err != nil {
		return ""
	}
	if path == "" || path == "." {
		return tree
	}
	out, err := p.git.run(ctx, "rev-parse", "--verify", "--end-of-options", tree+":"+path)
	if err != nil {
		p.fail(fmt.Errorf("gitx: resolving %s inside the tree %s: %w", path, tree, err))
		return ""
	}
	return strings.TrimSpace(out)
}

// ListTree walks a tree recursively and calls visit once per entry, as the
// entries arrive. Nothing accumulates: a manifest of a hundred thousand files
// is read one entry at a time, and a visit that fails stops the walk with its
// own error.
//
// The listing is NUL separated, which is what makes a path holding a space, a
// newline or any other byte git allows arrive as itself instead of as git's
// quoted rendering of itself.
func (p *Plumbing) ListTree(ctx context.Context, tree string, visit func(TreeEntry) error) {
	if p.err != nil {
		return
	}
	scanner := &treeScanner{visit: visit}
	if _, err := p.git.runStream(ctx, gitStream{stdout: scanner}, "ls-tree", "-r", "-l", "-z", tree); err != nil {
		p.fail(fmt.Errorf("gitx: listing the transport tree %s: %w", tree, err))
		return
	}
	if scanner.err != nil {
		p.fail(scanner.err)
		return
	}
	if scanner.pending.Len() > 0 {
		p.fail(fmt.Errorf("gitx: the listing of %s ended mid-entry", tree))
	}
}

// WorktreeAdd materializes a commit in its own detached worktree, which is how
// a node reads a transported tree as files without disturbing the checkout it
// is releasing from.
func (p *Plumbing) WorktreeAdd(ctx context.Context, dir, oid string) {
	if p.err != nil {
		return
	}
	if _, err := p.git.run(ctx, "worktree", "add", "--detach", "--", dir, oid); err != nil {
		p.fail(fmt.Errorf("gitx: adding the transport worktree: %w", err))
	}
}

// WorktreeRemove takes a transport worktree away again, forced because the
// folder it is removing is one a build wrote into.
func (p *Plumbing) WorktreeRemove(ctx context.Context, dir string) {
	if p.err != nil {
		return
	}
	if _, err := p.git.run(ctx, "worktree", "remove", "--force", "--", dir); err != nil {
		p.fail(fmt.Errorf("gitx: removing the transport worktree: %w", err))
	}
}

// WorktreePrune forgets the administrative records of transport worktrees
// whose folders are gone, which is what an interrupted run leaves behind.
func (p *Plumbing) WorktreePrune(ctx context.Context) {
	if p.err != nil {
		return
	}
	if _, err := p.git.run(ctx, "worktree", "prune"); err != nil {
		p.fail(fmt.Errorf("gitx: pruning the transport worktrees: %w", err))
	}
}

// treeScanner turns the NUL-separated stream of `ls-tree -r -l -z` into
// entries as they arrive. It is an io.Writer so that the listing is consumed
// while git is still producing it and never exists in one piece.
type treeScanner struct {
	visit   func(TreeEntry) error
	pending bytes.Buffer
	err     error
}

func (s *treeScanner) Write(p []byte) (int, error) {
	consumed := len(p)
	for s.err == nil {
		index := bytes.IndexByte(p, 0)
		if index < 0 {
			break
		}
		s.pending.Write(p[:index])
		s.handle(s.pending.String())
		s.pending.Reset()
		p = p[index+1:]
	}
	if s.err == nil {
		s.pending.Write(p)
		if s.pending.Len() > MaxTreeEntryBytes {
			s.err = fmt.Errorf("gitx: a tree listing entry exceeds %d bytes: %w",
				MaxTreeEntryBytes, ErrTransportLimit)
		}
	}
	// The whole write is always reported as consumed: git is told to stop by
	// cancellation rather than by a short write, and a short write here would
	// be reported as an I/O failure that hides the visit's own error.
	return consumed, nil
}

// handle parses one record, "<mode> SP <type> SP <oid> SP* <size> TAB <path>",
// where the size is right-aligned and is "-" for a tree.
func (s *treeScanner) handle(record string) {
	head, name, isRecord := strings.Cut(record, "\t")
	if !isRecord {
		s.err = errors.New("gitx: a tree listing entry has no path")
		return
	}
	fields := strings.Fields(head)
	if len(fields) != 4 {
		s.err = fmt.Errorf("gitx: a tree listing entry has %d fields rather than four", len(fields))
		return
	}
	entry := TreeEntry{Mode: fields[0], Type: fields[1], OID: fields[2], Size: -1, Name: name}
	if size, err := strconv.ParseInt(fields[3], 10, 64); err == nil {
		entry.Size = size
	}
	s.err = s.visit(entry)
}
