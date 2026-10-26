// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

const (
	workerReturnNoIdentity                     = ""
	workerReturnNoReason                       = ""
	workerReturnNoCapacityScope                = ""
	workerReturnMinimumWeight                  = 1
	workerReturnNoEntries                      = 0
	workerReturnFirstHeldLeaseIndex            = 0
	workerReturnFirstEventSequence      uint64 = 1
	workerReturnNoLedgerSequence        uint64 = 0
	workerReturnEventPageSize                  = 32
	workerReturnNoPackBudget                   = 0
	workerReturnNoBytes                        = 0
	workerReturnReserved                       = true
	workerReturnUnreserved                     = false
	workerReturnParked                         = true
	workerReturnNoProgress                     = false
	workerReturnProgress                       = true
	workerReturnPageValid                      = true
	workerReturnPageInvalid                    = false
	workerReturnClaimSettled                   = true
	workerReturnClaimUnsettled                 = false
	workerReturnTaskStarted                    = true
	workerReturnTaskNotStarted                 = false
	workerReturnUnconfirmedCommandStart        = false
	workerReturnSlotSettled                    = true
	workerReturnFinalityProbeOnly              = true
	workerReturnLegacyNoSlot                   = true
	workerReturnSlotUnavailable                = false
	workerReturnParkedNotWoken                 = false
	workerReturnParkedWoken                    = true
	workerReturnReceiptUnverified              = false
	workerReturnReceiptVerified                = true
	workerReturnPendingNotHandled              = false
	workerReturnPendingHandled                 = true
	workerReturnClaimNotAnswered               = false
	workerReturnClaimAnswered                  = true
	workerReturnClaimStateUnknown              = false
	workerReturnClaimStateHandled              = true
	workerReturnProposalDigestFormat           = "%s\x00%s\x00%s\x00%d\x00%s"
	workerReturnProposalPartSeparator          = "\x00"
	workerReturnResultPackFilePattern          = "dispat-result-pack-*"

	workerReturnLogRunField                = "run"
	workerReturnLogBranchField             = "branch"
	workerReturnLogTaskField               = "task"
	workerReturnParkedAwaitMessage         = "parked claim awaits signed capacity events"
	workerReturnParkedPageErrorMessage     = "parked claim received a noncontiguous event page"
	workerReturnParkedSequenceErrorMessage = "parked claim received an event sequence gap"
	workerReturnPendingChannelErrorMessage = "pending claim has no pinned return channel"
	workerReturnPendingUnsettledMessage    = "exact claim remains unsettled"
	workerReturnRefusalSlotErrorMessage    = "signed refusal did not settle local node capacity"
	workerReturnClaimAwaitTaskMessage      = "accepted claim is awaiting exact task state"
	workerReturnClaimStartErrorMessage     = "persisted claim could not start locally"
	workerReturnAwaitFinalityMessage       = "awaiting signed task finality before freeing local capacity"

	workerReturnWithdrawnError               = "execution: exact cancellation superseded the task result"
	workerReturnAuthorityError               = "execution: v3 task has no shared weighted node capacity authority"
	workerReturnRecoveryIdentityError        = "execution: v3 task has no exact retained Git recovery identity"
	workerReturnLostAuthorityError           = "execution: node slot authority was lost before settlement"
	workerReturnClaimLookupErrorFormat       = "execution: claim reply and receipt lookup are unsettled: %w"
	workerReturnClaimUnsettledErrorFormat    = "execution: claim reply is unsettled: %w"
	workerReturnClaimIdentityErrorFormat     = "execution: first-node claim receipt differs from exact proposal: %s"
	workerReturnCapacityResourceError        = "execution: capacity refusal names no exact resource"
	workerReturnCapacityScopeError           = "execution: non-capacity refusal names a capacity scope"
	workerReturnRefusalReasonError           = "execution: unknown signed refusal reason"
	workerReturnWithdrawalErrorFormat        = "execution: withdrawal of pending claim failed verification: %s"
	workerReturnPackUnavailableError         = "execution: signed result pack has no bounded first-node return"
	workerReturnEncodeResultErrorFormat      = "execution: encoding task result: %w"
	workerReturnOpenPackErrorFormat          = "execution: opening bounded result pack: %w"
	workerReturnResultUnsettledErrorFormat   = "execution: result remains unsettled: %w"
	workerReturnResultReceiptError           = "execution: first-node result receipt differs from exact proposal"
	workerReturnCancelIdentityError          = "execution: cancellation has no authenticated return identity"
	workerReturnAckUnsettledErrorFormat      = "execution: acknowledgement remains unsettled: %w"
	workerReturnAckReceiptError              = "execution: first-node acknowledgement receipt differs from exact proposal"
	workerReturnCursorError                  = "execution: accepted return has no signed ledger cursor for capacity settlement"
	workerReturnCapacityUnsettledErrorFormat = "execution: local node capacity remains unsettled: %w"
	workerReturnLocalEventError              = "execution: first-local capacity event is not the next signed task event"
	workerReturnLocalReleaseError            = "execution: first-local capacity release names another accepted effect"
	workerReturnPageError                    = "execution: capacity settlement event page is not contiguous"
	workerReturnSequenceError                = "execution: capacity settlement event has a sequence gap"
	workerReturnReleaseResultError           = "execution: capacity release names a different accepted result"
	workerReturnRetainedDecisionErrorFormat  = "execution: retained capacity finality has no exact signed decision: %w"

	workerReturnClaimMissingOIDReason    = "missing task OID"
	workerReturnClaimWrongRunReason      = "run binding"
	workerReturnClaimWrongIdentityReason = "claim identity"
	workerReturnClaimWrongStateReason    = "acceptance state"
	workerReturnClaimUnadvancedReason    = "accepted claim did not advance the task ref"
	workerReturnClaimFenceReason         = "accepted claim carries a refusal fence"
	workerReturnRefusalTerminalReason    = "refusal also claims terminality"
	workerReturnRefusalWrongOIDReason    = "refusal names another expected task OID"
	workerReturnRefusalNoFenceReason     = "refusal lacks a ledger fence"
	workerReturnRefusalUnadvancedReason  = "refusal did not advance the ledger epoch"
)
