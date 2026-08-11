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
	"reflect"
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
	File                     = public.File
	RunConfig                = public.RunConfig
	EntryFormatConfig        = public.EntryFormatConfig
	ChangelogConfig          = public.ChangelogConfig
	GitHubConfig             = public.GitHubConfig
	CommitConfig             = public.CommitConfig
	SpaceConfig              = public.SpaceConfig
	SpaceFile                = public.SpaceFile
	SpaceFlowConfig          = public.SpaceFlowConfig
	PackageConfig            = public.PackageConfig
	VersionGroupConfig       = public.VersionGroupConfig
	AutoVersionConfig        = public.AutoVersionConfig
	AutoVersionReplaceConfig = public.AutoVersionReplaceConfig
	DependencyConfig         = public.DependencyConfig
	ParserConfig             = public.ParserConfig
	ParserPropagationConfig  = public.ParserPropagationConfig
	ParserLimitsConfig       = public.ParserLimitsConfig
)

// Values of the commitErrors key; see the public package for semantics.
const (
	CommitErrorsWarn  = public.CommitErrorsWarn
	CommitErrorsError = public.CommitErrorsError
)

// Versioning values of a space; see the public package for semantics.
const (
	VersioningIndependent           = public.VersioningIndependent
	VersioningFixed                 = public.VersioningFixed
	VersioningFixedSparse           = public.VersioningFixedSparse
	VersioningFixedMajorMinor       = public.VersioningFixedMajorMinor
	VersioningFixedMajorMinorSparse = public.VersioningFixedMajorMinorSparse
	VersioningFixedMajor            = public.VersioningFixedMajor
	VersioningFixedMajorSparse      = public.VersioningFixedMajorSparse
)

// versioningNames lists every accepted versioning value in the order error
// messages spell them: the default first, then the shared modes from the most
// shared to the least. It is the single list normalizeVersioning matches
// against and every "want ..." message is rendered from, so a mode can never
// be accepted by one and omitted by the other.
var versioningNames = []string{
	VersioningIndependent,
	VersioningFixed,
	VersioningFixedSparse,
	VersioningFixedMajorMinor,
	VersioningFixedMajorMinorSparse,
	VersioningFixedMajor,
	VersioningFixedMajorSparse,
}

// sharedVersioningNames lists the modes a versionGroups declaration accepts:
// versioningNames without the independent default, which shares nothing.
func sharedVersioningNames() []string {
	out := make([]string, 0, len(versioningNames)-1)
	for _, name := range versioningNames {
		if model.Versioning(name).Shared() {
			out = append(out, name)
		}
	}
	return out
}

// quotedNames renders a list of accepted values for an error message as
// `"a", "b" or "c"`.
func quotedNames(names []string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = strconv.Quote(name)
	}
	if len(quoted) < 2 {
		return strings.Join(quoted, "")
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + " or " + quoted[len(quoted)-1]
}

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

// scriptScope is where a script reference may resolve: the names in view,
// plus the sentence an error uses to say where the name was looked for.
// Keeping the two together is what stops a resolution site and its error
// message from describing different sets of names.
type scriptScope struct {
	scripts map[string]string
	hint    string
}

// packageScope is the scope a package's references resolve in: the file's
// scripts overlaid with the space's, then the package's. sc arrives already
// merged, so its own map carries both of the lower two levels and this only
// has to add the top one underneath.
func packageScope(c *File, sc SpaceConfig) scriptScope {
	scripts := make(map[string]string, len(c.Scripts)+len(sc.Scripts))
	for k, v := range c.Scripts {
		scripts[k] = v
	}
	for k, v := range sc.Scripts {
		scripts[k] = v
	}
	return scriptScope{scripts, "no scripts entry in the package, its space or the top level"}
}

// rootScope is the scope of the run hooks: they execute once at the
// repository root, with no package in view, so only the file's scripts are.
func rootScope(c *File) scriptScope {
	return scriptScope{c.Scripts, "no top-level scripts entry"}
}

// commands resolves a sequence of script references into the shell commands
// they name, preserving order. Unknown references were rejected by check, so
// resolution cannot silently drop one.
func (s scriptScope) commands(refs []string) []string {
	if len(refs) == 0 {
		return nil
	}
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if cmd, ok := s.scripts[strings.ToLower(ref)]; ok {
			out = append(out, cmd)
		}
	}
	return out
}

// check verifies that every labelled script reference is non-empty and
// resolves in the scope, so validation and command resolution can never
// disagree about a list. prefix locates the owner in the error ("space
// \"libs\": "). Fields are checked in name order, so a config with several
// mistakes always reports the same one first.
func (s scriptScope) check(refs map[string][]string, prefix string) error {
	fields := make([]string, 0, len(refs))
	for field := range refs {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	for _, field := range fields {
		for _, ref := range refs[field] {
			if ref == "" {
				return fmt.Errorf("%s%s contains an empty script reference", prefix, field)
			}
			if _, ok := s.scripts[strings.ToLower(ref)]; !ok {
				return fmt.Errorf("%s%s references unknown script %q (%s)", prefix, field, ref, s.hint)
			}
		}
	}
	return nil
}

// checkSpaceRefs verifies every script reference a space-shaped config makes
// (its flow entries and its autoVersion.syncLock) against the scope of the
// package it was merged for. It runs per package, not per space, because a
// package may be the level that defines the script its space's flow names.
func (s scriptScope) checkSpaceRefs(label string, sc SpaceConfig) error {
	if err := s.check(scriptRefs(&sc), label+": "); err != nil {
		return err
	}
	if sc.AutoVersion == nil {
		return nil
	}
	return s.check(map[string][]string{"autoVersion.syncLock": sc.AutoVersion.SyncLock}, label+": ")
}

// checkScriptValues rejects the two ways a scripts map itself can be unusable,
// at whichever level holds it: a nameless entry, and a name bound to no
// command at all.
func checkScriptValues(label string, scripts map[string]string) error {
	for name, cmd := range scripts {
		if name == "" {
			return fmt.Errorf("%s: scripts contains an empty script name", label)
		}
		if strings.TrimSpace(cmd) == "" {
			return fmt.Errorf("%s: scripts[%q] is empty", label, name)
		}
	}
	return nil
}

// Load reads and validates the configuration file. When flags is non-nil the
// "concurrency", "log-level" and "log-format" flags are bound through viper,
// so explicitly set flags override file values (and file values override flag
// defaults). Defaults applied afterwards: concurrency 0 means the number of
// CPUs, logLevel defaults to "info", logFormat to "pretty".
// defaultFileNames are the config file names the CLI looks for, in order,
// when --config is not explicitly set: the file `dispat init` writes under
// each of its formats. The first that exists wins.
var defaultFileNames = []string{"dispat.json", "dispat.yaml", "dispat.yml", "dispat.toml"}

// ResolveFile returns the path of the configuration file to load and the
// monorepo root it establishes. An explicitly named file is used as-is,
// relative to root — a typo there must fail loudly, not fall back to a
// different file — while the default resolves to the first of
// defaultFileNames that exists in root or, failing that, in any parent
// directory up to the filesystem root: the ascent is what lets the CLI run
// from inside a package folder, with the config's own directory becoming the
// effective monorepo root.
//
// A space folder and a package folder may both carry a dispat config file of
// their own — their override layers — so what a found file declares decides
// whether the ascent ends there:
//
//	declares spaces      a monorepo root; the ascent stops
//	cannot be read       likewise, because a broken root config must fail in
//	                     Load rather than be silently skipped
//	declares packages    a root candidate, remembered, and the ascent goes on:
//	                     a space folder's file declares packages too
//	neither              a package folder's file; remembered as the weakest
//	                     fallback, and the ascent goes on
//
// A remembered candidate is only displaced by a spaces-declaring ancestor
// that owns it: the ancestor's space paths decide, so a space folder's file
// yields to the root above it while a repository whose only config declares
// packages stays its own root. When no file on the way up declares either
// key, the nearest file found is returned anyway: the "at least one space or
// package" error it produces names the real mistake. When nothing is found,
// the error says so and names every candidate tried.
func ResolveFile(root, name string, explicit bool) (path, resolvedRoot string, err error) {
	if explicit {
		return filepath.Join(root, name), root, nil
	}
	var candidate, candidateRoot string // declares packages, not spaces
	var fallback, fallbackRoot string   // declares neither
	// A directory contributes its first candidate name alone — the name-order
	// precedence within one folder predates the ascent and stays.
	try := func(dir string) (string, string, error) {
		names, err := configCandidates(dir)
		if err != nil {
			return "", "", fmt.Errorf("config: %s: %s: %w", dir, DispatignoreName, err)
		}
		if len(names) == 0 {
			return "", "", nil
		}
		p := filepath.Join(dir, names[0])
		switch classifyConfig(p) {
		case configRoot:
			// A candidate below is a space folder's file when this root claims
			// its folder, and a monorepo of its own when it does not.
			if candidate != "" && !ownsSpaceFolder(p, dir, candidateRoot) {
				return candidate, candidateRoot, nil
			}
			return p, dir, nil
		case configBroken:
			return p, dir, nil
		case configPackages:
			if candidate == "" {
				candidate, candidateRoot = p, dir
			}
		default:
			if fallback == "" {
				fallback, fallbackRoot = p, dir
			}
		}
		return "", "", nil
	}
	dirs := []string{root}
	// Beyond root itself the walk ascends; absolute paths make it
	// well-defined wherever the relative root pointed.
	if abs, absErr := filepath.Abs(root); absErr == nil {
		for dir := filepath.Dir(abs); ; dir = filepath.Dir(dir) {
			dirs = append(dirs, dir)
			if dir == filepath.Dir(dir) { // filesystem root
				break
			}
		}
	}
	for _, dir := range dirs {
		p, r, err := try(dir)
		if err != nil {
			return "", "", err
		}
		if p != "" {
			return p, r, nil
		}
	}
	if candidate != "" {
		return candidate, candidateRoot, nil
	}
	if fallback != "" {
		return fallback, fallbackRoot, nil
	}
	return "", "", fmt.Errorf(
		"config: no dispat config file found in %s or any parent directory (tried %s); run `dispat init` to create one",
		root, strings.Join(defaultFileNames, ", "))
}

// configClass is what a file found during the ascent turns out to be.
type configClass int

const (
	// configLoose declares neither spaces nor packages: a package folder's
	// override file, or a root config missing both.
	configLoose configClass = iota
	// configPackages declares packages but no spaces: either a monorepo of
	// standalone packages, or a space folder's own file.
	configPackages
	// configRoot declares spaces, so it is a monorepo root.
	configRoot
	// configBroken cannot be read at all.
	configBroken
)

// classifyConfig reads just enough of a file to place it. A file viper cannot
// read is broken rather than skippable: Load is where a broken config fails
// loudly, and stepping over it to use a parent's file would hide the
// breakage.
func classifyConfig(path string) configClass {
	v := viper.New()
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		return configBroken
	}
	switch {
	case v.IsSet("spaces"):
		return configRoot
	case v.IsSet("packages"):
		return configPackages
	default:
		return configLoose
	}
}

// ownsSpaceFolder reports whether the root config at path, loaded with rootDir
// as the monorepo root, declares a space whose folder is dir — which is what
// tells a space folder's config file apart from a monorepo of standalone
// packages. Folders are compared by identity, so a symlinked or
// case-insensitive path still matches itself.
func ownsSpaceFolder(path, rootDir, dir string) bool {
	target, err := os.Stat(dir)
	if err != nil {
		return false
	}
	v := viper.New()
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		return false
	}
	spaces, ok := v.Get("spaces").(map[string]any)
	if !ok {
		return false
	}
	for _, raw := range spaces {
		fields, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		p, ok := fields["path"].(string)
		if !ok || p == "" {
			continue
		}
		info, err := os.Stat(filepath.Join(rootDir, filepath.FromSlash(p)))
		if err == nil && os.SameFile(info, target) {
			return true
		}
	}
	return false
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

	// The keys a package entry may never hold are refused by name, at every
	// map that holds entries, before decoding drops them into an unknown-key
	// error that could not say why.
	if err := refusePackageEntryKeys("packages", rawEntries(v, "packages")); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	for _, sn := range sortedKeys(rawEntries(v, "spaces")) {
		if err := refusePackageEntryKeys(fmt.Sprintf("spaces[%q]: packages", sn),
			rawEntries(v, "spaces", sn, "packages")); err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}
	}

	var cfg File
	// UnmarshalExact rejects unknown keys, catching config typos early.
	// WeaklyTypedInput lets a scalar concurrency value decode into the slice,
	// and the shorthand hook expands {consumer: provider(s)} dependency items
	// into full entries before decoding.
	weak := func(dc *mapstructure.DecoderConfig) {
		dc.WeaklyTypedInput = true
		if dc.DecodeHook != nil {
			dc.DecodeHook = mapstructure.ComposeDecodeHookFunc(dependencyShorthandHook, dc.DecodeHook)
		} else {
			dc.DecodeHook = mapstructure.DecodeHookFunc(dependencyShorthandHook)
		}
	}
	if err := v.UnmarshalExact(&cfg, weak); err != nil {
		return nil, fmt.Errorf("config: invalid format in %s: %w", path, err)
	}
	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return &cfg, nil
}

// dependenciesType is the decode target the shorthand hook watches for: the
// top-level `dependencies` list.
var dependenciesType = reflect.TypeOf([]public.DependencyConfig(nil))

// dependencyShorthandHook expands shorthand items of the `dependencies`
// array before decoding. A full entry is an object carrying a `consumer` or
// `provider` key; any other object is shorthand — each key a consumer name,
// its value a provider name or array of names — and expands to one full
// entry per provider. Viper lowercases map keys, so shorthand consumer names
// are matched like every other name-keyed map in the config.
func dependencyShorthandHook(_, to reflect.Type, data any) (any, error) {
	if to != dependenciesType {
		return data, nil
	}
	items, ok := data.([]any)
	if !ok {
		return data, nil
	}
	out := make([]any, 0, len(items))
	for i, item := range items {
		m, ok := stringKeyMap(item)
		if !ok || isEdgeObject(m) {
			out = append(out, item)
			continue
		}
		consumers := make([]string, 0, len(m))
		for k := range m {
			consumers = append(consumers, k)
		}
		sort.Strings(consumers)
		for _, consumer := range consumers {
			providers, ok := providerNames(m[consumer])
			if !ok {
				return nil, fmt.Errorf(
					"dependencies[%d]: %q wants a provider name or an array of names", i, consumer)
			}
			for _, p := range providers {
				out = append(out, map[string]any{"consumer": consumer, "provider": p})
			}
		}
	}
	return out, nil
}

// stringKeyMap normalizes the two map shapes config formats decode into.
func stringKeyMap(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case map[string]any:
		return m, true
	case map[any]any:
		out := make(map[string]any, len(m))
		for k, val := range m {
			s, ok := k.(string)
			if !ok {
				return nil, false
			}
			out[s] = val
		}
		return out, true
	}
	return nil, false
}

// isEdgeObject reports whether a dependencies item is a full edge object
// rather than a consumer-keyed shorthand.
func isEdgeObject(m map[string]any) bool {
	for k := range m {
		switch strings.ToLower(k) {
		case "consumer", "provider":
			return true
		}
	}
	return false
}

// providerNames reads a shorthand value: one provider name or an array of
// names.
func providerNames(v any) ([]string, bool) {
	switch x := v.(type) {
	case string:
		return []string{x}, true
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			s, ok := e.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	}
	return nil, false
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
// onto the canonical constants; ok is false for an unknown value. An absent
// value is the independent default.
func normalizeVersioning(raw string) (string, bool) {
	if raw == "" {
		return VersioningIndependent, true
	}
	low := strings.ToLower(raw)
	for _, name := range versioningNames {
		if low == strings.ToLower(name) {
			return name, true
		}
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
	if len(c.Spaces) == 0 && len(c.Packages) == 0 {
		return errors.New("at least one space or package is required")
	}
	if err := validatePackageEntries(c); err != nil {
		return err
	}
	if err := validateVersionGroups(c); err != nil {
		return err
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
	for _, name := range sortedSpaceNames(c) {
		validated, err := validateSpace(name, c.Spaces[name])
		if err != nil {
			return err
		}
		if err := validateSpacePackages(fmt.Sprintf("spaces[%q]: packages", name), validated.Packages); err != nil {
			return err
		}
		c.Spaces[name] = validated
	}
	// versionGroup references can only be checked once every space's
	// versioning is normalized: a reference may name another space's
	// implicit group.
	for name, s := range c.Spaces {
		if s.VersionGroup == "" {
			continue
		}
		if _, _, err := resolveVersionGroup(c, s.VersionGroup); err != nil {
			return fmt.Errorf("space %q: %w", name, err)
		}
	}
	if err := checkScriptValues("config", c.Scripts); err != nil {
		return err
	}
	if err := rootScope(c).check(runHookRefs(c), ""); err != nil {
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
	// commit.include paths are staged relative to the monorepo root; anything
	// absolute or escaping the root would stage files outside the repository.
	for i, p := range c.Commit.Include {
		if p == "" || filepath.IsAbs(p) {
			return fmt.Errorf("commit.include[%d]: %q must be a repository-relative path", i, p)
		}
		if clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(p))); clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("commit.include[%d]: %q escapes the repository root", i, p)
		}
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
// scripts — and returns it with its versioning value normalized. What the
// space's flow references is not checked here: a reference resolves in a
// package's scope, which discovery knows and this does not, so
// checkSpaceRefs owns that check.
func validateSpace(name string, s SpaceConfig) (SpaceConfig, error) {
	return validateSpaceAs(fmt.Sprintf("space %q", name), s)
}

// validateSpaceAs is validateSpace under an explicit error label, so the
// same checks validate a package's merged override config ("space "libs":
// package "core"") — an override is space-shaped and held to space rules.
func validateSpaceAs(label string, s SpaceConfig) (SpaceConfig, error) {
	if s.Path == "" {
		return s, fmt.Errorf("%s: path is required", label)
	}
	if s.TagFormat != "" {
		if err := gitx.TagFormat(s.TagFormat).Validate(); err != nil {
			return s, fmt.Errorf("%s: %w", label, err)
		}
	}
	// Checked before normalization, which erases the difference between an
	// absent versioning and an explicit "independent" — only the former may
	// accompany a group reference.
	if s.VersionGroup != "" && s.Versioning != "" {
		return s, fmt.Errorf("%s: versioning and versionGroup are mutually exclusive (the group's versioning is authoritative)", label)
	}
	versioning, ok := normalizeVersioning(s.Versioning)
	if !ok {
		return s, fmt.Errorf("%s: unknown versioning %q (want %s)",
			label, s.Versioning, quotedNames(versioningNames))
	}
	s.Versioning = versioning
	if err := checkScriptValues(label, s.Scripts); err != nil {
		return s, err
	}
	if err := validateAutoVersion(label, s.AutoVersion); err != nil {
		return s, err
	}
	return s, nil
}

// validateAutoVersion checks an autoVersion object's own values under the
// owner's error label. The `only` names need the discovered packages and are
// checked in Discover instead; syncLock's references need a package's scope
// and are checked in checkScriptScope.
func validateAutoVersion(label string, av *public.AutoVersionConfig) error {
	if av == nil {
		return nil
	}
	prefix := label + ": autoVersion: "
	switch av.Manifests {
	case "", "root", "all", "none":
	default:
		return fmt.Errorf(`%smanifests: unknown value %q (want "root", "all" or "none")`, prefix, av.Manifests)
	}
	for i, r := range av.Replace {
		at := fmt.Sprintf("%sreplace[%d]: ", prefix, i)
		if len(r.Files) == 0 {
			return fmt.Errorf("%sfiles is required: a rule with nothing to apply to writes nothing", at)
		}
		for _, g := range r.Files {
			if g == "" {
				return fmt.Errorf("%sfiles: empty glob", at)
			}
			if _, err := filepath.Match(g, ""); err != nil {
				return fmt.Errorf("%sfiles: invalid pattern %q: %w", at, g, err)
			}
		}
		if r.Find == "" {
			return fmt.Errorf("%sfind is required: an empty pattern matches everywhere", at)
		}
		if r.Write == "" {
			return fmt.Errorf("%swrite is required: use find alone only when there is something to write", at)
		}
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
	return nil
}

// resolveAutoVersion maps a validated autoVersion object onto the domain
// policy; nil (or enabled: false) resolves to nil, feature off. scope is the
// owning package's, which is where syncLock's references resolve.
func resolveAutoVersion(scope scriptScope, av *public.AutoVersionConfig) *model.AutoVersion {
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
	manifests := model.ScopeRoot
	if av.Manifests != "" {
		manifests = model.ManifestScope(av.Manifests) // validated
	}
	var replace []model.ReplaceRule
	for _, r := range av.Replace {
		replace = append(replace, model.ReplaceRule{Files: r.Files, Find: r.Find, Write: r.Write})
	}
	return &model.AutoVersion{
		Manifests:           manifests,
		Replace:             replace,
		Kinds:               kinds,
		Only:                only,
		NameSubstring:       av.NameMatch == "substring",
		Match:               av.Match,
		Range:               av.Range,
		WriteVersion:        av.WriteVersionEnabled(),
		SyncLock:            scope.commands(av.SyncLock),
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

// DepSource locates where a dependency edge was declared, so `dispat
// compute` can edit the exact file (and key) holding it.
type DepSource struct {
	// File is the config file holding the declaration; empty means the
	// loaded root config itself.
	File string
	// KeyPath is the key path of the list inside that file, ending in
	// "dependencies" and preceded by a map name and a key for every level
	// above it: ["dependencies"] for a file's own list, ["packages", <key>,
	// "dependencies"] for a packages entry, and ["spaces", <space>,
	// "packages", <key>, "dependencies"] for a space's packages entry of the
	// root config.
	KeyPath []string
	// Index is the entry's position within that list.
	Index int
}

// Label renders the source for error messages and suggestion listings:
// "dependencies[2]", `packages["core"]: dependencies[0]`,
// `spaces["libs"]: packages["core"]: dependencies[0]`, or
// "packages/core/dispat.json: dependencies[0]".
func (s DepSource) Label() string {
	if len(s.KeyPath) == 0 {
		return "dependencies"
	}
	var b strings.Builder
	if s.File != "" {
		b.WriteString(s.File)
		b.WriteString(": ")
	}
	// Every level above the list itself is a map name and the key inside it,
	// so the path renders the same way however deep it goes.
	for i := 0; i+1 < len(s.KeyPath); i += 2 {
		fmt.Fprintf(&b, "%s[%q]: ", s.KeyPath[i], s.KeyPath[i+1])
	}
	fmt.Fprintf(&b, "%s[%d]", s.KeyPath[len(s.KeyPath)-1], s.Index)
	return b.String()
}

// IsRootList reports whether the source is the root config's own top-level
// `dependencies` list — the one place compute appends additions to.
func (s DepSource) IsRootList() bool {
	return s.File == "" && len(s.KeyPath) == 1
}

// DeclaredDependency is one dependency edge with its declaration source. A
// package-level declaration is already normalized: the consumer is the
// declaring package and the kind is empty (the default).
type DeclaredDependency struct {
	DependencyConfig
	Source DepSource
}

// sortedSpaceNames returns the configured space names in order, so validation
// and discovery always visit them the same way and a config with several
// mistakes always reports the same one first.
func sortedSpaceNames(c *File) []string {
	names := make([]string, 0, len(c.Spaces))
	for n := range c.Spaces {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Discover walks every space folder and returns the packages found inside
// (standalone `packages` entries included), plus the validated dependency
// edges merged from every declaration source. Every direct sub-folder of a
// space is a package named after the folder; names must be unique across all
// spaces.
func Discover(c *File, root string) ([]*model.Package, []model.Dependency, error) {
	pkgs, declared, err := DiscoverPackages(c, root)
	if err != nil {
		return nil, nil, err
	}
	owner := make(map[string]bool, len(pkgs))
	for _, p := range pkgs {
		owner[p.Name] = true
	}

	deps := make([]model.Dependency, 0, len(declared))
	for _, d := range declared {
		if !owner[d.Consumer] {
			return nil, nil, fmt.Errorf("config: %s: unknown consumer package %q", d.Source.Label(), d.Consumer)
		}
		if !owner[d.Provider] {
			return nil, nil, fmt.Errorf("config: %s: unknown provider package %q", d.Source.Label(), d.Provider)
		}
		kind, err := DepKind(d.Kind)
		if err != nil {
			return nil, nil, fmt.Errorf("config: %s: %w", d.Source.Label(), err)
		}
		deps = append(deps, model.Dependency{Consumer: d.Consumer, Provider: d.Provider, Kind: kind})
	}
	return pkgs, deps, nil
}

// DiscoverPackages is Discover without the dependency-list validation: the
// packages that exist on disk plus every declared dependency edge with its
// source, whatever the declarations say. It exists for `dispat compute`,
// whose whole job includes suggesting the removal of edges naming packages
// that no longer exist — edges Discover must refuse.
//
// Discovery is also where a package's effective configuration settles: a
// folder excluded by the space's .dispatignore is not a package at all, a
// package with overrides — a `packages` entry, an in-folder config file,
// or both — gets a derived Space of its own with the merged configuration,
// and a `packages` entry with a `path` becomes a standalone package outside
// every space, validated here because the override layers need the folders
// to exist.
func DiscoverPackages(c *File, root string) ([]*model.Package, []DeclaredDependency, error) {
	spaceNames := sortedSpaceNames(c) // deterministic discovery order

	baseChangelog := changelogSpec(c.Changelog)
	baseGitHub := githubSpec(c.GitHub)
	type onlyCheck struct {
		label string
		av    *AutoVersionConfig
	}
	var onlyChecks []onlyCheck

	// The merged declaration list: the root config's own `dependencies`
	// first, in file order, then each package's lists in discovery order.
	declared := make([]DeclaredDependency, 0, len(c.Dependencies))
	for i, d := range c.Dependencies {
		declared = append(declared, DeclaredDependency{
			DependencyConfig: d,
			Source:           DepSource{KeyPath: []string{"dependencies"}, Index: i},
		})
	}

	var pkgs []*model.Package
	owner := make(map[string]string)      // package name -> space name
	consumed := make(map[string][]string) // packages key -> matching folders
	var ignoredDirs []ignoredDir
	spaceConfigs := make(map[string]SpaceConfig, len(spaceNames))
	for _, sn := range spaceNames {
		sc := c.Spaces[sn]
		dir := filepath.Join(root, sc.Path)
		// The space folder's own config file, the layer between the root
		// file's space entry and anything said about one package. A space
		// rooted at the repository itself has no such layer: the file in that
		// folder is the root config, and merging it into itself would be
		// reading one statement twice.
		var spaceFile SpaceFile
		var spaceSrc string
		if !sameDir(dir, root) {
			var err error
			if spaceFile, spaceSrc, err = loadSpaceFile(dir); err != nil {
				return nil, nil, fmt.Errorf("config: space %q: %w", sn, err)
			}
		}
		if spaceSrc != "" {
			label := fmt.Sprintf("space %q (%s)", sn, spaceSrc)
			sc = mergePackageOverride(sc, spaceOverride(spaceFile))
			var err error
			if sc, err = validateSpaceAs(label, sc); err != nil {
				return nil, nil, fmt.Errorf("config: %w", err)
			}
			if err := validateSpacePackages(label+": packages", spaceFile.Packages); err != nil {
				return nil, nil, fmt.Errorf("config: %w", err)
			}
		}
		spaceConfigs[sn] = sc
		baseScope := packageScope(c, sc)
		base, err := buildSpace(c, baseScope, fmt.Sprintf("space %q", sn), sn, sc)
		if err != nil {
			return nil, nil, fmt.Errorf("config: %w", err)
		}
		// Every package without an override layer shares the space's scope, so
		// its references are the same question with the same answer: ask once,
		// under the name of the first package that asked.
		baseRefsChecked := false
		ignore, err := loadIgnore(dir)
		if err != nil {
			return nil, nil, fmt.Errorf("config: space %q: %s: %w", sn, DispatignoreName, err)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, nil, fmt.Errorf("config: space %q: %w", sn, err)
		}
		// The space's two package-level maps account for their keys against
		// the folders of this space alone.
		var spaceIgnored []ignoredDir
		spaceConsumed := make(map[string][]string)
		fileConsumed := make(map[string][]string)
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			name := e.Name()
			if ignoredName(ignore, name) {
				spaceIgnored = append(spaceIgnored, ignoredDir{sn, name})
				continue
			}
			if prev, dup := owner[name]; dup {
				return nil, nil, fmt.Errorf(
					"config: package %q exists in both space %q and space %q; package names must be unique",
					name, prev, sn)
			}
			owner[name] = sn
			pkg := &model.Package{
				Name:          name,
				Dir:           filepath.Join(dir, name),
				Space:         base,
				BuildWeight:   1,
				PublishWeight: 1,
				Changelog:     baseChangelog,
				GitHub:        baseGitHub,
			}

			key := strings.ToLower(name)
			label := fmt.Sprintf("space %q: package %q", sn, name)
			var layers []overrideLayer
			if entryPO, ok := c.Package(name); ok {
				if entryPO.Path != "" {
					return nil, nil, fmt.Errorf(
						"config: packages[%q]: package %q belongs to space %q — its location is the space folder, so path cannot be set",
						key, name, sn)
				}
				consumed[key] = append(consumed[key], name)
				layers = append(layers, overrideLayer{entryPO, label,
					DepSource{KeyPath: []string{"packages", key, "dependencies"}}})
			}
			if spacePO, ok := sc.Package(name); ok {
				spaceConsumed[key] = append(spaceConsumed[key], name)
				layers = append(layers, overrideLayer{spacePO, label + ": the space's packages entry",
					DepSource{KeyPath: []string{"spaces", sn, "packages", key, "dependencies"}}})
			}
			if filePO, ok := spaceFile.Package(name); ok {
				fileConsumed[key] = append(fileConsumed[key], name)
				layers = append(layers, overrideLayer{filePO, fmt.Sprintf("%s (%s: packages entry)", label, spaceSrc),
					DepSource{File: spaceSrc, KeyPath: []string{"packages", key, "dependencies"}}})
			}
			folderPO, folderSrc, err := loadPackageFile(pkg.Dir)
			if err != nil {
				return nil, nil, fmt.Errorf("config: space %q: package %q: %w", sn, name, err)
			}
			if folderSrc != "" {
				layers = append(layers, overrideLayer{folderPO, fmt.Sprintf("%s (%s)", label, folderSrc),
					DepSource{File: folderSrc, KeyPath: []string{"dependencies"}}})
			}
			if len(layers) > 0 {
				merged, ex, autoVersioned, withDeps, err := applyLayers(c, sc, name, layers, declared)
				if err != nil {
					return nil, nil, fmt.Errorf("config: %w", err)
				}
				declared = withDeps
				if merged, err = validateSpaceAs(label, merged); err != nil {
					return nil, nil, fmt.Errorf("config: %w", err)
				}
				scope := packageScope(c, merged)
				if err := scope.checkSpaceRefs(label, merged); err != nil {
					return nil, nil, fmt.Errorf("config: %w", err)
				}
				if pkg.Space, err = buildSpace(c, scope, label, sn, merged); err != nil {
					return nil, nil, fmt.Errorf("config: %w", err)
				}
				if autoVersioned {
					onlyChecks = append(onlyChecks, onlyCheck{label, merged.AutoVersion})
				}
				pkg.BuildWeight, pkg.PublishWeight = packageWeights(ex.concurrency)
				pkg.ManifestNames = ex.manifestNames
				pkg.Src = ex.src
				if ex.changelog != nil {
					pkg.Changelog = changelogSpec(ex.changelog)
				}
				if ex.github != nil {
					pkg.GitHub = githubSpec(ex.github)
				}
			} else if !baseRefsChecked {
				// No override layer: the package resolves the space's own
				// references, but the error still names the package, because
				// the scope a reference has to resolve in is always a
				// package's.
				if err := baseScope.checkSpaceRefs(label, sc); err != nil {
					return nil, nil, fmt.Errorf("config: %w", err)
				}
				baseRefsChecked = true
			}
			pkgs = append(pkgs, pkg)
		}
		if err := (keyCheck{
			label:    fmt.Sprintf("spaces[%q]: packages", sn),
			entries:  sc.Packages,
			consumed: spaceConsumed,
			ignored:  spaceIgnored,
			missing:  fmt.Sprintf("matches no folder of space %q (a package of another space is configured by that space, or by the top-level packages map)", sn),
		}).run(); err != nil {
			return nil, nil, err
		}
		if err := (keyCheck{
			label:    fmt.Sprintf("%s: packages", spaceSrc),
			entries:  spaceFile.Packages,
			consumed: fileConsumed,
			ignored:  spaceIgnored,
			missing:  fmt.Sprintf("matches no folder of space %q", sn),
		}).run(); err != nil {
			return nil, nil, err
		}
		ignoredDirs = append(ignoredDirs, spaceIgnored...)
	}
	if err := (keyCheck{
		label:      "packages",
		entries:    c.Packages,
		consumed:   consumed,
		ignored:    ignoredDirs,
		missing:    "matches no package folder (a standalone package needs a path)",
		standalone: true,
	}).run(); err != nil {
		return nil, nil, err
	}

	// Standalone packages: entries with a path, in deterministic key order.
	// An entry whose key matched a folder was rejected above, so every name
	// here is new.
	var standalone []string
	for key, po := range c.Packages {
		if po.Path != "" {
			standalone = append(standalone, key)
		}
	}
	sort.Strings(standalone)
	for _, key := range standalone {
		po := c.Packages[key]
		label := fmt.Sprintf("package %q", key)
		dir := filepath.Join(root, filepath.FromSlash(po.Path))
		info, err := os.Stat(dir)
		if err != nil {
			return nil, nil, fmt.Errorf("config: %s: %w", label, err)
		}
		if !info.IsDir() {
			return nil, nil, fmt.Errorf("config: %s: path %q is not a folder", label, po.Path)
		}
		owner[key] = ""
		pkg := &model.Package{
			Name:          key,
			Dir:           dir,
			BuildWeight:   1,
			PublishWeight: 1,
			Changelog:     baseChangelog,
			GitHub:        baseGitHub,
		}
		// The entry is the package's whole configuration: a synthetic
		// single-package space built through the same layers as an override —
		// the entry, then the in-folder file — so a standalone package can
		// never express something a space package cannot.
		layers := []overrideLayer{{po, label,
			DepSource{KeyPath: []string{"packages", key, "dependencies"}}}}
		filePO, fileSrc, err := loadPackageFile(dir)
		if err != nil {
			return nil, nil, fmt.Errorf("config: %s: %w", label, err)
		}
		if fileSrc != "" {
			layers = append(layers, overrideLayer{filePO, fmt.Sprintf("%s (%s)", label, fileSrc),
				DepSource{File: fileSrc, KeyPath: []string{"dependencies"}}})
		}
		merged, ex, autoVersioned, withDeps, err := applyLayers(c, SpaceConfig{Path: po.Path, Flow: &SpaceFlowConfig{}},
			key, layers, declared)
		if err != nil {
			return nil, nil, fmt.Errorf("config: %w", err)
		}
		declared = withDeps
		if merged, err = validateSpaceAs(label, merged); err != nil {
			return nil, nil, fmt.Errorf("config: %w", err)
		}
		scope := packageScope(c, merged)
		if err := scope.checkSpaceRefs(label, merged); err != nil {
			return nil, nil, fmt.Errorf("config: %w", err)
		}
		if pkg.Space, err = buildSpace(c, scope, label, key, merged); err != nil {
			return nil, nil, fmt.Errorf("config: %w", err)
		}
		if autoVersioned {
			onlyChecks = append(onlyChecks, onlyCheck{label, merged.AutoVersion})
		}
		pkg.BuildWeight, pkg.PublishWeight = packageWeights(ex.concurrency)
		pkg.ManifestNames = ex.manifestNames
		pkg.Src = ex.src
		if ex.changelog != nil {
			pkg.Changelog = changelogSpec(ex.changelog)
		}
		if ex.github != nil {
			pkg.GitHub = githubSpec(ex.github)
		}
		pkgs = append(pkgs, pkg)
	}

	// autoVersion `only` names must be discovered packages; anything else is
	// the same class of typo as an unknown dependency endpoint. Only enabled
	// blocks are held to it: a disabled block is inert configuration.
	for _, sn := range spaceNames {
		av := spaceConfigs[sn].AutoVersion
		if av == nil || !av.IsEnabled() {
			continue
		}
		for _, name := range av.Only {
			if _, ok := owner[name]; !ok {
				return nil, nil, fmt.Errorf("config: space %q: autoVersion.only: unknown package %q", sn, name)
			}
		}
	}
	for _, chk := range onlyChecks {
		if !chk.av.IsEnabled() {
			continue
		}
		for _, name := range chk.av.Only {
			if _, ok := owner[name]; !ok {
				return nil, nil, fmt.Errorf("config: %s: autoVersion.only: unknown package %q", chk.label, name)
			}
		}
	}
	if err := checkManifestNames(pkgs); err != nil {
		return nil, nil, err
	}
	if err := checkSrcFolders(pkgs); err != nil {
		return nil, nil, err
	}
	return pkgs, declared, nil
}

// checkSrcFolders proves every declared `src` names a folder that is there.
// A misspelled one would silently narrow the package to nothing — no file
// could ever be under it — and the package would stop releasing without
// anything ever saying why, release after release.
func checkSrcFolders(pkgs []*model.Package) error {
	for _, p := range pkgs {
		if p.Src == "" {
			continue
		}
		switch fi, err := os.Stat(p.ScopeDir()); {
		case err != nil:
			return fmt.Errorf("config: package %q: src %q names no folder inside the package", p.Name, p.Src)
		case !fi.IsDir():
			return fmt.Errorf("config: package %q: src %q names a file, want a folder", p.Name, p.Src)
		}
	}
	return nil
}

// checkManifestNames proves no two packages state the same manifest name. A
// name a manifest merely declares twice is a warning at scan time (W220,
// nothing is derived from it), but a name two packages were *told* to answer
// to is a typo in the configuration, and letting it resolve to neither would
// silently turn the feature off for both.
func checkManifestNames(pkgs []*model.Package) error {
	stated := make(map[string]string)
	for _, p := range pkgs {
		for _, name := range p.ManifestNames {
			if name == "" {
				return fmt.Errorf("config: package %q: manifestNames: empty name", p.Name)
			}
			if prev, taken := stated[name]; taken {
				first, second := prev, p.Name
				if second < first {
					first, second = second, first
				}
				return fmt.Errorf(
					"config: manifestNames: %q is stated by both package %q and package %q; a manifest name identifies one package",
					name, first, second)
			}
			stated[name] = p.Name
		}
	}
	return nil
}

// buildSpace resolves one validated space-shaped configuration onto the
// domain Space: script references become shell commands through the package's
// scope (its scripts over the space's over the file's), the tag format falls
// back to the repository's, and the versioning mode and group key come from
// the config's own versioning or the group it references. label locates the
// owner in errors: the space itself, or the package whose merged override
// this is. The caller checked the same scope with checkSpaceRefs, so nothing
// here can silently resolve to nothing.
func buildSpace(c *File, scope scriptScope, label, spaceName string, sc SpaceConfig) (*model.Space, error) {
	mode, group, err := resolveSpaceVersioning(c, spaceName, sc)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	tagFormat := sc.TagFormat
	if tagFormat == "" {
		tagFormat = c.TagFormat
	}
	return &model.Space{
		Name:                 spaceName,
		Path:                 sc.Path,
		BuildWaitsPublish:    sc.IsBuildWaitingPublish,
		RevertOnFail:         sc.RevertOnFail,
		Versioning:           model.Versioning(mode),
		VersionGroup:         group,
		Scripts:              scope.scripts,
		BuildScript:          scope.commands(sc.Flow.Build),
		PublishScript:        scope.commands(sc.Flow.Publish),
		VersionScript:        scope.commands(sc.Flow.Version),
		LoginScript:          scope.commands(sc.Flow.Login),
		AnnounceScript:       scope.commands(sc.Flow.Announce),
		BeforeAllScript:      scope.commands(sc.Flow.BeforeAll),
		BeforeVersionScript:  scope.commands(sc.Flow.BeforeVersion),
		PostVersionScript:    scope.commands(sc.Flow.PostVersion),
		BeforeBuildScript:    scope.commands(sc.Flow.BeforeBuild),
		PostBuildScript:      scope.commands(sc.Flow.PostBuild),
		BeforePublishScript:  scope.commands(sc.Flow.BeforePublish),
		PostPublishScript:    scope.commands(sc.Flow.PostPublish),
		BeforeAnnounceScript: scope.commands(sc.Flow.BeforeAnnounce),
		PostAnnounceScript:   scope.commands(sc.Flow.PostAnnounce),
		OnFailScript:         scope.commands(sc.Flow.OnFail),
		OnSkipScript:         scope.commands(sc.Flow.OnSkip),
		TagFormat:            tagFormat,
		AutoVersion:          resolveAutoVersion(scope, sc.AutoVersion),
	}, nil
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
