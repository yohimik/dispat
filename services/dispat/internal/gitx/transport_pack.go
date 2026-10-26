// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

// Stream an exact commit's reachable objects over the non-Git return channel.
// Workers write no remote refs; only the first node imports and later
// pushes the accepted task commit from its own object store.

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ExportCommitPack writes a Git pack containing the exact commit closure.
// It never buffers artifact bytes in the Go process.
func (c *LocalGitx) ExportCommitPack(ctx context.Context, oid string, to io.Writer) error {
	if !transportOID(oid) {
		return fmt.Errorf(invalidResultCommitIDErrorText)
	}
	resolved, err := c.ResolveCommit(ctx, oid)
	if err != nil || resolved != oid {
		return fmt.Errorf(resultCommitUnavailableErrorFormat, ErrCommitNotOnRef)
	}
	_, err = c.runStream(ctx, gitStream{stdin: strings.NewReader(oid + transportObjectLineSeparatorText), stdout: to},
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
