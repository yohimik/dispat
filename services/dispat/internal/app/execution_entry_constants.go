// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

const (
	compiledSweepSettlementTimeoutSeconds       = 30
	deferredReleaseSnapshotRequiredState        = true
	deferredReleaseTagUnsettledState            = false
	deferredReleaseUnsettledErrorFormat         = "execution: deferred release records are unsettled before unlock: %w"
	deferredTagIntentsUnreadableErrorFormat     = "execution: protected deferred release tag intents cannot be read before unlock: %w"
	deferredTagObjectUnrecordedLogText          = "deferred release tag object could not be durably recorded"
	deferredTagTargetsUnpreparedLogText         = "deferred release tag targets could not be durably prepared"
	executionChoreographyUnboundState           = false
	executionChoreographyUnretainedState        = false
	executionConfigurationCauseErrorFormat      = "%w"
	executionCoordinatorOpenState               = false
	executionDefaultRemoteName                  = "origin"
	firstNodeCapacityUnavailableErrorFormat     = "first-node shared task capacity cannot be established: %w"
	firstRunRetainedLogText                     = "first-node run identity remains for reconciliation"
	pinnedRepositoryRemoteAbsentErrorFormat     = "execution: pinned remote identity of %q is absent"
	preflightMissingGraphErrorText              = "distributed preflight has no compiled task graph"
	preflightMissingReturnChannelErrorText      = "distributed preflight has no return channel"
	releaseBeforeEffectsRefusalLogText          = "refusing release before task effects"
	releaseCleanupRetainedLogText               = "release cleanup retained its lock and evidence"
	releasePickerUnsettledLogText               = "first-node compiled picker stopped before settlement"
	releasePlanningBytesChangedErrorText        = "execution: source or configuration bytes changed while the release plan was computed"
	returnChannelCloseFailureLogText            = "return channel did not close cleanly"
	sweepCapacityUnavailableLogText             = "package could not take local node capacity"
	sweepDescriptorMismatchErrorText            = "sweep descriptors differ from signed complete logical graph"
	sweepIdentityRetainedLogText                = "first-node sweep identity remains for reconciliation"
	sweepMissingBlockedAuthorityErrorText       = "execution: sweep has no signed blocked-outcome authority"
	sweepMissingReturnChannelErrorText          = "distributed sweep has no return channel"
	sweepPickerUnsettledLogText                 = "first-node compiled sweep picker stopped before settlement"
	sweepPlanningBytesChangedErrorText          = "execution: source or configuration bytes changed while the run plan was computed"
	sweepTaskFailedState                        = true
	sweepTaskSettlementErrorFormat              = "execution: signed sweep task %s cannot settle: %w"
	unsupportedDistributionModeErrorFormat      = "unsupported distribution mode %q (this build supports compilation, interpretation and hybrid)"
	workerDefaultEndpointUnavailableErrorFormat = "a worker link states no endpoint, and the release remote it would reach could not be resolved: %w"
)
