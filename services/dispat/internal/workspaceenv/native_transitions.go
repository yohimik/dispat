package workspaceenv

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// NativeTransitionScope identifies the inherited private context of a nested
// native record command. A standalone command has no live context.
type NativeTransitionScope struct {
	Root        string
	ConfigPath  string
	Environment []string
}

// NativeTagRef is one exact tag object written by a native record.
type NativeTagRef struct {
	Name   string `json:"name"`
	Object string `json:"object"`
	Commit string `json:"commit"`
	Moving bool   `json:"moving"`
}

// NativeTransitionKind distinguishes a package's native record from a
// repository link settlement performed for that package.
type NativeTransitionKind string

const (
	NativeTransitionSourceRecord   NativeTransitionKind = "source-record"
	NativeTransitionLinkSettlement NativeTransitionKind = "link-settlement"
)

// NativeTransition describes the Git effects a nested native command intends
// and, once complete, the exact effects it verified.
type NativeTransition struct {
	Kind         NativeTransitionKind `json:"kind"`
	Owner        string               `json:"owner"`
	Package      string               `json:"package"`
	Version      string               `json:"version"`
	PriorHead    string               `json:"priorHead"`
	NewHead      string               `json:"newHead,omitempty"`
	TagRequested bool                 `json:"tagRequested"`
	PushRequired bool                 `json:"pushRequired"`
	PushComplete bool                 `json:"pushComplete"`
	Tags         []NativeTagRef       `json:"tags,omitempty"`
}

// NativeTransitionState is the state of a run-private native record attempt.
type NativeTransitionState string

const (
	NativeTransitionPending   NativeTransitionState = "pending"
	NativeTransitionCompleted NativeTransitionState = "completed"
	NativeTransitionFailed    NativeTransitionState = "failed"
)

// NativeTransitionRecord is one atomic entry in the current run's native
// record journal. Token identifies the entry, not Git authority.
type NativeTransitionRecord struct {
	Token string                `json:"token"`
	State NativeTransitionState `json:"state"`
	NativeTransition
}

// NativeJournalRevision is an O(1) observation of an owner's append-only
// change journal. Compare the complete value before and after reading Git.
type NativeJournalRevision struct {
	Exists bool
	Size   int64
	Tail   [32]byte
}

// BeginNativeTransition makes a pending record visible before any native Git
// mutation. No inherited context means a standalone command has no journal.
func BeginNativeTransition(scope NativeTransitionScope, transition NativeTransition) (string, error) {
	dir, owners, err := nativeTransitionDirectory(scope)
	if err != nil || dir == "" {
		return "", err
	}
	record := NativeTransitionRecord{State: NativeTransitionPending, NativeTransition: transition}
	if err := validateNativeRecord(record, owners); err != nil {
		return "", err
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", fmt.Errorf("generating native transition token: %w", err)
	}
	record.Token = hex.EncodeToString(tokenBytes)
	if err := writeNativeRecord(dir, record); err != nil {
		return "", err
	}
	if err := appendNativeEvent(dir, record.Owner); err != nil {
		return "", err
	}
	return record.Token, nil
}

// CompleteNativeTransition publishes exact successful effects only after the
// nested command has verified its requested tag and push work.
func CompleteNativeTransition(scope NativeTransitionScope, token string, transition NativeTransition) error {
	dir, owners, err := nativeTransitionDirectory(scope)
	if err != nil {
		return err
	}
	if dir == "" {
		if token == "" {
			return nil
		}
		return errors.New("native transition context disappeared before completion")
	}
	if !validNativeToken(token) {
		return errors.New("invalid native transition token")
	}
	pending, err := readNativeRecord(nativeRecordLookup{dir: dir, owner: transition.Owner, token: token, owners: owners})
	if err != nil {
		return err
	}
	if pending.State != NativeTransitionPending {
		return errors.New("native transition is no longer pending")
	}
	if !sameNativeIntent(pending.NativeTransition, transition) {
		return errors.New("native transition completion does not match its pending intent")
	}
	record := NativeTransitionRecord{Token: token, State: NativeTransitionCompleted, NativeTransition: transition}
	if err := validateNativeRecord(record, owners); err != nil {
		return err
	}
	// Announce the terminal change first. If either publication fails, the
	// pending record remains visible and cannot authorize a completed step.
	if err := appendNativeEvent(dir, record.Owner); err != nil {
		return err
	}
	return writeNativeRecord(dir, record)
}

// FailNativeTransition leaves a failed attempt visible. If this write fails,
// its pending record remains and the parent must still refuse admission.
func FailNativeTransition(scope NativeTransitionScope, token, owner string) error {
	dir, owners, err := nativeTransitionDirectory(scope)
	if err != nil {
		return err
	}
	if dir == "" {
		if token == "" {
			return nil
		}
		return errors.New("native transition context disappeared before failure recording")
	}
	if !validNativeToken(token) {
		return errors.New("invalid native transition token")
	}
	pending, err := readNativeRecord(nativeRecordLookup{dir: dir, owner: owner, token: token, owners: owners})
	if err != nil {
		return err
	}
	if pending.State != NativeTransitionPending {
		return errors.New("native transition is no longer pending")
	}
	pending.State = NativeTransitionFailed
	if err := appendNativeEvent(dir, owner); err != nil {
		return err
	}
	return writeNativeRecord(dir, pending)
}

// NativeTransitions reads every entry for one exact repository owner. The
// token order is deterministic; callers reconstruct Git history from heads.
func (s *LivePinStore) NativeTransitions(owner string) ([]NativeTransitionRecord, error) {
	if s == nil {
		return nil, nil
	}
	s.mu.Lock()
	dir, known := s.dir, s.transitionOwners[owner]
	s.mu.Unlock()
	if dir == "" {
		return nil, errors.New("live pin context is closed")
	}
	if !known {
		return nil, fmt.Errorf("native transition owner %q is not a participating repository identity", owner)
	}
	return readNativeTransitions(dir, owner, s.transitionOwners)
}

// NativeTransition reads one token afresh without scanning an owner's whole
// journal. A missing token is distinct from a malformed or unreadable record.
func (s *LivePinStore) NativeTransition(owner, token string) (NativeTransitionRecord, bool, error) {
	if s == nil {
		return NativeTransitionRecord{}, false, nil
	}
	s.mu.Lock()
	dir, known := s.dir, s.transitionOwners[owner]
	s.mu.Unlock()
	if dir == "" {
		return NativeTransitionRecord{}, false, errors.New("live pin context is closed")
	}
	if !known {
		return NativeTransitionRecord{}, false,
			fmt.Errorf("native transition owner %q is not a participating repository identity", owner)
	}
	record, err := readNativeRecord(nativeRecordLookup{dir: dir, owner: owner, token: token, owners: s.transitionOwners})
	if errors.Is(err, os.ErrNotExist) {
		return NativeTransitionRecord{}, false, nil
	}
	if err != nil {
		return NativeTransitionRecord{}, false, err
	}
	return record, true, nil
}

// NativeJournalRevision reads only one fixed-size tail record. A native writer
// appends after publishing pending state and before it may mutate Git.
func (s *LivePinStore) NativeJournalRevision(owner string) (NativeJournalRevision, error) {
	if s == nil {
		return NativeJournalRevision{}, nil
	}
	s.mu.Lock()
	dir, known := s.dir, s.transitionOwners[owner]
	s.mu.Unlock()
	if dir == "" {
		return NativeJournalRevision{}, errors.New("live pin context is closed")
	}
	if !known {
		return NativeJournalRevision{},
			fmt.Errorf("native transition owner %q is not a participating repository identity", owner)
	}
	return readNativeRevision(dir, owner)
}

// NativeTransitions reads the current run's owner entries afresh through an
// inherited, validated live context.
func (r *LivePinReader) NativeTransitions(owner string) ([]NativeTransitionRecord, error) {
	if r == nil {
		return nil, nil
	}
	if !r.transitionOwners[owner] {
		return nil, fmt.Errorf("native transition owner %q is not a participating repository identity", owner)
	}
	return readNativeTransitions(r.dir, owner, r.transitionOwners)
}

func nativeTransitionDirectory(scope NativeTransitionScope) (string, map[string]bool, error) {
	values := environmentValues(scope.Environment)
	dir := values[LivePins]
	if dir == "" {
		return "", nil, nil
	}
	metadata, err := validatedLiveMetadata(scope.Root, scope.ConfigPath, values, dir)
	if err != nil {
		return "", nil, err
	}
	return dir, nativeTransitionOwnerSet(metadata), nil
}

func sameNativeIntent(pending, complete NativeTransition) bool {
	return pending.Kind == complete.Kind && pending.Owner == complete.Owner && pending.Package == complete.Package &&
		pending.Version == complete.Version && pending.PriorHead == complete.PriorHead &&
		pending.TagRequested == complete.TagRequested && pending.PushRequired == complete.PushRequired
}

func validateNativeRecord(record NativeTransitionRecord, owners map[string]bool) error {
	if !validNativeToken(record.Token) && record.Token != "" {
		return errors.New("invalid native transition token")
	}
	if !owners[record.Owner] {
		return fmt.Errorf("native transition owner %q is not a participating repository identity", record.Owner)
	}
	if record.Kind != NativeTransitionSourceRecord && record.Kind != NativeTransitionLinkSettlement {
		return fmt.Errorf("unknown native transition kind %q", record.Kind)
	}
	if record.Kind == NativeTransitionLinkSettlement && record.TagRequested {
		return errors.New("a link settlement cannot request release tags")
	}
	if record.Package == "" || strings.TrimSpace(record.Package) != record.Package ||
		record.Version == "" || strings.TrimSpace(record.Version) != record.Version || !fullCommit(record.PriorHead) {
		return errors.New("native transition has an invalid package, version or prior head")
	}
	switch record.State {
	case NativeTransitionPending, NativeTransitionFailed:
		if record.NewHead != "" || len(record.Tags) != 0 || record.PushComplete {
			return errors.New("unfinished native transition claims completed Git effects")
		}
	case NativeTransitionCompleted:
		if !fullCommit(record.NewHead) || record.PushComplete != record.PushRequired ||
			record.TagRequested != (len(record.Tags) > 0) {
			return errors.New("completed native transition lacks its requested Git effects")
		}
		seen := make(map[string]bool, len(record.Tags))
		for _, tag := range record.Tags {
			if tag.Name == "" || strings.ContainsAny(tag.Name, "\x00\n\r\t ") ||
				!fullCommit(tag.Object) || tag.Commit != record.NewHead || seen[tag.Name] {
				return errors.New("completed native transition has an invalid exact tag")
			}
			seen[tag.Name] = true
		}
	default:
		return fmt.Errorf("unknown native transition state %q", record.State)
	}
	return nil
}

func readNativeTransitions(dir, owner string, owners map[string]bool) ([]NativeTransitionRecord, error) {
	if err := verifyNativeDirectory(dir); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("listing native transitions for %s: %w", owner, err)
	}
	prefix := nativeOwnerPrefix(owner)
	var records []NativeTransitionRecord
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		token, ok := strings.CutSuffix(strings.TrimPrefix(name, prefix), ".json")
		if !ok || !validNativeToken(token) {
			return nil, fmt.Errorf("malformed native transition entry %q", name)
		}
		record, err := readNativeRecord(nativeRecordLookup{dir: dir, owner: owner, token: token, owners: owners})
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	slices.SortFunc(records, func(a, b NativeTransitionRecord) int { return strings.Compare(a.Token, b.Token) })
	return records, nil
}

func verifyNativeDirectory(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("opening native transition context: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("native transition context is not a directory")
	}
	return nil
}

type nativeRecordLookup struct {
	dir    string
	owner  string
	token  string
	owners map[string]bool
}

func readNativeRecord(lookup nativeRecordLookup) (NativeTransitionRecord, error) {
	if !validNativeToken(lookup.token) {
		return NativeTransitionRecord{}, errors.New("invalid native transition token")
	}
	if err := verifyNativeDirectory(lookup.dir); err != nil {
		return NativeTransitionRecord{}, err
	}
	var record NativeTransitionRecord
	path := filepath.Join(lookup.dir, nativeRecordName(lookup.owner, lookup.token))
	if err := readBoundedJSON(path, 64<<10, &record); err != nil {
		return NativeTransitionRecord{}, fmt.Errorf("reading native transition for %s: %w", lookup.owner, err)
	}
	if record.Token != lookup.token || record.Owner != lookup.owner {
		return NativeTransitionRecord{}, errors.New("native transition identity does not match its file")
	}
	if err := validateNativeRecord(record, lookup.owners); err != nil {
		return NativeTransitionRecord{}, err
	}
	return record, nil
}

func writeNativeRecord(dir string, record NativeTransitionRecord) error {
	if !validNativeToken(record.Token) {
		return errors.New("invalid native transition token")
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encoding native transition: %w", err)
	}
	temporary, err := os.CreateTemp(dir, ".native-*")
	if err != nil {
		return fmt.Errorf("creating native transition: %w", err)
	}
	temporaryName := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryName)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if n, err := temporary.Write(append(payload, '\n')); err != nil {
		return err
	} else if n != len(payload)+1 {
		return io.ErrShortWrite
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryName, filepath.Join(dir, nativeRecordName(record.Owner, record.Token))); err != nil {
		return fmt.Errorf("publishing native transition: %w", err)
	}
	return nil
}

func nativeOwnerPrefix(owner string) string {
	digest := sha256.Sum256([]byte(owner))
	return "native-" + hex.EncodeToString(digest[:]) + "-"
}

func nativeTransitionOwnerSet(metadata livePinMetadata) map[string]bool {
	owners := make(map[string]bool, len(metadata.Repositories))
	for _, owner := range metadata.Repositories {
		owners[owner] = true
	}
	return owners
}

func nativeRecordName(owner, token string) string {
	return nativeOwnerPrefix(owner) + token + ".json"
}

func nativeRevisionName(owner string) string {
	return strings.TrimSuffix(nativeOwnerPrefix(owner), "-") + ".epoch"
}

func appendNativeEvent(dir, owner string) error {
	if err := verifyNativeDirectory(dir); err != nil {
		return err
	}
	path := filepath.Join(dir, nativeRevisionName(owner))
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size()%32 != 0 {
			return errors.New("native transition revision is malformed")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	event := make([]byte, 32)
	if _, err := rand.Read(event); err != nil {
		return fmt.Errorf("generating native transition revision: %w", err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("opening native transition revision: %w", err)
	}
	n, writeErr := file.Write(event)
	closeErr := file.Close()
	if writeErr != nil {
		return fmt.Errorf("appending native transition revision: %w", writeErr)
	}
	if n != len(event) {
		return io.ErrShortWrite
	}
	if closeErr != nil {
		return fmt.Errorf("closing native transition revision: %w", closeErr)
	}
	return nil
}

func readNativeRevision(dir, owner string) (NativeJournalRevision, error) {
	if err := verifyNativeDirectory(dir); err != nil {
		return NativeJournalRevision{}, err
	}
	path := filepath.Join(dir, nativeRevisionName(owner))
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return NativeJournalRevision{}, nil
	}
	if err != nil {
		return NativeJournalRevision{}, fmt.Errorf("reading native transition revision: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size()%32 != 0 {
		return NativeJournalRevision{}, errors.New("native transition revision is malformed")
	}
	revision := NativeJournalRevision{Exists: true, Size: info.Size()}
	if info.Size() == 0 {
		return revision, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return NativeJournalRevision{}, fmt.Errorf("opening native transition revision: %w", err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return NativeJournalRevision{}, errors.New("native transition revision changed during read")
	}
	if _, err := file.ReadAt(revision.Tail[:], revision.Size-32); err != nil {
		return NativeJournalRevision{}, fmt.Errorf("reading native transition revision tail: %w", err)
	}
	return revision, nil
}

func validNativeToken(token string) bool {
	if len(token) != 64 {
		return false
	}
	for _, digit := range token {
		if !((digit >= '0' && digit <= '9') || (digit >= 'a' && digit <= 'f')) {
			return false
		}
	}
	return true
}
