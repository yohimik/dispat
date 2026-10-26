// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

const (
	choreographyStageNoIdentity                    = ""
	choreographyStageNoPackage                     = ""
	choreographyStageSweepPrefix                   = "run:"
	choreographyStageNoLoginCommands               = 0
	choreographyStageNoTimeout                     = 0
	choreographyStageTaskEventBuffer               = 1
	choreographyStageUnresolvedFinality            = true
	choreographyStageResolvedFinality              = false
	choreographyStageOutputProducing               = true
	choreographyStageOfferInterrupted              = true
	choreographyStageOfferActive                   = false
	choreographyStageUnclaimedDecision             = true
	choreographyStageAcceptedResult                = true
	choreographyStageAcceptedAck                   = true
	choreographyStageDecisionSigned                = true
	choreographyStagePackageSettlementComplete     = false
	choreographyStageLocalSlotSettled              = true
	choreographyStageMissingSettlementErrorFormat  = "execution: choreography task %s has no admitted result awaiting settlement"
	choreographyStageLocalSlotPendingErrorFormat   = "execution: first-local choreography task %s has not settled its physical slot: %w"
	choreographyStageLocalSlotFailedErrorFormat    = "execution: first-local choreography task %s did not settle: %w"
	choreographyStageMissingLocalRunnerError       = "execution: first-local choreography frame has no local runner"
	choreographyStageOfferFenceErrorFormat         = "execution: interrupted offer has no durable claim fence: %w"
	choreographyStageOfferHistoryErrorFormat       = "execution: interrupted offer has no verified task history: %w"
	choreographyStageClaimStateMismatchError       = "execution: signed claim and broker state differ after admission stop"
	choreographyStageUnclaimedSlotErrorFormat      = "execution: unclaimed first-local offer has no settled physical slot: %w"
	choreographyStageCancelClaimErrorFormat        = "execution: timed-out claim could not be cancelled: %w"
	choreographyStageCancelAckMissingErrorFormat   = "execution: cancelled task %s has no signed stop acknowledgement: %w"
	choreographyStageCancelClaimMismatchError      = "execution: cancellation has no matching accepted claim"
	choreographyStageEarlierResultErrorFormat      = "execution: choreography task %s has an earlier unsettled result"
	choreographyStageAckWithoutCancelError         = "execution: acknowledgement has no accepted cancellation"
	choreographyStageAckUnreadableError            = "execution: accepted acknowledgement is unreadable"
	choreographyStageAckUnreadableCauseErrorFormat = "execution: accepted acknowledgement is unreadable: %w"
	choreographyStageAckMismatchError              = "execution: acknowledgement does not match cancelled attempt"
	choreographyStageSignedCancelErrorFormat       = "execution: task %s stopped after signed cancellation"
	choreographyStageUnexpectedTerminalErrorFormat = "execution: offered task reached %s before an admitted result"
	choreographyStageRetrySlotErrorFormat          = "execution: first-local retry has no settled physical slot: %w"
)
