// Package gitx wraps the git operations the tool needs behind an interface,
// with a CLI implementation that shells out to the git binary (matching CI
// environments exactly).
package gitx

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/yohimik/dispat/internal/semver"
)

// Tag is a "pkg@version" release tag. When the newest tag's version is not
// strict MAJOR.MINOR.PATCH (e.g. "pkg@0.0.1-0.0.0"), Parsed is false: Name is
// still usable as a git revision, but Version is meaningless and the caller
// must take the baseline from elsewhere (config initials).
type Tag struct {
	Name    string
	Version semver.Version
	Parsed  bool
	// Created is the tag's creation time (unix seconds). Used to detect
	// consumers whose provider was released after the consumer's own last
	// release (e.g. the consumer failed in that run) and catch them up.
	Created int64
}

// TagName renders the canonical release tag for a package version.
func TagName(pkg string, v semver.Version) string { return pkg + "@" + v.String() }

// Git abstracts the repository operations used by planning and publishing.
type Git interface {
	// LatestTag returns the latest "pkg@*" tag for the package, if any. When
	// the newest tag (by creation date) has a parseable version, the returned
	// tag is the highest parseable version with Parsed=true; when the newest
	// tag's version cannot be parsed, that tag is returned with Parsed=false.
	LatestTag(ctx context.Context, pkg string) (Tag, bool, error)
	// Subjects lists commit subject lines reachable from HEAD, newest first.
	// When sinceTag is non-empty only commits after that tag are listed;
	// otherwise the whole history down to the first commit is used.
	Subjects(ctx context.Context, sinceTag string) ([]string, error)
	// CreateTag creates an annotated tag at HEAD.
	CreateTag(ctx context.Context, name, message string) error
}

// CLI is the Git implementation backed by the git executable.
type CLI struct {
	Dir string // repository root
}

var _ Git = (*CLI)(nil)

func (c *CLI) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", c.Dir}, args...)...)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s",
			strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out.String(), nil
}

// LatestTag lists tags matching "pkg@*", newest first by creation date (ties
// broken by version-aware name order). If the newest tag's version parses,
// the highest parseable version is returned (robust against out-of-order tag
// creation, e.g. backport tags); if the newest tag's version cannot be
// parsed, that tag itself is returned with Parsed=false so the caller can
// fall back to a configured initial version while still scanning commits
// from the tag.
func (c *CLI) LatestTag(ctx context.Context, pkg string) (Tag, bool, error) {
	// The last --sort key is primary: creation date desc, name as tie-break.
	// Ref names cannot contain spaces, so "name timestamp" splits cleanly.
	out, err := c.run(ctx, "tag", "--list", "--sort=-v:refname", "--sort=-creatordate",
		"--format=%(refname:short) %(creatordate:unix)", pkg+"@*")
	if err != nil {
		return Tag{}, false, err
	}
	prefix := pkg + "@"
	type entry struct {
		name    string
		created int64
	}
	var entries []entry
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, created := line, int64(0)
		if i := strings.LastIndexByte(line, ' '); i > 0 {
			name = line[:i]
			if ts, terr := strconv.ParseInt(strings.TrimSpace(line[i+1:]), 10, 64); terr == nil {
				created = ts
			}
		}
		if strings.HasPrefix(name, prefix) {
			entries = append(entries, entry{name: name, created: created})
		}
	}
	if len(entries) == 0 {
		return Tag{}, false, nil
	}
	if _, perr := semver.Parse(entries[0].name[len(prefix):]); perr != nil {
		// Newest tag exists but is unparseable.
		return Tag{Name: entries[0].name, Created: entries[0].created}, true, nil
	}
	best := Tag{}
	for _, e := range entries {
		v, perr := semver.Parse(e.name[len(prefix):])
		if perr != nil {
			continue
		}
		if !best.Parsed || best.Version.Compare(v) < 0 {
			best = Tag{Name: e.name, Version: v, Parsed: true, Created: e.created}
		}
	}
	return best, true, nil
}

func (c *CLI) Subjects(ctx context.Context, sinceTag string) ([]string, error) {
	rangeArg := "HEAD"
	if sinceTag != "" {
		rangeArg = sinceTag + "..HEAD"
	}
	out, err := c.run(ctx, "log", "--format=%s", rangeArg)
	if err != nil {
		return nil, err
	}
	out = strings.TrimRight(out, "\n")
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

func (c *CLI) CreateTag(ctx context.Context, name, message string) error {
	_, err := c.run(ctx, "tag", "-a", name, "-m", message)
	return err
}

// pathspec renders dir relative to the repo root, avoiding symlinked-tempdir
// mismatches in git pathspecs.
func (c *CLI) pathspec(dir string) string {
	if rel, err := filepath.Rel(c.Dir, dir); err == nil {
		return rel
	}
	return dir
}

// RevertDir discards all local changes inside dir: tracked files are restored
// from HEAD and untracked files and folders are removed. Note this also wipes
// any pre-existing uncommitted changes in that folder — CI runs from a clean
// checkout, which is the intended environment.
func (c *CLI) RevertDir(ctx context.Context, dir string) error {
	spec := c.pathspec(dir)
	if _, err := c.run(ctx, "checkout", "--", spec); err != nil {
		return err
	}
	_, err := c.run(ctx, "clean", "-fd", "--", spec)
	return err
}

// CommitDirs stages all changes inside the given directories and creates a
// single commit. It reports whether a commit was actually created: when the
// staged set turns out empty (e.g. changelogs disabled and no manifest
// changes) no commit is made and (false, nil) is returned.
func (c *CLI) CommitDirs(ctx context.Context, dirs []string, message string) (bool, error) {
	args := []string{"add", "--"}
	for _, d := range dirs {
		args = append(args, c.pathspec(d))
	}
	if _, err := c.run(ctx, args...); err != nil {
		return false, err
	}
	// diff --cached --quiet exits non-zero when something is staged.
	if _, err := c.run(ctx, "diff", "--cached", "--quiet"); err == nil {
		return false, nil // nothing staged
	}
	if _, err := c.run(ctx, "commit", "-m", message); err != nil {
		return false, err
	}
	return true, nil
}

// HeadSHA returns the full SHA of the current HEAD commit.
func (c *CLI) HeadSHA(ctx context.Context) (string, error) {
	out, err := c.run(ctx, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// VerifyRemote checks that the remote exists, is reachable and authenticated.
// Meant to run before any release work so misconfigured credentials fail fast.
func (c *CLI) VerifyRemote(ctx context.Context, remote string) error {
	_, err := c.run(ctx, "ls-remote", "--heads", remote)
	return err
}

// Push pushes the current branch (HEAD) together with reachable annotated
// tags to the remote. Requires a checked-out branch (not a detached HEAD).
func (c *CLI) Push(ctx context.Context, remote string) error {
	_, err := c.run(ctx, "push", "--follow-tags", remote, "HEAD")
	return err
}
