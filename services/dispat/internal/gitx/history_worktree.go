// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// AddHistoryWorktree adds a worktree at dir whose HEAD is commit, detached and
// with nothing checked out.
//
// Every history read of LocalGitx is spelled against HEAD, so a reader opened
// on this folder sees the repository as it stood at commit: the commits HEAD
// reaches and the tags merged into it. Nothing is checked out because nothing
// that reads history needs the files, and a large tree would cost a full
// checkout for no reader.
func (c *LocalGitx) AddHistoryWorktree(ctx context.Context, dir, commit string) error {
	if _, err := c.run(ctx, "worktree", "add", "--detach", "--no-checkout", "--", dir, commit); err != nil {
		return fmt.Errorf("gitx: adding a history worktree at %s: %w", commit, err)
	}
	return nil
}

// RemoveWorktree removes a worktree this repository added. It is forced
// because a worktree with nothing checked out reads to git as one whose every
// file was deleted.
func (c *LocalGitx) RemoveWorktree(ctx context.Context, dir string) error {
	if _, err := c.run(ctx, "worktree", "remove", "--force", "--", dir); err != nil {
		return fmt.Errorf("gitx: removing the worktree %s: %w", dir, err)
	}
	return nil
}

// CommitMessage is one commit and its full message.
type CommitMessage struct {
	SHA     string
	Message string
}

// FirstParentMessages lists commit and its first-parent ancestors, newest
// first, at most limit of them, each with its full message.
func (c *LocalGitx) FirstParentMessages(ctx context.Context, commit string, limit int) ([]CommitMessage, error) {
	if limit <= 0 {
		return nil, nil
	}
	out, err := c.run(ctx, "log", "--first-parent", "--max-count="+strconv.Itoa(limit),
		"--format=%H%x1f%B", "-z", commit, "--")
	if err != nil {
		return nil, fmt.Errorf("gitx: reading the first-parent line of %s: %w", commit, err)
	}
	var commits []CommitMessage
	for _, record := range strings.Split(out, "\x00") {
		if record == "" {
			continue
		}
		sha, message, ok := strings.Cut(record, "\x1f")
		if !ok {
			return nil, fmt.Errorf("gitx: malformed log record reading the first-parent line of %s", commit)
		}
		commits = append(commits, CommitMessage{SHA: strings.TrimSpace(sha), Message: message})
	}
	return commits, nil
}

// TagDate is when the tag was written: the tagger date of an annotated tag,
// the commit date of a lightweight one.
func (c *LocalGitx) TagDate(ctx context.Context, name string) (time.Time, error) {
	if err := validRefName(name); err != nil {
		return time.Time{}, fmt.Errorf("gitx: reading the date of tag %q: %w", name, err)
	}
	ref := "refs/tags/" + name
	out, err := c.run(ctx, "for-each-ref", "--format=%(refname)\t%(creatordate:iso-strict)", ref)
	if err != nil {
		return time.Time{}, fmt.Errorf("gitx: reading the date of tag %s: %w", name, err)
	}
	var stamp string
	for line := range strings.Lines(out) {
		// A pattern matches the refs below it too, so the name is compared.
		if refname, value, ok := strings.Cut(strings.TrimSpace(line), "\t"); ok && refname == ref {
			stamp = value
		}
	}
	if stamp == "" {
		return time.Time{}, fmt.Errorf("gitx: no tag %s", name)
	}
	date, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return time.Time{}, fmt.Errorf("gitx: reading the date of tag %s: %w", name, err)
	}
	return date, nil
}
