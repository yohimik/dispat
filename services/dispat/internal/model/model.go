// Package model holds the domain types shared across the tool.
package model

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
)

// Shared reports whether the mode computes one version for the whole space
// (fixed or fixedSparse).
func (v Versioning) Shared() bool {
	return v == VersioningFixed || v == VersioningFixedSparse
}

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

// AutoVersion is a space's resolved autoVersion policy: which declarations
// the version stage rewrites natively (§9.4) and what it writes.
type AutoVersion struct {
	// AllManifests rewrites every manifest under the package folder instead
	// of only the root ones.
	AllManifests bool
	// Kinds are the manifest fields eligible for rewriting; always fully
	// populated (all four kinds when the config named none).
	Kinds map[DepKind]bool
	// Only restricts rewriting to declarations of these provider packages;
	// nil means every workspace provider.
	Only map[string]bool
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

// Package is a single releasable folder inside a space.
type Package struct {
	Name  string
	Dir   string // folder in which scripts run
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

// RecordFormat customises how a release entry renders — the resolved
// counterpart of the config's entry-format options, shared by the changelog
// file and the GitHub release body. Empty fields mean the renderer defaults.
type RecordFormat struct {
	DateFormat        string
	BreakingTitle     string
	FeaturesTitle     string
	FixesTitle        string
	DependenciesTitle string
}

// ChangelogSpec is a package's resolved changelog policy.
type ChangelogSpec struct {
	Enabled bool
	File    string // empty means the writer default (CHANGELOG.md)
	Title   string // empty means the writer default
	Format  RecordFormat
}

// GitHubSpec is a package's resolved GitHub-release policy. Owner/Repo may
// be empty here: the runtime fallback to $GITHUB_REPOSITORY stays with the
// releaser resolution, which is where "unresolvable" is an outcome.
type GitHubSpec struct {
	Enabled bool
	// AllPackages creates a release for every published package, even without
	// the DISPAT_EXPORT_GITHUB export (which then only adds assets).
	AllPackages bool
	Owner       string
	Repo        string
	APIURL      string // empty means the public GitHub API
	TokenEnv    string // empty means GITHUB_TOKEN
	Format      RecordFormat
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
