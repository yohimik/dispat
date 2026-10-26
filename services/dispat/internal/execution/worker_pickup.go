// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/fsx"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

const (
	workerPickupDirectoryName                      = "pickups"
	workerPickupFileSuffix                         = ".json"
	workerPickupDirectoryMode          os.FileMode = 0o700
	workerPickupFileMode               os.FileMode = 0o600
	workerPickupStateMaximumBytes                  = 4 << 10
	workerPickupNoIdentity                         = ""
	workerPickupNoRemoteHeads                      = 0
	workerPickupOneRemoteHead                      = 1
	workerPickupFirstRemoteHeadIndex               = 0
	workerPickupCreateAttempts                     = 2
	workerPickupFirstCreateAttempt                 = 0
	workerPickupCreateAttemptIncrement             = 1
	workerPickupClaimParentCount                   = 1
	workerPickupClaimParentIndex                   = 0
	workerPickupStateAbsent                        = false
	workerPickupStatePresent                       = true
	workerPickupNoModeBits                         = 0
	workerPickupStateError                         = "execution: worker pickup has no exact accepted claim, durable lease or private state"
	workerPickupClaimError                         = "execution: worker pickup claim differs from its signed assignment or durable lease"
	workerPickupStateMismatchError                 = "execution: private worker pickup state differs from its exact accepted claim or lease"
	workerPickupStateSizeError                     = "execution: private worker pickup state exceeds its bounded document size"
	workerPickupStateSymlinkError                  = "execution: private worker pickup state is a symlink"
	workerPickupPendingMissingError                = "execution: worker outcome has no confirmed pending branch"
	workerPickupResultMismatchError                = "execution: worker pickup result differs from its retained exact result"
	workerPickupRemoteConflictError                = "execution: worker pickup branch carries another object: %s"
	workerPickupRemoteUnknownError                 = "execution: worker pickup branch %s has no confirmed exact remote tip: %w"
	workerPickupRemoteReadError                    = "execution: reading exact worker pickup branch %s: %w"
	workerPickupStateReadError                     = "execution: reading private worker pickup state: %w"
	workerPickupStateWriteError                    = "execution: saving private worker pickup state: %w"
	workerPickupCommitError                        = "execution: writing signed worker pickup: %w"
	workerPickupOutcomeCommitError                 = "execution: writing signed worker pickup outcome: %w"
)

// workerPickupState is the private, bounded intent written before any remote
// create. The same OIDs are retried after an unknown push or a process restart.
// It contains no command output or resolved environment value.
type workerPickupState struct {
	LeaseID              string `json:"leaseId"`
	PickupID             string `json:"pickupId"`
	Run                  string `json:"run"`
	Task                 string `json:"task"`
	Attempt              int    `json:"attempt"`
	Node                 string `json:"node"`
	ClaimOID             string `json:"claimOid"`
	AssignmentOID        string `json:"assignmentOid"`
	PendingBranch        string `json:"pendingBranch"`
	PendingOID           string `json:"pendingOid"`
	OutcomeBranch        string `json:"outcomeBranch,omitempty"`
	ResultOID            string `json:"resultOid,omitempty"`
	ResultDocumentDigest string `json:"resultDocumentDigest,omitempty"`
}

type pickupOutcomeRequest struct {
	Task    *claimedTask
	Report  Result
	Carried []gitx.TreeEntry
}

type workerPickupRef struct {
	Branch string
	OID    string
}

func (w *Worker) pickupStatePath(leaseID string) string {
	digest := sha256.Sum256([]byte(leaseID))
	return filepath.Join(w.StateDir, workerPickupDirectoryName,
		hex.EncodeToString(digest[:])+workerPickupFileSuffix)
}

func (w *Worker) readPickupState(leaseID string) (workerPickupState, bool, error) {
	path := w.pickupStatePath(leaseID)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return workerPickupState{}, workerPickupStateAbsent, nil
	}
	if err != nil {
		return workerPickupState{}, workerPickupStateAbsent, fmt.Errorf(workerPickupStateReadError, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != workerPickupNoModeBits {
		return workerPickupState{}, workerPickupStateAbsent, fmt.Errorf(workerPickupStateSymlinkError)
	}
	if info.Size() > workerPickupStateMaximumBytes {
		return workerPickupState{}, workerPickupStateAbsent, fmt.Errorf(workerPickupStateSizeError)
	}
	document, err := os.ReadFile(path)
	if err != nil {
		return workerPickupState{}, workerPickupStateAbsent, fmt.Errorf(workerPickupStateReadError, err)
	}
	if len(document) > workerPickupStateMaximumBytes {
		return workerPickupState{}, workerPickupStateAbsent, fmt.Errorf(workerPickupStateSizeError)
	}
	var state workerPickupState
	if err := strictJSON(document, &state); err != nil {
		return workerPickupState{}, workerPickupStateAbsent, fmt.Errorf(workerPickupStateReadError, err)
	}
	return state, workerPickupStatePresent, nil
}

func (w *Worker) writePickupState(state workerPickupState) error {
	path := w.pickupStatePath(state.LeaseID)
	if err := os.MkdirAll(filepath.Dir(path), workerPickupDirectoryMode); err != nil {
		return fmt.Errorf(workerPickupStateWriteError, err)
	}
	document, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf(workerPickupStateWriteError, err)
	}
	if len(document) > workerPickupStateMaximumBytes {
		return fmt.Errorf(workerPickupStateSizeError)
	}
	if err := fsx.WriteFileAtomic(path, document, workerPickupFileMode); err != nil {
		return fmt.Errorf(workerPickupStateWriteError, err)
	}
	return nil
}

func (w *Worker) pickupClaim(ctx context.Context, task *claimedTask) (ChoreographyClaimProposal, error) {
	mailbox, ok := w.Mailbox.(*GitMailbox)
	if !ok || w.Cache == nil || w.ReturnSigner == nil || w.StateDir == workerPickupNoIdentity ||
		w.Endpoint == workerPickupNoIdentity || task == nil ||
		task.claimed == workerPickupNoIdentity || task.tip.OID == workerPickupNoIdentity ||
		task.slotLease.ID == workerPickupNoIdentity ||
		task.slotLease.Intent.PickupID == workerPickupNoIdentity ||
		task.assignment.GraphRootOID == workerPickupNoIdentity {
		return ChoreographyClaimProposal{}, fmt.Errorf(workerPickupStateError)
	}
	tree, err := mailbox.readTree(ctx, task.claimed)
	if err != nil {
		return ChoreographyClaimProposal{}, err
	}
	if tree.kind != choreographyClaimKind {
		return ChoreographyClaimProposal{}, fmt.Errorf(workerPickupClaimError)
	}
	document, err := mailbox.Read(ctx, ChainTip{OID: task.claimed, Kind: tree.kind,
		document: tree.document, signature: tree.signature}, w.Report.Limits.MaxManifestBytes)
	if err != nil {
		return ChoreographyClaimProposal{}, err
	}
	var claim ChoreographyClaimProposal
	if err := strictJSON(document, &claim); err != nil {
		return ChoreographyClaimProposal{}, err
	}
	parents, err := mailbox.plumbing.CommitParents(ctx, task.claimed)
	if err != nil {
		return ChoreographyClaimProposal{}, err
	}
	lease := task.slotLease
	assignment := task.assignment
	if len(parents) != workerPickupClaimParentCount || parents[workerPickupClaimParentIndex] != task.tip.OID ||
		claim.Run != assignment.Run || claim.Generation != assignment.Generation ||
		claim.PlanDigest != assignment.PlanDigest || claim.GraphDigest != assignment.GraphDigest ||
		claim.GraphRootOID != assignment.GraphRootOID || claim.Task != assignment.Task ||
		claim.Attempt != assignment.Attempt || claim.Node != w.Node ||
		claim.Branch != assignment.Branch || claim.PickupID != lease.Intent.PickupID ||
		claim.LeaseIntentID != lease.ID || claim.LeaseWeight != lease.Intent.Weight ||
		claim.CapacityAuthorityID != w.Report.AuthorityID ||
		claim.MembershipEpochOID == workerPickupNoIdentity ||
		claim.InputsDigest != assignment.InputsDigest ||
		lease.Intent.Run != claim.Run || lease.Intent.Task != claim.Task ||
		lease.Intent.Attempt != claim.Attempt {
		return ChoreographyClaimProposal{}, fmt.Errorf(workerPickupClaimError)
	}
	return claim, nil
}

func (w *Worker) ensurePickupPending(ctx context.Context, task *claimedTask) (workerPickupState, error) {
	claim, err := w.pickupClaim(ctx, task)
	if err != nil {
		return workerPickupState{}, err
	}
	identity := ChoreographyPickupIdentity{Run: claim.Run, Task: claim.Task,
		Node: claim.Node, PickupID: claim.PickupID}
	branch, err := FormatChoreographyPendingBranch(identity)
	if err != nil {
		return workerPickupState{}, err
	}
	state, found, err := w.readPickupState(task.slotLease.ID)
	if err != nil {
		return workerPickupState{}, err
	}
	if found {
		if state.LeaseID != task.slotLease.ID || state.PickupID != claim.PickupID ||
			state.Run != claim.Run || state.Task != claim.Task || state.Attempt != claim.Attempt ||
			state.Node != claim.Node || state.ClaimOID != task.claimed ||
			state.AssignmentOID != task.tip.OID || state.PendingBranch != branch ||
			state.PendingOID == workerPickupNoIdentity {
			return workerPickupState{}, fmt.Errorf(workerPickupStateMismatchError)
		}
	} else {
		pending := Pending{Header: Header{Protocol: ChoreographyProtocolVersion,
			Kind: task.assignment.Kind, Run: claim.Run, Generation: claim.Generation,
			PlanDigest: claim.PlanDigest, Task: claim.Task, Attempt: claim.Attempt,
			Node: claim.Node, Branch: branch, IssuedAt: time.Now().UTC().Format(time.RFC3339Nano)},
			TaskBranch: task.assignment.Branch, PickupID: claim.PickupID,
			ClaimOID: task.claimed, AssignmentOID: task.tip.OID,
			GraphDigest: claim.GraphDigest, GraphRootOID: claim.GraphRootOID,
			InputsDigest: claim.InputsDigest, MembershipEpochOID: claim.MembershipEpochOID,
			CapacityAuthorityID: claim.CapacityAuthorityID,
			LeaseIntentID:       claim.LeaseIntentID, LeaseWeight: claim.LeaseWeight}
		document, err := json.Marshal(pending)
		if err != nil {
			return workerPickupState{}, fmt.Errorf(workerPickupCommitError, err)
		}
		oid, err := formatMessageCommit(ctx, w.Cache, w.ReturnSigner,
			MessagePending, document, []string{task.claimed}, nil)
		if err != nil {
			return workerPickupState{}, fmt.Errorf(workerPickupCommitError, err)
		}
		state = workerPickupState{LeaseID: task.slotLease.ID, PickupID: claim.PickupID,
			Run: claim.Run, Task: claim.Task, Attempt: claim.Attempt, Node: claim.Node,
			ClaimOID: task.claimed, AssignmentOID: task.tip.OID,
			PendingBranch: branch, PendingOID: oid}
		if err := w.writePickupState(state); err != nil {
			return workerPickupState{}, err
		}
	}
	if err := w.pushPickupRef(ctx, workerPickupRef{Branch: state.PendingBranch,
		OID: state.PendingOID}); err != nil {
		return workerPickupState{}, err
	}
	return state, nil
}

func (w *Worker) publishPickupOutcome(ctx context.Context,
	request pickupOutcomeRequest) (Result, string, error) {
	task := request.Task
	if task == nil || task.slotLease.ID == workerPickupNoIdentity {
		return Result{}, workerPickupNoIdentity, fmt.Errorf(workerPickupStateError)
	}
	state, found, err := w.readPickupState(task.slotLease.ID)
	if err != nil {
		return Result{}, workerPickupNoIdentity, err
	}
	if !found || state.ClaimOID != task.claimed || state.AssignmentOID != task.tip.OID ||
		state.PickupID != task.slotLease.Intent.PickupID ||
		state.PendingOID == workerPickupNoIdentity {
		return Result{}, workerPickupNoIdentity, fmt.Errorf(workerPickupPendingMissingError)
	}
	pendingTip, err := w.pickupRemoteTip(ctx, state.PendingBranch)
	if err != nil {
		return Result{}, workerPickupNoIdentity, err
	}
	if pendingTip != state.PendingOID {
		return Result{}, workerPickupNoIdentity, fmt.Errorf(workerPickupPendingMissingError)
	}
	result := request.Report
	identity := ChoreographyPickupIdentity{Run: state.Run, Task: state.Task,
		Node: state.Node, PickupID: state.PickupID}
	outcomeBranch, err := FormatChoreographyOutcomeBranch(identity, result.Status)
	if err != nil {
		return Result{}, workerPickupNoIdentity, err
	}
	result.PickupID, result.PendingBranch, result.PendingOID =
		state.PickupID, state.PendingBranch, state.PendingOID
	result.OutcomeBranch = outcomeBranch
	if result.Protocol != ChoreographyProtocolVersion || result.Kind != task.assignment.Kind ||
		result.Run != state.Run || result.Generation != task.assignment.Generation ||
		result.PlanDigest != task.assignment.PlanDigest || result.Task != state.Task ||
		result.Attempt != state.Attempt || result.Node != state.Node ||
		result.Branch != task.assignment.Branch ||
		result.Assignment != state.ClaimOID {
		return Result{}, workerPickupNoIdentity, fmt.Errorf(workerPickupResultMismatchError)
	}
	document, err := json.Marshal(result)
	if err != nil {
		return Result{}, workerPickupNoIdentity, fmt.Errorf(workerPickupOutcomeCommitError, err)
	}
	digest := sha256.Sum256(document)
	documentDigest := hex.EncodeToString(digest[:])
	if state.ResultOID != workerPickupNoIdentity {
		if state.OutcomeBranch != outcomeBranch || state.ResultDocumentDigest != documentDigest {
			return Result{}, workerPickupNoIdentity, fmt.Errorf(workerPickupResultMismatchError)
		}
	} else {
		oid, err := formatMessageCommit(ctx, w.Cache, w.ReturnSigner,
			MessageResult, document, []string{state.PendingOID}, request.Carried)
		if err != nil {
			return Result{}, workerPickupNoIdentity, fmt.Errorf(workerPickupOutcomeCommitError, err)
		}
		state.ResultOID, state.OutcomeBranch, state.ResultDocumentDigest =
			oid, outcomeBranch, documentDigest
		if err := w.writePickupState(state); err != nil {
			return Result{}, workerPickupNoIdentity, err
		}
	}
	if err := w.pushPickupRef(ctx, workerPickupRef{Branch: state.OutcomeBranch,
		OID: state.ResultOID}); err != nil {
		return Result{}, workerPickupNoIdentity, err
	}
	return result, state.ResultOID, nil
}

// pickupRemoteTip does not infer absence from a transport error. An uncertain
// push keeps its physical lease until the exact branch can be read again.
func (w *Worker) pickupRemoteTip(ctx context.Context, branch string) (string, error) {
	heads, err := w.Cache.ListRemoteHeadsExact(ctx, w.Endpoint, []string{branch})
	if err != nil {
		return workerPickupNoIdentity, fmt.Errorf(workerPickupRemoteReadError, branch, err)
	}
	if len(heads) == workerPickupNoRemoteHeads {
		return workerPickupNoIdentity, nil
	}
	if len(heads) != workerPickupOneRemoteHead || heads[workerPickupFirstRemoteHeadIndex].Name != branch {
		return workerPickupNoIdentity, fmt.Errorf(workerPickupRemoteConflictError, branch)
	}
	return heads[workerPickupFirstRemoteHeadIndex].OID, nil
}

func (w *Worker) pushPickupRef(ctx context.Context, ref workerPickupRef) error {
	for attempt := workerPickupFirstCreateAttempt; attempt < workerPickupCreateAttempts; attempt++ {
		remoteTip, err := w.pickupRemoteTip(ctx, ref.Branch)
		if err != nil {
			return err
		}
		if remoteTip == ref.OID {
			return nil
		}
		if remoteTip != workerPickupNoIdentity {
			return fmt.Errorf(workerPickupRemoteConflictError, ref.Branch)
		}
		pushErr := w.Cache.PushCreate(ctx, w.Endpoint, ref.OID, ref.Branch)
		if pushErr == nil {
			return nil
		}
		readCtx, done := context.WithTimeout(context.WithoutCancel(ctx), taskReportTimeout)
		remoteTip, readErr := w.pickupRemoteTip(readCtx, ref.Branch)
		done()
		if readErr != nil {
			return fmt.Errorf(workerPickupRemoteUnknownError, ref.Branch,
				errors.Join(pushErr, readErr))
		}
		if remoteTip == ref.OID {
			return nil
		}
		if remoteTip != workerPickupNoIdentity {
			return fmt.Errorf(workerPickupRemoteConflictError, ref.Branch)
		}
		if attempt+workerPickupCreateAttemptIncrement == workerPickupCreateAttempts || ctx.Err() != nil {
			return fmt.Errorf(workerPickupRemoteUnknownError, ref.Branch, pushErr)
		}
	}
	return fmt.Errorf(workerPickupStateError)
}
