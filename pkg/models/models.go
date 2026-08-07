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
// script, space and run-script names are matched case-insensitively.
package models

import (
	"strings"

	"github.com/yohimik/dispat/pkg/ccme"
)

// File mirrors the configuration at the monorepo root. Viper infers the
// format from the file extension (yaml, json, toml, ...).
type File struct {
	Scripts      map[string]string      `mapstructure:"scripts" json:"scripts,omitempty"`
	Spaces       map[string]SpaceConfig `mapstructure:"spaces" json:"spaces,omitempty"`
	Dependencies []DependencyConfig     `mapstructure:"dependencies" json:"dependencies,omitempty"`
	// Concurrency accepts a single value applied to both stages
	// (concurrency: 4) or a [build, publish] pair (concurrency: [4, 2]).
	// 0 entries mean "number of CPUs".
	Concurrency []int `mapstructure:"concurrency" json:"concurrency,omitempty"`
	// LogLevel is the minimum level: trace, debug, info, warn or error.
	LogLevel string `mapstructure:"logLevel" json:"logLevel,omitempty"`
	// LogFormat selects the logger output: "pretty" (human console output)
	// or "json" (machine-readable lines for CI ingestion).
	LogFormat string          `mapstructure:"logFormat" json:"logFormat,omitempty"`
	Changelog ChangelogConfig `mapstructure:"changelog" json:"changelog,omitempty"`
	GitHub    GitHubConfig    `mapstructure:"github" json:"github,omitempty"`
	Commit    CommitConfig    `mapstructure:"commit" json:"commit,omitempty"`
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
	Run RunConfig `mapstructure:"run" json:"run,omitempty"`

	// Parser holds the commit-message parser options; see ParserConfig. Every
	// field is optional and defaults to the specification value.
	Parser ParserConfig `mapstructure:"parser" json:"parser,omitempty"`

	// Resolved values, populated by validation.
	BuildConcurrency   int                     `mapstructure:"-" json:"-"`
	PublishConcurrency int                     `mapstructure:"-" json:"-"`
	InitialVersions    map[string]ccme.Version `mapstructure:"-" json:"-"`
	ParserConfig       ccme.Config             `mapstructure:"-" json:"-"`
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
	// carry no directive of their own.
	Propagation ParserPropagationConfig `mapstructure:"propagation" json:"propagation,omitempty"`
	// Limits are the always-enforced parser bounds; exceeding one voids the
	// whole message. Defaults: 64 units, 256 scope terms, 1 MiB.
	Limits ParserLimitsConfig `mapstructure:"limits" json:"limits,omitempty"`
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
// names, exactly like the space stages — the two objects share one shape, and
// `run` is deliberately not called `scripts`: `scripts` defines named
// commands, `run` says what runs when.
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

// IsEnabled reports whether the changelog file is written (default true).
func (c ChangelogConfig) IsEnabled() bool { return c.Enabled == nil || *c.Enabled }

// GitHubConfig customises (or disables) GitHub release creation.
type GitHubConfig struct {
	Enabled           *bool  `mapstructure:"enabled" json:"enabled,omitempty"`   // default true
	Owner             string `mapstructure:"owner" json:"owner,omitempty"`       // default: derived from $GITHUB_REPOSITORY
	Repo              string `mapstructure:"repo" json:"repo,omitempty"`         // default: derived from $GITHUB_REPOSITORY
	APIURL            string `mapstructure:"apiUrl" json:"apiUrl,omitempty"`     // default https://api.github.com
	TokenEnv          string `mapstructure:"tokenEnv" json:"tokenEnv,omitempty"` // env var holding the token, default GITHUB_TOKEN
	EntryFormatConfig `mapstructure:",squash"`
}

// IsEnabled reports whether GitHub releases are created (default true; still
// requires a resolvable repository and token at runtime).
func (c GitHubConfig) IsEnabled() bool { return c.Enabled == nil || *c.Enabled }

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
	// Push pushes the release commit and tags (git push --follow-tags).
	// Remote access is verified before any release work starts.
	Push   bool   `mapstructure:"push" json:"push,omitempty"`     // default false
	Remote string `mapstructure:"remote" json:"remote,omitempty"` // default "origin"
}

// IsEnabled reports whether the release commit is created (default false).
func (c CommitConfig) IsEnabled() bool { return c.Enabled != nil && *c.Enabled }

// PushEnabled reports whether the release commit and tags are pushed; only
// meaningful with the commit enabled.
func (c CommitConfig) PushEnabled() bool { return c.IsEnabled() && c.Push }

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

// SpaceConfig is the raw configuration of one space. Everything the space
// runs — stages, hooks, outcome scripts — lives in its `run` object.
type SpaceConfig struct {
	Path                  string         `mapstructure:"path" json:"path,omitempty"`
	IsBuildWaitingPublish bool           `mapstructure:"isBuildWaitingPublish" json:"isBuildWaitingPublish,omitempty"`
	RevertOnFail          bool           `mapstructure:"revertOnFail" json:"revertOnFail,omitempty"`
	Run                   SpaceRunConfig `mapstructure:"run" json:"run,omitempty"`
	// TagFormat overrides the repository-wide tagFormat for this space.
	TagFormat string `mapstructure:"tagFormat" json:"tagFormat,omitempty"`
	// Versioning selects how versions relate across the space's packages:
	// "independent" (default), "fixed" or "fixedSparse". See the Versioning*
	// constants.
	Versioning string `mapstructure:"versioning" json:"versioning,omitempty"`
	// RunScripts are the space's named `dispat run <name>` scripts. Unlike
	// the stage entries, values are shell commands themselves, not references
	// into `scripts`. `dispat run <name>` executes the script inside each
	// changed package of the space, in topological order, with the package's
	// full DISPAT_* environment; spaces without the name are skipped.
	RunScripts map[string]string `mapstructure:"runScripts" json:"runScripts,omitempty"`
}

// SpaceRunConfig is a space's `run` object: what runs at which stage, keyed
// by stage or hook name with no decoration. All entries are optional — a
// stage with no script still runs, an unset hook is a no-op — and every one
// accepts a single script name or an array of names run in order (weak
// decoding lifts the scalar into a one-element slice, the same way a scalar
// concurrency becomes a pair).
type SpaceRunConfig struct {
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
type DependencyConfig struct {
	Consumer string `mapstructure:"consumer" json:"consumer,omitempty"`
	Provider string `mapstructure:"provider" json:"provider,omitempty"`
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

// Script resolves a script reference case-insensitively, because viper
// lowercases the keys of the scripts map.
func (c *File) Script(ref string) (string, bool) {
	s, ok := c.Scripts[strings.ToLower(ref)]
	return s, ok
}

// RunScript resolves a space's run script case-insensitively, for the same
// viper reason as Script.
func (s SpaceConfig) RunScript(name string) (string, bool) {
	cmd, ok := s.RunScripts[strings.ToLower(name)]
	return cmd, ok
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
