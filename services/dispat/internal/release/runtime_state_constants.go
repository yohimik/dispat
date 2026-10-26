// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

const (
	releaseRuntimeAbsentIdentityText              = ""
	releasePreparedLockIncompleteErrorText        = "release: incomplete prepared lock identity"
	releasePreparedLocalLockReadErrorFormat       = "release: local prepared lock identity cannot be read: %w"
	releasePreparedLocalLockMismatchErrorText     = "release: local prepared lock identity differs from journal"
	releasePreparedRemoteLockReadErrorFormat      = "release: prepared remote lock could not be verified: %w"
	releasePreparedRemoteLockMismatchErrorFormat  = "%w: prepared remote lock differs from journal"
	releasePreparedLockJournalErrorFormat         = "release lock attempt cannot be journaled before its remote push: %w"
	releasePreparedLockHeldState                  = true
	releaseOutcomeUnplannedPackageErrorFormat     = "release: package outcome names no planned release %q"
	releaseOutcomeInvalidStatusErrorFormat        = "release: invalid settled package outcome %q"
	releaseOutcomeChangedStatusErrorFormat        = "release: package %s changed settled outcome from %s to %s"
	releaseOutcomeSkipUnplannedPackageErrorFormat = "release: skip admission names no planned release %q"
	releaseOutcomeDeadProviderState               = true
	releaseOutcomeSkipDeclinedState               = false
	releaseUnresolvedEffectState                  = true
)
