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

	"github.com/spf13/pflag"
	"github.com/yohimik/dispat/pkg/ccme"

	public "github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// The configuration model, aliased from the public package so the rest of the
// CLI keeps importing internal/config alone.
type (
	File                     = public.File
	Script                   = public.Script
	RunConfig                = public.RunConfig
	EntryFormatConfig        = public.EntryFormatConfig
	EntryLine                = public.EntryLine
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
	Dependencies             = public.Dependencies
	ProviderList             = public.ProviderList
	AliasTagConfig           = public.AliasTagConfig

	ParserConfig            = public.ParserConfig
	ParserPropagationConfig = public.ParserPropagationConfig
	ParserLimitsConfig      = public.ParserLimitsConfig
)

// Providers builds a package's dependency list out of plain provider names.
var Providers = public.Providers

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
	VersioningNone                  = public.VersioningNone
)

// versioningNames lists every accepted versioning value in the order error
// messages spell them: the default first, then the shared modes from the most
// shared to the least, then none, which shares nothing and releases nothing.
// It is the single list normalizeVersioning matches against and every
// "want ..." message is rendered from, so a mode can never be accepted by one
// and omitted by the other.
var versioningNames = []string{
	VersioningIndependent,
	VersioningFixed,
	VersioningFixedSparse,
	VersioningFixedMajorMinor,
	VersioningFixedMajorMinorSparse,
	VersioningFixedMajor,
	VersioningFixedMajorSparse,
	VersioningNone,
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
	scripts map[string]Script
	hint    string
}

// packageScope is the scope a package's references resolve in: the file's
// scripts overlaid with the space's, then the package's. sc arrives already
// merged, so its own map carries both of the lower two levels and this only
// has to add the top one underneath.
func packageScope(c *File, sc SpaceConfig) scriptScope {
	scripts := make(map[string]Script, len(c.Scripts)+len(sc.Scripts))
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
//
// A reference contributes every command its script binds, so the two levels of
// ordering — the references, and the commands inside each — flatten into the
// one order the sequence runs in.
func (s scriptScope) commands(refs []string) []string {
	if len(refs) == 0 {
		return nil
	}
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if cmds, ok := s.scripts[strings.ToLower(ref)]; ok {
			out = append(out, cmds...)
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

// checkScriptValues rejects the ways a scripts map itself can be unusable, at
// whichever level holds it: a nameless entry, a name bound to no command at
// all, and a blank command inside a sequence.
//
// An empty array is the "no command at all" case rather than a way of clearing
// an inherited name — a name that resolves to nothing is what a missing name
// already is, said less clearly — so it reads as the same mistake as `""` and
// gets the same message. A blank command among several is reported by index,
// because "scripts[\"build\"] is empty" would be false of the entry the reader
// then goes and looks at.
func checkScriptValues(label string, scripts map[string]Script) error {
	for name, cmds := range scripts {
		if name == "" {
			return fmt.Errorf("%s: scripts contains an empty script name", label)
		}
		if len(cmds) == 0 {
			return fmt.Errorf("%s: scripts[%q] is empty", label, name)
		}
		for i, cmd := range cmds {
			if strings.TrimSpace(cmd) != "" {
				continue
			}
			if len(cmds) == 1 {
				return fmt.Errorf("%s: scripts[%q] is empty", label, name)
			}
			return fmt.Errorf("%s: scripts[%q][%d] is empty", label, name, i)
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
			return "", "", fmt.Errorf("config: %s: %s: %w", dir, DispatexcludeName, err)
		}
		if len(names) == 0 {
			return "", "", nil
		}
		p := filepath.Join(dir, names[0])
		// One parse answers both questions this file is asked, so a candidate
		// on the way up is read once however far the ascent goes.
		t, readErr := readTree(p)
		if readErr != nil {
			// A file dispat cannot read is broken rather than skippable: Load
			// is where a broken config fails loudly, and stepping over it to
			// use a parent's file would hide the breakage.
			return p, dir, nil
		}
		switch classifyTree(t.root) {
		case configRoot:
			// A candidate below is a space folder's file when this root claims
			// its folder, and a monorepo of its own when it does not.
			if candidate != "" && !ownsSpaceFolder(t.root, dir, candidateRoot) {
				return candidate, candidateRoot, nil
			}
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
)

// classifyTree places a parsed file. A key holding null is not a declaration,
// which is the rule the rest of the configuration follows too: a space map
// spelled out as empty says no more than an absent one.
func classifyTree(root map[string]any) configClass {
	switch {
	case declares(root, "spaces"):
		return configRoot
	case declares(root, "packages"):
		return configPackages
	default:
		return configLoose
	}
}

// declares reports that a file states a key and gives it a value. Keys are
// matched case-insensitively, because the tree is spelled as the file wrote it
// while everything reading it thinks in lowercase.
func declares(root map[string]any, key string) bool {
	v, ok := lookupFold(root, key)
	return ok && v != nil
}

// ownsSpaceFolder reports whether the root config parsed into root, sitting in
// rootDir, declares a space whose folder is dir — which is what tells a space
// folder's config file apart from a monorepo of standalone packages. Folders
// are compared by identity, so a symlinked or case-insensitive path still
// matches itself.
func ownsSpaceFolder(root map[string]any, rootDir, dir string) bool {
	target, err := os.Stat(dir)
	if err != nil {
		return false
	}
	raw, _ := lookupFold(root, "spaces")
	spaces, ok := raw.(map[string]any)
	if !ok {
		return false
	}
	for _, raw := range spaces {
		fields, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		p, ok := lookupString(fields, "path")
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
	t, err := readTree(path)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	v, err := viperFromTree(t, flags)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
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
	// and the shorthand hooks expand {consumer: provider(s)} dependency items
	// and bare record lines into full entries before decoding.
	if err := v.UnmarshalExact(&cfg, weakDecode); err != nil {
		return nil, fmt.Errorf("config: invalid format in %s: %w", path, err)
	}
	// Env keys must keep their exact case; viper lowercased them along with
	// every other map key. This runs before validation so the keys it reports
	// on are the ones the file actually wrote.
	restoreEnvCase(envRestorerOf(t), &cfg)
	cfg.SourceFiles = t.files
	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return &cfg, nil
}

// dependenciesType is the decode target the dependency hook watches for: the
// top-level `dependencies` key.
var dependenciesType = reflect.TypeOf(public.Dependencies(nil))

// providerListType is the same hook's other target: a package's own list.
var providerListType = reflect.TypeOf(public.ProviderList(nil))

// dependencyFormHook expands the `dependencies` key into the flat edge list
// before decoding, whichever of its forms the file used.
//
// The expansion itself lives in pkg/models, so this and the public type's own
// UnmarshalJSON cannot come to disagree about what the config language is.
// The one thing that differs here is the input: viper has already lowercased
// every map key by the time the hook sees it, so consumer names arrive folded
// — exactly like the keys of `packages` and `spaces`, and resolved back onto
// the packages they name in discovery.
func dependencyFormHook(_, to reflect.Type, data any) (any, error) {
	switch to {
	case dependenciesType:
		return public.NormalizeDependencies(data)
	case providerListType:
		return public.NormalizeProviders(data, "dependencies")
	default:
		return data, nil
	}
}

// validateRecords checks the record-line lists of every layer that may carry
// them: the two top-level objects and every package override, in the root
// config and inside each space. In-folder files are checked where they are
// loaded, alongside everything else those files say.
func validateRecords(c *File) error {
	if err := validateRecordObjects("changelog", "github", c.Changelog, c.GitHub); err != nil {
		return err
	}
	for _, name := range sortedKeys(c.Packages) {
		po := c.Packages[name]
		label := fmt.Sprintf("packages[%q]", name)
		if err := validateRecordObjects(label+": changelog", label+": github", po.Changelog, po.GitHub); err != nil {
			return err
		}
	}
	for _, sn := range sortedSpaceNames(c) {
		for _, name := range sortedKeys(c.Spaces[sn].Packages) {
			po := c.Spaces[sn].Packages[name]
			label := fmt.Sprintf("spaces[%q]: packages[%q]", sn, name)
			if err := validateRecordObjects(label+": changelog", label+": github", po.Changelog, po.GitHub); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateRecordObjects checks one layer's changelog and github objects, each
// under the label naming where in the file it sits.
func validateRecordObjects(clLabel, ghLabel string, cl *ChangelogConfig, gh *GitHubConfig) error {
	if cl != nil {
		if err := validateEntryLines(clLabel, "fileTitle", cl.FileTitle); err != nil {
			return err
		}
		// The file title is written once and matched against on the next
		// release, so a title that varies by channel would be prepended again
		// every time the channel moved.
		for i, l := range cl.FileTitle {
			if len(l.Channels) > 0 {
				return fmt.Errorf("%s: fileTitle[%d]: channels is not allowed here: the title is written once "+
					"and matched on the next release, so it must not vary from one release to the next", clLabel, i)
			}
		}
		if err := validateChannels(clLabel, cl.Channels); err != nil {
			return err
		}
		if err := validateEntryFormat(clLabel, cl.EntryFormatConfig); err != nil {
			return err
		}
	}
	if gh != nil {
		if err := validateChannels(ghLabel, gh.Channels); err != nil {
			return err
		}
		if err := validateEntryFormat(ghLabel, gh.EntryFormatConfig); err != nil {
			return err
		}
	}
	return nil
}

// validateChannels refuses a channel restriction naming nothing. Which names
// exist is a property of the repository's tags rather than of the config, so a
// name dispat has never seen is accepted and simply never matches.
func validateChannels(label string, channels []string) error {
	for _, ch := range channels {
		if strings.TrimSpace(ch) == "" {
			return fmt.Errorf("%s: channels must not contain an empty name", label)
		}
	}
	return nil
}

func validateEntryFormat(label string, f EntryFormatConfig) error {
	if err := validateEntryLines(label, "header", f.Header); err != nil {
		return err
	}
	return validateEntryLines(label, "footer", f.Footer)
}

// validateEntryLines refuses a line carrying only filters. Such an entry
// selects packages and then writes nothing to them, which is always a mistake
// rather than a way of writing nothing.
func validateEntryLines(label, key string, lines []EntryLine) error {
	for i, l := range lines {
		if len(l.Line) == 0 {
			return fmt.Errorf("%s: %s[%d]: line is required: an entry with nothing to write writes nothing",
				label, key, i)
		}
		if err := validateChannels(fmt.Sprintf("%s: %s[%d]", label, key, i), l.Channels); err != nil {
			return err
		}
	}
	return nil
}

// entryLinesType is the decode target the record-line hook watches for: the
// fileTitle, header and footer lists.
var entryLinesType = reflect.TypeOf([]public.EntryLine(nil))

// scriptType is the decode target the script hook watches for: one `scripts`
// entry, at any of the four levels that carry the key.
var scriptType = reflect.TypeOf(public.Script(nil))

// scriptFormHook lifts a script written as a scalar into the one-element
// sequence models.NormalizeScript defines it to be.
//
// It exists because WeaklyTypedInput would otherwise get there first, and its
// string-to-slice conversion splits on commas: `echo "a,b"` would decode as
// the two commands `echo "a` and `b"`, each an unbalanced shell fragment. That
// is silent — the file is valid, the commands are wrong — and a comma is
// ordinary in the shell text a script holds (`--output type=local,dest=out`).
//
// Only the scalar form is handled here; a list decodes element by element as
// it always did, and anything else is passed through untouched so mapstructure
// reports it against the key the user actually wrote.
func scriptFormHook(_, to reflect.Type, data any) (any, error) {
	if to != scriptType {
		return data, nil
	}
	if s, ok := data.(string); ok {
		return []string{s}, nil
	}
	return data, nil
}

// entryLinesHook expands the shorthand element shapes of a record-line list
// before decoding. An element is a full object, a bare string, or a bare array
// of strings; the two bare shapes are the common case — text with no filters —
// and become a `line` holding it. A whole list given as a single string is the
// same shorthand one level up ("header": "one line").
//
// Anything else is passed through untouched so that mapstructure reports it
// against the field the user actually wrote, rather than this hook inventing a
// message for a shape it does not understand.
func entryLinesHook(_, to reflect.Type, data any) (any, error) {
	if to != entryLinesType {
		return data, nil
	}
	if s, ok := data.(string); ok {
		return []any{map[string]any{"line": []string{s}}}, nil
	}
	items, ok := data.([]any)
	if !ok {
		return data, nil
	}
	out := make([]any, 0, len(items))
	for _, item := range items {
		if _, isMap := stringKeyMap(item); isMap {
			out = append(out, item)
			continue
		}
		if lines, ok := stringList(item); ok {
			out = append(out, map[string]any{"line": lines})
			continue
		}
		out = append(out, item)
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

// stringList reads the config's recurring "one name or an array of names"
// shape: a record line's text.
func stringList(v any) ([]string, bool) {
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
	if err := validateRecords(c); err != nil {
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
	// The root's own versioning is normalised here rather than only where it
	// lands, so a typo is reported against the key that holds it even in a
	// config whose every space overrides it.
	if c.Versioning != "" {
		mode, ok := normalizeVersioning(c.Versioning)
		if !ok {
			return fmt.Errorf("versioning %q is invalid (want %s)", c.Versioning, quotedNames(versioningNames))
		}
		c.Versioning = mode
	}
	if err := validateAliasTags("aliasTags", c.AliasTags); err != nil {
		return err
	}
	if err := gitx.TagFormat(c.TagFormat).Validate(); err != nil {
		return err
	}
	for _, pattern := range c.Run.AllowBranch {
		if pattern == "" {
			return errors.New("run.allowBranch contains an empty pattern")
		}
	}
	if err := validateEnv("env", c.Env); err != nil {
		return err
	}
	for _, name := range sortedSpaceNames(c) {
		if err := validateEnv(fmt.Sprintf("space %q: env", name), c.Spaces[name].Env); err != nil {
			return err
		}
	}
	for _, name := range sortedSpaceNames(c) {
		stated := c.Spaces[name].Versioning
		validated, err := validateSpace(name, c.Spaces[name])
		if err != nil {
			return err
		}
		if err := validateSpacePackages(fmt.Sprintf("spaces[%q]: packages", name), validated.Packages); err != nil {
			return err
		}
		// Validation normalizes, and normalizing an absent versioning yields
		// the default. Keeping that here would turn "said nothing" into "said
		// independent" before the root defaults are folded in, and the root
		// could never state a mode again. The default belongs at the bottom
		// of the ladder, which is where discovery applies it.
		if stated == "" {
			validated.Versioning = ""
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
	if err := validateObjectDeps("dependencies", c.Dependencies); err != nil {
		return err
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
	if err := validateAliasTags(label+": aliasTags", s.AliasTags); err != nil {
		return s, err
	}
	if err := validateSrc(label, s.Src); err != nil {
		return s, err
	}
	if err := validateWeights(label, s.Concurrency); err != nil {
		return s, err
	}
	if err := validateRecordObjects(label+": changelog", label+": github", s.Changelog, s.GitHub); err != nil {
		return s, err
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
	// Index is the entry's position within the merged list. It is the edit
	// identity — which entry of the loaded config an edit is aimed at — and
	// deliberately not a position anyone can point to in a file.
	Index int
	// Key and KeyIndex are where the entry sits in a consumer-keyed
	// `dependencies` object: the consumer, and the position within that one
	// consumer's providers. Only a label uses them, and only an object list
	// has them — a package's own list is a plain array of provider names.
	Key      string
	KeyIndex int
	// Space names the space whose `dependencies` object holds the entry, and
	// is empty for every other source. It carries the membership rule the
	// space level is held to (see checkSpaceDependencies) and is what tells a
	// space folder file's object apart from a package folder file's list:
	// both are ["dependencies"] in a file of their own, and only the object
	// may be written back keyed by consumer.
	Space string
}

// Label renders the source for error messages and suggestion listings:
// `dependencies["app"][0]`, `packages["core"]: dependencies[0]`,
// `spaces["libs"]: packages["core"]: dependencies[0]`, or
// "packages/core/dispat.json: dependencies[0]".
//
// The root object is labelled by consumer rather than by position, because
// that is what a reader can find in the file: its `dependencies` is keyed by
// consumer, and Index counts the merged list, which the file never spells.
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
	last := s.KeyPath[len(s.KeyPath)-1]
	if s.Key != "" {
		fmt.Fprintf(&b, "%s[%q][%d]", last, s.Key, s.KeyIndex)
	} else {
		fmt.Fprintf(&b, "%s[%d]", last, s.Index)
	}
	return b.String()
}

// IsRootList reports whether the source is the root config's own top-level
// `dependencies` list — the one place compute appends additions to.
func (s DepSource) IsRootList() bool {
	return s.File == "" && len(s.KeyPath) == 1 && s.Space == ""
}

// IsObjectList reports whether the source is a `dependencies` object keyed by
// consumer — the root file's own, or a space's — rather than a package's
// plain list of provider names. It is what an editor has to know to write the
// key back in the shape the loader reads.
func (s DepSource) IsObjectList() bool {
	return s.IsRootList() || s.Space != ""
}

// DeclaredDependency is one dependency edge with its declaration source. A
// package-level declaration is already normalized: the consumer is the
// declaring package and the kind is empty (the default).
type DeclaredDependency struct {
	DependencyConfig
	Source DepSource
}

// validateAliasTags checks one level's alias list on its own: each format has
// to render a name git accepts, and a moving alias has to be allowed to write
// over its own previous ref. Whether an alias collides with a real release tag
// needs the discovered packages and is checked in discovery instead.
func validateAliasTags(label string, aliases []AliasTagConfig) error {
	for i, a := range aliases {
		where := fmt.Sprintf("%s[%d]", label, i)
		if strings.TrimSpace(a.Format) == "" {
			return fmt.Errorf("%s: format is required", where)
		}
		if err := gitx.AliasFormat(a.Format).Validate(); err != nil {
			return fmt.Errorf("%s: %w", where, err)
		}
		// A moving alias exists to be re-pointed, and re-pointing is exactly
		// what force is. Accepting the pair would produce an alias that
		// silently stopped moving after its first release.
		if a.Moving && a.Force != nil && !*a.Force {
			return fmt.Errorf("%s: a moving alias cannot set force: false, since moving it means overwriting it", where)
		}
		for _, ch := range a.Channels {
			if strings.TrimSpace(ch) == "" {
				return fmt.Errorf("%s: channels must not contain an empty name", where)
			}
		}
	}
	return nil
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
// folder excluded by the space's .dispatexclude is not a package at all, a
// package with overrides — a `packages` entry, an in-folder config file,
// or both — gets a derived Space of its own with the merged configuration,
// and a `packages` entry with a `path` becomes a standalone package outside
// every space, validated here because the override layers need the folders
// to exist.
func DiscoverPackages(c *File, root string) ([]*model.Package, []DeclaredDependency, error) {
	d, err := newDiscovery(c, root)
	if err != nil {
		return nil, nil, err
	}
	spaceNames := sortedSpaceNames(c) // deterministic discovery order
	for _, sn := range spaceNames {
		if err := d.scanSpace(sn); err != nil {
			return nil, nil, err
		}
	}
	// The top-level map accounts for its keys against every folder discovered
	// above, which is what leaves the entries with a path as the standalone
	// ones below.
	if err := (keyCheck{
		label:      "packages",
		entries:    c.Packages,
		consumed:   d.consumed,
		ignored:    d.excluded,
		missing:    "matches no package folder (a standalone package needs a path)",
		standalone: true,
	}).run(); err != nil {
		return nil, nil, err
	}
	if err := d.scanStandalone(); err != nil {
		return nil, nil, err
	}
	if err := d.checkAll(spaceNames); err != nil {
		return nil, nil, err
	}
	return d.pkgs, d.declared, nil
}

// checkSpaceDependencies holds every edge declared in a space's own
// `dependencies` object to the rule that makes the level worth having: the
// edge has to touch the space it is written in, as consumer or as provider.
//
// A space is where the edges of its own packages belong, and a cross-space
// edge belongs to whichever of the two spaces its author thinks of as owning
// it. An edge between two packages of neither space is the one thing the
// level cannot express, because a reader looking for it would have no space
// to look in. That one goes in the root object.
//
// An edge naming no package at all is left alone: Discover reports the
// unknown endpoint in its own words, and `dispat compute` loads configs
// Discover would refuse precisely so it can suggest removing them. owner maps
// a package name to its space, empty for a standalone package.
func checkSpaceDependencies(declared []DeclaredDependency, owner map[string]string) error {
	for _, d := range declared {
		space := d.Source.Space
		if space == "" {
			continue
		}
		consumerSpace, consumerKnown := owner[d.Consumer]
		providerSpace, providerKnown := owner[d.Provider]
		if !consumerKnown && !providerKnown {
			continue
		}
		if consumerSpace == space || providerSpace == space {
			continue
		}
		return fmt.Errorf(
			"config: %s: neither consumer %q nor provider %q is a package of space %q; "+
				"an edge a space does not touch belongs in the root dependencies object",
			d.Source.Label(), d.Consumer, d.Provider, space)
	}
	return nil
}

// aliasSamples are the versions every alias is rendered for when checking what
// it can collide with: one stable, one prerelease. Two are enough because an
// alias's shape does not depend on the numbers, only on which placeholders it
// uses and whether the version carries a prerelease.
var aliasSamples = []ccme.Version{
	{Major: 1, Minor: 4, Patch: 2},
	{Major: 1, Minor: 4, Patch: 2, Prerelease: []string{"beta", "4"}},
}

// checkAliasTagsAreWriteOnly refuses a configuration where an alias tag could
// be read back as a release tag, or where two packages would fight over one
// alias name.
//
// This is the rule the whole feature rests on. An alias is written on every
// release and never parsed, so nothing keeps it out of a package's history
// except its name not matching any package's tagFormat. If one did match, the
// baseline query would pick it up, and a moving alias — always the newest tag
// by creation date — would be picked *first*:
//
//   - a bare "v1" does not parse as a version, and an unparseable newest tag
//     makes the whole baseline unreadable, so the package would look unreleased
//     on its very next run;
//   - a bare "v1.4.2" does parse, and would quietly become some package's
//     released version.
//
// Both are silent until a release goes wrong, which is why this is a load-time
// refusal rather than a warning.
func checkAliasTagsAreWriteOnly(pkgs []*model.Package) error {
	type rendered struct {
		tag   string
		owner string
	}
	var all []rendered
	for _, p := range pkgs {
		for _, alias := range p.Space.AliasTags {
			for _, v := range aliasSamples {
				all = append(all, rendered{gitx.AliasFormat(alias.Format).Render(p.Name, v), p.Name})
			}
		}
	}
	if len(all) == 0 {
		return nil
	}
	// Against every package's release format, not just the alias owner's: a
	// tag is read back per package, so an alias of A that reads as a tag of B
	// corrupts B.
	for _, a := range all {
		for _, p := range pkgs {
			format := gitx.TagFormat(p.Space.TagFormat).WithDefault()
			if !format.Matches(p.Name, a.tag) {
				continue
			}
			return fmt.Errorf(
				"config: package %q: alias tag %q would be read back as a release tag of package %q (tagFormat %q); "+
					"an alias must never be readable as a release tag, or it becomes that package's history",
				a.owner, a.tag, p.Name, format)
		}
	}
	// And against each other: two packages writing one name — a fixed group
	// whose members all declare "v{major}" — would force-move it between their
	// commits on every release.
	seen := make(map[string]string, len(all))
	for _, a := range all {
		if prev, taken := seen[a.tag]; taken && prev != a.owner {
			first, second := prev, a.owner
			if second < first {
				first, second = second, first
			}
			return fmt.Errorf(
				"config: packages %q and %q both write the alias tag %q; one name cannot record two packages",
				first, second, a.tag)
		}
		seen[a.tag] = a.owner
	}
	return nil
}

// canonicaliseEndpoints rewrites every declared edge's endpoints to the exact
// name of the package they mean.
//
// The `dependencies` map is keyed by consumer, and viper lowercases the keys
// of every map in the config, so a package folder named "Web" is declared as
// "web" by the time discovery sees it. That is the same fold `packages` and
// `spaces` entries already go through, and it is resolved the same way: keys
// are matched case-insensitively, and a key matching two packages is
// ambiguous rather than arbitrarily assigned.
//
// An endpoint matching no package is left exactly as it was written. Discover
// reports it as unknown, and `dispat compute` — which loads configs Discover
// would refuse — needs the author's own spelling to suggest removing it.
func canonicaliseEndpoints(declared []DeclaredDependency, pkgs []*model.Package) error {
	byFold := make(map[string][]string, len(pkgs))
	for _, p := range pkgs {
		fold := strings.ToLower(p.Name)
		byFold[fold] = append(byFold[fold], p.Name)
	}
	resolve := func(name string, src DepSource) (string, error) {
		matches := byFold[strings.ToLower(name)]
		switch len(matches) {
		case 0:
			return name, nil
		case 1:
			return matches[0], nil
		default:
			sort.Strings(matches)
			return "", fmt.Errorf("config: %s: %q matches packages %s ambiguously (names are matched case-insensitively)",
				src.Label(), name, strings.Join(matches, ", "))
		}
	}
	for i := range declared {
		consumer, err := resolve(declared[i].Consumer, declared[i].Source)
		if err != nil {
			return err
		}
		provider, err := resolve(declared[i].Provider, declared[i].Source)
		if err != nil {
			return err
		}
		declared[i].Consumer, declared[i].Provider = consumer, provider
	}
	return nil
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
//
// dir is the space folder already resolved against the monorepo root: the
// space's own folder for a space, the package's folder for the standalone
// package that is its own space. Both callers have it; neither the config
// nor a member package can be asked for it afterwards.
func buildSpace(c *File, scope scriptScope, label, spaceName, dir string, sc SpaceConfig) (*model.Space, error) {
	mode, group, err := resolveSpaceVersioning(c, spaceName, sc)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	force := c.Commit.ForceEnabled()
	resolvedAliases := make([]model.AliasTag, 0, len(sc.AliasTags))
	for _, a := range sc.AliasTags {
		resolvedAliases = append(resolvedAliases, model.AliasTag{
			Format: a.Format, Moving: a.Moving, Channels: a.Channels,
			Force: a.ForceEnabled(force),
		})
	}
	return &model.Space{
		Name: spaceName,
		Path: sc.Path,
		Dir:  dir,
		// The env layers merge key by key, most local last. sc arrives already
		// carrying the space plus any package override layers — the same
		// invariant packageScope relies on — so only the top level is left to
		// put underneath.
		Env:                  EnvPairs(MergeEnv(c.Env, sc.Env)),
		BuildWaitsPublish:    boolValue(sc.IsBuildWaitingPublish),
		RevertOnFail:         boolValue(sc.RevertOnFail),
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
		TagFormat:            sc.TagFormat,
		AliasTags:            resolvedAliases,
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
