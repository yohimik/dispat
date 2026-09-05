// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

// Package gitx wraps the git operations the tool needs behind an interface,
// with a CLI implementation that shells out to the git binary (matching CI
// environments exactly).
package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/pkg/ccme/v2"
)

// Tag is a "pkg@version" release tag. When the newest tag's version is not
// strict MAJOR.MINOR.PATCH (e.g. "pkg@0.0.1-0.0.0"), Parsed is false: Name is
// still usable as a git revision, but Version is meaningless and the caller
// must take the baseline from elsewhere (config initials).
type Tag struct {
	Name    string
	Version ccme.Version
	Parsed  bool
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

// LockTagName is the one tag name dispat reserves for itself: the release
// lock (see release.Lock), which says a release is running against this
// repository. It is a coordination ref and never a release record, so Tags
// keeps it out of every package's history whatever the tag format is broad
// enough to match — and it lives here, beside the reading of tags, because
// that is where the reservation has to hold.
const LockTagName = "dispat-release-lock"

// LockAttemptTagPrefix reserves the per-process local refs used to build an
// immutable lock object before it is offered under LockTagName remotely.
const LockAttemptTagPrefix = LockTagName + "-attempt-"

const (
	tagNamePlaceholder    = "{name}"
	tagVersionPlaceholder = "{version}"
	tagChannelPlaceholder = "{channel}"
	tagCounterPlaceholder = "{counter}"
	// Alias-only: the three numbers of the core version on their own.
	tagMajorPlaceholder = "{major}"
	tagMinorPlaceholder = "{minor}"
	tagPatchPlaceholder = "{patch}"
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

// Matches reports whether a tag belongs to a package under this format: the
// literal prefix and suffix check, and deliberately not ParseVersion.
//
// A tag can match the shape and still carry an unparseable version, which is
// the case the initials fallback exists for. It is also what a moving alias
// looks like: "v1" beside a "v{version}" tagFormat has the shape and no
// version in it. Telling those two apart is AliasFormat.Matches's job, not
// this one's.
func (f TagFormat) Matches(pkg, tag string) bool {
	prefix, suffix, ok := f.split(pkg)
	if !ok {
		return false
	}
	return len(tag) > len(prefix)+len(suffix) &&
		strings.HasPrefix(tag, prefix) && strings.HasSuffix(tag, suffix)
}

// Reader compiles this format for one package, so that a caller asking the
// same format about many names pays for the compile once. It is TagFormat's
// half of the pair AliasFormat.Matcher is the other half of.
func (f TagFormat) Reader(pkg string) VersionReader {
	tpl, err := f.template()
	if err != nil {
		return VersionReader{}
	}
	return VersionReader{tpl: tpl, pkg: pkg}
}

// VersionReader is one tag format compiled for one package: the question "what
// version does this package read out of that name?", asked repeatedly.
type VersionReader struct {
	tpl *tagTemplate
	pkg string
}

// ParseVersion extracts the version from a tag name. See
// TagFormat.ParseVersion. A reader built from a format that does not compile
// reads nothing, which is what that format's own ParseVersion answers too.
func (r VersionReader) ParseVersion(tag string) (ccme.Version, bool) {
	if r.tpl == nil {
		return ccme.Version{}, false
	}
	return r.tpl.parseVersion(r.pkg, tag)
}

// TagName renders a release tag under the default format. Callers that know a
// package's space should use its format instead.
//
// It is also what DISPAT_SEMVER_TAG carries: the same release named under the
// normative "{name}@{version}", so a script written against SemVer keeps a
// stable input whatever local convention the space's tagFormat encodes.
func TagName(pkg string, v ccme.Version) string { return DefaultTagFormat.Render(pkg, v) }

// AliasFormat is a template for an alias tag: an extra name a release is
// written under, beside the tag its package's TagFormat produces.
//
// It is deliberately a distinct type from TagFormat rather than the same one
// with looser rules. The two are validated differently and, more importantly,
// used differently: a TagFormat is written *and read*, and is how a package's
// history is found, while an AliasFormat is only ever written. Keeping them
// apart means no code path can read an alias back by accident, which is the
// one thing that would turn a convenience ref into a package's baseline.
//
// It accepts everything TagFormat does plus {major}, {minor} and {patch}, and
// needs at least one of those or {version}:
//
//	v{version}                  v1.4.2
//	v{major}                    v1
//	{name}-{major}.{minor}      core-1.4
type AliasFormat string

// Validate reports whether the format renders a name git will accept.
func (f AliasFormat) Validate() error {
	tpl := compileTagFormat(string(f))
	if err := tpl.validateAlias(); err != nil {
		return fmt.Errorf("alias tag format %q: %w", string(f), err)
	}
	samples := []ccme.Version{{Minor: 1}, {Minor: 1, Prerelease: []string{"beta", "4"}}}
	for _, v := range samples {
		sample := tpl.render("pkg", v)
		if err := validRefName(sample); err != nil {
			return fmt.Errorf("alias tag format %q would produce %q, which git rejects: %w",
				string(f), sample, err)
		}
	}
	return nil
}

// Render builds the alias name for a package version.
func (f AliasFormat) Render(pkg string, v ccme.Version) string {
	return compileTagFormat(string(f)).render(pkg, v)
}

// Matches reports whether a name is one this alias format could have written
// for a package: its literal text in place, and a number where it writes one.
//
// It exists so that a reader of a tag listing can tell an alias apart from a
// release that nobody can parse. The two look identical otherwise, and they
// call for opposite answers: an alias is not a release and belongs out of the
// listing, while a release tag carrying an unreadable version is exactly what
// the initials fallback is for and has to stay in.
func (f AliasFormat) Matches(pkg, tag string) bool {
	return f.Matcher(pkg).Matches(tag)
}

// Matcher compiles this format for one package, so that a caller reading a tag
// listing pays for the compile once rather than once per tag it looks at.
func (f AliasFormat) Matcher(pkg string) AliasMatcher {
	return AliasMatcher{tpl: compileTagFormat(string(f)), pkg: pkg}
}

// AliasMatcher is one alias format compiled for one package: the question
// "could this package's alias have written that name?", asked repeatedly.
type AliasMatcher struct {
	tpl *tagTemplate
	pkg string
}

// Matches reports whether the name is one this package's alias could have
// written. See AliasFormat.Matches.
func (m AliasMatcher) Matches(tag string) bool {
	if m.tpl == nil {
		return false
	}
	return m.tpl.matchesAlias(m.pkg, tag)
}

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
	// AuthorName and AuthorEmail are the commit's git author identity (%an and
	// %ae), which is what a release record attributes the work to. They are the
	// author rather than the committer on purpose: a rebase, a cherry-pick or a
	// squash-merge rewrites the committer and leaves the author alone, so the
	// committer would credit whoever last moved the commit.
	AuthorName  string
	AuthorEmail string
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
	// Tags.Baseline or Tags.StableBaseline. The planner calls it for many
	// packages concurrently, so implementations must be safe for concurrent
	// use.
	Tags(ctx context.Context, pkg string, format TagFormat) (Tags, error)
	// Commits lists commit messages lines reachable from HEAD, newest first.
	// When sinceTag is non-empty only commits after that tag are listed;
	// otherwise the whole history down to the first commit is used.
	Commits(ctx context.Context, sinceTag string) ([]Commit, error)
	// CreateTag creates an annotated tag at target, or at HEAD when target
	// is empty.
	CreateTag(ctx context.Context, name, message, target string) error
	// IsAncestor reports whether commit a is an ancestor-or-self of commit b
	// (§10.4). An implementation without ancestry knowledge — a test double
	// whose commits carry parent pointers instead — returns ErrNoAncestry
	// (embed NoAncestry for exactly that) and the planner falls back to those
	// pointers. Any other error aborts planning: a silently wrong ancestry
	// answer would change cancellation and prerelease-train containment.
	IsAncestor(ctx context.Context, a, b string) (bool, error)
	// IsShallow reports whether the repository's history is incomplete — a
	// shallow clone or a graft. The planner refuses to plan over one (§16
	// E196): hidden commits and tags make every window silently wrong.
	IsShallow(ctx context.Context) (bool, error)
}

// ErrNoAncestry is the IsAncestor answer of a Git implementation that cannot
// answer ancestry questions at all. It is a capability statement, not a
// failure: the caller uses its fallback for every ancestry question.
var ErrNoAncestry = errors.New("gitx: ancestry not available")

// NoAncestry is an embeddable IsAncestor stub for Git implementations that
// have no ancestry knowledge of their own.
type NoAncestry struct{}

// IsAncestor always answers ErrNoAncestry.
func (NoAncestry) IsAncestor(context.Context, string, string) (bool, error) {
	return false, ErrNoAncestry
}

// CLI is the Git implementation backed by the git executable.
type CLI struct {
	Dir string // repository root
	// Name and Email, when set, are the identity every commit and annotated
	// tag is created under (passed as `-c user.name/-c user.email`), so a CI
	// run needs no `git config` step. Empty fields fall back to git's own
	// configuration.
	Name  string
	Email string

	// Log traces every git invocation. The zero value discards, so a CLI
	// built without one still works; commands that have a logger set it, and
	// what comes out is the single most useful thing in a bug report about a
	// release: which git commands ran, in which order, and which one failed.
	// Trace rather than debug, because one run makes hundreds of these and
	// debug is where the release's own story is told — except the mutating
	// calls (commit, tag, push, the revert's checkout/clean), which are O(few)
	// per run and are exactly what a debug reader needs between those story
	// lines.
	Log zerolog.Logger

	// The ancestry DAG, loaded lazily by the first IsAncestor and shared by
	// every later one. Loaded once per CLI value: a release run creates
	// commits after planning, but planning's ancestry questions are all
	// asked against the history that existed when it started.
	dagOnce sync.Once
	dag     map[string][]string
	dagErr  error
}

var _ Git = (*CLI)(nil)

// mutates reports whether a git invocation changes repository state — the
// calls whose trace line rises to debug level. "tag --list" is the one
// read-only spelling sharing a subcommand with a mutation.
func mutates(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "push", "commit", "add", "checkout", "clean", "merge":
		return true
	case "tag":
		return len(args) > 1 && args[1] != "--list"
	}
	return false
}

func (c *CLI) run(ctx context.Context, args ...string) (string, error) {
	base := []string{"-C", c.Dir}
	if c.Name != "" {
		base = append(base, "-c", "user.name="+c.Name)
	}
	if c.Email != "" {
		base = append(base, "-c", "user.email="+c.Email)
	}
	cmd := exec.CommandContext(ctx, "git", append(base, args...)...)
	// git speaks the operator's language unless told otherwise, and one of
	// these answers is read rather than only shown: a push refused over a
	// branch that moved is recognised by its wording (see classifyPush). A
	// localised checkout would defeat that silently, so every invocation asks
	// for the C locale.
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	// git spawns no long-lived children here, but if one ever held the output
	// pipes past its exit, WaitDelay turns a silent hang into an error.
	cmd.WaitDelay = 10 * time.Second
	started := time.Now()
	err := cmd.Run()
	// Logged whether it worked or not, and with the same fields either way, so
	// a trace can be read as the sequence of git calls the run actually made.
	// The arguments are the command's own, never the repository path or the
	// identity flags, which are the same on every line and say nothing.
	level := zerolog.TraceLevel
	if mutates(args) {
		level = zerolog.DebugLevel
	}
	safeArgs := redactGitArgs(args)
	ev := c.Log.WithLevel(level).Strs("args", safeArgs).Dur("took", time.Since(started))
	if err != nil {
		safeStderr := strings.TrimSpace(redactGitOutput(stderr.String(), args))
		ev.Err(err).Str("stderr", safeStderr).Msg("git failed")
		return "", fmt.Errorf("git %s: %w: %s",
			strings.Join(safeArgs, " "), err, safeStderr)
	}
	ev.Int("outBytes", out.Len()).Msg("git")
	return out.String(), nil
}

func redactGitOutput(output string, args []string) string {
	for i, safe := range redactGitArgs(args) {
		if safe != args[i] {
			output = strings.ReplaceAll(output, args[i], safe)
		}
	}
	return gitOutputURL.ReplaceAllStringFunc(output, RedactURL)
}

var gitOutputURL = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://[^\s'"<>]+`)

// RedactURL removes user information, query strings and fragments from a
// remote URL before it is recorded. Named remotes are returned unchanged.
func RedactURL(value string) string {
	u, err := url.Parse(value)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return value
	}
	if u.User != nil {
		u.User = url.User("REDACTED")
	}
	if u.RawQuery != "" {
		u.RawQuery = "REDACTED"
	}
	if u.Fragment != "" {
		u.Fragment = "REDACTED"
	}
	return u.String()
}

// redactGitArgs removes credentials from URL-shaped arguments before they
// reach logs or returned errors. Git still receives the original arguments.
func redactGitArgs(args []string) []string {
	safe := append([]string(nil), args...)
	for i, arg := range safe {
		safe[i] = RedactURL(arg)
	}
	return safe
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
		"--format=%(refname:short)\t%(objectname)\t%(*objectname)",
		format.Glob(pkg))
	if err != nil {
		return nil, err
	}
	return parseTags(out, pkg, format), nil
}

// TagsForPackages returns the reachable tags for several packages from one
// ref inventory. Planning needs every package's tags at the same repository
// state; asking git to enumerate the same ref namespace once per package adds
// process and ref-walk overhead without adding information.
//
// The result is identical to calling Tags for each entry: each package still
// applies its own format matcher and parser, including custom formats whose
// globs overlap. Tags itself remains uncached and observes tags created after
// an earlier call, which callers outside one planning snapshot rely on.
func (c *CLI) TagsForPackages(ctx context.Context, formats map[string]TagFormat) (map[string]Tags, error) {
	result := make(map[string]Tags, len(formats))
	if len(formats) == 0 {
		return result, nil
	}
	out, err := c.run(ctx, "tag", "--list", "--merged", "HEAD",
		"--sort=-v:refname", "--sort=-creatordate",
		"--format=%(refname:short)\t%(objectname)\t%(*objectname)")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(formats))
	for name := range formats {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		result[name] = parseTags(out, name, formats[name].WithDefault())
	}
	return result, nil
}

func parseTags(out, pkg string, format TagFormat) Tags {
	var tags Tags
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 2 {
			continue
		}
		name := strings.TrimSpace(f[0])
		if name == LockTagName || strings.HasPrefix(name, LockAttemptTagPrefix) {
			// dispat's own coordination ref, which is on HEAD for the whole of
			// the run doing the planning. A format broad enough to match it —
			// "{version}" makes the glob "*" — would otherwise adopt it as the
			// package's newest tag and read the window as empty.
			continue
		}
		// The glob is not precise enough on its own: "*" matches any run of
		// characters, so under "{name}@{version}" the pattern "core@*" also
		// matches a tag of a package called "core@extra". Re-checking the
		// shape against the format is what keeps someone else's tags out
		// (§12.1).
		if !format.Matches(pkg, name) {
			continue
		}
		t := Tag{Name: name, Commit: strings.TrimSpace(f[1])}
		// An annotated tag's %(objectname) is the tag object; the commit is
		// the peeled %(*objectname). Lightweight tags leave it empty.
		if len(f) > 2 {
			if peeled := strings.TrimSpace(f[2]); peeled != "" {
				t.Commit = peeled
			}
		}
		if v, ok := format.ParseVersion(pkg, name); ok {
			t.Version, t.Parsed = v, true
		}
		tags = append(tags, t)
	}
	return tags
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
	// One `git rev-list --parents HEAD` loads the whole reachable DAG, after
	// which every ancestry question is an in-process walk. Planning asks this
	// question hundreds of times per run (baseline containment, cancels,
	// train windows), and paying a git process per question was the single
	// largest cost of `dispat status`.
	parents, err := c.commitDAG(ctx)
	if err != nil {
		return false, err
	}
	pa, pb := parents[a], parents[b]
	if pa != nil && pb != nil {
		return dagIsAncestor(parents, a, b), nil
	}
	// A commit outside HEAD's ancestry (or an abbreviated SHA): answer the
	// one question authoritatively instead of guessing from a partial graph.
	return c.mergeBaseIsAncestor(ctx, a, b)
}

// commitDAG returns the parent pointers of every commit reachable from HEAD,
// loaded once per CLI and reused for every ancestry question.
func (c *CLI) commitDAG(ctx context.Context) (map[string][]string, error) {
	c.dagOnce.Do(func() {
		out, err := c.run(ctx, "rev-list", "--parents", "HEAD")
		if err != nil {
			c.dagErr = err
			return
		}
		dag := make(map[string][]string)
		for line := range strings.Lines(out) {
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			dag[fields[0]] = fields[1:]
		}
		c.dag = dag
	})
	return c.dag, c.dagErr
}

// dagIsAncestor walks b's ancestry looking for a. The DAG is the repository's
// own history, so a plain iterative DFS with a seen-set is enough.
func dagIsAncestor(parents map[string][]string, a, b string) bool {
	seen := make(map[string]bool)
	stack := []string{b}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur == a {
			return true
		}
		if seen[cur] {
			continue
		}
		seen[cur] = true
		stack = append(stack, parents[cur]...)
	}
	return false
}

// mergeBaseIsAncestor is the per-question fallback for commits the loaded DAG
// does not cover.
func (c *CLI) mergeBaseIsAncestor(ctx context.Context, a, b string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", c.Dir,
		"merge-base", "--is-ancestor", a, b)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.WaitDelay = 10 * time.Second // same backstop as run()
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

// IsShallow reports whether the repository is a shallow clone.
func (c *CLI) IsShallow(ctx context.Context) (bool, error) {
	out, err := c.run(ctx, "rev-parse", "--is-shallow-repository")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "true", nil
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
//
// The message is deliberately the *last* fixed field before the file list: it
// is the only one that can contain anything, so everything of known shape is
// read before it and the split width stays exact. Adding a field means moving
// the message's index, which is why logCommitFields names it once.
const (
	logRecordSep = "\x1e"
	logFieldSep  = "\x1f"
	// logCommitFields is how many fields the format below emits before the
	// --name-only file list: sha, parents, author name, author email, message.
	// The file list is field logCommitFields, present only when the commit
	// changed anything.
	logCommitFields = 5
)

func (c *CLI) Commits(ctx context.Context, sinceTag string) ([]Commit, error) {
	rangeArg := "HEAD"
	if sinceTag != "" {
		rangeArg = sinceTag + "..HEAD"
	}

	out, err := c.run(ctx,
		"log",
		"--format="+logRecordSep+"%H"+logFieldSep+"%P"+logFieldSep+
			"%an"+logFieldSep+"%ae"+logFieldSep+"%B"+logFieldSep,
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
		fields := strings.SplitN(record, logFieldSep, logCommitFields+1)
		if len(fields) < logCommitFields {
			continue
		}
		commit := Commit{
			SHA:     strings.TrimSpace(fields[0]),
			Parents: strings.Fields(fields[1]),
			// Trimmed because git pads neither, but a name is free text and a
			// configured identity can carry trailing spaces the record would
			// otherwise render.
			AuthorName:  strings.TrimSpace(fields[2]),
			AuthorEmail: strings.TrimSpace(fields[3]),
			Message:     strings.Trim(fields[4], "\n"),
		}
		if len(fields) > logCommitFields {
			for _, line := range strings.Split(fields[logCommitFields], "\n") {
				if line = strings.TrimRight(line, "\r"); line != "" {
					commit.Files = append(commit.Files, line)
				}
			}
		}
		commits = append(commits, commit)
	}

	return commits, nil
}

// CreateTag creates an annotated tag at target (any commit-ish), or at HEAD
// when target is empty.
func (c *CLI) CreateTag(ctx context.Context, name, message, target string) error {
	return c.createTag(ctx, name, message, target, false)
}

// CreateTagForce is CreateTag with `git tag -f`: a name the repository already
// carries is rewritten instead of refused.
//
// It is what a moving tag needs — an alias like "v1" means "the newest 1.x"
// and has to be re-pointed on every release — and what keeps a run from
// dying on a tag some earlier attempt left behind. It is deliberately a
// separate method rather than a flag on CreateTag: overwriting a release
// record is not something a caller should be able to do by passing false.
func (c *CLI) CreateTagForce(ctx context.Context, name, message, target string) error {
	return c.createTag(ctx, name, message, target, true)
}

func (c *CLI) createTag(ctx context.Context, name, message, target string, force bool) error {
	args := []string{"tag"}
	if force {
		args = append(args, "-f")
	}
	args = append(args, "-a", name, "-m", message)
	if target != "" {
		args = append(args, target)
	}
	_, err := c.run(ctx, args...)
	return err
}

// DeleteTag removes a tag from this repository. It fails when the tag is not
// there, which callers that are cleaning up are free to ignore.
func (c *CLI) DeleteTag(ctx context.Context, name string) error {
	_, err := c.run(ctx, "tag", "-d", name)
	return err
}

// PushTag pushes one tag ref and nothing else: no branch moves, and no other
// tag travels with it.
//
// **It never forces, and must never learn to.** Unlike Push, whose tags are
// this run's own records and may be rewritten under commit.force, the caller
// here is contending for a name someone else may already hold. A rejection is
// the answer the caller asked for, not an obstacle to push through: forcing it
// would overwrite the holder's ref and tell both of them they won.
func (c *CLI) PushTag(ctx context.Context, remote, name string) error {
	_, err := c.run(ctx, "push", remote, "refs/tags/"+name)
	return err
}

// PushObjectToTag creates name on remote from the immutable object oid. The
// destination is never forced: an existing lock must make acquisition fail.
// Naming the source object, rather than a mutable local ref, also makes this
// safe when two dispat processes share one checkout.
func (c *CLI) PushObjectToTag(ctx context.Context, remote, oid, name string) error {
	_, err := c.run(ctx, "push", remote, oid+":refs/tags/"+name)
	return err
}

// TagObject resolves the tag object itself (without peeling it to its commit).
func (c *CLI) TagObject(ctx context.Context, name string) (string, error) {
	out, err := c.run(ctx, "rev-parse", "refs/tags/"+name)
	return strings.TrimSpace(out), err
}

// DeleteRemoteTag removes a tag from the remote. Deleting a ref the remote
// does not have succeeds: git warns and reports the deletion, because the
// fully qualified refspec leaves nothing to guess about. Cleanup is therefore
// idempotent on this side, unlike DeleteTag.
func (c *CLI) DeleteRemoteTag(ctx context.Context, remote, name string) error {
	_, err := c.run(ctx, "push", remote, "--delete", "refs/tags/"+name)
	return err
}

// DeleteRemoteTagLease deletes name only while it still names expectedOID.
// If ownership changed, git rejects the operation and preserves the new
// owner's lock.
func (c *CLI) DeleteRemoteTagLease(ctx context.Context, remote, name, expectedOID string) error {
	ref := "refs/tags/" + name
	_, err := c.run(ctx, "push", "--force-with-lease="+ref+":"+expectedOID,
		remote, ":"+ref)
	return err
}

// TagExists reports whether the named tag exists in this repository.
func (c *CLI) TagExists(ctx context.Context, name string) (bool, error) {
	_, err := c.run(ctx, "rev-parse", "-q", "--verify", "refs/tags/"+name)
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// RemoteTagMessage reads an annotated tag's message from the remote without
// touching this clone's refs: the fetch lands the object in FETCH_HEAD only.
// A lightweight tag has no message and comes back empty.
func (c *CLI) RemoteTagMessage(ctx context.Context, remote, name string) (string, error) {
	if _, err := c.run(ctx, "fetch", "--no-tags", remote, "refs/tags/"+name); err != nil {
		return "", err
	}
	out, err := c.run(ctx, "cat-file", "-p", "FETCH_HEAD")
	if err != nil {
		return "", err
	}
	// An annotated tag prints its headers, a blank line, then the message; a
	// peeled or lightweight ref prints a commit instead, which has no message
	// of the tag's own to offer.
	if !strings.HasPrefix(out, "object ") {
		return "", nil
	}
	if i := strings.Index(out, "\n\n"); i >= 0 {
		return out[i+2:], nil
	}
	return "", nil
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
	paths := make([]string, 0, len(dirs))
	for _, d := range dirs {
		paths = append(paths, c.pathspec(d))
	}
	// Check only this operation's paths. Unrelated staged changes belong to
	// the caller and must neither cause nor enter this commit.
	diffArgs := append([]string{"diff", "--cached", "--quiet", "--"}, paths...)
	if _, err := c.run(ctx, diffArgs...); err == nil {
		return false, nil // nothing staged
	}
	commitArgs := append([]string{"commit", "--only", "-m", message, "--"}, paths...)
	if _, err := c.run(ctx, commitArgs...); err != nil {
		return false, err
	}
	return true, nil
}

// DirtyPaths returns tracked, staged, and untracked paths beneath dirs. It is
// used before release work so automatic rollback and commit cannot overwrite
// changes that predate the run.
func (c *CLI) DirtyPaths(ctx context.Context, dirs []string) ([]string, error) {
	args := []string{"status", "--porcelain=v1", "-z", "--untracked-files=all", "--"}
	for _, d := range dirs {
		args = append(args, c.pathspec(d))
	}
	out, err := c.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	var paths []string
	entries := strings.Split(out, "\x00")
	for i := 0; i < len(entries); i++ {
		entry := entries[i]
		if len(entry) < 4 {
			continue
		}
		path := entry[3:]
		paths = append(paths, path)
		// Porcelain -z lists the destination followed by the source for
		// renames and copies. The source has no status prefix of its own.
		if strings.ContainsAny(entry[:2], "RC") {
			i++
		}
	}
	return paths, nil
}

// HeadSHA returns the full SHA of the current HEAD commit.
func (c *CLI) HeadSHA(ctx context.Context) (string, error) {
	return c.ResolveCommit(ctx, "HEAD")
}

// ResolveCommit resolves any commit-ish (a short SHA, a ref, HEAD) to its
// full commit SHA, peeling tags on the way.
func (c *CLI) ResolveCommit(ctx context.Context, rev string) (string, error) {
	out, err := c.run(ctx, "rev-parse", rev+"^{commit}")
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

// CurrentBranch returns the name of the checked-out branch, or "" when HEAD is
// detached.
func (c *CLI) CurrentBranch(ctx context.Context) (string, error) {
	out, err := c.run(ctx, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(out)
	if name == "HEAD" { // rev-parse's spelling of "detached"
		return "", nil
	}
	return name, nil
}

// BehindRemote reports whether the remote's tip of branch holds commits HEAD
// does not: the checkout is stale, so a release planned here would be planned
// against tags someone else has already moved past.
//
// Two cases are deliberately not "behind". A branch the remote does not have
// yet is not behind, because the first push is what creates it. A remote tip
// this clone has never fetched is behind by definition — that is exactly the
// unfetched-someone-else's-commit case the check exists for — which is why a
// failed ResolveCommit answers true rather than propagating: the object is
// missing because it is new, and the caller's remedy (pull) is the same either
// way.
func (c *CLI) BehindRemote(ctx context.Context, remote, branch string) (bool, error) {
	ref := "refs/heads/" + branch
	out, err := c.run(ctx, "ls-remote", remote, ref)
	if err != nil {
		return false, err
	}
	// ls-remote arguments are tail-matching patterns, so a branch literally
	// named "x/refs/heads/main" would list too: keep only the exact ref.
	var tip string
	for _, line := range strings.Split(out, "\n") {
		if sha, name, ok := strings.Cut(strings.TrimSpace(line), "\t"); ok && name == ref {
			tip = sha
			break
		}
	}
	if tip == "" {
		return false, nil
	}
	if _, err := c.ResolveCommit(ctx, tip); err != nil {
		return true, nil
	}
	head, err := c.HeadSHA(ctx)
	if err != nil {
		return false, err
	}
	contained, err := c.IsAncestor(ctx, tip, head)
	if err != nil {
		return false, err
	}
	return !contained, nil
}

// RemoteTags returns the names of the tags that exist on the remote.
func (c *CLI) RemoteTags(ctx context.Context, remote string) (map[string]bool, error) {
	out, err := c.run(ctx, "ls-remote", "--tags", remote)
	if err != nil {
		return nil, err
	}
	tags := make(map[string]bool)
	for _, line := range strings.Split(out, "\n") {
		_, ref, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		// Annotated tags list twice: refs/tags/x and the peeled refs/tags/x^{}.
		name := strings.TrimSuffix(strings.TrimPrefix(ref, "refs/tags/"), "^{}")
		if name != "" && name != ref {
			tags[name] = true
		}
	}
	return tags, nil
}

// ErrRejected is what a push refused by the remote because the branch has
// moved under it answers with: somebody landed commits on it while this run
// was working. It is a recoverable answer rather than a failure, which is why
// it is a sentinel: the caller replays its work on the new tip and pushes
// again.
var ErrRejected = errors.New("the remote branch has commits this push does not build on")

// rejectedPhrases are how git spells that refusal. The exact wording depends
// on the version and on whether the remote ref was fetched, so the test is on
// the phrases every spelling shares rather than on one sentence.
var rejectedPhrases = []string{"non-fast-forward", "fetch first", "stale info", "cannot lock ref"}

// classifyPush turns a push failure into ErrRejected when the remote refused
// it over a branch that moved, and leaves every other failure alone: a missing
// credential and a moved branch call for entirely different answers, and
// merging into a remote nobody could reach would be neither.
//
// The marker is "rejected]" rather than "[rejected]", because git writes
// "[remote rejected]" when the far side refused the update itself, which is
// what two runs pushing the same branch at the same instant produce
// ("cannot lock ref"). Matching the opening bracket read that as an ordinary
// failure and left the whole phrase list unreachable for it.
func classifyPush(err error) error {
	if err == nil {
		return nil
	}
	text := err.Error()
	if !strings.Contains(text, "rejected]") && !strings.Contains(text, "Updates were rejected") {
		return err
	}
	for _, phrase := range rejectedPhrases {
		if strings.Contains(text, phrase) {
			// git's own text leads, because that is what a reader of a failed
			// release needs first; the sentinel stays in the chain for
			// errors.Is, which is the only thing that reads it.
			return fmt.Errorf("%v: %w", err, ErrRejected)
		}
	}
	return err
}

// MergeConflict is what a merge that stopped on content answers with: the
// paths git could not join, still unmerged in the index, with the merge left
// in progress for the caller to finish.
//
// It is a value rather than a failure because the caller has an answer for it.
// A release that reaches this point has already published, and abandoning the
// merge would leave the release commit and its tags nowhere but this clone.
type MergeConflict struct{ Paths []string }

func (c *MergeConflict) Error() string {
	return "the merge conflicts in " + strings.Join(c.Paths, ", ")
}

// MergeRemote joins the remote's tip of branch into the checked-out branch:
// the recovery from a push refused because someone landed work while the
// release ran.
//
// A merge rather than a rebase, and that is the whole design. Nothing this run
// already made is rewritten, so the release commit keeps its identity, the
// tags this leg wrote still name the commit they were written on, and a commit
// a package's own script exported is still the commit it exported. The only
// thing that changes is the branch's tip.
//
// The merge is made from the branch, so the release commit is the first parent
// and the commits that arrived are the second. Either order would do for the
// planner, which reads the merge's own message and finds a scope it exempts,
// but this order is the one a single command can make and a single command can
// undo.
//
// A merge that stops on conflicting content is left in progress and reported
// as *MergeConflict, because that is a state the caller resolves rather than
// an error it reports. A merge that fails for any other reason is aborted
// here, so the working tree is left exactly as the run had it.
//
// The fetch is deliberately --no-tags: the tags this run just created are its
// own records, and pulling the remote's would be a second, unrelated change to
// the refs under a run that is already recovering from one surprise.
func (c *CLI) MergeRemote(ctx context.Context, remote, branch, message string) error {
	if _, err := c.run(ctx, "fetch", "--no-tags", remote, branch); err != nil {
		return err
	}
	// --no-ff for two reasons: a repository configured with merge.ff=only
	// refuses the merge outright without it, and the first-parent shape this
	// recovery documents is only a shape when there is a merge commit to have
	// one.
	if _, err := c.run(ctx, "merge", "--no-ff", "--no-edit", "-m", message, "FETCH_HEAD"); err != nil {
		if paths, uErr := c.UnmergedPaths(ctx); uErr == nil && len(paths) > 0 {
			return &MergeConflict{Paths: paths}
		}
		// The abort's own failure is not what the caller needs to hear about:
		// the merge is the thing that did not work, and saying so twice would
		// bury it.
		if abortErr := c.AbortMerge(ctx); abortErr != nil {
			c.Log.Warn().Err(abortErr).Msg("could not abort the merge")
		}
		return err
	}
	return nil
}

// UnmergedPaths are the paths a stopped merge left unresolved in the index.
func (c *CLI) UnmergedPaths(ctx context.Context) ([]string, error) {
	out, err := c.run(ctx, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		if p := strings.TrimSpace(line); p != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

// AbortMerge undoes a merge in progress, leaving the working tree as it was.
func (c *CLI) AbortMerge(ctx context.Context) error {
	_, err := c.run(ctx, "merge", "--abort")
	return err
}

// ResolveOurs settles every named unmerged path by taking this side of it, and
// leaves the result staged.
//
// This side, always. What is on this side is the release: a tree that was
// planned, built, published and tagged, and the tag already names it. Taking
// anything else would publish content the release never saw, so the other
// side is preserved somewhere it can be read instead (see the quarantine
// branch the caller pushes).
//
// A path this side deleted cannot be checked out, and is removed instead;
// every other shape of conflict, content or add/add, resolves to the file this
// side has.
func (c *CLI) ResolveOurs(ctx context.Context, paths []string) error {
	for _, path := range paths {
		if _, err := c.run(ctx, "checkout", "--ours", "--", path); err != nil {
			if _, rmErr := c.run(ctx, "rm", "-q", "-f", "--", path); rmErr != nil {
				return fmt.Errorf("resolving %s: %w", path, err)
			}
			continue
		}
		if _, err := c.run(ctx, "add", "--", path); err != nil {
			return fmt.Errorf("staging %s: %w", path, err)
		}
	}
	return nil
}

// StageFile adds one path to the index, which is how a caller puts something
// of its own into a merge commit before finishing it.
func (c *CLI) StageFile(ctx context.Context, path string) error {
	_, err := c.run(ctx, "add", "--", path)
	return err
}

// CommitMerge finishes a merge in progress with the message it was started
// with, whatever the caller resolved and staged in the meantime.
func (c *CLI) CommitMerge(ctx context.Context) error {
	_, err := c.run(ctx, "commit", "--no-edit")
	return err
}

// PushBranchAt creates a branch on the remote at rev, and refuses to touch one
// that is already there.
//
// Never forced, and deliberately so: this is where work somebody else pushed
// is put so it stays readable, and overwriting it would lose exactly what it
// exists to preserve. A name already taken is a failure rather than a fallback
// name, because the naming scheme makes a collision practically impossible and
// a surprise is worth stopping on.
func (c *CLI) PushBranchAt(ctx context.Context, remote, rev, name string) error {
	if err := ValidRefName(name); err != nil {
		return fmt.Errorf("%q: %w", name, err)
	}
	existing, err := c.run(ctx, "ls-remote", "--heads", remote, "refs/heads/"+name)
	if err != nil {
		return err
	}
	if strings.TrimSpace(existing) != "" {
		return fmt.Errorf("%s already has a branch called %s", remote, name)
	}
	_, err = c.run(ctx, "push", remote, rev+":refs/heads/"+name)
	return err
}

// ValidRefName reports whether git would accept name as a ref. It is the same
// check the alias tag formats are validated with, exposed for the callers that
// build a ref name out of things a person configured.
func ValidRefName(name string) error { return validRefName(name) }

// PushReport says what the push did about tags the remote already carried.// PushReport says what the push did about tags the remote already carried.
// Exactly one of the two lists is ever populated, decided by force.
type PushReport struct {
	// Skipped are tags left as they were, because the remote already had
	// them and force was off.
	Skipped []string
	// Replaced are tags the remote already had and that were overwritten.
	Replaced []string
}

// Push pushes the current branch (HEAD) and then the given tags to the remote.
// Requires a checked-out branch (not a detached HEAD).
//
// With force, the tags are pushed with --force and a tag the remote already
// carries is overwritten; the report names those, because replacing a
// published ref is worth saying out loud even when it is what was asked for.
// Without it, such a tag is left alone and reported as skipped, so a re-run
// after a partially pushed release converges instead of dying on "already
// exists".
//
// **The branch is never force pushed.** A rejected branch push means someone
// else pushed while this run was working, and the answer to that is to look,
// not to overwrite their commits. Only the tag refs, which are dispat's own
// namespace, are ever forced. A refusal of that kind comes back wrapping
// ErrRejected, so the caller can join what landed with MergeRemote and push
// again rather than reporting a release that never landed.
func (c *CLI) Push(ctx context.Context, remote string, tags []string, force bool) (PushReport, error) {
	var report PushReport
	if _, err := c.run(ctx, "push", remote, "HEAD"); err != nil {
		return report, classifyPush(err)
	}
	if len(tags) == 0 {
		return report, nil
	}
	existing, err := c.RemoteTags(ctx, remote)
	if err != nil {
		return report, err
	}
	refs := make([]string, 0, len(tags))
	for _, t := range tags {
		switch {
		case !existing[t]:
		case force:
			report.Replaced = append(report.Replaced, t)
		default:
			report.Skipped = append(report.Skipped, t)
			continue
		}
		refs = append(refs, "refs/tags/"+t)
	}
	if len(refs) == 0 {
		return report, nil
	}
	args := []string{"push"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, remote)
	if _, err := c.run(ctx, append(args, refs...)...); err != nil {
		return report, err
	}
	return report, nil
}
