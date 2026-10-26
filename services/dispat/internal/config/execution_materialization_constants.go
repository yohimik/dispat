// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package config

const (
	executionAdmissionTimeoutFieldPath                = "execution.timeouts.admission"
	executionIdentityUnsetText                        = ""
	executionMaxPackBytesFieldPath                    = "execution.transfer.maxPackBytes"
	executionMaxPackBytesLimitErrorFormat             = "execution.transfer.maxPackBytes must be <= %d"
	executionPackLimitProbeBytes                      = 1
	materializedConfigEscapesRepositoryErrorFormat    = "planning configuration %q of %q lies outside repository %q"
	materializedConfigResolutionErrorFormat           = "resolving planning configuration of %q: %w"
	materializedRepositoryResolutionErrorFormat       = "resolving planning repository %q: %w"
	materializedWorkspaceAbsentPath                   = ""
	materializedWorkspaceIncompleteRosterErrorText    = "planning materialization does not cover every workspace repository"
	materializedWorkspaceMissingControlErrorText      = "planning materialization omits the control repository"
	materializedWorkspaceMissingRepositoryErrorFormat = "planning materialization omits repository %q"
	materializedWorkspaceParentPath                   = ".."
)
