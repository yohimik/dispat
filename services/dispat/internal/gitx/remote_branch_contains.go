// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

const (
	gitReleaseBranchFetchOperationFormat         = "fetching release branch %s"
	gitNoReplaceObjectsEnvironment               = "GIT_NO_REPLACE_OBJECTS=1"
	gitReleaseBranchInvalidNameErrorFormat       = "gitx: invalid release branch %q: %w"
	gitReleasePreparedHeadInvalidErrorFormat     = "gitx: prepared release head %q is not a full object ID"
	gitReleaseBranchMissingErrorFormat           = "gitx: release branch %q has no advertised commit on %s"
	gitReleaseBranchRereadErrorFormat            = "gitx: release branch %q could not be reread after object import: %w"
	gitReleaseBranchMovedErrorFormat             = "gitx: release branch %q moved from %s to %s while its ancestry was checked"
	gitReleaseBranchObjectNotCommitErrorFormat   = "gitx: release branch ancestry object %s has type %s, expected commit"
	gitReleaseBranchObjectUnavailableErrorFormat = "gitx: release branch ancestry object %s is unavailable: %w"
	gitReleaseBranchNoPreparedHeadErrorFormat    = "gitx: release branch %q at %s does not contain prepared head %s"
	gitReleaseBranchAncestryErrorFormat          = "gitx: checking release branch %q ancestry: %w"
	gitLocalReleaseHeadUnavailableErrorFormat    = "gitx: local release head is unavailable: %w"
	gitLocalHeadNoRemoteBranchErrorFormat        = "gitx: local head %s does not contain remote release branch %q at %s"
	gitLocalReleaseHeadAncestryErrorFormat       = "gitx: checking local release head ancestry: %w"
)

// VerifyRemoteBranchContains proves that the exact advertised branch tip
// reaches a prepared release head. A rejected release push may be joined by
// a merge, so equality with the original prepared head is too strict. This
// fetch imports objects without updating local branch or tracking refs.
func (c *LocalGitx) VerifyRemoteBranchContains(ctx context.Context,
	remote, branch, preparedHead string) error {
	ref := transportBranchRefPrefixText + branch
	if err := ValidRefName(ref); err != nil {
		return fmt.Errorf(gitReleaseBranchInvalidNameErrorFormat, branch, err)
	}
	if !transportOID(preparedHead) {
		return fmt.Errorf(gitReleasePreparedHeadInvalidErrorFormat, preparedHead)
	}
	tip, err := c.remoteRefObject(ctx, remote, ref)
	if err != nil {
		return err
	}
	if !transportOID(tip) {
		return fmt.Errorf(gitReleaseBranchMissingErrorFormat,
			branch, RedactURL(remote))
	}
	if _, err := c.runEnv(ctx, []string{gitNoReplaceObjectsEnvironment},
		transportFetchCommandName, transportNoTagsFlag, transportNoFetchHeadWriteFlag, transportArgumentSeparatorText, remote, ref); err != nil {
		return transportError(err, remote, gitReleaseBranchFetchOperationFormat, ref)
	}
	current, err := c.remoteRefObject(ctx, remote, ref)
	if err != nil {
		return fmt.Errorf(gitReleaseBranchRereadErrorFormat,
			branch, err)
	}
	if current != tip {
		return fmt.Errorf(gitReleaseBranchMovedErrorFormat,
			branch, tip, current)
	}
	for _, oid := range []string{preparedHead, tip} {
		kind, kindErr := c.ObjectType(ctx, oid)
		if kindErr != nil {
			return fmt.Errorf(gitReleaseBranchObjectUnavailableErrorFormat, oid, kindErr)
		}
		if kind != transportCommitObjectTypeName {
			return fmt.Errorf(gitReleaseBranchObjectNotCommitErrorFormat, oid, kind)
		}
	}
	_, err = c.runEnv(ctx, []string{gitNoReplaceObjectsEnvironment},
		transportMergeBaseCommandName, transportIsAncestorFlag, preparedHead, tip)
	if err != nil {
		var failed *exec.ExitError
		if errors.As(err, &failed) && failed.ExitCode() == transportNotAncestorExitCode {
			return fmt.Errorf(gitReleaseBranchNoPreparedHeadErrorFormat,
				branch, tip, preparedHead)
		}
		return fmt.Errorf(gitReleaseBranchAncestryErrorFormat, branch, err)
	}
	localHead, err := c.HeadSHA(ctx)
	if err != nil {
		return fmt.Errorf(gitLocalReleaseHeadUnavailableErrorFormat, err)
	}
	_, err = c.runEnv(ctx, []string{gitNoReplaceObjectsEnvironment},
		transportMergeBaseCommandName, transportIsAncestorFlag, tip, localHead)
	if err == nil {
		return nil
	}
	var failed *exec.ExitError
	if errors.As(err, &failed) && failed.ExitCode() == transportNotAncestorExitCode {
		return fmt.Errorf(gitLocalHeadNoRemoteBranchErrorFormat,
			localHead, branch, tip)
	}
	return fmt.Errorf(gitLocalReleaseHeadAncestryErrorFormat, err)
}
