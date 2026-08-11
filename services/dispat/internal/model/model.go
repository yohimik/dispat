// Package model holds the domain types shared across the tool.
package model

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Versioning is a space's versioning mode: how the versions of the space's
// packages relate to each other.
type Versioning string

const (
	// VersioningIndependent computes every package's version from its own
	// history alone. The default and the zero value.
	VersioningIndependent Versioning = "independent"
	// VersioningFixed keeps every package of the space on one shared version:
	// a change to any package releases every package of the space at the same
	// next version, computed as if the whole space were one package (single
	// prerelease train included). Commit and file scopes still decide which
	// changelog entries a package receives; a package released only to keep
	// the versions aligned gets a single "no changes" changelog entry.
	VersioningFixed Versioning = "fixed"
	// VersioningFixedSparse computes the space version exactly like
	// VersioningFixed, but a package with no changes of its own keeps its
	// previous version and is not released; changed packages release at the
	// shared version.
	VersioningFixedSparse Versioning = "fixedSparse"
	// VersioningFixedMajorMinor shares the major and minor across the group:
	// a minor or major release moves every member to the same next version,
	// while patch releases stay each package's own.
	VersioningFixedMajorMinor Versioning = "fixedMajorMinor"
	// VersioningFixedMajorMinorSparse shares the major and minor exactly like
	// VersioningFixedMajorMinor, but a package with no changes of its own
	// keeps its previous version instead of riding along.
	VersioningFixedMajorMinorSparse Versioning = "fixedMajorMinorSparse"
	// VersioningFixedMajor shares the major alone: a major release moves every
	// member to the same next version, while minor and patch releases stay
	// each package's own.
	VersioningFixedMajor Versioning = "fixedMajor"
	// VersioningFixedMajorSparse shares the major exactly like
	// VersioningFixedMajor, but a package with no changes of its own keeps its
	// previous version instead of riding along.
	VersioningFixedMajorSparse Versioning = "fixedMajorSparse"
)

// SharedVersioningDepth is the depth at which a group shares the whole
// version: MAJOR, MINOR and PATCH all held in common. It is the maximum a
// SharedDepth can reach, and the planner reads it rather than a bare 3.
const SharedVersioningDepth = 3

// SharedDepth is how many leading version components the mode keeps equal
// across a versioning group: 3 for fixed (the whole version), 2 for
// fixedMajorMinor, 1 for fixedMajor, 0 for independent and for any
// unrecognised value.
//
// It is the one number the planner needs from a mode. Everything else about
// partial sharing follows from it: a bump reaching the shared depth moves the
// whole group, a smaller one is the package's own business, and a member
// joining the group adopts the leading components and zeroes the rest.
func (v Versioning) SharedDepth() int {
	switch v {
	case VersioningFixed, VersioningFixedSparse:
		return SharedVersioningDepth
	case VersioningFixedMajorMinor, VersioningFixedMajorMinorSparse:
		return 2
	case VersioningFixedMajor, VersioningFixedMajorSparse:
		return 1
	default:
		return 0
	}
}

// Sparse reports whether a member with no changes of its own stays at its
// previous version instead of riding along when the group's shared part moves.
func (v Versioning) Sparse() bool {
	switch v {
	case VersioningFixedSparse, VersioningFixedMajorMinorSparse, VersioningFixedMajorSparse:
		return true
	default:
		return false
	}
}

// Shared reports whether the mode versions its package as part of a group,
// which is exactly the modes that hold some leading part of the version in
// common.
func (v Versioning) Shared() bool { return v.SharedDepth() > 0 }

// Space groups packages that share build and publish behaviour. A package
// whose configuration overrides its space's carries its own Space value — a
// derived copy with the overrides applied — so every consumer of Space reads
// per-package behaviour without knowing overrides exist; Name always stays
// the configured space's name.
type Space struct {
	Name string
	// Path of the space folder, relative to the monorepo root. Every direct
	// sub-folder is a package, unless a .dispatignore file in the space
	// folder excludes it.
	Path string
	// Versioning is how versions relate across the space's packages; the zero
	// value means VersioningIndependent.
	Versioning Versioning
	// VersionGroup is the shared-versioning group key the planner groups by:
	// the referenced versionGroups entry (or another space's group) when
	// configuration names one, the space's own name otherwise. The zero
	// value means the space's own group. Only read when Versioning is
	// shared.
	VersionGroup string
	// Scripts is the effective script map of the package this Space was
	// derived for: the file's scripts, overlaid with the space's, overlaid
	// with the package's, keyed by lowercased name and holding shell commands.
	// It is what every script name resolves through — the flow entries below
	// were resolved from it, and `dispat run <name>` looks the name up here,
	// executing the command inside each changed package that has one, in
	// topological order, with the package's full DISPAT_* environment.
	Scripts map[string]string
	// Env is the static environment of this package's scripts: the
	// configuration's env layers (top level, space, space folder file, package
	// overrides) merged key by key and flattened to sorted KEY=value pairs,
	// keys spelled exactly as the file wrote them. Script execution places
	// these before the computed DISPAT_* variables — so a static key can never
	// shadow a computed one — and expands $NAME references in the values
	// against the computed set and the process environment.
	Env []string
	// BuildWaitsPublish: when true, consumers of packages from this space may
	// only start building after the provider has been published (not merely
	// built).
	BuildWaitsPublish bool
	// RevertOnFail: when true, all local changes inside the package folder
	// are rolled back (tracked files restored, untracked files removed) if
	// the package fails during its version, build or publish stage.
	RevertOnFail bool
	// BuildScript, PublishScript and VersionScript are sequences of resolved
	// shell commands (not script names) executed in order inside the package
	// folder. Any of them may be empty: the corresponding pipeline stage still
	// runs (with tags, changelogs and ordering intact) but executes no shell
	// command. A failing command stops its sequence and fails the stage.
	// VersionScript runs right before a consumer's build, only when the
	// consumer is released because of provider updates — its job is syncing
	// manifests (package.json, go.mod, ...) to the new provider versions.
	BuildScript   []string
	PublishScript []string
	VersionScript []string
	// LoginScript runs once per space, before the first publish of any of the
	// space's packages; every other publish of the space waits for it. Its
	// job is authentication (npm login, docker login, ...), which is why it is
	// per space and not per package, and why a failure fails every publish in
	// the space: none of them could have succeeded.
	LoginScript []string
	// AnnounceScript is a fourth per-package stage, run after a successful
	// publish (and its postPublish hook). Its job is pushing the release out
	// to update channels — a Slack message, a Discord webhook, a docs feed —
	// which is why it receives the release-notes variables
	// (DISPAT_BREAKING_CHANGES / DISPAT_FEATURES / DISPAT_FIXES) alongside the
	// full stage environment, and why the whole frame — both hooks included —
	// only warns on failure: the release is out, and a failed announcement
	// must not report it as unpublished.
	AnnounceScript []string
	// The hooks. Each is a sequence of resolved commands run around a stage of
	// every package in the space; an empty hook is a no-op. The Before*/Post*
	// hooks up to BeforePublish fail the package's release when they fail —
	// they exist to gate it. PostPublish runs after a successful publish and
	// only warns: the release is out, failing the package would misreport it.
	BeforeAllScript      []string // before the package's first stage
	BeforeVersionScript  []string
	PostVersionScript    []string
	BeforeBuildScript    []string
	PostBuildScript      []string
	BeforePublishScript  []string
	PostPublishScript    []string
	BeforeAnnounceScript []string // warn-only, like the announce stage they bracket
	PostAnnounceScript   []string
	// The outcome scripts. OnFailScript runs once when a package of the space
	// fails at any stage (version, build, publish — a failing hook, recorder
	// or tag included); OnSkipScript runs once when the package is skipped
	// because a provider failed or was skipped. Both observe an outcome that
	// has already settled, so they only warn on failure, and both receive the
	// full package environment plus the specifics: DISPAT_FAILED_STAGE and
	// DISPAT_ERROR for a failure, DISPAT_BLOCKED_BY for a skip.
	OnFailScript []string
	OnSkipScript []string
	// TagFormat is the release tag template for this space, e.g.
	// "{name}@{version}" or "services/{name}@v{version}". Empty means the
	// repository default. It lives on the space rather than the repository
	// because the convention usually follows the toolchain a group of
	// packages ships with, and a monorepo mixing two of them is the case
	// worth supporting.
	TagFormat string
	// AutoVersion is the space's resolved native manifest-rewriting policy
	// for the version stage; nil means the feature is off and manifest
	// syncing stays the VersionScript's job alone.
	AutoVersion *AutoVersion
}

// ManifestScope is how much of a package the parsing strategy covers.
type ManifestScope string

// The three manifest scopes. ScopeNone turns the parsing strategy off, which
// leaves the replace rules and syncLock as the whole of the version stage.
const (
	ScopeRoot ManifestScope = "root"
	ScopeAll  ManifestScope = "all"
	ScopeNone ManifestScope = "none"
)

// AutoVersion is a space's resolved autoVersion policy: which declarations
// the version stage rewrites natively (§9.4) and what it writes.
//
// Two strategies live here and either may be off. The parsing one reads the
// package's manifests and rewrites the declarations naming a workspace
// provider; the replacing one substitutes literal text in whatever files the
// rules point at, parsing nothing. With both off the block still schedules a
// version task, which is how a space asks for syncLock and nothing else.
type AutoVersion struct {
	// Manifests is the parsing strategy's scope: the package's root
	// manifests, every manifest under it, or none at all.
	Manifests ManifestScope
	// Replace are the resolved literal substitution rules; empty means the
	// replacing strategy is off.
	Replace []ReplaceRule
	// Kinds are the manifest fields eligible for rewriting; always fully
	// populated (all four kinds when the config named none).
	Kinds map[DepKind]bool
	// Only restricts rewriting to declarations of these provider packages;
	// nil means every workspace provider.
	Only map[string]bool
	// OnlyUpdated restricts rewriting to declarations naming a provider this
	// run releases. Without it a declaration that had fallen behind a provider
	// released earlier catches up too (W197), which is usually what a release
	// wants and is not always what a standalone invocation does. Only is an
	// allowlist of packages; this is a question about the run.
	OnlyUpdated bool
	// NameSubstring additionally matches a declared name onto the package
	// whose folder name equals its last /- or :-separated segment, for
	// packages whose manifests declare no name the workspace can learn.
	NameSubstring bool
	// Match are the declared-range globs eligible for rewriting; empty means
	// any. A range matching none of the globs — a hand-pinned version — is
	// left alone.
	Match []string
	// Range is the write policy: "caret" (also the empty default), "tilde",
	// "exact", a {version} template, or a verbatim literal.
	Range string
	// WriteVersion also rewrites the package's own manifest version field.
	WriteVersion bool
	// SyncLock are resolved shell commands run inside the package folder
	// after its manifests changed, between version and build.
	SyncLock []string
	// SyncLockConcurrency is the space's vote for the run-wide syncLock
	// budget; 0 means the default of 1.
	SyncLockConcurrency int
}

// ReplaceRule is one resolved literal substitution: which files it applies to
// and the templated text it looks for and writes.
type ReplaceRule struct {
	Files       []string
	Find, Write string
}

// Reconciles reports whether either strategy has work to do. When it does
// not, the version stage still runs and syncLock still runs with it: a space
// asking only for a lock-file refresh has no manifest change to key off, so
// gating on one would mean it never fired. Nil-safe.
func (a *AutoVersion) Reconciles() bool {
	return a != nil && (a.Manifests != ScopeNone || len(a.Replace) > 0)
}

// Package is a single releasable folder inside a space.
type Package struct {
	Name string
	Dir  string // folder in which scripts run
	// Src narrows which of the package's files count as changes to it: a
	// Dir-relative path, empty for the whole folder. See ScopeDir.
	Src   string
	Space *Space
	// BuildWeight and PublishWeight are the stage-budget slots the package's
	// tasks occupy, always >= 1; 1 is the ordinary cost. A weight reaching
	// the stage's budget makes the package run that stage alone.
	BuildWeight   int
	PublishWeight int
	// Changelog and GitHub are the package's resolved record policies: the
	// top-level configuration overlaid with the package's override, so the
	// recorders read the package alone.
	Changelog ChangelogSpec
	GitHub    GitHubSpec
	// ManifestNames are the manifest names the configuration states this
	// package is known by, for the packages whose files declare none the
	// workspace can learn. They outrank a declared name and feed the one
	// index `dispat compute` and auto-versioning share.
	ManifestNames []string
}

// ScopeDir is the folder a changed file must sit under to count as a change
// to this package: Dir narrowed by Src when the package declares one, and
// Dir itself otherwise. It is the only place the narrowing lives, so
// everything that resolves ownership by path agrees on it.
func (p *Package) ScopeDir() string {
	if p.Src == "" {
		return p.Dir
	}
	return filepath.Join(p.Dir, filepath.FromSlash(p.Src))
}

// VersionGroupName names the versioning group the package belongs to, or ""
// when its versioning is independent. The group is a package's third address,
// beside its name and its folder: the planner moves a group's versions
// together, a selection can be pointed at one, and both have to agree on which
// packages are in it. Nil-safe.
func (p *Package) VersionGroupName() string {
	if p == nil || p.Space == nil || !p.Space.Versioning.Shared() {
		return ""
	}
	if p.Space.VersionGroup != "" {
		return p.Space.VersionGroup
	}
	return p.Space.Name // the zero value means the space's own group
}

// EntryLine is one block of record text with the filters deciding which
// packages it is written for — the resolved counterpart of the config's line
// shorthands, which have all been expanded into this one shape by the time it
// gets here. Filters are case-insensitive glob patterns; empty means "every
// package".
type EntryLine struct {
	Line    []string
	Package []string
	Space   []string
	Group   []string
}

// RecordFormat customises how a release entry renders — the resolved
// counterpart of the config's entry-format options, shared by the changelog
// file and the GitHub release body. Empty fields mean the renderer defaults.
type RecordFormat struct {
	DateFormat        string
	BreakingTitle     string
	FeaturesTitle     string
	FixesTitle        string
	DependenciesTitle string
	// ReleaseName names the release: the GitHub release's name, or a
	// sub-header in a changelog entry. Empty means the destination's own
	// default (the tag on GitHub, nothing in a file).
	ReleaseName string
	// Header and Footer bracket the sections of every entry.
	Header []EntryLine
	Footer []EntryLine
}

// ChangelogSpec is a package's resolved changelog policy.
type ChangelogSpec struct {
	Enabled bool
	// Prerelease writes an entry for a prerelease version too; false keeps
	// the file a record of stable releases alone.
	Prerelease bool
	File       string // empty means the writer default (CHANGELOG.md)
	// FileTitle heads the file; an empty list means the writer default.
	FileTitle []EntryLine
	Format    RecordFormat
}

// Records reports whether a release on this policy is written at all:
// enabled, and — when the version is a prerelease — not held back by the
// prerelease opt-out. The caller passes the release's own answer rather than
// the release, so the domain model stays free of the planner's types.
func (s ChangelogSpec) Records(isPrerelease bool) bool {
	return s.Enabled && (s.Prerelease || !isPrerelease)
}

// GitHubSpec is a package's resolved GitHub-release policy. Owner/Repo may
// be empty here: the runtime fallback to $GITHUB_REPOSITORY stays with the
// releaser resolution, which is where "unresolvable" is an outcome.
type GitHubSpec struct {
	Enabled bool
	// Prerelease creates a release for a prerelease version too; false keeps
	// the releases page a list of stable releases alone.
	Prerelease bool
	// AllPackages creates a release for every published package, even without
	// the DISPAT_EXPORT_GITHUB export (which then only adds assets).
	AllPackages bool
	Owner       string
	Repo        string
	APIURL      string // empty means the public GitHub API
	TokenEnv    string // empty means GITHUB_TOKEN
	Format      RecordFormat
}

// Records reports whether a release on this policy is created at all, the
// GitHub counterpart of ChangelogSpec.Records.
func (s GitHubSpec) Records(isPrerelease bool) bool {
	return s.Enabled && (s.Prerelease || !isPrerelease)
}

// Key identifies the releaser a policy needs: two packages whose specs share
// a Key share one GitHub releaser, resolved and verified once and reused for
// both. Everything a releaser is built from goes in, the entry format
// included, since it shapes every body the releaser sends.
//
// It exists because the spec carries line lists and so cannot be a map key
// itself. Values are quoted and separated by a byte no configuration can
// contain, so no two distinct policies can encode alike.
func (s GitHubSpec) Key() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%t\x00%t\x00%t\x00%q\x00%q\x00%q\x00%q", s.Enabled, s.Prerelease, s.AllPackages,
		s.Owner, s.Repo, s.APIURL, s.TokenEnv)
	s.Format.writeKey(&b)
	return b.String()
}

// writeKey appends a format's contribution to a policy key.
func (f RecordFormat) writeKey(b *strings.Builder) {
	fmt.Fprintf(b, "\x00%q\x00%q\x00%q\x00%q\x00%q\x00%q", f.DateFormat, f.BreakingTitle, f.FeaturesTitle,
		f.FixesTitle, f.DependenciesTitle, f.ReleaseName)
	writeLinesKey(b, f.Header)
	writeLinesKey(b, f.Footer)
}

func writeLinesKey(b *strings.Builder, lines []EntryLine) {
	fmt.Fprintf(b, "\x00%d", len(lines))
	for _, l := range lines {
		fmt.Fprintf(b, "\x00%q\x00%q\x00%q\x00%q", l.Line, l.Package, l.Space, l.Group)
	}
}

// DepKind is the manifest dependency field a graph edge stands for (§8.4).
//
// Which fields imply "must be republished" is a property of the repository
// rather than of any one commit, so §8.4 gives units no override: the set of
// traversed kinds is fixed for the whole run. dispat has no manifest model —
// the kind comes from the edge's `kind` key in the configuration's
// dependencies list (default: a plain runtime dependency) — and a unit's
// propagation is filtered by kind exactly as the specification describes.
type DepKind string

// Dependency edge kinds. The zero value is KindDependencies, so an edge that
// says nothing is a runtime dependency.
const (
	KindDependencies         DepKind = ""
	KindDevDependencies      DepKind = "devDependencies"
	KindPeerDependencies     DepKind = "peerDependencies"
	KindOptionalDependencies DepKind = "optionalDependencies"
)

// String implements fmt.Stringer, spelling the zero value out.
func (k DepKind) String() string {
	if k == KindDependencies {
		return "dependencies"
	}
	return string(k)
}

// Dependency is a consumer -> provider relation between two packages.
type Dependency struct {
	Consumer string
	Provider string
	// Kind is the manifest field the edge stands for (§8.4). The zero value
	// is a plain runtime dependency, which is what configuration produces.
	Kind DepKind
}
