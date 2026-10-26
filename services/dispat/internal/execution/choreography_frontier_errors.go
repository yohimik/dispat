// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

const (
	frontierMissingSignedRootError        = "execution: compiled frontier has no signed complete graph root"
	frontierIncompleteIdentityError       = "execution: incomplete interpreted frontier"
	frontierInvalidNodePolicyError        = "execution: frontier has an invalid admitted node policy"
	frontierInvalidTaskIdentityError      = "execution: invalid interpreted task identity"
	frontierInvalidPredecessorErrorFormat = "execution: invalid interpreted predecessor of %s"
	frontierCycleError                    = "execution: interpreted task graph has a cycle"
	frontierLateOutcomePolicyError        = "execution: outcome policy cannot be attached after interpretation starts"
	frontierReadyMissingRootError         = "execution: compiled frontier lacks its complete graph root"
	frontierReadyInputStateError          = "execution: selected task has no exact ready input state"
	frontierUnsettledPredecessorError     = "execution: selected task has an unsettled predecessor"
	frontierUnsignedPredecessorError      = "execution: selected task lacks signed predecessor finality"
	frontierQuarantinedError              = "execution: interpreted event frontier is quarantined"
	frontierMissingPackagePolicyError     = "execution: interpreted packages have no outcome policy"
	frontierMissingReleasePolicyError     = "execution: interpreted release has no outcome policy"
	frontierUnproposedStateError          = "execution: unproposed compiled task has another frontier state"
	frontierRefusedRequeueError           = "execution: refused compiled task is not eligible for reconsideration"
	frontierInvalidMembershipError        = "execution: frontier membership names another or invalid node"
	frontierNoncontiguousPageError        = "execution: interpreted event page is not contiguous"
	frontierEventSequenceGapError         = "execution: interpreted event has a sequence gap"
	frontierInitializationMismatchError   = "execution: initialization event differs from the complete compiled graph"
	frontierExecutionStartMismatchError   = "execution: execution-start event differs from the complete compiled graph"
	frontierBeforeInitializationError     = "execution: compiled event precedes signed initialization completion"
	frontierMembershipMismatchError       = "execution: membership event differs from the complete compiled graph"
	frontierInvalidRunSealError           = "execution: invalid run-seal event"
	frontierInvalidInputInvalidationError = "execution: invalid signed prepared-input invalidation"
	frontierInputInvalidationTaskError    = "execution: prepared-input invalidation names another task"
	frontierInvalidRunStoppingError       = "execution: invalid run-stopping event"
	frontierUnreadyTaskEventError         = "execution: interpreted event names an unready task"
	frontierUnboundPackageOutcomeError    = "execution: package outcome is not bound to logical finality"
	frontierWrongEventPackageError        = "execution: event names another task's package"
	frontierFrameReadyTipError            = "execution: frame-ready event changes an exact task tip or terminal state"
	frontierOfferTipError                 = "execution: assignment offer differs from the ready task tip"
	frontierQueueFenceError               = "execution: queue fence does not answer the exact unclaimed offer"
	frontierInvalidClaimRefusalError      = "execution: invalid refused claim event"
	frontierClaimStateConflictError       = "execution: interpreted claim event conflicts with task state"
	frontierWrongClaimRootError           = "execution: compiled claim has another exact task root"
	frontierWrongOfferedClaimError        = "execution: claim does not answer the exact signed offer"
	frontierResultWithoutClaimError       = "execution: result or acknowledgement has no active claim"
	frontierCancelWithoutClaimError       = "execution: cancellation does not answer the exact active claim"
	frontierRetryWithoutClaimError        = "execution: retry event has no active claim"
	frontierUnclaimedRetryFenceError      = "execution: unclaimed retry does not fence its exact offer"
	frontierClaimedRetryResultError       = "execution: claimed retry has no accepted result or acknowledgement"
	frontierTerminalWithoutAttemptError   = "execution: terminal event has no settled attempt"
	frontierUnclaimedTerminalFenceError   = "execution: unclaimed terminal does not fence its exact offer"
	frontierSuccessfulTerminalResultError = "execution: successful terminal event lacks result carrier"
	frontierBlockedWithoutCauseError      = "execution: blocked event has no settled no-claim cause"
	frontierNoClaimWithoutCauseError      = "execution: no-claim expiry lacks an inert exact task and placement cause"
	frontierNoClaimSettlementError        = "execution: no-claim package settlement lacks exact deferred terminal"
	frontierTaskPackageSettlementError    = "execution: accepted-result package settlement lacks exact deferred terminal"
	frontierInertCancelStateError         = "execution: inert cancellation lacks an exact stopped task"
	frontierInertCancelSettlementError    = "execution: inert cancellation settlement lacks exact deferred terminal"
	frontierUnknownEventKindError         = "execution: unknown interpreted event kind"
)

const (
	frontierPackageOutcomeSkipped       = "skipped"
	frontierPackageOutcomeFailed        = "failed"
	frontierPackageOutcomeCancelled     = "cancelled"
	frontierPackageOutcomeRecordBlocked = "record-blocked"
)

const (
	frontierFirstStateOrdinal               = 1
	frontierSingleNodePolicy                = 1
	frontierFirstNodePolicyIndex            = 0
	frontierMinimumNodeCapacity             = 0
	frontierFirstAttemptNumber              = 1
	frontierInitialQueueLength              = 0
	frontierNoPredecessors                  = 0
	frontierInitialVisitedCount             = 0
	frontierFirstQueueIndex                 = 0
	frontierQueueAdvanceStep                = 1
	frontierInitialVirtualRootCount         = 0
	frontierInitialCursorSequence    uint64 = 0
	frontierInitialReadyHead                = 0
	frontierInitialInputCount               = 0
	frontierNoSignedEvents                  = 0
	frontierNoAffectedTasks                 = 0
	frontierFirstEventSequence       uint64 = 1
	frontierNoTaskAttempt                   = 0
	frontierFirstAffectedTaskIndex          = 0
	frontierPreviousAffectedTaskStep        = 1
	frontierNoTransferWeight                = 0
	frontierQuarantined                     = true
	frontierInitializationVerified          = true
	frontierExecutionAuthorized             = true
	frontierMemberDraining                  = true
	frontierMemberSelecting                 = false
	frontierRunSealed                       = true
	frontierRunStopped                      = true
	frontierInputInvalidated                = true
	frontierInputCurrent                    = false
	frontierFramePrepared                   = true
	frontierFrameNotPrepared                = false
	frontierNotReadyRefused                 = true
	frontierNotReadyCleared                 = false
	frontierPackageSettled                  = true
	frontierSelectionMiss                   = false
	frontierSelectionHit                    = true
	frontierOutputUnavailable               = false
	frontierOutputAvailable                 = true
	frontierClaimNotFenced                  = false
	frontierNodeIneligible                  = false
	frontierNodeEligible                    = true
	frontierAbsentPreparedInputOID          = ""
	frontierNoPackage                       = ""
	frontierNoPackageOutcome                = ""
	frontierNoGraphRootOID                  = ""
	frontierNoNode                          = ""
	frontierNoTaskID                        = ""
	frontierNoTaskOID                       = ""
	frontierNoResultReceiptOID              = ""
	frontierNoOutputOID                     = ""
	frontierNoAssignmentOID                 = ""
	frontierNoOfferOID                      = ""
	frontierNoInputsDigest                  = ""
	frontierVirtualPredecessorOID           = ""
	frontierNoLedgerOID                     = ""
	frontierNoAdmissionDeadlineUTC          = ""
	frontierNoBlocker                       = ""
	frontierNoProposalDigest                = ""
	frontierNoProposalID                    = ""
	frontierNoRefusalReason                 = ""
	frontierNoBudget                        = ""
)
