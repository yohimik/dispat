// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

const (
	firstRunActiveReconciliationErrorFormat          = "execution: first-node run %s remains active for exact reconciliation"
	firstRunCapacityInspectionErrorFormat            = "execution: first-node capacity cannot be inspected before retirement: %w"
	firstRunCapacityInspectionTimeoutSeconds         = 30
	firstRunEmptyListenAddressErrorText              = "execution: first-node return listen address is empty"
	firstRunIdentityAlreadyExistsErrorFormat         = "execution: first-node run %s already has an identity; resume or reconcile it"
	firstRunInvalidListenAddressErrorFormat          = "execution: first-node return listen address is invalid: %w"
	firstRunInvalidReturnIdentityErrorText           = "distributed first-node run has no return endpoint or a repeated identity"
	firstRunMissingGitStoreErrorText                 = "execution: first-node Git store is absent"
	firstRunPrivateGitDirectoryErrorFormat           = "execution: resolving private Git directory for first-node identity: %w"
	firstRunReturnIdentityCreationErrorFormat        = "execution: creating first-node return identity: %w"
	firstRunUnsafeRetirementState                    = false
	firstRunUnsettledTaskCapacityErrorFormat         = "execution: first-node run %s retains task %s capacity without settled evidence"
	retainedReleaseObservedEffectState               = true
	retainedRunAbsentValueText                       = ""
	retainedRunCompilationModeName                   = "compilation"
	retainedRunEventPageSizeCount                    = 32
	retainedRunLastEventOffsetCount                  = 1
	retainedRunLocalRecordingModeName                = "local-only"
	retainedRunLockDeleteTimeoutSeconds              = 18
	retainedRunLockReadTimeoutSeconds                = 10
	retainedRunNoEntriesCount                        = 0
	retainedRunParentDirectoryName                   = ".."
	retainedRunPublishStageName                      = "publish"
	retainedRunRecordStageName                       = "record"
	retainedSweepAbortedSealState                    = true
	retainedSweepEventReadErrorFormat                = "execution: retained sweep events cannot be read: %w"
	retainedSweepEventSequenceGapErrorText           = "execution: retained sweep event sequence has a gap"
	retainedSweepInadmissibleResultErrorFormat       = "execution: retained sweep result %s is not admissible: %w"
	retainedSweepNoProgressState                     = false
	retainedSweepPollIntervalMilliseconds            = 250
	retainedSweepProgressState                       = true
	retainedSweepReplyWithoutClaimErrorFormat        = "execution: retained sweep task %s has a reply without a signed claim"
	retainedSweepResultFinalityErrorFormat           = "execution: retained sweep result %s cannot reach finality: %w"
	retainedSweepSealDuringReconciliationErrorText   = "execution: retained sweep was sealed during reconciliation"
	retainedSweepSealedState                         = true
	retainedSweepSettlementTimeoutSeconds            = 20
	retainedSweepTaskCancellationErrorFormat         = "execution: retained sweep task %s cannot be cancelled: %w"
	retainedSweepUnsealedState                       = false
	retainedSweepUnsuccessfulState                   = false
	retainedSweepUnverifiedAcceptedStopErrorText     = "execution: retained sweep has an accepted attempt without verified stop"
	retainedSweepUnverifiedTaskStopErrorFormat       = "execution: retained sweep task %s has no verified stop"
	sharedNodeSlotConfigurationPathErrorFormat       = "execution: locating persistent node state: %w"
	sharedNodeSlotDirectoryName                      = "node-state"
	sharedNodeSlotInitialAttemptCount                = 1
	sharedNodeSlotInvalidTaskWeightErrorFormat       = "execution: local task %s requires weight %d outside node capacity %d"
	sharedNodeSlotLocalCapacityErrorFormat           = "execution: explicit local node capacity cannot be established: %w"
	sharedNodeSlotMissingTaskIdentityErrorText       = "execution: local release capacity has no compiled task identity"
	sharedNodeSlotNoCapacityCount                    = 0
	sharedNodeSlotNoExecutableTaskErrorText          = "execution: local release has no executable task"
	sharedNodeSlotPollIntervalMilliseconds           = 100
	sharedNodeSlotRelativeConfigurationPathErrorText = "execution: user config directory is not absolute"
	sharedNodeSlotTaskAbsentFromGraphErrorFormat     = "execution: local release task %s is absent from the compiled graph"
	sharedNodeSlotUnavailableCapacityErrorFormat     = "execution: local node capacity did not become available: %w"
	sharedNodeSlotUnconfiguredWaitErrorText          = "execution: local node slot wait is not configured"
)
