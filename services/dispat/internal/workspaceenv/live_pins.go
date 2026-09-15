package workspaceenv

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// LivePins is the inherited path of one release run's private, transient pin
// directory. It coordinates nested commands; it is never repository state.
const LivePins = "DISPAT_INTERNAL_WORKSPACE_LIVE_PINS"

const liveMetadataFile = "context.json"

type livePinMetadata struct {
	Root         string            `json:"root"`
	Config       string            `json:"config"`
	Owners       map[string]string `json:"owners"`
	Repositories []string          `json:"repositories"`
}

type livePinRecord struct {
	Owner    string `json:"owner"`
	Revision string `json:"revision"`
}

// LivePinStore is a parent release's private live-pin directory.
type LivePinStore struct {
	dir    string
	owners map[string]bool
	mu     sync.Mutex
}

// LivePinReader is a validated read handle for one inherited coordinator.
// Its metadata is immutable; Pins reads the owner's atomic record afresh.
type LivePinReader struct {
	dir    string
	owners map[string]bool
}

// NewLivePins creates a private run-scoped coordinator bound to the canonical
// control root, control config, exact package-owner map, and exact source
// repository identities.
func NewLivePins(root, configPath string, owners map[string]string, repositories []string) (*LivePinStore, error) {
	metadata, err := canonicalLiveMetadata(root, configPath, owners, repositories)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "dispat-live-pins-*")
	if err != nil {
		return nil, fmt.Errorf("creating live pin directory: %w", err)
	}
	store := &LivePinStore{dir: dir, owners: liveOwnerSet(metadata)}
	data, err := json.Marshal(metadata)
	if err == nil {
		err = os.WriteFile(filepath.Join(dir, liveMetadataFile), append(data, '\n'), 0o600)
	}
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("writing live pin context: %w", err)
	}
	return store, nil
}

// Environment returns the private path inherited by workspace scripts.
func (s *LivePinStore) Environment() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dir == "" {
		return ""
	}
	return LivePins + "=" + s.dir
}

// Remember atomically publishes one exact source revision. Callers serialize
// same-owner Git mutation and this write as one transaction.
func (s *LivePinStore) Remember(owner, revision string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dir == "" {
		return errors.New("live pin context is closed")
	}
	return writeLivePin(s.dir, s.owners, owner, revision)
}

// Close removes all transient coordination data. It is safe to call twice.
func (s *LivePinStore) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dir == "" {
		return nil
	}
	err := os.RemoveAll(s.dir)
	s.dir = ""
	return err
}

// RememberLivePin publishes through an inherited coordinator. With no live
// context it is a standalone invocation and succeeds without writing. A
// present context must match the workspace exactly or it is an error.
func RememberLivePin(root, configPath string, env []string, owner, revision string) error {
	values := environmentValues(env)
	dir := values[LivePins]
	if dir == "" {
		return nil
	}
	metadata, err := validatedLiveMetadata(root, configPath, values, dir)
	if err != nil {
		return err
	}
	return writeLivePin(dir, liveOwnerSet(metadata), owner, revision)
}

// OpenLivePins validates an inherited coordinator once. It returns nil when
// this environment has no live context.
func OpenLivePins(root, configPath string, env []string) (*LivePinReader, error) {
	values := environmentValues(env)
	dir := values[LivePins]
	if dir == "" {
		return nil, nil
	}
	metadata, err := validatedLiveMetadata(root, configPath, values, dir)
	if err != nil {
		return nil, err
	}
	return &LivePinReader{dir: dir, owners: liveOwnerSet(metadata)}, nil
}

// Pins reads the latest atomic record for one exact source identity.
func (r *LivePinReader) Pins(owner string) ([]string, error) {
	if r == nil {
		return nil, nil
	}
	if !r.owners[owner] {
		return nil, fmt.Errorf("live pin owner %q is not an exact source repository identity", owner)
	}
	record, ok, err := readLivePin(r.dir, owner)
	if err != nil || !ok {
		return nil, err
	}
	return []string{record.Revision}, nil
}

func livePins(root, configPath string, values map[string]string) (map[string][]string, error) {
	dir := values[LivePins]
	if dir == "" {
		return nil, nil
	}
	metadata, err := validatedLiveMetadata(root, configPath, values, dir)
	if err != nil {
		return nil, err
	}
	pins := make(map[string][]string)
	seenOwners := make(map[string]bool)
	for _, owner := range metadata.Repositories {
		if owner == "" || owner == "control" || seenOwners[owner] {
			continue
		}
		seenOwners[owner] = true
		record, ok, err := readLivePin(dir, owner)
		if err != nil {
			return nil, err
		}
		if ok {
			pins[owner] = []string{record.Revision}
		}
	}
	return pins, nil
}

func canonicalLiveMetadata(root, configPath string, owners map[string]string, repositories []string) (livePinMetadata, error) {
	root, err := canonicalLivePath(root)
	if err != nil {
		return livePinMetadata{}, fmt.Errorf("canonicalizing live pin root: %w", err)
	}
	configPath, err = canonicalLivePath(configPath)
	if err != nil {
		return livePinMetadata{}, fmt.Errorf("canonicalizing live pin config: %w", err)
	}
	repositories = append([]string(nil), repositories...)
	slices.Sort(repositories)
	for i, repository := range repositories {
		if repository == "" || repository == "control" {
			return livePinMetadata{}, fmt.Errorf("invalid source repository identity %q", repository)
		}
		if i > 0 && repositories[i-1] == repository {
			return livePinMetadata{}, fmt.Errorf("duplicate source repository identity %q", repository)
		}
	}
	known := make(map[string]bool, len(repositories))
	for _, repository := range repositories {
		known[repository] = true
	}
	for key, repository := range owners {
		if key == "" || repository == "" || repository != "control" && !known[repository] {
			return livePinMetadata{}, fmt.Errorf("package owner %q names unknown repository %q", key, repository)
		}
	}
	return livePinMetadata{Root: root, Config: configPath, Owners: maps.Clone(owners), Repositories: repositories}, nil
}

func canonicalLivePath(path string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(path), nil
}

func validatedLiveMetadata(root, configPath string, values map[string]string, dir string) (livePinMetadata, error) {
	var owners map[string]string
	if err := json.Unmarshal([]byte(values[Owners]), &owners); err != nil || owners == nil {
		return livePinMetadata{}, errors.New("live pin context has no valid package owners")
	}
	var repositories []string
	if err := json.Unmarshal([]byte(values[Repositories]), &repositories); err != nil {
		return livePinMetadata{}, errors.New("live pin context has no valid repository identities")
	}
	want, err := canonicalLiveMetadata(root, configPath, owners, repositories)
	if err != nil {
		return livePinMetadata{}, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return livePinMetadata{}, fmt.Errorf("opening live pin context: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return livePinMetadata{}, errors.New("live pin context is not a directory")
	}
	var got livePinMetadata
	if err := readBoundedJSON(filepath.Join(dir, liveMetadataFile), 8<<20, &got); err != nil {
		return livePinMetadata{}, fmt.Errorf("reading live pin context: %w", err)
	}
	if got.Root != want.Root || got.Config != want.Config || !maps.Equal(got.Owners, want.Owners) ||
		!slices.Equal(got.Repositories, want.Repositories) {
		return livePinMetadata{}, errors.New("live pin context does not match this workspace")
	}
	return got, nil
}

func writeLivePin(dir string, owners map[string]bool, owner, revision string) error {
	if owner == "" || owner == "control" || !owners[owner] {
		return fmt.Errorf("live pin owner %q is not an exact source repository identity", owner)
	}
	if !fullCommit(revision) {
		return fmt.Errorf("live pin for repository %s is not a full commit id", owner)
	}
	record, err := json.Marshal(livePinRecord{Owner: owner, Revision: revision})
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(dir, ".pin-*")
	if err != nil {
		return fmt.Errorf("creating live pin for repository %s: %w", owner, err)
	}
	temporaryName := temporary.Name()
	remove := true
	defer func() {
		_ = temporary.Close()
		if remove {
			_ = os.Remove(temporaryName)
		}
	}()
	err = temporary.Chmod(0o600)
	if err == nil {
		_, err = temporary.Write(append(record, '\n'))
	}
	if err == nil {
		err = temporary.Close()
	}
	if err == nil {
		err = os.Rename(temporaryName, filepath.Join(dir, livePinFilename(owner)))
	}
	if err != nil {
		return fmt.Errorf("publishing live pin for repository %s: %w", owner, err)
	}
	remove = false
	return nil
}

func readLivePin(dir, owner string) (livePinRecord, bool, error) {
	var record livePinRecord
	err := readBoundedJSON(filepath.Join(dir, livePinFilename(owner)), 4096, &record)
	if errors.Is(err, os.ErrNotExist) {
		return livePinRecord{}, false, nil
	}
	if err != nil {
		return livePinRecord{}, false, fmt.Errorf("reading live pin for repository %s: %w", owner, err)
	}
	if record.Owner != owner || !fullCommit(record.Revision) {
		return livePinRecord{}, false, fmt.Errorf("live pin for repository %s is malformed", owner)
	}
	return record, true, nil
}

func readBoundedJSON(path string, limit int64, target any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("live pin file is not a regular file")
	}
	if info.Size() > limit {
		return errors.New("live pin file exceeds its size limit")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	decoder := json.NewDecoder(io.LimitReader(f, limit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("live pin file has trailing data")
	}
	return nil
}

func livePinFilename(owner string) string {
	digest := sha256.Sum256([]byte(owner))
	return hex.EncodeToString(digest[:]) + ".pin"
}

func liveOwnerSet(metadata livePinMetadata) map[string]bool {
	owners := make(map[string]bool)
	for _, candidate := range metadata.Repositories {
		if candidate != "" && candidate != "control" {
			owners[candidate] = true
		}
	}
	return owners
}

func environmentValues(env []string) map[string]string {
	values := make(map[string]string, len(env))
	for _, pair := range env {
		if name, value, ok := strings.Cut(pair, "="); ok {
			values[name] = value
		}
	}
	return values
}
