// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"
	"io"
	"os"
)

const (
	workerResultPackIndexMinimumBytes int64 = 16 << 20
	workerResultPackStatErrorFormat         = "execution: prepared result pack is unavailable: %w"
	workerResultPackSizeError               = "execution: prepared result pack differs from its bounded export"
	workerResultPackIndexErrorFormat        = "execution: prepared result pack could not be indexed: %w"
)

type workerResultPack struct {
	Result        Result
	OID           string
	Payload       *os.File
	MaxBytes      int64
	ExportedBytes int64
}

func (w *Worker) publishPreparedResult(ctx context.Context, pack workerResultPack) error {
	if pack.Result.PendingOID == workerReturnNoIdentity {
		return nil
	}
	if err := w.indexPreparedResultPack(ctx, pack); err != nil {
		return err
	}
	return w.pushPickupRef(ctx, workerPickupRef{Branch: pack.Result.OutcomeBranch, OID: pack.OID})
}

func (w *Worker) indexPreparedResultPack(ctx context.Context, pack workerResultPack) error {
	info, err := pack.Payload.Stat()
	if err != nil {
		return fmt.Errorf(workerResultPackStatErrorFormat, err)
	}
	if info.Size() != pack.ExportedBytes || info.Size() > pack.MaxBytes {
		return fmt.Errorf(workerResultPackSizeError)
	}
	// The guard avoids an extra Git process for small results. This is a
	// conservative reuse threshold, not a transport limit or a measured optimum.
	if info.Size() < workerResultPackIndexMinimumBytes {
		return nil
	}
	if _, err := pack.Payload.Seek(returnSeekStartOffset, io.SeekStart); err != nil {
		return fmt.Errorf(workerResultPackIndexErrorFormat, err)
	}
	// This exact file came from the bounded raw-object export above. Indexing
	// exposes its compressed representations to the subsequent create-only push;
	// finding an already-local commit alone would not prove pack membership.
	if err := w.Cache.ImportCommitPack(ctx, pack.OID, pack.Payload, pack.MaxBytes); err != nil {
		return fmt.Errorf(workerResultPackIndexErrorFormat, err)
	}
	return nil
}
