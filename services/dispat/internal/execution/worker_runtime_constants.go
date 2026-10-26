// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

const (
	workerRuntimeNoIdentity                   = ""
	workerRuntimeNoActiveWork                 = 0
	workerRuntimeMinimumSlotCapacity          = 1
	workerRuntimeFirstHeldLeaseIndex          = 0
	workerRuntimeProbeEventPageSize           = 1
	workerRuntimeNoProgress                   = false
	workerRuntimeTaskNotClaimed               = false
	workerRuntimeCompiledClaimPathError       = "execution: compiled tasks require a graph-bound pickup claim"
	workerRuntimeTaskClaimed                  = true
	workerRuntimeNoWithdrawal                 = false
	workerRuntimeBranchLogField               = "branch"
	workerRuntimeCommitLogField               = "commit"
	workerRuntimeStatusLogField               = "status"
	workerRuntimeCompilationMode              = bootstrapCompilationModeName
	workerRuntimeRecoverSlotsMessage          = "unsettled node slots retain capacity until signed Git finality is readable"
	workerRuntimeDrainErrorMessage            = "worker could not settle its signed membership drain"
	workerRuntimeClaimUnsettledMessage        = "claim or task ref is unsettled; retaining capacity and exact proposal"
	workerRuntimePreflightDoneMessage         = "preflight returned"
	workerRuntimeReturnErrorFormat            = "execution: task lacks its authenticated first-node return: %w"
	workerRuntimeReturnSignerError            = "execution: task lacks its authenticated first-node return signer"
	workerRuntimeCapacityErrorFormat          = "execution: signed capacity epoch is unavailable: %w"
	workerRuntimeEmptyCapacityError           = "execution: signed capacity epoch is empty"
	workerRuntimeProbeSignerError             = "execution: worker has no authenticated first-node return signer"
	workerRuntimeProbeIdentityError           = "execution: signed probe lacks its return-channel or graph identity"
	workerRuntimeProbeBootstrapError          = "execution: signed preflight changed one run's bootstrap"
	workerRuntimeProbeReturnErrorFormat       = "execution: returning signed preflight: %w"
	workerRuntimeProbeReceiptError            = "execution: first node did not persist the preflight reply"
	workerRuntimePinnedProbeError             = "execution: signed v3 probe cannot be verified by this node"
	workerRuntimeBootstrapErrorFormat         = "execution: authenticating retained run bootstrap: %w"
	workerRuntimeReturnIdentityError          = "execution: signed preflight changed one run's return identity"
	workerRuntimeTaskAuthorityError           = "execution: offered v3 task lacks retained return authority"
	workerRuntimeTaskReaderError              = "execution: offered v3 task has no signed bootstrap reader"
	workerRuntimeTaskCarrierErrorFormat       = "execution: offered v3 task differs from its signed carrier: %w"
	workerRuntimeTaskCarrierError             = "execution: offered v3 task differs from its signed carrier"
	workerRuntimeTaskBootstrapFormat          = "execution: authenticating retained task bootstrap: %w"
	workerRuntimeTaskReturnIdentity           = "execution: offered task has a different signed return identity"
	workerRuntimeTaskPreflightError           = "execution: task has no matching signed preflight return identity"
	workerRuntimeProcessGroupUnsettledMessage = "task processes have not proved stopped; retaining the pickup and its capacity"
)
