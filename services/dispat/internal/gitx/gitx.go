// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

// Package gitx wraps the git operations the tool needs behind an interface,
// with a local implementation that shells out to the git binary (matching CI
// environments exactly).
package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/services/dispat/internal/script"
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
// too, so one pattern covers both shapes. The pattern is only a filter: IsMatch
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

// IsMatch reports whether a name is one this alias format could have written
// for a package: its literal text in place, and a number where it writes one.
//
// It exists so that a reader of a tag listing can tell an alias apart from a
// release that nobody can parse. The two look identical otherwise, and they
// call for opposite answers: an alias is not a release and belongs out of the
// listing, while a release tag carrying an unreadable version is exactly what
// the initials fallback is for and has to stay in.
func (f AliasFormat) IsMatch(pkg, tag string) bool {
	return f.Matcher(pkg).IsMatch(tag)
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

// IsMatch reports whether the name is one this package's alias could have
// written. See AliasFormat.IsMatch.
func (m AliasMatcher) IsMatch(tag string) bool {
	if m.tpl == nil {
		return false
	}
	return m.tpl.matchesAlias(m.pkg, tag)
}

// literalPrefix is text every name this matcher accepts begins with: the
// literal and {name} segments a shape opens with, taken over every shape the
// format renders, since a format that spells its prerelease drops that whole
// section for a stable version and may open differently for it. The match is
// anchored and byte-exact over those segments, so a name that does not carry
// the prefix cannot match and need not be tried.
func (m AliasMatcher) literalPrefix() string {
	if m.tpl == nil {
		return ""
	}
	shapes := []shape{shapeFull}
	if m.tpl.spellsPrerelease() {
		shapes = []shape{shapeFull, shapeNoCount, shapeStable}
	}
	prefix := ""
	for i, sh := range shapes {
		var lead strings.Builder
	segments:
		for _, seg := range m.tpl.reduce(sh) {
			switch seg.kind {
			case segLiteral:
				lead.WriteString(seg.text)
			case segName:
				lead.WriteString(m.pkg)
			default:
				break segments
			}
		}
		if i == 0 {
			prefix = lead.String()
			continue
		}
		n := 0
		for other := lead.String(); n < len(prefix) && n < len(other) && prefix[n] == other[n]; {
			n++
		}
		prefix = prefix[:n]
	}
	return prefix
}

// AliasIndex answers whether any of a workspace's alias formats could have
// written a name, by trying only the formats whose literal prefix the name
// carries. Every package writes aliases under its own name, so a listing of T
// unparsed tags against P packages' aliases is T walks of a name's bytes
// where trying every format for every tag is T times P matches.
type AliasIndex struct {
	root *tagPrefixNode[AliasMatcher]
	size int
}

// NewAliasIndex indexes the matchers. The zero AliasIndex matches nothing.
func NewAliasIndex(matchers []AliasMatcher) AliasIndex {
	root := &tagPrefixNode[AliasMatcher]{}
	for _, m := range matchers {
		root.add(m.literalPrefix(), m)
	}
	return AliasIndex{root: root, size: len(matchers)}
}

// Len is the number of alias formats indexed.
func (x AliasIndex) Len() int { return x.size }

// IsMatch reports whether any indexed alias could have written the name.
func (x AliasIndex) IsMatch(tag string) bool {
	node := x.root
	for depth := 0; node != nil; depth++ {
		for _, m := range node.matchers {
			if m.IsMatch(tag) {
				return true
			}
		}
		if depth == len(tag) {
			break
		}
		node = node.child(tag[depth])
	}
	return false
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
	// AreFilesDeferred reports that Files was not read with the commit: the
	// implementation lists a commit's changed paths on request instead
	// (ChangedFilesx), and the caller asks for the commits whose paths it
	// needs. Listing paths means diffing every commit against its parent,
	// which is most of what reading a history costs, and only a commit
	// carrying a unit whose scope comes from its files (§6.2) ever reads them.
	AreFilesDeferred bool
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

// Gitx abstracts the repository operations used by planning and publishing.
type Gitx interface {
	// Tags returns every reachable tag of the package under format, newest
	// first by creation date. Callers select a baseline from it with
	// Tags.Baseline or Tags.StableBaseline. The planner calls it for many
	// packages concurrently, so implementations must be safe for concurrent
	// use.
	Tags(ctx context.Context, pkg string, format TagFormat) (Tags, error)
	// Commits lists commit messages lines reachable from HEAD, newest first.
	// When sinceTag is non-empty only commits after that tag are listed;
	// otherwise the whole history down to the first commit is used. An
	// implementation with ChangedFilesx leaves each commit's Files to it and
	// says so with AreFilesDeferred.
	Commits(ctx context.Context, sinceTag string) ([]Commit, error)
	// CreateTag creates an annotated tag at target, or at HEAD when target
	// is empty.
	CreateTag(ctx context.Context, name, message, target string) error
	// IsAncestor reports whether commit a is an ancestor-or-self of commit b
	// (§10.4). An implementation without ancestry knowledge — a test double
	// whose commits carry parent pointers instead — returns ErrNoAncestry,
	// and the planner falls back to those
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

// CommitProbex is the optional Gitx capability that answers whether a
// repository holds an object at all, which is a different question from where
// that object sits in the graph.
//
// The two are separate because a polyrepository control snapshot can pin a
// source revision the local source clone has never held: the pointer lives in
// the control tree, not in the source. Asking git an ancestry question about
// such a revision fails the command, and that failure is the truth about the
// command but not about the fleet — the answer planning needs is "this
// checkout does not contain it". Implementations without the capability are
// simply not asked; the caller keeps its ordinary ancestry path.
type CommitProbex interface {
	// IsCommitPresent reports whether rev resolves to a commit object in this
	// repository. An absent or unparsable revision is the answer false with a
	// nil error; only a repository that cannot be read at all is an error.
	IsCommitPresent(ctx context.Context, rev string) (bool, error)
}

// UnionHistoryx is the optional Gitx capability behind the planner's single
// history read and its marker pass (CCME §13.11).
//
// A plan needs one pending window per distinct baseline commit. Read one at a
// time, the messages and changed paths of the commits those windows share are
// read and parsed once per window. The windows are nested or overlapping views
// of one history, so their union is one walk, and every window is recovered
// from it by ancestry alone.
//
// An implementation promises three things, and the planner relies on each:
//
//   - the result is exactly the union of what Commits returns for every
//     boundary, a boundary being a full commit id;
//   - the commits of any one of those windows appear in the result in the
//     order Commits lists them, so a window is a subsequence of the union and
//     the precedence that order carries (§11.6) does not move;
//   - every Commit carries its complete parent list, so ancestry among the
//     returned commits is what their Parents say, and IsAncestor need not be
//     asked about two of them.
//
// Every boundary has to be an ancestor-or-self of HEAD, and the implementation
// checks it, answering ErrBoundaryNotBehindHead otherwise. The planner recovers
// a window by ancestry inside the union, which is exact only then: a boundary
// on a line HEAD never merged excludes commits of the union without being in
// it, and nothing in the union says so. A tag is behind HEAD by construction
// (Tags lists reachable tags only); a revision pinned by another repository
// need not be.
//
// Implementations without the capability are simply not asked: the planner
// reads one window per boundary and asks IsAncestor, exactly as before.
type UnionHistoryx interface {
	CommitsSinceAny(ctx context.Context, boundaries []string) ([]Commit, error)
}

// ChangedFilesx is the optional Gitx capability that lists commits' changed
// paths apart from their messages (§6.2, CCME §13.11). An implementation with
// it answers Commits and CommitsSinceAny without paths, marking each commit
// AreFilesDeferred, and the caller asks ChangedFiles for the commits whose
// scope derives from their files.
//
// The answer for a commit is its changed-file list of §6.2: the changes
// against the first parent for a merge, every path of a root commit, both
// paths of a rename, a deleted path and a path whose mode alone changed, each
// spelled exactly as the repository records it.
type ChangedFilesx interface {
	ChangedFiles(ctx context.Context, commits []string) (map[string][]string, error)
}

// ErrBoundaryNotBehindHead is CommitsSinceAny declining a boundary HEAD does
// not descend from. It is not a failure: the caller reads that history one
// window at a time instead.
var ErrBoundaryNotBehindHead = errors.New("gitx: a union history boundary is not an ancestor of HEAD")

// LocalGitx implements Gitx through the local git executable.
type LocalGitx struct {
	Dir string // repository root
	// Name and Email, when set, are the identity every commit and annotated
	// tag is created under (passed as `-c user.name/-c user.email`), so a CI
	// run needs no `git config` step. Empty fields fall back to git's own
	// configuration.
	Name  string
	Email string

	// LinkPaths are the repository-relative gitlink paths holding a
	// choreographed fleet's links. They are excluded from every pathspec this
	// type builds for the release's own work: a fleet link's pin is advisory
	// between settlements, so a link left ahead of its committed value would
	// otherwise make the whole repository look dirty and be swept into the
	// next release commit. Empty for an orchestrated or single repository,
	// which is what keeps their command lines exactly as they were.
	LinkPaths []string

	// Log traces every git invocation. The zero value discards, so a LocalGitx
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
	// every later one. Loaded once per LocalGitx value: a release run creates
	// commits after planning, but planning's ancestry questions are all
	// asked against the history that existed when it started.
	//
	// A mutex rather than sync.Once because the load takes the caller's
	// context: a first caller whose context is cancelled must not settle the
	// answer for every later one.
	dagMu  sync.Mutex
	dag    *commitGraph
	dagErr error
}

var _ Gitx = (*LocalGitx)(nil)

// mutates reports whether a git invocation changes repository state — the
// calls whose trace line rises to debug level. "tag --list" is the one
// read-only spelling sharing a subcommand with a mutation.
func mutates(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "push", "commit", "add", "checkout", "clean", "merge", "restore",
		// The plumbing a choreographed settlement writes with: the fleet
		// links it stages, the commit object it creates, the ref it moves,
		// and the checkout a link is materialized by. Each is O(few) per run
		// and each changes the repository, so each belongs beside the other
		// mutations a debug reader follows a release by.
		"submodule", "update-index", "update-ref", "commit-tree":
		return true
	case "tag":
		return len(args) > 1 && args[1] != "--list"
	}
	return false
}

// gitInvocations counts every git subprocess this process has started.
//
// It exists because the cost of a run is mostly the number of git processes it
// forks, and nothing else measures that: plan.HistoryStats counts the
// planner's own calls and says nothing about discovery, recording or the
// locks. A benchmark or a test that claims one part of the tool asks git less
// often can read this and show it.
//
// It is a counter and not a cap. The pools that fork git are independent by
// design, and a process-wide limit would couple them.
var gitInvocations atomic.Uint64

// GitInvocations reports how many git subprocesses this process has started.
// Exported for benchmarks and for the tests that assert a call was not made.
func GitInvocations() uint64 { return gitInvocations.Load() }

// gitOutputBytes counts the standard output every git subprocess this process
// started has written, buffered or streamed. The number of processes says what
// a run pays in forks; this says what it pays in reading and parsing, which is
// the other half of a history read's cost.
var gitOutputBytes atomic.Uint64

// GitOutputBytes reports how many bytes of standard output git subprocesses
// have written to this process. Exported for benchmarks.
func GitOutputBytes() uint64 { return gitOutputBytes.Load() }

// commitsDiffed counts the commits whose changed paths a git subprocess
// computed for this process: every commit of a history read that lists
// paths. Diffing a commit against its parent is the expensive part of such a
// read, and most of the paths it produces are never looked at.
var commitsDiffed atomic.Uint64

// CommitsDiffed reports how many commits git has diffed to list their changed
// paths for this process. Exported for benchmarks.
func CommitsDiffed() uint64 { return commitsDiffed.Load() }

func (c *LocalGitx) run(ctx context.Context, args ...string) (string, error) {
	return c.runEnv(ctx, nil, args...)
}

// runEnv is run with extra environment variables for this invocation alone.
//
// It exists for the one thing git takes from the environment rather than from
// its arguments: GIT_INDEX_FILE, which is how a commit can be built from a
// temporary index without the repository's own index or worktree being touched
// at all. The variables are appended, so they win over the inherited ones.
//
// A failed invocation returns no output, as it always has: a caller that
// ignores the error must not be handed whatever git happened to print before
// it failed (`rev-parse` echoes an unknown argument on its way out). The one
// family that reads its answer off a failed command asks runStream directly.
func (c *LocalGitx) runEnv(ctx context.Context, env []string, args ...string) (string, error) {
	out, err := c.runStream(ctx, gitStream{env: env}, args...)
	if err != nil {
		return "", err
	}
	return out, nil
}

// gitStream is the input and output of one invocation that does not fit in a
// returned string: a blob hashed from a reader, a blob written to a writer, a
// listing scanned entry by entry. Its zero value is the buffered invocation
// every other caller makes.
type gitStream struct {
	stdin  io.Reader
	stdout io.Writer
	env    []string
}

// newCommand keeps Git's automatic maintenance in this process tree. A worker
// can poll a mailbox for days; detached maintenance would become a zombie
// under a container PID 1 that does not reap grandchildren. These request-scoped
// options preserve automatic maintenance without changing repository config.
func (c *LocalGitx) newCommand(ctx context.Context, args ...string) *exec.Cmd {
	base := []string{
		"-c", "maintenance.autoDetach=false",
		"-c", "gc.autoDetach=false",
	}
	if c.Name != "" {
		base = append(base, "-c", "user.name="+c.Name)
	}
	if c.Email != "" {
		base = append(base, "-c", "user.email="+c.Email)
	}
	base = append(base, "-C", c.Dir)
	return exec.CommandContext(ctx, "git", append(base, args...)...)
}

// runStream is the shared runner for Git commands with buffered or streamed
// output. It exists apart from runEnv so that the transport's bounded
// streaming (see transport.go) shares the identical environment, process
// group, cancellation, redaction and trace logging.
//
// The standard output is returned whether or not git succeeded. One family of
// commands reports its machine-readable answer there and still exits
// non-zero: `git push --porcelain` writes a status flag per ref and exits 1
// when any one of them was rejected, and telling a rejected lease from an
// unreachable remote is exactly what the caller has to do. Only the callers
// that need that answer come here; run and runEnv keep the contract every
// other caller was written against and return nothing beside an error.
func (c *LocalGitx) runStream(ctx context.Context, stream gitStream, args ...string) (string, error) {
	gitInvocations.Add(1)
	cmd := c.newCommand(ctx, args...)
	// git speaks the operator's language unless told otherwise, and one of
	// these answers is read rather than only shown: a push refused over a
	// branch that moved is recognised by its wording (see classifyPush). A
	// localised checkout would defeat that silently, so every invocation asks
	// for the C locale.
	cmd.Env = append(append(os.Environ(), "LC_ALL=C", "LANG=C"), stream.env...)
	var out, stderr bytes.Buffer
	cmd.Stdin = stream.stdin
	cmd.Stdout = &out
	written := &countingWriter{to: stream.stdout}
	if stream.stdout != nil {
		cmd.Stdout = written
	}
	cmd.Stderr = &stderr
	// A network call forks ssh, a credential helper or git-remote-https, and
	// those inherit the output pipes. Killing git alone would leave them
	// running and hold Wait until WaitDelay expired, so cancellation signals
	// the whole group. WaitDelay remains the backstop for anything that
	// escapes it.
	script.SetProcessGroup(cmd)
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
		gitOutputBytes.Add(uint64(out.Len() + written.count))
		return out.String(), fmt.Errorf("git %s: %w: %s",
			strings.Join(safeArgs, " "), err, safeStderr)
	}
	ev.Int("outBytes", out.Len()+written.count).Msg("git")
	gitOutputBytes.Add(uint64(out.Len() + written.count))
	return out.String(), nil
}

// countingWriter forwards to a caller's writer and remembers how much went
// through, so an invocation whose output never lands in a buffer still writes
// the same trace line as every other one.
type countingWriter struct {
	to    io.Writer
	count int
}

func (w *countingWriter) Write(p []byte) (int, error) {
	n, err := w.to.Write(p)
	w.count += n
	return n, err
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
// remote URL before it is recorded. Named remotes are returned unchanged, and
// so is the scp-like host:path form unless its user half carries a password.
// A value that names a scheme and still does not parse, a password holding a
// stray `%` or `#` above all, has everything up to its last `@` cut instead:
// what could not be parsed cannot be trusted to hold no credential.
func RedactURL(value string) string {
	u, err := url.Parse(value)
	if isMalformedURL(value, u, err) {
		return redactMalformedURL(value)
	}
	if err != nil || u.Scheme == "" || u.Host == "" {
		return redactScpPassword(value)
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

// isMalformedURL reports whether a value names a scheme and an authority and
// Go's parser could not read an authority out of it. A path after the scheme,
// `file:///srv/app.git`, has no authority to hold a credential and is not one.
func isMalformedURL(value string, u *url.URL, err error) bool {
	scheme, rest, hasScheme := strings.Cut(value, "://")
	if !hasScheme || scheme == "" || strings.ContainsAny(scheme, "/@:") || strings.HasPrefix(rest, "/") {
		return false
	}
	return err != nil || u.Host == ""
}

// redactMalformedURL cuts the user information of a scheme-carrying value Go
// could not parse, the way RedactEndpoint does, and its query and fragment
// after it. The cut is at the last `@`, because a password is exactly where a
// stray `@`, `/` or `#` would be.
func redactMalformedURL(value string) string {
	scheme, rest, _ := strings.Cut(value, "://")
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		rest = "REDACTED@" + rest[at+1:]
	}
	if head, _, hasQuery := strings.Cut(rest, "?"); hasQuery {
		rest = head + "?REDACTED"
	}
	if head, _, hasFragment := strings.Cut(rest, "#"); hasFragment {
		rest = head + "#REDACTED"
	}
	return scheme + "://" + rest
}

// redactScpPassword masks the user half of the scp-like user:password@host:path
// form, which Go's URL parser does not read as a URL at all. A bare account,
// git@host:path, is not a credential and stays as written, and so is anything
// that is not that form: a user half holding a slash is a refspec or a path
// with an @ in it, such as oid:refs/tags/pkg@1.0.0, and not an address.
func redactScpPassword(value string) string {
	if strings.Contains(value, "://") || strings.HasPrefix(value, "/") {
		return value
	}
	userinfo, address, hasUser := strings.Cut(value, "@")
	if !hasUser || !strings.Contains(userinfo, ":") || strings.Contains(userinfo, "/") ||
		!strings.Contains(address, ":") {
		return value
	}
	return "REDACTED@" + address
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
func (c *LocalGitx) Tags(ctx context.Context, pkg string, format TagFormat) (Tags, error) {
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
	return parseTags(out, pkg, format)
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
func (c *LocalGitx) TagsForPackages(ctx context.Context, formats map[string]TagFormat) (map[string]Tags, error) {
	if len(formats) == 0 {
		return map[string]Tags{}, nil
	}
	out, err := c.run(ctx, "tag", "--list", "--merged", "HEAD",
		"--sort=-v:refname", "--sort=-creatordate",
		"--format=%(refname:short)\t%(objectname)\t%(*objectname)")
	if err != nil {
		return nil, err
	}
	return parseTagsForPackages(out, formats)
}

// FindTag looks one tag up by its exact name among the tags HEAD reaches,
// with its commit peeled: the tag the baseline query (Tags) would see under
// that name, found without listing every other one. It is the question a
// refused release tag write asks (release.CreateReleaseTagAs), which is rare;
// a tag HEAD cannot reach is not found, exactly as the baseline never saw it.
func (c *LocalGitx) FindTag(ctx context.Context, name string) (Tag, bool, error) {
	if err := validRefName(name); err != nil {
		return Tag{}, false, fmt.Errorf("gitx: finding tag %q: %w", name, err)
	}
	ref := "refs/tags/" + name
	out, err := c.run(ctx, "for-each-ref", "--merged", "HEAD",
		"--format=%(refname)\t%(objectname)\t%(*objectname)", ref)
	if err != nil {
		return Tag{}, false, err
	}
	for line := range strings.Lines(out) {
		entry, err := parseTagInventoryLine(line)
		if err != nil {
			return Tag{}, false, err
		}
		// A pattern matches the refs below it too, so the name is compared.
		if entry.name == ref {
			return Tag{Name: name, Commit: strings.Clone(entry.commit)}, true, nil
		}
	}
	return Tag{}, false, nil
}

type packageTagMatcher struct {
	packageName string
	prefix      string
	suffix      string
	reader      VersionReader
}

func newPackageTagMatcher(pkg string, format TagFormat) (packageTagMatcher, bool) {
	tpl, err := format.WithDefault().template()
	if err != nil {
		return packageTagMatcher{}, false
	}
	return packageTagMatcherFromTemplate(pkg, tpl)
}

func packageTagMatcherFromTemplate(pkg string, tpl *tagTemplate) (packageTagMatcher, bool) {
	prefix, suffix, ok := tpl.split(pkg)
	if !ok {
		return packageTagMatcher{}, false
	}
	return packageTagMatcher{
		packageName: pkg,
		prefix:      prefix,
		suffix:      suffix,
		reader:      VersionReader{tpl: tpl, pkg: pkg},
	}, true
}

func (m packageTagMatcher) matches(tag string) bool {
	return len(tag) > len(m.prefix)+len(m.suffix) &&
		strings.HasPrefix(tag, m.prefix) && strings.HasSuffix(tag, m.suffix)
}

type tagInventoryEntry struct {
	name   string
	commit string
}

func parseTagInventoryLine(line string) (tagInventoryEntry, error) {
	// strings.Lines retains the line ending. Remove only that framing: tabs
	// and spaces are field contents here, and trimming the whole record would
	// turn a missing leading or trailing tab into a different record shape.
	line = strings.TrimSuffix(line, "\n")
	line = strings.TrimSuffix(line, "\r")
	if strings.TrimSpace(line) == "" {
		return tagInventoryEntry{}, nil
	}
	name, rest, ok := strings.Cut(line, "\t")
	if !ok {
		return tagInventoryEntry{}, fmt.Errorf("gitx: malformed tag inventory record")
	}
	object, peeled, ok := strings.Cut(rest, "\t")
	if !ok || strings.Contains(peeled, "\t") {
		return tagInventoryEntry{}, fmt.Errorf("gitx: malformed tag inventory record")
	}
	entry := tagInventoryEntry{name: strings.TrimSpace(name), commit: strings.TrimSpace(object)}
	if entry.name == "" || !fullObjectID(entry.commit) {
		return tagInventoryEntry{}, fmt.Errorf("gitx: malformed tag inventory identity")
	}
	if peeled := strings.TrimSpace(peeled); peeled != "" {
		if !fullObjectID(peeled) {
			return tagInventoryEntry{}, fmt.Errorf("gitx: malformed peeled tag object id")
		}
		entry.commit = peeled
	}
	return entry, nil
}

// detach copies the retained fields out of the full git-for-each-ref output.
// A Tag can outlive planning, so keeping a small matching slice must not retain
// the potentially very large inventory string that surrounded it.
func (e tagInventoryEntry) detach() tagInventoryEntry {
	return tagInventoryEntry{name: strings.Clone(e.name), commit: strings.Clone(e.commit)}
}

func (m packageTagMatcher) read(entry tagInventoryEntry) Tag {
	tag := Tag{Name: entry.name, Commit: entry.commit}
	if version, ok := m.reader.ParseVersion(entry.name); ok {
		tag.Version, tag.Parsed = version, true
	}
	return tag
}

// parseTagsForPackages parses each raw ref once, then dispatches it only to
// formats whose expanded literal prefix can match. The trie walk is linear in
// the tag's bytes (each node has at most the fixed byte alphabet); work after
// that is the real candidate overlap between custom formats. Empty-prefix
// formats deliberately remain candidates for every tag.
func parseTagsForPackages(out string, formats map[string]TagFormat) (map[string]Tags, error) {
	index := newPackageTagIndex(formats)
	for line := range strings.Lines(out) {
		entry, err := parseTagInventoryLine(line)
		if err != nil {
			return nil, err
		}
		index.add(entry)
	}
	return index.tags, nil
}

// packageTagIndex dispatches a tag inventory to the packages whose format
// could have written each name. It is shared by the local listing above and
// the remote one (see records.go), so a record is recognised by the same rule
// wherever it is read, and it consumes one entry at a time so neither reader
// has to hold a parsed copy of a large inventory.
type packageTagIndex struct {
	prefixes *tagPrefixNode[packageTagMatcher]
	tags     map[string]Tags
}

func newPackageTagIndex(formats map[string]TagFormat) *packageTagIndex {
	index := &packageTagIndex{
		prefixes: &tagPrefixNode[packageTagMatcher]{},
		tags:     make(map[string]Tags, len(formats)),
	}
	templates := make(map[TagFormat]*tagTemplate)
	names := make([]string, 0, len(formats))
	for name := range formats {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		index.tags[name] = nil
		format := formats[name].WithDefault()
		tpl, compiled := templates[format]
		if !compiled {
			var err error
			tpl, err = format.template()
			if err != nil {
				tpl = nil
			}
			templates[format] = tpl
		}
		if tpl == nil {
			continue
		}
		matcher, ok := packageTagMatcherFromTemplate(name, tpl)
		if ok {
			index.prefixes.add(matcher.prefix, matcher)
		}
	}
	return index
}

func (x *packageTagIndex) add(entry tagInventoryEntry) {
	if entry.name == "" || entry.name == LockTagName || strings.HasPrefix(entry.name, LockAttemptTagPrefix) {
		return
	}
	detached := false
	node := x.prefixes
	for depth := 0; node != nil; depth++ {
		for _, matcher := range node.matchers {
			if matcher.matches(entry.name) {
				if !detached {
					entry = entry.detach()
					detached = true
				}
				x.tags[matcher.packageName] = append(x.tags[matcher.packageName], matcher.read(entry))
			}
		}
		if depth == len(entry.name) {
			break
		}
		node = node.child(entry.name[depth])
	}
}

func parseTags(out, pkg string, format TagFormat) (Tags, error) {
	matcher, ok := newPackageTagMatcher(pkg, format)
	if !ok {
		return nil, nil
	}
	var tags Tags
	for line := range strings.Lines(out) {
		entry, err := parseTagInventoryLine(line)
		if err != nil {
			return nil, err
		}
		if entry.name == "" {
			continue
		}
		if entry.name == LockTagName || strings.HasPrefix(entry.name, LockAttemptTagPrefix) {
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
		if !matcher.matches(entry.name) {
			continue
		}
		tags = append(tags, matcher.read(entry.detach()))
	}
	return tags, nil
}

// IsAncestor reports whether commit a is an ancestor-or-self of commit b.
//
// Ancestry rather than commit or tag dates is what keeps cancellation (§10.4)
// and the staleness screen (§13.7b) deterministic under merges, rebases and
// equal timestamps.
func (c *LocalGitx) IsAncestor(ctx context.Context, a, b string) (bool, error) {
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
	graph, err := c.commitDAG(ctx)
	if err != nil {
		return false, err
	}
	if yes, known := graph.isAncestor(a, b); known {
		return yes, nil
	}
	// A commit outside HEAD's ancestry (or an abbreviated SHA): answer the
	// one question authoritatively instead of guessing from a partial graph.
	return c.mergeBaseIsAncestor(ctx, a, b)
}

// commitDAG returns the history reachable from HEAD in index form, loaded once
// per LocalGitx and reused for every ancestry question.
func (c *LocalGitx) commitDAG(ctx context.Context) (*commitGraph, error) {
	c.dagMu.Lock()
	defer c.dagMu.Unlock()
	if c.dag != nil || c.dagErr != nil {
		return c.dag, c.dagErr
	}
	out, err := c.run(ctx, "rev-list", "--parents", "HEAD")
	if err != nil {
		// A cancelled or expired context says nothing about the repository,
		// so that answer is not remembered: a later caller with a live
		// context asks git again instead of inheriting the interruption.
		if ctx.Err() == nil {
			c.dagErr = err
		}
		return nil, err
	}
	c.dag = newCommitGraph(out)
	return c.dag, nil
}

// commitGraph is the history reachable from HEAD with every commit given a
// dense index, so that a set of commits is a bitset and a parent is four
// bytes rather than another copy of its id.
//
// Planning asks whether a is an ancestor of b thousands of times, and b is
// almost always one of a few commits: a baseline, a cancel barrier, the commit
// of a correction (CCME §13.11). A fresh walk per question costs the whole
// ancestry of b every time the answer is no, which on a prerelease train is
// every fresh commit. The first question about b walks its ancestry once and
// keeps it; every later one is a bit test.
type commitGraph struct {
	index   map[string]int32
	parents [][]int32

	mu    sync.Mutex
	reach map[int32][]uint64 // descendant -> its ancestors-or-self
	order []int32            // insertion order, oldest first, for eviction
}

// reachBudget bounds the retained ancestor sets. One set is a bit per
// reachable commit, 12 KiB for a history of 100,000, so the budget holds
// thousands of boundaries; past it the oldest set is dropped and recomputed
// if it is asked about again, which changes no answer.
const reachBudget = 64 << 20

// newCommitGraph reads `git rev-list --parents` output. rev-list does not
// promise that a parent's own line precedes its mention, so indices are
// assigned in one pass and parents resolved in a second.
func newCommitGraph(revList string) *commitGraph {
	g := &commitGraph{index: make(map[string]int32), reach: make(map[int32][]uint64)}
	for line := range strings.Lines(revList) {
		if id, _, _ := strings.Cut(strings.TrimSpace(line), " "); id != "" {
			if _, dup := g.index[id]; !dup {
				// The ids are sub-slices of one rev-list buffer. Cloning them
				// keeps the whole listing from staying live for as long as
				// this repository handle does.
				g.index[strings.Clone(id)] = int32(len(g.index))
			}
		}
	}
	g.parents = make([][]int32, len(g.index))
	for line := range strings.Lines(revList) {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		child := g.index[fields[0]]
		if g.parents[child] != nil {
			continue
		}
		parents := make([]int32, 0, len(fields)-1)
		for _, parent := range fields[1:] {
			// A shallow clone names parents it does not hold.
			if i, ok := g.index[parent]; ok {
				parents = append(parents, i)
			}
		}
		g.parents[child] = parents
	}
	return g
}

// isAncestor reports whether a is an ancestor-or-self of b. known is false
// when either commit lies outside the loaded history, and the caller asks git.
func (g *commitGraph) isAncestor(a, b string) (yes, known bool) {
	ia, okA := g.index[a]
	ib, okB := g.index[b]
	if !okA || !okB {
		return false, false
	}
	set := g.ancestors(ib)
	return set[ia>>6]&(1<<(uint(ia)&63)) != 0, true
}

// ancestors returns the ancestor-or-self set of b, walking it on first use.
// The set under construction is its own seen-set.
func (g *commitGraph) ancestors(b int32) []uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	if set, ok := g.reach[b]; ok {
		return set
	}
	set := make([]uint64, (len(g.parents)+63)/64)
	stack := []int32{b}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if set[cur>>6]&(1<<(uint(cur)&63)) != 0 {
			continue
		}
		set[cur>>6] |= 1 << (uint(cur) & 63)
		stack = append(stack, g.parents[cur]...)
	}
	size := len(set) * 8
	for len(g.order) > 0 && (len(g.order)+1)*size > reachBudget {
		delete(g.reach, g.order[0])
		g.order = g.order[1:]
	}
	if size <= reachBudget {
		g.reach[b] = set
		g.order = append(g.order, b)
	}
	return set
}

// mergeBaseIsAncestor is the per-question fallback for commits the loaded DAG
// does not cover.
// It goes through run like every other invocation, so the question appears in
// the trace and its failure carries the same redaction as the rest.
func (c *LocalGitx) mergeBaseIsAncestor(ctx context.Context, a, b string) (bool, error) {
	_, err := c.run(ctx, "merge-base", "--is-ancestor", a, b)
	if err == nil {
		return true, nil
	}
	// Exit status 1 is the answer "no"; anything else is a real failure.
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

var _ CommitProbex = (*LocalGitx)(nil)

// IsCommitPresent reports whether rev resolves to a commit object here.
//
// `rev-parse --quiet --verify` is the one spelling that separates the two
// failures this has to tell apart: an unknown revision exits 1 silently,
// while a repository git cannot read at all exits 128 and says why. Peeling
// with `^{commit}` makes the question about a commit rather than any object,
// which is what a gitlink pin names.
//
// Callers use this before an ancestry question whose left side may be absent
// — a control snapshot's source pin, above all. `merge-base --is-ancestor`
// treats an unknown commit as a fatal error, so without the probe a source
// clone that simply never fetched the pinned revision aborts planning with a
// git exit status instead of reporting the fleet condition that caused it.
func (c *LocalGitx) IsCommitPresent(ctx context.Context, rev string) (bool, error) {
	if rev == "" {
		return false, nil
	}
	cmd := c.newCommand(ctx, "rev-parse", "--quiet", "--verify", rev+"^{commit}")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.WaitDelay = 10 * time.Second // same backstop as run()
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	// Exit status 1 is the answer "no such commit here"; anything else is a
	// real failure.
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("git rev-parse --verify %s: %w: %s",
		rev, err, strings.TrimSpace(stderr.String()))
}

// IsShallow reports whether the repository is a shallow clone.
func (c *LocalGitx) IsShallow(ctx context.Context) (bool, error) {
	out, err := c.run(ctx, "rev-parse", "--is-shallow-repository")
	if err != nil {
		return false, err
	}
	switch strings.TrimSpace(out) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("gitx: malformed shallow-repository reply")
	}
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

func (c *LocalGitx) Commits(ctx context.Context, sinceTag string) ([]Commit, error) {
	if sinceTag == "" {
		return c.log(ctx, "HEAD")
	}
	return c.log(ctx, sinceTag+"..HEAD")
}

// unionBoundaryChunk bounds one merge-base command line. A full object id is
// at most 64 bytes, and the shortest process limit dispat meets is Windows'
// 32,767 characters. A variable so that a test can fold a small history.
var unionBoundaryChunk = 256

// CommitsSinceAny implements UnionHistoryx. The union of the windows b..HEAD
// is everything reachable from HEAD and not reachable from every boundary at
// once, and the commits reachable from all of them are the ancestors of their
// octopus merge bases, so the union is one walk: HEAD --not <bases>.
//
// The order promise holds because git emits a limited walk by commit date
// with ties broken by insertion, a window is closed under descendants, and a
// commit is therefore only ever queued by a commit of its own window: what
// else the walk is holding decides nothing about the order within a window.
func (c *LocalGitx) CommitsSinceAny(ctx context.Context, boundaries []string) ([]Commit, error) {
	seen := make(map[string]bool, len(boundaries))
	distinct := make([]string, 0, len(boundaries))
	for _, b := range boundaries {
		if b == "" {
			// No boundary at all: that window is the whole history, and so is
			// the union.
			return c.log(ctx, "HEAD")
		}
		if !fullObjectID(b) {
			return nil, fmt.Errorf("gitx: union history boundary %q is not a full commit id", b)
		}
		if !seen[b] {
			seen[b] = true
			distinct = append(distinct, b)
		}
	}
	if len(distinct) == 0 {
		return c.log(ctx, "HEAD")
	}
	// Anything reachable from a boundary and not from HEAD means that boundary
	// is not behind HEAD. One counting walk per chunk, bounded like the log
	// itself: it ends where HEAD's history has covered the boundaries.
	for rest := distinct; len(rest) > 0; {
		n := min(len(rest), unionBoundaryChunk)
		out, err := c.run(ctx, append(append([]string{"rev-list", "--count"}, rest[:n]...), "--not", "HEAD")...)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(out) != "0" {
			return nil, ErrBoundaryNotBehindHead
		}
		rest = rest[n:]
	}
	// Folded a chunk at a time. With R the bases so far, the commits common to
	// R's ancestry and the chunk's are, for each r in R, the ancestors of the
	// octopus bases of r and the chunk; their union is the next R.
	bases := distinct[:1]
	for rest := distinct[1:]; len(rest) > 0 && len(bases) > 0; {
		n := min(len(rest), unionBoundaryChunk)
		chunk := rest[:n]
		rest = rest[n:]
		var next []string
		known := make(map[string]bool)
		for _, r := range bases {
			found, err := c.octopusBases(ctx, append([]string{r}, chunk...))
			if err != nil {
				return nil, err
			}
			for _, base := range found {
				if !known[base] {
					known[base] = true
					next = append(next, base)
				}
			}
		}
		bases = next
	}
	if len(bases) == 0 {
		// The boundaries share no history, so nothing is behind all of them.
		return c.log(ctx, "HEAD")
	}
	return c.log(ctx, append([]string{"HEAD", "--not"}, bases...)...)
}

// octopusBases lists the best common ancestors of all the commits, or nothing
// when they have none.
func (c *LocalGitx) octopusBases(ctx context.Context, commits []string) ([]string, error) {
	out, err := c.run(ctx, append([]string{"merge-base", "--octopus", "--all"}, commits...)...)
	if err != nil {
		// Exit status 1 is the answer "none"; anything else is a real failure.
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			return nil, nil
		}
		return nil, err
	}
	var bases []string
	for line := range strings.Lines(out) {
		if id := strings.TrimSpace(line); id != "" {
			if !fullObjectID(id) {
				return nil, fmt.Errorf("gitx: malformed merge base object id")
			}
			bases = append(bases, strings.Clone(id))
		}
	}
	return bases, nil
}

var (
	_ UnionHistoryx = (*LocalGitx)(nil)
	_ ChangedFilesx = (*LocalGitx)(nil)
)

// log reads the commits a revision range selects, with the payload planning
// needs from each: everything but the changed paths, which ChangedFiles reads
// for the few commits that need them. The walk is the same either way; what
// the paths would add is a diff of every commit against its parent.
func (c *LocalGitx) log(ctx context.Context, revisions ...string) ([]Commit, error) {
	args := []string{
		"log",
		"--format=" + logRecordSep + "%H" + logFieldSep + "%P" + logFieldSep +
			"%an" + logFieldSep + "%ae" + logFieldSep + "%B" + logFieldSep,
	}
	var log commitLog
	if _, err := c.runStream(ctx, gitStream{stdout: &log}, append(args, revisions...)...); err != nil {
		return nil, err
	}
	commits, err := log.close()
	for i := range commits {
		commits[i].AreFilesDeferred = true
	}
	return commits, err
}

// ChangedFiles implements ChangedFilesx in one git process, whatever the
// number of commits.
//
// It is `git log` over exactly the named commits (--no-walk, fed on standard
// input), with every setting §6.2 depends on stated rather than left to the
// operator's configuration: --diff-merges=first-parent counts a merge's
// changes against its first parent, --root lists a root commit's paths,
// --no-renames lists both paths of a rename where rename detection would keep
// the new one alone (vector 29: a file moved across packages derives both),
// and -z hands every path over unquoted, so a name with a letter outside ASCII
// or a tab in it reaches the package that owns it rather than a quoted string
// no package folder is a prefix of.
func (c *LocalGitx) ChangedFiles(ctx context.Context, commits []string) (map[string][]string, error) {
	var stdin strings.Builder
	seen := make(map[string]bool, len(commits))
	for _, commit := range commits {
		if !fullObjectID(commit) {
			return nil, fmt.Errorf("gitx: changed files of %q: not a full commit id", commit)
		}
		if !seen[commit] {
			seen[commit] = true
			stdin.WriteString(commit + "\n")
		}
	}
	if len(seen) == 0 {
		return map[string][]string{}, nil
	}
	out, err := c.runStream(ctx, gitStream{stdin: strings.NewReader(stdin.String())},
		"log", "--no-walk=unsorted", "--stdin",
		"--format="+logRecordSep+"%H"+logFieldSep,
		"--name-only", "--diff-merges=first-parent", "--root", "--no-renames", "-z")
	if err != nil {
		return nil, err
	}
	commitsDiffed.Add(uint64(len(seen)))
	files, err := parseChangedFiles(out)
	if err != nil {
		return nil, err
	}
	if len(files) != len(seen) {
		return nil, fmt.Errorf("gitx: changed files listed %d of %d commits", len(files), len(seen))
	}
	return files, nil
}

// parseChangedFiles reads the ChangedFiles listing, which -z frames: per
// commit, a record separator, its id, a field separator and the format's NUL
// terminator, then, when the commit changed anything, a newline and every path
// NUL-terminated and raw. A path cannot hold a NUL, so the list ends where the
// next record's separator follows a terminator; a listing that breaks this
// framing is refused rather than read as fewer paths.
func parseChangedFiles(out string) (map[string][]string, error) {
	files := make(map[string][]string)
	for out != "" {
		record, isRecord := strings.CutPrefix(out, logRecordSep)
		sha, rest, isFramed := strings.Cut(record, logFieldSep+"\x00")
		if !isRecord || !isFramed || !fullObjectID(sha) {
			return nil, fmt.Errorf("gitx: malformed changed files record")
		}
		var paths []string
		if list, isListed := strings.CutPrefix(rest, "\n"); isListed {
			rest = list
			for rest != "" && !strings.HasPrefix(rest, logRecordSep) {
				path, after, isTerminated := strings.Cut(rest, "\x00")
				if !isTerminated {
					return nil, fmt.Errorf("gitx: malformed changed files record: an unterminated path")
				}
				if path != "" {
					paths = append(paths, strings.Clone(path))
				}
				rest = after
			}
		}
		files[strings.Clone(sha)] = paths
		out = rest
	}
	return files, nil
}

// parseCommits reads a whole commit log held in memory. The history read
// itself streams (commitLog); this is the same parse over a string.
func parseCommits(out string) ([]Commit, error) {
	var log commitLog
	for {
		i := strings.Index(out, logRecordSep)
		if i < 0 {
			log.addRecord(out, false)
			break
		}
		log.addRecord(out[:i], false)
		out = out[i+len(logRecordSep):]
	}
	return log.close()
}

// commitLog parses the commit log as git writes it, one record at a time.
//
// The output is never held whole: a record is collected in a buffer bounded
// by the largest record and parsed as soon as the next one starts. What a
// commit keeps is copied out of the record, so no commit's strings point
// into a buffer holding anything else: a plan keeps its commits' messages
// for the whole run, and a message that was a sub-slice of the log output
// kept the entire output alive with it, separators, other commits and path
// lists included.
type commitLog struct {
	record  []byte
	commits []Commit
	err     error
}

// Write implements io.Writer for the git process's standard output. It never
// refuses a write: a malformed record is remembered for close to report, and
// git is left to finish rather than be killed by a closed pipe.
func (l *commitLog) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, logRecordSep[0])
		if i < 0 {
			l.record = append(l.record, p...)
			break
		}
		l.record = append(l.record, p[:i]...)
		l.flush()
		p = p[i+1:]
	}
	return n, nil
}

// flush parses the buffered record and empties the buffer for the next one.
func (l *commitLog) flush() {
	if len(l.record) > 0 {
		l.addRecord(string(l.record), true)
	}
	l.record = l.record[:0]
}

// addRecord parses one record. isOwned says the string is a copy made for
// this record alone, which the commit may keep as it is.
func (l *commitLog) addRecord(record string, isOwned bool) {
	if l.err != nil || strings.TrimSpace(record) == "" {
		return
	}
	commit, err := parseCommitRecord(record, isOwned)
	if err != nil {
		l.err = err
		return
	}
	l.commits = append(l.commits, commit)
}

// close parses the last record and answers the commits, or the first
// malformed record's error and no commits at all: a truncated history is not
// a shorter one.
func (l *commitLog) close() ([]Commit, error) {
	l.flush()
	if l.err != nil {
		return nil, l.err
	}
	return l.commits, nil
}

// parseCommitRecord reads one record: the fixed fields, then the path list
// when the log listed paths. The fixed fields are everything a commit keeps
// of the record, and they are one string of their own; each path is another.
func parseCommitRecord(record string, isOwned bool) (Commit, error) {
	end, separators := len(record), 0
	for i := 0; i < len(record); i++ {
		if record[i] != logFieldSep[0] {
			continue
		}
		if separators++; separators == logCommitFields {
			end = i
			break
		}
	}
	if separators < logCommitFields-1 {
		return Commit{}, fmt.Errorf("gitx: malformed commit log record: expected at least %d fields, got %d",
			logCommitFields, separators+1)
	}
	kept := record[:end]
	if !isOwned || end < len(record) {
		kept = strings.Clone(kept)
	}
	fields := strings.SplitN(kept, logFieldSep, logCommitFields)
	sha := strings.TrimSpace(fields[0])
	if !fullObjectID(sha) {
		return Commit{}, fmt.Errorf("gitx: malformed commit log object id")
	}
	parents := strings.Fields(fields[1])
	for _, parent := range parents {
		if !fullObjectID(parent) {
			return Commit{}, fmt.Errorf("gitx: malformed commit log parent")
		}
	}
	commit := Commit{
		SHA:     sha,
		Parents: parents,
		// Trimmed because git pads neither, but a name is free text and a
		// configured identity can carry trailing spaces the record would
		// otherwise render.
		AuthorName:  strings.TrimSpace(fields[2]),
		AuthorEmail: strings.TrimSpace(fields[3]),
		Message:     strings.Trim(fields[4], "\n"),
	}
	if end < len(record) {
		for line := range strings.SplitSeq(record[end+1:], "\n") {
			if line = strings.TrimRight(line, "\r"); line != "" {
				commit.Files = append(commit.Files, strings.Clone(line))
			}
		}
	}
	return commit, nil
}

// CreateTag creates an annotated tag at target (any commit-ish), or at HEAD
// when target is empty.
func (c *LocalGitx) CreateTag(ctx context.Context, name, message, target string) error {
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
func (c *LocalGitx) CreateTagForce(ctx context.Context, name, message, target string) error {
	return c.createTag(ctx, name, message, target, true)
}

func (c *LocalGitx) createTag(ctx context.Context, name, message, target string, force bool) error {
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
func (c *LocalGitx) DeleteTag(ctx context.Context, name string) error {
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
func (c *LocalGitx) PushTag(ctx context.Context, remote, name string) error {
	_, err := c.run(ctx, "push", remote, "refs/tags/"+name)
	return err
}

// PushObjectToTag creates name on remote from the immutable object oid. The
// destination is never forced: an existing lock must make acquisition fail.
// Naming the source object, rather than a mutable local ref, also makes this
// safe when two dispat processes share one checkout.
func (c *LocalGitx) PushObjectToTag(ctx context.Context, remote, oid, name string) error {
	_, err := c.run(ctx, "push", remote, oid+":refs/tags/"+name)
	return err
}

// TagObject resolves the tag object itself (without peeling it to its commit).
func (c *LocalGitx) TagObject(ctx context.Context, name string) (string, error) {
	out, err := c.run(ctx, "rev-parse", "refs/tags/"+name)
	return strings.TrimSpace(out), err
}

// DeleteRemoteTag removes a tag from the remote. Deleting a ref the remote
// does not have succeeds: git warns and reports the deletion, because the
// fully qualified refspec leaves nothing to guess about. Cleanup is therefore
// idempotent on this side, unlike DeleteTag.
func (c *LocalGitx) DeleteRemoteTag(ctx context.Context, remote, name string) error {
	_, err := c.run(ctx, "push", remote, "--delete", "refs/tags/"+name)
	return err
}

// DeleteRemoteTagLease deletes name only while it still names expectedOID.
// If ownership changed, git rejects the operation and preserves the new
// owner's lock.
func (c *LocalGitx) DeleteRemoteTagLease(ctx context.Context, remote, name, expectedOID string) error {
	ref := "refs/tags/" + name
	_, err := c.run(ctx, "push", "--force-with-lease="+ref+":"+expectedOID,
		remote, ":"+ref)
	return err
}

// TagExists reports whether the named tag exists in this repository.
func (c *LocalGitx) TagExists(ctx context.Context, name string) (bool, error) {
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
func (c *LocalGitx) RemoteTagMessage(ctx context.Context, remote, name string) (string, error) {
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
func (c *LocalGitx) pathspec(dir string) string {
	if rel, err := filepath.Rel(c.Dir, dir); err == nil {
		return rel
	}
	return dir
}

// RevertDir discards all local changes inside dir: tracked files are restored
// from HEAD and untracked files and folders are removed. Note this also wipes
// any pre-existing uncommitted changes in that folder — CI runs from a clean
// checkout, which is the intended environment.
func (c *LocalGitx) RevertDir(ctx context.Context, dir string) error {
	specs := c.withoutLinks([]string{c.pathspec(dir)})
	if _, err := c.run(ctx, append([]string{"checkout", "--"}, specs...)...); err != nil {
		return err
	}
	_, err := c.run(ctx, append([]string{"clean", "-fd", "--"}, specs...)...)
	return err
}

// RestoreToHead restores the tracked files beneath paths to their content at
// HEAD, in the index and in the working tree, and leaves every untracked file
// alone. A file the index holds and HEAD does not is removed from both, and
// nothing beneath an exclusion is touched. Both lists are absolute paths or
// paths relative to the repository root; the exclusions are matched
// literally.
//
// It restores exactly the files that differ from HEAD, each named literally,
// so a path neither HEAD nor the index holds is not an error: a caller may
// name configured paths that do not exist.
func (c *LocalGitx) RestoreToHead(ctx context.Context, paths, exclusions []string) error {
	if len(paths) == 0 {
		return nil
	}
	specs := make([]string, 0, len(paths)+len(exclusions))
	for _, path := range paths {
		specs = append(specs, c.pathspec(path))
	}
	for _, exclusion := range exclusions {
		specs = append(specs, ":(exclude,literal)"+filepath.ToSlash(c.pathspec(exclusion)))
	}
	diffArgs := append([]string{"diff", "--name-only", "-z", "--no-renames", "HEAD", "--"}, c.withoutLinks(specs)...)
	changed, err := c.run(ctx, diffArgs...)
	if err != nil {
		return fmt.Errorf("listing the files to restore: %w", err)
	}
	if strings.Trim(changed, "\x00") == "" {
		return nil
	}
	stream := gitStream{stdin: strings.NewReader(changed), env: []string{"GIT_LITERAL_PATHSPECS=1"}}
	if _, err := c.runStream(ctx, stream, "restore", "--source=HEAD", "--staged", "--worktree",
		"--pathspec-from-file=-", "--pathspec-file-nul"); err != nil {
		return fmt.Errorf("restoring files to HEAD: %w", err)
	}
	return nil
}

// CommitDirs stages all changes inside the given directories and creates a
// single commit. It reports whether a commit was actually created: when the
// staged set turns out empty (e.g. changelogs disabled and no manifest
// changes) no commit is made and (false, nil) is returned.
func (c *LocalGitx) CommitDirs(ctx context.Context, dirs []string, message string) (bool, error) {
	paths := make([]string, 0, len(dirs))
	for _, d := range dirs {
		paths = append(paths, c.pathspec(d))
	}
	paths = c.withoutLinks(paths)
	if _, err := c.run(ctx, append([]string{"add", "--"}, paths...)...); err != nil {
		return false, err
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
func (c *LocalGitx) DirtyPaths(ctx context.Context, dirs []string) ([]string, error) {
	specs := make([]string, 0, len(dirs))
	for _, d := range dirs {
		specs = append(specs, c.pathspec(d))
	}
	args := append([]string{"status", "--porcelain=v1", "-z", "--untracked-files=all", "--"}, c.withoutLinks(specs)...)
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
func (c *LocalGitx) HeadSHA(ctx context.Context) (string, error) {
	return c.ResolveCommit(ctx, "HEAD")
}

// ResolveCommit resolves any commit-ish (a short SHA, a ref, HEAD) to its
// full commit SHA, peeling tags on the way.
func (c *LocalGitx) ResolveCommit(ctx context.Context, rev string) (string, error) {
	out, err := c.run(ctx, "rev-parse", rev+"^{commit}")
	if err != nil {
		return "", err
	}
	oid := strings.TrimSpace(out)
	if !fullObjectID(oid) {
		return "", fmt.Errorf("gitx: malformed commit object id")
	}
	return oid, nil
}

// VerifyRemote checks that the remote exists, is reachable and authenticated.
// Meant to run before any release work so misconfigured credentials fail fast.
func (c *LocalGitx) VerifyRemote(ctx context.Context, remote string) error {
	_, err := c.run(ctx, "ls-remote", "--heads", remote)
	return err
}

// CurrentBranch returns the name of the checked-out branch, or "" when HEAD is
// detached.
func (c *LocalGitx) CurrentBranch(ctx context.Context) (string, error) {
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
func (c *LocalGitx) BehindRemote(ctx context.Context, remote, branch string) (bool, error) {
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
func (c *LocalGitx) RemoteTags(ctx context.Context, remote string) (map[string]bool, error) {
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
			// errors.Is, which is the only thing that reads it. Both are
			// wrapped, so a cancelled push still answers context.Canceled
			// and an exit status is still reachable through errors.As.
			return fmt.Errorf("%w: %w", err, ErrRejected)
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
func (c *LocalGitx) MergeRemote(ctx context.Context, remote, branch, message string) error {
	if _, err := c.run(ctx, "fetch", "--no-tags", remote, branch); err != nil {
		return err
	}
	// --no-ff for two reasons: a repository configured with merge.ff=only
	// refuses the merge outright without it, and the first-parent shape this
	// recovery documents is only a shape when there is a merge commit to have
	// one.
	if _, err := c.run(ctx, "merge", "--no-ff", "--no-edit", "-m", message, "FETCH_HEAD"); err != nil {
		// The merge that just failed may have failed because the run was
		// cancelled. Reading the index and undoing the merge are the cleanup
		// that cancellation is the reason for, so they run on a detached
		// context with a deadline of their own: handing them the dead one
		// would leave the merge in progress in the working tree, which is
		// exactly what this function promises not to do.
		cleanupCtx, cancel := detachedCleanup(ctx, mergeCleanupTimeout)
		defer cancel()
		if paths, uErr := c.UnmergedPaths(cleanupCtx); uErr == nil && len(paths) > 0 {
			return &MergeConflict{Paths: paths}
		}
		// The abort's own failure is not what the caller needs to hear about:
		// the merge is the thing that did not work, and saying so twice would
		// bury it.
		if abortErr := c.AbortMerge(cleanupCtx); abortErr != nil {
			c.Log.Warn().Err(abortErr).Msg("could not abort the merge")
		}
		return err
	}
	return nil
}

// mergeCleanupTimeout bounds the detached index read and merge abort that
// follow a failed merge. Two local git calls; a bound rather than none so an
// unresponsive repository cannot hold an interrupted run open.
const mergeCleanupTimeout = 30 * time.Second

// detachedCleanup is a context for work that must still run after the
// caller's was cancelled: the caller's values are kept, its cancellation is
// not, and a deadline bounds the detachment.
func detachedCleanup(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), d)
}

// UnmergedPaths are the paths a stopped merge left unresolved in the index.
func (c *LocalGitx) UnmergedPaths(ctx context.Context) ([]string, error) {
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
func (c *LocalGitx) AbortMerge(ctx context.Context) error {
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
func (c *LocalGitx) ResolveOurs(ctx context.Context, paths []string) error {
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
func (c *LocalGitx) StageFile(ctx context.Context, path string) error {
	_, err := c.run(ctx, "add", "--", path)
	return err
}

// CommitMerge finishes a merge in progress with the message it was started
// with, whatever the caller resolved and staged in the meantime.
func (c *LocalGitx) CommitMerge(ctx context.Context) error {
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
func (c *LocalGitx) PushBranchAt(ctx context.Context, remote, rev, name string) error {
	if err := ValidRefName(name); err != nil {
		return fmt.Errorf("%q: %w", name, err)
	}
	existing, err := c.run(ctx, "ls-remote", "--heads", remote, "refs/heads/"+name)
	if err != nil {
		return err
	}
	if strings.TrimSpace(existing) != "" {
		// The remote may be a resolved push URL rather than a name, and a
		// release's error text reaches hook scripts through DISPAT_ERROR.
		return fmt.Errorf("%s already has a branch called %s", RedactURL(remote), name)
	}
	_, err = c.run(ctx, "push", remote, rev+":refs/heads/"+name)
	return err
}

// ValidRefName reports whether git would accept name as a ref. It is the same
// check the alias tag formats are validated with, exposed for the callers that
// build a ref name out of things a person configured.
func ValidRefName(name string) error { return validRefName(name) }

// PushReport says what the push did about tags the remote already carried.// PushReport says what the push did about tag names the remote already
// carried. A name it did not carry appears in none of the lists: creating a
// record is the ordinary outcome and says nothing worth reading back.
type PushReport struct {
	// Skipped are records the remote already held at this release's commit,
	// which is the retry of a write whose answer was lost (§19.4). Nothing
	// was written and nothing is wrong.
	Skipped []string
	// Replaced are moving aliases that were re-pointed. Only an alias
	// declared `moving: true` can appear here: a release record is never
	// replaced, whatever commit.force says.
	Replaced []string
	// Conflicts are records the remote holds at another commit. They were
	// left exactly where they are, and each is a refusal the caller reports
	// rather than a difference the push resolved.
	Conflicts []RefOutcome
}

// Push pushes the current branch (HEAD) and then the given refs to the remote.
// Requires a checked-out branch (not a detached HEAD).
//
// The records travel create-only and the moving aliases travel forced; see
// PushReleaseRefs, which is where that rule lives. A name the remote already
// holds is therefore never replaced by a release record, and the report says
// which of the three things happened to it, because "the remote already had
// this one at the same commit" and "the remote holds this version somewhere
// else" are a convergent re-run and an integrity failure.
//
// **The branch is never force pushed.** A rejected branch push means someone
// else pushed while this run was working, and the answer to that is to look,
// not to overwrite their commits. A refusal of that kind comes back wrapping
// ErrRejected, so the caller can join what landed with MergeRemote and push
// again rather than reporting a release that never landed.
func (c *LocalGitx) Push(ctx context.Context, remote string, refs []ReleaseRef) (PushReport, error) {
	var report PushReport
	if _, err := c.run(ctx, "push", remote, "HEAD"); err != nil {
		return report, classifyPush(err)
	}
	if len(refs) == 0 {
		return report, nil
	}
	outcomes, err := c.PushReleaseRefs(ctx, remote, refs)
	if err != nil {
		return report, err
	}
	for _, outcome := range outcomes {
		switch outcome.Result {
		case RefExisting:
			report.Skipped = append(report.Skipped, outcome.Name)
		case RefMoved:
			report.Replaced = append(report.Replaced, outcome.Name)
		case RefAtOtherCommit:
			report.Conflicts = append(report.Conflicts, outcome)
		}
	}
	return report, nil
}
