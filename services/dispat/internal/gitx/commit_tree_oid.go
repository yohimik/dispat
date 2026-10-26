// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"context"
	"fmt"
	"strings"
)

// CommitTreeOID returns an exact commit's tree identity. Transport snapshot
// commits include a timestamp, so two captures of identical source bytes can
// have different commit OIDs while this tree OID stays equal.
func (c *LocalGitx) CommitTreeOID(ctx context.Context, commitOID string) (string, error) {
	if !transportOID(commitOID) {
		return transportAbsentObjectText, fmt.Errorf(invalidSnapshotCommitIDErrorText)
	}
	kind, err := c.ObjectType(ctx, commitOID)
	if err != nil {
		return transportAbsentObjectText, fmt.Errorf(snapshotObjectUnavailableErrorFormat, commitOID, err)
	}
	if kind != transportCommitObjectTypeName {
		return transportAbsentObjectText, fmt.Errorf(snapshotObjectTypeMismatchErrorFormat, commitOID, kind)
	}
	out, err := c.runEnv(ctx, []string{transportDisableReplacementEnvironmentValue},
		transportResolveRevisionCommandName, transportVerifyRevisionFlag, transportEndOptionsFlag, commitOID+transportCommitTreeSuffixText)
	if err != nil {
		return transportAbsentObjectText, fmt.Errorf(snapshotTreeReadErrorFormat, err)
	}
	tree := strings.TrimSpace(out)
	if !transportOID(tree) {
		return transportAbsentObjectText, fmt.Errorf(invalidSnapshotTreeIDErrorText)
	}
	return tree, nil
}
