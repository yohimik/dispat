// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"
)

const (
	compiledRetirementMailboxError    = "execution: sealed compiled run has no signed canonical mailbox"
	compiledRetirementMissingRunError = "execution: sealed compiled run lost its pinned return channel"
	compiledRetirementNoPendingClaims = 0
	compiledRetirementNoParkedClaims  = 0
)

// retireSealedCompiledRun reads retained signed Git finality after the
// first-node HTTPS listener closes. It only removes a run once the signed
// feed is sealed and this node has no unresolved proposal or physical lease.
// A transient listener failure cannot retire a live run.
func (w *Worker) retireSealedCompiledRun(ctx context.Context, run *workerCompiledRun) (bool, error) {
	if run == nil || run.frontier == nil || run.joining {
		return compiledNoSelectionProgress, nil
	}
	if !run.frontier.sealed {
		if run.retainedFeed == nil {
			mailbox, ok := w.Mailbox.(*GitMailbox)
			if !ok {
				return compiledNoSelectionProgress, fmt.Errorf(compiledRetirementMailboxError)
			}
			feed, err := OpenRetainedChoreographyFeed(ctx, RetainedChoreographyFeedOptions{
				Mailbox: mailbox, Binding: run.binding, BootstrapOID: run.bootstrapOID,
				MaxDocumentBytes: w.Report.Limits.MaxManifestBytes,
				MaxTagBytes:      w.Report.Limits.MaxPackBytes})
			if err != nil {
				return compiledNoSelectionProgress, err
			}
			run.retainedFeed = feed
		}
		for {
			page, err := run.retainedFeed.ReadEvents(ctx, run.frontier.Cursor(), compiledEventPageSize)
			if err != nil {
				return compiledNoSelectionProgress, err
			}
			if _, err := w.applyCompiledEventPage(run, page); err != nil {
				return compiledNoSelectionProgress, err
			}
			if !page.More {
				break
			}
		}
	}
	if !run.frontier.sealed || len(run.pending) != compiledRetirementNoPendingClaims {
		return compiledNoSelectionProgress, nil
	}
	w.claims.Lock()
	for _, claim := range w.claimed {
		if claim.assignment.Run == run.binding.Run {
			w.claims.Unlock()
			return compiledNoSelectionProgress, nil
		}
	}
	w.claims.Unlock()
	for _, claim := range w.pendingReturn {
		if claim.assignment.Run == run.binding.Run {
			return compiledNoSelectionProgress, nil
		}
	}
	if parked := w.parkedReturn[run.binding.Run]; parked != nil &&
		len(parked.entries) != compiledRetirementNoParkedClaims {
		return compiledNoSelectionProgress, nil
	}
	if w.NodeSlots != nil {
		leases, err := w.NodeSlots.Held(ctx)
		if err != nil {
			return compiledNoSelectionProgress, err
		}
		for _, lease := range leases {
			if lease.Intent.Run == run.binding.Run {
				return compiledNoSelectionProgress, nil
			}
		}
	}
	w.returnMu.Lock()
	if w.returnChannels[run.binding.Run] != run.channel {
		w.returnMu.Unlock()
		return compiledNoSelectionProgress, fmt.Errorf(compiledRetirementMissingRunError)
	}
	delete(w.returnChannels, run.binding.Run)
	w.returnMu.Unlock()
	delete(w.compiledRuns, run.binding.Run)
	run.channel.client.Close()
	return compiledSelectionProgress, nil
}

func (w *Worker) refreshSealedCompiledRuns(ctx context.Context) {
	for _, run := range w.compiledRuns {
		_, _ = w.retireSealedCompiledRun(ctx, run)
	}
}
