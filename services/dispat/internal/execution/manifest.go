// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// Turning a build's declared output folders into something another machine
// may use (CCME §28.5).
//
// Source synchronization alone is insufficient: a provider's revision says
// nothing about the `dist` folder its build wrote, because that folder is
// ignored by Git and therefore absent from every checkout of it. So a build
// that declares outputs has them captured into a tree of their own, described
// by a manifest bound to the run that produced them, and the consuming node
// verifies that description before a single command of its own starts.
//
// Three operations, one vocabulary. Capture runs on the node that built;
// validation runs on the orchestrator that admits and again on the node that
// consumes, and it is deliberately the same function, because two readers
// disagreeing about what a valid output set is would be a way to get bytes
// past one of them. Installation is in install.go.
//
// Everything streams. A build output is the one thing in a release that does
// not fit in memory, so a tree is listed entry by entry and every blob is
// copied through a fixed buffer into a hash or onto a disk, never into a
// string.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	lib "github.com/yohimik/dispat/pkg/config"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// OutputReason is why an output set was refused, as a stable word.
//
// It is an enum for the reason every other refusal in this profile is: what
// travels through a mailbox is unauthenticated until it is not, so a log line
// about a refused output set says which rule was broken and never which path
// broke it. An operator filtering for `link-escape` is looking at something
// quite different from one filtering for `too-many-bytes`, and neither should
// have to read prose to tell them apart.
type OutputReason string

// The rules an output set is held to, one word each. They are grouped the way
// they are checked: what the tree may hold, what a path may be, what a link
// may point at, what the two descriptions must agree on, and what the set as a
// whole has to be for this consumer.
const (
	// ReasonRootAbsent is a declared output root the build did not produce. It
	// fails the task: a build that did not write what its configuration
	// promises cannot satisfy a consumer, and a silently empty transfer is
	// exactly the substitution §28.5 forbids.
	ReasonRootAbsent OutputReason = "root-absent"
	// ReasonGitlink is a submodule entry. A gitlink names a commit of another
	// repository, so installing one would mean fetching something nobody
	// declared.
	ReasonGitlink OutputReason = "gitlink"
	// ReasonEntryType is a tree entry that is neither a file nor a symlink.
	ReasonEntryType OutputReason = "entry-type"
	// ReasonDuplicatePath is two entries whose paths are one path after case
	// folding. They are distinct on the machine that built them and one file
	// on the machine that installs them, which is a set whose content depends
	// on where it lands.
	ReasonDuplicatePath OutputReason = "duplicate-path"
	// ReasonTooManyFiles and ReasonTooManyBytes are the configured transfer
	// ceilings, applied while the tree is walked so that an oversized set is
	// refused before it is described rather than after it is moved.
	ReasonTooManyFiles OutputReason = "too-many-files"
	ReasonTooManyBytes OutputReason = "too-many-bytes"
	// ReasonManifestOversize is a manifest larger than the ceiling the run
	// holds it to, which is what bounds the memory a consumer spends on a
	// description it has not verified yet.
	ReasonManifestOversize OutputReason = "manifest-oversize"
	// ReasonTransferRefused is a result the mailbox would not take with its
	// outputs: a server rule, a hook or a size limit refused the push. The
	// node reports the failure without them, and names the server's reason,
	// redacted, in its own log.
	ReasonTransferRefused OutputReason = "transfer-refused"

	// The path rules. A manifest path is a relative, slash-separated path
	// under one of the declared roots and nothing else, because it becomes a
	// file name on a machine whose rules about names are not this one's.
	ReasonPathAbsolute    OutputReason = "path-absolute"
	ReasonPathEscape      OutputReason = "path-escape"
	ReasonPathComponent   OutputReason = "path-component"
	ReasonPathGitFolder   OutputReason = "path-git-folder"
	ReasonPathBackslash   OutputReason = "path-backslash"
	ReasonPathColon       OutputReason = "path-colon"
	ReasonPathNul         OutputReason = "path-nul"
	ReasonPathOutsideRoot OutputReason = "path-outside-root"
	ReasonPathUnsorted    OutputReason = "path-unsorted"
	ReasonLinkAbsolute    OutputReason = "link-absolute"
	ReasonLinkEscape      OutputReason = "link-escape"
	ReasonLinkTargetEmpty OutputReason = "link-target-empty"
	ReasonMode            OutputReason = "mode"
	ReasonStagedComponent OutputReason = "staged-component"

	// The agreement rules: the manifest and the tree it names describe one set
	// of files or the set is refused. A file in one and not the other is the
	// whole of what a forged description would be.
	ReasonTreeMissing OutputReason = "tree-missing"
	ReasonTreeExtra   OutputReason = "tree-extra"
	ReasonTreeType    OutputReason = "tree-type"
	ReasonTreeMode    OutputReason = "tree-mode"
	ReasonTreeSize    OutputReason = "tree-size"
	ReasonTreeTotals  OutputReason = "tree-totals"

	// The set rules: what this output set has to be for this consumer, now.
	ReasonOutputProtocol OutputReason = "output-protocol"
	ReasonOutputIdentity OutputReason = "output-identity"
	ReasonOutputDigest   OutputReason = "manifest-digest"
	ReasonOutputPlatform OutputReason = "platform"
	ReasonContentDigest  OutputReason = "content-digest"
	// ReasonBytesMissing is a reference whose bytes are not retrievable: a
	// tree nothing fetched, a blob the store does not hold. References alone
	// are not a transfer (§28.5).
	ReasonBytesMissing OutputReason = "bytes-missing"
	// ReasonAuthorization is a publication whose command never started because
	// the run never authorized it, or stopped authorizing it before it began.
	// It is in this vocabulary rather than beside it because it answers the
	// same question every word here answers: what stopped this task that was
	// not a command exiting non-zero.
	ReasonAuthorization OutputReason = "authorization"
)

// OutputFault is one refused output set, carrying the rule it broke and
// nothing else.
//
// Nothing else on purpose. The manifest a fault is about may have been written
// by anybody able to push to a mailbox, so naming the offending path in the
// error would put that path in a log, in a summary and in a CI annotation. The
// reason is what a reader has to act on and it is the whole of what travels.
type OutputFault struct {
	Reason OutputReason
}

// Error is the sentence a reader is owed: what happened, and which rule.
func (f *OutputFault) Error() string {
	return fmt.Sprintf("execution: the build output set was refused (%s)", f.Reason)
}

// refuseOutputs is the one place a fault is built, so that every refusal in
// this file is the same kind of error.
func refuseOutputs(reason OutputReason) error { return &OutputFault{Reason: reason} }

// OutputFaultReason is the rule err names, and the empty string for an error
// that is not an output refusal at all. A transport failure is deliberately
// not a fault: "the bytes could not be read" and "the bytes are not what they
// claim" are both E227 here, but only the second is a statement about the set.
func OutputFaultReason(err error) OutputReason {
	var fault *OutputFault
	if !errors.As(err, &fault) {
		return ""
	}
	return fault.Reason
}

// maxLinkTargetBytes bounds one symlink target. A path is a few hundred bytes
// on every system that has a limit at all, so a blob claiming to be a link and
// being something else is refused at the read rather than after it.
const maxLinkTargetBytes = 8192

// OutputTotals is what an output set weighs: the numbers the ceilings are
// applied to and the numbers a log line reports.
type OutputTotals struct {
	Files int
	Bytes int64
}

// CaptureRequest is one capture: the repository the outputs sit in, the
// package folder they are relative to, what was declared, and what the run
// holds the transfer to.
type CaptureRequest struct {
	// Git is the repository whose working tree holds the built package. The
	// tree is written into its object store, which is also the store the
	// result is pushed out of.
	Git *gitx.LocalGitx
	// Dir is the package folder, absolute, as the capturing node sees it.
	Dir string
	// PackagePath is Dir relative to the repository root, slash-separated, and
	// empty for a package at the root. It is what the captured tree is
	// narrowed by, so that a manifest names files the way a package declares
	// them.
	PackagePath string
	// Roots are the declared build output roots, relative to Dir.
	Roots []string
	// Limits are the ceilings the set is held to.
	Limits TransferLimits
	// Manifest is the binding the capture fills the entries into: the run, the
	// task, the package, the states consumed and the platform. Everything but
	// the entries, the totals, the tree and the digest is the caller's.
	Manifest OutputManifest
	// IsAbsentRootEmpty admits a declared root the task did not write as an
	// empty one instead of refusing it. It is a sweep's rule and never a
	// build's (§28.10): a build that did not produce what its consumers were
	// promised has failed them, while a sweep's script may have nothing to
	// write for a package, and a package with nothing to say is legitimate.
	IsAbsentRootEmpty bool
}

// ReasonPathConflict is two tasks of one sweep writing one path under a
// declared sweep root with different bytes. Neither file is installed,
// because the root would otherwise hold whichever task finished last
// (§28.10).
const ReasonPathConflict OutputReason = "path-conflict"

// ReasonDestinationComponent is a file of a merged set whose destination
// cannot be written as a file: a folder already sits at its path, or a folder
// on the way to it is a link or a file. A merge writes into a checkout it
// does not replace, so a path that would write through a link is refused
// rather than followed.
const ReasonDestinationComponent OutputReason = "destination-component"

// CaptureOutputs turns one package's declared output roots into a tree and the
// manifest that describes it.
//
// The order is the order of refusal. A root the build did not write fails
// before anything is hashed; a tree holding something that is not a file or a
// link fails before anything is read; the ceilings fail while the tree is
// walked. Only what survives all three is digested, and only then is there a
// manifest at all, so nothing is ever committed describing a set that was
// already refused.
func CaptureOutputs(ctx context.Context, request CaptureRequest) (*OutputManifest, error) {
	roots, err := resolveDeclaredRoots(request)
	if err != nil {
		return nil, err
	}
	tree, err := writeOutputTree(ctx, request, roots.present)
	if err != nil {
		return nil, err
	}
	captured, totals, err := readOutputTree(ctx, request.Git, tree, roots.declared, request.Limits)
	if err != nil {
		return nil, err
	}
	entries, err := digestCapturedEntries(ctx, request.Git, captured, request.Limits)
	if err != nil {
		return nil, err
	}
	manifest := request.Manifest
	manifest.Protocol = ProtocolVersion
	manifest.Roots = roots.declared
	manifest.Entries = entries
	manifest.Files, manifest.Bytes = totals.Files, totals.Bytes
	manifest.OutputTree = tree
	manifest.Digest = CalculateManifestDigest(&manifest)
	return &manifest, nil
}

// declaredRoots are one capture's roots: every declared root in the shape a
// manifest path is compared against, and the ones the task actually wrote.
type declaredRoots struct {
	declared []string
	present  []string
}

// resolveDeclaredRoots holds every declared root to the shape a manifest path
// is compared against, and refuses one the build did not produce unless the
// capture admits an absent root as empty.
//
// The declaration is the configuration's, so `dist/` and `dist` are one root
// and both are written as one here. The existence check is Lstat rather than
// Stat because a root that is a dangling symlink is still a root somebody
// created, and what it is is decided by the tree that comes out, not here.
func resolveDeclaredRoots(request CaptureRequest) (declaredRoots, error) {
	roots := declaredRoots{declared: make([]string, 0, len(request.Roots))}
	for _, root := range request.Roots {
		clean := path.Clean(strings.TrimSuffix(filepath.ToSlash(root), "/"))
		roots.declared = append(roots.declared, clean)
		if _, err := os.Lstat(filepath.Join(request.Dir, filepath.FromSlash(clean))); err != nil {
			if request.IsAbsentRootEmpty {
				continue
			}
			return declaredRoots{}, refuseOutputs(ReasonRootAbsent)
		}
		roots.present = append(roots.present, clean)
	}
	return roots, nil
}

// writeOutputTree stages the declared roots through an index of its own and
// answers the tree of the package folder alone.
//
// Forced, because a build output is ignored by construction and an unforced
// add would stage nothing at all; literal, because a folder whose name holds a
// glob character is the folder it is named; and narrowed to the package
// folder, because an index is rooted at the repository and a manifest is not.
func writeOutputTree(ctx context.Context, request CaptureRequest, present []string) (string, error) {
	if len(present) == 0 {
		// Nothing was written under any declared root, which only a capture
		// that admits absent roots reaches: the set is the empty tree, and
		// staging no path at all is not something git is asked to do.
		plumbing := gitx.NewPlumbing(request.Git)
		empty := plumbing.MakeTree(ctx, nil)
		if err := plumbing.Err(); err != nil {
			return "", fmt.Errorf("execution: capturing the outputs of %s: %w", request.Dir, err)
		}
		return empty, nil
	}
	// A folder rather than a file: git reads an index that is not there as an
	// empty one and refuses an empty file as a truncated index, so the only
	// way to start from nothing is to name a path that does not exist yet.
	folder, err := os.MkdirTemp("", "dispat-outputs-index-")
	if err != nil {
		return "", fmt.Errorf("execution: preparing a temporary index for the build outputs: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(folder); err != nil {
			request.Git.Log.Warn().Err(err).Str("code", CodeTransportRetained).
				Str("category", CategoryTransportCleanup).Msg("a temporary index was not removed")
		}
	}()
	plumbing := gitx.NewPlumbing(request.Git)
	repositoryTree := plumbing.WriteTreeFromPaths(ctx, request.Dir,
		filepath.Join(folder, "index"), present, true)
	// A build whose declared roots are all empty folders stages nothing, and
	// git has no path to resolve inside a tree with no entries. That is a real
	// output set, so the empty tree is what describes it, and it is asked for
	// rather than written down because its name depends on the repository's
	// hash algorithm.
	empty := plumbing.MakeTree(ctx, nil)
	tree := empty
	if repositoryTree != empty {
		tree = plumbing.ResolveSubtree(ctx, repositoryTree, request.PackagePath)
	}
	if err := plumbing.Err(); err != nil {
		return "", fmt.Errorf("execution: capturing the build outputs of %s: %w", request.Dir, err)
	}
	return tree, nil
}

// readOutputTree walks the captured tree as a stream and answers the entries
// it holds, refusing at the first thing an output set may not contain.
//
// Nothing accumulates but the entries themselves, and they are bounded by the
// file ceiling, which is checked as the walk goes rather than at the end: a
// tree with a million files is refused after the first ceiling-breaking entry
// rather than after the millionth.
func readOutputTree(ctx context.Context, git *gitx.LocalGitx, tree string, roots []string,
	limits TransferLimits) ([]capturedEntry, OutputTotals, error) {
	var entries []capturedEntry
	var totals OutputTotals
	folded := map[string]bool{}
	plumbing := gitx.NewPlumbing(git)
	plumbing.ListTree(ctx, tree, func(entry gitx.TreeEntry) error {
		described, err := describeTreeEntry(entry)
		if err != nil {
			return err
		}
		if err := checkManifestPath(described.Path, roots); err != nil {
			return err
		}
		if folded[lib.Fold(described.Path)] {
			return refuseOutputs(ReasonDuplicatePath)
		}
		folded[lib.Fold(described.Path)] = true
		totals.Files++
		totals.Bytes += described.Size
		if err := checkOutputTotals(totals, limits); err != nil {
			return err
		}
		entries = append(entries, capturedEntry{entry: described, oid: entry.OID})
		return nil
	})
	if err := plumbing.Err(); err != nil {
		if OutputFaultReason(err) != "" {
			return nil, OutputTotals{}, err
		}
		return nil, OutputTotals{}, fmt.Errorf("execution: reading the captured build outputs: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].entry.Path < entries[j].entry.Path })
	return entries, totals, nil
}

// capturedEntry is one entry on its way into a manifest: what it will be
// described as, and the object its bytes are still only reachable by.
//
// The two are kept apart because they are two different names for the same
// file, and only one of them travels: git's object id is a name this
// repository gave the bytes, while the manifest's digest is a name the
// consumer can recompute from what it wrote to disk.
type capturedEntry struct {
	entry ManifestEntry
	oid   string
}

// describeTreeEntry turns one tree entry into a manifest entry, refusing
// everything an output set may not hold.
func describeTreeEntry(entry gitx.TreeEntry) (ManifestEntry, error) {
	switch entry.Mode {
	case gitx.TreeModeFile:
		return ManifestEntry{Path: entry.Name, Type: EntryFile, Mode: EntryModeFile, Size: entry.Size}, nil
	case treeModeExecutable:
		return ManifestEntry{Path: entry.Name, Type: EntryFile, Mode: EntryModeExecutable, Size: entry.Size}, nil
	case treeModeSymlink:
		return ManifestEntry{Path: entry.Name, Type: EntrySymlink, Mode: EntryModeFile, Size: entry.Size}, nil
	case treeModeGitlink:
		return ManifestEntry{}, refuseOutputs(ReasonGitlink)
	default:
		return ManifestEntry{}, refuseOutputs(ReasonEntryType)
	}
}

// The tree modes a captured output set is read through. Git's own spelling,
// stated here rather than matched by prefix so that a mode nobody expected is
// refused instead of being read as the closest thing to it.
const (
	treeModeExecutable = "100755"
	treeModeSymlink    = "120000"
	treeModeGitlink    = "160000"
)

// checkOutputTotals applies the two ceilings to what has been counted so far.
func checkOutputTotals(totals OutputTotals, limits TransferLimits) error {
	if limits.MaxFiles > 0 && totals.Files > limits.MaxFiles {
		return refuseOutputs(ReasonTooManyFiles)
	}
	if limits.MaxBytes > 0 && totals.Bytes > limits.MaxBytes {
		return refuseOutputs(ReasonTooManyBytes)
	}
	return nil
}

// digestCapturedEntries gives every entry the digest of what it holds, through
// one batch reader, and answers the manifest entries that result.
//
// One reader rather than two invocations per file: a `dist` folder is tens of
// thousands of files, and the cost of describing it must not be tens of
// thousands of processes. A symlink's digest is over its target, which is also
// what its blob holds, so both kinds travel through one loop; the target is
// the only content this file ever keeps, and it is bounded before it is read.
func digestCapturedEntries(ctx context.Context, git *gitx.LocalGitx, captured []capturedEntry,
	limits TransferLimits) ([]ManifestEntry, error) {
	entries := make([]ManifestEntry, 0, len(captured))
	if len(captured) == 0 {
		return entries, nil
	}
	reader, err := gitx.OpenObjectReader(ctx, git)
	if err != nil {
		return nil, fmt.Errorf("execution: reading the captured build outputs: %w", err)
	}
	defer func() { _ = reader.Close() }()
	for _, one := range captured {
		hash := sha256.New()
		ceiling := limits.MaxBytes
		sink := io.Writer(hash)
		var target strings.Builder
		if one.entry.Type == EntrySymlink {
			ceiling, sink = maxLinkTargetBytes, io.MultiWriter(hash, &target)
		}
		if _, err := reader.ReadBlob(one.oid, sink, ceiling); err != nil {
			if errors.Is(err, gitx.ErrTransportLimit) {
				return nil, refuseOutputs(ReasonTooManyBytes)
			}
			return nil, fmt.Errorf("execution: digesting a captured build output: %w", err)
		}
		one.entry.SHA256, one.entry.Target = hex.EncodeToString(hash.Sum(nil)), target.String()
		entries = append(entries, one.entry)
	}
	if err := reader.Close(); err != nil {
		return nil, fmt.Errorf("execution: digesting the captured build outputs: %w", err)
	}
	return entries, nil
}

// OutputExpectation is what a reader requires of an output set before it uses
// it: whose work it must be, what it must lie under, where it may have been
// built and, when the caller already knows it, which manifest it must be.
//
// Every field is checked only when it is stated, because the two readers know
// different things. The orchestrator admitting a result knows the whole
// identity of the attempt it offered; a consuming node knows the run, the plan
// and the ownership it was assigned under, and knows the provider's manifest
// by the digest the orchestrator put in its assignment, which binds everything
// else anyway.
type OutputExpectation struct {
	Run        string
	PlanDigest string
	Generation string
	Task       string
	Attempt    int
	// Roots are the producer's declared output roots. They are the reader's
	// own copy of the configuration rather than the manifest's, which is the
	// point: a manifest that widened its own roots would otherwise be a
	// manifest that may write anywhere.
	Roots []string
	// Platforms are the platforms the CONSUMER's package may build on, empty
	// for any. A native output set built somewhere else is refused here rather
	// than discovered as a binary that will not load.
	Platforms []string
	// Digest is the manifest the reader was promised, empty when it has not
	// been told one.
	Digest string
	// Limits are the ceilings this reader holds the set to.
	Limits TransferLimits
}

// ValidateOutputs holds one manifest, and the tree it names, to everything a
// consumer needs before its stage becomes ready (§28.5).
//
// It is one function used by the orchestrator at admission and by every
// consuming node, and that is deliberate: two implementations of "is this
// output set usable" would differ somewhere, and the difference would be a way
// of getting bytes past one of them. What differs between the callers is the
// expectation they pass, never the rules.
//
// The tree is walked after the manifest is checked, because walking it is the
// expensive half and a manifest that fails on its own says nothing about what
// is in the store.
func ValidateOutputs(ctx context.Context, git *gitx.LocalGitx, manifest *OutputManifest,
	expect OutputExpectation) (OutputTotals, error) {
	if err := checkManifestHeader(manifest, expect); err != nil {
		return OutputTotals{}, err
	}
	totals, err := checkManifestEntries(manifest, expect)
	if err != nil {
		return OutputTotals{}, err
	}
	if err := checkOutputTree(ctx, git, manifest); err != nil {
		return OutputTotals{}, err
	}
	return totals, nil
}

// checkManifestHeader asks whether this output set is this reader's at all:
// the protocol it speaks, the work it belongs to, the platform it was built
// on, and the name it was promised under.
func checkManifestHeader(manifest *OutputManifest, expect OutputExpectation) error {
	if manifest.Protocol != ProtocolVersion {
		return refuseOutputs(ReasonOutputProtocol)
	}
	if !areIdentitiesExpected(manifest, expect) {
		return refuseOutputs(ReasonOutputIdentity)
	}
	if expect.Digest != "" && manifest.Digest != expect.Digest {
		return refuseOutputs(ReasonOutputDigest)
	}
	if manifest.Digest != CalculateManifestDigest(manifest) {
		return refuseOutputs(ReasonOutputDigest)
	}
	if !isPlatformAcceptable(manifest.Platform, expect.Platforms) {
		return refuseOutputs(ReasonOutputPlatform)
	}
	return nil
}

// areIdentitiesExpected reports whether the manifest names the work the reader
// asked about. A field the reader did not state is not compared, because a
// consumer knows the run and not the attempt of the provider that filled it.
func areIdentitiesExpected(manifest *OutputManifest, expect OutputExpectation) bool {
	for _, stated := range []struct{ wanted, carried string }{
		{expect.Run, manifest.Run},
		{expect.PlanDigest, manifest.PlanDigest},
		{expect.Generation, manifest.Generation},
		{expect.Task, manifest.Task},
	} {
		if stated.wanted != "" && stated.wanted != stated.carried {
			return false
		}
	}
	return expect.Attempt == 0 || expect.Attempt == manifest.Attempt
}

// isPlatformAcceptable reports whether a set built on one platform may be
// installed by a package that declares these. An empty declaration is every
// platform, which is what a workspace that never says otherwise means.
func isPlatformAcceptable(built Platform, platforms []string) bool {
	if len(platforms) == 0 {
		return true
	}
	own := built.OS + "/" + built.Arch
	for _, platform := range platforms {
		if platform == own {
			return true
		}
	}
	return false
}

// checkManifestEntries holds every entry to the path, link, mode and ordering
// rules, and the set as a whole to the ceilings and to its own totals.
func checkManifestEntries(manifest *OutputManifest, expect OutputExpectation) (OutputTotals, error) {
	var totals OutputTotals
	folded := map[string]bool{}
	previous := ""
	for _, entry := range manifest.Entries {
		if err := checkManifestEntry(entry, expect.Roots); err != nil {
			return OutputTotals{}, err
		}
		if entry.Path <= previous && previous != "" {
			return OutputTotals{}, refuseOutputs(ReasonPathUnsorted)
		}
		previous = entry.Path
		if folded[lib.Fold(entry.Path)] {
			return OutputTotals{}, refuseOutputs(ReasonDuplicatePath)
		}
		folded[lib.Fold(entry.Path)] = true
		totals.Files++
		totals.Bytes += entry.Size
		if err := checkOutputTotals(totals, expect.Limits); err != nil {
			return OutputTotals{}, err
		}
	}
	if totals.Files != manifest.Files || totals.Bytes != manifest.Bytes {
		return OutputTotals{}, refuseOutputs(ReasonTreeTotals)
	}
	return totals, nil
}

// checkManifestEntry is one entry's own rules: what it is, where it says it
// is, and where a link says it points.
func checkManifestEntry(entry ManifestEntry, roots []string) error {
	if err := checkManifestPath(entry.Path, roots); err != nil {
		return err
	}
	if entry.Type != EntryFile && entry.Type != EntrySymlink {
		return refuseOutputs(ReasonEntryType)
	}
	if entry.Mode != EntryModeFile && entry.Mode != EntryModeExecutable {
		return refuseOutputs(ReasonMode)
	}
	if entry.Type == EntrySymlink && entry.Mode != EntryModeFile {
		return refuseOutputs(ReasonMode)
	}
	if entry.Size < 0 {
		return refuseOutputs(ReasonTreeSize)
	}
	if entry.Type != EntrySymlink {
		return nil
	}
	return checkLinkTarget(entry.Path, entry.Target, roots)
}

// checkManifestPath holds one path to everything it has to be before it
// becomes a file name on another machine.
//
// The rules read as a list because they are a list: each of them is a way a
// path that looks harmless reaches somewhere it should not, and every one of
// them has its own word so a refusal says which.
func checkManifestPath(carried string, roots []string) error {
	if strings.ContainsRune(carried, 0) {
		return refuseOutputs(ReasonPathNul)
	}
	if strings.Contains(carried, `\`) {
		return refuseOutputs(ReasonPathBackslash)
	}
	if strings.Contains(carried, ":") {
		return refuseOutputs(ReasonPathColon)
	}
	if carried == "" || strings.HasPrefix(carried, "/") || filepath.IsAbs(filepath.FromSlash(carried)) {
		return refuseOutputs(ReasonPathAbsolute)
	}
	for _, component := range strings.Split(carried, "/") {
		if component == ".." {
			return refuseOutputs(ReasonPathEscape)
		}
		if component == "" || component == "." {
			return refuseOutputs(ReasonPathComponent)
		}
		if strings.EqualFold(component, gitFolderName) {
			return refuseOutputs(ReasonPathGitFolder)
		}
	}
	if resolveOwningRoot(carried, roots) == "" {
		return refuseOutputs(ReasonPathOutsideRoot)
	}
	return nil
}

// gitFolderName is the one folder name an output path may never hold. It is
// refused as a component rather than as a prefix, because a build that writes
// into repository metadata rewrites history on whichever machine installs it.
const gitFolderName = ".git"

// resolveOwningRoot is the declared root one path lies under, and the empty
// string for a path that lies under none.
//
// The comparison is by component, so `dist-old/x` is not a file inside `dist`,
// and a root cannot be a file of itself: a root travels as a folder.
func resolveOwningRoot(carried string, roots []string) string {
	for _, root := range roots {
		if carried == root || strings.HasPrefix(carried, root+"/") {
			return root
		}
	}
	return ""
}

// checkLinkTarget holds one symlink to the rule that makes a link portable: it
// points at something relative, and what it points at is still inside the root
// it travelled in.
//
// The resolution is lexical rather than through the file system, on purpose.
// A link is checked on the machine that captured it and again on the machine
// that will install it, and the second one has not created the files yet, so
// the only thing both can agree about is what the path says.
func checkLinkTarget(carried, target string, roots []string) error {
	if target == "" {
		return refuseOutputs(ReasonLinkTargetEmpty)
	}
	if strings.ContainsRune(target, 0) {
		return refuseOutputs(ReasonPathNul)
	}
	if strings.Contains(target, `\`) {
		return refuseOutputs(ReasonPathBackslash)
	}
	if strings.HasPrefix(target, "/") || filepath.IsAbs(filepath.FromSlash(target)) {
		return refuseOutputs(ReasonLinkAbsolute)
	}
	if isClimbingAfterDescending(target) {
		return refuseOutputs(ReasonLinkEscape)
	}
	root := resolveOwningRoot(carried, roots)
	resolved := path.Join(path.Dir(carried), target)
	if resolved != root && !strings.HasPrefix(resolved, root+"/") {
		return refuseOutputs(ReasonLinkEscape)
	}
	return nil
}

// isClimbingAfterDescending reports a target that steps up (`..`) after it has
// stepped into a name, such as `sub/../x`.
//
// The lexical check below is only as good as the assumption that `name/..` is
// where it started, and a link breaks that assumption: when `sub` is itself a
// link to a folder higher up, `sub/../x` reads as `x` beside the link and
// resolves somewhere above it, outside the root the set travelled in. Steps up
// at the front of a target are different, because they are taken from the
// link's own folder, and every folder of a staged set is a real one. So a
// target may climb first and descend after, which is what every tool that
// writes relative links produces, and may not do it the other way round.
func isClimbingAfterDescending(target string) bool {
	hasDescended := false
	for _, component := range strings.Split(target, "/") {
		if component == ".." && hasDescended {
			return true
		}
		if component != ".." && component != "." && component != "" {
			hasDescended = true
		}
	}
	return false
}

// checkOutputTree walks the tree the manifest names and requires the two to
// describe one set of files exactly.
//
// Exactly, in both directions: a manifest naming a file the tree lacks is a
// description of bytes nobody can retrieve, and a tree holding a file the
// manifest does not name is a file that would be installed without ever having
// been checked. The walk streams, so the comparison costs one map of the
// manifest rather than a second listing in memory.
func checkOutputTree(ctx context.Context, git *gitx.LocalGitx, manifest *OutputManifest) error {
	wanted := make(map[string]ManifestEntry, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		wanted[entry.Path] = entry
	}
	seen := 0
	plumbing := gitx.NewPlumbing(git)
	plumbing.ListTree(ctx, manifest.OutputTree, func(entry gitx.TreeEntry) error {
		// What the entry is comes before whether it was expected: a submodule
		// or a device node is refused for being one, whatever a manifest that
		// named it would have called it.
		described, err := describeTreeEntry(entry)
		if err != nil {
			return err
		}
		carried, isWanted := wanted[entry.Name]
		if !isWanted {
			return refuseOutputs(ReasonTreeExtra)
		}
		seen++
		return checkTreeAgreement(carried, described)
	})
	// A tree that could not be listed at all is a reference whose bytes never
	// travelled, which is the same refusal as a missing blob: a digest or a
	// branch name without retrievable content is not a transfer (§28.5).
	if err := plumbing.Err(); err != nil {
		if OutputFaultReason(err) != "" {
			return err
		}
		return refuseOutputs(ReasonBytesMissing)
	}
	if seen != len(manifest.Entries) {
		return refuseOutputs(ReasonTreeMissing)
	}
	return nil
}

// checkTreeAgreement requires one tree entry to be what the manifest said it
// was: the same kind of thing, the same mode and the same length.
func checkTreeAgreement(carried, expected ManifestEntry) error {
	if expected.Type != carried.Type {
		return refuseOutputs(ReasonTreeType)
	}
	if expected.Mode != carried.Mode {
		return refuseOutputs(ReasonTreeMode)
	}
	if expected.Size != carried.Size {
		return refuseOutputs(ReasonTreeSize)
	}
	return nil
}

// CalculateManifestDigest names one manifest: the SHA-256 of the document with
// the digest field empty, as lowercase hex.
//
// It is over the marshalled document rather than over a hand-rolled
// concatenation so that every field of the manifest is covered by construction,
// including the ones a later gate adds. Both parties marshal the same struct,
// so the byte sequence is the same one on both machines.
func CalculateManifestDigest(manifest *OutputManifest) string {
	named := *manifest
	named.Digest = ""
	document, err := json.Marshal(&named)
	if err != nil {
		// A manifest holds strings, numbers and slices of both, so there is
		// nothing here encoding/json can refuse; an empty digest matches
		// nothing and refuses the set, which is the safe answer anyway.
		return ""
	}
	sum := sha256.Sum256(document)
	return hex.EncodeToString(sum[:])
}
