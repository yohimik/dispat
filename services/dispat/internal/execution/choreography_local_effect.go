// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

const choreographyDeferredFinalizeRecordingMode = "deferred-finalize"

// ChoreographyLocalEffectEvidence records what the first node actually
// observed for a first-node publish or record vertex. A publisher command's
// completion is an observation under the existing release contract, not a
// claim that an external registry transaction is atomic. Git records name
// their exact objects when the configured recording path creates them.
type ChoreographyLocalEffectEvidence struct {
	Operation      string `json:"operation"`
	Repository     string `json:"repository,omitempty"`
	Package        string `json:"package"`
	Version        string `json:"version"`
	Tag            string `json:"tag"`
	Attempt        int    `json:"attempt"`
	PlanDigest     string `json:"planDigest"`
	RecordingMode  string `json:"recordingMode"`
	Observation    string `json:"observation"`
	ReceiptObject  string `json:"receiptObject,omitempty"`
	ReceiptCommit  string `json:"receiptCommit,omitempty"`
	PreparedHead   string `json:"preparedHead,omitempty"`
	PreparedTarget string `json:"preparedTarget,omitempty"`
	TagObject      string `json:"tagObject,omitempty"`
	TagTarget      string `json:"tagTarget,omitempty"`
}

// DigestChoreographyLocalEffect gives the exact typed evidence its stable
// digest. The full evidence is carried in the signed local-outcome commit;
// callers must resolve the named native objects before making the assertion.
func DigestChoreographyLocalEffect(e ChoreographyLocalEffectEvidence) (string, error) {
	if e.Operation == choreographyLocalAbsentIdentityText || e.Package == choreographyLocalAbsentIdentityText || e.Version == choreographyLocalAbsentIdentityText || e.Tag == choreographyLocalAbsentIdentityText ||
		e.Attempt != choreographyLocalFirstAttemptCount || e.PlanDigest == choreographyLocalAbsentIdentityText ||
		(e.Observation != choreographyLocalSucceededObservation && e.Observation != choreographyLocalFailedObservation) ||
		(e.RecordingMode != choreographyLocalCommitPushMode && e.RecordingMode != choreographyLocalTagPushMode &&
			e.RecordingMode != choreographyLocalOnlyMode && e.RecordingMode != choreographyLocalCommandOnlyMode &&
			e.RecordingMode != choreographyDeferredFinalizeRecordingMode) {
		return choreographyLocalAbsentIdentityText, fmt.Errorf(choreographyLocalEffectIncompleteErrorText)
	}
	for _, oid := range []string{e.ReceiptObject, e.ReceiptCommit, e.PreparedHead,
		e.PreparedTarget, e.TagObject, e.TagTarget} {
		if oid != choreographyLocalAbsentIdentityText && !validSlotHex(oid, choreographyLocalSHA1HexCharacters) && !validSlotHex(oid, choreographyLocalSHA256HexCharacters) {
			return choreographyLocalAbsentIdentityText, fmt.Errorf(choreographyLocalEffectGitObjectMalformedErrorText)
		}
	}
	if (e.ReceiptObject == choreographyLocalAbsentIdentityText) != (e.ReceiptCommit == choreographyLocalAbsentIdentityText) ||
		(e.PreparedHead == choreographyLocalAbsentIdentityText) != (e.PreparedTarget == choreographyLocalAbsentIdentityText) ||
		(e.TagObject == choreographyLocalAbsentIdentityText) != (e.TagTarget == choreographyLocalAbsentIdentityText) {
		return choreographyLocalAbsentIdentityText, fmt.Errorf(choreographyLocalEffectNativeRecordIncompleteErrorText)
	}
	if e.PreparedHead != choreographyLocalAbsentIdentityText && e.ReceiptObject == choreographyLocalAbsentIdentityText {
		return choreographyLocalAbsentIdentityText, fmt.Errorf(choreographyLocalPreparedReceiptMissingErrorText)
	}
	encoded, err := json.Marshal(e)
	if err != nil {
		return choreographyLocalAbsentIdentityText, err
	}
	if len(encoded) > choreographyLocalMaximumEvidenceBytes {
		return choreographyLocalAbsentIdentityText, fmt.Errorf(choreographyLocalEffectLimitErrorText)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

// verifyLocalOutcome binds a retained ledger terminal to the complete signed
// effect record before replay exposes it to a cold first node or peer.
func (g *GitChoreographyPersist) verifyLocalOutcome(ctx context.Context,
	event ChoreographyEvent) error {
	return verifyChoreographyLocalOutcome(ctx, choreographyLocalOutcomeProof{
		Mailbox: g.opts.Mailbox, Binding: g.opts.Binding,
		Event: event, Task: g.localTasks[event.Task]})
}

// ReadChoreographyLocalOutcome opens a retained terminal's exact signed
// first-node effect evidence. The caller must still compare its named native
// Git objects with live remote state before retiring a release lock.
func (mailbox *GitMailbox) ReadChoreographyLocalOutcome(ctx context.Context,
	binding ChoreographyRunBinding, event ChoreographyEvent,
	spec ChoreographyTaskSpec) (ChoreographyLocalOutcome, error) {
	if mailbox == nil || event.Kind != ChoreographyTerminal ||
		!spec.LocalAuthority || spec.Task != event.Task || spec.Package != event.Package {
		return ChoreographyLocalOutcome{}, fmt.Errorf(choreographyLocalEffectQueryNotTerminalErrorText)
	}
	if err := verifyChoreographyLocalOutcome(ctx, choreographyLocalOutcomeProof{
		Mailbox: mailbox, Binding: binding, Event: event, Task: spec}); err != nil {
		return ChoreographyLocalOutcome{}, err
	}
	tree, err := mailbox.readTree(ctx, event.TaskOID)
	if err != nil {
		return ChoreographyLocalOutcome{}, err
	}
	document, err := mailbox.Read(ctx, ChainTip{OID: event.TaskOID, Kind: tree.kind,
		document: tree.document, signature: tree.signature}, returnReplyLimit)
	if err != nil {
		return ChoreographyLocalOutcome{}, err
	}
	var outcome ChoreographyLocalOutcome
	if err := strictJSON(document, &outcome); err != nil {
		return ChoreographyLocalOutcome{}, err
	}
	return outcome, nil
}

type choreographyLocalOutcomeProof struct {
	Mailbox *GitMailbox
	Binding ChoreographyRunBinding
	Event   ChoreographyEvent
	Task    ChoreographyTaskSpec
}

func verifyChoreographyLocalOutcome(ctx context.Context,
	proof choreographyLocalOutcomeProof) error {
	mailbox, binding, event, spec := proof.Mailbox, proof.Binding, proof.Event, proof.Task
	tree, err := mailbox.readTree(ctx, event.TaskOID)
	if err != nil || tree.kind != choreographyLocalOutcomeKind {
		return fmt.Errorf(choreographyLocalEffectObjectUnsignedErrorText)
	}
	parents, err := mailbox.plumbing.CommitParents(ctx, event.TaskOID)
	if err != nil || len(parents) != choreographyLocalEffectParentCount || parents[choreographyLocalFirstParentIndex] != event.PriorTaskOID {
		return fmt.Errorf(choreographyLocalEffectParentsUnexpectedErrorText)
	}
	document, err := mailbox.Read(ctx, ChainTip{OID: event.TaskOID, Kind: tree.kind,
		document: tree.document, signature: tree.signature}, returnReplyLimit)
	if err != nil {
		return err
	}
	var outcome ChoreographyLocalOutcome
	if err := strictJSON(document, &outcome); err != nil ||
		outcome.ChoreographyRunBinding != binding || outcome.Task != event.Task ||
		outcome.Node != event.Node || outcome.ClaimOID != event.PriorTaskOID ||
		outcome.Succeeded != event.Succeeded ||
		outcome.PackageOutcome != event.PackageOutcome {
		return fmt.Errorf(choreographyLocalEffectTerminalMismatchErrorText)
	}
	return validateChoreographyLocalEffect(outcome, spec)
}

func validateChoreographyLocalEffect(outcome ChoreographyLocalOutcome,
	spec ChoreographyTaskSpec) error {
	e := outcome.EffectEvidence
	if e.Operation != spec.Kind || e.Package != spec.Package ||
		e.PlanDigest != outcome.PlanDigest ||
		(e.Observation == choreographyLocalSucceededObservation) != outcome.Succeeded {
		return fmt.Errorf(choreographyLocalEffectTaskOutcomeMismatchErrorText)
	}
	if e.RecordingMode == choreographyLocalCommandOnlyMode && e.Operation != choreographyLocalPublishOperation {
		return fmt.Errorf(choreographyLocalRecordCommandEvidenceInvalidErrorText)
	}
	if e.RecordingMode == choreographyDeferredFinalizeRecordingMode &&
		e.Operation != choreographyLocalPublishOperation && e.Operation != choreographyLocalRecordOperation {
		return fmt.Errorf(choreographyLocalDeferredTaskInvalidErrorText)
	}
	digest, err := DigestChoreographyLocalEffect(e)
	if err != nil {
		return err
	}
	if digest != outcome.EffectDigest {
		return fmt.Errorf(choreographyLocalEffectDigestMismatchErrorText)
	}
	if outcome.Succeeded &&
		e.RecordingMode != choreographyLocalOnlyMode && e.RecordingMode != choreographyLocalCommandOnlyMode &&
		e.RecordingMode != choreographyDeferredFinalizeRecordingMode &&
		e.ReceiptObject == choreographyLocalAbsentIdentityText && e.TagObject == choreographyLocalAbsentIdentityText {
		return fmt.Errorf(choreographyLocalPushedRecordMissingErrorText)
	}
	return nil
}
