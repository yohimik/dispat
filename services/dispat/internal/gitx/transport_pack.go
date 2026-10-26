// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

// Stream signed result objects over the return channel independently of
// worker outcome refs. Only the first node admits canonical task outcomes.

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	resultPackNoBaseOID                  = ""
	resultPackExcludeRevisionPrefix      = "^"
	resultPackInvalidBaseErrorText       = "gitx: result pack has an invalid known ancestor"
	resultPackBaseUnavailableErrorFormat = "gitx: result pack ancestor is unavailable: %w"
	resultPackBaseNotAncestorErrorText   = "gitx: result pack base is not an ancestor of the result"
)

type CommitPackDeltaOptions struct {
	CommitOID string
	BaseOID   string
	Writer    io.Writer
}

// ExportCommitPack writes a Git pack containing the exact commit closure.
// It never buffers artifact bytes in the Go process.
func (c *LocalGitx) ExportCommitPack(ctx context.Context, oid string, to io.Writer) error {
	return c.exportCommitPack(ctx, CommitPackDeltaOptions{CommitOID: oid, Writer: to})
}

// ExportCommitPackSince excludes an ancestor already held by the receiver.
// Pending and outcome commits stay in the pack without retransmitting the
// accepted claim's complete source and graph ancestry.
func (c *LocalGitx) ExportCommitPackSince(ctx context.Context, options CommitPackDeltaOptions) error {
	if !transportOID(options.CommitOID) {
		return fmt.Errorf(invalidResultCommitIDErrorText)
	}
	if !transportOID(options.BaseOID) {
		return fmt.Errorf(resultPackInvalidBaseErrorText)
	}
	isAncestor, err := c.mergeBaseIsAncestor(ctx, options.BaseOID, options.CommitOID)
	if err != nil {
		return fmt.Errorf(resultPackBaseUnavailableErrorFormat, err)
	}
	if !isAncestor {
		return fmt.Errorf(resultPackBaseNotAncestorErrorText)
	}
	return c.exportCommitPack(ctx, options)
}

func (c *LocalGitx) exportCommitPack(ctx context.Context, options CommitPackDeltaOptions) error {
	oid, to := options.CommitOID, options.Writer
	if !transportOID(oid) {
		return fmt.Errorf(invalidResultCommitIDErrorText)
	}
	resolved, err := c.ResolveCommit(ctx, oid)
	if err != nil || resolved != oid {
		return fmt.Errorf(resultCommitUnavailableErrorFormat, ErrCommitNotOnRef)
	}
	revisions := oid + transportObjectLineSeparatorText
	if options.BaseOID != resultPackNoBaseOID {
		revisions += resultPackExcludeRevisionPrefix + options.BaseOID + transportObjectLineSeparatorText
	}
	_, err = c.runStream(ctx, gitStream{stdin: strings.NewReader(revisions), stdout: to},
		transportPackObjectsCommandName, transportPackOutputStreamFlag, transportPackRevisionsFlag)
	if err != nil {
		return fmt.Errorf(resultCommitExportErrorFormat, oid, err)
	}
	return nil
}

// ImportCommitPack consumes at most maxBytes of pack input and verifies that
// the requested exact commit is available afterward. It creates no ref.
func (c *LocalGitx) ImportCommitPack(ctx context.Context, oid string, from io.Reader, maxBytes int64) error {
	if !transportOID(oid) || maxBytes <= transportNoObjectBytesCount {
		return fmt.Errorf(invalidPackedResultBoundErrorText)
	}
	bounded := &boundedPackReader{from: from, remaining: maxBytes}
	_, err := c.runStream(ctx, gitStream{stdin: bounded}, transportIndexPackCommandName, transportInputStreamFlag,
		transportPackInputLimitOptionPrefix+strconv.FormatInt(maxBytes, transportByteCountRadix))
	if err != nil {
		return fmt.Errorf(resultPackImportErrorFormat, err)
	}
	if bounded.exceeded {
		return fmt.Errorf(resultPackSizeLimitErrorFormat, maxBytes, ErrTransportLimit)
	}
	resolved, err := c.ResolveCommit(ctx, oid)
	if err != nil || resolved != oid {
		return fmt.Errorf(resultPackMissingCommitErrorFormat, oid, ErrCommitNotOnRef)
	}
	return nil
}

type boundedPackReader struct {
	from      io.Reader
	remaining int64
	exceeded  bool
}

func (r *boundedPackReader) Read(p []byte) (int, error) {
	if r.remaining == transportNoObjectBytesCount {
		var one [transportOverflowProbeBytes]byte
		n, err := r.from.Read(one[:])
		if n != transportNoObjectBytesCount {
			r.exceeded = transportPackLimitExceededState
			return transportNoObjectBytesCount, ErrTransportLimit
		}
		return transportNoObjectBytesCount, err
	}
	if int64(len(p)) > r.remaining {
		p = p[:int(r.remaining)]
	}
	n, err := r.from.Read(p)
	r.remaining -= int64(n)
	return n, err
}
