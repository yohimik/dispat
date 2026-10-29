// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"
)

const (
	transportCommitHeaderMaximumBytes     = 16 << 20
	transportCommitHeaderLineMaximumBytes = MaxTreeEntryBytes
	transportCommitTreeHeaderPrefix       = "tree "
	transportCommitParentHeaderPrefix     = "parent "
	transportCommitHeaderEmptyLine        = ""
	transportCommitHeaderLineDelimiter    = '\n'
	transportCommitHeaderFieldSeparator   = " "
	transportCommitHeaderForbiddenKey     = "\t\r\x00"
	transportCommitHeaderParentName       = "parent"
	transportCommitHeaderTreeName         = "tree"
	transportCommitNoParents              = 0
	transportCommitFirstParent            = 0
	transportCommitNextByte               = 1
	transportCommitStatePresent           = true
	transportCommitStateAbsent            = false
	transportCommitInvalidHeaderError     = "gitx: commit %s has an invalid raw header: %w"
	transportCommitHeaderLimitError       = "raw commit header exceeds its byte limit: %w"
	transportCommitMissingTreeError       = "raw commit header has no valid leading tree"
	transportCommitMalformedParentError   = "raw commit header has a malformed, duplicate or misplaced parent"
	transportCommitDuplicateTreeError     = "raw commit header repeats its tree"
	transportCommitUnterminatedError      = "raw commit header has no terminating blank line"
	transportCommitMalformedHeaderError   = "raw commit header field has invalid syntax"
	transportCommitRawReadError           = "gitx: reading raw commit %s: %w"
	transportCommitNotFirstParentError    = "gitx: commit %s is not on the raw first-parent chain of %s"
	transportCommitRepeatedParentError    = "gitx: raw first-parent chain repeats commit %s"
	transportCommitAbsentRefError         = "gitx: commit %s is not on the raw first-parent chain of %s: %w"
	transportCommitLocalRefReadError      = "gitx: resolving exact local ref %s: %w"
	transportCommitInvalidRefOIDError     = "gitx: local ref %s has no exact object id"
	transportCommitShowRefCommand         = "show-ref"
	transportCommitShowRefHashFlag        = "--hash"
	transportRawGraftEnvironmentPrefix    = "GIT_GRAFT_FILE="
	transportRawShallowFileFlag           = "--shallow-file"
	transportRawCommitPeelSuffix          = "^{commit}"
)

func rawTransportObjectEnvironment() []string {
	return []string{transportDisableReplacementEnvironmentValue, transportRawGraftEnvironmentPrefix + os.DevNull}
}

// CommitParents reads object bytes rather than revision history: replacement
// refs, grafts and shallow boundaries cannot change an authenticated parent.
func (r *ObjectReader) CommitParents(oid string) ([]string, error) {
	if !transportOID(oid) {
		return nil, fmt.Errorf(invalidSnapshotCommitIDErrorText)
	}
	header := &transportRawCommitHeader{}
	if _, err := r.readObject(transportObjectRead{oid: oid, objectType: transportCommitObjectTypeName,
		to: header, maxBytes: math.MaxInt64}); err != nil {
		return nil, fmt.Errorf(transportCommitRawReadError, oid, err)
	}
	if !header.isComplete {
		return nil, fmt.Errorf(transportCommitInvalidHeaderError, oid, errors.New(transportCommitUnterminatedError))
	}
	return header.parents, nil
}

type transportRawCommitHeader struct {
	pending       []byte
	parents       []string
	seenParents   map[string]bool
	headerBytes   int
	isTreePresent bool
	isParentEnd   bool
	isComplete    bool
}

func (h *transportRawCommitHeader) Write(content []byte) (int, error) {
	count := len(content)
	for !h.isComplete && len(content) > transportNoObjectBytesCount {
		end := bytes.IndexByte(content, transportCommitHeaderLineDelimiter)
		lineBytes := len(content)
		if end >= transportNoObjectBytesCount {
			lineBytes = end + transportCommitNextByte
		}
		if h.headerBytes+lineBytes > transportCommitHeaderMaximumBytes ||
			len(h.pending)+lineBytes > transportCommitHeaderLineMaximumBytes {
			return transportNoObjectBytesCount, fmt.Errorf(transportCommitHeaderLimitError, ErrTransportLimit)
		}
		h.headerBytes += lineBytes
		if end < transportNoObjectBytesCount {
			h.pending = append(h.pending, content...)
			break
		}
		h.pending = append(h.pending, content[:end]...)
		if err := h.acceptLine(string(h.pending)); err != nil {
			return transportNoObjectBytesCount, err
		}
		h.pending = h.pending[:transportNoObjectBytesCount]
		content = content[lineBytes:]
	}
	return count, nil
}

func (h *transportRawCommitHeader) acceptLine(line string) error {
	if !h.isTreePresent {
		tree, found := strings.CutPrefix(line, transportCommitTreeHeaderPrefix)
		if !found || !transportOID(tree) {
			return errors.New(transportCommitMissingTreeError)
		}
		h.isTreePresent = transportCommitStatePresent
		return nil
	}
	if line == transportCommitHeaderEmptyLine {
		h.isComplete = transportCommitStatePresent
		return nil
	}
	if strings.HasPrefix(line, transportCommitTreeHeaderPrefix) {
		return errors.New(transportCommitDuplicateTreeError)
	}
	if parent, found := strings.CutPrefix(line, transportCommitParentHeaderPrefix); found {
		if h.isParentEnd || !transportOID(parent) || h.seenParents[parent] {
			return errors.New(transportCommitMalformedParentError)
		}
		if h.seenParents == nil {
			h.seenParents = make(map[string]bool)
		}
		h.seenParents[parent] = transportCommitStatePresent
		h.parents = append(h.parents, parent)
		return nil
	}
	if !strings.HasPrefix(line, transportCommitHeaderFieldSeparator) {
		key, _, hasValue := strings.Cut(line, transportCommitHeaderFieldSeparator)
		if !hasValue || strings.ContainsAny(key, transportCommitHeaderForbiddenKey) ||
			key == transportCommitHeaderParentName || key == transportCommitHeaderTreeName {
			return errors.New(transportCommitMalformedHeaderError)
		}
	}
	h.isParentEnd = transportCommitStatePresent
	return nil
}

func (c *LocalGitx) rawCommitParents(ctx context.Context, oid string) (parents []string, err error) {
	reader, err := OpenObjectReader(ctx, c)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, reader.Close()) }()
	return reader.CommitParents(oid)
}

func (c *LocalGitx) rawFirstParentOIDs(ctx context.Context, tip, after string) (history []string, err error) {
	reader, err := OpenObjectReader(ctx, c)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, reader.Close()) }()
	seen := make(map[string]bool)
	for oid := tip; ; {
		if seen[oid] {
			return nil, fmt.Errorf(transportCommitRepeatedParentError, oid)
		}
		seen[oid] = transportCommitStatePresent
		parents, err := reader.CommitParents(oid)
		if err != nil {
			return nil, err
		}
		if oid == after {
			slices.Reverse(history)
			return history, nil
		}
		history = append(history, oid)
		if len(parents) == transportCommitNoParents {
			return nil, fmt.Errorf(transportCommitNotFirstParentError, after, tip)
		}
		oid = parents[transportCommitFirstParent]
	}
}

func (c *LocalGitx) rawFetchedCommit(ctx context.Context, localRef, wantOID string, maxDepth int) (err error) {
	if !transportOID(wantOID) || maxDepth <= transportCommitNoParents {
		return ErrCommitNotOnRef
	}
	resolved, err := c.run(ctx, transportCommitShowRefCommand, transportVerifyRevisionFlag,
		transportCommitShowRefHashFlag, transportArgumentSeparatorText, localRef)
	if err != nil {
		return fmt.Errorf(transportCommitLocalRefReadError, localRef, errors.Join(ErrCommitNotOnRef, err))
	}
	oid := strings.TrimSpace(resolved)
	if !transportOID(oid) {
		return fmt.Errorf(transportCommitInvalidRefOIDError, localRef)
	}
	reader, err := OpenObjectReader(ctx, c)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, reader.Close()) }()
	for range maxDepth {
		parents, err := reader.CommitParents(oid)
		if err != nil {
			return errors.Join(ErrCommitNotOnRef, err)
		}
		if oid == wantOID {
			return nil
		}
		if len(parents) == transportCommitNoParents {
			break
		}
		oid = parents[transportCommitFirstParent]
	}
	return fmt.Errorf(transportCommitAbsentRefError, wantOID, strings.TrimSpace(resolved), ErrCommitNotOnRef)
}

func (c *LocalGitx) isRawAncestor(ctx context.Context, ancestor, tip string) (isAncestor bool, err error) {
	reader, err := OpenObjectReader(ctx, c)
	if err != nil {
		return transportCommitStateAbsent, err
	}
	defer func() { err = errors.Join(err, reader.Close()) }()
	seen := make(map[string]bool)
	pending := []string{tip}
	for len(pending) > transportCommitNoParents {
		last := len(pending) - transportCommitNextByte
		oid := pending[last]
		pending = pending[:last]
		if seen[oid] {
			continue
		}
		seen[oid] = transportCommitStatePresent
		parents, err := reader.CommitParents(oid)
		if err != nil {
			return transportCommitStateAbsent, err
		}
		if oid == ancestor {
			return transportCommitStatePresent, nil
		}
		for index := len(parents) - transportCommitNextByte; index >= transportCommitFirstParent; index-- {
			pending = append(pending, parents[index])
		}
	}
	return transportCommitStateAbsent, nil
}
