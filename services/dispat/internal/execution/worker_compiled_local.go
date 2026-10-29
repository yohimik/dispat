// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"
)

const (
	compiledLocalPickerIdentityError          = "execution: first-node compiled picker differs from signed complete run"
	compiledLocalPickerSettlementTimeoutError = "execution: first-node compiled picker has unsettled accepted tasks after its stop bound"
	compiledLocalPickerHeldLeaseError         = "execution: first-node compiled run retains a physical node lease after task settlement"
	compiledLocalPickerWrappedErrorFormat     = "%s: %w"
	compiledLocalMissingIdentity              = ""
	compiledLocalFirstRosterIndex             = 0
	compiledLocalMinimumSlots                 = 1
	compiledLocalInitialPendingClaims         = 0
	compiledLocalInitialWithdrawalBranches    = 0
)

// WaitCompiledLocalFinality keeps the signed feed alive until every accepted
// local task has observed its terminal decision and released its exact slot.
// The caller bounds this wait; a timed-out lease remains durable for recovery.
func (w *Worker) WaitCompiledLocalFinality(ctx context.Context, runID string) error {
	if w == nil || w.NodeSlots == nil || runID == compiledLocalMissingIdentity {
		return fmt.Errorf(compiledLocalPickerIdentityError)
	}
	settled := make(chan struct{})
	go func() {
		w.running.Wait()
		close(settled)
	}()
	select {
	case <-settled:
	case <-ctx.Done():
		return fmt.Errorf(compiledLocalPickerWrappedErrorFormat, compiledLocalPickerSettlementTimeoutError, ctx.Err())
	}
	held, err := w.NodeSlots.Held(ctx)
	if err != nil {
		return err
	}
	for _, lease := range held {
		if lease.Intent.Run == runID {
			return fmt.Errorf(compiledLocalPickerHeldLeaseError)
		}
	}
	return nil
}

// RunCompiledLocal lets the first node select its own ordinary compatible
// tasks from the same complete signed frontier as peers. Executor-owned
// native vertices are left to the release scheduler and its effect lanes.
func (w *Worker) RunCompiledLocal(ctx context.Context, broker *FirstNodeBroker,
	bootstrapOID, graphRootOID string) (runErr error) {
	if w == nil {
		return fmt.Errorf(compiledLocalPickerIdentityError)
	}
	mailbox, ok := w.Mailbox.(*GitMailbox)
	if !ok || broker == nil || w.NodeSlots == nil ||
		w.NodeSlots.AuthorityID() != w.Report.AuthorityID || w.Seen == nil ||
		w.Cache == nil || w.ReturnSigner == nil || bootstrapOID == compiledLocalMissingIdentity || graphRootOID == compiledLocalMissingIdentity {
		return fmt.Errorf(compiledLocalPickerIdentityError)
	}
	bootstrap, err := mailbox.ReadChoreographyBootstrap(ctx, bootstrapOID,
		w.Report.Limits.MaxManifestBytes, w.Report.Limits.MaxPackBytes)
	if err != nil {
		return err
	}
	if !validChoreographyMode(bootstrap.Mode) || bootstrap.ChoreographyRunBinding != broker.binding ||
		bootstrap.CoordinationEndpoint != w.Endpoint {
		return fmt.Errorf(compiledLocalPickerIdentityError)
	}
	carrier, rootOID, err := mailbox.ReadChoreographyGraphCarrier(ctx,
		broker.binding, bootstrapOID, w.Report.Limits.MaxManifestBytes)
	if err != nil {
		return err
	}
	if err := validateChoreographyHybridPolicy(bootstrap.Mode, &bootstrap.PinnedInputs, carrier); err != nil {
		return err
	}
	if err := validateChoreographyRetryPolicy(bootstrap.JoinPolicy, carrier.Tasks); err != nil {
		return err
	}
	if rootOID != graphRootOID {
		return fmt.Errorf(compiledLocalPickerIdentityError)
	}
	_, roster, err := ReadChoreographyLedgerRoot(ctx, mailbox, broker.binding, bootstrapOID)
	if err != nil {
		return err
	}
	index := slices.IndexFunc(roster.Nodes, func(node ChoreographyRuntimeNode) bool {
		return node.Name == w.Node
	})
	if index < compiledLocalFirstRosterIndex || !roster.Nodes[index].First ||
		roster.Nodes[index].AuthorityID != w.Report.AuthorityID ||
		roster.Nodes[index].Capacity != w.Report.Capacity {
		return fmt.Errorf(compiledLocalPickerIdentityError)
	}
	var interpreted *InterpretedRun
	isInstalled := false
	defer func() {
		if !isInstalled && interpreted != nil && interpreted.Close != nil {
			runErr = errors.Join(runErr, interpreted.Close())
		}
	}()
	if bootstrap.Mode == bootstrapInterpretationModeName {
		if w.PrepareInterpretedRun == nil {
			return fmt.Errorf(interpretedRunMissingCallbackError)
		}
		interpreted, err = w.PrepareInterpretedRun(ctx, InterpretedRunOptions{
			Bootstrap: bootstrap, BootstrapOID: bootstrapOID,
			GraphRootOID: rootOID, Roster: roster,
			ObjectStoreDir: mailbox.ObjectStoreDir(), Carrier: carrier})
		if err != nil {
			return err
		}
		if err := verifyInterpretedRun(bootstrap, carrier, interpreted); err != nil {
			return err
		}
	}
	client, err := DirectChoreographyReturn(broker)
	if err != nil {
		return err
	}
	channel := &workerReturnChannel{binding: broker.binding, endpoint: bootstrap.ReturnEndpoint,
		certSHA256: bootstrap.ReturnCertSHA256, bootstrapOID: bootstrapOID,
		bootstrap: &bootstrap, client: client}
	defer func() {
		if !isInstalled {
			w.closeUnrememberedReturnChannel(channel)
		}
	}()
	run := &workerCompiledRun{binding: broker.binding, bootstrapOID: bootstrapOID,
		rootOID: rootOID, bootstrap: bootstrap, carrier: carrier, channel: channel,
		member: roster.Nodes[index], interpreted: interpreted, templates: make(map[string]ChoreographyCompiledTemplate,
			len(carrier.Templates)), pending: make(map[string]*workerCompiledClaim, compiledLocalInitialPendingClaims),
		parked: make(map[string]compiledParkedCapacity)}
	for _, template := range carrier.Templates {
		run.templates[template.Task] = template
	}
	if err := run.openFrontier(w.Node); err != nil {
		return err
	}
	w.rememberReturnChannel(channel)
	w.compiledRuns = map[string]*workerCompiledRun{broker.binding.Run: run}
	isInstalled = true
	w.slots = make(chan struct{}, max(w.Report.Capacity, compiledLocalMinimumSlots))
	defer func() {
		settled := make(chan struct{})
		go func() {
			w.running.Wait()
			w.closeReturnChannels()
			close(settled)
		}()
		isFinished := false
		select {
		case <-settled:
			isFinished = true
		case <-time.After(max(taskReportTimeout, w.TransferTimeout)):
			w.Log.Error().Msg(compiledLocalPickerSettlementTimeoutError)
			if runErr == nil {
				runErr = fmt.Errorf(compiledLocalPickerSettlementTimeoutError)
			}
		}
		if isFinished && run.interpreted != nil {
			queryCtx, stopQuery := context.WithTimeout(context.WithoutCancel(ctx), taskReportTimeout)
			held, heldErr := w.NodeSlots.Held(queryCtx)
			stopQuery()
			if heldErr != nil {
				runErr = errors.Join(runErr, heldErr)
			} else {
				isCloseAllowed := true
				for _, lease := range held {
					if lease.Intent.Run == run.binding.Run {
						isCloseAllowed = false
						break
					}
				}
				if isCloseAllowed {
					runErr = errors.Join(runErr, run.interpreted.Close())
					run.interpreted = nil
				}
			}
		}
	}()
	ticker := time.NewTicker(minimumPollInterval)
	defer ticker.Stop()
	for {
		w.settlePendingReturnClaims(ctx)
		w.pollParkedReturnClaims(ctx)
		if err := w.pollCompiledLocalWithdrawals(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if _, err := w.pollCompiledRuns(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// pollCompiledLocalWithdrawals reads only refs for accepted local tasks.
// The ordinary worker's mailbox poll does this; the direct first-node picker
// has no slot-branch poll and must still deliver a signed Cancel to its task.
func (w *Worker) pollCompiledLocalWithdrawals(ctx context.Context) error {
	w.claims.Lock()
	branches := make([]string, compiledLocalInitialWithdrawalBranches, len(w.claimed))
	for identity := range w.claimed {
		branches = append(branches, identity.Branch)
	}
	w.claims.Unlock()
	for _, branch := range branches {
		head, err := w.Mailbox.Reread(ctx, branch)
		if err != nil {
			return err
		}
		if head.OID == compiledLocalMissingIdentity {
			return fmt.Errorf(compiledLocalPickerIdentityError)
		}
		tip, err := w.Mailbox.Inspect(ctx, head)
		if err != nil {
			return err
		}
		if tip.Kind == MessageCancel {
			w.observeWithdrawal(ctx, tip)
		}
	}
	return nil
}
