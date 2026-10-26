// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
)

const (
	invalidInputChangeError                 = "execution: native input invalidation lacks exact changed prepared state and affected task list"
	invalidInputTransitionError             = "execution: native input invalidation belongs to another run or graph"
	invalidInputAlreadyStoppingError        = "execution: native input changed after run stopping"
	invalidInputNoPersistError              = "execution: native input invalidation has no signed ledger persistence"
	invalidInputNoResultError               = "execution: changed package has no accepted result to invalidate"
	invalidInputLedgerError                 = "execution: native input invalidation cannot settle against a contiguous signed result history"
	invalidInputAcceptedSourceConflictError = "execution: accepted task claims disagree on the pinned source snapshot"
	invalidInputMissingIdentity             = ""
	invalidInputInvalid                     = false
	invalidInputValid                       = true
	invalidInputAffected                    = true
	invalidInputMatchingClaim               = true
	invalidInputRunStopping                 = true
	invalidInputRunInvalidated              = true
	invalidInputSignedStop                  = true
	invalidInputVisitedTask                 = true
	invalidInputSHA1HexDigits               = 40
	invalidInputSHA256HexDigits             = 64
	invalidInputNoTasks                     = 0
	invalidInputNoEvents                    = 0
	invalidInputFirstParentIndex            = 0
	invalidInputSingleParentCount           = 1
	invalidInputEventPageSize               = 32
	invalidInputQueueInitialCapacity        = 0
	invalidInputTaskListInitialLength       = 0
	invalidInputQueueHeadIndex              = 0
	invalidInputQueueNextIndex              = 1
)

// ChoreographyInputInvalidationRequest is invoked only after the first node
// proves a relevant native preparation changed. It never rewrites a Result.
type ChoreographyInputInvalidationRequest struct {
	ChoreographyRunBinding
	GraphRootOID   string   `json:"graphRootOid"`
	OldPreparedOID string   `json:"oldPreparedOid"`
	NewPreparedOID string   `json:"newPreparedOid"`
	AffectedTasks  []string `json:"affectedTasks"`
}

func validChoreographyInputInvalidation(request ChoreographyInputInvalidationRequest) bool {
	if !request.ChoreographyRunBinding.valid() || request.GraphRootOID == invalidInputMissingIdentity ||
		request.OldPreparedOID == request.NewPreparedOID ||
		!(validSlotHex(request.OldPreparedOID, invalidInputSHA1HexDigits) ||
			validSlotHex(request.OldPreparedOID, invalidInputSHA256HexDigits)) ||
		!(validSlotHex(request.NewPreparedOID, invalidInputSHA1HexDigits) ||
			validSlotHex(request.NewPreparedOID, invalidInputSHA256HexDigits)) ||
		len(request.AffectedTasks) == invalidInputNoTasks || !slices.IsSorted(request.AffectedTasks) {
		return invalidInputInvalid
	}
	prior := invalidInputMissingIdentity
	for _, task := range request.AffectedTasks {
		if task == invalidInputMissingIdentity || task == prior {
			return invalidInputInvalid
		}
		prior = task
	}
	return invalidInputValid
}

func (g *GitChoreographyPersist) PersistInputInvalidation(ctx context.Context,
	request ChoreographyInputInvalidationRequest) error {
	if err := g.opts.VerifyFirst(ctx); err != nil {
		return err
	}
	if !validChoreographyInputInvalidation(request) {
		return fmt.Errorf(invalidInputChangeError)
	}
	if request.ChoreographyRunBinding != g.opts.Binding || request.GraphRootOID != g.opts.GraphRootOID {
		return fmt.Errorf(invalidInputTransitionError)
	}
	for _, task := range request.AffectedTasks {
		if g.tasks[task].Task == invalidInputMissingIdentity {
			return fmt.Errorf(invalidInputChangeError)
		}
	}
	head, err := g.LedgerHead(ctx)
	if err != nil {
		return err
	}
	data, err := json.Marshal(request)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	identity := hex.EncodeToString(sum[:])
	return g.persistAtomic(ctx, choreographyLedgerMutation{
		Identity: identity, ExpectedLedgerOID: head.OID, NewTaskOID: g.opts.GraphRootOID,
		Event: ChoreographyEvent{Kind: ChoreographyInputInvalidation,
			Task: choreographyRunTaskID, TaskOID: g.opts.GraphRootOID,
			GraphRootOID:   g.opts.GraphRootOID,
			OldPreparedOID: request.OldPreparedOID,
			NewPreparedOID: request.NewPreparedOID,
			AffectedTasks:  slices.Clone(request.AffectedTasks)},
	})
}

// ReadAcceptedClaimSourceSnapshot resolves one repository's immutable source
// parent through the signed accepted Claim and its signed prepared carrier.
func (g *GitChoreographyPersist) ReadAcceptedClaimSourceSnapshot(ctx context.Context,
	event ChoreographyEvent, repository string) (string, error) {
	if event.Kind != ChoreographyClaimAccepted || event.GraphRootOID != g.opts.GraphRootOID ||
		event.TaskOID == invalidInputMissingIdentity || g.tasks[event.Task].Task == invalidInputMissingIdentity {
		return invalidInputMissingIdentity, fmt.Errorf(invalidInputLedgerError)
	}
	tree, err := g.opts.Mailbox.readTree(ctx, event.TaskOID)
	if err != nil {
		return invalidInputMissingIdentity, err
	}
	if tree.kind != choreographyClaimKind {
		return invalidInputMissingIdentity, fmt.Errorf(invalidInputLedgerError)
	}
	document, err := g.opts.Mailbox.Read(ctx, ChainTip{OID: event.TaskOID, Kind: tree.kind,
		document: tree.document, signature: tree.signature}, returnReplyLimit)
	if err != nil {
		return invalidInputMissingIdentity, err
	}
	var claim ChoreographyClaimProposal
	if err := strictJSON(document, &claim); err != nil {
		return invalidInputMissingIdentity, err
	}
	if claim.ChoreographyRunBinding != g.opts.Binding || claim.GraphRootOID != g.opts.GraphRootOID ||
		claim.Task != event.Task || claim.Attempt != event.Attempt || claim.Node != event.Node ||
		claim.ProposalID != event.ProposalID || claim.Branch != g.tasks[event.Task].Branch ||
		claim.InputsDigest != event.InputsDigest {
		return invalidInputMissingIdentity, fmt.Errorf(invalidInputLedgerError)
	}
	if claim.Bindings.PreparedInputOID == invalidInputMissingIdentity {
		parents, err := g.opts.Mailbox.plumbing.CommitParents(ctx, event.TaskOID)
		if err != nil || len(parents) != invalidInputSingleParentCount {
			return invalidInputMissingIdentity, fmt.Errorf(invalidInputLedgerError)
		}
		view, err := g.opts.Mailbox.OpenCarriedAssignment(ctx,
			parents[invalidInputFirstParentIndex], returnReplyLimit)
		if err != nil {
			return invalidInputMissingIdentity, err
		}
		if view.Assignment.GraphRootOID != g.opts.GraphRootOID || view.Assignment.Task != event.Task ||
			view.Assignment.TemplateDigest != claim.TemplateDigest {
			return invalidInputMissingIdentity, fmt.Errorf(invalidInputLedgerError)
		}
		for _, source := range view.Assignment.Repositories {
			if source.Name == repository {
				return source.Snapshot, nil
			}
		}
		return invalidInputMissingIdentity, nil
	}
	carrier, err := g.opts.Mailbox.ReadChoreographyPreparedCarrier(ctx, claim.Bindings.PreparedInputOID, returnReplyLimit)
	if err != nil {
		return invalidInputMissingIdentity, err
	}
	if carrier.ChoreographyRunBinding != g.opts.Binding || carrier.GraphRootOID != g.opts.GraphRootOID ||
		carrier.Task != event.Task || carrier.TemplateDigest != claim.TemplateDigest {
		return invalidInputMissingIdentity, fmt.Errorf(invalidInputLedgerError)
	}
	return carrier.SourceSnapshot(repository), nil
}

// AcceptedSourceSnapshot reconstructs the first accepted build/preparation
// source identity from the retained signed ledger, including after restart.
// Every later accepted claim for the same package/repository must agree.
func (b *FirstNodeBroker) AcceptedSourceSnapshot(ctx context.Context,
	repository, packageName string) (string, bool, error) {
	if b == nil || packageName == invalidInputMissingIdentity {
		return invalidInputMissingIdentity, invalidInputInvalid, fmt.Errorf(invalidInputChangeError)
	}
	reader, ok := b.persist.(interface {
		ReadAcceptedClaimSourceSnapshot(context.Context, ChoreographyEvent, string) (string, error)
	})
	if !ok {
		return invalidInputMissingIdentity, invalidInputInvalid, fmt.Errorf(invalidInputNoPersistError)
	}
	cursor := ChoreographyEventCursor{}
	baseline := invalidInputMissingIdentity
	claimSources := make(map[string]string)
	for {
		page, err := b.persist.ReadEvents(ctx, cursor, invalidInputEventPageSize)
		if err != nil {
			return invalidInputMissingIdentity, invalidInputInvalid, err
		}
		if page.Cursor.OID == invalidInputMissingIdentity || page.Cursor.Sequence != cursor.Sequence+uint64(len(page.Events)) ||
			len(page.Events) == invalidInputNoEvents && page.More {
			return invalidInputMissingIdentity, invalidInputInvalid, fmt.Errorf(invalidInputLedgerError)
		}
		for _, event := range page.Events {
			task := b.tasks[event.Task]
			if task == nil || task.spec.Package != packageName ||
				(task.spec.Kind != string(KindBuild) && task.spec.Kind != string(KindPrepare)) {
				continue
			}
			if event.Kind == ChoreographyClaimAccepted {
				oid, err := reader.ReadAcceptedClaimSourceSnapshot(ctx, event, repository)
				if err != nil {
					return invalidInputMissingIdentity, invalidInputInvalid, err
				}
				claimSources[event.TaskOID] = oid
			}
			if event.Kind == ChoreographyResultAccepted {
				oid := claimSources[event.PriorTaskOID]
				if oid == invalidInputMissingIdentity {
					continue
				}
				if baseline != invalidInputMissingIdentity && baseline != oid {
					return invalidInputMissingIdentity, invalidInputInvalid,
						fmt.Errorf(invalidInputAcceptedSourceConflictError)
				}
				baseline = oid
			}
		}
		cursor = page.Cursor
		if !page.More {
			break
		}
	}
	return baseline, baseline != invalidInputMissingIdentity, nil
}

// InvalidatePreparedInput fences admission in the same durable CAS that
// publishes the changed-input evidence. A later explicit RunStopping event
// completes the public stop sequence; a crash between them remains fenced.
func (b *FirstNodeBroker) InvalidatePreparedInput(ctx context.Context,
	request ChoreographyInputInvalidationRequest) error {
	if !validChoreographyInputInvalidation(request) {
		return fmt.Errorf(invalidInputChangeError)
	}
	if request.ChoreographyRunBinding != b.binding || request.GraphRootOID != b.graphRootOID {
		return fmt.Errorf(invalidInputTransitionError)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.invalidatePreparedInputLocked(ctx, request)
}

func (b *FirstNodeBroker) invalidatePreparedInputLocked(ctx context.Context,
	request ChoreographyInputInvalidationRequest) error {
	if b.stopping {
		return fmt.Errorf(invalidInputAlreadyStoppingError)
	}
	for _, task := range request.AffectedTasks {
		if b.tasks[task] == nil {
			return fmt.Errorf(invalidInputChangeError)
		}
	}
	persist, ok := b.persist.(interface {
		PersistInputInvalidation(context.Context, ChoreographyInputInvalidationRequest) error
	})
	if !ok {
		return fmt.Errorf(invalidInputNoPersistError)
	}
	if err := persist.PersistInputInvalidation(ctx, request); err != nil {
		return err
	}
	b.stopping, b.invalidated = invalidInputRunStopping, invalidInputRunInvalidated
	b.wakeBudgetWaiters()
	_, err := b.persist.StopClaims(ctx)
	if err == nil {
		b.signedStop = invalidInputSignedStop
	}
	return err
}

// InvalidatePackagePreparedInput derives affected task identities from the
// signed Result feed while holding the same broker mutex as Result admission.
// The caller has already proven a relevant prepared-path change under the
// native effect lane; this method atomically records the invalidation fence.
type ChoreographyPreparedInputChange struct {
	Repository     string
	Package        string
	OldPreparedOID string
	NewPreparedOID string
}

func (b *FirstNodeBroker) InvalidatePackagePreparedInput(ctx context.Context,
	change ChoreographyPreparedInputChange) error {
	repository, packageName := change.Repository, change.Package
	oldPreparedOID, newPreparedOID := change.OldPreparedOID, change.NewPreparedOID
	if b == nil || packageName == invalidInputMissingIdentity {
		return fmt.Errorf(invalidInputChangeError)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stopping {
		return fmt.Errorf(invalidInputAlreadyStoppingError)
	}
	affected := make(map[string]bool)
	matchingClaims := make(map[string]bool)
	cursor := ChoreographyEventCursor{}
	for {
		page, err := b.persist.ReadEvents(ctx, cursor, invalidInputEventPageSize)
		if err != nil {
			return err
		}
		if page.Cursor.OID == invalidInputMissingIdentity || page.Cursor.Sequence != cursor.Sequence+uint64(len(page.Events)) ||
			len(page.Events) == invalidInputNoEvents && page.More {
			return fmt.Errorf(invalidInputLedgerError)
		}
		for _, event := range page.Events {
			if event.Kind == ChoreographyClaimAccepted {
				if task := b.tasks[event.Task]; task != nil && task.spec.Package == packageName {
					reader, ok := b.persist.(interface {
						ReadAcceptedClaimSourceSnapshot(context.Context, ChoreographyEvent, string) (string, error)
					})
					if !ok {
						return fmt.Errorf(invalidInputNoPersistError)
					}
					preparedOID, err := reader.ReadAcceptedClaimSourceSnapshot(ctx, event, repository)
					if err != nil {
						return err
					}
					matchingClaims[event.TaskOID] = preparedOID == oldPreparedOID
				}
			}
			if event.Kind == ChoreographyResultAccepted && matchingClaims[event.PriorTaskOID] {
				if task := b.tasks[event.Task]; task != nil && task.spec.Package == packageName {
					affected[event.Task] = invalidInputAffected
				}
			}
		}
		cursor = page.Cursor
		if !page.More {
			break
		}
	}
	if len(affected) == invalidInputNoTasks {
		return fmt.Errorf(invalidInputNoResultError)
	}
	queue := make([]string, invalidInputQueueInitialCapacity, len(affected))
	visited := make(map[string]bool, len(b.tasks))
	for task := range affected {
		queue = append(queue, task)
		visited[task] = invalidInputVisitedTask
	}
	for len(queue) > invalidInputNoTasks {
		name := queue[invalidInputQueueHeadIndex]
		queue = queue[invalidInputQueueNextIndex:]
		for _, child := range b.tasks[name].children {
			if visited[child] {
				continue
			}
			visited[child] = invalidInputVisitedTask
			next := b.tasks[child]
			if next == nil {
				return fmt.Errorf(invalidInputLedgerError)
			}
			queue = append(queue, child)
			if !next.spec.Virtual && !next.terminal {
				affected[child] = invalidInputAffected
			}
		}
	}
	tasks := make([]string, invalidInputTaskListInitialLength, len(affected))
	for task := range affected {
		tasks = append(tasks, task)
	}
	slices.Sort(tasks)
	return b.invalidatePreparedInputLocked(ctx, ChoreographyInputInvalidationRequest{
		ChoreographyRunBinding: b.binding, GraphRootOID: b.graphRootOID,
		OldPreparedOID: oldPreparedOID, NewPreparedOID: newPreparedOID,
		AffectedTasks: tasks})
}
