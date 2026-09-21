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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The files one node's state folder holds. They are named rather than
// composed at the call sites so that a reader of this package can see the
// whole layout in one place.
const (
	stateCacheDir = "cache"
	stateSeenFile = "seen.json"
	stateLockFile = "worker.lock"
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
	}
	if err := os.MkdirAll(state.Cache, 0o755); err != nil {
		return nil, nil, fmt.Errorf("execution: preparing the node state folder %s: %w", dir, err)
	}
	release, err := lockNodeState(filepath.Join(dir, stateLockFile))
	if err != nil {
		return nil, nil, err
	}
	return state, release, nil
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
func lockNodeState(path string) (func() error, error) {
	owner, err := claimNodeLock(path)
	if err != nil {
		return nil, err
	}
	if owner != 0 {
		return nil, NewDiagnostic(CodeConfiguration, CategoryConfiguration,
			"the worker state folder %s is served by process %d already: two workers sharing one state folder would each hold half the record of what has been answered",
			filepath.Dir(path), owner)
	}
	return func() error {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("execution: releasing the worker state lock %s: %w", path, err)
		}
		return nil
	}, nil
}

// claimNodeLock writes this process id into an unheld lock and answers the
// process id of a live holder instead.
//
// A stale lock is removed and the claim is retried exactly once. Once,
// because the only thing a second round could win against is another process
// taking over the same stale lock at the same instant, and that process is
// then a live owner this one has to report rather than race.
func claimNodeLock(path string) (int, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err == nil {
		defer func() { _ = file.Close() }()
		if _, err := file.WriteString(strconv.Itoa(os.Getpid())); err != nil {
			return 0, fmt.Errorf("execution: writing the worker state lock %s: %w", path, err)
		}
		return 0, nil
	}
	if !os.IsExist(err) {
		return 0, fmt.Errorf("execution: opening the worker state lock %s: %w", path, err)
	}
	held, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("execution: reading the worker state lock %s: %w", path, err)
	}
	owner, err := strconv.Atoi(strings.TrimSpace(string(held)))
	if err == nil && IsProcessRunning(owner) {
		return owner, nil
	}
	if err := os.Remove(path); err != nil {
		return 0, fmt.Errorf("execution: taking over the worker state lock %s: %w", path, err)
	}
	return claimNodeLockAfterTakeover(path)
}

// claimNodeLockAfterTakeover is the second and last attempt, after a stale
// lock was removed. A folder claimed in the meantime belongs to whoever
// claimed it.
func claimNodeLockAfterTakeover(path string) (int, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, fmt.Errorf("execution: claiming the worker state lock %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.WriteString(strconv.Itoa(os.Getpid())); err != nil {
		return 0, fmt.Errorf("execution: writing the worker state lock %s: %w", path, err)
	}
	return 0, nil
}

// SeenSet is the record of the work this node has already answered, kept as
// (run, task, attempt) triples with the time each was accepted.
//
// It is what makes duplicate delivery recognisable (§28.3): a mailbox may
// hold the same assignment twice, an orchestrator may re-offer one on a new
// branch, and neither may make this node run the same attempt again. Entries
// older than the replay window are dropped when the file is read, because a
// message that old is refused by the window anyway and keeping it would make
// the file grow for the life of the node.
type SeenSet struct {
	path    string
	entries map[string]time.Time
}

// LoadSeenSet reads one node's record, pruned to the replay window, and
// answers an empty one for a node that has answered nothing yet.
func LoadSeenSet(path string, now time.Time) (*SeenSet, error) {
	set := &SeenSet{path: path, entries: map[string]time.Time{}}
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return set, nil
	}
	if err != nil {
		return nil, fmt.Errorf("execution: reading the answered-work record %s: %w", path, err)
	}
	stored := map[string]time.Time{}
	if err := json.Unmarshal(content, &stored); err != nil {
		return nil, fmt.Errorf("execution: reading the answered-work record %s: %w", path, err)
	}
	for triple, at := range stored {
		if at.Before(now.Add(-replayWindow)) {
			continue
		}
		set.entries[triple] = at
	}
	return set, nil
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
// attempt on again. The file is replaced through a temporary file and a
// rename, so a record that is being written is never a record that is half
// there.
func (s *SeenSet) Record(run, task string, attempt int, now time.Time) error {
	s.entries[formatTriple(run, task, attempt)] = now
	content, err := json.Marshal(s.entries)
	if err != nil {
		return fmt.Errorf("execution: writing the answered-work record: %w", err)
	}
	temporary := s.path + ".tmp"
	if err := os.WriteFile(temporary, content, 0o644); err != nil {
		return fmt.Errorf("execution: writing the answered-work record %s: %w", temporary, err)
	}
	if err := os.Rename(temporary, s.path); err != nil {
		return fmt.Errorf("execution: replacing the answered-work record %s: %w", s.path, err)
	}
	return nil
}

// formatTriple is the key one attempt is remembered under. The separator is a
// space because a run id is hex, an attempt is a number and a task name is
// whatever the plan called a package, so a space cannot make two different
// triples collide the way a dot or a dash could.
func formatTriple(run, task string, attempt int) string {
	return run + " " + task + " " + strconv.Itoa(attempt)
}
