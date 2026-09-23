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
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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

// A lock file may stay empty this long before its writer is taken for dead,
// polled at the second interval. The grace is orders of magnitude longer than
// the gap it covers (one write after one open) and short enough that a node
// restarted over such a leftover starts within a second.
const (
	stateLockWriteGrace = time.Second
	stateLockWritePoll  = 10 * time.Millisecond
	// A PID is only a few decimal digits. Refuse oversized legacy content
	// instead of allocating unbounded memory before deciding who owns state.
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
}

// OpenNodeState takes ownership of one node's state folder and answers the
// paths inside it together with the release that gives the ownership back.
//
// The lock is held by the kernel on a stable file inode; its PID is diagnostic
// and preserves refusal of an older PID-only worker. A crashed holder releases
// the kernel lock automatically, so restart needs no file deletion.
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

// lockNodeState holds a kernel lock on one stable worker.lock inode for the
// entire serving lifetime. A PID in the file is a diagnostic and a bridge from
// older workers that only wrote a PID; it is not the exclusion primitive.
// Neither acquisition nor release renames or removes the file, so contenders
// can never acquire different inodes through an absent-path window.
func lockNodeState(path string) (func() error, error) {
	file, created, err := openNodeLock(path)
	if err != nil {
		return nil, fmt.Errorf("execution: opening the worker state lock %s: %w", path, err)
	}
	locked, err := tryNodeFileLock(file)
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("execution: acquiring the worker state lock %s: %w", path, err)
	}
	if !locked {
		_ = file.Close()
		// Windows denies a second handle reads of a byte locked exclusively
		// by another process. The kernel already proved occupancy here.
		return nil, nodeLockOccupied(path, 0)
	}
	giveUp := func() {
		_ = unlockNodeFile(file)
		_ = file.Close()
	}
	// Refuse a symlink or a file replaced between open and acquisition.
	// Cooperative workers never replace this path, and this check prevents
	// an accidental alias from making two different lock inodes look equal.
	opened, err := file.Stat()
	if err != nil {
		giveUp()
		return nil, fmt.Errorf("execution: inspecting the worker state lock %s: %w", path, err)
	}
	named, err := os.Lstat(path)
	if err != nil || !os.SameFile(opened, named) {
		giveUp()
		return nil, fmt.Errorf("execution: worker state lock %s was replaced or is a symbolic link", path)
	}
	if !created {
		owner, readErr := readNodeLockOwner(file)
		if readErr != nil {
			giveUp()
			return nil, fmt.Errorf("execution: reading the worker state lock %s: %w", path, readErr)
		}
		// A legacy worker may hold no kernel lock. Respect its live PID;
		// modern workers are refused above by the kernel before this read.
		if owner != 0 && IsProcessRunning(owner) {
			giveUp()
			return nil, nodeLockOccupied(path, owner)
		}
	}
	if err := writeNodeLockOwner(file, os.Getpid()); err != nil {
		giveUp()
		return nil, fmt.Errorf("execution: writing the worker state lock %s: %w", path, err)
	}
	var once sync.Once
	var releaseErr error
	return func() error {
		once.Do(func() {
			// Zero clears the owner without making the file empty. A fresh
			// claimant need not wait the grace reserved for an old worker
			// caught between creating a file and writing its PID.
			clearErr := writeNodeLockOwner(file, 0)
			unlockErr := unlockNodeFile(file)
			closeErr := file.Close()
			releaseErr = errors.Join(clearErr, unlockErr, closeErr)
			if releaseErr != nil {
				releaseErr = fmt.Errorf("execution: releasing the worker state lock %s: %w", path, releaseErr)
			}
		})
		return releaseErr
	}, nil
}

func openNodeLock(path string) (*os.File, bool, error) {
	for attempt := 0; attempt < 3; attempt++ {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o644)
		if err == nil {
			return file, true, nil
		}
		if !os.IsExist(err) {
			return nil, false, err
		}
		file, err = os.OpenFile(path, os.O_RDWR, 0)
		if os.IsNotExist(err) {
			// An older worker may have removed its PID file on exit.
			continue
		}
		return file, false, err
	}
	return nil, false, fmt.Errorf("worker state lock %s kept disappearing", path)
}

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

func writeNodeLockOwner(file *os.File, owner int) error {
	if err := file.Truncate(0); err != nil {
		return err
	}
	if _, err := file.Seek(0, 0); err != nil {
		return err
	}
	if _, err := file.WriteString(strconv.Itoa(owner)); err != nil {
		return err
	}
	return file.Sync()
}

// readNodeLockOwner answers the process id a held lock names, and zero when it
// names nobody: content that is no process id, or a file that stayed empty
// past the grace. The grace preserves compatibility with older workers that
// created the file and only then wrote their PID without a kernel lock. An
// oversized owner is refused rather than treated as an unowned stale file.
func readNodeLockOwner(file *os.File) (int, error) {
	deadline := time.Now().Add(stateLockWriteGrace)
	for {
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return 0, err
		}
		held, err := io.ReadAll(io.LimitReader(file, stateLockOwnerMaxBytes+1))
		if err != nil {
			return 0, err
		}
		if len(held) > stateLockOwnerMaxBytes {
			return 0, fmt.Errorf("worker state lock owner exceeds %d bytes", stateLockOwnerMaxBytes)
		}
		content := strings.TrimSpace(string(held))
		if content != "" {
			return parseNodeLockOwner(content), nil
		}
		if !time.Now().Before(deadline) {
			return 0, nil
		}
		time.Sleep(stateLockWritePoll)
	}
}

// parseNodeLockOwner reads a lock's content as a process id. Anything else
// names nobody; such stale legacy content can be overwritten under the
// kernel lock without removing the inode.
func parseNodeLockOwner(content string) int {
	owner, err := strconv.Atoi(content)
	if err != nil || owner < 0 {
		return 0
	}
	return owner
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
	// A worker may serve for days without reloading this file. Prune at each
	// write so both its memory and the durable record stay within the replay
	// window, while retaining newer knowledge of a repeated tuple.
	cutoff := now.Add(-replayWindow)
	for triple, at := range s.entries {
		if at.Before(cutoff) {
			delete(s.entries, triple)
		}
	}
	triple := formatTriple(run, task, attempt)
	if at, ok := s.entries[triple]; !ok || now.After(at) {
		s.entries[triple] = now
	}
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
