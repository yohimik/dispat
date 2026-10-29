// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

// ObjectType reads the type of an exact object, without following a ref.
func (c *LocalGitx) ObjectType(ctx context.Context, oid string) (string, error) {
	if !transportOID(oid) {
		return transportAbsentObjectText, fmt.Errorf(invalidTransportObjectIDErrorText)
	}
	out, err := c.runEnv(ctx, []string{transportDisableReplacementEnvironmentValue}, transportReadObjectCommandName, transportObjectTypeFlag, oid)
	if err != nil {
		return transportAbsentObjectText, fmt.Errorf(transportObjectTypeReadErrorFormat, oid, err)
	}
	kind := strings.TrimSpace(out)
	switch kind {
	case transportCommitObjectTypeName, transportTreeObjectTypeName, transportBlobObjectTypeName, transportTagObjectTypeName:
		return kind, nil
	default:
		return transportAbsentObjectText, fmt.Errorf(unsupportedTransportObjectTypeErrorFormat, kind)
	}
}

// TagTarget returns an annotated tag's immediate object, without peeling a
// nested tag. The signed tag inventory needs this exact intermediate link.
func (c *LocalGitx) TagTarget(ctx context.Context, tagOID string) (string, error) {
	kind, err := c.ObjectType(ctx, tagOID)
	if err != nil {
		return transportAbsentObjectText, err
	}
	if kind != transportTagObjectTypeName {
		return transportAbsentObjectText, fmt.Errorf(unannotatedTransportTagErrorFormat, tagOID)
	}
	header := &tagObjectHeader{}
	if _, err := c.runStream(ctx, gitStream{stdout: header, env: []string{transportDisableReplacementEnvironmentValue}},
		transportReadObjectCommandName, transportTagObjectTypeName, tagOID); err != nil {
		return transportAbsentObjectText, fmt.Errorf(transportTagTargetReadErrorFormat, tagOID, err)
	}
	line := string(header.line)
	if !strings.HasPrefix(line, transportTagTargetHeaderPrefix) || !transportOID(strings.TrimPrefix(line, transportTagTargetHeaderPrefix)) {
		return transportAbsentObjectText, fmt.Errorf(invalidTransportTagTargetErrorFormat, tagOID)
	}
	// A pack may carry the tag before its separately pinned commit target is
	// imported. This answers the encoded OID without claiming it is present.
	return strings.TrimPrefix(line, transportTagTargetHeaderPrefix), nil
}

type tagObjectHeader struct {
	line []byte
	done bool
}

func (h *tagObjectHeader) Write(p []byte) (int, error) {
	if h.done {
		return len(p), nil
	}
	for _, b := range p {
		if b == transportObjectHeaderLineDelimiterByte {
			h.done = tagObjectHeaderCompleteState
			break
		}
		if len(h.line) >= maximumTagObjectHeaderBytes {
			return transportNoObjectBytesCount, fmt.Errorf(transportTagHeaderLimitErrorFormat, ErrTransportLimit)
		}
		h.line = append(h.line, b)
	}
	return len(p), nil
}

// PeelObject follows annotated tags until the exact non-tag target is found.
func (c *LocalGitx) PeelObject(ctx context.Context, oid string) (string, error) {
	seen := make(map[string]struct{})
	for {
		if _, exists := seen[oid]; exists {
			return transportAbsentObjectText, fmt.Errorf(cyclicTransportTagChainErrorFormat, oid)
		}
		seen[oid] = struct{}{}
		kind, err := c.ObjectType(ctx, oid)
		if err != nil {
			return transportAbsentObjectText, err
		}
		if kind != transportTagObjectTypeName {
			return oid, nil
		}
		oid, err = c.TagTarget(ctx, oid)
		if err != nil {
			return transportAbsentObjectText, err
		}
	}
}

func exactClosureObjects(ctx context.Context, source *LocalGitx, oids []string, commitOnly bool) ([]string, error) {
	unique := make(map[string]struct{}, len(oids))
	for _, oid := range oids {
		kind, err := source.ObjectType(ctx, oid)
		if err != nil {
			return nil, err
		}
		if commitOnly && kind != transportCommitObjectTypeName {
			return nil, fmt.Errorf(nonCommitClosureExclusionErrorFormat, oid)
		}
		unique[oid] = struct{}{}
	}
	answer := make([]string, objectClosureInitialObjectCount, len(unique))
	for oid := range unique {
		answer = append(answer, oid)
	}
	sort.Strings(answer)
	return answer, nil
}

type ObjectClosurePackImportOptions struct {
	SourceDir         string
	IncludeOIDs       []string
	ExcludeCommitOIDs []string
	MaxBytes          int64
}

// ImportObjectClosurePackAsBlob carries exact object closures through a blob
// in this store. The source pack is bounded on disk and never buffered in RAM.
// Exclusions are complete commit targets already carried by the bootstrap.
func (c *LocalGitx) ImportObjectClosurePackAsBlob(ctx context.Context,
	options ObjectClosurePackImportOptions) (string, int64, error) {
	sourceRepoPath, include, exclude, maxBytes := options.SourceDir, options.IncludeOIDs,
		options.ExcludeCommitOIDs, options.MaxBytes
	if sourceRepoPath == objectClosureMissingSourcePath || maxBytes <= transportNoObjectBytesCount {
		return transportAbsentObjectText, transportNoObjectBytesCount, fmt.Errorf(invalidObjectClosureRequestErrorText)
	}
	source := &LocalGitx{Dir: sourceRepoPath, Log: c.Log}
	inc, err := exactClosureObjects(ctx, source, include, objectClosureIncludesRequireCommitState)
	if err != nil {
		return transportAbsentObjectText, transportNoObjectBytesCount, fmt.Errorf(invalidObjectClosureIncludeErrorFormat, err)
	}
	if len(inc) == objectClosureInitialObjectCount {
		return transportAbsentObjectText, transportNoObjectBytesCount, fmt.Errorf(missingObjectClosureIncludeErrorText)
	}
	exc, err := exactClosureObjects(ctx, source, exclude, objectClosureExclusionsRequireCommitState)
	if err != nil {
		return transportAbsentObjectText, transportNoObjectBytesCount, fmt.Errorf(invalidObjectClosureExcludeErrorFormat, err)
	}
	var revs strings.Builder
	for _, oid := range inc {
		revs.WriteString(oid + transportObjectLineSeparatorText)
	}
	for _, oid := range exc {
		revs.WriteString(objectClosureExclusionRevisionPrefix + oid + transportObjectLineSeparatorText)
	}
	tmp, err := os.CreateTemp(transportSystemTemporaryDirectoryPath, objectClosureStagingFilePattern)
	if err != nil {
		return transportAbsentObjectText, transportNoObjectBytesCount, fmt.Errorf(objectClosureStagingErrorFormat, err)
	}
	defer func() { _ = tmp.Close(); _ = os.Remove(tmp.Name()) }()
	bounded := &closurePackWriter{to: tmp, remaining: maxBytes}
	_, err = source.runStream(ctx, gitStream{stdin: strings.NewReader(revs.String()), stdout: bounded,
		env: rawTransportObjectEnvironment()},
		transportRawShallowFileFlag, os.DevNull, transportPackObjectsCommandName, transportPackOutputStreamFlag, transportPackRevisionsFlag)
	if bounded.exceeded {
		return transportAbsentObjectText, transportNoObjectBytesCount, fmt.Errorf(objectClosureSizeLimitErrorFormat, maxBytes, ErrTransportLimit)
	}
	if err != nil {
		return transportAbsentObjectText, transportNoObjectBytesCount, fmt.Errorf(objectClosureExportErrorFormat, err)
	}
	if _, err := tmp.Seek(transportObjectStartOffsetBytes, io.SeekStart); err != nil {
		return transportAbsentObjectText, transportNoObjectBytesCount, fmt.Errorf(objectClosureRewindErrorFormat, err)
	}
	blob := NewPlumbing(c)
	blobOID := blob.HashObject(ctx, tmp)
	if err := blob.Err(); err != nil {
		return transportAbsentObjectText, transportNoObjectBytesCount, err
	}
	return blobOID, maxBytes - bounded.remaining, nil
}

type closurePackWriter struct {
	to        io.Writer
	remaining int64
	exceeded  bool
}

func (w *closurePackWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		w.exceeded = objectClosureLimitExceededState
		return transportNoObjectBytesCount, ErrTransportLimit
	}
	n, err := w.to.Write(p)
	w.remaining -= int64(n)
	return n, err
}

// RestoreObjectClosurePackFromBlob indexes a bounded pack without creating
// refs. The caller verifies the exact signed object inventory afterward.
func (c *LocalGitx) RestoreObjectClosurePackFromBlob(ctx context.Context, blobOID string,
	maxBytes int64) (int64, error) {
	if !transportOID(blobOID) || maxBytes <= transportNoObjectBytesCount {
		return transportNoObjectBytesCount, fmt.Errorf(invalidObjectClosureRestoreErrorText)
	}
	tmp, err := os.CreateTemp(transportSystemTemporaryDirectoryPath, objectClosureRestoreFilePattern)
	if err != nil {
		return transportNoObjectBytesCount, fmt.Errorf(objectClosureRestoreStagingErrorFormat, err)
	}
	defer func() { _ = tmp.Close(); _ = os.Remove(tmp.Name()) }()
	plumbing := NewPlumbing(c)
	plumbing.ReadBlob(ctx, blobOID, tmp, maxBytes)
	if err := plumbing.Err(); err != nil {
		return transportNoObjectBytesCount, fmt.Errorf(objectClosureBlobReadErrorFormat, err)
	}
	info, err := tmp.Stat()
	if err != nil {
		return transportNoObjectBytesCount, fmt.Errorf(objectClosureRestoreStatErrorFormat, err)
	}
	if _, err := tmp.Seek(transportObjectStartOffsetBytes, io.SeekStart); err != nil {
		return transportNoObjectBytesCount, fmt.Errorf(objectClosureRestoreRewindErrorFormat, err)
	}
	if _, err := c.runStream(ctx, gitStream{stdin: tmp}, transportIndexPackCommandName, transportInputStreamFlag,
		transportPackInputLimitOptionPrefix+strconv.FormatInt(maxBytes, transportByteCountRadix)); err != nil {
		return transportNoObjectBytesCount, fmt.Errorf(objectClosureIndexErrorFormat, err)
	}
	return info.Size(), nil
}

// ImportObjectFrom copies one exact object's reachable closure from a local
// canonical store into this private store. It creates no refs and checks that
// the requested object retains its type and OID after the bounded transfer.
func (c *LocalGitx) ImportObjectFrom(ctx context.Context, sourceDir, exactOID string,
	maxBytes int64) error {
	if sourceDir == objectClosureMissingSourcePath || !transportOID(exactOID) || maxBytes <= transportNoObjectBytesCount {
		return fmt.Errorf(invalidExactObjectImportErrorText)
	}
	source := &LocalGitx{Dir: sourceDir, Log: c.Log}
	kind, err := source.ObjectType(ctx, exactOID)
	if err != nil {
		return fmt.Errorf(exactObjectSourceUnavailableErrorFormat, err)
	}
	blobOID, _, err := c.ImportObjectClosurePackAsBlob(ctx, ObjectClosurePackImportOptions{
		SourceDir: sourceDir, IncludeOIDs: []string{exactOID}, MaxBytes: maxBytes})
	if err != nil {
		return err
	}
	if _, err := c.RestoreObjectClosurePackFromBlob(ctx, blobOID, maxBytes); err != nil {
		return err
	}
	actualKind, err := c.ObjectType(ctx, exactOID)
	if err != nil || actualKind != kind {
		return fmt.Errorf(exactObjectSourceTypeMismatchErrorFormat, exactOID, kind)
	}
	return nil
}
