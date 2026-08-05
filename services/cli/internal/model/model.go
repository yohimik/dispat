// Package model holds the domain types shared across the tool.
package model

// Space groups packages that share build and publish behaviour.
type Space struct {
	Name string
	// Path of the space folder, relative to the monorepo root. Every direct
	// sub-folder is a package.
	Path string
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
}

// Package is a single releasable folder inside a space.
type Package struct {
	Name  string
	Dir   string // folder in which scripts run
	Space *Space
}

// DepKind is the manifest dependency field a graph edge stands for (§8.4).
//
// Which fields imply "must be republished" is a property of the repository
// rather than of any one commit, so §8.4 gives units no override: the set of
// traversed kinds is fixed for the whole run. dispat has no manifest model and
// no configuration for this, so every configured edge is a KindDependencies
// edge — but the distinction is carried through the graph so that a unit's
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
