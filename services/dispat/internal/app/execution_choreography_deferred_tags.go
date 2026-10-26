// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"context"
	"fmt"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

const (
	deferredTagMissingIdentity                        = ""
	deferredTagSingleRepositoryKey                    = ""
	deferredTagNoProtectedIntents                     = 0
	deferredTagAnnotatedObjectKind                    = "tag"
	deferredTagReleaseMessagePrefix                   = "release "
	deferredTagReleaseMessageSuffix                   = "\n"
	deferredTagHeadReadErrorFormat                    = "execution: deferred release commit head is unavailable: %w"
	deferredTagUnnamedPackageError                    = "execution: deferred release has an unnamed package"
	deferredTagTargetUnavailableErrorFormat           = "execution: deferred release tag %s target is unavailable: %w"
	deferredTagPresenceUnavailableErrorFormat         = "execution: deferred release tag %s presence is unavailable: %w"
	deferredTagMissingReceiptLedgerErrorFormat        = "execution: deferred pushed tag %s has no receipt ledger"
	deferredTagPreparedReceiptMismatchErrorFormat     = "execution: deferred pushed tag %s differs from its prepared receipt"
	deferredTagDuplicatePackageErrorFormat            = "execution: deferred release tag %s names two packages"
	deferredTagMissingIntentErrorFormat               = "execution: deferred release tag %s has no protected target intent"
	deferredTagOutcomeSetMismatchError                = "execution: deferred release tag intent set differs from signed task outcomes"
	deferredTagMissingDestinationError                = "execution: deferred pushed release has no exact prepared destination"
	deferredTagUnsignedTaskErrorFormat                = "execution: deferred release tag %s has no signed successful task"
	deferredTagIncompleteNativeIntentErrorFormat      = "execution: deferred release tag %s has no complete protected native intent"
	deferredTagMissingPushIdentityErrorFormat         = "execution: deferred pushed tag %s has no prepared receipt and branch identity"
	deferredTagRemoteMismatchErrorFormat              = "execution: deferred pushed tag %s differs from exact local object and target: object=%q target=%q remote error=%v target error=%v"
	deferredTagBranchHeadMissingErrorFormat           = "execution: deferred pushed tag %s branch no longer contains prepared release head %s: %w"
	deferredTagUnexpectedLocalPushIdentityErrorFormat = "execution: deferred local tag %s unexpectedly names a pushed receipt or branch"
	deferredTagBranchReadErrorFormat                  = "execution: reading deferred pushed release branch: %w"
	deferredTagBranchMissingError                     = "execution: deferred pushed release has no checked-out branch"
	deferredTagExistingObjectReadErrorFormat          = "execution: reading existing deferred release tag %s object: %w"
	deferredTagExistingTargetReadErrorFormat          = "execution: reading existing deferred release tag %s target: %w"
	deferredTagExistingTargetMismatchErrorFormat      = "execution: existing deferred release tag %s differs from prepared target"
	deferredTagObjectReadErrorFormat                  = "execution: reading deferred release tag %s object: %w"
	deferredTagObjectMissingErrorFormat               = "execution: deferred release tag %s has no exact object"
	deferredTagTargetReadErrorFormat                  = "execution: reading deferred release tag %s target: %w"
	deferredTagTargetMismatchErrorFormat              = "execution: deferred release tag %s target differs from protected intent"
	deferredTagInventoryObjectMismatchErrorFormat     = "execution: deferred release tag %s object differs from protected intent"
	deferredTagInventoryTargetMismatchErrorFormat     = "execution: deferred release tag %s target %q differs from protected target %q"
	deferredTagInventoryTypeReadErrorFormat           = "execution: reading deferred release tag %s object type: %w"
	deferredTagInventoryTypeMismatchErrorFormat       = "execution: deferred release tag %s has no annotated object after pre-tag intent"
	deferredTagInventoryMessageReadErrorFormat        = "execution: reading deferred release tag %s message: %w"
	deferredTagInventoryMessageMismatchErrorFormat    = "execution: deferred release tag %s message differs from its prepared release identity"
	deferredTagReceiptReadErrorFormat                 = "execution: reading deferred pushed tag %s publication receipt: %w"
	deferredTagReceiptOutstandingErrorFormat          = "execution: deferred pushed tag %s still has an outstanding publication receipt %q"
)

// prepareDeferredTagIntents fsyncs the complete target set before finalize
// creates its first tag ref. The protected expectations were written before
// each signed publish/record outcome, so a missing or extra release is a
// failed native settlement rather than a reason to guess a tag target.
func (a *App) prepareDeferredTagIntents(ctx context.Context, rels []*plan.Release) error {
	if a.returnJournal == nil {
		return nil
	}
	head, err := a.git.HeadSHA(ctx)
	if err != nil {
		return fmt.Errorf(deferredTagHeadReadErrorFormat, err)
	}
	intents := make(map[string]firstRunDeferredTag, len(rels))
	branch := deferredTagMissingIdentity
	if a.cfg.Commit.IsPushEnabled() {
		branch, err = a.git.CurrentBranch(ctx)
		if err != nil {
			return fmt.Errorf(deferredTagBranchReadErrorFormat, err)
		}
		if branch == deferredTagMissingIdentity {
			return fmt.Errorf(deferredTagBranchMissingError)
		}
	}
	for _, rel := range rels {
		if rel == nil || rel.Pkg == nil {
			return fmt.Errorf(deferredTagUnnamedPackageError)
		}
		tag := rel.TagName()
		target := rel.ExportedCommit()
		if target == deferredTagMissingIdentity {
			target = head
		}
		target, err = a.git.ResolveCommit(ctx, target)
		if err != nil {
			return fmt.Errorf(deferredTagTargetUnavailableErrorFormat, tag, err)
		}
		intent := firstRunDeferredTag{Tag: tag, Target: target}
		present, err := a.git.TagExists(ctx, tag)
		if err != nil {
			return fmt.Errorf(deferredTagPresenceUnavailableErrorFormat, tag, err)
		}
		if present {
			object, objectErr := a.git.TagObject(ctx, tag)
			currentTarget, targetErr := a.git.ResolveCommit(ctx, tag)
			if objectErr != nil {
				return fmt.Errorf(deferredTagExistingObjectReadErrorFormat, tag, objectErr)
			}
			if targetErr != nil {
				return fmt.Errorf(deferredTagExistingTargetReadErrorFormat, tag, targetErr)
			}
			if object == deferredTagMissingIdentity || currentTarget != target {
				return fmt.Errorf(deferredTagExistingTargetMismatchErrorFormat, tag)
			}
			intent.Object = object
		}
		if a.cfg.Commit.IsPushEnabled() {
			if a.receipts == nil {
				return fmt.Errorf(deferredTagMissingReceiptLedgerErrorFormat, tag)
			}
			receipt, held := a.receipts.findHeld(tag)
			prepared, recorded := a.receipts.preparedFor(tag)
			if !held || !recorded || receipt.Object == deferredTagMissingIdentity || receipt.Commit == deferredTagMissingIdentity ||
				prepared.ReceiptObject != receipt.Object || prepared.TagTarget != target ||
				prepared.BranchHead != head {
				return fmt.Errorf(deferredTagPreparedReceiptMismatchErrorFormat, tag)
			}
			intent.ReceiptObject, intent.ReceiptCommit = receipt.Object, receipt.Commit
			intent.BranchHead = prepared.BranchHead
			intent.Branch = branch
		}
		if _, duplicate := intents[tag]; duplicate {
			return fmt.Errorf(deferredTagDuplicatePackageErrorFormat, tag)
		}
		intents[tag] = intent
	}
	return a.returnJournal.prepareDeferredTags(intents)
}

func (a *App) recordDeferredTagObject(ctx context.Context, rel *plan.Release) error {
	if a.returnJournal == nil {
		return nil
	}
	tag := rel.TagName()
	identity, err := a.returnJournal.read()
	if err != nil {
		return err
	}
	intent, expected := identity.DeferredTags[tag]
	if !expected || intent.Target == deferredTagMissingIdentity {
		return fmt.Errorf(deferredTagMissingIntentErrorFormat, tag)
	}
	object, err := a.git.TagObject(ctx, tag)
	if err != nil {
		return fmt.Errorf(deferredTagObjectReadErrorFormat, tag, err)
	}
	if object == deferredTagMissingIdentity {
		return fmt.Errorf(deferredTagObjectMissingErrorFormat, tag)
	}
	target, err := a.git.ResolveCommit(ctx, tag)
	if err != nil {
		return fmt.Errorf(deferredTagTargetReadErrorFormat, tag, err)
	}
	if target != intent.Target {
		return fmt.Errorf(deferredTagTargetMismatchErrorFormat, tag)
	}
	return a.returnJournal.recordDeferredTagObject(tag, object)
}

// verifyDeferredTagInventory proves every tag in a protected pre-tag intent
// still has its intended target. The caller supplies the complete set from
// authenticated task outcomes on cold replay. An object observed after tag
// creation is exact; without one, only an annotated tag created after the
// pre-tag intent can satisfy the crash cut before the object journal write.
type deferredTagInventoryCheck struct {
	Identity        firstRunIdentity
	Expected        map[string]bool
	Push            bool
	RequireSnapshot bool
}

func (a *App) verifyDeferredTagInventory(ctx context.Context, check deferredTagInventoryCheck) error {
	identity, expected, push, requireSnapshot := check.Identity, check.Expected, check.Push, check.RequireSnapshot
	if expected != nil && len(identity.DeferredTags) != len(expected) {
		return fmt.Errorf(deferredTagOutcomeSetMismatchError)
	}
	if len(identity.DeferredTags) == deferredTagNoProtectedIntents {
		return nil
	}
	remote := deferredTagMissingIdentity
	if push {
		lock, held := identity.PreparedLocks[deferredTagSingleRepositoryKey]
		if !held || lock.Run != identity.Run || lock.Remote == deferredTagMissingIdentity {
			return fmt.Errorf(deferredTagMissingDestinationError)
		}
		remote = lock.Remote
	}
	for tag, intent := range identity.DeferredTags {
		if expected != nil && !expected[tag] {
			return fmt.Errorf(deferredTagUnsignedTaskErrorFormat, tag)
		}
		if intent.Tag != tag || intent.Target == deferredTagMissingIdentity ||
			(requireSnapshot && intent.Object == deferredTagMissingIdentity) {
			return fmt.Errorf(deferredTagIncompleteNativeIntentErrorFormat, tag)
		}
		object, err := a.git.TagObject(ctx, tag)
		if err != nil {
			return fmt.Errorf(deferredTagObjectReadErrorFormat, tag, err)
		}
		if object == deferredTagMissingIdentity || intent.Object != deferredTagMissingIdentity && object != intent.Object {
			return fmt.Errorf(deferredTagInventoryObjectMismatchErrorFormat, tag)
		}
		target, err := a.git.ResolveCommit(ctx, tag)
		if err != nil {
			return fmt.Errorf(deferredTagTargetReadErrorFormat, tag, err)
		}
		if target != intent.Target {
			return fmt.Errorf(deferredTagInventoryTargetMismatchErrorFormat,
				tag, target, intent.Target)
		}
		if intent.Object == deferredTagMissingIdentity {
			kind, kindErr := a.git.ObjectType(ctx, object)
			if kindErr != nil {
				return fmt.Errorf(deferredTagInventoryTypeReadErrorFormat, tag, kindErr)
			}
			if kind != deferredTagAnnotatedObjectKind {
				return fmt.Errorf(deferredTagInventoryTypeMismatchErrorFormat, tag)
			}
			message, messageErr := a.git.TagMessage(ctx, tag)
			if messageErr != nil {
				return fmt.Errorf(deferredTagInventoryMessageReadErrorFormat, tag, messageErr)
			}
			if message != deferredTagReleaseMessagePrefix+tag+deferredTagReleaseMessageSuffix {
				return fmt.Errorf(deferredTagInventoryMessageMismatchErrorFormat, tag)
			}
		}
		if push {
			if intent.ReceiptObject == deferredTagMissingIdentity || intent.ReceiptCommit == deferredTagMissingIdentity ||
				intent.Branch == deferredTagMissingIdentity || intent.BranchHead == deferredTagMissingIdentity {
				return fmt.Errorf(deferredTagMissingPushIdentityErrorFormat, tag)
			}
			remoteObject, remoteErr := a.git.RemoteTagObject(ctx, remote, tag)
			remoteTarget, targetErr := a.git.RemoteTagCommit(ctx, remote, tag)
			if remoteErr != nil || targetErr != nil || remoteObject != object || remoteTarget != intent.Target {
				return fmt.Errorf(deferredTagRemoteMismatchErrorFormat,
					tag, remoteObject, remoteTarget, remoteErr, targetErr)
			}
			receiptObject, receiptErr := a.git.RemoteReceiptObject(ctx, remote, tag)
			if receiptErr != nil {
				return fmt.Errorf(deferredTagReceiptReadErrorFormat, tag, receiptErr)
			}
			if receiptObject != deferredTagMissingIdentity {
				return fmt.Errorf(deferredTagReceiptOutstandingErrorFormat, tag, receiptObject)
			}
			if err := a.git.VerifyRemoteBranchContains(ctx, remote, intent.Branch,
				intent.BranchHead); err != nil {
				return fmt.Errorf(deferredTagBranchHeadMissingErrorFormat,
					tag, intent.BranchHead, err)
			}
		} else if intent.ReceiptObject != deferredTagMissingIdentity || intent.ReceiptCommit != deferredTagMissingIdentity ||
			intent.Branch != deferredTagMissingIdentity || intent.BranchHead != deferredTagMissingIdentity {
			return fmt.Errorf(deferredTagUnexpectedLocalPushIdentityErrorFormat, tag)
		}
	}
	return nil
}
