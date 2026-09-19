// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

// The Git operations a choreographed fleet's links need, and nothing else
// needs.
//
// Two of them are unusual enough to say why here. A fleet link is recorded
// through a temporary index rather than through `git add`, because the link
// this repository has to record is often the back-link of a pair and a
// back-link is deliberately never checked out: staging from the worktree would
// see an empty folder where the pin belongs and delete the link. And every
// pathspec the release builds excludes the link paths, because between two
// settlements a link's pin is advisory — it may sit ahead of what the last
// commit recorded — and an advisory pin swept into a release commit would
// publish a pointer nobody decided on.

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Submodule is one fleet link as a repository declares it: the identity it is
// linked under, where it is fetched from, where it lives, and the branch a
// materialized link follows.
type Submodule struct {
	Name   string
	URL    string
	Path   string
	Branch string
}

// withoutLinks appends an exclusion for every fleet link path to a pathspec
// list. A repository with no links gets its arguments back untouched, which is
// what keeps an orchestrated release's Git command lines exactly as they were.
func (c *LocalGitx) withoutLinks(paths []string) []string {
	if len(c.LinkPaths) == 0 {
		return paths
	}
	out := append([]string(nil), paths...)
	if len(out) == 0 {
		// An exclusion has to have something to exclude from: on its own it
		// names no paths at all, so the whole repository is named first.
		out = append(out, ":/")
	}
	for _, link := range c.LinkPaths {
		if link == "" {
			continue
		}
		out = append(out, ":(exclude)"+filepath.ToSlash(filepath.Clean(link)))
	}
	return out
}

// GitlinksAtPaths returns the recorded commit of each named gitlink in one
// tree, keyed by path. Paths the tree holds no gitlink at are absent from the
// result rather than an error: a link added after that revision is exactly the
// absence a boundary walk has to be able to read.
func (c *LocalGitx) GitlinksAtPaths(ctx context.Context, revision string, paths []string) (map[string]string, error) {
	entries, err := c.treeEntriesAtPaths(ctx, revision, paths)
	if err != nil {
		return nil, err
	}
	links := make(map[string]string, len(entries))
	for path, entry := range entries {
		if entry.mode == gitlinkMode {
			links[path] = entry.oid
		}
	}
	return links, nil
}

// gitlinkMode is the tree mode of a submodule entry.
const gitlinkMode = "160000"

// treeEntry is one path of a tree as Git records it.
type treeEntry struct {
	mode string
	oid  string
}

// treeEntriesAtPaths reads the named paths of one tree without descending into
// them, so a caller learns both what is there and what kind of thing it is.
func (c *LocalGitx) treeEntriesAtPaths(ctx context.Context, revision string, paths []string) (map[string]treeEntry, error) {
	if len(paths) == 0 {
		return map[string]treeEntry{}, nil
	}
	if revision == "" {
		revision = "HEAD"
	}
	out, err := c.run(ctx, append([]string{"ls-tree", "-z", revision, "--"}, paths...)...)
	if err != nil {
		return nil, err
	}
	entries := make(map[string]treeEntry, len(paths))
	for _, record := range strings.Split(out, "\x00") {
		if record == "" {
			continue
		}
		meta, path, ok := strings.Cut(record, "\t")
		if !ok {
			return nil, fmt.Errorf("gitx: malformed ls-tree record")
		}
		fields := strings.Fields(meta)
		if len(fields) != 3 {
			return nil, fmt.Errorf("gitx: malformed ls-tree metadata %q", meta)
		}
		entries[path] = treeEntry{mode: fields[0], oid: fields[2]}
	}
	return entries, nil
}

// CommitSubjects returns the subject line of each revision, in one Git call.
//
// Cross-repository evidence is read subject by subject — a release commit
// names the tag it recorded — and a fleet walk would otherwise ask once per
// hop per consumer. Every revision must resolve: a subject that cannot be read
// is the difference between "this commit does not say so" and "this history
// does not contain it", and the caller has to be able to tell them apart.
func (c *LocalGitx) CommitSubjects(ctx context.Context, revisions []string) (map[string]string, error) {
	if len(revisions) == 0 {
		return map[string]string{}, nil
	}
	args := append([]string{"log", "--no-walk", "--format=%H%x1f%s", "-z"}, revisions...)
	out, err := c.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	subjects := make(map[string]string, len(revisions))
	for _, record := range strings.Split(out, "\x00") {
		if record == "" {
			continue
		}
		oid, subject, ok := strings.Cut(record, "\x1f")
		if !ok {
			return nil, fmt.Errorf("gitx: malformed log record")
		}
		subjects[strings.TrimSpace(oid)] = subject
	}
	return subjects, nil
}

// CommitGitlinks records exactly these gitlink pins as one commit on top of
// HEAD and answers the resulting revision and whether a commit was needed.
//
// The commit is built through a temporary index: the repository's own index
// and worktree are never staged from, so an unpopulated back-link stays a pin
// rather than becoming a deletion, and whatever else the operator had staged
// is left alone. The reference moves by compare-and-swap against the HEAD this
// started from, so a concurrent writer is refused rather than overwritten, and
// a detached HEAD moves as itself.
//
// Pins whose value the current tree already carries need no commit, which is
// the ordinary case once a fleet has settled: the fast path costs one tree
// read and creates nothing.
func (c *LocalGitx) CommitGitlinks(ctx context.Context, message string, pins map[string]string) (string, bool, error) {
	if len(pins) == 0 {
		head, err := c.HeadSHA(ctx)
		return head, false, err
	}
	paths := make([]string, 0, len(pins))
	for path := range pins {
		paths = append(paths, filepath.ToSlash(filepath.Clean(path)))
	}
	sort.Strings(paths)
	parent, err := c.HeadSHA(ctx)
	if err != nil {
		return "", false, err
	}
	recorded, err := c.treeEntriesAtPaths(ctx, parent, paths)
	if err != nil {
		return "", false, err
	}
	pending := false
	for _, path := range paths {
		entry, present := recorded[path]
		// `update-index --cacheinfo` would happily replace a tracked file with
		// a gitlink, and the commit would delete that file from the tree. A
		// settlement records links and nothing else.
		if present && entry.mode != gitlinkMode {
			return "", false, fmt.Errorf("gitx: %s is not a fleet link in %s (mode %s)", path, parent, entry.mode)
		}
		if entry.oid != pins[path] {
			pending = true
		}
	}
	if !pending {
		return parent, false, nil
	}
	dir, err := os.MkdirTemp("", "dispat-gitlinks-")
	if err != nil {
		return "", false, fmt.Errorf("gitx: gitlink index: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(dir, "index")}
	if _, err := c.runEnv(ctx, env, "read-tree", parent); err != nil {
		return "", false, err
	}
	// Every pin in one write. `update-index` takes `--cacheinfo` as often as
	// it is given, and the number of Git processes a settlement forks is what
	// its benchmark reports.
	stage := []string{"update-index", "--add"}
	for _, path := range paths {
		stage = append(stage, "--cacheinfo", gitlinkMode+","+pins[path]+","+path)
	}
	if _, err := c.runEnv(ctx, env, stage...); err != nil {
		return "", false, err
	}
	tree, err := c.runEnv(ctx, env, "write-tree")
	if err != nil {
		return "", false, err
	}
	created, err := c.run(ctx, "commit-tree", strings.TrimSpace(tree), "-p", parent, "-m", message)
	if err != nil {
		return "", false, err
	}
	revision := strings.TrimSpace(created)
	if _, err := c.run(ctx, "update-ref", "-m", message, "HEAD", revision, parent); err != nil {
		return "", false, err
	}
	// The commit was built outside the repository's index, which therefore
	// still holds the pins the settlement replaced. Refreshing exactly those
	// entries is what keeps `git status` honest for a reader afterwards; it is
	// reported rather than swallowed, because a recorded commit with a stale
	// index is a state the next run must be told about.
	if _, err := c.run(ctx, stage...); err != nil {
		return revision, true, fmt.Errorf(
			"gitx: %s records the fleet links but the index still holds the previous pins of %s: %w",
			revision, strings.Join(paths, ", "), err)
	}
	return revision, true, nil
}

// AddSubmodule creates a fleet link by cloning the peer into this repository,
// which is how the forward half of a link is made.
func (c *LocalGitx) AddSubmodule(ctx context.Context, module Submodule) error {
	if err := requireCredentialFreeURL(module.URL); err != nil {
		return err
	}
	args := []string{"submodule", "add", "--name", module.Name}
	if module.Branch != "" {
		args = append(args, "-b", module.Branch)
	}
	args = append(args, "--", module.URL, module.Path)
	_, err := c.run(ctx, args...)
	return err
}

// AddGitlink declares a fleet link and stages its pin without fetching
// anything, which is how the back half of a two-sided link is made.
//
// The peer already holds a checkout of this repository, or is about to; what
// it needs is the declaration and a pin that its remote can serve. The empty
// folder is part of the record: Git reports a gitlink whose directory is
// missing as a deleted path, so the directory is what keeps the repository
// clean for everyone who never initializes the link.
func (c *LocalGitx) AddGitlink(ctx context.Context, module Submodule, revision string) error {
	if err := requireCredentialFreeURL(module.URL); err != nil {
		return err
	}
	path := filepath.ToSlash(filepath.Clean(module.Path))
	key := "submodule." + module.Name
	settings := [][2]string{{key + ".path", path}, {key + ".url", module.URL}}
	if module.Branch != "" {
		settings = append(settings, [2]string{key + ".branch", module.Branch})
	}
	for _, setting := range settings {
		if _, err := c.run(ctx, "config", "--file", ".gitmodules", setting[0], setting[1]); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Join(c.Dir, filepath.FromSlash(path)), 0o755); err != nil {
		return fmt.Errorf("gitx: fleet link folder %s: %w", path, err)
	}
	if _, err := c.run(ctx, "update-index", "--add", "--cacheinfo", "160000,"+revision+","+path); err != nil {
		return err
	}
	_, err := c.run(ctx, "update-index", "--add", "--", ".gitmodules")
	return err
}

// InitSubmodule materializes one declared link at its branch tip.
//
// It follows the remote rather than the recorded pin because a fleet pin is
// advisory: the repository at the other end releases on its own schedule, and
// what a reader of this link wants is that repository as it is now. It is
// deliberately not recursive, so the back-link the peer carries to this very
// repository stays an empty folder instead of becoming a second copy of it.
func (c *LocalGitx) InitSubmodule(ctx context.Context, path string) error {
	_, err := c.run(ctx, "submodule", "update", "--init", "--remote", "--", filepath.ToSlash(filepath.Clean(path)))
	return err
}

// requireCredentialFreeURL refuses a remote carrying user information. A fleet
// link's URL is written into `.gitmodules`, which is committed and pushed, so
// a password in it would be published with the repository.
func requireCredentialFreeURL(remote string) error {
	parsed, err := url.Parse(remote)
	if err != nil || parsed.User == nil {
		return nil
	}
	return fmt.Errorf("gitx: fleet link URL %s carries user information, which a committed .gitmodules would publish", RedactURL(remote))
}
