// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"context"
	"fmt"
	"strings"
)

// PreparedRelease binds a local retry to the exact receipt and release refs
// prepared before a tag is created. It is local evidence; the remote atomic
// push remains the publication authority.
type PreparedRelease struct {
	ReceiptObject string
	BranchHead    string
	TagTarget     string
}

const preparedRefPrefix = "refs/dispat/prepared/"

func preparedRef(tag string) string { return preparedRefPrefix + tag }

// PrepareRelease writes the intent before the local tag can exist. A crash
// before this ref leaves no local tag; after it, a retry knows its exact target.
func (c *LocalGitx) PrepareRelease(ctx context.Context, receipt Receipt, branchHead, tagTarget string) error {
	ref := preparedRef(receipt.Tag)
	if err := ValidRefName(ref); err != nil {
		return err
	}
	if receipt.Object == "" || !fullObjectID(branchHead) || !fullObjectID(tagTarget) {
		return fmt.Errorf("preparing %s: missing receipt or commit identity", receipt.Tag)
	}
	want := PreparedRelease{ReceiptObject: receipt.Object, BranchHead: branchHead, TagTarget: tagTarget}
	if existing, isHeld, err := c.ReadPreparedRelease(ctx, receipt.Tag); err != nil {
		return err
	} else if isHeld {
		if existing != want {
			return fmt.Errorf("prepared release %s names another receipt or target", receipt.Tag)
		}
		return nil
	}
	content := "dispat prepared v1\n" + receipt.Object + "\n" + branchHead + "\n" + tagTarget + "\n"
	oid, err := c.runStream(ctx, gitStream{stdin: strings.NewReader(content)}, "hash-object", "-w", "--stdin")
	if err != nil {
		return fmt.Errorf("preparing %s: %w", receipt.Tag, err)
	}
	if _, err := c.run(ctx, "update-ref", ref, strings.TrimSpace(oid), ""); err != nil {
		return fmt.Errorf("storing prepared release %s: %w", receipt.Tag, err)
	}
	return nil
}

// ReadPreparedRelease returns only a fully formed marker; malformed local
// evidence is an error rather than permission to guess a tag target.
func (c *LocalGitx) ReadPreparedRelease(ctx context.Context, tag string) (PreparedRelease, bool, error) {
	ref := preparedRef(tag)
	if err := ValidRefName(ref); err != nil {
		return PreparedRelease{}, false, err
	}
	value, err := c.run(ctx, "for-each-ref", "--format=%(objectname)", ref)
	if err != nil {
		return PreparedRelease{}, false, err
	}
	if strings.TrimSpace(value) == "" {
		return PreparedRelease{}, false, nil
	}
	content, err := c.run(ctx, "cat-file", "-p", ref)
	if err != nil {
		return PreparedRelease{}, false, err
	}
	lines := strings.Split(strings.TrimSpace(content), "\n")
	if len(lines) != 4 || lines[0] != "dispat prepared v1" ||
		!fullObjectID(lines[1]) || !fullObjectID(lines[2]) || !fullObjectID(lines[3]) {
		return PreparedRelease{}, false, fmt.Errorf("prepared release %s is malformed", tag)
	}
	return PreparedRelease{ReceiptObject: lines[1], BranchHead: lines[2], TagTarget: lines[3]}, true, nil
}

// DeletePreparedRelease removes local retry evidence after the remote update
// is verified. A later fetch does not bring this local marker back.
func (c *LocalGitx) DeletePreparedRelease(ctx context.Context, tag string) error {
	_, err := c.run(ctx, "update-ref", "-d", preparedRef(tag))
	return err
}
