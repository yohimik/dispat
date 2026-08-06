// Package config loads and validates the monorepo configuration file (via
// viper) and discovers the packages living inside the configured spaces.
//
// The configuration model itself — the structs a config file decodes into —
// is public, in the pkg/models module, so external tooling can author
// configurations as typed values; this package aliases those types and owns
// everything that needs the rest of the CLI: loading, validation, defaulting
// and workspace discovery.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/yohimik/dispat/pkg/ccme"

	public "github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/services/cli/internal/gitx"
	"github.com/yohimik/dispat/services/cli/internal/model"
)

// The configuration model, aliased from the public package so the rest of the
// CLI keeps importing internal/config alone.
type (
	File              = public.File
	RunConfig         = public.RunConfig
	EntryFormatConfig = public.EntryFormatConfig
	ChangelogConfig   = public.ChangelogConfig
	GitHubConfig      = public.GitHubConfig
	CommitConfig      = public.CommitConfig
	SpaceConfig       = public.SpaceConfig
	SpaceRunConfig    = public.SpaceRunConfig
	DependencyConfig  = public.DependencyConfig
)

// Values of the commitErrors key; see the public package for semantics.
const (
	CommitErrorsWarn  = public.CommitErrorsWarn
	CommitErrorsError = public.CommitErrorsError
)

// Versioning values of a space; see the public package for semantics.
const (
	VersioningIndependent = public.VersioningIndependent
	VersioningFixed       = public.VersioningFixed
	VersioningFixedSparse = public.VersioningFixedSparse
)

// DefaultNonPackageScopes returns the scopes exempt from the unknown-include
// error by default. "release" is dispat's own release-commit scope.
func DefaultNonPackageScopes() []string { return public.DefaultNonPackageScopes() }

// scriptRefs returns every script reference field of the space, labelled for
// error messages, so validation and resolution never disagree about the list.
func scriptRefs(s *SpaceConfig) map[string][]string {
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

// runHookRefs returns the run-level hook references, labelled for error
// messages.
func runHookRefs(c *File) map[string][]string {
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

var validLevels = map[string]bool{
	"trace": true, "debug": true, "info": true, "warn": true, "error": true,
}

// checkScriptRefs verifies that every labelled script reference is non-empty
// and resolves, so validation and Commands resolution can never disagree
// about a list. prefix locates the owner in the error ("space \"libs\": ").
func checkScriptRefs(c *File, refs map[string][]string, prefix string) error {
	for field, list := range refs {
		for _, ref := range list {
			if ref == "" {
				return fmt.Errorf("%s%s contains an empty script reference", prefix, field)
			}
			if _, ok := c.Script(ref); !ok {
				return fmt.Errorf("%s%s references unknown script %q", prefix, field, ref)
			}
		}
	}
	return nil
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
	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return &cfg, nil
}

// resolveParser maps the config's `parser` object onto a ccme.Config. Unset
// fields stay at their zero values, which ccme documents as "the
// specification default" — an absent parser object is exactly the parser
// dispat always built. The result is validated by actually constructing a
// parser, so a bad value fails the load rather than the first release.
func resolveParser(p public.ParserConfig) (ccme.Config, error) {
	cfg := ccme.Config{
		Separator:            p.Separator,
		StrictTypes:          p.StrictTypes,
		Lenient:              p.Lenient,
		MaxDescriptionLength: p.MaxDescriptionLength,
		AllowedChannels:      p.AllowedChannels,
		MessageLevelTrailers: p.MessageLevelTrailers,
		IssueTrailers:        p.IssueTrailers,
		Limits: ccme.Limits{
			UnitsPerMessage:   p.Limits.UnitsPerMessage,
			ScopeTermsPerUnit: p.Limits.ScopeTermsPerUnit,
			MessageBytes:      p.Limits.MessageBytes,
		},
	}
	if len(p.Types) > 0 {
		cfg.Types = make(map[string]ccme.Bump, len(p.Types))
		for name, raw := range p.Types {
			bump, ok := ccme.ParseBump(raw)
			if !ok {
				return cfg, fmt.Errorf("parser: types[%q]: unknown bump %q (want none, patch, minor or major)", name, raw)
			}
			cfg.Types[name] = bump
		}
	}
	if p.Propagation.Bump != "" {
		prop, ok := ccme.ParsePropagate(p.Propagation.Bump)
		if !ok {
			return cfg, fmt.Errorf("parser: propagation.bump: unknown value %q", p.Propagation.Bump)
		}
		cfg.Propagation.Bump = prop
	}
	var err error
	if cfg.Propagation.Depth, err = parseDepth(p.Propagation.Depth); err != nil {
		return cfg, fmt.Errorf("parser: propagation.depth: %w", err)
	}
	if cfg.Propagation.ChannelDepth, err = parseDepth(p.Propagation.ChannelDepth); err != nil {
		return cfg, fmt.Errorf("parser: propagation.channelDepth: %w", err)
	}
	if p.Propagation.Kinds != nil {
		cfg.Propagation.Kinds = make([]ccme.DependencyKind, 0, len(p.Propagation.Kinds))
		for _, raw := range p.Propagation.Kinds {
			kind, ok := ccme.ParseDependencyKind(raw)
			if !ok {
				return cfg, fmt.Errorf("parser: propagation.kinds: unknown kind %q", raw)
			}
			cfg.Propagation.Kinds = append(cfg.Propagation.Kinds, kind)
		}
	}
	cfg.Propagation.Channel = p.Propagation.Channel

	if _, err := ccme.NewParser(cfg); err != nil {
		return cfg, fmt.Errorf("parser: %w", err)
	}
	return cfg, nil
}

// parseDepth reads a config depth: "" keeps the default (0), "all" or "*" is
// the transitive closure, anything else a non-negative edge count. Weak
// decoding turns a numeric config value into its decimal string, so both
// `depth: 1` and `depth: "all"` load.
func parseDepth(raw string) (ccme.Depth, error) {
	switch raw {
	case "":
		return 0, nil
	case "all", "*":
		return ccme.DepthAll, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("want a non-negative number or %q, got %q", "all", raw)
	}
	return ccme.Depth(n), nil
}

// normalizeVersioning resolves a space's versioning value case-insensitively
// onto the canonical constants; ok is false for an unknown value.
func normalizeVersioning(raw string) (string, bool) {
	switch strings.ToLower(raw) {
	case "", strings.ToLower(VersioningIndependent):
		return VersioningIndependent, true
	case strings.ToLower(VersioningFixed):
		return VersioningFixed, true
	case strings.ToLower(VersioningFixedSparse):
		return VersioningFixedSparse, true
	}
	return "", false
}

func validate(c *File) error {
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
		versioning, ok := normalizeVersioning(s.Versioning)
		if !ok {
			return fmt.Errorf("space %q: unknown versioning %q (want %q, %q or %q)",
				name, s.Versioning, VersioningIndependent, VersioningFixed, VersioningFixedSparse)
		}
		s.Versioning = versioning
		c.Spaces[name] = s
		for scriptName, cmd := range s.RunScripts {
			if scriptName == "" {
				return fmt.Errorf("space %q: runScripts contains an empty script name", name)
			}
			if strings.TrimSpace(cmd) == "" {
				return fmt.Errorf("space %q: runScripts[%q] is empty", name, scriptName)
			}
		}
		if err := checkScriptRefs(c, scriptRefs(&s), fmt.Sprintf("space %q: ", name)); err != nil {
			return err
		}
	}
	if err := checkScriptRefs(c, runHookRefs(c), ""); err != nil {
		return err
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
	parserCfg, err := resolveParser(c.Parser)
	if err != nil {
		return err
	}
	c.ParserConfig = parserCfg
	return nil
}

// Discover walks every space folder and returns the packages found inside,
// plus the validated dependency edges. Every direct sub-folder of a space is a
// package named after the folder; names must be unique across all spaces.
func Discover(c *File, root string) ([]*model.Package, []model.Dependency, error) {
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
			Versioning:           model.Versioning(sc.Versioning),
			RunScripts:           sc.RunScripts,
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
