// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

const (
	bootstrapAbsentIdentityText                     = ""
	bootstrapAlgorithmVersionMissingErrorText       = "execution: bootstrap has no graph algorithm version"
	bootstrapBlobObjectTypeName                     = "blob"
	bootstrapCommitObjectTypeName                   = "commit"
	bootstrapCompilationModeName                    = "compilation"
	bootstrapCoordinationEndpointInvalidErrorFormat = "execution: invalid bootstrap coordination endpoint: %w"
	bootstrapDocumentEncodingErrorFormat            = "execution: bootstrap document cannot be encoded: %w"
	bootstrapDocumentLimitErrorText                 = "execution: bootstrap document exceeds signed limit"
	bootstrapDocumentPath                           = "dispat/choreography-bootstrap.json"
	bootstrapFirstEntryIndex                        = 0
	bootstrapIncompleteErrorText                    = "execution: incomplete choreography bootstrap"
	bootstrapInitialEntryCount                      = 0
	bootstrapInitialTreeCapacity                    = 1
	bootstrapInterpretationModeName                 = "interpretation"
	bootstrapHybridModeName                         = "hybrid"
	bootstrapInventoryItemPresentState              = true
	bootstrapMultipleTagStoresErrorText             = "execution: one repository has multiple tag source stores"
	bootstrapNativeRefInvalidErrorText              = "execution: invalid bootstrap native ref"
	bootstrapNativeRefKeySeparatorText              = "\u0000"
	bootstrapNoEntriesCount                         = 0
	bootstrapNoObjectBytes                          = 0
	bootstrapPackFilePrefixText                     = "pack-"
	bootstrapPackTreePathPrefix                     = "tag-objects/pack-"
	bootstrapPinnedObjectsCarryErrorFormat          = "execution: carrying pinned objects of %s: %w"
	bootstrapPinnedObjectsRestoreErrorFormat        = "execution: restoring pinned objects of %s: %w"
	bootstrapReadIncompleteErrorText                = "execution: incomplete choreography bootstrap read"
	bootstrapReturnEndpointInvalidErrorText         = "execution: invalid bootstrap return endpoint"
	bootstrapReturnRootPath                         = "/"
	bootstrapReturnSchemeName                       = "https"
	bootstrapSignaturePath                          = "dispat/choreography-bootstrap.sig"
	bootstrapSinceAnchorImportErrorFormat           = "execution: importing pinned since anchor: %w"
	bootstrapSinceAnchorIncompleteErrorText         = "execution: incomplete pinned since anchor"
	bootstrapSinceAnchorUnexpectedErrorText         = "execution: unexpected since anchor source"
	bootstrapSingleRepositoryCount                  = 1
	bootstrapSnapshotParentCount                    = 1
	bootstrapSourceInvalidErrorText                 = "execution: invalid bootstrap source"
	bootstrapTagCarrierTreeWriteErrorFormat         = "execution: writing tag carrier tree: %w"
	bootstrapTagClosureLimitErrorText               = "execution: tag closure exceeds bootstrap limit"
	bootstrapTagInspectionErrorFormat               = "execution: inspecting pinned tag %s: %w"
	bootstrapTagObjectTypeName                      = "tag"
	bootstrapTagObjectsDirectoryName                = "tag-objects"
	bootstrapTagPeelErrorFormat                     = "execution: peeling pinned tag %s: %w"
	bootstrapTagSourceInventoryIncompleteErrorText  = "execution: incomplete pinned tag source inventory"
	bootstrapTagSourceMismatchErrorText             = "execution: tag source differs from pinned ref"
	bootstrapTagSourceUnexpectedErrorText           = "execution: unexpected pinned tag source"
	bootstrapTagTargetImportErrorFormat             = "execution: importing pinned tag target %s: %w"
	bootstrapTagTargetMismatchErrorText             = "execution: pinned tag peeled target differs from inventory"
	bootstrapTagTargetMissingErrorText              = "execution: pinned tag has no final target"
	bootstrapTagTargetUnavailableErrorFormat        = "execution: pinned tag %s final target is unavailable: %w"
	bootstrapTlsPinInvalidErrorText                 = "execution: invalid bootstrap TLS pin"
	bootstrapTreeEntryUnexpectedErrorFormat         = "execution: unexpected choreography bootstrap tree entry %s"
	bootstrapTreeObjectTypeName                     = "tree"
)
