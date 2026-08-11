package config

// Per-package configuration and versioning groups. A space package's
// effective configuration is its space's, overlaid with up to two override
// layers — its top-level `packages` entry, then the package folder's own
// dispat config file — most local winning field by field; a standalone
// package (a `packages` entry with a path) starts from a synthetic base
// instead of a space. The merge produces an ordinary space-shaped config,
// validated with the same helpers as a space, so a package can never express
// something a space cannot.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"

	"github.com/yohimik/dispat/services/dispat/internal/globx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// DispatignoreName is the per-folder ignore file: one pattern per line,
// matched against the names in the folder it sits in. In a space folder the
// patterns mark direct sub-folders that are not packages; in any folder they
// also mark config file names dispat must not pick up, which is how a folder
// holding both dispat.json and dispat.yaml says which one is real. Blank
// lines and #-comments are skipped; "*" in a pattern matches any run of
// characters.
const DispatignoreName = ".dispatignore"

// configCandidates returns the config files present in dir, in the
// defaultFileNames precedence order, minus the names the folder's own
// .dispatignore excludes. It is the single probe ResolveFile, loadSpaceFile
// and loadPackageFile share, so the three can never disagree about which file
// a folder offers.
//
// The ignore file is only read when the folder actually holds a config file:
// a folder with nothing to pick has nothing to exclude, and the ascent walks
// folders that are none of dispat's business.
func configCandidates(dir string) ([]string, error) {
	var present []string
	for _, cand := range defaultFileNames {
		if _, err := os.Stat(filepath.Join(dir, cand)); err == nil {
			present = append(present, cand)
		}
	}
	if len(present) == 0 {
		return nil, nil
	}
	patterns, err := loadIgnore(dir)
	if err != nil {
		return nil, err
	}
	if len(patterns) == 0 {
		return present, nil
	}
	kept := present[:0:0]
	for _, cand := range present {
		if !ignoredName(patterns, cand) {
			kept = append(kept, cand)
		}
	}
	return kept, nil
}

// validateVersionGroups normalizes the declared groups in place and rejects
// the two declaration mistakes: a versioning mode that does not share (a
// group exists to share versions), and a name a space already holds — group
// and space names share one namespace, because a versionGroup reference may
// name either.
func validateVersionGroups(c *File) error {
	for name, g := range c.VersionGroups {
		if name == "" {
			return errors.New("versionGroups: group name must not be empty")
		}
		if _, taken := c.Spaces[name]; taken {
			return fmt.Errorf("versionGroups[%q]: a space has the same name; group and space names share one namespace", name)
		}
		mode, ok := normalizeVersioning(g.Versioning)
		if !ok || !model.Versioning(mode).Shared() {
			return fmt.Errorf("versionGroups[%q]: versioning %q is invalid (a group exists to share versions; want %s)",
				name, g.Versioning, quotedNames(sharedVersioningNames()))
		}
		g.Versioning = mode
		c.VersionGroups[name] = g
	}
	return nil
}

// resolveVersionGroup resolves a versionGroup reference onto the group key
// and versioning mode it stands for: a declared versionGroups entry, or a
// space whose own versioning is shared (its implicit group). The lookup is
// case-insensitive because viper lowercases both maps' keys.
func resolveVersionGroup(c *File, ref string) (key, mode string, err error) {
	low := strings.ToLower(ref)
	if g, ok := c.VersionGroups[low]; ok {
		return low, g.Versioning, nil
	}
	if s, ok := c.Spaces[low]; ok {
		if s.VersionGroup != "" {
			return "", "", fmt.Errorf("versionGroup %q: space %q is itself a member of group %q; name that group directly",
				ref, low, s.VersionGroup)
		}
		if !model.Versioning(s.Versioning).Shared() {
			return "", "", fmt.Errorf("versionGroup %q: space %q does not version as a group (its versioning is %q)",
				ref, low, s.Versioning)
		}
		return low, s.Versioning, nil
	}
	return "", "", fmt.Errorf("versionGroup %q matches no versionGroups entry and no space", ref)
}

// resolveSpaceVersioning returns the effective versioning mode and group key
// of a validated space-shaped config: the referenced group's when one is
// named, the config's own mode under the space's implicit group otherwise.
func resolveSpaceVersioning(c *File, spaceName string, sc SpaceConfig) (mode, group string, err error) {
	if sc.VersionGroup != "" {
		group, mode, err = resolveVersionGroup(c, sc.VersionGroup)
		return mode, group, err
	}
	return sc.Versioning, spaceName, nil
}

// validatePackageEntries checks the top-level packages map on its own: keys
// must be named, and a standalone entry's path must stay inside the
// repository. Whether a key without a path matches a package folder needs
// the folders on disk and is checked in discovery instead.
func validatePackageEntries(c *File) error {
	for name, po := range c.Packages {
		if name == "" {
			return errors.New("packages: package name must not be empty")
		}
		if po.Path == "" {
			continue
		}
		if filepath.IsAbs(po.Path) {
			return fmt.Errorf("packages[%q]: path %q must be a repository-relative path", name, po.Path)
		}
		clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(po.Path)))
		if clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("packages[%q]: path %q escapes the repository root", name, po.Path)
		}
		if clean == "." {
			return fmt.Errorf("packages[%q]: path %q must name a folder inside the repository", name, po.Path)
		}
	}
	return nil
}

// collectPackageDeps appends one package layer's provider names to the
// declared-edge list: the consumer is the package itself and the kind is the
// default. The source's Index is filled per entry.
func collectPackageDeps(declared []DeclaredDependency, pkg string, src DepSource, providers []string) ([]DeclaredDependency, error) {
	for i, p := range providers {
		src.Index = i
		if strings.TrimSpace(p) == "" {
			return nil, fmt.Errorf("%s: provider name must not be empty", src.Label())
		}
		if strings.EqualFold(p, pkg) {
			return nil, fmt.Errorf("%s: package %q cannot depend on itself", src.Label(), pkg)
		}
		declared = append(declared, DeclaredDependency{
			DependencyConfig: DependencyConfig{Consumer: pkg, Provider: p},
			Source:           src,
		})
	}
	return declared, nil
}

// packageEntryKeys are the keys a `packages` entry may never hold. A package
// entry configures one package, so it holds neither spaces nor packages of
// its own; UnmarshalExact would refuse them as unknown keys, and naming them
// here says why and points at the levels that do take them.
var packageEntryKeys = []string{"spaces", "packages"}

// refusePackageEntryKeys walks a raw `packages` map straight from the config
// reader — before decoding, which is the only moment the offending key still
// exists — and rejects an entry holding one of packageEntryKeys. label names
// the map for the error; keys arrive lowercased, like every viper map key.
func refusePackageEntryKeys(label string, entries map[string]any) error {
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names) // one config, one first error
	for _, name := range names {
		fields, ok := entries[name].(map[string]any)
		if !ok {
			continue
		}
		for _, refused := range packageEntryKeys {
			if _, bad := fields[refused]; bad {
				return fmt.Errorf(
					"%s[%q]: %s cannot be set on a package entry: an entry configures one package, so it holds no spaces or packages of its own; declare them in the root config or in a space folder's config file",
					label, name, refused)
			}
		}
	}
	return nil
}

// sortedKeys returns a raw map's keys in order, so a config with several
// mistakes always reports the same one first.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// rawEntries reads a raw map value out of a config reader, at the key path
// given. Anything that is not a map of objects yields nothing: decoding is
// what reports a malformed shape, in its own words.
func rawEntries(v *viper.Viper, keyPath ...string) map[string]any {
	var cur any = v.Get(keyPath[0])
	for _, key := range keyPath[1:] {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[key]
	}
	m, _ := cur.(map[string]any)
	return m
}

// validateSpacePackages checks a space's own `packages` map: every entry is
// held to the same rules as a top-level one, and none of them may carry a
// `path` — a space configures the packages inside its folder, and cannot move
// one elsewhere.
func validateSpacePackages(label string, entries map[string]PackageConfig) error {
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if name == "" {
			return fmt.Errorf("%s: package name must not be empty", label)
		}
		entryLabel := fmt.Sprintf("%s[%q]", label, name)
		if entries[name].Path != "" {
			return fmt.Errorf("%s: %s", entryLabel, pathRefused("a space's packages entry"))
		}
		if err := validatePackageLayer(entryLabel, entries[name]); err != nil {
			return err
		}
	}
	return nil
}

// validatePackageLayer checks what can be wrong with one override layer on
// its own, before it is merged.
func validatePackageLayer(label string, po PackageConfig) error {
	if po.Flow != nil && po.Flow.Login != nil {
		return fmt.Errorf("%s: flow.login cannot be overridden per package: login runs once per space, in the space folder, gating every publish of the space", label)
	}
	if po.Versioning != "" && po.VersionGroup != "" {
		return fmt.Errorf("%s: versioning and versionGroup are mutually exclusive (the group's versioning is authoritative)", label)
	}
	if len(po.Concurrency) > 2 {
		return fmt.Errorf("%s: concurrency accepts at most two values [build, publish], got %v", label, po.Concurrency)
	}
	for _, v := range po.Concurrency {
		if v < 0 {
			return fmt.Errorf("%s: concurrency values must be >= 0, got %v", label, po.Concurrency)
		}
	}
	return nil
}

// mergePackageOverride overlays one override layer onto a space-shaped
// config. Versioning and versionGroup are one axis — a layer setting either
// supersedes both inherited values — flow merges entry by entry, scripts
// name by name, and autoVersion replaces wholesale: its empty fields already
// carry meaning relative to their siblings (no kinds means all four), so an
// overlay could never express them against a non-empty base.
func mergePackageOverride(sc SpaceConfig, po PackageConfig) SpaceConfig {
	if po.IsBuildWaitingPublish != nil {
		sc.IsBuildWaitingPublish = *po.IsBuildWaitingPublish
	}
	if po.RevertOnFail != nil {
		sc.RevertOnFail = *po.RevertOnFail
	}
	if po.TagFormat != "" {
		sc.TagFormat = po.TagFormat
	}
	if po.Versioning != "" {
		sc.Versioning = po.Versioning
		sc.VersionGroup = ""
	}
	if po.VersionGroup != "" {
		sc.VersionGroup = po.VersionGroup
		sc.Versioning = ""
	}
	if po.Flow != nil {
		sc.Flow = mergeFlow(sc.Flow, po.Flow)
	}
	if len(po.Scripts) > 0 {
		merged := make(map[string]string, len(sc.Scripts)+len(po.Scripts))
		for k, v := range sc.Scripts {
			merged[k] = v
		}
		for k, v := range po.Scripts {
			merged[k] = v
		}
		sc.Scripts = merged
	}
	if po.AutoVersion != nil {
		sc.AutoVersion = po.AutoVersion
	}
	return sc
}

// spaceOverride reduces a space folder's file to the space-shaped override it
// is, so the merge a package entry already goes through serves the space
// layer too. Its `packages` map is not part of the merge: it is a layer of
// its own, applied per package.
func spaceOverride(f SpaceFile) PackageConfig {
	return PackageConfig{
		IsBuildWaitingPublish: f.IsBuildWaitingPublish,
		RevertOnFail:          f.RevertOnFail,
		Flow:                  f.Flow,
		TagFormat:             f.TagFormat,
		Versioning:            f.Versioning,
		VersionGroup:          f.VersionGroup,
		Scripts:               f.Scripts,
		AutoVersion:           f.AutoVersion,
	}
}

// overrideLayer is one layer of a package's configuration: the entry itself,
// the label errors about it carry, and where its dependency declarations
// live. The layers are applied in order, nearest the package last.
type overrideLayer struct {
	po    PackageConfig
	label string
	src   DepSource
}

// applyLayers folds every present override layer onto a space-shaped base,
// in order, and returns the merged configuration together with the
// package-only knobs, whether any layer set an autoVersion block, and the
// dependency declarations the layers contribute.
//
// One loop serves both kinds of package: a space package folds its four
// layers onto its space, a standalone package folds its two onto a synthetic
// base. The base's own `packages` map is dropped, because the result
// describes one package and a package holds no packages.
func applyLayers(c *File, base SpaceConfig, pkg string, layers []overrideLayer,
	declared []DeclaredDependency) (SpaceConfig, *packageExtras, bool, []DeclaredDependency, error) {
	ex := &packageExtras{}
	autoVersioned := false
	base.Packages = nil
	for _, l := range layers {
		if err := validatePackageLayer(l.label, l.po); err != nil {
			return base, ex, autoVersioned, declared, err
		}
		base = mergePackageOverride(base, l.po)
		ex.apply(c, l.po)
		autoVersioned = autoVersioned || l.po.AutoVersion != nil
		var err error
		if declared, err = collectPackageDeps(declared, pkg, l.src, l.po.Dependencies); err != nil {
			return base, ex, autoVersioned, declared, err
		}
	}
	return base, ex, autoVersioned, declared, nil
}

// mergeFlow overlays flow entries one by one: a nil entry inherits, a
// non-nil one — the explicit empty array included — replaces, which is how
// an override clears an inherited stage. Login is not merged; validation
// rejects it on the override layer.
func mergeFlow(base, over *SpaceFlowConfig) *SpaceFlowConfig {
	var out SpaceFlowConfig
	// Load fills every space's flow in, but discovery also serves callers that
	// build a config by hand, and an absent object means the same as an empty
	// one.
	if base != nil {
		out = *base
	}
	pick := func(dst *[]string, src []string) {
		if src != nil {
			*dst = src
		}
	}
	pick(&out.Build, over.Build)
	pick(&out.Publish, over.Publish)
	pick(&out.Version, over.Version)
	pick(&out.Announce, over.Announce)
	pick(&out.BeforeAll, over.BeforeAll)
	pick(&out.BeforeVersion, over.BeforeVersion)
	pick(&out.PostVersion, over.PostVersion)
	pick(&out.BeforeBuild, over.BeforeBuild)
	pick(&out.PostBuild, over.PostBuild)
	pick(&out.BeforePublish, over.BeforePublish)
	pick(&out.PostPublish, over.PostPublish)
	pick(&out.BeforeAnnounce, over.BeforeAnnounce)
	pick(&out.PostAnnounce, over.PostAnnounce)
	pick(&out.OnFail, over.OnFail)
	pick(&out.OnSkip, over.OnSkip)
	return &out
}

// packageExtras carries the package-only override knobs — the keys that are
// not space-shaped — across the layers.
type packageExtras struct {
	changelog     *ChangelogConfig
	github        *GitHubConfig
	concurrency   []int
	manifestNames []string
}

// apply folds one layer's package-only keys in. Changelog and github overlay
// the top-level objects field by field (the first layer starts from the
// global config), so a package can flip enabled and keep the global titles,
// or point at another repository and keep the global tokenEnv.
func (ex *packageExtras) apply(c *File, po PackageConfig) {
	if po.Changelog != nil {
		base := c.Changelog
		if base == nil {
			base = &ChangelogConfig{}
		}
		if ex.changelog != nil {
			base = ex.changelog
		}
		ex.changelog = overlayChangelog(base, po.Changelog)
	}
	if po.GitHub != nil {
		base := c.GitHub
		if base == nil {
			base = &GitHubConfig{}
		}
		if ex.github != nil {
			base = ex.github
		}
		ex.github = overlayGitHub(base, po.GitHub)
	}
	if po.Concurrency != nil {
		ex.concurrency = po.Concurrency
	}
	if len(po.ManifestNames) > 0 {
		// A list replaces rather than appends: the layer nearest the package
		// states what the package is called, and adding to an inherited list
		// could never take a name away again.
		ex.manifestNames = po.ManifestNames
	}
}

func overlayFormat(base, over EntryFormatConfig) EntryFormatConfig {
	if over.DateFormat != "" {
		base.DateFormat = over.DateFormat
	}
	if over.BreakingTitle != "" {
		base.BreakingTitle = over.BreakingTitle
	}
	if over.FeaturesTitle != "" {
		base.FeaturesTitle = over.FeaturesTitle
	}
	if over.FixesTitle != "" {
		base.FixesTitle = over.FixesTitle
	}
	if over.DependenciesTitle != "" {
		base.DependenciesTitle = over.DependenciesTitle
	}
	return base
}

func overlayChangelog(base, over *ChangelogConfig) *ChangelogConfig {
	out := *base
	if over.Enabled != nil {
		out.Enabled = over.Enabled
	}
	if over.File != "" {
		out.File = over.File
	}
	if over.Title != "" {
		out.Title = over.Title
	}
	out.EntryFormatConfig = overlayFormat(out.EntryFormatConfig, over.EntryFormatConfig)
	return &out
}

func overlayGitHub(base, over *GitHubConfig) *GitHubConfig {
	out := *base
	if over.Enabled != nil {
		out.Enabled = over.Enabled
	}
	if over.Owner != "" {
		out.Owner = over.Owner
	}
	if over.Repo != "" {
		out.Repo = over.Repo
	}
	if over.APIURL != "" {
		out.APIURL = over.APIURL
	}
	if over.TokenEnv != "" {
		out.TokenEnv = over.TokenEnv
	}
	if over.AllPackages != nil {
		out.AllPackages = over.AllPackages
	}
	out.EntryFormatConfig = overlayFormat(out.EntryFormatConfig, over.EntryFormatConfig)
	return &out
}

// packageWeights resolves a package's concurrency override onto its stage
// weights. The shape was validated with the layer; absence and 0 mean 1,
// the ordinary cost — deliberately unlike the top-level key, where 0 means
// the number of CPUs, because a weight has no CPU reading.
func packageWeights(conc []int) (build, publish int) {
	build, publish = 1, 1
	switch len(conc) {
	case 1:
		build, publish = conc[0], conc[0]
	case 2:
		build, publish = conc[0], conc[1]
	}
	return max(1, build), max(1, publish)
}

// recordFormat maps the config entry-format options onto the resolved model.
func recordFormat(f EntryFormatConfig) model.RecordFormat {
	return model.RecordFormat{
		DateFormat:        f.DateFormat,
		BreakingTitle:     f.BreakingTitle,
		FeaturesTitle:     f.FeaturesTitle,
		FixesTitle:        f.FixesTitle,
		DependenciesTitle: f.DependenciesTitle,
	}
}

// changelogSpec resolves a changelog config onto a package's record policy.
// Nil-safe like IsEnabled: DiscoverPackages accepts configs that skipped
// Load's optional-object filling.
func changelogSpec(cc *ChangelogConfig) model.ChangelogSpec {
	if cc == nil {
		cc = &ChangelogConfig{}
	}
	return model.ChangelogSpec{
		Enabled: cc.IsEnabled(),
		File:    cc.File,
		Title:   cc.Title,
		Format:  recordFormat(cc.EntryFormatConfig),
	}
}

// githubSpec resolves a github config onto a package's record policy.
// Nil-safe, for the same reason as changelogSpec.
func githubSpec(gc *GitHubConfig) model.GitHubSpec {
	if gc == nil {
		gc = &GitHubConfig{}
	}
	return model.GitHubSpec{
		Enabled:     gc.IsEnabled(),
		AllPackages: gc.AllPackagesEnabled(),
		Owner:       gc.Owner,
		Repo:        gc.Repo,
		APIURL:      gc.APIURL,
		TokenEnv:    gc.TokenEnv,
		Format:      recordFormat(gc.EntryFormatConfig),
	}
}

// openFolderConfig probes a folder for its in-folder dispat config file — the
// same names and formats the root config resolves through, minus what the
// folder's .dispatignore excludes — and returns the reader positioned on it.
// An absent file yields an empty path and a nil reader, which every caller
// reads as "this folder says nothing".
func openFolderConfig(dir string) (*viper.Viper, string, error) {
	names, err := configCandidates(dir)
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", DispatignoreName, err)
	}
	if len(names) == 0 {
		return nil, "", nil
	}
	p := filepath.Join(dir, names[0])
	v := viper.New()
	v.SetConfigFile(p)
	if err := v.ReadInConfig(); err != nil {
		return nil, p, fmt.Errorf("cannot read %s: %w", p, err)
	}
	return v, p, nil
}

// weakDecode is the decoding stance every in-folder file shares with the root
// config: unknown keys are rejected, and weak typing lifts a scalar flow
// entry into its slice.
func weakDecode(dc *mapstructure.DecoderConfig) { dc.WeaklyTypedInput = true }

// refuseNestedRoot rejects a folder file declaring one of the keys only a
// monorepo root may declare. The folder holds a repository of its own, and a
// nested root must be ignored rather than half-merged; role and remedy name
// what the folder was being read as and how to take it out of the walk.
func refuseNestedRoot(v *viper.Viper, path, role, remedy string, keys ...string) error {
	for _, key := range keys {
		if v.IsSet(key) {
			return fmt.Errorf(
				"%s declares %s, so the folder looks like a monorepo root of its own rather than a %s; %s, or remove the file",
				path, key, role, remedy)
		}
	}
	return nil
}

// loadPackageFile probes a package folder for its in-folder dispat config
// file and decodes it as a PackageConfig, the file's top-level object.
func loadPackageFile(dir string) (PackageConfig, string, error) {
	var pc PackageConfig
	v, p, err := openFolderConfig(dir)
	if err != nil || v == nil {
		return pc, p, err
	}
	if err := refuseNestedRoot(v, p, "package",
		"exclude the folder with "+DispatignoreName, "spaces", "packages"); err != nil {
		return pc, p, err
	}
	if err := v.UnmarshalExact(&pc, weakDecode); err != nil {
		return pc, p, fmt.Errorf("invalid format in %s: %w", p, err)
	}
	if pc.Path != "" {
		return pc, p, fmt.Errorf(
			"%s: %s", p, pathRefused("a package folder's config file"))
	}
	return pc, p, nil
}

// loadSpaceFile probes a space folder for its in-folder dispat config file
// and decodes it as a SpaceFile, the space's own configuration overriding
// what the root file says about it. `path` has no field to land in — the
// folder the file sits in already is the space's path — so it is reported by
// name rather than as a bare unknown key.
func loadSpaceFile(dir string) (SpaceFile, string, error) {
	var sf SpaceFile
	v, p, err := openFolderConfig(dir)
	if err != nil || v == nil {
		return sf, p, err
	}
	if err := refuseNestedRoot(v, p, "space folder",
		"drop the space from the root config", "spaces"); err != nil {
		return sf, p, err
	}
	if v.IsSet("path") {
		return sf, p, fmt.Errorf("%s: %s", p, pathRefused("a space folder's config file"))
	}
	if err := refusePackageEntryKeys(p+": packages", rawEntries(v, "packages")); err != nil {
		return sf, p, err
	}
	if err := v.UnmarshalExact(&sf, weakDecode); err != nil {
		return sf, p, fmt.Errorf("invalid format in %s: %w", p, err)
	}
	return sf, p, nil
}

// pathRefused is the one sentence every layer that may not set `path` gives,
// so a space entry, a space file and a package file all explain the rule the
// same way.
func pathRefused(where string) string {
	return fmt.Sprintf(
		"path cannot be set in %s: a folder's location is the folder itself, and only a top-level packages entry declares a package elsewhere",
		where)
}

// ignoredDir is a folder a space's .dispatignore kept out of discovery, so an
// entry naming it can be told apart from an entry naming nothing at all.
type ignoredDir struct{ space, name string }

// keyCheck proves that every key of a `packages` map matched exactly one
// folder. An unmatched key is the same class of typo as an unknown dependency
// endpoint; a key matching two folders (names differing only by case) has no
// single package to configure; a key naming an excluded folder gets the
// exclusion spelled out rather than the typo message.
type keyCheck struct {
	label      string                   // the map, as the error names it
	entries    map[string]PackageConfig // the keys to account for
	consumed   map[string][]string      // key -> folders it matched
	ignored    []ignoredDir             // folders excluded by .dispatignore
	missing    string                   // what a key matching nothing means
	standalone bool                     // entries with a path declare one, and match no folder
}

func (k keyCheck) run() error {
	keys := make([]string, 0, len(k.entries))
	for key := range k.entries {
		keys = append(keys, key)
	}
	sort.Strings(keys) // one config, one first error
	for _, key := range keys {
		if k.standalone && k.entries[key].Path != "" {
			continue
		}
		matches := k.consumed[key]
		if len(matches) > 1 {
			sort.Strings(matches)
			return fmt.Errorf("config: %s[%q] matches folders %s ambiguously (keys are matched case-insensitively)",
				k.label, key, strings.Join(matches, ", "))
		}
		if len(matches) == 1 {
			continue
		}
		for _, ig := range k.ignored {
			if strings.EqualFold(ig.name, key) {
				return fmt.Errorf("config: %s[%q]: folder %q in space %q is excluded by %s",
					k.label, key, ig.name, ig.space, DispatignoreName)
			}
		}
		return fmt.Errorf("config: %s[%q] %s", k.label, key, k.missing)
	}
	return nil
}

// loadIgnore reads a folder's .dispatignore patterns; an absent file
// means none.
func loadIgnore(dir string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(dir, DispatignoreName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var patterns []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns, nil
}

// sameDir reports whether two paths name the same folder, by identity rather
// than by string, so a symlinked, relative or differently-cased path still
// matches itself. A path that cannot be stat'ed is nobody's twin.
func sameDir(a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(ai, bi)
}

// ignoredName reports whether a folder name matches one of the space's
// .dispatignore patterns.
func ignoredName(patterns []string, name string) bool {
	for _, p := range patterns {
		if globx.Match(p, name) {
			return true
		}
	}
	return false
}
