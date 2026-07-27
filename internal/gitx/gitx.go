// Package gitx wraps the git operations the tool needs behind an interface,
// with a CLI implementation that shells out to the git binary (matching CI
// environments exactly).
package gitx

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/yohimik/monorel/internal/semver"
)

// Tag is a parsed "pkg@MAJOR.MINOR.PATCH" release tag.
type Tag struct {
	Name    string
	Version semver.Version
}

// TagName renders the canonical release tag for a package version.
func TagName(pkg string, v semver.Version) string { return pkg + "@" + v.String() }

// Git abstracts the repository operations used by planning and publishing.
type Git interface {
	// LatestTag returns the highest "pkg@semver" tag for the package, if any.
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

// LatestTag lists tags matching "pkg@*" and returns the highest parseable
// semantic version. Tags with unparseable versions are ignored.
func (c *CLI) LatestTag(ctx context.Context, pkg string) (Tag, bool, error) {
	out, err := c.run(ctx, "tag", "--list", pkg+"@*")
	if err != nil {
		return Tag{}, false, err
	}
	prefix := pkg + "@"
	var best Tag
	found := false
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		v, perr := semver.Parse(line[len(prefix):])
		if perr != nil {
			continue
		}
		if !found || best.Version.Compare(v) < 0 {
			best = Tag{Name: line, Version: v}
			found = true
		}
	}
	return best, found, nil
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
