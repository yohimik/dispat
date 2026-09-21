// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// Putting a verified output set where the package that consumes it expects it
// (CCME §28.5).
//
// Installation is atomic at the task-input boundary: a task cannot observe a
// partial output set. That is two things here. Every file is written into a
// staging folder of its own first, so a transfer that fails halfway has
// written nothing anybody can see; and each declared root then replaces its
// destination by a rename, so the folder a consumer's command opens is either
// entirely the old one or entirely the new one. Whether the roots of several
// providers are all in place before the first command runs is the caller's
// ordering, and it is why nothing here starts anything.
//
// Every byte is verified while it is written rather than afterwards. Reading a
// file back to check it would be a second pass over gigabytes and, worse,
// would check what is on disk a moment after the moment it mattered: a digest
// computed as the bytes go past is a digest of exactly what was written.
//
// Nothing here uses a platform-specific open flag or a mode beyond the two an
// entry may carry, because this code is compiled for TinyGo and for Windows as
// well as for the machines a CI job usually runs on.

import (
	"context"

	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// The two folder names an installation leaves in a checkout while it works.
// Both are swept at start-up as well as removed on every path here, because
// the one thing that can leave them behind is a process that was killed.
const (
	// stagingDirName is where a set is assembled before any of it is visible
	// under the name a consumer reads.
	stagingDirName = ".dispat-staging"
	// asidePrefix begins the name a replaced root is moved to. The rename is
	// what makes the replacement atomic, and the old folder is removed
	// afterwards rather than before, so a failed install can put it back.
	asidePrefix = ".dispat-old-"
)

// InstallRequest is one output set being put in place: the store its bytes are
// in, the manifest that describes them, where the roots go, and where the
// assembly happens.
type InstallRequest struct {
	// Git is the object store holding the manifest's tree, which is the
	// mailbox's own store on either machine.
	Git *gitx.LocalGitx
	// Manifest is the set, already validated: nothing here re-checks a path,
	// because a path that reached this far has been held to every rule by the
	// one function both readers use.
	Manifest *OutputManifest
	// Dir is the folder the declared roots are installed into, absolute. It is
	// the consuming package's folder in a task checkout, or the orchestrator's
	// own checkout of the package that produced the set.
	Dir string
	// Staging is the folder the set is assembled in, absolute. It is created
	// here and removed on every path, and it is never inside Dir, so a build
	// that looks at its own folder never sees a half-written transfer.
	Staging string
	// Log is the logger of whoever is installing.
	Log zerolog.Logger
}

// InstallOutputs materializes one verified output set and replaces the
// declared roots of the destination with it.
//
// The set is assembled, then moved: a failure before the first rename leaves
// the destination exactly as it was, and a failure between two renames puts
// back the root it was in the middle of. That is as atomic as a file system
// gives without a transaction, and it is the boundary §28.5 asks for.
func InstallOutputs(ctx context.Context, request InstallRequest) error {
	if err := os.RemoveAll(request.Staging); err != nil {
		return fmt.Errorf("execution: clearing the staging folder %s: %w", request.Staging, err)
	}
	defer func() {
		if err := os.RemoveAll(request.Staging); err != nil {
			request.Log.Warn().Err(err).Str("code", CodeTransportRetained).
				Str("category", CategoryTransportCleanup).Msg("a staging folder was not removed")
		}
	}()
	if err := stageOutputs(ctx, request); err != nil {
		return err
	}
	return replaceOutputRoots(request)
}

// stageOutputs writes every entry of the set into the staging folder, files
// first and links last.
//
// Links last because a link is the one entry that can make a path mean
// something other than itself: creating them after every file exists means no
// file was ever written through one.
func stageOutputs(ctx context.Context, request InstallRequest) error {
	if err := os.MkdirAll(request.Staging, 0o755); err != nil {
		return fmt.Errorf("execution: preparing the staging folder %s: %w", request.Staging, err)
	}
	if err := stageDeclaredRoots(request); err != nil {
		return err
	}
	objects, err := readOutputObjects(ctx, request)
	if err != nil {
		return err
	}
	reader, err := gitx.OpenObjectReader(ctx, request.Git)
	if err != nil {
		return fmt.Errorf("execution: reading the build outputs to install: %w", err)
	}
	defer func() { _ = reader.Close() }()
	for _, entry := range request.Manifest.Entries {
		if entry.Type == EntrySymlink {
			continue
		}
		if err := stageOneFile(request, reader, entry, objects[entry.Path]); err != nil {
			return err
		}
	}
	if err := reader.Close(); err != nil {
		return fmt.Errorf("execution: reading the build outputs to install: %w", err)
	}
	return stageSymlinks(request)
}

// readOutputObjects is which object each path of the set is, read off the tree
// rather than off the manifest.
//
// Off the tree on purpose. The tree is what validation compared the manifest
// against, so the bytes installed under a name are the bytes that name was
// checked for; a manifest naming its own object ids would be a manifest that
// decides what it is a description of. The listing streams and the map is
// bounded by the file ceiling the set has already been held to.
func readOutputObjects(ctx context.Context, request InstallRequest) (map[string]string, error) {
	objects := make(map[string]string, len(request.Manifest.Entries))
	plumbing := gitx.NewPlumbing(request.Git)
	plumbing.ListTree(ctx, request.Manifest.OutputTree, func(entry gitx.TreeEntry) error {
		objects[entry.Name] = entry.OID
		return nil
	})
	if err := plumbing.Err(); err != nil {
		return nil, refuseOutputs(ReasonBytesMissing)
	}
	return objects, nil
}

// stageDeclaredRoots creates the folder of every declared root the entries do
// not name themselves.
//
// A build that produced an empty `dist` has a root with no entry under it, and
// the transfer still has to replace whatever the consumer had there: without
// this, an empty output set would leave a stale folder in place and the
// consumer would build against the previous run's bytes.
func stageDeclaredRoots(request InstallRequest) error {
	named := map[string]bool{}
	for _, entry := range request.Manifest.Entries {
		named[entry.Path] = true
	}
	for _, root := range request.Manifest.Roots {
		if named[root] {
			continue
		}
		if err := os.MkdirAll(filepath.Join(request.Staging, filepath.FromSlash(root)), 0o755); err != nil {
			return fmt.Errorf("execution: preparing the staged root %s: %w", root, err)
		}
	}
	return nil
}

// stageOneFile streams one blob onto disk, hashing as it goes, and refuses the
// whole set the moment what arrived is not what the manifest promised.
func stageOneFile(request InstallRequest, reader *gitx.ObjectReader, entry ManifestEntry, oid string) error {
	if oid == "" {
		return refuseOutputs(ReasonBytesMissing)
	}
	path, err := resolveStagedPath(request.Staging, entry.Path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("execution: preparing the folder of a staged build output: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, resolveEntryMode(entry.Mode))
	if err != nil {
		return fmt.Errorf("execution: writing a staged build output: %w", err)
	}
	hash := sha256.New()
	_, readErr := reader.ReadBlob(oid, io.MultiWriter(file, hash), entry.Size)
	if err := file.Close(); err != nil {
		return fmt.Errorf("execution: closing a staged build output: %w", err)
	}
	if readErr != nil {
		return describeReadFailure(readErr)
	}
	if hex.EncodeToString(hash.Sum(nil)) != entry.SHA256 {
		return refuseOutputs(ReasonContentDigest)
	}
	return nil
}

// stageSymlinks creates the set's links, after every file of it exists.
func stageSymlinks(request InstallRequest) error {
	for _, entry := range request.Manifest.Entries {
		if entry.Type != EntrySymlink {
			continue
		}
		path, err := resolveStagedPath(request.Staging, entry.Path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("execution: preparing the folder of a staged build output: %w", err)
		}
		if err := os.Symlink(filepath.FromSlash(entry.Target), path); err != nil {
			return fmt.Errorf("execution: writing a staged build output link: %w", err)
		}
	}
	return nil
}

// resolveStagedPath is where one manifest path lands inside the staging
// folder, proving on the way that nothing between the two is a link.
//
// The proof is the point. Every folder under the staging root is created by
// this file and every path was held to the rules before it got here, so the
// walk should find directories and nothing else; a component that is a link,
// or a file where a folder has to be, is a set that would write outside the
// folder it is being assembled in, and it is refused rather than followed.
func resolveStagedPath(staging, carried string) (string, error) {
	walked := staging
	components := strings.Split(carried, "/")
	for _, component := range components[:len(components)-1] {
		walked = filepath.Join(walked, component)
		info, err := os.Lstat(walked)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("execution: inspecting a staged folder: %w", err)
		}
		if !info.IsDir() {
			return "", refuseOutputs(ReasonStagedComponent)
		}
	}
	return filepath.Join(walked, components[len(components)-1]), nil
}

// resolveEntryMode is the file mode one entry is written with. The manifest
// carries the two modes an output may have and nothing else, so the executable
// bit is the whole decision.
func resolveEntryMode(mode string) os.FileMode {
	if mode == EntryModeExecutable {
		return 0o755
	}
	return 0o644
}

// describeReadFailure turns a failed object read into the refusal it is: bytes
// that are not there, or a length that is not what was promised. Both fail the
// prerequisite rather than the process.
func describeReadFailure(err error) error {
	if errors.Is(err, gitx.ErrObjectMissing) {
		return refuseOutputs(ReasonBytesMissing)
	}
	if errors.Is(err, gitx.ErrTransportLimit) {
		return refuseOutputs(ReasonTreeSize)
	}
	return fmt.Errorf("execution: reading a build output to install: %w", err)
}

// replaceOutputRoots moves each staged root into place, putting back what was
// there if the move fails.
//
// The old folder is renamed aside rather than deleted first, because deleting
// it first would leave a consumer with nothing at all should the second rename
// fail; and it is removed after the new one is in place rather than left, so
// the checkout a build sees holds one copy of its inputs.
func replaceOutputRoots(request InstallRequest) error {
	for _, root := range request.Manifest.Roots {
		if err := replaceOneRoot(request, root); err != nil {
			return err
		}
	}
	request.Log.Debug().Str("package", request.Manifest.Package).
		Int("files", request.Manifest.Files).Int64("bytes", request.Manifest.Bytes).
		Int("roots", len(request.Manifest.Roots)).Msg("outputs installed")
	return nil
}

// replaceOneRoot puts one staged root where the consuming package reads it.
func replaceOneRoot(request InstallRequest, root string) error {
	staged := filepath.Join(request.Staging, filepath.FromSlash(root))
	destination := filepath.Join(request.Dir, filepath.FromSlash(root))
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return fmt.Errorf("execution: preparing the folder of the build output root %s: %w", root, err)
	}
	aside, err := moveRootAside(destination)
	if err != nil {
		return err
	}
	if err := os.Rename(staged, destination); err != nil {
		restoreRootAside(request, destination, aside)
		return fmt.Errorf("execution: installing the build output root %s: %w", root, err)
	}
	if aside == "" {
		return nil
	}
	if err := os.RemoveAll(aside); err != nil {
		request.Log.Warn().Err(err).Str("code", CodeTransportRetained).
			Str("category", CategoryTransportCleanup).Msg("a replaced build output root was not removed")
	}
	return nil
}

// moveRootAside renames whatever is at the destination out of the way and
// answers where it went, or the empty string when there was nothing there.
func moveRootAside(destination string) (string, error) {
	if _, err := os.Lstat(destination); os.IsNotExist(err) {
		return "", nil
	} else if err != nil {
		return "", fmt.Errorf("execution: inspecting the build output root %s: %w", destination, err)
	}
	aside := destination + asidePrefix + formatRandomHex(8)
	if err := os.Rename(destination, aside); err != nil {
		return "", fmt.Errorf("execution: moving the previous build output root aside: %w", err)
	}
	return aside, nil
}

// restoreRootAside puts back the folder that was moved out of the way, so that
// a failed install leaves the destination as it found it.
func restoreRootAside(request InstallRequest, destination, aside string) {
	if aside == "" {
		return
	}
	if err := os.Rename(aside, destination); err != nil {
		request.Log.Warn().Err(err).Str("code", CodeTransportRetained).
			Str("category", CategoryTransportCleanup).
			Msg("the previous build output root was not put back")
	}
}
