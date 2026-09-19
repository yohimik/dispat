// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"context"
	"fmt"
	"strings"
)

// GitlinkTransition is one commit's before/after value for a submodule path.
// An all-zero object id represents an absent side of an add or delete.
type GitlinkTransition struct {
	From string
	To   string
}

// GitlinksAt returns the full object id of every gitlink in one tree, keyed
// by its control-repository path.
func (c *LocalGitx) GitlinksAt(ctx context.Context, revision string) (map[string]string, error) {
	if revision == "" {
		revision = "HEAD"
	}
	out, err := c.run(ctx, "ls-tree", "-r", "--full-tree", "-z", revision)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string)
	for _, record := range strings.Split(out, "\x00") {
		if record == "" {
			continue
		}
		meta, path, ok := strings.Cut(record, "\t")
		if !ok {
			return nil, fmt.Errorf("gitx: malformed ls-tree record")
		}
		parts := strings.Fields(meta)
		if len(parts) != 3 {
			return nil, fmt.Errorf("gitx: malformed ls-tree metadata %q", meta)
		}
		if parts[0] == "160000" && parts[1] == "commit" {
			result[path] = parts[2]
		}
	}
	return result, nil
}
