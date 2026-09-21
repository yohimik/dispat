// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// The files one task runs on, and how they stop existing afterwards.
//
// A node materializes the exact object ids its assignment named, each as a
// detached worktree of the cache the objects were fetched into. Detached
// because nothing here is a branch: the state is one commit somebody prepared,
// and a node that checked out a branch would be checking out whatever that
// branch holds now.
//
// The layout is the workspace's own: every repository sits at the path the
// orchestrator said it sits at, relative to one checkout root, so that a
// composed workspace's relative paths mean on the node what they mean at home.
// The root repository is materialized first, because every other path is
// inside it.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// Checkout is the set of worktrees one task runs in, and the record of what
// has to be taken away again.
type Checkout struct {
	git  *gitx.LocalGitx
	root string
	log  zerolog.Logger
	// added are the folders worktrees were actually created at, newest first,
	// so that removal takes a nested checkout away before the one it sits in.
	added []string
}

// NewCheckout opens an empty checkout rooted at one folder.
//
// The folder must not exist yet: git creates the root worktree's folder
// itself, and a folder somebody else made is a folder whose contents nothing
// in this run accounts for.
func NewCheckout(git *gitx.LocalGitx, root string, log zerolog.Logger) *Checkout {
	return &Checkout{git: git, root: root, log: log}
}

// Materialize creates one detached worktree per repository, in the order given.
func (c *Checkout) Materialize(ctx context.Context, repositories []AssignmentRepository) error {
	for _, repository := range repositories {
		dir := c.Dir(repository.Path)
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			return fmt.Errorf("execution: preparing the checkout folder of %s: %w", repository.Name, err)
		}
		plumbing := gitx.NewPlumbing(c.git)
		plumbing.WorktreeAdd(ctx, dir, repository.Snapshot)
		if err := plumbing.Err(); err != nil {
			return fmt.Errorf("execution: materializing %s at %s: %w", repository.Name, repository.Snapshot, err)
		}
		c.added = append([]string{dir}, c.added...)
		c.log.Debug().Str("repository", repository.Name).Str("commit", repository.Snapshot).
			Str("path", repository.Path).Msg("worktree added")
	}
	return nil
}

// Dir is where one repository-relative path sits inside this checkout. The
// path is the workspace's own spelling, with "." and the empty string both
// naming the root.
func (c *Checkout) Dir(parts ...string) string {
	dir := c.root
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		dir = filepath.Join(dir, filepath.FromSlash(part))
	}
	return dir
}

// Remove takes every worktree away again and forgets the administrative
// records left behind.
//
// Forced, because the folders being removed are folders a build wrote into,
// and pruned afterwards, because a worktree whose folder is gone is a record
// the cache would otherwise keep for ever. A failure is reported and never
// fatal: the task's result is what the run is about, and a folder that could
// not be removed is disk rather than correctness.
func (c *Checkout) Remove(ctx context.Context) {
	plumbing := gitx.NewPlumbing(c.git)
	for _, dir := range c.added {
		plumbing.WorktreeRemove(ctx, dir)
	}
	plumbing.WorktreePrune(ctx)
	if err := plumbing.Err(); err != nil {
		c.log.Warn().Err(err).Str("code", CodeTransportRetained).
			Str("category", CategoryTransportCleanup).Msg("a task checkout was not removed")
		return
	}
	c.log.Debug().Int("worktrees", len(c.added)).Msg("worktrees removed")
}

// CountStrayWrites is how many tracked files the task changed in one
// repository's checkout.
//
// It is a count rather than a list because the point is that they exist: a
// build that writes into files nobody declared has written somewhere the run
// will not carry, and the number is what makes that visible without putting a
// build's private paths in somebody's log. Untracked files are not counted:
// build products are untracked by definition, and what this asks about is the
// source the task was given. Neither are the declared output roots, whose
// files this run carries on purpose once they are captured.
func (c *Checkout) CountStrayWrites(ctx context.Context, path string, declared []string) int {
	changed, err := (&gitx.LocalGitx{Dir: c.Dir(path), Log: c.log}).CountChangedPaths(ctx, declared)
	if err != nil {
		c.log.Warn().Err(err).Msg("the task checkout could not be inspected for stray writes")
		return 0
	}
	return changed
}

// formatTaskFolder names the folder one attempt owns: the run, the task and
// the attempt, with everything a folder name cannot carry replaced.
//
// A task name is "<package>:<stage>", and a package name is whatever a
// workspace calls its packages, so the name is mapped rather than trusted: a
// folder is created from it, and a path is the one thing a name must never
// become on its own.
func formatTaskFolder(run, task string, attempt int) string {
	return formatPathWord(run) + "-" + formatPathWord(task) + "-" + formatPathWord(fmt.Sprint(attempt))
}

// formatPathWord maps one word onto the alphabet a folder name is made of.
func formatPathWord(word string) string {
	var mapped strings.Builder
	for _, letter := range word {
		isPlain := letter == '.' || letter == '_' || letter == '-' ||
			(letter >= '0' && letter <= '9') ||
			(letter >= 'a' && letter <= 'z') || (letter >= 'A' && letter <= 'Z')
		if isPlain {
			mapped.WriteRune(letter)
			continue
		}
		mapped.WriteByte('-')
	}
	return mapped.String()
}
