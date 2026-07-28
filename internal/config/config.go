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

	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/yohimik/dispat/internal/model"
	"github.com/yohimik/dispat/internal/semver"
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
	// "pkg@0.0.1-0.0.0" tag). The next release bumps on top of this value.
	// Keys are matched case-insensitively against discovered packages.
	Initials map[string]string `mapstructure:"initials"`

	// Resolved values, populated by validation.
	BuildConcurrency   int                       `mapstructure:"-"`
	PublishConcurrency int                       `mapstructure:"-"`
	InitialVersions    map[string]semver.Version `mapstructure:"-"`
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

// SpaceConfig is the raw configuration of one space. All script references
// are optional: a missing script means the stage runs without executing a
// shell command.
type SpaceConfig struct {
	Path                  string `mapstructure:"path"`
	IsBuildWaitingPublish bool   `mapstructure:"isBuildWaitingPublish"`
	RevertOnFail          bool   `mapstructure:"revertOnFail"`
	BuildScript           string `mapstructure:"buildScript"`
	PublishScript         string `mapstructure:"publishScript"`
	VersionScript         string `mapstructure:"versionScript"`
}

// DependencyConfig is one consumer -> provider relation.
type DependencyConfig struct {
	Consumer string `mapstructure:"consumer"`
	Provider string `mapstructure:"provider"`
}

var validLevels = map[string]bool{
	"trace": true, "debug": true, "info": true, "warn": true, "error": true,
}

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
	for name, s := range c.Spaces {
		if s.Path == "" {
			return fmt.Errorf("space %q: path is required", name)
		}
		for _, ref := range []string{s.BuildScript, s.PublishScript, s.VersionScript} {
			if ref == "" { // scripts are optional
				continue
			}
			if _, ok := c.script(ref); !ok {
				return fmt.Errorf("space %q references unknown script %q", name, ref)
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
		c.InitialVersions = make(map[string]semver.Version, len(c.Initials))
		for name, raw := range c.Initials {
			v, err := semver.Parse(raw)
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
		build, _ := c.script(sc.BuildScript)
		publish, _ := c.script(sc.PublishScript)
		version, _ := c.script(sc.VersionScript)
		space := &model.Space{
			Name:              sn,
			Path:              sc.Path,
			BuildWaitsPublish: sc.IsBuildWaitingPublish,
			RevertOnFail:      sc.RevertOnFail,
			BuildScript:       build,
			PublishScript:     publish,
			VersionScript:     version,
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
