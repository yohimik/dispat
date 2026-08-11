// Package models holds the public configuration model of the dispat CLI: the
// structs a dispat.json / dispat.yaml file decodes into. It contains models
// only — loading, validation and package discovery live in the CLI's internal
// config package — so external tooling (and the black-box integration suite)
// can author configurations as typed values and marshal them to JSON instead
// of hand-writing raw config strings.
//
// Every field carries both a mapstructure tag (how viper decodes the file) and
// a json tag with the same key (how a model marshals back into a loadable
// file). Viper treats keys case-insensitively and lowercases map keys, so
// script and space names are matched case-insensitively.
//
// `scripts` is one shape at three levels — the file, a space and a package —
// and a package resolves a name through its own map first, then its space's,
// then the file's. Every level's values are shell commands; `flow` entries and
// `dispat run <name>` both name them.
package models

import (
	"strings"

	"github.com/yohimik/dispat/pkg/ccme"
)

// File mirrors the configuration at the monorepo root. Viper infers the
// format from the file extension (yaml, json, toml, ...).
type File struct {
	Scripts map[string]string      `mapstructure:"scripts" json:"scripts,omitempty"`
	Spaces  map[string]SpaceConfig `mapstructure:"spaces" json:"spaces,omitempty"`
	// Packages holds per-package configuration, keyed by package name. An
	// entry without `path` adjusts the configuration of a package discovered
	// in one of the space folders, matched by folder name (every key must
	// match exactly one folder across all spaces). An entry with `path`
	// declares a standalone package living outside every space, at that
	// root-relative path; see PackageConfig.
	Packages map[string]PackageConfig `mapstructure:"packages" json:"packages,omitempty"`
	// VersionGroups declares shared-versioning groups that cut across the
	// filesystem, keyed by group name. Spaces and packages join a group by
	// naming it in their versionGroup key; see VersionGroupConfig.
	VersionGroups map[string]VersionGroupConfig `mapstructure:"versionGroups" json:"versionGroups,omitempty"`
	// Dependencies declares consumer -> provider edges. Besides the full
	// {consumer, provider, kind, keep} objects, the CLI's loader accepts
	// shorthand array items — an object keyed by consumer name whose value is
	// a provider name or array of names — normalized into full entries at
	// load time. Packages may also declare their own providers; see
	// PackageConfig.Dependencies. All declarations merge into one list.
	Dependencies []DependencyConfig `mapstructure:"dependencies" json:"dependencies,omitempty"`
	// Concurrency accepts a single value applied to both stages
	// (concurrency: 4) or a [build, publish] pair (concurrency: [4, 2]).
	// 0 entries mean "number of CPUs".
	Concurrency []int `mapstructure:"concurrency" json:"concurrency,omitempty"`
	// LogLevel is the minimum level: trace, debug, info, warn or error.
	LogLevel string `mapstructure:"logLevel" json:"logLevel,omitempty"`
	// LogFormat selects the logger output: "pretty" (human console output)
	// or "json" (machine-readable lines for CI ingestion).
	LogFormat string `mapstructure:"logFormat" json:"logFormat,omitempty"`
	// The optional sub-objects are pointers so that an unset object marshals
	// as an absent key rather than as "{}" — omitempty has no effect on a
	// struct value. nil means "all defaults"; the CLI's config loader fills
	// the pointers in after decoding, so code past validation never sees nil.
	Changelog *ChangelogConfig `mapstructure:"changelog" json:"changelog,omitempty"`
	GitHub    *GitHubConfig    `mapstructure:"github" json:"github,omitempty"`
	Commit    *CommitConfig    `mapstructure:"commit" json:"commit,omitempty"`
	// Shell is the command prefix scripts are appended to, e.g.
	// ["bash", "-c"] or ["cmd", "/C"]. Default: ["/bin/sh", "-c"].
	Shell []string `mapstructure:"shell" json:"shell,omitempty"`
	// Initials maps package names to the baseline version used when the
	// package's latest release tag is missing or unparseable (e.g. a stray
	// "pkg@0.0.1.0" tag). The next release bumps on top of this value.
	// Keys are matched case-insensitively against discovered packages.
	Initials map[string]string `mapstructure:"initials" json:"initials,omitempty"`
	// TagFormat is the repository-wide release tag template, overridable per
	// space. Placeholders are {name}, {version} and the optional prerelease
	// pair {channel}/{counter}; every other byte is literal, so
	// "{name}@v{version}" and "services/{name}@v{version}" both work.
	// Default: "{name}@{version}", the form §14 makes normative.
	TagFormat string `mapstructure:"tagFormat" json:"tagFormat,omitempty"`
	// CommitErrors decides what an error in a commit message does to the run
	// (§16):
	//
	//	"warn"  (default) the offending unit contributes nothing and the run
	//	                  continues, which is the blast radius §16 assigns to
	//	                  unit- and message-scoped errors
	//	"error"           any error stops the run before anything is released
	//
	// Repository-scoped errors — a tag that cannot be read, a computed
	// version that goes backwards, a dependency cycle — abort the run under
	// either setting, because §16 requires it: they mean no correct plan
	// exists, so no partial release may be emitted.
	CommitErrors string `mapstructure:"commitErrors" json:"commitErrors,omitempty"`
	// NonPackageScopes are scope names that are deliberately not packages, so
	// naming one is not the typo E130 exists to catch. Default: ["release"],
	// which is the scope of dispat's own release commit — without the
	// exemption every run would poison the next one.
	NonPackageScopes []string `mapstructure:"nonPackageScopes" json:"nonPackageScopes,omitempty"`

	// Run is the run-level hooks object; see RunConfig.
	Run *RunConfig `mapstructure:"run" json:"run,omitempty"`

	// Parser holds the commit-message parser options; see ParserConfig. Every
	// field is optional and defaults to the specification value.
	Parser *ParserConfig `mapstructure:"parser" json:"parser,omitempty"`

	// Resolved values, populated by validation.
	BuildConcurrency   int                     `mapstructure:"-" json:"-"`
	PublishConcurrency int                     `mapstructure:"-" json:"-"`
	InitialVersions    map[string]ccme.Version `mapstructure:"-" json:"-"`
	// ResolvedParser is the ccme parser configuration the `parser` object
	// resolves to. (Its type is deliberately not ParserConfig: that struct is
	// the file's raw shape, this is the parser's.)
	ResolvedParser ccme.Config `mapstructure:"-" json:"-"`
}

// ParserConfig is the top-level `parser` object: the commit-message parser
// options, mirroring the parsing-relevant knobs of ccme.Config. Every field
// is optional; anything unset keeps the specification default, so an absent
// `parser` object is exactly the parser dispat always had.
type ParserConfig struct {
	// Separator is the unit separator line. Default "---"; repositories that
	// exchange patches by mail typically set "%%%".
	Separator string `mapstructure:"separator" json:"separator,omitempty"`
	// Types maps a commit type to its direct bump: "none", "patch", "minor"
	// or "major". A non-empty map REPLACES the standard table wholesale
	// (feat=minor, fix/perf/revert=patch, the rest none), so list every type
	// you want to keep.
	Types map[string]string `mapstructure:"types" json:"types,omitempty"`
	// StrictTypes turns an unknown commit type into an error (E140) instead
	// of a warning.
	StrictTypes bool `mapstructure:"strictTypes" json:"strictTypes,omitempty"`
	// Lenient downgrades selected authoring errors to warnings: an uppercase
	// type is lowercased, a missing space after ':' is accepted, and a footer
	// contradicting an inline directive wins instead of erroring.
	Lenient bool `mapstructure:"lenient" json:"lenient,omitempty"`
	// MaxDescriptionLength is the long-description warning threshold, in
	// Unicode scalar values. Default 100; negative disables the check.
	MaxDescriptionLength int `mapstructure:"maxDescriptionLength" json:"maxDescriptionLength,omitempty"`
	// Propagation holds the propagation defaults units inherit when they
	// carry no directive of their own. nil means all defaults.
	Propagation *ParserPropagationConfig `mapstructure:"propagation" json:"propagation,omitempty"`
	// Limits are the always-enforced parser bounds; exceeding one voids the
	// whole message. Defaults: 64 units, 256 scope terms, 1 MiB. nil keeps
	// every default.
	Limits *ParserLimitsConfig `mapstructure:"limits" json:"limits,omitempty"`
	// AllowedChannels restricts prerelease channel names; empty means
	// unrestricted. "stable" is always accepted.
	AllowedChannels []string `mapstructure:"allowedChannels" json:"allowedChannels,omitempty"`
	// MessageLevelTrailers are the authorship/review trailers ignored
	// wherever they appear (Signed-off-by, Co-authored-by, ...). Setting the
	// key replaces the default list.
	MessageLevelTrailers []string `mapstructure:"messageLevelTrailers" json:"messageLevelTrailers,omitempty"`
	// IssueTrailers are the issue-reference trailers (Closes, Fixes, ...),
	// ignored for versioning but surfaced for changelog use. Setting the key
	// replaces the default list.
	IssueTrailers []string `mapstructure:"issueTrailers" json:"issueTrailers,omitempty"`
}

// ParserPropagationConfig is the `parser.propagation` object: what a unit
// propagates when it says nothing itself. A directive written on the unit
// always wins over these defaults.
type ParserPropagationConfig struct {
	// Bump is the default propagated bump: "none", "patch" (default),
	// "minor", "major" or "inherit" (copy the unit's own bump).
	Bump string `mapstructure:"bump" json:"bump,omitempty"`
	// Depth is the default propagation depth: a number of edges, or "all"
	// for the transitive closure. Default 0 — nothing propagates unless a
	// unit opts in. Repositories that bundle their dependencies usually set 1.
	Depth string `mapstructure:"depth" json:"depth,omitempty"`
	// ChannelDepth is the channel axis counterpart of Depth. Default 0.
	ChannelDepth string `mapstructure:"channelDepth" json:"channelDepth,omitempty"`
	// Kinds are the dependency edges propagation follows: "dependencies",
	// "peerDependencies", "optionalDependencies", "devDependencies" or
	// "all". Default: every kind except devDependencies.
	Kinds []string `mapstructure:"kinds" json:"kinds,omitempty"`
	// Channel is the default propagated channel: "inherit" (default),
	// "none", "stable" or a channel name.
	Channel string `mapstructure:"channel" json:"channel,omitempty"`
}

// ParserLimitsConfig is the `parser.limits` object. Zero values keep the
// defaults; a negative value disables that bound (trusted input only).
type ParserLimitsConfig struct {
	UnitsPerMessage   int `mapstructure:"unitsPerMessage" json:"unitsPerMessage,omitempty"`
	ScopeTermsPerUnit int `mapstructure:"scopeTermsPerUnit" json:"scopeTermsPerUnit,omitempty"`
	MessageBytes      int `mapstructure:"messageBytes" json:"messageBytes,omitempty"`
}

// RunConfig is the top-level `run` object: the hooks that observe the run as
// a whole, keyed by hook name. Every value is a script name or an array of
// names, exactly like a space's `flow` entries — the objects share one
// shape, and neither is called `scripts`: `scripts` defines named commands,
// `run` and `flow` say what runs when.
//
// BeforeAll is the one gating run hook: it runs once before the task graph
// starts, when nothing has happened yet, so its failure can honestly stop the
// run — and does, before anything is built, published or tagged.
//
// All the others only warn on failure: they run after release work, when
// failing them could no longer stop anything — a warning is the honest
// report. PostAll runs once after the whole task graph finishes, with the
// run-result variables. The commit hooks bracket the finalize phase —
// beforeCommit / afterCommit around the release commit, postCommit after
// commit and tags — and the push hooks bracket the push; all of them are
// no-ops unless the corresponding phase is enabled and something published.
type RunConfig struct {
	BeforeAll    []string `mapstructure:"beforeAll" json:"beforeAll,omitempty"`
	PostAll      []string `mapstructure:"postAll" json:"postAll,omitempty"`
	BeforeCommit []string `mapstructure:"beforeCommit" json:"beforeCommit,omitempty"`
	AfterCommit  []string `mapstructure:"afterCommit" json:"afterCommit,omitempty"`
	PostCommit   []string `mapstructure:"postCommit" json:"postCommit,omitempty"`
	BeforePush   []string `mapstructure:"beforePush" json:"beforePush,omitempty"`
	AfterPush    []string `mapstructure:"afterPush" json:"afterPush,omitempty"`
}

// EntryFormatConfig customises how a release entry is rendered; shared by the
// changelog file and the GitHub release body. All fields are optional.
type EntryFormatConfig struct {
	DateFormat        string `mapstructure:"dateFormat" json:"dateFormat,omitempty"`               // Go time layout, default "2006-01-02"
	BreakingTitle     string `mapstructure:"breakingTitle" json:"breakingTitle,omitempty"`         // default "Breaking Changes"
	FeaturesTitle     string `mapstructure:"featuresTitle" json:"featuresTitle,omitempty"`         // default "Features"
	FixesTitle        string `mapstructure:"fixesTitle" json:"fixesTitle,omitempty"`               // default "Fixes"
	DependenciesTitle string `mapstructure:"dependenciesTitle" json:"dependenciesTitle,omitempty"` // default "Dependencies"
}

// ChangelogConfig customises (or disables) the per-package changelog file.
type ChangelogConfig struct {
	Enabled           *bool  `mapstructure:"enabled" json:"enabled,omitempty"` // default true
	File              string `mapstructure:"file" json:"file,omitempty"`       // default "CHANGELOG.md"
	Title             string `mapstructure:"title" json:"title,omitempty"`     // default "# Changelog"
	EntryFormatConfig `mapstructure:",squash"`
}

// IsEnabled reports whether the changelog file is written (default true). It
// is nil-safe: an absent changelog object means all defaults.
func (c *ChangelogConfig) IsEnabled() bool { return c == nil || c.Enabled == nil || *c.Enabled }

// GitHubConfig customises (or disables) GitHub release creation.
type GitHubConfig struct {
	Enabled  *bool  `mapstructure:"enabled" json:"enabled,omitempty"`   // default true
	Owner    string `mapstructure:"owner" json:"owner,omitempty"`       // default: derived from $GITHUB_REPOSITORY
	Repo     string `mapstructure:"repo" json:"repo,omitempty"`         // default: derived from $GITHUB_REPOSITORY
	APIURL   string `mapstructure:"apiUrl" json:"apiUrl,omitempty"`     // default https://api.github.com
	TokenEnv string `mapstructure:"tokenEnv" json:"tokenEnv,omitempty"` // env var holding the token, default GITHUB_TOKEN
	// AllPackages creates a GitHub release for every published package, even
	// when no script exported DISPAT_EXPORT_GITHUB (the export then only adds
	// assets). Default false: the export stays the per-package opt-in.
	AllPackages       *bool `mapstructure:"allPackages" json:"allPackages,omitempty"`
	EntryFormatConfig `mapstructure:",squash"`
}

// IsEnabled reports whether GitHub releases are created (default true; still
// requires a resolvable repository and token at runtime). Nil-safe.
func (c *GitHubConfig) IsEnabled() bool { return c == nil || c.Enabled == nil || *c.Enabled }

// AllPackagesEnabled reports whether every published package gets a GitHub
// release regardless of the DISPAT_EXPORT_GITHUB export. Nil-safe.
func (c *GitHubConfig) AllPackagesEnabled() bool {
	return c != nil && c.AllPackages != nil && *c.AllPackages
}

// CommitConfig customises the finalize phase: a single release commit created
// at the end of a successful run, capturing changelog and version-script
// manifest changes of all published packages. Disabled by default. When
// enabled, tags are created on the release commit (after it) instead of
// during each publish, and GitHub releases move to the end of the run — after
// the push when push is enabled, so they reference commits and tags that
// exist on the remote.
type CommitConfig struct {
	Enabled *bool `mapstructure:"enabled" json:"enabled,omitempty"` // default false
	// MessageFormat supports {tags} and {packages} placeholders (comma-
	// separated lists). Default: "chore(release): {tags}".
	MessageFormat string `mapstructure:"messageFormat" json:"messageFormat,omitempty"`
	// Push pushes the release commit and tags. Tags that already exist on
	// the remote are skipped with a warning; the rest are pushed.
	Push   bool   `mapstructure:"push" json:"push,omitempty"`     // default false
	Remote string `mapstructure:"remote" json:"remote,omitempty"` // default "origin"
	// Verify controls the upfront remote-access check (git ls-remote) run
	// before any release work when Push is enabled. Default true; set false
	// to skip it, e.g. for a remote that rejects ls-remote but accepts
	// pushes.
	Verify *bool `mapstructure:"verify" json:"verify,omitempty"` // default true
	// Include lists extra repo-relative paths the release commit stages on
	// top of the published packages' folders: the shared artifacts a version
	// stage or an autoVersion syncLock regenerates outside every package
	// folder, a workspace-level package-lock.json first among them. Paths
	// must stay inside the repository (no absolute paths, no "..") and may
	// name files that do not exist yet.
	Include []string `mapstructure:"include" json:"include,omitempty"`
	// Name and Email, when set, are the git identity every commit and
	// annotated tag dispat creates is authored under, so a CI run needs no
	// `git config` step. Empty values fall back to git's own configuration.
	Name  string `mapstructure:"name" json:"name,omitempty"`
	Email string `mapstructure:"email" json:"email,omitempty"`
}

// IsEnabled reports whether the release commit is created (default false).
// Nil-safe.
func (c *CommitConfig) IsEnabled() bool { return c != nil && c.Enabled != nil && *c.Enabled }

// PushEnabled reports whether the release commit and tags are pushed; only
// meaningful with the commit enabled. Nil-safe.
func (c *CommitConfig) PushEnabled() bool { return c.IsEnabled() && c.Push }

// VerifyEnabled reports whether remote access is verified before any release
// work when pushing (default true). Nil-safe.
func (c *CommitConfig) VerifyEnabled() bool { return c == nil || c.Verify == nil || *c.Verify }

// Versioning values of a space (the `versioning` key).
const (
	// VersioningIndependent is the default: every package's version is
	// computed from its own history alone.
	VersioningIndependent = "independent"
	// VersioningFixed keeps every package of the space on one shared version:
	// a change to any package releases every package of the space at the same
	// (single) next version, with a single prerelease train. Commit and file
	// scopes still decide which changelog entries a package receives; a
	// package released only to keep the versions aligned gets a single
	// "no changes" changelog entry.
	VersioningFixed = "fixed"
	// VersioningFixedSparse computes the space version exactly like fixed,
	// but a package with no changes of its own keeps its previous version and
	// is not released; changed packages release at the shared version.
	VersioningFixedSparse = "fixedSparse"
)

// VersionGroupConfig declares one entry of the top-level `versionGroups`
// map: a shared-versioning group whose membership is stated by the members
// themselves, through their versionGroup key. The declaration owns the
// group's versioning mode — "fixed" or "fixedSparse"; "independent" is
// invalid, because a group exists to share — so every member moves under one
// rule and a member cannot contradict it.
type VersionGroupConfig struct {
	Versioning string `mapstructure:"versioning" json:"versioning,omitempty"`
}

// SpaceConfig is the raw configuration of one space. Everything the space
// runs — stages, hooks, outcome scripts — lives in its `flow` object.
type SpaceConfig struct {
	Path                  string           `mapstructure:"path" json:"path,omitempty"`
	IsBuildWaitingPublish bool             `mapstructure:"isBuildWaitingPublish" json:"isBuildWaitingPublish,omitempty"`
	RevertOnFail          bool             `mapstructure:"revertOnFail" json:"revertOnFail,omitempty"`
	Flow                  *SpaceFlowConfig `mapstructure:"flow" json:"flow,omitempty"`
	// TagFormat overrides the repository-wide tagFormat for this space.
	TagFormat string `mapstructure:"tagFormat" json:"tagFormat,omitempty"`
	// Versioning selects how versions relate across the space's packages:
	// "independent" (default), "fixed" or "fixedSparse". See the Versioning*
	// constants.
	Versioning string `mapstructure:"versioning" json:"versioning,omitempty"`
	// VersionGroup names the shared-versioning group the space's packages
	// join: an entry of the top-level versionGroups map, or the name of
	// another space with fixed/fixedSparse versioning. Empty means the
	// space's own implicit group (its name) when its versioning is shared.
	// A declared group's versioning mode is authoritative, so a space naming
	// one must not set versioning itself.
	VersionGroup string `mapstructure:"versionGroup" json:"versionGroup,omitempty"`
	// Scripts are the space's named shell commands, the same shape as the
	// file's own `scripts` and layered over it name by name: the space's
	// packages resolve a name here before falling back to the top level.
	// `flow` entries name them, and `dispat run <name>` executes the one it
	// resolves inside each changed package of the space, in topological order,
	// with the package's full DISPAT_* environment.
	Scripts map[string]string `mapstructure:"scripts" json:"scripts,omitempty"`
	// AutoVersion enables native manifest rewriting at the version stage:
	// dispat itself updates the declared ranges of workspace dependencies
	// (and the package's own version field) in package.json and go.mod,
	// before any flow.version script runs. nil means off. See
	// AutoVersionConfig.
	AutoVersion *AutoVersionConfig `mapstructure:"autoVersion" json:"autoVersion,omitempty"`
	// Packages holds per-package configuration for this space's packages
	// alone, keyed by folder name — the same entry shape as the file's own
	// `packages` map, scoped to the space that owns the folders. Every key
	// must match exactly one folder of this space, and no entry may carry a
	// `path`: a space package's location is its folder, and a package living
	// outside every space is declared in the top-level map instead. An entry
	// here outranks the top-level entry for the same package, being the
	// nearer statement about it.
	Packages map[string]PackageConfig `mapstructure:"packages" json:"packages,omitempty"`
}

// SpaceFile is the top-level object of a dispat config file placed inside a
// space folder: the space's own configuration, overriding what the root
// file's `spaces` entry says about it field by field, plus the `packages`
// entries for the folders next to the file.
//
// It mirrors SpaceConfig minus `path` — the file sits in the space folder, so
// the folder it lives in already *is* the path, and a file able to redefine
// it could point a space at a folder it is not in. `spaces` is refused for
// the same reason a package folder's file refuses it: a file declaring spaces
// is a monorepo root of its own, and a nested root must be ignored rather
// than half-merged.
//
// A field left unset inherits from the root file's space entry, which is why
// the scalar booleans are pointers here where SpaceConfig's are plain: an
// override must be able to say nothing.
type SpaceFile struct {
	IsBuildWaitingPublish *bool                    `mapstructure:"isBuildWaitingPublish" json:"isBuildWaitingPublish,omitempty"`
	RevertOnFail          *bool                    `mapstructure:"revertOnFail" json:"revertOnFail,omitempty"`
	Flow                  *SpaceFlowConfig         `mapstructure:"flow" json:"flow,omitempty"`
	TagFormat             string                   `mapstructure:"tagFormat" json:"tagFormat,omitempty"`
	Versioning            string                   `mapstructure:"versioning" json:"versioning,omitempty"`
	VersionGroup          string                   `mapstructure:"versionGroup" json:"versionGroup,omitempty"`
	Scripts               map[string]string        `mapstructure:"scripts" json:"scripts,omitempty"`
	AutoVersion           *AutoVersionConfig       `mapstructure:"autoVersion" json:"autoVersion,omitempty"`
	Packages              map[string]PackageConfig `mapstructure:"packages" json:"packages,omitempty"`
}

// PackageConfig is one entry of a `packages` map. Without `path` it overrides
// the enclosing space's configuration for the package whose folder name
// matches the entry key; with `path` — only ever in the file's top-level map —
// it declares a standalone package outside every space, whose configuration
// the entry itself is. It mirrors SpaceConfig's keys plus the package-only
// keys: `changelog`, `github`, `concurrency` and `dependencies`. The same
// shape is the top-level object of a dispat config file placed inside a
// package folder.
//
// One entry shape therefore serves four override layers, applied in this
// order, each overlaying the previous field by field and the one nearest the
// package winning:
//
//  1. the file's top-level `packages` entry
//  2. the space's `packages` entry (SpaceConfig.Packages)
//  3. the space folder file's `packages` entry (SpaceFile.Packages)
//  4. the package folder's own config file
//
// under the space's configuration, itself the root file's `spaces` entry
// overlaid with the space folder file. A standalone package has no space, so
// only layers 1 and 4 apply, over a synthetic single-package base.
//
// The entry has no `packages` and no `spaces` field, and that is deliberate:
// a package configures one package, so a package holding spaces or packages
// of its own is refused wherever it is written. `path` is refused everywhere
// but layer 1 — a package inside a space lives in its folder, and neither the
// space nor the file inside the folder may move it.
//
// A field left unset inherits from the layer below, which is why the scalar
// booleans are pointers here where SpaceConfig's are plain: an override must
// be able to say nothing. `flow.login` is deliberately absent from the
// override surface (validation rejects it): login runs once per space, in
// the space folder, gating every publish of the space — a per-package login
// would contradict all three.
type PackageConfig struct {
	// Path declares a standalone package at this root-relative folder,
	// outside every space. Only valid on the file's top-level `packages`
	// entry whose key matches no space folder: a space package's location is
	// its folder and cannot be redefined, so neither a space's own `packages`
	// entry nor a config file inside a folder may set it.
	Path                  string           `mapstructure:"path" json:"path,omitempty"`
	IsBuildWaitingPublish *bool            `mapstructure:"isBuildWaitingPublish" json:"isBuildWaitingPublish,omitempty"`
	RevertOnFail          *bool            `mapstructure:"revertOnFail" json:"revertOnFail,omitempty"`
	Flow                  *SpaceFlowConfig `mapstructure:"flow" json:"flow,omitempty"`
	TagFormat             string           `mapstructure:"tagFormat" json:"tagFormat,omitempty"`
	// Versioning overrides how the package relates to its space's shared
	// version — most usefully "independent", opting one package out of a
	// fixed space. Mutually exclusive with naming a declared versionGroup,
	// whose mode is authoritative.
	Versioning string `mapstructure:"versioning" json:"versioning,omitempty"`
	// VersionGroup names the shared-versioning group this package joins; see
	// SpaceConfig.VersionGroup.
	VersionGroup string `mapstructure:"versionGroup" json:"versionGroup,omitempty"`
	// Scripts are merged into the space's map name by name; a name set here
	// wins over the space's, which wins over the file's. A name only this
	// package defines is the package's alone: `dispat run <name>` reaches no
	// other package with it.
	Scripts     map[string]string  `mapstructure:"scripts" json:"scripts,omitempty"`
	AutoVersion *AutoVersionConfig `mapstructure:"autoVersion" json:"autoVersion,omitempty"`
	// ManifestNames are the manifest names this package is known by, stated
	// here rather than read from its files. They exist for the packages whose
	// manifests declare no name the workspace can learn — a Gradle module, a
	// bare Makefile project, a folder in an ecosystem dispat cannot parse —
	// so `dispat compute` still derives the edges pointing at it and
	// auto-versioning still reconciles the declarations naming it.
	//
	// A stated name outranks one a manifest declares, and no two packages may
	// state the same name.
	ManifestNames []string `mapstructure:"manifestNames" json:"manifestNames,omitempty"`
	// Changelog and GitHub overlay the top-level objects field by field for
	// this package's release records — flip enabled, rename the file, target
	// another repository — leaving unset fields at the global values.
	Changelog *ChangelogConfig `mapstructure:"changelog" json:"changelog,omitempty"`
	GitHub    *GitHubConfig    `mapstructure:"github" json:"github,omitempty"`
	// Concurrency is the number of stage-budget slots the package's tasks
	// occupy: a single value for both stages or a [build, publish] pair.
	// Absent or 0 means 1, the ordinary cost; a package whose value reaches
	// the stage's budget runs that stage alone. (Deliberately unlike the
	// top-level key, where 0 means "number of CPUs" — a weight has no CPU
	// reading.)
	Concurrency []int `mapstructure:"concurrency" json:"concurrency,omitempty"`
	// Dependencies names the provider packages this package depends on — a
	// package name or an array of names (weak decoding lifts the scalar into
	// a slice). The consumer is the package itself and the edge kind is the
	// default ("dependencies"); an edge needing another kind or `keep` is
	// declared in the top-level list instead. Entry-layer and in-folder-layer
	// lists both count: all declarations merge with the top-level list.
	Dependencies []string `mapstructure:"dependencies" json:"dependencies,omitempty"`
}

// AutoVersionConfig is a space's `autoVersion` object: the native
// manifest-rewriting policy of the version stage (§9.4, §12.4). The presence
// of the object enables the feature unless `enabled: false` says otherwise.
type AutoVersionConfig struct {
	// Enabled turns the block off without deleting it. Default true when the
	// block sets any key at all. A completely empty {} block is treated as
	// absent (the config loader's flattening prunes empty objects), so the
	// minimal opt-in is {"enabled": true}.
	Enabled *bool `mapstructure:"enabled" json:"enabled,omitempty"`
	// Manifests selects which manifests of a package are parsed and
	// rewritten: "root" (default) — only manifests directly in the package
	// folder — "all", every manifest found under it, or "none", which turns
	// the parsing strategy off entirely and leaves the work to `replace` and
	// `syncLock`.
	Manifests string `mapstructure:"manifests" json:"manifests,omitempty"`
	// Replace are the literal text substitutions applied to the package's
	// files after (or instead of) the manifest rewriting: the strategy for
	// the versions no manifest writer can reach. Empty means off.
	Replace []AutoVersionReplaceConfig `mapstructure:"replace" json:"replace,omitempty"`
	// Kinds restricts rewriting to the named manifest fields
	// ("dependencies", "devDependencies", "peerDependencies",
	// "optionalDependencies"). Empty means all four.
	Kinds []string `mapstructure:"kinds" json:"kinds,omitempty"`
	// Only restricts rewriting to declarations of the named provider
	// packages. Empty means every workspace provider.
	Only []string `mapstructure:"only" json:"only,omitempty"`
	// NameMatch selects how a declared dependency name is matched onto a
	// workspace package when neither a manifest-declared name nor a local
	// path already matches:
	//
	//	"exact" (default)  only names the workspace's manifests declare
	//	"substring"        additionally, a declared name whose last segment
	//	                   (after / or :) equals a package's folder name
	//	                   matches that package — so package "app" matches a
	//	                   declared "@core/app", "com.acme:app" or a bare
	//	                   "app" line, even when the app package has no
	//	                   parseable manifest of its own
	NameMatch string `mapstructure:"nameMatch" json:"nameMatch,omitempty"`
	// Match restricts rewriting to declared ranges matching one of the
	// globs, e.g. ["workspace:*"] — so a range the user pinned by hand is
	// never overridden. Empty means any declared range is rewritten.
	Match []string `mapstructure:"match" json:"match,omitempty"`
	// Range is the write policy — what the new declared range looks like,
	// built from the provider's end-of-run version:
	//
	//	"caret" (default)   ^1.2.3
	//	"tilde"             ~1.2.3
	//	"exact"             1.2.3
	//	a {version} template  e.g. ">={version}"
	//	any other literal   written verbatim, e.g. "workspace:*"
	//
	// go.mod requires exact canonical versions, so Go manifests always
	// receive vX.Y.Z regardless of this policy.
	Range string `mapstructure:"range" json:"range,omitempty"`
	// WriteVersion also writes the package's own new version into its
	// manifest's version field (§12.4; a drifted manifest version is W192).
	// Default true.
	WriteVersion *bool `mapstructure:"writeVersion" json:"writeVersion,omitempty"`
	// SyncLock names top-level scripts run inside the package folder after
	// its manifests were rewritten (e.g. "npm install" to sync the lock
	// file), between the version and build stages.
	SyncLock []string `mapstructure:"syncLock" json:"syncLock,omitempty"`
	// SyncLockConcurrency caps how many syncLock scripts run at the same
	// moment across the whole run — shared lock files corrupt under
	// parallel writers, so the default is 1. When spaces disagree, the
	// smallest configured value wins.
	SyncLockConcurrency int `mapstructure:"syncLockConcurrency" json:"syncLockConcurrency,omitempty"`
}

// AutoVersionReplaceConfig is one entry of an `autoVersion.replace` list: a
// literal find/write pair applied to the files matching a set of globs.
//
// Both texts are templates over the release being made. {name}, {version} and
// {previous} stand for the package itself; {provider}, {providerVersion} and
// {providerPrevious} stand for one of its configured providers, and a rule
// mentioning any of the three is expanded once per provider.
//
// Nothing is parsed, so the rule reaches any file at all. That also means it
// does exactly what it says: a find that matches somewhere unintended is
// replaced there too, which is why a rule should carry enough context to be
// unambiguous.
type AutoVersionReplaceConfig struct {
	// Files are globs, relative to the package folder, selecting what the
	// rule applies to. "*" matches any run of characters, separators
	// included, so "*.gradle" reaches nested build scripts. Dependency,
	// virtual-environment and build-output folders are never entered.
	// Required.
	Files []string `mapstructure:"files" json:"files,omitempty"`
	// Find is the literal text to look for, after the placeholders in it are
	// filled in. Required.
	Find string `mapstructure:"find" json:"find,omitempty"`
	// Write is the literal text to put in its place, after the placeholders
	// in it are filled in. Required.
	Write string `mapstructure:"write" json:"write,omitempty"`
}

// IsEnabled reports whether the autoVersion block is active (default true
// when the block is present). Nil-safe.
func (c *AutoVersionConfig) IsEnabled() bool {
	return c != nil && (c.Enabled == nil || *c.Enabled)
}

// WriteVersionEnabled reports whether the package's own version field is
// rewritten (default true). Nil-safe.
func (c *AutoVersionConfig) WriteVersionEnabled() bool {
	return c != nil && (c.WriteVersion == nil || *c.WriteVersion)
}

// SpaceFlowConfig is a space's `flow` object: what runs at which stage, keyed
// by stage or hook name with no decoration. All entries are optional — a
// stage with no script still runs, an unset hook is a no-op — and every one
// accepts a single script name or an array of names run in order (weak
// decoding lifts the scalar into a one-element slice, the same way a scalar
// concurrency becomes a pair).
type SpaceFlowConfig struct {
	Build   []string `mapstructure:"build" json:"build,omitempty"`
	Publish []string `mapstructure:"publish" json:"publish,omitempty"`
	Version []string `mapstructure:"version" json:"version,omitempty"`
	// Login runs once per space before its first publish; every other
	// publish of the space waits for it, and its failure fails them all.
	Login []string `mapstructure:"login" json:"login,omitempty"`
	// Announce is a fourth stage after a successful publish: pushing the
	// release out to update channels, with the release-notes variables in its
	// environment. The whole frame — hooks included — only warns on failure.
	Announce []string `mapstructure:"announce" json:"announce,omitempty"`
	// Hooks around the package stages. The before*/post* hooks up to
	// beforePublish fail the package's release when they fail; postPublish and
	// the announce hooks only warn, because by then the release is out.
	BeforeAll      []string `mapstructure:"beforeAll" json:"beforeAll,omitempty"`
	BeforeVersion  []string `mapstructure:"beforeVersion" json:"beforeVersion,omitempty"`
	PostVersion    []string `mapstructure:"postVersion" json:"postVersion,omitempty"`
	BeforeBuild    []string `mapstructure:"beforeBuild" json:"beforeBuild,omitempty"`
	PostBuild      []string `mapstructure:"postBuild" json:"postBuild,omitempty"`
	BeforePublish  []string `mapstructure:"beforePublish" json:"beforePublish,omitempty"`
	PostPublish    []string `mapstructure:"postPublish" json:"postPublish,omitempty"`
	BeforeAnnounce []string `mapstructure:"beforeAnnounce" json:"beforeAnnounce,omitempty"`
	PostAnnounce   []string `mapstructure:"postAnnounce" json:"postAnnounce,omitempty"`
	// Outcome scripts, both warn-only: onFail runs when a package of the
	// space fails at any stage, onSkip when it is skipped because a provider
	// failed.
	OnFail []string `mapstructure:"onFail" json:"onFail,omitempty"`
	OnSkip []string `mapstructure:"onSkip" json:"onSkip,omitempty"`
}

// DependencyConfig is one consumer -> provider relation.
// The yaml tags exist because the CLI's compute command re-encodes this one
// struct when editing a YAML config in place; without them the encoder would
// write lowercased field names with `kind: ""` / `keep: false` noise on every
// edge.
type DependencyConfig struct {
	Consumer string `mapstructure:"consumer" json:"consumer,omitempty" yaml:"consumer"`
	Provider string `mapstructure:"provider" json:"provider,omitempty" yaml:"provider"`
	// Kind is the manifest dependency field the edge stands for:
	// "dependencies" (the default when empty), "devDependencies",
	// "peerDependencies" or "optionalDependencies". Propagation follows or
	// ignores the edge according to parser.propagation.kinds, whose default
	// is every kind except devDependencies.
	Kind string `mapstructure:"kind" json:"kind,omitempty" yaml:"kind,omitempty"`
	// Keep marks an edge `dispat compute` must never suggest removing: the
	// declaration is deliberate even though no manifest declares it (a Docker
	// image chain, a codegen coupling). Purely a compute-command annotation —
	// the planner treats kept edges like any other.
	Keep bool `mapstructure:"keep" json:"keep,omitempty" yaml:"keep,omitempty"`
}

// Values of the commitErrors key.
const (
	// CommitErrorsWarn is the §16 blast radius: a unit- or message-scoped
	// error invalidates only what it names, and the run continues.
	CommitErrorsWarn = "warn"
	// CommitErrorsError stops the run on any error at all. Stricter than §16,
	// and the setting to choose when a mistyped scope silently dropping a
	// package is the worse failure.
	CommitErrorsError = "error"
)

// DefaultNonPackageScopes returns the scopes exempt from the unknown-include
// error by default. "release" is dispat's own release-commit scope.
func DefaultNonPackageScopes() []string { return []string{"release"} }

// Bool returns a pointer to b — the helper for the tri-state *bool option
// fields (Changelog.Enabled, GitHub.Enabled, Commit.Enabled, Commit.Verify),
// whose nil means "use the default".
func Bool(b bool) *bool { return &b }

// Script resolves a script reference case-insensitively, because viper
// lowercases the keys of the scripts map.
func (c *File) Script(ref string) (string, bool) {
	s, ok := c.Scripts[strings.ToLower(ref)]
	return s, ok
}

// Script resolves one of the space's own scripts case-insensitively, for the
// same viper reason as File.Script. It looks no further than this level: the
// fallback to the file's scripts belongs to the layer that knows the package.
func (s SpaceConfig) Script(name string) (string, bool) {
	cmd, ok := s.Scripts[strings.ToLower(name)]
	return cmd, ok
}

// Package resolves a `packages` entry by package name case-insensitively,
// for the same viper reason as Script.
func (c *File) Package(name string) (PackageConfig, bool) {
	pc, ok := c.Packages[strings.ToLower(name)]
	return pc, ok
}

// Package resolves one of the space file's `packages` entries by package
// name, case-insensitively for the same viper reason as File.Package.
func (f SpaceFile) Package(name string) (PackageConfig, bool) {
	pc, ok := f.Packages[strings.ToLower(name)]
	return pc, ok
}

// Package resolves one of the space's own `packages` entries by package name,
// case-insensitively for the same viper reason as File.Package. It looks no
// further than this level: the file's entry is a separate layer, applied
// before this one.
func (s SpaceConfig) Package(name string) (PackageConfig, bool) {
	pc, ok := s.Packages[strings.ToLower(name)]
	return pc, ok
}

// Commands resolves a sequence of script references into the shell commands
// they name, preserving order. Unknown references were rejected by validation,
// so resolution cannot silently drop one.
func (c *File) Commands(refs []string) []string {
	if len(refs) == 0 {
		return nil
	}
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if s, ok := c.Script(ref); ok {
			out = append(out, s)
		}
	}
	return out
}
