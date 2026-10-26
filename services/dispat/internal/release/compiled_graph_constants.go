// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

const (
	compiledBuildStageName                         = "build"
	compiledFrameProviderRecordBlockedState        = true
	compiledFrameUnsettledPredecessorErrorFormat   = "release: task %q lacks settled predecessor %q"
	compiledGraphAbsentIdentityText                = ""
	compiledGraphDigestVersion                     = 3
	compiledGraphInitialEntryCount                 = 0
	compiledGraphLexicalAfterOrder                 = 1
	compiledGraphLexicalBeforeOrder                = -1
	compiledGraphLexicalEqualOrder                 = 0
	compiledGraphMinimumTaskWeight                 = 1
	compiledGraphNilPlanErrorText                  = "release: cannot compile a nil plan"
	compiledGraphNoEntriesCount                    = 0
	compiledGraphPackageChangedState               = true
	compiledGraphSyncLockWeight                    = 1
	compiledGraphVirtualTaskState                  = true
	compiledPreparedGraphAlreadyUsedErrorText      = "release: prepared tasks require an unused compiled graph"
	compiledPreparedGraphDigestVersion             = 4
	compiledPreparedProducesOutputsState           = true
	compiledPreparedTaskConsumerUnknownErrorFormat = "release: prepared task %q has unknown consumer %q"
	compiledPreparedTaskDuplicateErrorFormat       = "release: duplicate prepared task %q"
	compiledPreparedTaskIDSuffix                   = ":prepare"
	compiledPreparedTaskInvalidErrorFormat         = "release: invalid unreleasing prepared task %q"
	compiledTaskIDFormat                           = "%s:%d:%s"
	declaredOutputAbsentPathText                   = ""
	declaredOutputForbiddenPathCharacters          = ":\\\u0000"
	declaredOutputParentPath                       = ".."
	declaredOutputPathMatchedState                 = true
	declaredOutputPathNULRune                      = 0
	declaredOutputPathSeparatorRune                = '/'
	declaredOutputPathSeparatorText                = "/"
	declaredOutputPathUnmatchedState               = false
	declaredOutputRootRelativePath                 = "."
	declaredOutputWindowsSeparatorText             = "\\"
	invalidDeclaredOutputPathErrorText             = "release: invalid declared output path"
	undeclaredOutputPathErrorText                  = "release: output path is outside declared roots"
)
