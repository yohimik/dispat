// Package config loads and validates the monorepo configuration file (via
// viper) and discovers the packages living inside the configured spaces.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/cli/internal/gitx"
	"github.com/yohimik/dispat/services/cli/internal/model"
)

// File mirrors the configuration at the monorepo root. Viper infers the
// format from the file extension (yaml, json, toml, ...); note that viper
// treats keys case-insensitively and lowercases map keys, so script and space
// names are matched case-insensitively.
type File struct {
	Scripts      map[string]string      `mapstructure:"scripts"`
	Spaces       map[string]SpaceConfig `mapstructure:"spaces"`
	Dependencies []DependencyConfig     `mapstructure:"dependencies"`
	// Concurrency accepts a single value applied to both stages
	// (concurrency: 4) or a [build, publish] pair (concurrency: [4, 2]).
	// 0 entries mean "number of CPUs".
	Concurrency []int `mapstructure:"concurrency"`
	// LogLevel is the minimum level: trace, debug, info, warn or error.
	LogLevel string `mapstructure:"logLevel"`
	// LogFormat selects the logger output: "pretty" (human console output)
	// or "json" (machine-readable lines for CI ingestion).
	LogFormat string          `mapstructure:"logFormat"`
	Changelog ChangelogConfig `mapstructure:"changelog"`
	GitHub    GitHubConfig    `mapstructure:"github"`
	Commit    CommitConfig    `mapstructure:"commit"`
	// Shell is the command prefix scripts are appended to, e.g.
	// ["bash", "-c"] or ["cmd", "/C"]. Default: ["/bin/sh", "-c"].
	Shell []string `mapstructure:"shell"`
	// Initials maps package names to the baseline version used when the
	// package's latest release tag is missing or unparseable (e.g. a stray
	// "pkg@0.0.1.0" tag). The next release bumps on top of this value.
	// Keys are matched case-insensitively against discovered packages.
	Initials map[string]string `mapstructure:"initials"`
	// TagFormat is the repository-wide release tag template, overridable per
	// space. Placeholders are {name} and {version}; every other byte is
	// literal, so "{name}@v{version}" and "services/{name}@v{version}" both
	// work. Default: "{name}@{version}", the form §14 makes normative.
	TagFormat string `mapstructure:"tagFormat"`
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
	CommitErrors string `mapstructure:"commitErrors"`
	// NonPackageScopes are scope names that are deliberately not packages, so
	// naming one is not the typo E130 exists to catch. Default: ["release"],
	// which is the scope of dispat's own release commit — without the
	// exemption every run would poison the next one.
	NonPackageScopes []string `mapstructure:"nonPackageScopes"`

	// Run is the run-level hooks object; see RunConfig.
	Run RunConfig `mapstructure:"run"`

	// Resolved values, populated by validation.
	BuildConcurrency   int                     `mapstructure:"-"`
	PublishConcurrency int                     `mapstructure:"-"`
	InitialVersions    map[string]ccme.Version `mapstructure:"-"`
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
	BeforeAll    []string `mapstructure:"beforeAll"`
	PostAll      []string `mapstructure:"postAll"`
	BeforeCommit []string `mapstructure:"beforeCommit"`
	AfterCommit  []string `mapstructure:"afterCommit"`
	PostCommit   []string `mapstructure:"postCommit"`
	BeforePush   []string `mapstructure:"beforePush"`
	AfterPush    []string `mapstructure:"afterPush"`
}

// EntryFormatConfig customises how a release entry is rendered; shared by the
// changelog file and the GitHub release body. All fields are optional.
type EntryFormatConfig struct {
	DateFormat        string `mapstructure:"dateFormat"`        // Go time layout, default "2006-01-02"
	BreakingTitle     string `mapstructure:"breakingTitle"`     // default "Breaking Changes"
	FeaturesTitle     string `mapstructure:"featuresTitle"`     // default "Features"
	FixesTitle        string `mapstructure:"fixesTitle"`        // default "Fixes"
	DependenciesTitle string `mapstructure:"dependenciesTitle"` // default "Dependencies"
}

// ChangelogConfig customises (or disables) the per-package changelog file.
type ChangelogConfig struct {
	Enabled           *bool  `mapstructure:"enabled"` // default true
	File              string `mapstructure:"file"`    // default "CHANGELOG.md"
	Title             string `mapstructure:"title"`   // default "# Changelog"
	EntryFormatConfig `mapstructure:",squash"`
}

// IsEnabled reports whether the changelog file is written (default true).
func (c ChangelogConfig) IsEnabled() bool { return c.Enabled == nil || *c.Enabled }

// GitHubConfig customises (or disables) GitHub release creation.
type GitHubConfig struct {
	Enabled           *bool  `mapstructure:"enabled"`  // default true
	Owner             string `mapstructure:"owner"`    // default: derived from $GITHUB_REPOSITORY
	Repo              string `mapstructure:"repo"`     // default: derived from $GITHUB_REPOSITORY
	APIURL            string `mapstructure:"apiUrl"`   // default https://api.github.com
	TokenEnv          string `mapstructure:"tokenEnv"` // env var holding the token, default GITHUB_TOKEN
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
	Enabled *bool `mapstructure:"enabled"` // default false
	// MessageFormat supports {tags} and {packages} placeholders (comma-
	// separated lists). Default: "chore(release): {tags}".
	MessageFormat string `mapstructure:"messageFormat"`
	// Push pushes the release commit and tags (git push --follow-tags).
	// Remote access is verified before any release work starts.
	Push   bool   `mapstructure:"push"`   // default false
	Remote string `mapstructure:"remote"` // default "origin"
}

// IsEnabled reports whether the release commit is created (default false).
func (c CommitConfig) IsEnabled() bool { return c.Enabled != nil && *c.Enabled }

// PushEnabled reports whether the release commit and tags are pushed; only
// meaningful with the commit enabled.
func (c CommitConfig) PushEnabled() bool { return c.IsEnabled() && c.Push }

// SpaceConfig is the raw configuration of one space. Everything the space
// runs — stages, hooks, outcome scripts — lives in its `run` object.
type SpaceConfig struct {
	Path                  string         `mapstructure:"path"`
	IsBuildWaitingPublish bool           `mapstructure:"isBuildWaitingPublish"`
	RevertOnFail          bool           `mapstructure:"revertOnFail"`
	Run                   SpaceRunConfig `mapstructure:"run"`
	// TagFormat overrides the repository-wide tagFormat for this space.
	TagFormat string `mapstructure:"tagFormat"`
}

// SpaceRunConfig is a space's `run` object: what runs at which stage, keyed
// by stage or hook name with no decoration. All entries are optional — a
// stage with no script still runs, an unset hook is a no-op — and every one
// accepts a single script name or an array of names run in order (weak
// decoding lifts the scalar into a one-element slice, the same way a scalar
// concurrency becomes a pair).
type SpaceRunConfig struct {
	Build   []string `mapstructure:"build"`
	Publish []string `mapstructure:"publish"`
	Version []string `mapstructure:"version"`
	// Login runs once per space before its first publish; every other
	// publish of the space waits for it, and its failure fails them all.
	Login []string `mapstructure:"login"`
	// Announce is a fourth stage after a successful publish: pushing the
	// release out to update channels, with the release-notes variables in its
	// environment. The whole frame — hooks included — only warns on failure.
	Announce []string `mapstructure:"announce"`
	// Hooks around the package stages. The before*/post* hooks up to
	// beforePublish fail the package's release when they fail; postPublish and
	// the announce hooks only warn, because by then the release is out.
	BeforeAll      []string `mapstructure:"beforeAll"`
	BeforeVersion  []string `mapstructure:"beforeVersion"`
	PostVersion    []string `mapstructure:"postVersion"`
	BeforeBuild    []string `mapstructure:"beforeBuild"`
	PostBuild      []string `mapstructure:"postBuild"`
	BeforePublish  []string `mapstructure:"beforePublish"`
	PostPublish    []string `mapstructure:"postPublish"`
	BeforeAnnounce []string `mapstructure:"beforeAnnounce"`
	PostAnnounce   []string `mapstructure:"postAnnounce"`
	// Outcome scripts, both warn-only: onFail runs when a package of the
	// space fails at any stage, onSkip when it is skipped because a provider
	// failed.
	OnFail []string `mapstructure:"onFail"`
	OnSkip []string `mapstructure:"onSkip"`
}

// scriptRefs returns every script reference field of the space, labelled for
// error messages, so validation and resolution never disagree about the list.
func (s *SpaceConfig) scriptRefs() map[string][]string {
	return map[string][]string{
		"run.build":          s.Run.Build,
		"run.publish":        s.Run.Publish,
		"run.version":        s.Run.Version,
		"run.login":          s.Run.Login,
		"run.announce":       s.Run.Announce,
		"run.beforeAll":      s.Run.BeforeAll,
		"run.beforeVersion":  s.Run.BeforeVersion,
		"run.postVersion":    s.Run.PostVersion,
		"run.beforeBuild":    s.Run.BeforeBuild,
		"run.postBuild":      s.Run.PostBuild,
		"run.beforePublish":  s.Run.BeforePublish,
		"run.postPublish":    s.Run.PostPublish,
		"run.beforeAnnounce": s.Run.BeforeAnnounce,
		"run.postAnnounce":   s.Run.PostAnnounce,
		"run.onFail":         s.Run.OnFail,
		"run.onSkip":         s.Run.OnSkip,
	}
}

// DependencyConfig is one consumer -> provider relation.
type DependencyConfig struct {
	Consumer string `mapstructure:"consumer"`
	Provider string `mapstructure:"provider"`
}

var validLevels = map[string]bool{
	"trace": true, "debug": true, "info": true, "warn": true, "error": true,
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

// Load reads and validates the configuration file. When flags is non-nil the
// "concurrency", "log-level" and "log-format" flags are bound through viper,
// so explicitly set flags override file values (and file values override flag
// defaults). Defaults applied afterwards: concurrency 0 means the number of
// CPUs, logLevel defaults to "info", logFormat to "pretty".
func Load(path string, flags *pflag.FlagSet) (*File, error) {
	v := viper.New()
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("config: cannot read %s: %w", path, err)
	}
	if flags != nil {
		for key, flagName := range map[string]string{
			"concurrency": "concurrency",
			"logLevel":    "log-level",
			"logFormat":   "log-format",
		} {
			if f := flags.Lookup(flagName); f != nil {
				if err := v.BindPFlag(key, f); err != nil {
					return nil, fmt.Errorf("config: binding flag %s: %w", flagName, err)
				}
			}
		}
	}

	var cfg File
	// UnmarshalExact rejects unknown keys, catching config typos early.
	// WeaklyTypedInput lets a scalar concurrency value decode into the slice.
	weak := func(dc *mapstructure.DecoderConfig) { dc.WeaklyTypedInput = true }
	if err := v.UnmarshalExact(&cfg, weak); err != nil {
		return nil, fmt.Errorf("config: invalid format in %s: %w", path, err)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return &cfg, nil
}

// script resolves a script reference case-insensitively, because viper
// lowercases the keys of the scripts map.
func (c *File) script(ref string) (string, bool) {
	s, ok := c.Scripts[strings.ToLower(ref)]
	return s, ok
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
		if s, ok := c.script(ref); ok {
			out = append(out, s)
		}
	}
	return out
}

// runHookRefs returns the run-level hook references, labelled for error
// messages.
func (c *File) runHookRefs() map[string][]string {
	return map[string][]string{
		"run.beforeAll":    c.Run.BeforeAll,
		"run.postAll":      c.Run.PostAll,
		"run.beforeCommit": c.Run.BeforeCommit,
		"run.afterCommit":  c.Run.AfterCommit,
		"run.postCommit":   c.Run.PostCommit,
		"run.beforePush":   c.Run.BeforePush,
		"run.afterPush":    c.Run.AfterPush,
	}
}

func (c *File) validate() error {
	if len(c.Spaces) == 0 {
		return errors.New("at least one space is required")
	}
	var build, publish int
	switch len(c.Concurrency) {
	case 0: // not configured: default both
	case 1:
		build, publish = c.Concurrency[0], c.Concurrency[0]
	case 2:
		build, publish = c.Concurrency[0], c.Concurrency[1]
	default:
		return fmt.Errorf("concurrency accepts at most two values [build, publish], got %v", c.Concurrency)
	}
	if build < 0 || publish < 0 {
		return fmt.Errorf("concurrency values must be >= 0, got %v", c.Concurrency)
	}
	if build == 0 {
		build = runtime.NumCPU()
	}
	if publish == 0 {
		publish = runtime.NumCPU()
	}
	c.BuildConcurrency, c.PublishConcurrency = build, publish
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	if !validLevels[c.LogLevel] {
		return fmt.Errorf("unknown logLevel %q (want trace, debug, info, warn or error)", c.LogLevel)
	}
	if c.LogFormat == "" {
		c.LogFormat = "pretty"
	}
	if c.LogFormat != "pretty" && c.LogFormat != "json" {
		return fmt.Errorf("unknown logFormat %q (want pretty or json)", c.LogFormat)
	}
	if c.CommitErrors == "" {
		c.CommitErrors = CommitErrorsWarn
	}
	if c.CommitErrors != CommitErrorsWarn && c.CommitErrors != CommitErrorsError {
		return fmt.Errorf("unknown commitErrors %q (want %q or %q)",
			c.CommitErrors, CommitErrorsWarn, CommitErrorsError)
	}
	if c.NonPackageScopes == nil {
		c.NonPackageScopes = DefaultNonPackageScopes()
	}
	if c.TagFormat == "" {
		c.TagFormat = string(gitx.DefaultTagFormat)
	}
	if err := gitx.TagFormat(c.TagFormat).Validate(); err != nil {
		return err
	}
	for name, s := range c.Spaces {
		if s.Path == "" {
			return fmt.Errorf("space %q: path is required", name)
		}
		if s.TagFormat != "" {
			if err := gitx.TagFormat(s.TagFormat).Validate(); err != nil {
				return fmt.Errorf("space %q: %w", name, err)
			}
		}
		for field, refs := range s.scriptRefs() {
			for _, ref := range refs {
				if ref == "" {
					return fmt.Errorf("space %q: %s contains an empty script reference", name, field)
				}
				if _, ok := c.script(ref); !ok {
					return fmt.Errorf("space %q: %s references unknown script %q", name, field, ref)
				}
			}
		}
	}
	for field, refs := range c.runHookRefs() {
		for _, ref := range refs {
			if ref == "" {
				return fmt.Errorf("%s contains an empty script reference", field)
			}
			if _, ok := c.script(ref); !ok {
				return fmt.Errorf("%s references unknown script %q", field, ref)
			}
		}
	}
	for i, d := range c.Dependencies {
		if d.Consumer == "" || d.Provider == "" {
			return fmt.Errorf("dependencies[%d]: consumer and provider are required", i)
		}
		if d.Consumer == d.Provider {
			return fmt.Errorf("dependencies[%d]: package %q cannot depend on itself", i, d.Consumer)
		}
	}
	if len(c.Shell) > 0 && c.Shell[0] == "" {
		return errors.New("shell: first element (the interpreter) must not be empty")
	}
	if len(c.Initials) > 0 {
		c.InitialVersions = make(map[string]ccme.Version, len(c.Initials))
		for name, raw := range c.Initials {
			v, err := ccme.ParseVersion(raw)
			if err != nil {
				return fmt.Errorf("initials[%q]: invalid version %q: %w", name, raw, err)
			}
			c.InitialVersions[name] = v
		}
	}
	return nil
}

// Discover walks every space folder and returns the packages found inside,
// plus the validated dependency edges. Every direct sub-folder of a space is a
// package named after the folder; names must be unique across all spaces.
func (c *File) Discover(root string) ([]*model.Package, []model.Dependency, error) {
	spaceNames := make([]string, 0, len(c.Spaces))
	for n := range c.Spaces {
		spaceNames = append(spaceNames, n)
	}
	sort.Strings(spaceNames) // deterministic discovery order

	var pkgs []*model.Package
	owner := make(map[string]string) // package name -> space name
	for _, sn := range spaceNames {
		sc := c.Spaces[sn]
		tagFormat := sc.TagFormat
		if tagFormat == "" {
			tagFormat = c.TagFormat
		}
		space := &model.Space{
			Name:                 sn,
			Path:                 sc.Path,
			BuildWaitsPublish:    sc.IsBuildWaitingPublish,
			RevertOnFail:         sc.RevertOnFail,
			BuildScript:          c.Commands(sc.Run.Build),
			PublishScript:        c.Commands(sc.Run.Publish),
			VersionScript:        c.Commands(sc.Run.Version),
			LoginScript:          c.Commands(sc.Run.Login),
			AnnounceScript:       c.Commands(sc.Run.Announce),
			BeforeAllScript:      c.Commands(sc.Run.BeforeAll),
			BeforeVersionScript:  c.Commands(sc.Run.BeforeVersion),
			PostVersionScript:    c.Commands(sc.Run.PostVersion),
			BeforeBuildScript:    c.Commands(sc.Run.BeforeBuild),
			PostBuildScript:      c.Commands(sc.Run.PostBuild),
			BeforePublishScript:  c.Commands(sc.Run.BeforePublish),
			PostPublishScript:    c.Commands(sc.Run.PostPublish),
			BeforeAnnounceScript: c.Commands(sc.Run.BeforeAnnounce),
			PostAnnounceScript:   c.Commands(sc.Run.PostAnnounce),
			OnFailScript:         c.Commands(sc.Run.OnFail),
			OnSkipScript:         c.Commands(sc.Run.OnSkip),
			TagFormat:            tagFormat,
		}
		dir := filepath.Join(root, sc.Path)
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, nil, fmt.Errorf("config: space %q: %w", sn, err)
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			name := e.Name()
			if prev, dup := owner[name]; dup {
				return nil, nil, fmt.Errorf(
					"config: package %q exists in both space %q and space %q; package names must be unique",
					name, prev, sn)
			}
			owner[name] = sn
			pkgs = append(pkgs, &model.Package{
				Name:  name,
				Dir:   filepath.Join(dir, name),
				Space: space,
			})
		}
	}

	deps := make([]model.Dependency, 0, len(c.Dependencies))
	for i, d := range c.Dependencies {
		if _, ok := owner[d.Consumer]; !ok {
			return nil, nil, fmt.Errorf("config: dependencies[%d]: unknown consumer package %q", i, d.Consumer)
		}
		if _, ok := owner[d.Provider]; !ok {
			return nil, nil, fmt.Errorf("config: dependencies[%d]: unknown provider package %q", i, d.Provider)
		}
		deps = append(deps, model.Dependency{Consumer: d.Consumer, Provider: d.Provider})
	}
	return pkgs, deps, nil
}
