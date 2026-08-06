// Package gitx wraps the git operations the tool needs behind an interface,
// with a CLI implementation that shells out to the git binary (matching CI
// environments exactly).
package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/yohimik/dispat/pkg/ccme"
)

// Tag is a "pkg@version" release tag. When the newest tag's version is not
// strict MAJOR.MINOR.PATCH (e.g. "pkg@0.0.1-0.0.0"), Parsed is false: Name is
// still usable as a git revision, but Version is meaningless and the caller
// must take the baseline from elsewhere (config initials).
type Tag struct {
	Name    string
	Version ccme.Version
	Parsed  bool
	// Created is the tag's creation time (unix seconds). Reporting only: no
	// release decision may depend on it, because tag creation order is not
	// stable under merges, rebases or equal timestamps (§10.4).
	Created int64
	// Commit is the commit the tag points at, annotated tags peeled. For a
	// stable tag this is stableCommit(P) of §12.3, which is the origin of the
	// package's pending window (§13.3) and the operand of the ancestry screen
	// in §13.7b.
	Commit string
}

// TagFormat is a template for release tag names. Four placeholders are
// substituted; every other byte is literal:
//
//	{name}     the package name
//	{version}  the SemVer version, with no "v" prefix of its own
//	{channel}  the prerelease channel, e.g. "beta"
//	{counter}  the prerelease counter, e.g. "4"
//
// §14 makes only "{name}@{version}" normative and leaves other formats
// implementation-defined, which is what allows the common local conventions —
// a "v" prefix, or a path prefix mirroring a monorepo's layout:
//
//	{name}@{version}            core@1.2.3
//	{name}@v{version}           core@v1.2.3
//	services/{name}@v{version}  services/core@v1.2.3
//
// {channel} and {counter} spell the prerelease out instead of leaving it
// inside {version}, for the conventions that do not write it the way SemVer
// does. They are used together — a counter with no channel cannot tell two
// trains apart, and a channel with no counter gives every prerelease of a
// train the same tag — and their presence narrows {version} to the
// MAJOR.MINOR.PATCH core:
//
//	{name}@{version}-{channel}{counter}   core@1.2.3-beta4
//	{name}@{version}.{channel}.{counter}  core@1.2.3.beta.4
//
// On a stable version there is no channel to write, so the whole prerelease
// section — the two placeholders and the literal text glued to them — is
// dropped and "core@1.2.3" is what both of those render. Only the tag's shape
// changes: the version itself is SemVer throughout, and it is the parsed
// version, never the tag text, that orders releases (§11.3).
//
// The format is a property of a space rather than of the repository, because
// the convention usually follows the toolchain a group of packages is built
// with — Go modules want the path form, npm packages the plain one — and a
// monorepo mixing the two is the case worth supporting.
type TagFormat string

// DefaultTagFormat is the format §14 makes normative.
const DefaultTagFormat TagFormat = "{name}@{version}"

const (
	tagNamePlaceholder    = "{name}"
	tagVersionPlaceholder = "{version}"
	tagChannelPlaceholder = "{channel}"
	tagCounterPlaceholder = "{counter}"
)

// WithDefault returns the format, or DefaultTagFormat when it is empty.
func (f TagFormat) WithDefault() TagFormat {
	if f == "" {
		return DefaultTagFormat
	}
	return f
}

// Validate reports whether the format can both render and parse a tag, and
// whether what it renders is a name git will accept.
//
// The second half matters more than it looks. A format is only exercised at
// the very end of a release — after the artefact is published — so an
// unacceptable name fails at the worst possible moment, leaving a package
// published and untagged, which is exactly the state the next run reads as
// "never released". Rejecting it at load time costs nothing and prevents that
// entirely. The leading slash in "/services/{name}@v{version}" is the mistake
// this is for: it reads naturally and git refuses it.
//
// The round trip is checked for the same reason. A format free to place
// {version} next to {channel} is also free to place them so that no reader can
// tell where one ends and the other begins; rendering a sample and parsing it
// back is the cheap way to find that out now rather than one run later, when
// the ambiguous tag is already the baseline.
func (f TagFormat) Validate() error {
	tpl, err := f.template()
	if err != nil {
		return fmt.Errorf("tag format %q: %w", string(f), err)
	}
	samples := []ccme.Version{{Minor: 1}}
	if tpl.spellsPrerelease() {
		samples = append(samples, ccme.Version{Minor: 1, Prerelease: []string{"beta", "4"}})
	}
	for _, v := range samples {
		// Validate what the format renders, not the template: the template
		// legitimately contains "@{", which git forbids in a ref name.
		sample := tpl.render("pkg", v)
		if err := validRefName(sample); err != nil {
			return fmt.Errorf("tag format %q would produce %q, which git rejects: %w",
				string(f), sample, err)
		}
		back, ok := tpl.parseVersion("pkg", sample)
		if !ok || back.String() != v.String() {
			return fmt.Errorf("tag format %q renders %s as %q but cannot read it back",
				string(f), v.String(), sample)
		}
	}
	return nil
}

// validRefName applies the rules of git-check-ref-format that a tag template
// can plausibly violate. It is deliberately not the complete grammar: the
// point is to catch a misconfiguration early, and git remains the authority.
func validRefName(name string) error {
	switch {
	case name == "":
		return errors.New("a ref name may not be empty")
	case strings.HasPrefix(name, "/"), strings.HasSuffix(name, "/"):
		return errors.New("a ref name may not begin or end with '/'")
	case strings.HasPrefix(name, "-"):
		return errors.New("a ref name may not begin with '-'")
	case strings.HasPrefix(name, "."), strings.HasSuffix(name, "."):
		return errors.New("a ref name may not begin or end with '.'")
	case strings.HasSuffix(name, ".lock"):
		return errors.New("a ref name may not end with '.lock'")
	case strings.Contains(name, "//"), strings.Contains(name, ".."), strings.Contains(name, "@{"):
		return errors.New("a ref name may not contain '//', '..' or '@{'")
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c <= ' ' || c == 0x7f {
			return errors.New("a ref name may not contain whitespace or control characters")
		}
		if strings.IndexByte("~^:?*[\\", c) >= 0 {
			return fmt.Errorf("a ref name may not contain %q", string(c))
		}
	}
	return nil
}

// Render builds the tag name for a package version.
func (f TagFormat) Render(pkg string, v ccme.Version) string {
	tpl, err := f.template()
	if err != nil {
		// Unreachable for a validated format; rendering something is still
		// better than rendering nothing at the end of a release.
		return DefaultTagFormat.Render(pkg, v)
	}
	return tpl.render(pkg, v)
}

// RenderVersion builds only the version section of a tag: the {version}
// placeholder through {counter}, with the literals between them, and nothing
// of the name or the decoration around it — under
// "{name}@v{version}-{channel}{counter}" a 1.2.3-beta.4 renders as
// "1.2.3-beta4" (the "v" belongs to the tag, not the version). A format that
// leaves the prerelease inside {version} renders the plain SemVer string.
func (f TagFormat) RenderVersion(v ccme.Version) string {
	tpl, err := f.template()
	if err != nil {
		return v.String()
	}
	return tpl.renderVersion(v)
}

// Glob builds the `git tag --list` pattern matching every version of one
// package.
//
// It is built from the stable shape, whose "*" spans the prerelease section
// too, so one pattern covers both shapes. The pattern is only a filter: Matches
// re-checks every candidate.
func (f TagFormat) Glob(pkg string) string {
	prefix, suffix, ok := f.split(pkg)
	if !ok {
		return pkg + "*"
	}
	return prefix + "*" + suffix
}

// split returns the literal text surrounding the version for one package: what
// precedes it, and what follows the whole prerelease section.
func (f TagFormat) split(pkg string) (prefix, suffix string, ok bool) {
	tpl, err := f.template()
	if err != nil {
		return "", "", false
	}
	return tpl.split(pkg)
}

// ParseVersion extracts the version from a tag name.
//
// The tag is matched against the format itself rather than split on a
// separator. That matters for the reason §12.1 gives about splitting at the
// last "@": package names may contain the separator and versions never do, and
// a format-driven match is right for every convention rather than for one. A
// format spelling the prerelease out is tried in its prerelease shape first
// and its stable shape second, so "core@1.2.3" and "core@1.2.3-beta4" both
// read back under one format.
func (f TagFormat) ParseVersion(pkg, tag string) (ccme.Version, bool) {
	tpl, err := f.template()
	if err != nil {
		return ccme.Version{}, false
	}
	return tpl.parseVersion(pkg, tag)
}

// Matches reports whether a tag belongs to a package under this format. A tag
// can match the shape and still carry an unparseable version, which is the
// case the initials fallback exists for — so this is deliberately the loose
// literal check and not ParseVersion.
func (f TagFormat) Matches(pkg, tag string) bool {
	prefix, suffix, ok := f.split(pkg)
	if !ok {
		return false
	}
	return len(tag) > len(prefix)+len(suffix) &&
		strings.HasPrefix(tag, prefix) && strings.HasSuffix(tag, suffix)
}

// TagName renders a release tag under the default format. Callers that know a
// package's space should use its format instead.
//
// It is also what DISPAT_SEMVER_TAG carries: the same release named under the
// normative "{name}@{version}", so a script written against SemVer keeps a
// stable input whatever local convention the space's tagFormat encodes.
func TagName(pkg string, v ccme.Version) string { return DefaultTagFormat.Render(pkg, v) }

// Commit is one commit of a pending window.
type Commit struct {
	// SHA identifies the commit. It is the key a pending window is a set of
	// (§13.3), so it must be present for propagation to be admitted against
	// the right window at all.
	SHA string
	// Parents are the commit's parent SHAs, first parent first. They let the
	// planner answer ancestor-or-self questions (§10.4) without a git call per
	// pair.
	Parents []string
	Message string
	// Files are the paths the commit changed, used for file-derived scope
	// resolution (§6.2). For a merge commit these are the changes against the
	// first parent.
	Files []string
}

// Tags are one package's reachable release tags, newest first by creation
// date. Both baselines of §12.3 are selections over this one list, which is
// why it is the primitive: a planner that asked for them separately would run
// the same `git tag` query twice per package.
type Tags []Tag

// Baseline is the highest tag by SemVer precedence, prereleases included: what
// the package last published, and what its channel is derived from.
func (t Tags) Baseline() (Tag, bool) {
	return t.highest(func(Tag) bool { return true })
}

// StableBaseline is the highest tag with no prerelease component.
//
// This — not Baseline — is what a pending window is measured from. The
// distinction is invisible for a package on the stable channel, where the two
// coincide, and load-bearing for one on a prerelease train: the window has to
// span the whole train so that the train's target can be recomputed from the
// stable baseline on every run, which is what makes a breaking change arriving
// mid-train move the whole train.
func (t Tags) StableBaseline() (Tag, bool) {
	return t.highest(func(tag Tag) bool { return !tag.Version.IsPrerelease() })
}

// highest picks the highest-precedence tag satisfying keep.
//
// An unparseable newest tag short-circuits both selections identically: a
// baseline that cannot be read makes every older tag untrustworthy too, so the
// caller falls back to a configured initial version while still measuring the
// window from the tag — which is what stops already-released commits being
// counted twice.
func (t Tags) highest(keep func(Tag) bool) (Tag, bool) {
	if len(t) == 0 {
		return Tag{}, false
	}
	if !t[0].Parsed {
		return t[0], true
	}
	best, found := Tag{}, false
	for _, tag := range t {
		if !tag.Parsed || !keep(tag) {
			continue
		}
		if !found || best.Version.Compare(tag.Version) < 0 {
			best, found = tag, true
		}
	}
	return best, found
}

// Git abstracts the repository operations used by planning and publishing.
type Git interface {
	// Tags returns every reachable tag of the package under format, newest
	// first by creation date. Callers select a baseline from it with
	// Tags.Baseline or Tags.StableBaseline.
	Tags(ctx context.Context, pkg string, format TagFormat) (Tags, error)
	// Subjects lists commit subject lines reachable from HEAD, newest first.
	// When sinceTag is non-empty only commits after that tag are listed;
	// otherwise the whole history down to the first commit is used.
	Subjects(ctx context.Context, sinceTag string) ([]string, error)
	// Commits lists commit messages lines reachable from HEAD, newest first.
	// When sinceTag is non-empty only commits after that tag are listed;
	// otherwise the whole history down to the first commit is used.
	Commits(ctx context.Context, sinceTag string) ([]Commit, error)
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

// Tags returns every reachable tag of the package, newest first by creation
// date (ties broken by version-aware name order), with its peeled target
// commit.
//
// This is one `git tag --list` per package and the only tag query planning
// makes: both baselines are selections over the result. Only tags reachable
// from HEAD are considered, so a tag on an unmerged branch does not affect
// this branch's computation and per-branch release lines work with no
// configuration.
func (c *CLI) Tags(ctx context.Context, pkg string, format TagFormat) (Tags, error) {
	format = format.WithDefault()
	// The last --sort key is primary: creation date desc, name as tie-break.
	// Tabs separate the fields; a ref name can contain neither a tab nor a
	// space, and %(*objectname) is empty for a lightweight tag.
	out, err := c.run(ctx, "tag", "--list", "--merged", "HEAD",
		"--sort=-v:refname", "--sort=-creatordate",
		"--format=%(refname:short)\t%(creatordate:unix)\t%(objectname)\t%(*objectname)",
		format.Glob(pkg))
	if err != nil {
		return nil, err
	}
	var tags Tags
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 3 {
			continue
		}
		name := strings.TrimSpace(f[0])
		// The glob is not precise enough on its own: "*" matches any run of
		// characters, so under "{name}@{version}" the pattern "core@*" also
		// matches a tag of a package called "core@extra". Re-checking the
		// shape against the format is what keeps someone else's tags out
		// (§12.1).
		if !format.Matches(pkg, name) {
			continue
		}
		t := Tag{Name: name, Commit: strings.TrimSpace(f[2])}
		if ts, terr := strconv.ParseInt(strings.TrimSpace(f[1]), 10, 64); terr == nil {
			t.Created = ts
		}
		// An annotated tag's %(objectname) is the tag object; the commit is
		// the peeled %(*objectname). Lightweight tags leave it empty.
		if len(f) > 3 {
			if peeled := strings.TrimSpace(f[3]); peeled != "" {
				t.Commit = peeled
			}
		}
		if v, ok := format.ParseVersion(pkg, name); ok {
			t.Version, t.Parsed = v, true
		}
		tags = append(tags, t)
	}
	return tags, nil
}

// IsAncestor reports whether commit a is an ancestor-or-self of commit b.
//
// Ancestry rather than commit or tag dates is what keeps cancellation (§10.4)
// and the staleness screen (§13.7b) deterministic under merges, rebases and
// equal timestamps.
func (c *CLI) IsAncestor(ctx context.Context, a, b string) (bool, error) {
	if a == "" || b == "" {
		return false, nil
	}
	if a == b {
		return true, nil
	}
	cmd := exec.CommandContext(ctx, "git", "-C", c.Dir,
		"merge-base", "--is-ancestor", a, b)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	// Exit status 1 is the answer "no"; anything else is a real failure.
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("git merge-base --is-ancestor %s %s: %w: %s",
		a, b, err, strings.TrimSpace(stderr.String()))
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

// Record and field separators for the commit log. Both are ASCII control
// characters that cannot occur in a commit message, a SHA or a path, so the
// output splits unambiguously.
//
// The explicit field separator after %B is what makes the parse correct at
// all: a CCME message is *expected* to contain blank lines — the one after the
// header is required, and the footer block is preceded by another (§4.4) — so
// a parser that treats the first blank line as the end of the message
// truncates almost every well-formed message and reads its body and footers as
// file paths.
const (
	logRecordSep = "\x1e"
	logFieldSep  = "\x1f"
)

func (c *CLI) Commits(ctx context.Context, sinceTag string) ([]Commit, error) {
	rangeArg := "HEAD"
	if sinceTag != "" {
		rangeArg = sinceTag + "..HEAD"
	}

	out, err := c.run(ctx,
		"log",
		"--format="+logRecordSep+"%H"+logFieldSep+"%P"+logFieldSep+"%B"+logFieldSep,
		"--name-only",
		// §6.2: a merge commit's changed-file list is its diff against the
		// *first parent*. Without this git shows no diff for merges at all, so
		// every file-derived scope inside a merge silently resolves to nothing.
		// This does not change which commits are traversed — the window is
		// still every commit reachable from HEAD (§13.3).
		"--diff-merges=first-parent",
		rangeArg,
	)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(out) == "" {
		return nil, nil
	}

	var commits []Commit
	for _, record := range strings.Split(out, logRecordSep) {
		if strings.TrimSpace(record) == "" {
			continue
		}
		fields := strings.SplitN(record, logFieldSep, 4)
		if len(fields) < 3 {
			continue
		}
		commit := Commit{
			SHA:     strings.TrimSpace(fields[0]),
			Parents: strings.Fields(fields[1]),
			Message: strings.Trim(fields[2], "\n"),
		}
		if len(fields) > 3 {
			for _, line := range strings.Split(fields[3], "\n") {
				if line = strings.TrimRight(line, "\r"); line != "" {
					commit.Files = append(commit.Files, line)
				}
			}
		}
		commits = append(commits, commit)
	}

	return commits, nil
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
