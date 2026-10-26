// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

const (
	choreographyLocalAbsentIdentityText                    = ""
	choreographyLocalBlockedTaskIncompleteErrorText        = "execution: first-local blocked task is incomplete"
	choreographyLocalBlockedTaskPackageMissingErrorText    = "execution: first-local blocked task has no package"
	choreographyLocalBrokerMissingErrorText                = "execution: first-local task has no attached broker"
	choreographyLocalClaimReceiptInvalidErrorText          = "execution: first-local task claim lacks exact signed receipt"
	choreographyLocalCommandOnlyMode                       = "command-only"
	choreographyLocalCommitPushMode                        = "commit-push"
	choreographyLocalDeferredTaskInvalidErrorText          = "execution: deferred finalization names neither a publish nor a record task"
	choreographyLocalEffectDigestMismatchErrorText         = "execution: first-node effect digest differs from signed evidence"
	choreographyLocalEffectGitObjectMalformedErrorText     = "execution: first-node effect names a malformed Git object"
	choreographyLocalEffectIncompleteErrorText             = "execution: incomplete first-node effect evidence"
	choreographyLocalEffectLimitErrorText                  = "execution: first-node effect evidence exceeds its signed limit"
	choreographyLocalEffectNativeRecordIncompleteErrorText = "execution: first-node effect has an incomplete native record"
	choreographyLocalEffectObjectUnsignedErrorText         = "execution: local authority terminal has no signed effect object"
	choreographyLocalEffectParentCount                     = 1
	choreographyLocalEffectParentsUnexpectedErrorText      = "execution: local authority effect has unexpected parents"
	choreographyLocalEffectQueryNotTerminalErrorText       = "execution: local effect query names no terminal task"
	choreographyLocalEffectTaskOutcomeMismatchErrorText    = "execution: first-node effect differs from the signed task outcome"
	choreographyLocalEffectTerminalMismatchErrorText       = "execution: signed local effect differs from retained terminal"
	choreographyLocalFailedObservation                     = "failed"
	choreographyLocalFirstAttemptCount                     = 1
	choreographyLocalFirstNodeIneligibleErrorText          = "execution: first node is not eligible for this task"
	choreographyLocalFirstParentIndex                      = 0
	choreographyLocalMaximumEvidenceBytes                  = 16 << 10
	choreographyLocalMinimumWeight                         = 1
	choreographyLocalNoLedgerSequence                      = 0
	choreographyLocalNoObjectBytes                         = 0
	choreographyLocalNoPredecessorsCount                   = 0
	choreographyLocalNodeAbsentState                       = false
	choreographyLocalNodePresentState                      = true
	choreographyLocalOnlyMode                              = "local-only"
	choreographyLocalOutputCaptureDisabledState            = false
	choreographyLocalPackBoundMissingErrorText             = "execution: first-local result has no bounded pack ceiling"
	choreographyLocalPlacementUnavailableErrorText         = "execution: first-local placement is unavailable"
	choreographyLocalPreparedReceiptMissingErrorText       = "execution: prepared release has no exact receipt"
	choreographyLocalProposalSeparatorText                 = "\u0000"
	choreographyLocalPublishOperation                      = "publish"
	choreographyLocalPushedRecordMissingErrorText          = "execution: successful pushed release has no exact native record"
	choreographyLocalReceiptChangedErrorText               = "execution: first-local result receipt changed during reconciliation"
	choreographyLocalReceiptNotDurableErrorText            = "execution: first-local result has no exact durable receipt"
	choreographyLocalReceiptReconciliationErrorFormat      = "execution: first-local result receipt could not be reconciled: %w"
	choreographyLocalRecordCommandEvidenceInvalidErrorText = "execution: record task cannot use publish-command-only evidence"
	choreographyLocalRecordOperation                       = "record"
	choreographyLocalResultPackFilePattern                 = "dispat-local-result-*"
	choreographyLocalSHA1HexCharacters                     = 40
	choreographyLocalSHA256HexCharacters                   = 64
	choreographyLocalSettlementChangedErrorText            = "execution: first-local task settlement changed after its first result"
	choreographyLocalSettlementStartedState                = true
	choreographyLocalSucceededObservation                  = "succeeded"
	choreographyLocalTagPushMode                           = "tag-push"
	choreographyLocalTaskNotReadyErrorText                 = "execution: first-local task is not ready"
	choreographyLocalTicketAbsentErrorText                 = "execution: first-local task ticket is absent"
	choreographyLocalTicketFinishedState                   = true
	choreographyLocalUncertainPackChangedErrorText         = "execution: first-local result pack changed after an uncertain return"
	choreographyLocalVertexIneligibleErrorText             = "execution: task is not an eligible first-local vertex"
)
