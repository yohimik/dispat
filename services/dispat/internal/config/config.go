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

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// The configuration model, aliased from the public package so the rest of the
// CLI keeps importing internal/config alone.
type (
	File                    = public.File
	RunConfig               = public.RunConfig
	EntryFormatConfig       = public.EntryFormatConfig
	ChangelogConfig         = public.ChangelogConfig
	GitHubConfig            = public.GitHubConfig
	CommitConfig            = public.CommitConfig
	SpaceConfig             = public.SpaceConfig
	SpaceFlowConfig         = public.SpaceFlowConfig
	AutoVersionConfig       = public.AutoVersionConfig
	DependencyConfig        = public.DependencyConfig
	ParserConfig            = public.ParserConfig
	ParserPropagationConfig = public.ParserPropagationConfig
	ParserLimitsConfig      = public.ParserLimitsConfig
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
		"flow.build":          s.Flow.Build,
		"flow.publish":        s.Flow.Publish,
		"flow.version":        s.Flow.Version,
		"flow.login":          s.Flow.Login,
		"flow.announce":       s.Flow.Announce,
		"flow.beforeAll":      s.Flow.BeforeAll,
		"flow.beforeVersion":  s.Flow.BeforeVersion,
		"flow.postVersion":    s.Flow.PostVersion,
		"flow.beforeBuild":    s.Flow.BeforeBuild,
		"flow.postBuild":      s.Flow.PostBuild,
		"flow.beforePublish":  s.Flow.BeforePublish,
		"flow.postPublish":    s.Flow.PostPublish,
		"flow.beforeAnnounce": s.Flow.BeforeAnnounce,
		"flow.postAnnounce":   s.Flow.PostAnnounce,
		"flow.onFail":         s.Flow.OnFail,
		"flow.onSkip":         s.Flow.OnSkip,
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
// DefaultFileNames are the config file names the CLI looks for, in order,
// when --config is not explicitly set: the file `dispat init` writes under
// each of its formats. The first that exists wins.
var DefaultFileNames = []string{"dispat.json", "dispat.yaml", "dispat.yml", "dispat.toml"}

// ResolveFile returns the path of the configuration file to load and the
// monorepo root it establishes. An explicitly named file is used as-is,
// relative to root — a typo there must fail loudly, not fall back to a
// different file — while the default resolves to the first of
// DefaultFileNames that exists in root or, failing that, in any parent
// directory up to the filesystem root: the ascent is what lets the CLI run
// from inside a package folder, with the config's own directory becoming the
// effective monorepo root. When nothing is found, the error says so and
// names every candidate tried.
func ResolveFile(root, name string, explicit bool) (path, resolvedRoot string, err error) {
	if explicit {
		return filepath.Join(root, name), root, nil
	}
	for _, cand := range DefaultFileNames {
		p := filepath.Join(root, cand)
		if _, err := os.Stat(p); err == nil {
			return p, root, nil
		}
	}
	// Not in root itself: ascend. Absolute paths make the parent walk
	// well-defined wherever the relative root pointed.
	if abs, absErr := filepath.Abs(root); absErr == nil {
		for dir := filepath.Dir(abs); ; dir = filepath.Dir(dir) {
			for _, cand := range DefaultFileNames {
				p := filepath.Join(dir, cand)
				if _, err := os.Stat(p); err == nil {
					return p, dir, nil
				}
			}
			if dir == filepath.Dir(dir) { // filesystem root
				break
			}
		}
	}
	return "", "", fmt.Errorf(
		"config: no dispat config file found in %s or any parent directory (tried %s); run `dispat init` to create one",
		root, strings.Join(DefaultFileNames, ", "))
}

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
func resolveParser(p *public.ParserConfig) (ccme.Config, error) {
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

// fillOptional replaces nil optional sub-objects with their zero values. The
// pointers exist for marshalling — an unset object is an absent key rather
// than "{}" — not for behaviour: nil and the zero struct mean the same thing,
// so filling them lets validation and everything after it dereference freely.
func fillOptional(c *File) {
	if c.Changelog == nil {
		c.Changelog = &ChangelogConfig{}
	}
	if c.GitHub == nil {
		c.GitHub = &GitHubConfig{}
	}
	if c.Commit == nil {
		c.Commit = &CommitConfig{}
	}
	if c.Run == nil {
		c.Run = &RunConfig{}
	}
	if c.Parser == nil {
		c.Parser = &ParserConfig{}
	}
	if c.Parser.Propagation == nil {
		c.Parser.Propagation = &ParserPropagationConfig{}
	}
	if c.Parser.Limits == nil {
		c.Parser.Limits = &ParserLimitsConfig{}
	}
	for name, s := range c.Spaces {
		if s.Flow == nil {
			s.Flow = &SpaceFlowConfig{}
			c.Spaces[name] = s
		}
	}
}

// validate checks the loaded configuration and resolves its defaulted values
// in place. Each concern lives in its own helper; the order only matters in
// that everything is validated before Discover consumes any of it.
func validate(c *File) error {
	fillOptional(c)
	if len(c.Spaces) == 0 {
		return errors.New("at least one space is required")
	}
	if err := validateConcurrency(c); err != nil {
		return err
	}
	if err := validateLogging(c); err != nil {
		return err
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
		validated, err := validateSpace(c, name, s)
		if err != nil {
			return err
		}
		c.Spaces[name] = validated
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
	if err := resolveInitials(c); err != nil {
		return err
	}
	parserCfg, err := resolveParser(c.Parser)
	if err != nil {
		return err
	}
	c.ResolvedParser = parserCfg
	return nil
}

// validateConcurrency resolves the scalar-or-pair concurrency value onto the
// two stage budgets, defaulting 0 (and absence) to the number of CPUs.
func validateConcurrency(c *File) error {
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
	return nil
}

// validateLogging defaults and checks logLevel and logFormat.
func validateLogging(c *File) error {
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
	return nil
}

// validateSpace checks one space — path, tag format, versioning mode,
// runScripts, script references — and returns it with its versioning value
// normalized.
func validateSpace(c *File, name string, s SpaceConfig) (SpaceConfig, error) {
	if s.Path == "" {
		return s, fmt.Errorf("space %q: path is required", name)
	}
	if s.TagFormat != "" {
		if err := gitx.TagFormat(s.TagFormat).Validate(); err != nil {
			return s, fmt.Errorf("space %q: %w", name, err)
		}
	}
	versioning, ok := normalizeVersioning(s.Versioning)
	if !ok {
		return s, fmt.Errorf("space %q: unknown versioning %q (want %q, %q or %q)",
			name, s.Versioning, VersioningIndependent, VersioningFixed, VersioningFixedSparse)
	}
	s.Versioning = versioning
	for scriptName, cmd := range s.RunScripts {
		if scriptName == "" {
			return s, fmt.Errorf("space %q: runScripts contains an empty script name", name)
		}
		if strings.TrimSpace(cmd) == "" {
			return s, fmt.Errorf("space %q: runScripts[%q] is empty", name, scriptName)
		}
	}
	if err := checkScriptRefs(c, scriptRefs(&s), fmt.Sprintf("space %q: ", name)); err != nil {
		return s, err
	}
	if err := validateAutoVersion(c, name, s.AutoVersion); err != nil {
		return s, err
	}
	return s, nil
}

// validateAutoVersion checks a space's autoVersion object. The `only` names
// need the discovered packages and are checked in Discover instead.
func validateAutoVersion(c *File, space string, av *public.AutoVersionConfig) error {
	if av == nil {
		return nil
	}
	prefix := fmt.Sprintf("space %q: autoVersion: ", space)
	switch av.Manifests {
	case "", "root", "all":
	default:
		return fmt.Errorf(`%smanifests: unknown value %q (want "root" or "all")`, prefix, av.Manifests)
	}
	for _, k := range av.Kinds {
		if _, err := DepKind(k); err != nil {
			return fmt.Errorf("%skinds: %w", prefix, err)
		}
	}
	for _, m := range av.Match {
		if _, err := filepath.Match(m, ""); err != nil {
			return fmt.Errorf("%smatch: invalid pattern %q: %w", prefix, m, err)
		}
	}
	switch av.NameMatch {
	case "", "exact", "substring":
	default:
		return fmt.Errorf(`%snameMatch: unknown value %q (want "exact" or "substring")`, prefix, av.NameMatch)
	}
	if av.SyncLockConcurrency < 0 {
		return fmt.Errorf("%ssyncLockConcurrency must be >= 0, got %d", prefix, av.SyncLockConcurrency)
	}
	return checkScriptRefs(c, map[string][]string{"autoVersion.syncLock": av.SyncLock}, fmt.Sprintf("space %q: ", space))
}

// resolveAutoVersion maps a validated autoVersion object onto the domain
// policy; nil (or enabled: false) resolves to nil, feature off.
func resolveAutoVersion(c *File, av *public.AutoVersionConfig) *model.AutoVersion {
	if !av.IsEnabled() {
		return nil
	}
	kinds := make(map[model.DepKind]bool, 4)
	if len(av.Kinds) == 0 {
		for _, k := range []model.DepKind{model.KindDependencies, model.KindDevDependencies,
			model.KindPeerDependencies, model.KindOptionalDependencies} {
			kinds[k] = true
		}
	} else {
		for _, raw := range av.Kinds {
			k, _ := DepKind(raw) // validated
			kinds[k] = true
		}
	}
	var only map[string]bool
	if len(av.Only) > 0 {
		only = make(map[string]bool, len(av.Only))
		for _, name := range av.Only {
			only[name] = true
		}
	}
	return &model.AutoVersion{
		AllManifests:        av.Manifests == "all",
		Kinds:               kinds,
		Only:                only,
		NameSubstring:       av.NameMatch == "substring",
		Match:               av.Match,
		Range:               av.Range,
		WriteVersion:        av.WriteVersionEnabled(),
		SyncLock:            c.Commands(av.SyncLock),
		SyncLockConcurrency: av.SyncLockConcurrency,
	}
}

// resolveInitials parses the initials map into versions.
func resolveInitials(c *File) error {
	if len(c.Initials) == 0 {
		return nil
	}
	c.InitialVersions = make(map[string]ccme.Version, len(c.Initials))
	for name, raw := range c.Initials {
		v, err := ccme.ParseVersion(raw)
		if err != nil {
			return fmt.Errorf("initials[%q]: invalid version %q: %w", name, raw, err)
		}
		c.InitialVersions[name] = v
	}
	return nil
}

// Discover walks every space folder and returns the packages found inside,
// plus the validated dependency edges. Every direct sub-folder of a space is a
// package named after the folder; names must be unique across all spaces.
func Discover(c *File, root string) ([]*model.Package, []model.Dependency, error) {
	pkgs, err := DiscoverPackages(c, root)
	if err != nil {
		return nil, nil, err
	}
	owner := make(map[string]bool, len(pkgs))
	for _, p := range pkgs {
		owner[p.Name] = true
	}

	deps := make([]model.Dependency, 0, len(c.Dependencies))
	for i, d := range c.Dependencies {
		if !owner[d.Consumer] {
			return nil, nil, fmt.Errorf("config: dependencies[%d]: unknown consumer package %q", i, d.Consumer)
		}
		if !owner[d.Provider] {
			return nil, nil, fmt.Errorf("config: dependencies[%d]: unknown provider package %q", i, d.Provider)
		}
		kind, err := DepKind(d.Kind)
		if err != nil {
			return nil, nil, fmt.Errorf("config: dependencies[%d]: %w", i, err)
		}
		deps = append(deps, model.Dependency{Consumer: d.Consumer, Provider: d.Provider, Kind: kind})
	}
	return pkgs, deps, nil
}

// DiscoverPackages is Discover without the dependency-list validation: the
// packages that exist on disk, whatever the `dependencies` key says. It exists
// for `dispat compute`, whose whole job includes suggesting the removal of
// edges naming packages that no longer exist — edges Discover must refuse.
func DiscoverPackages(c *File, root string) ([]*model.Package, error) {
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
			BuildScript:          c.Commands(sc.Flow.Build),
			PublishScript:        c.Commands(sc.Flow.Publish),
			VersionScript:        c.Commands(sc.Flow.Version),
			LoginScript:          c.Commands(sc.Flow.Login),
			AnnounceScript:       c.Commands(sc.Flow.Announce),
			BeforeAllScript:      c.Commands(sc.Flow.BeforeAll),
			BeforeVersionScript:  c.Commands(sc.Flow.BeforeVersion),
			PostVersionScript:    c.Commands(sc.Flow.PostVersion),
			BeforeBuildScript:    c.Commands(sc.Flow.BeforeBuild),
			PostBuildScript:      c.Commands(sc.Flow.PostBuild),
			BeforePublishScript:  c.Commands(sc.Flow.BeforePublish),
			PostPublishScript:    c.Commands(sc.Flow.PostPublish),
			BeforeAnnounceScript: c.Commands(sc.Flow.BeforeAnnounce),
			PostAnnounceScript:   c.Commands(sc.Flow.PostAnnounce),
			OnFailScript:         c.Commands(sc.Flow.OnFail),
			OnSkipScript:         c.Commands(sc.Flow.OnSkip),
			TagFormat:            tagFormat,
			AutoVersion:          resolveAutoVersion(c, sc.AutoVersion),
		}
		dir := filepath.Join(root, sc.Path)
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, fmt.Errorf("config: space %q: %w", sn, err)
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			name := e.Name()
			if prev, dup := owner[name]; dup {
				return nil, fmt.Errorf(
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

	// autoVersion `only` names must be discovered packages; anything else is
	// the same class of typo as an unknown dependency endpoint. Only enabled
	// blocks are held to it: a disabled block is inert configuration.
	for _, sn := range spaceNames {
		av := c.Spaces[sn].AutoVersion
		if av == nil || !av.IsEnabled() {
			continue
		}
		for _, name := range av.Only {
			if _, ok := owner[name]; !ok {
				return nil, fmt.Errorf("config: space %q: autoVersion.only: unknown package %q", sn, name)
			}
		}
	}
	return pkgs, nil
}

// DepKind maps a dependency edge's configured kind onto the model's. Empty
// means a plain runtime dependency (§8.4's zero value).
func DepKind(s string) (model.DepKind, error) {
	switch s {
	case "", "dependencies":
		return model.KindDependencies, nil
	case "devDependencies":
		return model.KindDevDependencies, nil
	case "peerDependencies":
		return model.KindPeerDependencies, nil
	case "optionalDependencies":
		return model.KindOptionalDependencies, nil
	}
	return "", fmt.Errorf(`unknown dependency kind %q (one of "dependencies", "devDependencies", "peerDependencies", "optionalDependencies")`, s)
}
