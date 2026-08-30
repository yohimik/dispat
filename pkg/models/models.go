// Package models holds the public configuration model of the dispat CLI: the
// structs a dispat.json / dispat.yaml file decodes into. It contains models
// only — loading, validation and package discovery live in the CLI's internal
// config package — so external tooling (and the black-box integration suite)
// can author configurations as typed values and marshal them to JSON instead
// of hand-writing raw config strings.
//
// Every field carries one json tag, and it says both things: which key of a
// config file fills the field, and which key the model marshals back into a
// loadable file. dispat matches those keys case-insensitively, and matches the
// keys of a map — a script, space or package name — case-insensitively too,
// through the helpers below rather than by renaming anything: a map key holds
// the case the file wrote it in.
//
// `scripts` is one shape at three levels — the file, a space and a package —
// and a package resolves a name through its own map first, then its space's,
// then the file's. Every level's values are shell commands, one per entry or a
// sequence of them (see Script); `flow` entries and `dispat run <name>` both
// name them.
package models

import (
	"encoding/json"
	"strings"

	"github.com/yohimik/dispat/pkg/ccme"
)

// File mirrors the configuration at the monorepo root. The file extension
// decides the format (yaml, json, toml, ...).
type File struct {
	Scripts map[string]Script      `json:"scripts,omitempty"`
	Spaces  map[string]SpaceConfig `json:"spaces,omitempty"`
	// Packages holds per-package configuration, keyed by package name. An
	// entry without `path` adjusts the configuration of a package discovered
	// in one of the space folders, matched by folder name (every key must
	// match exactly one folder across all spaces). An entry with `path`
	// declares a standalone package living outside every space, at that
	// root-relative path; see PackageConfig.
	Packages map[string]PackageConfig `json:"packages,omitempty"`
	// VersionGroups declares shared-versioning groups that cut across the
	// filesystem, keyed by group name. Spaces and packages join a group by
	// naming it in their versionGroup key; see VersionGroupConfig.
	VersionGroups map[string]VersionGroupConfig `json:"versionGroups,omitempty"`
	// Dependencies declares consumer -> provider edges, as an object keyed by
	// consumer. See the Dependencies type. Spaces and packages may declare
	// their own; see SpaceConfig.Dependencies and PackageConfig.Dependencies.
	// All declarations merge into one list.
	Dependencies Dependencies `json:"dependencies,omitempty"`
	// Concurrency accepts a single value applied to both stages
	// (concurrency: 4) or a [build, publish] pair (concurrency: [4, 2]).
	// 0 entries mean "number of CPUs".
	Concurrency []int `json:"concurrency,omitempty"`
	// LogLevel is the minimum level: trace, debug, info, warn or error.
	LogLevel string `json:"logLevel,omitempty"`
	// LogFormat selects the logger output: "pretty" (human console output)
	// or "json" (machine-readable lines for CI ingestion).
	LogFormat string `json:"logFormat,omitempty"`
	// The optional sub-objects are pointers so that an unset object marshals
	// as an absent key rather than as "{}" — omitempty has no effect on a
	// struct value. nil means "all defaults"; the CLI's config loader fills
	// the pointers in after decoding, so code past validation never sees nil.
	Changelog *ChangelogConfig `json:"changelog,omitempty"`
	GitHub    *GitHubConfig    `json:"github,omitempty"`
	Commit    *CommitConfig    `json:"commit,omitempty"`
	// Shell is the command prefix scripts are appended to, e.g.
	// ["bash", "-c"] or ["cmd", "/C"]. Default: ["/bin/sh", "-c"].
	Shell []string `json:"shell,omitempty"`
	// Env is static environment added to every script the run executes,
	// exported with the keys spelled exactly as the file writes them. Spaces
	// and packages declare their own `env` objects; the layers merge key by
	// key with the most local winning — package over space over this map —
	// and the computed DISPAT_* variables always win over static env. Values
	// may reference other variables ($NAME, ${NAME}), resolved against the
	// computed set first and the process environment second.
	Env map[string]string `json:"env,omitempty"`
	// Custom is an optional free-form object dispat itself never reads: a
	// place for anything the repository's own tooling wants to keep in the
	// config file without tripping the unknown-key guard. Spaces and package
	// entries have their own independent `custom` objects; nothing merges.
	Custom map[string]any `json:"custom,omitempty"`
	// Initials maps package names to the baseline version used when the
	// package's latest release tag is missing or unparseable (e.g. a stray
	// "pkg@0.0.1.0" tag). The next release bumps on top of this value.
	// Keys are matched case-insensitively against discovered packages.
	Initials map[string]string `json:"initials,omitempty"`
	// TagFormat is the repository-wide release tag template, overridable per
	// space. Placeholders are {name}, {version} and the optional prerelease
	// pair {channel}/{counter}; every other byte is literal, so
	// "{name}@v{version}" and "services/{name}@v{version}" both work.
	// Default: "{name}@{version}", the form §14 makes normative.
	TagFormat string `json:"tagFormat,omitempty"`
	// AliasTags are extra tags each release is written under, beside the one
	// tagFormat produces; see AliasTagConfig. Overridable per space and per
	// package, where a list replaces the inherited one rather than adding to it.
	AliasTags []AliasTagConfig `json:"aliasTags,omitempty"`
	// Webhooks are HTTP endpoints notified of release progress; see
	// WebhookConfig. Deliveries are asynchronous and observe only: a failed or
	// unreachable endpoint warns and never affects the release.
	Webhooks []WebhookConfig `json:"webhooks,omitempty"`

	// The repository-wide defaults for the space-shaped keys. Each is the
	// bottom of the same ladder a package's configuration is folded through —
	// root, then the space, then the space folder's file, then the package's
	// layers — so stating one here is stating it once for every space and
	// every standalone package, and any level below can still say otherwise.
	//
	// Flow includes `login`: it still runs once per space, in the space
	// folder, and declaring it here only saves repeating it. A package
	// override may still not touch it.
	Flow *SpaceFlowConfig `json:"flow,omitempty"`
	// AutoVersion is the default manifest-rewriting policy. Like every other
	// autoVersion, a level that states one replaces it wholesale rather than
	// merging into it: its empty fields carry meaning against their siblings.
	AutoVersion           *AutoVersionConfig `json:"autoVersion,omitempty"`
	IsBuildWaitingPublish *bool              `json:"isBuildWaitingPublish,omitempty"`
	RevertOnFail          *bool              `json:"revertOnFail,omitempty"`
	// Versioning is the default versioning mode. It applies under each
	// space's own implicit group, so `fixed` here means every space versions
	// its own packages as one, not that all spaces share a version. Joining
	// spaces into one group is what versionGroups is for, and versionGroup
	// stays a space-and-package key for that reason.
	Versioning string `json:"versioning,omitempty"`
	// Src is the default scope folder, resolved against each package's own
	// folder exactly as a package's own src is; see PackageConfig.Src. A
	// package that has no such folder fails the load, because the alternative
	// is a package that silently owns no files and quietly stops releasing.
	Src string `json:"src,omitempty"`
	// Ignore are change-scope ignore patterns for every package, matched
	// against paths relative to the repository root; see PackageConfig.Ignore.
	Ignore []string `json:"ignore,omitempty"`
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
	CommitErrors string `json:"commitErrors,omitempty"`
	// NonPackageScopes are scope names that are deliberately not packages, so
	// naming one is not the typo E130 exists to catch. Default: ["release"],
	// which is the scope of dispat's own release commit — without the
	// exemption every run would poison the next one.
	NonPackageScopes []string `json:"nonPackageScopes,omitempty"`

	// UpdateCheck asks dispat whether a newer stable release of dispat itself
	// exists, and prints a one-line suggestion when there is one (default
	// true). The check runs alongside the command and is dropped if it has
	// not answered by the time the command is done, so it can never slow a
	// run down; it is skipped entirely when logFormat is "json", since a
	// machine reading the output cannot act on the suggestion.
	UpdateCheck *bool `json:"updateCheck,omitempty"`

	// UnsafeDisableLock turns the release lock off (default false): the tag
	// `dispat release` pushes to the remote before it plans, so that a second
	// release started while one is running is refused rather than raced.
	//
	// It is named unsafe because it is. Switching it off is for repositories
	// with no remote to coordinate through — a scratch clone, a fixture, a
	// local experiment — where the alternative is not an unguarded release but
	// no release at all. DISPAT_UNSAFE_DISABLE_LOCK=true says the same thing
	// for one invocation, and either saying it is enough.
	UnsafeDisableLock bool `json:"unsafeDisableLock,omitempty"`

	// Run is the run-level hooks object; see RunConfig.
	Run *RunConfig `json:"run,omitempty"`

	// Parser holds the commit-message parser options; see ParserConfig. Every
	// field is optional and defaults to the specification value.
	Parser *ParserConfig `json:"parser,omitempty"`

	// SourceFiles are the files this configuration was read from: the config
	// file itself, followed by every file a `$ref` in it named, in the order
	// they were read. Populated by the loader, so that a configuration split
	// across files can say what it was made of.
	SourceFiles []string `json:"-"`

	// Resolved values, populated by validation.
	BuildConcurrency   int                     `json:"-"`
	PublishConcurrency int                     `json:"-"`
	InitialVersions    map[string]ccme.Version `json:"-"`
	// ResolvedParser is the ccme parser configuration the `parser` object
	// resolves to. (Its type is deliberately not ParserConfig: that struct is
	// the file's raw shape, this is the parser's.)
	ResolvedParser ccme.Config `json:"-"`
}

// ParserConfig is the top-level `parser` object: the commit-message parser
// options, mirroring the parsing-relevant knobs of ccme.Config. Every field
// is optional; anything unset keeps the specification default, so an absent
// `parser` object is exactly the parser dispat always had.
type ParserConfig struct {
	// Separator is the unit separator line. Default "---"; repositories that
	// exchange patches by mail typically set "%%%".
	Separator string `json:"separator,omitempty"`
	// Types maps a commit type to its direct bump: "none", "patch", "minor"
	// or "major". A non-empty map REPLACES the standard table wholesale
	// (feat=minor, fix/perf/revert=patch, the rest none), so list every type
	// you want to keep.
	Types map[string]string `json:"types,omitempty"`
	// StrictTypes turns an unknown commit type into an error (E140) instead
	// of a warning.
	StrictTypes bool `json:"strictTypes,omitempty"`
	// Quiet hides the parser's own diagnostics — the E0xx/E1xx errors and
	// W0xx/W1xx warnings a commit message earns — from the log. It is a
	// display decision alone: a hidden error still counts, still blocks the
	// run under commitErrors "error", and is still summarised, so a
	// repository with a noisy history can read its plan without losing the
	// signal that something is wrong.
	Quiet bool `json:"quiet,omitempty"`
	// Lenient downgrades selected authoring errors to warnings: an uppercase
	// type is lowercased, a missing space after ':' is accepted, and a footer
	// contradicting an inline directive wins instead of erroring.
	Lenient bool `json:"lenient,omitempty"`
	// MaxDescriptionLength is the long-description warning threshold, in
	// Unicode scalar values. Default 100; negative disables the check.
	MaxDescriptionLength int `json:"maxDescriptionLength,omitempty"`
	// Propagation holds the propagation defaults units inherit when they
	// carry no directive of their own. nil means all defaults.
	Propagation *ParserPropagationConfig `json:"propagation,omitempty"`
	// Limits are the always-enforced parser bounds; exceeding one voids the
	// whole message. Defaults: 64 units, 256 scope terms, 1 MiB. nil keeps
	// every default.
	Limits *ParserLimitsConfig `json:"limits,omitempty"`
	// AllowedChannels restricts prerelease channel names; empty means
	// unrestricted. "stable" is always accepted.
	AllowedChannels []string `json:"allowedChannels,omitempty"`
	// MessageLevelTrailers are the authorship/review trailers ignored
	// wherever they appear (Signed-off-by, Co-authored-by, ...). Setting the
	// key replaces the default list.
	MessageLevelTrailers []string `json:"messageLevelTrailers,omitempty"`
	// IssueTrailers are the issue-reference trailers (Closes, Fixes, ...),
	// ignored for versioning but surfaced for changelog use. Setting the key
	// replaces the default list.
	IssueTrailers []string `json:"issueTrailers,omitempty"`
}

// ParserPropagationConfig is the `parser.propagation` object: what a unit
// propagates when it says nothing itself. A directive written on the unit
// always wins over these defaults.
type ParserPropagationConfig struct {
	// Bump is the default propagated bump: "none", "patch" (default),
	// "minor", "major" or "inherit" (copy the unit's own bump).
	Bump string `json:"bump,omitempty"`
	// Depth is the default propagation depth: a number of edges, or "all"
	// for the transitive closure. Default 0 — nothing propagates unless a
	// unit opts in. Repositories that bundle their dependencies usually set 1.
	Depth string `json:"depth,omitempty"`
	// ChannelDepth is the channel axis counterpart of Depth. Default 0.
	ChannelDepth string `json:"channelDepth,omitempty"`
	// Kinds are the dependency edges propagation follows: "dependencies",
	// "peerDependencies", "optionalDependencies", "devDependencies" or
	// "all". Default: every kind except devDependencies.
	Kinds []string `json:"kinds,omitempty"`
	// Channel is the default propagated channel: "inherit" (default),
	// "none", "stable" or a channel name.
	Channel string `json:"channel,omitempty"`
}

// ParserLimitsConfig is the `parser.limits` object. Zero values keep the
// defaults; a negative value disables that bound (trusted input only).
type ParserLimitsConfig struct {
	UnitsPerMessage   int `json:"unitsPerMessage,omitempty"`
	ScopeTermsPerUnit int `json:"scopeTermsPerUnit,omitempty"`
	MessageBytes      int `json:"messageBytes,omitempty"`
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
//
// AllowBranch is not a hook but a guard: when set, a release run refuses to
// start unless the checked-out branch matches one of its globs ("main",
// "release/*"). A detached HEAD matches nothing. Read-only commands are not
// guarded, and neither are the step commands, which run inside a release
// stage the guard has already cleared.
type RunConfig struct {
	AllowBranch  []string `json:"allowBranch,omitempty"`
	BeforeAll    []string `json:"beforeAll,omitempty"`
	PostAll      []string `json:"postAll,omitempty"`
	BeforeCommit []string `json:"beforeCommit,omitempty"`
	AfterCommit  []string `json:"afterCommit,omitempty"`
	PostCommit   []string `json:"postCommit,omitempty"`
	BeforePush   []string `json:"beforePush,omitempty"`
	AfterPush    []string `json:"afterPush,omitempty"`
}

// EntryLine is one block of record text — a file title, a header or a footer
// — optionally restricted to some packages. In a list a bare string is
// shorthand for {"line": "<string>"} and a bare array of strings for
// {"line": [...]}, so the common case needs no object at all.
//
// The filters are case-insensitive glob patterns, the same matching the
// --package/--space/--group flags use. Several values under one key match any
// of them; several keys must all match. A line with no filters is written for
// every package.
type EntryLine struct {
	// Line is the text: one line, or several written consecutively.
	Line []string `json:"line,omitempty"`
	// Package, Space and Group restrict which packages the line is written
	// for. Group names a versioning group.
	Package []string `json:"package,omitempty"`
	Space   []string `json:"space,omitempty"`
	Group   []string `json:"group,omitempty"`
	// Channels restricts which releases the line is written for: "stable",
	// "*" for any prerelease channel, or a channel name such as "beta",
	// matched case-insensitively. An empty list writes the line for every
	// release. It is not allowed on a changelog's fileTitle, which is written
	// once and must not vary from one release to the next.
	Channels []string `json:"channels,omitempty"`
}

// AuthorsConfig adds commit authors to a release entry. It is off by default,
// so a repository that says nothing records exactly what it recorded before.
//
// The identity is git's own — the name and email a commit was authored under,
// plus everyone its Co-authored-by trailers name — so no forge is asked who
// that is and the attribution costs no API call.
type AuthorsConfig struct {
	// Placement decides where the authors appear: "off" (default), "inline"
	// (a "(by ...)" suffix on each entry line), "section" (one list under its
	// own heading) or "both". "off" is a value rather than an absence, so a
	// package can switch off what its space turned on.
	Placement string `json:"placement,omitempty"`
	// Format is how one author is written: "fullname" (default) or
	// "username", the local part of the email address.
	Format string `json:"format,omitempty"`
	// Commits chooses which commits the section is built from: "ccme"
	// (default) counts the commits behind the entry's own lines, "all" counts
	// every commit in the release's window, including those whose messages are
	// not release records at all. It has no effect on the inline suffix, which
	// can only ever name the authors of a line that exists.
	Commits string `json:"commits,omitempty"`
	// Include and Exclude filter the authors by case-insensitive glob, tried
	// against the full name, the username and the email address, and matching
	// on any of the three. An empty Include admits everyone; Exclude is
	// applied afterwards and wins, which is what keeps a bot out of a list its
	// pattern would otherwise admit.
	Include []string `json:"include,omitempty"`
	Exclude []string `json:"exclude,omitempty"`
	// Title heads the section, default "Authors".
	Title string `json:"title,omitempty"`
}

// SectionConfig is one element of an entry's `sections` list: either a
// built-in section named by key, or a custom section claiming commit types of
// its own.
//
// The two are told apart by `types`. An element with no types names a built-in
// — "breaking", "features", "fixes" or "dependencies", matched
// case-insensitively — and in a list a bare string is the shorthand for it, so
// `sections: [fixes, features]` reorders the defaults with no objects at all.
// An element with types is a custom section: it claims those commit types out
// of the bump-keyed grouping and renders them under its own title.
//
// Listing sections is never how one is removed. A built-in the list omits is
// appended after the listed ones in the default relative order, because a
// section silently dropped would take released work out of the record with it.
type SectionConfig struct {
	// Title is the custom section's heading, or the built-in's key when the
	// element names one. A built-in keeps its configured title
	// (breakingTitle, featuresTitle, fixesTitle, dependenciesTitle), so the
	// key here only says which section is meant and where it goes.
	Title string `json:"title,omitempty"`
	// Types are the commit types the custom section claims, matched
	// case-insensitively against the type a commit was written with. A type
	// claimed here is grouped under this section instead of the bump-keyed
	// built-in, except when the unit is breaking: a breaking change is always
	// rendered under the breaking section, so `add(x)!:` cannot hide in
	// "Added".
	Types []string `json:"types,omitempty"`
	// Bump is the version bump the claimed types carry: "none", "patch",
	// "minor" or "major". It merges into the parser's own type table, which is
	// what makes a type dispat has never heard of releasable by declaring the
	// section that renders it. Optional, and only meaningful on a custom
	// section; a type declared both here and under `parser.types` must agree.
	Bump string `json:"bump,omitempty"`
}

// CommitRefsConfig appends the commit behind an entry line to the line, so a
// reader can reach the change itself. Off by default, which is what keeps an
// entry byte for byte what it was before refs existed.
type CommitRefsConfig struct {
	// Placement decides where the reference appears: "off" (default) or
	// "suffix", after the description and its attribution. "off" is a value
	// rather than an absence, so a package can switch off what its space
	// turned on.
	Placement string `json:"placement,omitempty"`
	// Format is the reference's text, interpolated like every other record
	// line and additionally carrying $DISPAT_COMMIT and $DISPAT_COMMIT_SHORT.
	// Default "$DISPAT_COMMIT_SHORT".
	Format string `json:"format,omitempty"`
	// Link turns the reference into a markdown link. Empty renders it plain;
	// "auto" derives the forge URL from the package's github owner and repo;
	// anything else is a URL template interpolated the same way Format is.
	Link string `json:"link,omitempty"`
}

// EntryFormatConfig customises how a release entry is rendered; shared by the
// changelog file and the GitHub release body. All fields are optional.
//
// ReleaseName, Header and Footer are interpolated: $VAR and ${VAR} are
// replaced with the releasing package's DISPAT_* variables and script outputs,
// falling back to the process environment, so one configured line can name the
// package and tag it belongs to.
type EntryFormatConfig struct {
	DateFormat        string `json:"dateFormat,omitempty"`        // Go time layout, default "2006-01-02"
	BreakingTitle     string `json:"breakingTitle,omitempty"`     // default "Breaking Changes"
	FeaturesTitle     string `json:"featuresTitle,omitempty"`     // default "Features"
	FixesTitle        string `json:"fixesTitle,omitempty"`        // default "Fixes"
	DependenciesTitle string `json:"dependenciesTitle,omitempty"` // default "Dependencies"
	// ReleaseName is what the release is called. On GitHub it replaces the
	// release name, which defaults to the tag; in a changelog it writes a
	// sub-header under the entry's date line, and nothing when empty.
	ReleaseName string `json:"releaseName,omitempty"`
	// Header and Footer are written inside every entry, above the sections
	// and after them.
	Header []EntryLine `json:"header,omitempty"`
	Footer []EntryLine `json:"footer,omitempty"`
	// Authors attributes the entry to the people behind it. Nil means the
	// layer says nothing about attribution, which is what lets a nearer layer
	// inherit a broader one field by field.
	Authors *AuthorsConfig `json:"authors,omitempty"`
	// DependencyLink turns each line of the dependencies section into a link
	// to the provider's release. Empty renders the plain line; "auto" derives
	// the forge URL from the package's github owner and repo; anything else is
	// a URL template interpolated against the release's variables plus
	// $DISPAT_DEP_NAME, $DISPAT_DEP_FROM, $DISPAT_DEP_TO and $DISPAT_DEP_TAG.
	//
	// "auto" degrades to the plain line rather than to a broken one whenever
	// the owner and repo are unresolvable, or a github API URL outside
	// github.com is configured.
	DependencyLink string `json:"dependencyLink,omitempty"`
	// NoChangesText replaces the sentence an entry with no sections carries.
	// It is interpolated like every other record line, and an expansion that
	// comes out empty falls back to the built-in sentences: an entry must
	// never render empty. It must not begin with "---", which is where
	// `dispat self-update` cuts a release's notes.
	NoChangesText string `json:"noChangesText,omitempty"`
	// Sections states the whole order of the entry's sections, built-ins and
	// custom ones together. An empty list is the default order: breaking
	// changes, features, fixes, dependencies.
	Sections []SectionConfig `json:"sections,omitempty"`
	// CommitRefs appends the commit behind each entry line to the line. Nil
	// means the layer says nothing about references, which is what lets a
	// nearer layer inherit a broader one field by field.
	CommitRefs *CommitRefsConfig `json:"commitRefs,omitempty"`
}

// ChangelogConfig customises (or disables) the per-package changelog file.
type ChangelogConfig struct {
	Enabled *bool  `json:"enabled,omitempty"` // default true
	File    string `json:"file,omitempty"`    // default "CHANGELOG.md"
	// FileTitle heads the file, above every entry. An absent list means the
	// default "# Changelog"; [""] writes a blank first line instead. It takes
	// the same shapes as Header and Footer, so a file can open with several
	// lines and say something different per package — but unlike them it is
	// written once and matched against on the next release, so it must not
	// contain anything that varies from one release to the next.
	FileTitle []EntryLine `json:"fileTitle,omitempty"`
	// Channels restricts which releases get an entry: "stable", "*" for any
	// prerelease channel, or a channel name such as "beta", matched
	// case-insensitively. An empty list writes an entry for every release.
	//
	// ["stable"] keeps the file a record of stable releases alone: the betas
	// of a version leave nothing behind, and the graduation to stable writes
	// the one entry covering the whole window. A package under a space that
	// restricts the channels opts back in with ["stable", "*"], which the two
	// values together cover.
	Channels []string `json:"channels,omitempty"`
	// EntrySpacing is how many blank lines separate one entry from the entry
	// below it, 1 to 10. Nil is the default 2, which is what makes the seam
	// the same wherever the entry above it happened to end.
	EntrySpacing *int `json:"entrySpacing,omitempty"`
	EntryFormatConfig
}

// EntrySpacingOrDefault is the blank-line count between entries, with the
// default applied. Nil-safe.
func (c *ChangelogConfig) EntrySpacingOrDefault() int {
	if c == nil || c.EntrySpacing == nil {
		return DefaultEntrySpacing
	}
	return *c.EntrySpacing
}

// The bounds of changelog.entrySpacing. One blank line is the tightest seam
// that still separates two entries; ten is a generous ceiling that keeps a
// mistyped value from writing a screenful of nothing between every release.
const (
	DefaultEntrySpacing = 2
	MinEntrySpacing     = 1
	MaxEntrySpacing     = 10
)

// IsEnabled reports whether the changelog file is written (default true). It
// is nil-safe: an absent changelog object means all defaults.
func (c *ChangelogConfig) IsEnabled() bool { return c == nil || c.Enabled == nil || *c.Enabled }

// RecordChannels are the channels the changelog records on, empty meaning
// every release. Nil-safe.
func (c *ChangelogConfig) RecordChannels() []string {
	if c == nil {
		return nil
	}
	return c.Channels
}

// GitHubConfig customises (or disables) GitHub release creation.
type GitHubConfig struct {
	Enabled  *bool  `json:"enabled,omitempty"`  // default true
	Owner    string `json:"owner,omitempty"`    // default: derived from $GITHUB_REPOSITORY
	Repo     string `json:"repo,omitempty"`     // default: derived from $GITHUB_REPOSITORY
	APIURL   string `json:"apiUrl,omitempty"`   // default https://api.github.com
	TokenEnv string `json:"tokenEnv,omitempty"` // env var holding the token, default GITHUB_TOKEN
	// AllPackages creates a GitHub release for every published package, even
	// when no script exported DISPAT_EXPORT_GITHUB (the export then only adds
	// assets). Default false: the export stays the per-package opt-in.
	AllPackages *bool `json:"allPackages,omitempty"`
	// Draft creates every release as a draft, so a human publishes it after
	// reading the rendered notes. Default false. A draft carries no tag ref
	// until it is published, so nothing that looks a release up by its tag
	// (dispat install, self-update, the alias-tag chain) sees it meanwhile.
	Draft *bool `json:"draft,omitempty"`
	// Channels restricts which releases get a GitHub release: "stable", "*"
	// for any prerelease channel, or a channel name such as "beta", matched
	// case-insensitively. An empty list creates a release for every release.
	//
	// ["stable"] keeps the repository's releases page a list of stable
	// releases alone, while the betas are still tagged and still published by
	// the flow. This chooses which releases are created; the prerelease flag
	// GitHub shows on a created release always follows the version itself.
	Channels []string `json:"channels,omitempty"`
	EntryFormatConfig
}

// IsEnabled reports whether GitHub releases are created (default true; still
// requires a resolvable repository and token at runtime). Nil-safe.
func (c *GitHubConfig) IsEnabled() bool { return c == nil || c.Enabled == nil || *c.Enabled }

// RecordChannels are the channels GitHub releases are created on, empty
// meaning every release. Nil-safe.
func (c *GitHubConfig) RecordChannels() []string {
	if c == nil {
		return nil
	}
	return c.Channels
}

// AllPackagesEnabled reports whether every published package gets a GitHub
// release regardless of the DISPAT_EXPORT_GITHUB export. Nil-safe.
func (c *GitHubConfig) AllPackagesEnabled() bool {
	return c != nil && c.AllPackages != nil && *c.AllPackages
}

// DraftEnabled reports whether releases are created as drafts, left for a
// human to publish. Nil-safe.
func (c *GitHubConfig) DraftEnabled() bool {
	return c != nil && c.Draft != nil && *c.Draft
}

// CommitConfig customises the finalize phase: a single release commit created
// at the end of a successful run, capturing changelog and version-script
// manifest changes of all published packages. Disabled by default. When
// enabled, tags are created on the release commit (after it) instead of
// during each publish, and GitHub releases move to the end of the run — after
// the push when push is enabled, so they reference commits and tags that
// exist on the remote.
type CommitConfig struct {
	Enabled *bool `json:"enabled,omitempty"` // default false
	// MessageFormat supports {tags} and {packages} placeholders (comma-
	// separated lists). Default: "chore(release): {tags}".
	MessageFormat string `json:"messageFormat,omitempty"`
	// Push pushes the release commit and tags.
	Push   bool   `json:"push,omitempty"`   // default false
	Remote string `json:"remote,omitempty"` // default "origin"
	// Force writes tags that the repository or the remote already carries,
	// instead of leaving them as they are. Default true.
	//
	// It exists because a tag the remote already has is otherwise skipped
	// forever, which is what a moving tag (see PackageConfig.AliasTags) can
	// never live with, and because a tag appearing between the check and the
	// push would otherwise reject the whole push at the very end of a release.
	// The branch is never force pushed under either setting, and a release tag
	// found sitting at a different commit is still left alone: force means
	// "do not fail because the ref exists", not "overwrite whatever is there".
	Force *bool `json:"force,omitempty"` // default true
	// Verify controls the upfront remote-access check (git ls-remote) run
	// before any release work when Push is enabled. Default true; set false
	// to skip it, e.g. for a remote that rejects ls-remote but accepts
	// pushes.
	Verify *bool `json:"verify,omitempty"` // default true
	// Include lists extra repo-relative paths the release commit stages on
	// top of the published packages' folders: the shared artifacts a version
	// stage or an autoVersion syncLock regenerates outside every package
	// folder, a workspace-level package-lock.json first among them. Paths
	// must stay inside the repository (no absolute paths, no "..") and may
	// name files that do not exist yet.
	Include []string `json:"include,omitempty"`
	// Name and Email, when set, are the git identity every commit and
	// annotated tag dispat creates is authored under, so a CI run needs no
	// `git config` step. Empty values fall back to git's own configuration.
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

// IsEnabled reports whether the release commit is created (default false).
// Nil-safe.
func (c *CommitConfig) IsEnabled() bool { return c != nil && c.Enabled != nil && *c.Enabled }

// PushEnabled reports whether the release commit and tags are pushed; only
// meaningful with the commit enabled. Nil-safe.
func (c *CommitConfig) PushEnabled() bool { return c.IsEnabled() && c.Push }

// ForceEnabled reports whether tags are written over ones that already exist
// (default true). Nil-safe.
func (c *CommitConfig) ForceEnabled() bool { return c == nil || c.Force == nil || *c.Force }

// VerifyEnabled reports whether remote access is verified before any release
// work when pushing (default true). Nil-safe.
func (c *CommitConfig) VerifyEnabled() bool { return c == nil || c.Verify == nil || *c.Verify }

// Versioning values of a space (the `versioning` key).
//
// The shared modes differ along two axes. The first is how much of the
// version the group holds in common: the whole of it under fixed, the major
// and minor under fixedMajorMinor, the major alone under fixedMajor.
// Components below that are each package's own, so under fixedMajor a fix in
// one member moves nothing else. The second axis is what happens to a member
// with no changes of its own when the shared part does move: the plain modes
// release it too, at the shared version, with a single "no changes" changelog
// entry explaining the bump; the *Sparse modes leave it at its previous
// version until it next changes, at which point it joins the shared part.
//
// VersioningNone sits outside both axes: its packages are excluded from
// releasing entirely and exist to run scripts.
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
	// VersioningFixedMajorMinor shares the major and minor: a minor or major
	// release moves every package of the group to the same next version, while
	// patch releases stay each package's own.
	VersioningFixedMajorMinor = "fixedMajorMinor"
	// VersioningFixedMajorMinorSparse shares the major and minor exactly like
	// fixedMajorMinor, but a package with no changes of its own keeps its
	// previous version instead of riding along.
	VersioningFixedMajorMinorSparse = "fixedMajorMinorSparse"
	// VersioningFixedMajor shares the major alone: a major release moves every
	// package of the group to the same next version, while minor and patch
	// releases stay each package's own.
	VersioningFixedMajor = "fixedMajor"
	// VersioningFixedMajorSparse shares the major exactly like fixedMajor, but
	// a package with no changes of its own keeps its previous version instead
	// of riding along.
	VersioningFixedMajorSparse = "fixedMajorSparse"
	// VersioningNone keeps the space's packages outside the release flow
	// entirely: they are never versioned, tagged, changelogged or published.
	// They exist to run scripts, and may depend on releasable packages, while
	// a releasable package must not depend on them.
	VersioningNone = "none"
)

// VersionGroupConfig declares one entry of the top-level `versionGroups`
// map: a shared-versioning group whose membership is stated by the members
// themselves, through their versionGroup key. The declaration owns the
// group's versioning mode — any of the shared modes above; "independent" is
// invalid, because a group exists to share — so every member moves under one
// rule and a member cannot contradict it.
type VersionGroupConfig struct {
	Versioning string `json:"versioning,omitempty"`
}

// PathList is a space's `path` key: one folder or a list of folders, each
// relative to the repository root and each holding packages of the space.
// The first entry is the space's primary folder — the login script runs
// there, and `dispat exec --in <space>` resolves there. It marshals a single
// folder back to the scalar form, so a config written from the model reads
// the way most authors write it.
type PathList []string

// First returns the primary folder: the first configured path, or "" for an
// empty list.
func (p PathList) First() string {
	if len(p) == 0 {
		return ""
	}
	return p[0]
}

// MarshalJSON renders one folder as a bare string and several as an array,
// mirroring the two shapes UnmarshalJSON accepts.
func (p PathList) MarshalJSON() ([]byte, error) {
	if len(p) == 1 {
		return json.Marshal(p[0])
	}
	return json.Marshal([]string(p))
}

// UnmarshalJSON accepts the scalar and the array form.
func (p *PathList) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*p = PathList{s}
		return nil
	}
	var list []string
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}
	*p = PathList(list)
	return nil
}

// SpaceConfig is the raw configuration of one space. Everything the space
// runs — stages, hooks, outcome scripts — lives in its `flow` object.
type SpaceConfig struct {
	Path PathList `json:"path,omitempty"`
	// The scalar booleans are pointers for the same reason SpaceFile's and
	// PackageConfig's are: the root file now states defaults for them, and a
	// space that cannot say "false" could not override a root "true".
	IsBuildWaitingPublish *bool            `json:"isBuildWaitingPublish,omitempty"`
	RevertOnFail          *bool            `json:"revertOnFail,omitempty"`
	Flow                  *SpaceFlowConfig `json:"flow,omitempty"`
	// TagFormat overrides the repository-wide tagFormat for this space.
	TagFormat string `json:"tagFormat,omitempty"`
	// AliasTags replaces the inherited alias list for this level; see
	// AliasTagConfig. An empty list declared here means "no aliases",
	// which is how a package opts out of its space's.
	AliasTags []AliasTagConfig `json:"aliasTags,omitempty"`
	// Webhooks replaces the inherited webhook list for this level's packages;
	// see WebhookConfig. A stated list replaces the whole inherited one, so an
	// empty list declared here means "no webhooks", which is how a level opts
	// out. The run-bracket events (release.started, release.finished) always
	// deliver to the top-level list alone: they describe the run, which no one
	// package speaks for.
	Webhooks []WebhookConfig `json:"webhooks,omitempty"`
	// Versioning selects how versions relate across the space's packages:
	// "independent" (default) or one of the shared modes. See the Versioning*
	// constants.
	Versioning string `json:"versioning,omitempty"`
	// VersionGroup names the shared-versioning group the space's packages
	// join: an entry of the top-level versionGroups map, or the name of
	// another space whose own versioning is shared. Empty means the
	// space's own implicit group (its name) when its versioning is shared.
	// A declared group's versioning mode is authoritative, so a space naming
	// one must not set versioning itself.
	VersionGroup string `json:"versionGroup,omitempty"`
	// Scripts are the space's named shell commands, the same shape as the
	// file's own `scripts` and layered over it name by name: the space's
	// packages resolve a name here before falling back to the top level.
	// `flow` entries name them, and `dispat run <name>` executes the one it
	// resolves inside each changed package of the space, in topological order,
	// with the package's full DISPAT_* environment.
	Scripts map[string]Script `json:"scripts,omitempty"`
	// AutoVersion enables native manifest rewriting at the version stage:
	// dispat itself updates the declared ranges of workspace dependencies
	// (and the package's own version field) in package.json and go.mod,
	// before any flow.version script runs. nil means off. See
	// AutoVersionConfig.
	AutoVersion *AutoVersionConfig `json:"autoVersion,omitempty"`
	// Env is static environment for every script of the space's packages —
	// its stages, hooks, run scripts and its login script — merged over the
	// top-level map key by key; see File.Env.
	Env map[string]string `json:"env,omitempty"`
	// Custom is an optional free-form object dispat itself never reads; see
	// File.Custom.
	Custom map[string]any `json:"custom,omitempty"`
	// Changelog and GitHub overlay the top-level objects field by field for
	// this space's packages, and a package's own overlay sits on top of the
	// result; see File.Changelog.
	Changelog *ChangelogConfig `json:"changelog,omitempty"`
	GitHub    *GitHubConfig    `json:"github,omitempty"`
	// Src is the scope folder for this space's packages, resolved against
	// each package's own folder; see PackageConfig.Src. It is the usual place
	// for it, since a layout tends to be a property of a space.
	Src string `json:"src,omitempty"`
	// Concurrency is the stage-budget weight this space's packages occupy,
	// the same meaning as PackageConfig.Concurrency and deliberately not the
	// top-level key's, which is the budget itself.
	Concurrency []int `json:"concurrency,omitempty"`
	// Ignore are change-scope ignore patterns for this space's packages,
	// matched against paths relative to the space folder; see
	// PackageConfig.Ignore.
	Ignore []string `json:"ignore,omitempty"`
	// Dependencies declares consumer -> provider edges next to the space they
	// describe, in the same object-keyed-by-consumer shape as File.Dependencies
	// — a space is not a package, so there is no consumer to leave implicit.
	//
	// Every edge declared here must touch the space: its consumer or its
	// provider is one of the space's own packages. An edge between two other
	// spaces' packages says nothing about this one, and a config where it
	// could be written anywhere is a config where nobody knows where to look.
	// A cross-space edge — one endpoint here, the other elsewhere — is exactly
	// what this level is for, and belongs to whichever of the two spaces the
	// author thinks of as owning it.
	//
	// Like every other declaration, these merge into one list rather than
	// overriding anything.
	Dependencies Dependencies `json:"dependencies,omitempty"`
	// Packages holds per-package configuration for this space's packages
	// alone, keyed by folder name — the same entry shape as the file's own
	// `packages` map, scoped to the space that owns the folders. Every key
	// must match exactly one folder of this space, and no entry may carry a
	// `path`: a space package's location is its folder, and a package living
	// outside every space is declared in the top-level map instead. An entry
	// here outranks the top-level entry for the same package, being the
	// nearer statement about it.
	Packages map[string]PackageConfig `json:"packages,omitempty"`
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
	IsBuildWaitingPublish *bool            `json:"isBuildWaitingPublish,omitempty"`
	RevertOnFail          *bool            `json:"revertOnFail,omitempty"`
	Flow                  *SpaceFlowConfig `json:"flow,omitempty"`
	TagFormat             string           `json:"tagFormat,omitempty"`
	// AliasTags replaces the inherited alias list for this level; see
	// AliasTagConfig. An empty list declared here means "no aliases",
	// which is how a package opts out of its space's.
	AliasTags    []AliasTagConfig   `json:"aliasTags,omitempty"`
	Webhooks     []WebhookConfig    `json:"webhooks,omitempty"`
	Versioning   string             `json:"versioning,omitempty"`
	VersionGroup string             `json:"versionGroup,omitempty"`
	Scripts      map[string]Script  `json:"scripts,omitempty"`
	AutoVersion  *AutoVersionConfig `json:"autoVersion,omitempty"`
	Env          map[string]string  `json:"env,omitempty"`
	Custom       map[string]any     `json:"custom,omitempty"`
	// Changelog, GitHub, Src and Concurrency are this space's; see
	// SpaceConfig.
	Changelog   *ChangelogConfig `json:"changelog,omitempty"`
	GitHub      *GitHubConfig    `json:"github,omitempty"`
	Src         string           `json:"src,omitempty"`
	Concurrency []int            `json:"concurrency,omitempty"`
	Ignore      []string         `json:"ignore,omitempty"`
	// Dependencies are this space's own edges; see SpaceConfig.Dependencies.
	// They add to what the root file's space entry declares rather than
	// replacing it, because dependency declarations never override.
	Dependencies Dependencies             `json:"dependencies,omitempty"`
	Packages     map[string]PackageConfig `json:"packages,omitempty"`
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
	Path string `json:"path,omitempty"`
	// Src narrows which of the package's files count as changes to it: a
	// folder-relative path, so only what sits under <packageFolder>/<src>
	// makes a scopeless commit address the package. Everything outside it —
	// docs, fixtures, a scratch folder — stops triggering releases, while
	// the package folder stays the package: scripts still run there, the
	// changelog is still written there, and the release commit still stages
	// all of it.
	//
	// It narrows file-derived scope resolution alone. A commit naming the
	// package by scope always addresses it, wherever its files sit, and
	// manifest discovery is deliberately untouched: a manifest usually sits
	// at the package root, outside src, and auto-versioning must still find
	// it.
	Src string `json:"src,omitempty"`
	// Ignore keeps some of the package's own files from counting as changes
	// to it: folder-relative patterns, matched against every changed file
	// that would otherwise make a scopeless commit address the package.
	//
	// Where `src` narrows the package to one folder, this excludes from
	// whatever is left, which is what a package needs when the files that do
	// not deserve a release — docs, fixtures, a scratch folder — sit beside
	// the ones that do. "*" matches any run of characters, separators
	// included; a pattern without a separator matches at any depth; a
	// trailing "/" covers a folder; a leading "!" re-includes.
	//
	// The levels concatenate rather than replace — the repository's patterns,
	// then the space's, then the package's — and the last pattern to match
	// decides, so a package can re-include what a broader level excluded.
	// A `.dispatignore` file in the folder says the same thing, one pattern
	// per line, and is read after this list.
	//
	// Like `src` it narrows file-derived scope resolution alone: a commit
	// naming the package by scope still addresses it, the release commit
	// still stages the whole folder, and manifest discovery is untouched.
	Ignore                []string         `json:"ignore,omitempty"`
	IsBuildWaitingPublish *bool            `json:"isBuildWaitingPublish,omitempty"`
	RevertOnFail          *bool            `json:"revertOnFail,omitempty"`
	Flow                  *SpaceFlowConfig `json:"flow,omitempty"`
	TagFormat             string           `json:"tagFormat,omitempty"`
	// AliasTags replaces the inherited alias list for this level; see
	// AliasTagConfig. An empty list declared here means "no aliases",
	// which is how a package opts out of its space's.
	AliasTags []AliasTagConfig `json:"aliasTags,omitempty"`
	// Webhooks replaces the inherited webhook list for this level's packages;
	// see WebhookConfig. A stated list replaces the whole inherited one, so an
	// empty list declared here means "no webhooks", which is how a level opts
	// out. The run-bracket events (release.started, release.finished) always
	// deliver to the top-level list alone: they describe the run, which no one
	// package speaks for.
	Webhooks []WebhookConfig `json:"webhooks,omitempty"`
	// Versioning overrides how the package relates to its space's shared
	// version — most usefully "independent", opting one package out of a
	// fixed space. Mutually exclusive with naming a declared versionGroup,
	// whose mode is authoritative.
	Versioning string `json:"versioning,omitempty"`
	// VersionGroup names the shared-versioning group this package joins; see
	// SpaceConfig.VersionGroup.
	VersionGroup string `json:"versionGroup,omitempty"`
	// Scripts are merged into the space's map name by name; a name set here
	// wins over the space's, which wins over the file's. A name only this
	// package defines is the package's alone: `dispat run <name>` reaches no
	// other package with it.
	Scripts     map[string]Script  `json:"scripts,omitempty"`
	AutoVersion *AutoVersionConfig `json:"autoVersion,omitempty"`
	// ManifestNames are the manifest names this package is known by, stated
	// here rather than read from its files. They exist for the packages whose
	// manifests declare no name the workspace can learn — a Gradle module, a
	// bare Makefile project, a folder in an ecosystem dispat cannot parse —
	// so `dispat compute` still derives the edges pointing at it and
	// auto-versioning still reconciles the declarations naming it.
	//
	// A stated name outranks one a manifest declares, and no two packages may
	// state the same name.
	ManifestNames []string `json:"manifestNames,omitempty"`
	// Changelog and GitHub overlay the top-level objects field by field for
	// this package's release records — flip enabled, rename the file, target
	// another repository — leaving unset fields at the global values.
	Changelog *ChangelogConfig `json:"changelog,omitempty"`
	GitHub    *GitHubConfig    `json:"github,omitempty"`
	// Concurrency is the number of stage-budget slots the package's tasks
	// occupy: a single value for both stages or a [build, publish] pair.
	// Absent or 0 means 1, the ordinary cost; a package whose value reaches
	// the stage's budget runs that stage alone. (Deliberately unlike the
	// top-level key, where 0 means "number of CPUs" — a weight has no CPU
	// reading.)
	Concurrency []int `json:"concurrency,omitempty"`
	// Dependencies names the provider packages this package depends on: one
	// name, or an array of names and objects, exactly as a consumer lists
	// them in the top-level object. The consumer is the package itself.
	// Entry-layer and in-folder-layer lists both count: all declarations
	// merge with the top-level object.
	Dependencies ProviderList `json:"dependencies,omitempty"`
	// Env is static environment for this package's scripts, merged key by key
	// over the space's map (and the in-folder layer over the entry layer);
	// see File.Env.
	Env map[string]string `json:"env,omitempty"`
	// Custom is an optional free-form object dispat itself never reads; see
	// File.Custom. Like every other field it belongs to its layer: an entry's
	// object and an in-folder file's object are independent, not merged.
	Custom map[string]any `json:"custom,omitempty"`
}

// AliasTagConfig is one extra tag a release is written under, beside the tag
// its TagFormat produces.
//
// It exists for the refs that name a *line* rather than a release: a GitHub
// Action consumed as `uses: owner/repo@v1` needs a bare `v1.4.2` and a `v1`
// that follows the newest 1.x, neither of which can be the package's real tag
// in a monorepo whose tags carry path prefixes.
//
// An alias is **write-only**. Nothing reads one back, so it never becomes a
// package's baseline; the config is refused if a package's aliases could be
// mistaken for any package's release tags.
type AliasTagConfig struct {
	// Format is the template. It takes everything tagFormat takes plus
	// {major}, {minor} and {patch}, and needs at least one of those or
	// {version}: "v{version}", "v{major}", "{name}-{major}.{minor}".
	Format string `json:"format"`
	// Moving says the alias is re-pointed on every release it applies to,
	// rather than written once and left. "v1" means "the newest 1.x", which is
	// only true if each 1.x release moves it. A moving alias must be allowed
	// to force (see Force).
	Moving bool `json:"moving,omitempty"`
	// Channels restricts the alias to releases on these channels. Empty means
	// every channel. A moving major alias almost always wants ["stable"]: a
	// "v1" that follows release candidates is not what anyone pinning it
	// expects.
	Channels []string `json:"channels,omitempty"`
	// Force overrides commit.force for this alias alone. Defaults to the
	// run's setting; false on a moving alias is refused, since an alias that
	// cannot overwrite its own previous ref cannot move.
	Force *bool `json:"force,omitempty"`
}

// ForceEnabled reports whether this alias overwrites an existing ref, given
// the run's default.
func (a AliasTagConfig) ForceEnabled(runDefault bool) bool {
	if a.Force != nil {
		return *a.Force
	}
	return runDefault
}

// AppliesTo reports whether the alias is written for a release on channel.
func (a AliasTagConfig) AppliesTo(channel string) bool {
	if len(a.Channels) == 0 {
		return true
	}
	for _, c := range a.Channels {
		if strings.EqualFold(c, channel) {
			return true
		}
	}
	return false
}

// AutoVersionConfig is a space's `autoVersion` object: the native
// manifest-rewriting policy of the version stage (§9.4, §12.4). The presence
// of the object enables the feature unless `enabled: false` says otherwise.
type AutoVersionConfig struct {
	// Enabled turns the block off without deleting it. Default true when the
	// block sets any key at all. A completely empty {} block is treated as
	// absent (the config loader's flattening prunes empty objects), so the
	// minimal opt-in is {"enabled": true}.
	Enabled *bool `json:"enabled,omitempty"`
	// Manifests selects which manifests of a package are parsed and
	// rewritten: "root" (default) — only manifests directly in the package
	// folder — "all", every manifest found under it, or "none", which turns
	// the parsing strategy off entirely and leaves the work to `replace` and
	// `syncLock`.
	Manifests string `json:"manifests,omitempty"`
	// Replace are the literal text substitutions applied to the package's
	// files after (or instead of) the manifest rewriting: the strategy for
	// the versions no manifest writer can reach. Empty means off.
	Replace []AutoVersionReplaceConfig `json:"replace,omitempty"`
	// Kinds restricts rewriting to the named manifest fields
	// ("dependencies", "devDependencies", "peerDependencies",
	// "optionalDependencies"). Empty means all four.
	Kinds []string `json:"kinds,omitempty"`
	// Only restricts rewriting to declarations of the named provider
	// packages. Empty means every workspace provider.
	Only []string `json:"only,omitempty"`
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
	NameMatch string `json:"nameMatch,omitempty"`
	// Match restricts rewriting to declared ranges matching one of the
	// globs, e.g. ["workspace:*"] — so a range the user pinned by hand is
	// never overridden. Empty means any declared range is rewritten.
	Match []string `json:"match,omitempty"`
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
	Range string `json:"range,omitempty"`
	// WriteVersion also writes the package's own new version into its
	// manifest's version field (§12.4; a drifted manifest version is W192).
	// Default true.
	WriteVersion *bool `json:"writeVersion,omitempty"`
	// SyncLock names top-level scripts run inside the package folder after
	// its manifests were rewritten (e.g. "npm install" to sync the lock
	// file), between the version and build stages.
	SyncLock []string `json:"syncLock,omitempty"`
	// SyncLockConcurrency caps how many syncLock scripts run at the same
	// moment across the whole run — shared lock files corrupt under
	// parallel writers, so the default is 1. When spaces disagree, the
	// smallest configured value wins.
	SyncLockConcurrency int `json:"syncLockConcurrency,omitempty"`
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
	Files []string `json:"files,omitempty"`
	// Find is the literal text to look for, after the placeholders in it are
	// filled in. Required.
	Find string `json:"find,omitempty"`
	// Write is the literal text to put in its place, after the placeholders
	// in it are filled in. Required.
	Write string `json:"write,omitempty"`
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
	Build   []string `json:"build,omitempty"`
	Publish []string `json:"publish,omitempty"`
	Version []string `json:"version,omitempty"`
	// Login runs once per space before its first publish; every other
	// publish of the space waits for it, and its failure fails them all.
	Login []string `json:"login,omitempty"`
	// Announce is a fourth stage after a successful publish: pushing the
	// release out to update channels, with the release-notes variables in its
	// environment. The whole frame — hooks included — only warns on failure.
	Announce []string `json:"announce,omitempty"`
	// Hooks around the package stages. The before*/post* hooks up to
	// beforePublish fail the package's release when they fail; postPublish and
	// the announce hooks only warn, because by then the release is out.
	BeforeAll      []string `json:"beforeAll,omitempty"`
	BeforeVersion  []string `json:"beforeVersion,omitempty"`
	PostVersion    []string `json:"postVersion,omitempty"`
	BeforeBuild    []string `json:"beforeBuild,omitempty"`
	PostBuild      []string `json:"postBuild,omitempty"`
	BeforePublish  []string `json:"beforePublish,omitempty"`
	PostPublish    []string `json:"postPublish,omitempty"`
	BeforeAnnounce []string `json:"beforeAnnounce,omitempty"`
	PostAnnounce   []string `json:"postAnnounce,omitempty"`
	// Outcome scripts, both warn-only: onFail runs when a package of the
	// space fails at any stage, onSkip when it is skipped because a provider
	// failed.
	OnFail []string `json:"onFail,omitempty"`
	OnSkip []string `json:"onSkip,omitempty"`
}

// DependencyConfig is one consumer -> provider relation: the decoded form of
// the `dependencies` key, and the element type of Dependencies.
//
// The yaml tags exist because the CLI's compute command re-encodes this one
// struct when editing a YAML config in place; without them the encoder would
// write lowercased field names with `kind: ""` / `keep: false` noise on every
// edge.
type DependencyConfig struct {
	// Consumer is filled in from the key the entry sits under, never from the
	// entry itself: a `consumer` key inside a provider object is refused.
	Consumer string `json:"consumer,omitempty" yaml:"consumer"`
	Provider string `json:"provider,omitempty" yaml:"provider"`
	// Kind is the manifest dependency field the edge stands for:
	// "dependencies" (the default when empty), "devDependencies",
	// "peerDependencies" or "optionalDependencies". Propagation follows or
	// ignores the edge according to parser.propagation.kinds, whose default
	// is every kind except devDependencies.
	Kind string `json:"kind,omitempty" yaml:"kind,omitempty"`
	// Keep marks an edge `dispat compute` must never suggest removing: the
	// declaration is deliberate even though no manifest declares it (a Docker
	// image chain, a codegen coupling). Purely a compute-command annotation —
	// the planner treats kept edges like any other.
	Keep bool `json:"keep,omitempty" yaml:"keep,omitempty"`
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

// Int returns a pointer to n — the same helper for the tri-state *int option
// fields (Changelog.EntrySpacing), whose nil means "use the default".
func Int(n int) *int { return &n }

// UpdateCheckEnabled reports whether dispat looks for a newer release of
// itself (default true). Nil-safe, so a configuration that never mentions it
// gets the default.
func (c *File) UpdateCheckEnabled() bool {
	return c == nil || c.UpdateCheck == nil || *c.UpdateCheck
}

// FoldLookup finds what a config map holds under a name, whatever case either
// side spells it with. It answers with the map's own key as well as the value,
// for the callers that have to record which entry they read.
//
// The exact key is tried first, which is both the common case and the cheap
// one; only a name spelled differently pays for the scan. Two keys of one map
// that fold together would make the scan's answer a matter of luck, so loading
// refuses them, and every map reaching here holds at most one.
func FoldLookup[T any](m map[string]T, name string) (string, T, bool) {
	if value, ok := m[name]; ok {
		return name, value, true
	}
	for key, value := range m {
		if strings.EqualFold(key, name) {
			return key, value, true
		}
	}
	var zero T
	return "", zero, false
}

// Script resolves a script reference case-insensitively, because a script is
// named in a config file and again in a command line, and the two spellings
// are not asked to agree.
func (c *File) Script(ref string) (Script, bool) {
	_, s, ok := FoldLookup(c.Scripts, ref)
	return s, ok
}

// Script resolves one of the space's own scripts case-insensitively, for the
// same reason as File.Script. It looks no further than this level: the
// fallback to the file's scripts belongs to the layer that knows the package.
func (s SpaceConfig) Script(name string) (Script, bool) {
	_, cmd, ok := FoldLookup(s.Scripts, name)
	return cmd, ok
}

// Package resolves a `packages` entry by package name case-insensitively,
// for the same reason as Script.
func (c *File) Package(name string) (PackageConfig, bool) {
	_, pc, ok := FoldLookup(c.Packages, name)
	return pc, ok
}

// PackageEntry is Package with the key it matched, for the callers that record
// what a configuration was read under: an entry written `MyLib` is consumed,
// reported and edited under `MyLib`, whatever the name reaching here looked
// like.
func (c *File) PackageEntry(name string) (string, PackageConfig, bool) {
	return FoldLookup(c.Packages, name)
}

// Space resolves a `spaces` entry by space name case-insensitively, for the
// same reason as Package.
func (c *File) Space(name string) (SpaceConfig, bool) {
	_, sc, ok := FoldLookup(c.Spaces, name)
	return sc, ok
}

// SpaceEntry is Space with the key it matched, for the same reason as
// PackageEntry.
func (c *File) SpaceEntry(name string) (string, SpaceConfig, bool) {
	return FoldLookup(c.Spaces, name)
}

// Package resolves one of the space file's `packages` entries by package
// name, case-insensitively for the same reason as File.Package.
func (f SpaceFile) Package(name string) (PackageConfig, bool) {
	_, pc, ok := FoldLookup(f.Packages, name)
	return pc, ok
}

// PackageEntry is Package with the key it matched, for the same reason as
// File.PackageEntry.
func (f SpaceFile) PackageEntry(name string) (string, PackageConfig, bool) {
	return FoldLookup(f.Packages, name)
}

// Package resolves one of the space's own `packages` entries by package name,
// case-insensitively for the same reason as File.Package. It looks no
// further than this level: the file's entry is a separate layer, applied
// before this one.
func (s SpaceConfig) Package(name string) (PackageConfig, bool) {
	_, pc, ok := FoldLookup(s.Packages, name)
	return pc, ok
}

// PackageEntry is Package with the key it matched, for the same reason as
// File.PackageEntry.
func (s SpaceConfig) PackageEntry(name string) (string, PackageConfig, bool) {
	return FoldLookup(s.Packages, name)
}

// Commands resolves a sequence of script references into the shell commands
// they name, preserving order. Unknown references were rejected by validation,
// so resolution cannot silently drop one.
//
// A reference contributes every command its script binds, so a name bound to
// two commands lengthens the sequence by two: the two levels of ordering — the
// references, and the commands inside each — flatten into the one order they
// run in.
func (c *File) Commands(refs []string) []string {
	if len(refs) == 0 {
		return nil
	}
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if s, ok := c.Script(ref); ok {
			out = append(out, s...)
		}
	}
	return out
}
