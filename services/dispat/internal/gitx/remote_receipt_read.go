// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"context"
	"fmt"
)

const gitRemoteReceiptInvalidTagErrorFormat = "gitx: invalid publication receipt tag %q: %w"

// RemoteReceiptObject reads the exact unpeeled receipt ref at a release
// destination. An empty answer proves the named ref is absent at this read.
func (c *LocalGitx) RemoteReceiptObject(ctx context.Context, remote, tag string) (string, error) {
	ref := ReceiptRefPrefix + tag
	if err := ValidRefName(ref); err != nil {
		return transportAbsentObjectText, fmt.Errorf(gitRemoteReceiptInvalidTagErrorFormat, tag, err)
	}
	return c.remoteRefObject(ctx, remote, ref)
}
