// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// What a serving node keeps between two messages, and the one thing it may
// not share.
//
// Almost nothing here is precious. The object cache is a bare repository full
// of fetched coordination objects and can be deleted at any moment; losing it
// costs one full fetch and nothing else. The exception is the record of which
// work this node has already answered: two processes serving one node name
// out of one folder would each hold half of that record, and a replayed
// assignment would be answered twice. So the folder is owned by one process
// at a time, and the ownership is written down where the second one can read
// it.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/fsx"
)

// The files one node's state folder holds. They are named rather than
// composed at the call sites so that a reader of this package can see the
// whole layout in one place.
const (
	stateCacheDir = "cache"
	stateSeenFile = "seen.json"
	stateLockFile = "worker.lock"
)

// A lock file may stay empty this long before its writer is taken for dead,
// polled at the second interval. The grace is orders of magnitude longer than
// the gap it covers (one write after one open) and short enough that a node
// restarted over such a leftover starts within a second. A claim settles for
// the same grace before it is trusted (see lockNodeState).
const (
	stateLockWriteGrace = time.Second
	stateLockWritePoll  = 10 * time.Millisecond
	// A PID is only a few decimal digits. Refuse oversized content instead of
	// allocating unbounded memory before deciding who owns the state.
	stateLockOwnerMaxBytes = 64
)

// NodeState is one serving node's own folder: where its object cache lives,
// where the record of answered work is kept, and the lock that says this
// process owns both.
type NodeState struct {
	// Dir is <state>/<name>, the folder this node owns entirely.
	Dir string
	// Cache is the bare repository fetched coordination objects land in, one
	// per endpoint, so that a node serving two mailboxes keeps their objects
	// apart and neither fetch invalidates the other.
	Cache string
	// Seen is the file the answered-work record is persisted to.
	Seen string
	// lock is the file naming the process that owns the folder.
	lock string
}

// OpenNodeState takes ownership of one node's state folder and answers the
// paths inside it together with the release that gives the ownership back.
//
// The lock is a file holding the owning process id. A lock whose process is
// gone is taken over, because the alternative is a node that cannot be
// restarted after a crash without somebody deleting a file by hand; a lock
// whose process is alive is refused, because the record of answered work has
// exactly one owner.
func OpenNodeState(root, node, endpoint string) (*NodeState, func() error, error) {
	dir := filepath.Join(root, node)
	state := &NodeState{
		Dir:   dir,
		Cache: filepath.Join(dir, stateCacheDir, formatEndpointKey(endpoint)+".git"),
		Seen:  filepath.Join(dir, stateSeenFile),
		lock:  filepath.Join(dir, stateLockFile),
	}
	if err := os.MkdirAll(state.Cache, 0o755); err != nil {
		return nil, nil, fmt.Errorf("execution: preparing the node state folder %s: %w", dir, err)
	}
	release, err := lockNodeState(state.lock)
	if err != nil {
		return nil, nil, err
	}
	return state, release, nil
}

// VerifyOwner reports another process's claim on this folder: the E225
// refusal when worker.lock names a process other than this one, and nil while
// it names this process or nobody.
//
// A serving node asks before it claims each assignment. Two processes can
// both believe they own one folder only when one of them misjudged the
// other's process id, for example across process namespaces; claims are
// compare-and-swap pushes, so the question bounds how long the two serve
// together to the claims already in flight.
func (s *NodeState) VerifyOwner() error {
	content, err := readNodeLockContent(s.lock)
	if err != nil {
		return nil
	}
	owner := parseNodeLockOwner(content)
	if owner == 0 || owner == os.Getpid() {
		return nil
	}
	return nodeLockOccupied(s.lock, owner)
}

// formatEndpointKey names one endpoint's cache: the first 16 hex characters
// of the SHA-256 of its address. The address itself cannot be a folder name,
// and a hash of it is also what keeps a credential-free URL from being
// readable in a process listing of the node.
func formatEndpointKey(endpoint string) string {
	sum := sha256.Sum256([]byte(endpoint))
	return hex.EncodeToString(sum[:])[:16]
}

// lockNodeState claims the folder for this process, or reports who has it.
//
// A claim is trusted only once it has settled. Taking over a stale lock is
// several filesystem operations, and processes started together can each
// finish one they read as a win: one renames aside a claim another has just
// created, or restores a claim over one a third has written. Each of them
// wrote its own process id last, so each waits stateLockWriteGrace, long
// enough for every contender that started with it to have written, and then
// reads the lock again. Only the process the file still names serves; every
// other one is refused as the E225 it would have met a moment later.
func lockNodeState(path string) (func() error, error) {
	owner, err := claimNodeLock(path)
	if err != nil {
		return nil, err
	}
	if owner != 0 {
		return nil, nodeLockOccupied(path, owner)
	}
	time.Sleep(stateLockWriteGrace)
	settled := 0
	if content, readErr := readNodeLockContent(path); readErr == nil {
		settled = parseNodeLockOwner(content)
	}
	if settled != os.Getpid() {
		return nil, nodeLockOccupied(path, settled)
	}
	return func() error { return releaseNodeLock(path) }, nil
}

// releaseNodeLock gives the folder back by removing the lock, and only while
// the lock still names this process: a lock another process has written since
// is that process's claim, not this one's to remove.
func releaseNodeLock(path string) error {
	content, err := readNodeLockContent(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("execution: releasing the worker state lock %s: %w", path, err)
	}
	if parseNodeLockOwner(content) != os.Getpid() {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("execution: releasing the worker state lock %s: %w", path, err)
	}
	return nil
}

// nodeLockOccupied is the refusal of a folder somebody else serves, naming
// the process when the lock names one.
func nodeLockOccupied(path string, owner int) error {
	if owner != 0 {
		return NewDiagnostic(CodeConfiguration, CategoryConfiguration,
			"the worker state folder %s is served by process %d already: two workers sharing one state folder would each hold half the record of what has been answered",
			filepath.Dir(path), owner)
	}
	return NewDiagnostic(CodeConfiguration, CategoryConfiguration,
		"the worker state folder %s is served by another process already: two workers sharing one state folder would each hold half the record of what has been answered",
		filepath.Dir(path))
}

// claimNodeLock writes this process id into an unheld lock and answers the
// process id of a live holder instead.
//
// Taking over a stale lock is the part worth reading. Removing the file and
// creating a new one is two operations, and two processes that both read the
// same stale content can interleave them: the second one removes the lock the
// first has just written and claims the folder, so both believe they own it
// and each holds half the record of what has been answered. That is the one
// thing this lock exists to prevent.
//
// So the stale lock is taken over by renaming it aside and then looking at
// what was actually renamed. A process that finds its own stale content took
// over the lock it meant to; one that finds a live process's content has taken
// a fresh claim away from its owner, puts it straight back and reports that
// owner. Renaming is the primitive rather than removing because it is what
// makes the second half possible at all: a removed file cannot be examined and
// cannot be put back.
//
// The window this leaves is the instant the path is absent between the rename
// aside and a restore, in which a third process could create a lock of its
// own. That is why the restore looks before it writes, and why lockNodeState
// lets every claim settle before it trusts it.
func claimNodeLock(path string) (int, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err == nil {
		return 0, writeNodeLockClaim(file, path)
	}
	if !os.IsExist(err) {
		return 0, fmt.Errorf("execution: opening the worker state lock %s: %w", path, err)
	}
	owner, err := readNodeLockOwner(path)
	if errors.Is(err, fs.ErrNotExist) {
		// The lock went between the two steps: its owner gave it back, or
		// another process is taking it over. Either way the path is free
		// only for whoever creates it first.
		return claimNodeLockAfterTakeover(path)
	}
	if err != nil {
		return 0, fmt.Errorf("execution: reading the worker state lock %s: %w", path, err)
	}
	if isLiveLockOwner(owner) {
		return owner, nil
	}
	return takeOverNodeLock(path, owner)
}

// isLiveLockOwner reports whether the process a lock names is another process
// that is still running.
//
// This process's own id is never a live owner. It has not written its claim
// yet when it asks, so a lock naming it was written by an earlier process that
// happened to have the same id, which is the ordinary case for a worker
// restarted in a container, where it is process 1 every time. Reading that as
// a live owner would refuse every restart until somebody deleted the file.
func isLiveLockOwner(owner int) bool {
	return owner != 0 && owner != os.Getpid() && IsProcessRunning(owner)
}

// takeOverNodeLock renames a stale lock aside, verifies what it renamed, and
// claims the folder only if the lock it took away really was the stale one.
func takeOverNodeLock(path string, stale int) (int, error) {
	aside := path + ".taken." + strconv.Itoa(os.Getpid())
	if err := os.Rename(path, aside); err != nil {
		// errors.Is rather than os.IsNotExist: a rename fails with an
		// *os.LinkError, which the TinyGo runtime's os.IsNotExist does not
		// look into, so the lost race below would read as a failure there.
		if errors.Is(err, fs.ErrNotExist) {
			// Somebody else took the same stale lock over first; the folder is
			// theirs unless they have not written their claim yet, which the
			// second attempt below finds out.
			return claimNodeLockAfterTakeover(path)
		}
		return 0, fmt.Errorf("execution: taking over the worker state lock %s: %w", path, err)
	}
	if taken := readTakenNodeLockOwner(aside); taken != stale && isLiveLockOwner(taken) {
		// A fresh claim, written between the read above and the rename: it is
		// put back and its owner is reported, which is what the doc comment of
		// the refusal has always promised and what a plain remove could not
		// deliver.
		return restoreNodeLock(path, aside, taken)
	}
	if err := os.Remove(aside); err != nil && !os.IsNotExist(err) {
		return 0, fmt.Errorf("execution: removing the stale worker state lock %s: %w", aside, err)
	}
	return claimNodeLockAfterTakeover(path)
}

// readTakenNodeLockOwner is the process id a renamed lock names, and zero for
// one that names nobody. It never waits: the file is no longer where a writer
// would be writing it, so there is nothing to wait for.
func readTakenNodeLockOwner(path string) int {
	content, err := readNodeLockContent(path)
	if err != nil {
		return 0
	}
	return parseNodeLockOwner(content)
}

// restoreNodeLock puts a live owner's lock back and answers that owner.
//
// It looks before it writes: a path a third process has claimed in the
// meantime belongs to that process, so the renamed file is simply dropped and
// the owner this process took away is still the one it reports, since both
// answers refuse this process the folder.
func restoreNodeLock(path, aside string, owner int) (int, error) {
	if _, err := os.Stat(path); err == nil {
		if err := os.Remove(aside); err != nil && !os.IsNotExist(err) {
			return 0, fmt.Errorf("execution: removing the worker state lock copy %s: %w", aside, err)
		}
		return owner, nil
	}
	if err := os.Rename(aside, path); err != nil {
		return 0, fmt.Errorf("execution: restoring the worker state lock %s: %w", path, err)
	}
	return owner, nil
}

// readNodeLockOwner answers the process id a lock names, and zero when it
// names nobody: content that is no process id, or a file that stayed empty
// past the grace.
//
// The wait is what keeps a live owner's claim. Creating the lock and writing
// the process id into it are two operations, so a second process can open the
// file in the instant between them; reading that instant as a stale lock would
// remove the claim of a process that is starting up and leave two processes
// serving one folder, each holding half the record of answered work. A file
// that is still empty after the grace belongs to a writer that died between
// the two steps, and is taken over like any other stale lock.
func readNodeLockOwner(path string) (int, error) {
	deadline := time.Now().Add(stateLockWriteGrace)
	for {
		content, err := readNodeLockContent(path)
		if err != nil {
			return 0, err
		}
		if content != "" {
			return parseNodeLockOwner(content), nil
		}
		if !time.Now().Before(deadline) {
			return 0, nil
		}
		time.Sleep(stateLockWritePoll)
	}
}

// readNodeLockContent is a lock's content without surrounding space. Content
// longer than any process id is refused rather than read whole.
func readNodeLockContent(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	held, err := io.ReadAll(io.LimitReader(file, stateLockOwnerMaxBytes+1))
	if err != nil {
		return "", err
	}
	if len(held) > stateLockOwnerMaxBytes {
		return "", fmt.Errorf("worker state lock owner exceeds %d bytes", stateLockOwnerMaxBytes)
	}
	return strings.TrimSpace(string(held)), nil
}

// parseNodeLockOwner reads a lock's content as a process id. Anything else
// names nobody, which makes the lock stale rather than an error: the file is
// this engine's own, and content it never writes is a leftover to replace.
func parseNodeLockOwner(content string) int {
	owner, err := strconv.Atoi(content)
	if err != nil || owner < 0 {
		return 0
	}
	return owner
}

// claimNodeLockAfterTakeover is the second and last attempt, after a stale
// lock was removed. A folder claimed in the meantime belongs to whoever
// claimed it, and is reported as theirs.
func claimNodeLockAfterTakeover(path string) (int, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if os.IsExist(err) {
		owner, readErr := readNodeLockOwner(path)
		if readErr == nil && isLiveLockOwner(owner) {
			return owner, nil
		}
		return 0, nodeLockOccupied(path, 0)
	}
	if err != nil {
		return 0, fmt.Errorf("execution: claiming the worker state lock %s: %w", path, err)
	}
	return 0, writeNodeLockClaim(file, path)
}

// writeNodeLockClaim writes this process id into a lock this process has just
// created, and refuses a short write even when a runtime reports it without an
// error: a truncated id names somebody else.
func writeNodeLockClaim(file *os.File, path string) error {
	owner := strconv.Itoa(os.Getpid())
	n, err := file.WriteString(owner)
	if err == nil && n != len(owner) {
		err = io.ErrShortWrite
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("execution: writing the worker state lock %s: %w", path, err)
	}
	return nil
}

// SeenSet is the record of the work this node has already answered, kept as
// (run, task, attempt) triples with the time each was accepted.
//
// It is what makes duplicate delivery recognisable (§28.3): a mailbox may
// hold the same assignment twice, an orchestrator may re-offer one on a new
// branch, and neither may make this node run the same attempt again. Entries
// older than the longest time an accepted message can remain valid are
// dropped. A message issued one replay window in the future can be accepted
// now and remain valid for another window; retaining only one window would
// let the same attempt run again after a restart or ref rewind.
//
// The file is one JSON object from triple to acceptance time, and a record is
// appended to it in place rather than the whole object rewritten: a busy node
// holds thousands of triples, and rewriting all of them made every claim cost
// the whole record. A reader of the object keeps the last value of a repeated
// name, so a triple accepted again is appended again, and a triple dropped
// from memory is appended once more with the zero time, which every horizon
// drops on reading; without that, a wall clock stepped back before a restart
// would bring back a triple this process had already forgotten. The file is
// rewritten whole once it holds twice the records still live, so it stays
// within a constant factor of the acceptance horizon, and whenever it is not
// the file this process last left, so that a change from outside is repaired
// by the next claim as it was when every claim rewrote it.
type SeenSet struct {
	path    string
	entries map[string]seenEntry
	// Expiry waits in two queues because a time with a monotonic clock
	// reading is compared by that reading and a time without one by the wall
	// clock, and the two orders part once the wall clock is stepped.
	wallExpiry      seenQueue
	monotonicExpiry seenQueue
	recorded        uint64
	// forgotten are the triples dropped from memory since the file last
	// caught up with it.
	forgotten []string
	// stored is how many records the file holds, live or expired.
	stored int
	// written is the file as this process last left it, its last byte the
	// closing brace. It is nil when the file is not known to hold every live
	// entry in that layout: it was read rather than written by this process,
	// or a write failed.
	written os.FileInfo
}

// seenEntry is one remembered triple: when it was accepted, and which
// recording of it that was, so that the expiry of an earlier recording of the
// same triple does not forget a later one.
type seenEntry struct {
	at      time.Time
	version uint64
}

// seenExpiry is one recording waiting for its acceptance time to leave the
// horizon.
type seenExpiry struct {
	triple  string
	at      time.Time
	version uint64
}

// seenRetentionWindow is measured from acceptance, not issuance. CheckHeader
// admits an issuedAt up to one replayWindow in the future, and that message
// remains admissible until one replayWindow after its issuedAt.
const seenRetentionWindow = 2 * replayWindow

// LoadSeenSet reads one node's record, pruned to the acceptance horizon, and
// answers an empty one for a node that has answered nothing yet.
func LoadSeenSet(path string, now time.Time) (*SeenSet, error) {
	set := &SeenSet{path: path, entries: map[string]seenEntry{}}
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return set, nil
	}
	if err != nil {
		return nil, fmt.Errorf("execution: reading the answered-work record %s: %w", path, err)
	}
	stored, err := decodeSeenRecord(content)
	if err != nil {
		return nil, fmt.Errorf("execution: reading the answered-work record %s: %w", path, err)
	}
	cutoff := now.Add(-seenRetentionWindow)
	loaded := make([]seenExpiry, 0, len(stored))
	for triple, at := range stored {
		if at.Before(cutoff) {
			continue
		}
		loaded = append(loaded, seenExpiry{triple: triple, at: at})
	}
	// Sorted, the recordings enter their queue without moving, and the order
	// the file is next rewritten in does not depend on the map's.
	slices.SortFunc(loaded, compareSeenExpiry)
	for _, recording := range loaded {
		set.remember(recording.triple, recording.at)
	}
	return set, nil
}

// decodeSeenRecord reads the file's object, or what an append that did not
// finish left of it.
func decodeSeenRecord(content []byte) (map[string]time.Time, error) {
	stored := map[string]time.Time{}
	err := json.Unmarshal(content, &stored)
	if err == nil {
		return stored, nil
	}
	if cut, isCut := decodeCutSeenRecord(content); isCut {
		return cut, nil
	}
	return nil, err
}

// decodeCutSeenRecord reads a file that ends inside its object with nothing
// before the end that a reader would refuse. An append overwrites the closing
// brace with its first byte, so a process killed partway through leaves
// exactly that. The members before the cut are kept and a member whose value
// was cut is dropped, which is safe because the Record writing it never
// returned, so its attempt was never run; a member cut after its value is
// complete is kept, as a claim that did return.
func decodeCutSeenRecord(content []byte) (map[string]time.Time, bool) {
	decoder := json.NewDecoder(bytes.NewReader(content))
	if open, err := decoder.Token(); err != nil || open != json.Delim('{') {
		return nil, false
	}
	// An append writes at least one byte where the closing brace was, so a
	// file that ends at its opening brace was not cut by one.
	if decoder.InputOffset() == int64(len(content)) {
		return nil, false
	}
	stored := map[string]time.Time{}
	for {
		name, err := decoder.Token()
		if isCutShort(err) {
			return stored, true
		}
		triple, isName := name.(string)
		if err != nil || !isName {
			return nil, false
		}
		var at time.Time
		err = decoder.Decode(&at)
		if isCutShort(err) {
			return stored, true
		}
		if err != nil {
			return nil, false
		}
		stored[triple] = at
	}
}

// isCutShort reports whether a decoder stopped because its input ended.
func isCutShort(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

// compareSeenExpiry orders recordings by acceptance time, then by triple.
func compareSeenExpiry(first, second seenExpiry) int {
	if order := first.at.Compare(second.at); order != 0 {
		return order
	}
	return strings.Compare(first.triple, second.triple)
}

// IsSeen reports whether this node has already answered one attempt.
func (s *SeenSet) IsSeen(run, task string, attempt int) bool {
	_, isSeen := s.entries[formatTriple(run, task, attempt)]
	return isSeen
}

// Record remembers one answered attempt and persists the record.
//
// It is written before the work is reported rather than after, because the
// question the record answers is "did this node already take this on", and a
// node that crashed between claiming and reporting must not take the same
// attempt on again. A record is appended by one positioned write, and a write
// that fails falls back to replacing the file through a temporary file and a
// rename. A process killed during the positioned write leaves the object
// without its closing brace, which LoadSeenSet reads as the members before
// the cut.
func (s *SeenSet) Record(run, task string, attempt int, now time.Time) error {
	// A worker may serve for days without reloading this file. Prune at each
	// write so its memory stays within the full acceptance horizon, while
	// retaining newer knowledge of a repeated tuple.
	s.prune(now.Add(-seenRetentionWindow))
	triple := formatTriple(run, task, attempt)
	if entry, isKnown := s.entries[triple]; isKnown && !now.After(entry.at) {
		return s.store(nil)
	}
	s.remember(triple, now)
	return s.store(&seenExpiry{triple: triple, at: now})
}

// remember holds one recording of a triple in memory and queues its expiry.
func (s *SeenSet) remember(triple string, at time.Time) {
	s.recorded++
	s.entries[triple] = seenEntry{at: at, version: s.recorded}
	queue := &s.wallExpiry
	if isMonotonic(at) {
		queue = &s.monotonicExpiry
	}
	queue.push(seenExpiry{triple: triple, at: at, version: s.recorded})
}

// isMonotonic reports whether a time carries a monotonic clock reading, which
// Round(0) is documented to strip.
func isMonotonic(at time.Time) bool {
	return at != at.Round(0)
}

// prune forgets every entry accepted before the cutoff.
func (s *SeenSet) prune(cutoff time.Time) {
	s.wallExpiry.popBefore(cutoff, s.forget)
	if isMonotonic(cutoff) {
		s.monotonicExpiry.popBefore(cutoff, s.forget)
		return
	}
	// A cutoff without a monotonic reading compares even the monotonic
	// recordings by wall clock, which is an order their queue does not keep.
	s.monotonicExpiry.removeBefore(cutoff, s.forget)
}

// forget drops the entry one expired recording made, unless the triple has
// been recorded again since.
func (s *SeenSet) forget(expired seenExpiry) {
	if s.entries[expired.triple].version == expired.version {
		delete(s.entries, expired.triple)
		s.forgotten = append(s.forgotten, expired.triple)
	}
}

// store brings the file up to memory: the triples forgotten since it last did
// and the recording just made, if any. It rewrites the file instead when that
// is due.
func (s *SeenSet) store(recorded *seenExpiry) error {
	if s.written == nil {
		return s.rewrite()
	}
	// The file already reads as memory does, so a write here could change
	// nothing a reload answers, only fail; and every caller asks IsSeen
	// before it records.
	if len(s.forgotten) == 0 && recorded == nil {
		return nil
	}
	if s.stored >= 2*len(s.entries) {
		return s.rewrite()
	}
	if err := s.append(recorded); err != nil {
		// The rewrite is the write this record always had, so a file that
		// cannot be written in place fails no claim the rewrite would not.
		return s.rewrite()
	}
	return nil
}

// append writes the pending records over the file's closing brace, in one
// positioned write: the forgotten triples first, so that a triple forgotten
// and recorded again in one call reads as recorded.
func (s *SeenSet) append(recorded *seenExpiry) error {
	pending := make([]seenExpiry, 0, len(s.forgotten)+1)
	for _, triple := range s.forgotten {
		pending = append(pending, seenExpiry{triple: triple})
	}
	if recorded != nil {
		pending = append(pending, *recorded)
	}
	records, err := formatSeenRecords(pending, s.stored > 0)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(s.path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	err = s.writeRecords(file, records)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	s.stored += len(pending)
	s.forgotten = s.forgotten[:0]
	return nil
}

// errSeenRecordChanged refuses an append to a file that is not the one this
// process last left: only a rewrite puts every live record back into it.
var errSeenRecordChanged = errors.New("execution: the answered-work record was changed by another writer")

// writeRecords writes the records over the closing brace of the opened file.
func (s *SeenSet) writeRecords(file *os.File, records []byte) error {
	brace := s.written.Size() - 1
	if err := s.verifyUnchanged(file, brace); err != nil {
		return err
	}
	written, err := file.WriteAt(records, brace)
	if err == nil && written != len(records) {
		err = io.ErrShortWrite
	}
	if err != nil {
		// The rewrite that follows replaces the file whatever this leaves; it
		// only keeps the file readable should the process die before that.
		_ = file.Truncate(brace + 1)
		_, _ = file.WriteAt([]byte{'}'}, brace)
		return err
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	s.written = info
	return nil
}

// verifyUnchanged refuses the opened file unless it is the file this process
// last left and still ends in the closing brace the append overwrites.
func (s *SeenSet) verifyUnchanged(file *os.File, brace int64) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(info, s.written) || info.Size() != s.written.Size() || !info.ModTime().Equal(s.written.ModTime()) {
		return errSeenRecordChanged
	}
	var last [1]byte
	if _, err := file.ReadAt(last[:], brace); err != nil {
		return err
	}
	if last[0] != '}' {
		return errSeenRecordChanged
	}
	return nil
}

// formatSeenRecords is the members of the file's object that follow its last
// one, and the closing brace.
func formatSeenRecords(pending []seenExpiry, isAfterAMember bool) ([]byte, error) {
	var records []byte
	for _, recording := range pending {
		record, err := formatSeenRecord(recording.triple, recording.at)
		if err != nil {
			return nil, err
		}
		if isAfterAMember {
			records = append(records, ',')
		}
		records = append(records, record...)
		isAfterAMember = true
	}
	return append(records, '}'), nil
}

// rewrite replaces the file with every live entry, through a temporary file
// and a rename.
func (s *SeenSet) rewrite() error {
	s.written = nil
	content, err := s.formatRecord()
	if err != nil {
		return fmt.Errorf("execution: writing the answered-work record: %w", err)
	}
	temporary := s.path + ".tmp"
	if err := fsx.WriteFileComplete(temporary, content, 0o644); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("execution: writing the answered-work record %s: %w", temporary, err)
	}
	if err := os.Rename(temporary, s.path); err != nil {
		return fmt.Errorf("execution: replacing the answered-work record %s: %w", s.path, err)
	}
	s.stored = len(s.entries)
	s.forgotten = s.forgotten[:0]
	// A file this process cannot look at is still written; the next claim
	// rewrites it rather than append to a file it cannot vouch for.
	if info, err := os.Stat(s.path); err == nil {
		s.written = info
	}
	return nil
}

// formatRecord is the whole file: one JSON object of every live entry, in the
// order their recordings wait to expire.
func (s *SeenSet) formatRecord() ([]byte, error) {
	live := s.liveRecordings()
	content := []byte{'{'}
	for _, recording := range live {
		record, err := formatSeenRecord(recording.triple, recording.at)
		if err != nil {
			return nil, err
		}
		if len(content) > 1 {
			content = append(content, ',')
		}
		content = append(content, record...)
	}
	return append(content, '}'), nil
}

// liveRecordings is every claim the record still holds, ordered by time and
// then by triple, the order LoadSeenSet reads them back in, so a rewrite of
// the same claims is the same bytes whatever the heaps' shape.
func (s *SeenSet) liveRecordings() []seenExpiry {
	var live []seenExpiry
	for _, queue := range []seenQueue{s.wallExpiry, s.monotonicExpiry} {
		for _, recording := range queue {
			if s.entries[recording.triple].version == recording.version {
				live = append(live, recording)
			}
		}
	}
	slices.SortFunc(live, func(a, b seenExpiry) int {
		if c := a.at.Compare(b.at); c != 0 {
			return c
		}
		return strings.Compare(a.triple, b.triple)
	})
	return live
}

// formatSeenRecord is one member of the file's object, encoded exactly as
// encoding/json encodes one entry of a map from triple to time.
func formatSeenRecord(triple string, at time.Time) ([]byte, error) {
	name, err := json.Marshal(triple)
	if err != nil {
		return nil, err
	}
	value, err := at.MarshalJSON()
	if err != nil {
		return nil, err
	}
	return append(append(name, ':'), value...), nil
}

// seenQueue is a binary min-heap of recordings by acceptance time.
type seenQueue []seenExpiry

// push queues one recording.
func (q *seenQueue) push(recording seenExpiry) {
	*q = append(*q, recording)
	queue := *q
	for child := len(queue) - 1; child > 0; {
		parent := (child - 1) / 2
		if !queue[child].at.Before(queue[parent].at) {
			return
		}
		queue[child], queue[parent] = queue[parent], queue[child]
		child = parent
	}
}

// popBefore hands every recording accepted before the cutoff to expire,
// earliest first.
func (q *seenQueue) popBefore(cutoff time.Time, expire func(seenExpiry)) {
	for len(*q) > 0 && (*q)[0].at.Before(cutoff) {
		expire(q.popEarliest())
	}
}

// popEarliest takes the earliest recording out of the queue.
func (q *seenQueue) popEarliest() seenExpiry {
	queue := *q
	earliest := queue[0]
	last := len(queue) - 1
	queue[0] = queue[last]
	queue[last] = seenExpiry{}
	*q = queue[:last]
	q.siftDown(0)
	return earliest
}

// removeBefore hands every recording accepted before the cutoff to expire,
// whatever order the queue keeps, and rebuilds the queue from the rest.
func (q *seenQueue) removeBefore(cutoff time.Time, expire func(seenExpiry)) {
	queue := *q
	kept := queue[:0]
	for _, recording := range queue {
		if recording.at.Before(cutoff) {
			expire(recording)
			continue
		}
		kept = append(kept, recording)
	}
	clear(queue[len(kept):])
	*q = kept
	for parent := len(kept)/2 - 1; parent >= 0; parent-- {
		q.siftDown(parent)
	}
}

// siftDown moves one recording down until neither of its children is earlier.
func (q *seenQueue) siftDown(parent int) {
	queue := *q
	for {
		child := 2*parent + 1
		if child >= len(queue) {
			return
		}
		if child+1 < len(queue) && queue[child+1].at.Before(queue[child].at) {
			child++
		}
		if !queue[child].at.Before(queue[parent].at) {
			return
		}
		queue[child], queue[parent] = queue[parent], queue[child]
		parent = child
	}
}

// formatTriple is the key one attempt is remembered under. The separator is a
// space because a run id is hex, an attempt is a number and a task name is
// whatever the plan called a package, so a space cannot make two different
// triples collide the way a dot or a dash could.
func formatTriple(run, task string, attempt int) string {
	return run + " " + task + " " + strconv.Itoa(attempt)
}
